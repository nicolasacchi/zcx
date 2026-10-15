package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/store"
)

type fakeConv struct {
	ID       int64
	Updated  string
	LastMsg  string
	Messages int
}

type fake struct {
	mu       sync.Mutex
	convs    map[int64]*fakeConv
	details  map[int64]int
	listQ    []string
	fail429  map[int64]bool
	detailsN int
}

func (f *fake) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/chats/7/conversations")
	if p == "" {
		f.listQ = append(f.listQ, r.URL.RawQuery)
		var items []map[string]any
		for _, c := range f.convs {
			items = append(items, f.item(c, false, 0, 0))
		}
		json.NewEncoder(w).Encode(map[string]any{"conversations": items,
			"pagination": map[string]any{"page": 1, "per_page": 100, "total_count": len(items), "total_pages": 1, "has_next_page": false}})
		return
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(p, "/"), 10, 64)
	c := f.convs[id]
	if f.fail429[id] {
		w.WriteHeader(429)
		return
	}
	f.details[id]++
	page, _ := strconv.Atoi(r.URL.Query().Get("message_page"))
	if page == 0 {
		page = 1
	}
	per, _ := strconv.Atoi(r.URL.Query().Get("message_per_page"))
	if per == 0 {
		per = 200
	}
	json.NewEncoder(w).Encode(map[string]any{"conversation": f.item(c, true, page, per)})
}

func (f *fake) item(c *fakeConv, detail bool, page, per int) map[string]any {
	m := map[string]any{"id": c.ID, "chat_id": 7, "created_at": "2026-10-01T10:00:00Z",
		"updated_at": c.Updated, "last_message_at": c.LastMsg, "share_url": "https://x/" + fmt.Sprint(c.ID)}
	if detail {
		total := c.Messages
		pages := (total + per - 1) / per
		var msgs []map[string]any
		for i := (page - 1) * per; i < total && i < page*per; i++ {
			msgs = append(msgs, map[string]any{"id": int64(i + 1), "created_at": "2026-10-01T10:00:00Z", "role": "user", "message": "synthetic"})
		}
		m["messages"] = msgs
		m["messages_pagination"] = map[string]any{"page": page, "per_page": per, "total_count": total, "total_pages": pages, "has_next_page": page < pages}
	}
	return m
}

func setup(t *testing.T, f *fake) (*client.Client, *store.Store) {
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	zero := 0
	c := client.New(client.Options{Token: "zcu-test", BaseURL: srv.URL + "/api/v1", MaxRetries: &zero})
	st, err := store.OpenAt(t.TempDir(), "7")
	if err != nil {
		t.Fatal(err)
	}
	return c, st
}

func newFake() *fake {
	return &fake{convs: map[int64]*fakeConv{}, details: map[int64]int{}, fail429: map[int64]bool{}}
}

func TestSyncFlow(t *testing.T) {
	f := newFake()
	f.convs[1] = &fakeConv{1, "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z", 3}
	f.convs[2] = &fakeConv{2, "2026-10-02T10:00:00Z", "2026-10-02T10:00:00Z", 450} // 3 message pages
	c, st := setup(t, f)
	ctx := context.Background()

	s, err := Run(ctx, c, st, Options{})
	if err != nil || s.Fetched != 2 || s.Skipped != 0 {
		t.Fatalf("first run: %+v %v", s, err)
	}
	got, _ := st.LoadConversation(2)
	if len(got.Messages) != 450 {
		t.Fatalf("pagination: %d messages", len(got.Messages))
	}
	state, _ := st.State()
	if state.UpdatedAtHighWater == nil || !state.UpdatedAtHighWater.Equal(time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("high water %v", state.UpdatedAtHighWater)
	}

	// unchanged: skipped, listing carries since minus 5 minutes
	f.listQ = nil
	s, err = Run(ctx, c, st, Options{})
	if err != nil || s.Fetched != 0 || s.Skipped != 2 {
		t.Fatalf("second run: %+v %v", s, err)
	}
	if len(f.listQ) != 2 || !strings.Contains(f.listQ[0], "updated_at_since=2026-10-02T09%3A55") || !strings.Contains(f.listQ[1], "last_message_at_since=") {
		t.Fatalf("list queries: %v", f.listQ)
	}

	// updated + new
	f.convs[1].Updated, f.convs[1].LastMsg, f.convs[1].Messages = "2026-10-03T10:00:00Z", "2026-10-03T10:00:00Z", 4
	f.convs[3] = &fakeConv{3, "2026-10-03T11:00:00Z", "2026-10-03T11:00:00Z", 1}
	f.details = map[int64]int{}
	s, err = Run(ctx, c, st, Options{})
	if err != nil || s.Fetched != 2 || s.Skipped != 1 || f.details[2] != 0 {
		t.Fatalf("third run: %+v %v %v", s, err, f.details)
	}
	if c1, _ := st.LoadConversation(1); len(c1.Messages) != 4 {
		t.Fatal("conversation 1 not refreshed")
	}

	// full refetches all
	s, err = Run(ctx, c, st, Options{Full: true})
	if err != nil || s.Fetched != 3 || s.Skipped != 0 {
		t.Fatalf("full: %+v %v", s, err)
	}
}

