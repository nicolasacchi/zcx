package commands

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/store"
	"github.com/tidwall/gjson"
)

func readHandler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		switch {
		case r.URL.Path == "/api/v1/chats":
			chatsHandler(21356)(w, r)
		case strings.HasSuffix(r.URL.Path, "/conversations"):
			page := r.URL.Query().Get("page")
			next := page == "" || page == "1"
			id := 2
			if next {
				id = 1
			}
			_ = enc.Encode(map[string]any{"conversations": []map[string]any{{"id": id, "channel": "web", "created_at": "2026-10-01T10:00:00Z"}},
				"pagination": map[string]any{"page": 1, "has_next_page": next}})
		case strings.Contains(r.URL.Path, "/conversations/"):
			_ = enc.Encode(map[string]any{"conversation": map[string]any{"id": 7, "channel": "web", "created_at": "2026-10-01T10:00:00Z",
				"messages":            []map[string]any{{"id": 1, "created_at": "2026-10-01T10:00:00Z", "role": "user", "message": "hello"}},
				"messages_pagination": map[string]any{"page": 1, "has_next_page": false}}})
		case strings.HasSuffix(r.URL.Path, "/users"):
			_ = enc.Encode(map[string]any{"users": []map[string]any{{"id": 1, "name": "Op", "email": "jane.doe@example.com"}}, "pagination": map[string]any{"page": 1}})
		case strings.HasSuffix(r.URL.Path, "/custom_tools"):
			_ = enc.Encode(map[string]any{"custom_tools": []map[string]any{{"id": 3, "name": "t1", "active": true}, {"id": 4, "name": "t2", "active": false}}, "pagination": map[string]any{"page": 1}})
		case strings.HasSuffix(r.URL.Path, "/variables"):
			_ = enc.Encode(map[string]any{"variables": []map[string]any{{"id": 1, "name": "API_KEY", "has_value": true}}, "pagination": map[string]any{"page": 1}})
		case strings.HasSuffix(r.URL.Path, "/prompt"):
			_ = enc.Encode(map[string]any{"prompt": map[string]any{"chat_id": 21356, "master_prompt": "0123456789", "master_prompt_max_length": 100}})
		default:
			http.NotFound(w, r)
		}
	}
}

func TestConversationsListAll(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	cap, out, _, err := runZcx(t, readHandler(t), "conversations", "list", "--all", "--since", "2026-10-01")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "#").Int() != 2 {
		t.Errorf("out %s", out)
	}
	if !strings.Contains(cap.Requests[0].RawQuery, "last_message_at_since=2026-09-30T22") {
		t.Errorf("query %s", cap.Requests[0].RawQuery)
	}
}

func TestConversationsGet(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, out, _, err := runZcx(t, readHandler(t), "conversations", "get", "7", "--json")
	if err != nil || gjson.Get(out, "messages.0.message").String() != "hello" {
		t.Fatalf("%v %s", err, out)
	}
}

func TestUsersMasked(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, out, _, err := runZcx(t, readHandler(t), "users", "list", "--json")
	if err != nil || gjson.Get(out, "0.email").String() != "j***@example.com" {
		t.Fatalf("%v %s", err, out)
	}
	_, out, _, _ = runZcx(t, readHandler(t), "users", "list", "--json", "--show-pii")
	if gjson.Get(out, "0.email").String() != "jane.doe@example.com" {
		t.Errorf("%s", out)
	}
}

func TestToolsAndVariables(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, out, _, err := runZcx(t, readHandler(t), "tools", "list", "--json")
	if err != nil || gjson.Get(out, "#").Int() != 2 {
		t.Fatalf("%v %s", err, out)
	}
	_, out, _, err = runZcx(t, readHandler(t), "tools", "get", "4", "--json")
	if err != nil || gjson.Get(out, "name").String() != "t2" {
		t.Fatalf("%v %s", err, out)
	}
	_, out, _, err = runZcx(t, readHandler(t), "variables", "list", "--json")
	if err != nil || !gjson.Get(out, "0.has_value").Bool() || gjson.Get(out, "0.value").Exists() {
		t.Fatalf("%v %s", err, out)
	}
}

func TestAuditWithoutAndWithCache(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, out, _, err := runZcx(t, readHandler(t), "audit", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "prompt.percent_used").Float() != 10 || gjson.Get(out, "custom_tools.active").Int() != 1 || gjson.Get(out, "cache.available").Bool() {
		t.Errorf("%s", out)
	}

	// seed a cache: runZcx isolates XDG_DATA_HOME, so seed through a handler hook
	rate := "up"
	seed := func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/prompt") {
			s, err := store.OpenAt(os.Getenv("XDG_DATA_HOME")+"/zcx", "21356")
			if err != nil {
				t.Error(err)
			} else {
				now := time.Now()
				_ = s.SaveConversation(&client.Conversation{ID: 1, CreatedAt: now, Lead: &client.Lead{Email: "x@example.com"},
					Messages: []client.Message{{ID: 1, Role: "assistant", Rate: &rate}, {ID: 2, Role: "assistant"}, {ID: 3, Role: "user"}}})
				_ = s.SaveConversation(&client.Conversation{ID: 2, CreatedAt: now})
				_ = s.SaveState(store.State{LastSync: &now})
			}
		}
		readHandler(t)(w, r)
	}
	_, out, _, err = runZcx(t, seed, "audit", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "cache.rating_coverage").Float() != 0.5 || gjson.Get(out, "cache.lead_coverage").Float() != 0.5 || gjson.Get(out, "cache.conversations").Int() != 2 {
		t.Errorf("%s", out)
	}
}
