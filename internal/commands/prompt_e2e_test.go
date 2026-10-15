package commands

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nicolasacchi/zcx/internal/client"
	"github.com/nicolasacchi/zcx/internal/store"
	"github.com/tidwall/gjson"
)

// fakePrompt is a stateful fake of GET/PATCH /chats/21356/prompt.
type fakePrompt struct {
	text     string
	max      int
	patchTo  int  // non-zero: answer PATCH with this status
	drop     bool // PATCH: close the connection without answering
	ignore   bool // PATCH: answer 200 but do not store
	patchLog []string
}

func (f *fakePrompt) handler(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/chats/21356/prompt") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"prompt": map[string]any{"chat_id": 21356, "master_prompt": f.text, "master_prompt_max_length": f.max}})
	case http.MethodPatch:
		var b struct {
			MasterPrompt string `json:"master_prompt"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.patchLog = append(f.patchLog, b.MasterPrompt)
		if f.drop {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		if f.patchTo != 0 {
			w.WriteHeader(f.patchTo)
			_, _ = w.Write([]byte(`{"error":"nope"}`))
			return
		}
		if !f.ignore {
			f.text = strings.TrimSpace(b.MasterPrompt)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"prompt": map[string]any{"chat_id": 21356, "master_prompt": f.text, "master_prompt_max_length": f.max}})
	}
}

func patches(c *Captured) (n int) {
	for _, r := range c.Requests {
		if r.Method == http.MethodPatch {
			n++
		}
	}
	return
}

func exitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	var c interface{ ExitCode() int }
	if errors.As(err, &c) {
		return c.ExitCode()
	}
	if err != nil {
		return 1
	}
	return 0
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "p.txt")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const livePrompt = "You are a helpful synthetic assistant.\nBe brief."

var liveSHA = store.SHA256(livePrompt)

func TestPromptPushHappyPath(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: livePrompt, max: 1000}
	file := writeTemp(t, "\n"+livePrompt+"\nAlways greet.\n\n")
	cap, out, _, err := runZcx(t, f.handler, "prompt", "push", file, "--base-sha", liveSHA, "--yes")
	if err != nil {
		t.Fatal(err, out)
	}
	if patches(cap) != 1 || len(f.patchLog) != 1 {
		t.Fatalf("patches = %d", patches(cap))
	}
	if f.patchLog[0] != livePrompt+"\nAlways greet." {
		t.Fatalf("PATCH body not trimmed: %q", f.patchLog[0])
	}
	// order: GET before PATCH, GET again after.
	want := []string{"GET /api/v1/chats/21356/prompt", "PATCH /api/v1/chats/21356/prompt", "GET /api/v1/chats/21356/prompt"}
	got := cap.Paths()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sequence %v", got)
	}
	if cap.Requests[1].Auth != "Bearer zcu-test-token" {
		t.Fatal("auth")
	}
	// backup of the OLD prompt exists in the store.
	dir := filepath.Join(os.Getenv("XDG_DATA_HOME"), "zcx", "21356", "prompts")
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("backups: %d", len(ents))
	}
	b, _ := os.ReadFile(filepath.Join(dir, ents[0].Name()))
	if string(b) != livePrompt {
		t.Fatalf("backup content %q", b)
	}
}

func TestPromptPushRefusalsSendNoPatch(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	cases := []struct {
		name    string
		file    string
		args    []string
		max     int
		code    int
		errPart string
	}{
		{"blank file", "  \n ", []string{"--base-sha", liveSHA, "--yes"}, 1000, 3, "blank"},
		{"base mismatch", livePrompt + "x", []string{"--base-sha", strings.Repeat("a", 64), "--yes"}, 1000, 6, "changed since you pulled"},
		{"missing base sha", livePrompt + "x", []string{"--yes"}, 1000, 6, "--base-sha is required"},
		{"too long", livePrompt + strings.Repeat("y", 50), []string{"--base-sha", liveSHA, "--yes"}, 60, 3, "maximum is 60"},
		{"missing yes", livePrompt + "x", []string{"--base-sha", liveSHA}, 1000, 6, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakePrompt{text: livePrompt, max: c.max}
			cap, _, stderr, err := runZcx(t, f.handler, append([]string{"prompt", "push", writeTemp(t, c.file)}, c.args...)...)
			if err == nil {
				t.Fatal("expected error")
			}
			if exitCode(err) != c.code {
				t.Fatalf("exit %d want %d (%v)", exitCode(err), c.code, err)
			}
			if !strings.Contains(err.Error()+stderr, c.errPart) {
				t.Fatalf("message %q lacks %q", err.Error(), c.errPart)
			}
			if patches(cap) != 0 {
				t.Fatal("PATCH sent in a refusal path")
			}
			if f.text != livePrompt {
				t.Fatal("live changed")
			}
		})
	}
}

func TestPromptPushIdenticalIsNoop(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: livePrompt, max: 1000}
	cap, out, _, err := runZcx(t, f.handler, "prompt", "push", writeTemp(t, livePrompt+"\n"), "--base-sha", liveSHA, "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if patches(cap) != 0 || gjson.Get(out, "changed").Bool() || !gjson.Get(out, "ok").Bool() {
		t.Fatalf("patches=%d out=%q", patches(cap), out)
	}
}

func TestPromptPushDryRun(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: livePrompt, max: 1000}
	cap, out, errOut, err := runZcx(t, f.handler, "prompt", "push", writeTemp(t, livePrompt+"\nmore"), "--base-sha", liveSHA, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if patches(cap) != 0 {
		t.Fatal("PATCH in dry-run")
	}
	if !strings.Contains(out, "--dry-run: would") || !strings.Contains(errOut, "+more") {
		t.Fatalf("out %q", out)
	}
	// no backup written either.
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_DATA_HOME"), "zcx", "21356", "prompts")); err == nil {
		ents, _ := os.ReadDir(filepath.Join(os.Getenv("XDG_DATA_HOME"), "zcx", "21356", "prompts"))
		if len(ents) != 0 {
			t.Fatal("backup written during dry-run")
		}
	}
}

func TestPromptPushServerRejections(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	for status, code := range map[int]int{403: 2, 422: 3} {
		f := &fakePrompt{text: livePrompt, max: 1000, patchTo: status}
		cap, _, stderr, err := runZcx(t, f.handler, "prompt", "push", writeTemp(t, livePrompt+"\nmore"), "--base-sha", liveSHA, "--yes")
		if exitCode(err) != code {
			t.Fatalf("status %d: exit %d want %d (%v)", status, exitCode(err), code, err)
		}
		if patches(cap) != 1 {
			t.Fatalf("status %d: patches %d", status, patches(cap))
		}
		if !strings.Contains(stderr, "was not changed") {
			t.Fatalf("stderr %q", stderr)
		}
		var ae *client.APIError
		if status == 403 && (!errors.As(err, &ae) || !strings.Contains(ae.Hint, "read-only")) {
			t.Fatalf("403 hint missing: %v", err)
		}
	}
}

func TestPromptPushNetworkErrorNeverRetried(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: livePrompt, max: 1000, drop: true}
	cap, _, _, err := runZcx(t, f.handler, "prompt", "push", writeTemp(t, livePrompt+"\nmore"), "--base-sha", liveSHA, "--yes")
	if err == nil {
		t.Fatal("expected error")
	}
	if patches(cap) != 1 {
		t.Fatalf("PATCH retried: %d", patches(cap))
	}
	msg := err.Error()
	for _, part := range []string{"MAY HAVE HAPPENED", "NOT retried", "backup", "zcx prompt diff", "zcx prompt restore"} {
		if !strings.Contains(msg, part) {
			t.Fatalf("message lacks %q: %s", part, msg)
		}
	}
	if exitCode(err) == 0 {
		t.Fatal("exit 0")
	}
}

func TestPromptPushVerificationFailure(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: livePrompt, max: 1000, ignore: true}
	cap, _, _, err := runZcx(t, f.handler, "prompt", "push", writeTemp(t, livePrompt+"\nmore"), "--base-sha", liveSHA, "--yes")
	if err == nil || !strings.Contains(err.Error(), "VERIFICATION FAILED") || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("err %v", err)
	}
	if patches(cap) != 1 {
		t.Fatal("patches")
	}
}

func TestPromptPullDiffHistoryRestore(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: livePrompt, max: 1000}
	out := filepath.Join(t.TempDir(), "pulled.txt")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	cap, stdout, _, err := runZcx(t, f.handler, "prompt", "pull", "-o", out)
	if fi, serr := os.Stat(out); serr != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("pull must leave mode 0600 on an existing file: %v %v", fi, serr)
	}
	if err != nil || !strings.Contains(stdout, liveSHA) {
		t.Fatalf("pull: %v %q", err, stdout)
	}
	if b, _ := os.ReadFile(out); string(b) != livePrompt {
		t.Fatalf("pulled file %q", b)
	}
	if patches(cap) != 0 {
		t.Fatal("pull wrote")
	}
	// diff
	_, stdout, _, err = runZcx(t, f.handler, "prompt", "diff", writeTemp(t, livePrompt+"\nextra"))
	if err != nil || !strings.Contains(stdout, "+extra") {
		t.Fatalf("diff: %v %q", err, stdout)
	}
	// history sees the backup from pull (same XDG per runZcx call, so redo in one dir).
	t.Setenv("ZCX_CHAT_ID", "21356")
	_, stdout, _, err = runZcx(t, f.handler, "prompt", "history", "--json")
	if err != nil || !gjson.Valid(stdout) {
		t.Fatalf("history: %v %q", err, stdout)
	}
}

func TestPromptRestore(t *testing.T) {
	t.Setenv("ZCX_CHAT_ID", "21356")
	f := &fakePrompt{text: "current live text", max: 1000}
	// runZcx isolates XDG_DATA_HOME per run, so an existing backup is not
	// reachable: restoring an unknown backup must fail with not_found and send nothing.
	cap, _, _, err := runZcx(t, f.handler, "prompt", "restore", "20260101T000000.000Z-deadbeef.txt", "--base-sha", store.SHA256(f.text), "--yes")
	if exitCode(err) != 4 {
		t.Fatalf("exit %d (%v)", exitCode(err), err)
	}
	if patches(cap) != 0 {
		t.Fatal("PATCH sent")
	}
}
