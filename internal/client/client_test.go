package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func zero() *int { z := 0; return &z }

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(Options{Token: "zcu-test", BaseURL: srv.URL + "/api/v1", MaxRetries: zero()})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestBearerAndListChatsPagination(t *testing.T) {
	var auth string
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/chats" {
			t.Errorf("path %s", r.URL.Path)
		}
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		writeJSON(w, 200, map[string]any{
			"chats":      []map[string]any{{"id": p, "name": fmt.Sprint("c", p), "domain": "d"}},
			"pagination": map[string]any{"page": p, "has_next_page": p < 3},
		})
	})
	chats, err := c.ListChats(context.Background())
	if err != nil || len(chats) != 3 || chats[2].ID != 3 {
		t.Fatalf("%v %+v", err, chats)
	}
	if auth != "Bearer zcu-test" {
		t.Errorf("auth %q", auth)
	}
}

func TestListConversationsParamsAndEach(t *testing.T) {
	var queries []string
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		writeJSON(w, 200, map[string]any{
			"conversations": []map[string]any{
				{"id": p*10 + 1, "created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:00:00.123456Z", "last_message_at": "2026-10-01T10:00:00Z"},
				{"id": p*10 + 2, "created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:00:00Z", "last_message_at": nil},
			},
			"pagination": map[string]any{"page": p, "has_next_page": p < 5},
		})
	})
	since := time.Date(2026, 10, 1, 8, 0, 0, 0, time.FixedZone("x", 7200))
	o := ListConversationsOptions{UpdatedAtSince: since, LastMessageAtSince: since}
	all, err := c.ListAllConversations(context.Background(), "21356", o, 0)
	if err != nil || len(all) != 10 {
		t.Fatalf("%v len=%d", err, len(all))
	}
	if all[0].UpdatedAt.Nanosecond() != 123456000 || all[1].LastMessageAt != nil {
		t.Errorf("time parsing: %+v", all[:2])
	}
	q := queries[0]
	for _, frag := range []string{"updated_at_since=2026-10-01T06%3A00%3A00Z", "last_message_at_since=2026-10-01T06%3A00%3A00Z", "per_page=100"} {
		if !contains(q, frag) {
			t.Errorf("query %q lacks %s", q, frag)
		}
	}
	got, err := c.ListAllConversations(context.Background(), "1", ListConversationsOptions{}, 3)
	if err != nil || len(got) != 3 {
		t.Errorf("limit: %v %d", err, len(got))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestGetConversationAllMessagePages(t *testing.T) {
	var pages atomic.Int32
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		p, _ := strconv.Atoi(r.URL.Query().Get("message_page"))
		if r.URL.Query().Get("message_per_page") != "200" {
			t.Errorf("per page %s", r.URL.RawQuery)
		}
		msgs := []map[string]any{{"id": p*10 + 1, "role": "user", "message": "hi", "created_at": "2026-10-01T10:00:00Z"}}
		if p == 2 { // overlap: id 11 repeated by offset shift
			msgs = append(msgs, map[string]any{"id": 11, "role": "user", "created_at": "2026-10-01T10:00:00Z"})
		}
		writeJSON(w, 200, map[string]any{"conversation": map[string]any{
			"id": 5, "created_at": "2026-10-01T10:00:00Z", "updated_at": "2026-10-01T10:00:00Z",
			"messages": msgs, "messages_pagination": map[string]any{"page": p, "total_pages": 3, "has_next_page": p < 3},
		}})
	})
	cv, err := c.GetConversation(context.Background(), "1", 5)
	if err != nil || pages.Load() != 3 || len(cv.Messages) != 3 {
		t.Fatalf("%v pages=%d msgs=%d", err, pages.Load(), len(cv.Messages))
	}
	if cv.MessagesPagination.HasNextPage {
		t.Error("pagination must be marked complete")
	}
}

