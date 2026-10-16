package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/store"
)

// slowServer serves n conversations (ids 1..n); detail requests take delay
// and are tracked for max in-flight and per-id hit counts.
type slowServer struct {
	n        int
	delay    time.Duration
	failID   int64
	inflight atomic.Int32
	maxSeen  atomic.Int32
	mu       sync.Mutex
	hits     map[int64]int
}

func (s *slowServer) handler(w http.ResponseWriter, r *http.Request) {
	conv := func(id int64, detail bool) map[string]any {
		m := map[string]any{"id": id, "chat_id": 7, "created_at": "2026-10-01T10:00:00Z",
			"updated_at": fmt.Sprintf("2026-10-%02dT10:00:00Z", 1+id%20), "last_message_at": "2026-10-01T10:00:00Z"}
		if detail {
			m["messages"] = []map[string]any{{"id": 1, "created_at": "2026-10-01T10:00:00Z", "role": "user", "message": "x"}}
			m["messages_pagination"] = map[string]any{"page": 1, "per_page": 200, "total_count": 1, "total_pages": 1, "has_next_page": false}
		}
		return m
	}
	p := strings.TrimPrefix(r.URL.Path, "/api/v1/chats/7/conversations")
	if p == "" {
		var items []map[string]any
		for i := 1; i <= s.n; i++ {
			items = append(items, conv(int64(i), false))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"conversations": items,
			"pagination": map[string]any{"page": 1, "per_page": 100, "total_count": s.n, "total_pages": 1, "has_next_page": false}})
		return
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(p, "/"), 10, 64)
	cur := s.inflight.Add(1)
	for {
		m := s.maxSeen.Load()
		if cur <= m || s.maxSeen.CompareAndSwap(m, cur) {
			break
		}
	}
	time.Sleep(s.delay)
	s.inflight.Add(-1)
	s.mu.Lock()
	s.hits[id]++
	s.mu.Unlock()
	if id == s.failID {
		w.WriteHeader(500)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"conversation": conv(id, true)})
}

func slowSetup(t *testing.T, s *slowServer) (*client.Client, *store.Store) {
	t.Helper()
	s.hits = map[int64]int{}
	srv := httptest.NewServer(http.HandlerFunc(s.handler))
	t.Cleanup(srv.Close)
	zero := 0
	c := client.New(client.Options{Token: "t", BaseURL: srv.URL + "/api/v1", MaxRetries: &zero})
	st, err := store.OpenAt(t.TempDir(), "7")
	if err != nil {
		t.Fatal(err)
	}
	return c, st
}

func TestConcurrencyBoundedAndEachSavedOnce(t *testing.T) {
	for _, conc := range []int{1, 3, 4, 100} {
		s := &slowServer{n: 40, delay: 5 * time.Millisecond}
		c, st := slowSetup(t, s)
		var lines []string
		var lmu sync.Mutex
		sum, err := Run(context.Background(), c, st, Options{Concurrency: conc, Progress: func(f string, a ...any) {
			lmu.Lock()
			lines = append(lines, fmt.Sprintf(f, a...))
			lmu.Unlock()
		}})
		if err != nil || sum.Listed != 40 || sum.Fetched != 40 || sum.Errors != 0 {
			t.Fatalf("conc %d: %+v %v", conc, sum, err)
		}
		want := ClampConcurrency(conc)
		if got := int(s.maxSeen.Load()); got > want {
			t.Errorf("conc %d: max in flight %d > %d", conc, got, want)
		}
		if conc >= 3 && s.maxSeen.Load() < 2 {
			t.Errorf("conc %d: no parallelism observed", conc)
		}
		for id := int64(1); id <= 40; id++ {
			if s.hits[id] != 1 {
				t.Errorf("conc %d: id %d fetched %d times", conc, id, s.hits[id])
			}
			if _, err := st.LoadConversation(id); err != nil {
				t.Errorf("conc %d: id %d not saved: %v", conc, id, err)
			}
		}
		synced := 0
		for _, l := range lines {
			if strings.HasPrefix(l, "synced ") {
				synced++
			}
		}
		if synced != 1 || !strings.HasPrefix(lines[len(lines)-1], "done: listed 40, fetched 40") {
			t.Errorf("conc %d: progress lines %q", conc, lines)
		}
	}
}

func TestClampConcurrency(t *testing.T) {
	for in, want := range map[int]int{0: 4, -3: 1, 1: 1, 8: 8, 9: 8} {
		if got := ClampConcurrency(in); got != want {
			t.Errorf("%d -> %d, want %d", in, got, want)
		}
	}
}

func TestFailureKeepsMarksAndReportsLowestID(t *testing.T) {
	s := &slowServer{n: 20, delay: time.Millisecond, failID: 7}
	c, st := slowSetup(t, s)
	sum, err := Run(context.Background(), c, st, Options{Concurrency: 4})
	if err == nil || sum.Errors != 1 || sum.Fetched != 19 || !strings.Contains(err.Error(), "conversation 7") {
		t.Fatalf("%+v %v", sum, err)
	}
	state, _ := st.State()
	if state.UpdatedAtHighWater != nil || state.LastSync != nil {
		t.Fatalf("marks advanced: %+v", state)
	}
}

func TestCancellationReturnsPromptly(t *testing.T) {
	s := &slowServer{n: 200, delay: 50 * time.Millisecond}
	c, st := slowSetup(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	sum, err := Run(ctx, c, st, Options{Concurrency: 4})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	if sum.Fetched >= 200 {
		t.Fatalf("not cancelled: %+v", sum)
	}
	if state, _ := st.State(); state.UpdatedAtHighWater != nil {
		t.Fatal("marks advanced after cancellation")
	}
}
