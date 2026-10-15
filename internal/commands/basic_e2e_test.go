package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func chatsHandler(ids ...int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "120")
		w.Header().Set("X-RateLimit-Remaining", "119")
		w.Header().Set("X-RateLimit-Reset", "2000000000")
		chats := []map[string]any{}
		for _, id := range ids {
			chats = append(chats, map[string]any{"id": id, "name": "Synthetic Store", "domain": "shop.example", "created_at": "2026-01-15T10:30:00Z"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"chats": chats, "pagination": map[string]any{"page": 1, "has_next_page": false}})
	}
}

func TestVersion(t *testing.T) {
	_, out, _, err := runZcx(t, nil, "version", "--json")
	if err != nil || gjson.Get(out, "version").String() == "" {
		t.Fatalf("%v %s", err, out)
	}
}

func TestChatsList(t *testing.T) {
	cap, out, _, err := runZcx(t, chatsHandler(21356), "chats", "list")
	if err != nil {
		t.Fatal(err)
	}
	if got := cap.Paths(); len(got) != 1 || got[0] != "GET /api/v1/chats" {
		t.Errorf("paths %v", got)
	}
	if cap.Requests[0].Auth != "Bearer zcu-test-token" {
		t.Errorf("auth %q", cap.Requests[0].Auth)
	}
	if gjson.Get(out, "0.id").Int() != 21356 || gjson.Get(out, "0.domain").String() != "shop.example" {
		t.Errorf("out %s", out)
	}
}

func TestChatsListJQ(t *testing.T) {
	_, out, _, err := runZcx(t, chatsHandler(1, 2), "chats", "list", "--jq", "#.id")
	if err != nil || strings.Join(strings.Fields(out), "") != "[1,2]" {
		t.Fatalf("%v %q", err, out)
	}
}

func TestChatsListUnauthorizedExitCode(t *testing.T) {
	_, _, _, err := runZcx(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
	}, "chats", "list")
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Fatalf("want exit 2, got %v", err)
	}
}

func TestGetEnvMissingTokenAndOffline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ZIPCHAT_API_TOKEN", "")
	t.Setenv("ZCX_CHAT_ID", "")
	resetFlags(Root())
	_, err := getEnv(false, false)
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) || coder.ExitCode() != 2 {
		t.Fatalf("missing token must be exit 2: %v", err)
	}
	// Offline needs no token; chat id comes from the only cached chat dir.
	if _, err := getEnv(true, true); err == nil {
		t.Fatal("offline without any chat id or cache must fail")
	}
	dd := os.Getenv("XDG_DATA_HOME")
	if err := os.MkdirAll(filepath.Join(dd, "zcx", "21356"), 0o700); err != nil {
		t.Fatal(err)
	}
	e, err := getEnv(true, true)
	if err != nil || e.ChatID != "21356" || e.Client != nil {
		t.Fatalf("%v %+v", err, e)
	}
}

func TestConfigShowMasksToken(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, out, _, err := runZcx(t, nil, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "zcu-test-token") {
		t.Fatal("token leaked in config show")
	}
	if gjson.Get(out, "token_source").String() != "env:ZIPCHAT_API_TOKEN" || gjson.Get(out, "chat_id").String() != "21356" {
		t.Errorf("%s", out)
	}
	if gjson.Get(out, "hours.timezone").String() != "Europe/Rome" || gjson.Get(out, "hours.schedule.mon.0").String() != "09:00-19:00" {
		t.Errorf("hours: %s", out)
	}
}

func TestDoctorOKAutoDetectsChat(t *testing.T) {
	cap, out, _, err := runZcx(t, chatsHandler(21356), "config", "doctor")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(cap.Requests) != 1 {
		t.Errorf("doctor should make exactly one request, got %v", cap.Paths())
	}
	if !gjson.Get(out, "ok").Bool() || gjson.Get(out, "chat.id").String() != "21356" ||
		gjson.Get(out, "chat.source").String() != "auto-detected" ||
		gjson.Get(out, "rate_limit.remaining").Int() != 119 || !gjson.Get(out, "rate_limit.headers_seen").Bool() {
		t.Errorf("%s", out)
	}
	if strings.Contains(out, "zcu-test-token") {
		t.Fatal("token leaked in doctor")
	}
}

func TestDoctorFailsOnAuthAndReportsLoosePerms(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "5")
	_, out, _, err := runZcx(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":"Unauthorized"}`))
	}, "config", "doctor")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Fatalf("want ExitError 2, got %v", err)
	}
	if gjson.Get(out, "ok").Bool() || gjson.Get(out, "api.ok").Bool() {
		t.Errorf("%s", out)
	}

	// Loose permissions in the data dir are flagged.
	dd := t.TempDir()
	loose := filepath.Join(dd, "zcx")
	if e := os.MkdirAll(loose, 0o755); e != nil {
		t.Fatal(e)
	}
	if bad := dirModeProblems(dd); len(bad) == 0 {
		t.Error("0755 dir must be reported")
	}
}

func TestChatAutoDetectAmbiguous(t *testing.T) {
	_, _, _, err := runZcx(t, chatsHandler(1, 2), "config", "doctor")
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("doctor with 2 chats and no chat id must fail: %v", err)
	}
}