func TestPromptRoundTrip(t *testing.T) {
	var gotBody map[string]string
	var method string
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		if r.Method == http.MethodPatch {
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &gotBody)
		}
		writeJSON(w, 200, map[string]any{"prompt": map[string]any{"chat_id": 1, "master_prompt": "new", "master_prompt_max_length": 10000}})
	})
	p, err := c.GetPrompt(context.Background(), "1")
	if err != nil || p.MasterPromptMaxLength != 10000 {
		t.Fatalf("%v %+v", err, p)
	}
	if _, err := c.UpdatePrompt(context.Background(), "1", "new"); err != nil {
		t.Fatal(err)
	}
	if method != http.MethodPatch || gotBody["master_prompt"] != "new" {
		t.Errorf("%s %v", method, gotBody)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		method string
		body   any
		exit   int
		detail string
		hint   string
	}{
		{401, "GET", map[string]string{"error": "Unauthorized"}, 2, "Unauthorized", "doctor"},
		{403, "PATCH", map[string]string{"error": "Forbidden"}, 2, "Forbidden", "read-only"},
		{404, "GET", map[string]string{"error": "Conversation not found"}, 4, "Conversation not found", "not found"},
		{422, "PATCH", map[string]any{"errors": []string{"too long", "blank"}}, 3, "too long; blank", "unprocessable"},
	}
	for _, tc := range cases {
		c := newTest(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.status, tc.body) })
		var err error
		if tc.method == "PATCH" {
			_, err = c.UpdatePrompt(context.Background(), "1", "x")
		} else {
			_, err = c.GetPrompt(context.Background(), "1")
		}
		var ae *APIError
		if !errors.As(err, &ae) {
			t.Fatalf("%d: not APIError: %v", tc.status, err)
		}
		if ae.ExitCode() != tc.exit || ae.Detail != tc.detail || !contains(ae.Hint, tc.hint) {
			t.Errorf("%d: %+v exit=%d", tc.status, ae, ae.ExitCode())
		}
	}
}

func TestRetriesExhausted429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := New(Options{Token: "t", BaseURL: srv.URL, MaxRetries: zero()})
	_, err := c.GetPrompt(context.Background(), "1")
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 429 || ae.ExitCode() != 5 {
		t.Fatalf("%v", err)
	}
}

func TestMissingToken(t *testing.T) {
	c := New(Options{BaseURL: "http://127.0.0.1:1"})
	_, err := c.ListChats(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.ExitCode() != 2 {
		t.Fatalf("%v", err)
	}
}

func TestPatchNotRetriedOnNetworkError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close() // drop the connection mid-request
	}))
	defer srv.Close()
	three := 3
	c := New(Options{Token: "t", BaseURL: srv.URL, MaxRetries: &three})
	if _, err := c.UpdatePrompt(context.Background(), "1", "x"); err == nil {
		t.Fatal("expected network error")
	}
	if hits.Load() != 1 {
		t.Fatalf("PATCH was sent %d times; must never be retried after a network error", hits.Load())
	}
}

func TestRateLimitSleepsWhenLow(t *testing.T) {
	var calls int
	reset := time.Now().Add(30 * time.Second)
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Limit", "120")
		w.Header().Set("X-RateLimit-Remaining", "2")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		writeJSON(w, 200, map[string]any{"chats": []any{}, "pagination": map[string]any{}})
	})
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	if _, err := c.ListChats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 0 {
		t.Fatalf("first request must not sleep: %v", slept)
	}
	rl := c.RateLimit()
	if !rl.Known || rl.Limit != 120 || rl.Remaining != 2 {
		t.Fatalf("%+v", rl)
	}
	if _, err := c.ListChats(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(slept) != 1 || slept[0] < 25*time.Second || slept[0] > 33*time.Second {
		t.Fatalf("second request should wait for the reset: %v", slept)
	}
	if calls != 2 {
		t.Errorf("calls %d", calls)
	}
}

