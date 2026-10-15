package commands

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

func syncHandler(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/chats/21356/conversations":
		w.Write([]byte(`{"conversations":[{"id":5,"chat_id":21356,"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:00Z","last_message_at":"2026-10-01T10:00:00Z"}],"pagination":{"page":1,"per_page":100,"total_count":1,"total_pages":1,"has_next_page":false}}`))
	case "/api/v1/chats/21356/conversations/5":
		w.Write([]byte(`{"conversation":{"id":5,"chat_id":21356,"created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T10:00:00Z","last_message_at":"2026-10-01T10:00:00Z","messages":[{"id":1,"created_at":"2026-10-01T10:00:00Z","role":"user","message":"hi"}],"messages_pagination":{"page":1,"per_page":200,"total_count":1,"total_pages":1,"has_next_page":false}}}`))
	default:
		http.NotFound(w, r)
	}
}

func TestSyncCommand(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, out, _, err := runZcx(t, syncHandler, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if gjson.Get(out, "fetched").Int() != 1 || gjson.Get(out, "chat_id").String() != "21356" {
		t.Fatalf("summary: %s", out)
	}
}

func TestSyncRefusesOffline(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	if _, _, _, err := runZcx(t, syncHandler, "sync", "--offline"); err == nil {
		t.Fatal("expected refusal")
	}
}
