package commands

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/tidwall/gjson"
)

const exportConvJSON = `{"id":1,"chat_id":21356,"created_at":"2026-10-14T09:00:00Z","updated_at":"2026-10-14T10:00:00Z","last_message_at":"2026-10-14T10:00:00Z","last_escalated_at":"2026-10-14T10:00:00Z","start_link":"https://example.test/p/1","share_url":"https://example.test/s/1","messages_pagination":{"page":1,"per_page":200,"total_count":3,"total_pages":1,"has_next_page":false},"messages":[
{"id":1,"role":"user","message":"mail a test@example.com ordine F123456789","created_at":"2026-10-14T09:00:00Z"},
{"id":2,"role":"assistant","message":"<p>Ciao</p>","created_at":"2026-10-14T09:00:05Z"},
{"id":3,"role":"assistant","manual_reply":true,"message":"Salve","created_at":"2026-10-14T10:00:00Z"}]}`

func TestExportTranscripts(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	h := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/conversations"):
			_, _ = w.Write([]byte(`{"conversations":[` + exportConvJSON + `],"pagination":{"page":1,"per_page":100,"total_count":1,"total_pages":1,"has_next_page":false}}`))
		case strings.HasSuffix(r.URL.Path, "/conversations/1"):
			_, _ = w.Write([]byte(`{"conversation":` + exportConvJSON + `}`))
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}
	_, so, se, err := runZcx(t, h, "export", "transcripts", "--format", "jsonl", "--mask-orders", "--quiet")
	if err != nil {
		t.Fatalf("%v %s", err, se)
	}
	if strings.Contains(so, "test@example.com") || !strings.Contains(so, "[EMAIL]") || !strings.Contains(so, "[ORDER]") {
		t.Errorf("not redacted: %s", so)
	}
	if gjson.Get(so, "messages.1.role").String() != "BOT" || gjson.Get(so, "messages.2.role").String() != "OPERATOR" {
		t.Errorf("roles: %s", so)
	}
	if gjson.Get(so, "messages.1.text").String() != "Ciao" {
		t.Errorf("html not stripped: %s", so)
	}

	_, so, se, err = runZcx(t, h, "export", "transcripts", "--raw", "--quiet")
	if err != nil || !strings.Contains(so, "test@example.com") || !strings.Contains(se, "WARNING") {
		t.Errorf("raw: err=%v out=%s err=%s", err, so, se)
	}
}

func TestExportBuild(t *testing.T) {
	c := client.Conversation{ID: 7, CreatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC),
		Messages: []client.Message{
			{Role: "user", Message: "tel 333 123 4567"},
			{Role: "assistant", Message: "<b>ok</b>"},
			{Role: "assistant", ManualReply: true, Message: "x"},
		}}
	got := exportBuild(c, false, false)
	if got.Messages[0].Text != "tel [PHONE]" || got.Messages[1].Text != "ok" || got.Messages[2].Role != "OPERATOR" {
		t.Errorf("%+v", got.Messages)
	}
	if got.CreatedAt != "2026-10-14 11:00:00" {
		t.Errorf("rome time: %s", got.CreatedAt)
	}
	raw := exportBuild(c, true, false)
	if raw.Messages[0].Text != "tel 333 123 4567" {
		t.Errorf("raw redacted: %s", raw.Messages[0].Text)
	}
	var sb strings.Builder
	if err := exportWriteText(&sb, got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "CUSTOMER: tel [PHONE]") {
		t.Error(sb.String())
	}
}

func TestExportStartLinkRedactedAndOutFileMode(t *testing.T) {
	c := client.Conversation{ID: 7, CreatedAt: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
	link := "https://www.example.test/p/1?email=mario%40example.com&tel=3331234567#x"
	c.StartLink = &link
	if got := exportBuild(c, false, false).StartPage; got != "https://www.example.test/p/1" {
		t.Errorf("redacted start page %q", got)
	}
	if got := exportBuild(c, true, false).StartPage; got != link {
		t.Errorf("raw start page %q", got)
	}

	t.Setenv("ZCX_CHAT_ID", "21356")
	h := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/conversations"):
			_, _ = w.Write([]byte(`{"conversations":[` + exportConvJSON + `],"pagination":{"page":1,"per_page":100,"total_count":1,"total_pages":1,"has_next_page":false}}`))
		case strings.HasSuffix(r.URL.Path, "/conversations/1"):
			_, _ = w.Write([]byte(`{"conversation":` + exportConvJSON + `}`))
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}
	out := filepath.Join(t.TempDir(), "t.txt")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, se, err := runZcx(t, h, "export", "transcripts", "--out", out, "--quiet"); err != nil {
		t.Fatalf("%v %s", err, se)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
}