func TestGetCustomToolFallback(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/chats/1/custom_tools" {
			writeJSON(w, 404, map[string]string{"error": "no route"})
			return
		}
		writeJSON(w, 200, map[string]any{"custom_tools": []map[string]any{{"id": 12, "name": "Order lookup", "active": true}},
			"pagination": map[string]any{"has_next_page": false}})
	})
	ct, err := c.GetCustomTool(context.Background(), "1", 12)
	if err != nil || ct.Name != "Order lookup" {
		t.Fatalf("%v %+v", err, ct)
	}
	if _, err := c.GetCustomTool(context.Background(), "1", 99); err == nil {
		t.Fatal("missing tool must 404")
	}
}

func TestUsersAndVariables(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/chats/1/users":
			writeJSON(w, 200, map[string]any{"users": []map[string]any{{"id": 42, "name": "N", "email": "n@example.com", "avatar_url": nil}}, "pagination": map[string]any{}})
		case "/api/v1/chats/1/users/42":
			writeJSON(w, 200, map[string]any{"user": map[string]any{"id": 42, "name": "N"}})
		case "/api/v1/chats/1/variables":
			writeJSON(w, 200, map[string]any{"variables": []map[string]any{{"id": 5, "name": "API_KEY", "has_value": true, "custom_tool_ids": []int{12}}}, "pagination": map[string]any{}})
		}
	})
	us, err := c.ListUsers(context.Background(), "1")
	if err != nil || len(us) != 1 || us[0].Email != "n@example.com" {
		t.Fatalf("%v %+v", err, us)
	}
	if u, err := c.GetUser(context.Background(), "1", 42); err != nil || u.ID != 42 {
		t.Fatalf("%v %+v", err, u)
	}
	vs, err := c.ListVariables(context.Background(), "1")
	if err != nil || !vs[0].HasValue || vs[0].CustomToolIDs[0] != 12 {
		t.Fatalf("%v %+v", err, vs)
	}
}

func TestRetry429WaitsForReset(t *testing.T) {
	var hits atomic.Int32
	reset := time.Now().Add(2 * time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("X-RateLimit-Limit", "120")
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
			w.WriteHeader(429)
			return
		}
		writeJSON(w, 200, map[string]any{"prompt": map[string]any{"master_prompt": "x"}})
	}))
	defer srv.Close()
	c := New(Options{Token: "t", BaseURL: srv.URL})
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	if _, err := c.GetPrompt(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || len(slept) != 1 || slept[0] < time.Second || slept[0] > 4*time.Second {
		t.Fatalf("hits=%d slept=%v", hits.Load(), slept)
	}
}

func TestRetry429BoundedAttempts(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("X-RateLimit-Limit", "120")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(time.Second).Unix(), 10))
		w.WriteHeader(429)
	}))
	defer srv.Close()
	c := New(Options{Token: "t", BaseURL: srv.URL})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	var ae *APIError
	if _, err := c.GetPrompt(context.Background(), "1"); !errors.As(err, &ae) || ae.Status != 429 {
		t.Fatalf("%v", err)
	}
	if hits.Load() != 6 {
		t.Fatalf("hits %d, want 1 + 5 retries", hits.Load())
	}
}

func TestPatchNeverRetriedOn5xxOr429(t *testing.T) {
	for _, status := range []int{502, 429} {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(status)
		}))
		c := New(Options{Token: "t", BaseURL: srv.URL})
		c.sleep = func(context.Context, time.Duration) error { return nil }
		if _, err := c.UpdatePrompt(context.Background(), "1", "x"); err == nil {
			t.Fatal("expected error")
		}
		srv.Close()
		if hits.Load() != 1 {
			t.Fatalf("status %d: PATCH sent %d times", status, hits.Load())
		}
	}
}

func TestGet5xxRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		writeJSON(w, 200, map[string]any{"prompt": map[string]any{"master_prompt": "x"}})
	}))
	defer srv.Close()
	c := New(Options{Token: "t", BaseURL: srv.URL})
	c.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := c.GetPrompt(context.Background(), "1"); err != nil || hits.Load() != 3 {
		t.Fatalf("%v %d", err, hits.Load())
	}
}
