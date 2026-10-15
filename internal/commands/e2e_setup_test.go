package commands

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Captured records the requests a command produced against the fake server.
type Captured struct {
	mu       sync.Mutex
	Requests []CapturedReq
}

// CapturedReq is one request seen by the fake server.
type CapturedReq struct {
	Method, Path, RawQuery, Auth string
	Body                         []byte
}

// Paths returns "METHOD path" for every captured request.
func (c *Captured) Paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.Requests {
		out = append(out, r.Method+" "+r.Path)
	}
	return out
}

// resetFlags restores every flag in the tree to its default so package-level
// flag variables do not leak between runs (cobra does not reset them).
func resetFlags(c *cobra.Command) {
	reset := func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	c.Flags().VisitAll(reset)
	c.PersistentFlags().VisitAll(reset)
	for _, sub := range c.Commands() {
		resetFlags(sub)
	}
}

// capture redirects os.Stdout/os.Stderr to pipes for the duration of fn and
// returns what was written.
func capture(fn func()) (stdout, stderr string) {
	oldOut, oldErr := os.Stdout, os.Stderr
	ro, wo, _ := os.Pipe()
	re, we, _ := os.Pipe()
	os.Stdout, os.Stderr = wo, we
	var wg sync.WaitGroup
	var so, se []byte
	wg.Add(2)
	go func() { defer wg.Done(); so, _ = io.ReadAll(ro) }()
	go func() { defer wg.Done(); se, _ = io.ReadAll(re) }()
	fn()
	_ = wo.Close()
	_ = we.Close()
	wg.Wait()
	os.Stdout, os.Stderr = oldOut, oldErr
	return string(so), string(se)
}

// runZcx runs the real root command against an httptest server driven by
// handler (nil = 200 "{}"). The environment is fully isolated: HOME and
// XDG_DATA_HOME are temp dirs, the token is "zcu-test-token", the chat id is
// NOT preset (set ZCX_CHAT_ID yourself via t.Setenv before calling, or pass
// --chat). Tests must NOT call t.Parallel() (stdout is swapped, env mutated).
// Returns captured requests, stdout, stderr and the command error.
func runZcx(t *testing.T, handler http.HandlerFunc, args ...string) (*Captured, string, string, error) {
	t.Helper()
	cap := &Captured{}
	if handler == nil {
		handler = func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("{}")) }
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.mu.Lock()
		cap.Requests = append(cap.Requests, CapturedReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), body})
		cap.mu.Unlock()
		r.Body = io.NopCloser(bytesReader(body))
		handler(w, r)
	}))
	defer srv.Close()

	t.Setenv("ZCX_BASE_URL", srv.URL+"/api/v1")
	t.Setenv("ZIPCHAT_API_TOKEN", "zcu-test-token")
	if _, ok := os.LookupEnv("ZCX_CHAT_ID"); !ok {
		t.Setenv("ZCX_CHAT_ID", "")
	}
	if h := os.Getenv("ZCX_TEST_HOME"); h != "" {
		t.Setenv("HOME", h) // a test that seeded ~/.config/zcx/config.toml
	} else {
		t.Setenv("HOME", t.TempDir())
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	root := Root()
	resetFlags(root)
	root.SetArgs(args)
	var err error
	stdout, stderr := capture(func() { err = root.Execute() })
	return cap, stdout, stderr, err
}