func TestCrashDoesNotAdvanceMarks(t *testing.T) {
	f := newFake()
	f.convs[1] = &fakeConv{1, "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z", 1}
	c, st := setup(t, f)
	if _, err := Run(context.Background(), c, st, Options{}); err != nil {
		t.Fatal(err)
	}
	before, _ := st.State()

	f.convs[1].Updated, f.convs[1].LastMsg = "2026-10-05T10:00:00Z", "2026-10-05T10:00:00Z"
	f.convs[2] = &fakeConv{2, "2026-10-06T10:00:00Z", "2026-10-06T10:00:00Z", 1}
	f.fail429[2] = true // 429 on detail, no retries configured
	s, err := Run(context.Background(), c, st, Options{})
	if err == nil || s.Errors != 1 {
		t.Fatalf("expected error: %+v %v", s, err)
	}
	after, _ := st.State()
	if !after.UpdatedAtHighWater.Equal(*before.UpdatedAtHighWater) {
		t.Fatalf("marks advanced after failure: %v -> %v", before.UpdatedAtHighWater, after.UpdatedAtHighWater)
	}

	// recovery: next run picks both up again
	f.fail429[2] = false
	s, err = Run(context.Background(), c, st, Options{})
	if err != nil || s.Fetched != 1 || s.Skipped != 1 { // conv 1 was saved before the failure
		t.Fatalf("recovery: %+v %v", s, err)
	}
	after, _ = st.State()
	if !after.UpdatedAtHighWater.Equal(time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("mark not advanced: %v", after.UpdatedAtHighWater)
	}
}

func TestSyncRelistsWhenCacheLostButStateKept(t *testing.T) {
	f := newFake()
	f.convs[1] = &fakeConv{1, "2026-10-01T10:00:00Z", "2026-10-01T10:00:00Z", 3}
	f.convs[2] = &fakeConv{2, "2026-10-02T10:00:00Z", "2026-10-02T10:00:00Z", 2}
	c, st := setup(t, f)
	ctx := context.Background()
	if _, err := Run(ctx, c, st, Options{}); err != nil {
		t.Fatal(err)
	}
	// manual cleanup: conversations/ deleted, state.json kept
	if err := os.RemoveAll(st.Dir + "/conversations"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.Dir+"/conversations", 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Run(ctx, c, st, Options{})
	if err != nil || !s.CacheRebuilt || s.Fetched != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	// partially lost cache is detected too
	ents, _ := os.ReadDir(st.Dir + "/conversations")
	_ = os.Remove(st.Dir + "/conversations/" + ents[0].Name())
	s, err = Run(ctx, c, st, Options{})
	if err != nil || !s.CacheRebuilt || s.Fetched != 1 || s.Skipped != 1 {
		t.Fatalf("%+v %v", s, err)
	}
	// healthy cache: incremental again
	s, _ = Run(ctx, c, st, Options{})
	if s.CacheRebuilt {
		t.Fatalf("healthy cache flagged: %+v", s)
	}
}
