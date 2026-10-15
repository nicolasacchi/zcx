// Package client is the typed Zipchat Backend API client. It sits on the
// fleet-shared clicore/httpclient (retries 429/5xx for any method, never
// retries POST/PATCH after a network error) and adds bearer auth, rate-limit
// awareness (X-RateLimit-*) and APIError mapping with hints.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nicolasacchi/clicore/httpclient"
	"github.com/nicolasacchi/clicore/redact"
)

const (
	// DefaultBaseURL is the production API origin + version prefix.
	DefaultBaseURL = "https://app.zipchat.ai/api/integrations/backend_api/v1"

	// MaxPerPage is the largest page size accepted by list endpoints.
	MaxPerPage = 100
	// MaxMessagePerPage is the largest message page size of a conversation detail.
	MaxMessagePerPage = 200

	// lowWater is the Remaining value at or below which the client sleeps until
	// the window resets before sending the next request.
	lowWater = 3
	// maxWait caps one rate-limit sleep (the window is 60 s).
	maxWait = 65 * time.Second
)

// Options configures New.
type Options struct {
	Token      string
	BaseURL    string // default DefaultBaseURL
	Verbose    bool
	HTTPClient *http.Client // tests; default 60 s timeout client
	// MaxRetries bounds retries of GET on 5xx/network errors (default 3) and of
	// any request on 429 (default 5). Tests pass 0. PATCH is never retried.
	MaxRetries *int
	// OnRateWait, if set, is called with the sleep duration before the client
	// pauses for the rate limit (used for progress messages on stderr).
	OnRateWait func(d time.Duration)
}

// RateLimit is the last observed X-RateLimit-* state.
type RateLimit struct {
	Known     bool      `json:"known"`
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
}

// Client talks to the Zipchat Backend API.
type Client struct {
	http    *httpclient.Client
	base    string
	token   string
	verbose bool
	onWait  func(time.Duration)

	maxRetries     int // 5xx / network (GET only)
	maxRateRetries int // 429 (GET only)

	mu    sync.Mutex
	rl    RateLimit
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

type bearer struct{ c *Client }

func (b bearer) Apply(ctx context.Context, req *http.Request, _ int) error {
	if err := b.c.waitForQuota(ctx); err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+b.c.token)
	return nil
}
func (bearer) OnUnauthorized() bool { return false }

// New builds a client. An empty token is allowed here; requests then fail with
// a 401-style APIError carrying a hint (so `config doctor` can report it).
func New(o Options) *Client {
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	c := &Client{base: base, token: o.Token, verbose: o.Verbose, onWait: o.OnRateWait,
		now: time.Now, sleep: sleepCtx}
	c.maxRetries, c.maxRateRetries = 3, 5
	if o.MaxRetries != nil {
		c.maxRetries, c.maxRateRetries = *o.MaxRetries, *o.MaxRetries
	}
	var hc http.Client
	if o.HTTPClient != nil {
		hc = *o.HTTPClient
	} else {
		hc = http.Client{Timeout: httpclient.DefaultTimeout}
	}
	rt := hc.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	hc.Transport = &observingTransport{base: rt, c: c}
	// The retry loop lives in do(): clicore retries any method on 429/5xx and
	// drops response headers when retries run out, so it is disabled here.
	c.http = httpclient.New(base, bearer{c}, httpclient.WithVerbose(o.Verbose),
		httpclient.WithHTTPClient(&hc), httpclient.WithMaxRetries(0))
	return c
}

// observingTransport feeds every response (including 429 and 5xx) to the
// rate-limit state, so the reset time of a 429 is never lost.
type observingTransport struct {
	base http.RoundTripper
	c    *Client
}

func (t *observingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	t.c.observe(resp.Header)
	if resp.StatusCode == http.StatusTooManyRequests {
		t.c.markExhausted(resp.Header)
	}
	return resp, nil
}

// markExhausted records that the window is spent, so the next attempt's
// waitForQuota sleeps until X-RateLimit-Reset (+1 s, capped at maxWait).
func (c *Client) markExhausted(h http.Header) {
	now := c.now()
	reset := now.Add(3 * time.Second) // no usable header: conservative guess
	if rst, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		reset = time.Unix(rst, 0)
	}
	if !reset.After(now) {
		reset = now // waitForQuota still adds its 1 s margin
	}
	c.mu.Lock()
	c.rl.Known = true
	c.rl.Remaining = 0
	if c.rl.Limit == 0 {
		c.rl.Limit = 120
	}
	c.rl.Reset = reset
	c.mu.Unlock()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BaseURL returns the origin+prefix in use.
func (c *Client) BaseURL() string { return c.base }

// RateLimit returns the last observed rate-limit state.
func (c *Client) RateLimit() RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rl
}

func (c *Client) observe(h http.Header) {
	if h == nil {
		return
	}
	lim, e1 := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	rem, e2 := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	rst, e3 := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return
	}
	c.mu.Lock()
	c.rl = RateLimit{Known: true, Limit: lim, Remaining: rem, Reset: time.Unix(rst, 0)}
	c.mu.Unlock()
}

// waitForQuota sleeps until the window resets when Remaining is low.
func (c *Client) waitForQuota(ctx context.Context) error {
	c.mu.Lock()
	rl := c.rl
	c.mu.Unlock()
	if !rl.Known || rl.Remaining > lowWater {
		return nil
	}
	d := rl.Reset.Sub(c.now())
	if d < 0 {
		return nil
	}
	d += time.Second // clock-skew margin
	if d > maxWait {
		d = maxWait
	}
	if c.onWait != nil {
		c.onWait(d)
	}
	if err := c.sleep(ctx, d); err != nil {
		return err
	}
	c.mu.Lock()
	c.rl.Remaining = c.rl.Limit // window rolled over
	c.mu.Unlock()
	return nil
}

func (c *Client) buildURL(path string, q url.Values) string {
	u := c.base + "/" + strings.TrimLeft(path, "/")
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// do performs a request and returns the raw body of a 2xx response.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any) ([]byte, error) {
	if c.token == "" {
		return nil, &APIError{Status: 401, Kind: "auth_failed", Detail: "no API token configured",
			Hint: "set ZIPCHAT_API_TOKEN, --token, or ~/.config/zipchat/env; run 'zcx config doctor'"}
	}
	var payload []byte
	ctype := ""
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		ctype = "application/json"
	}
	u := c.buildURL(path, q)
	if c.verbose {
		fmt.Fprintf(os.Stderr, "> %s %s\n", method, redact.URL(u))
		if len(payload) > 0 {
			fmt.Fprintf(os.Stderr, "> body: %d bytes\n", len(payload))
		}
	}
	start := time.Now()
	var respBody []byte
	var resp *httpclient.Response
	for attempt := 0; ; attempt++ {
		var err error
		respBody, resp, err = c.http.Do(ctx, method, u, payload, ctype)
		if resp != nil {
			c.observe(resp.Header)
		}
		if err == nil {
			break
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		// Only GET is retried; a PATCH is sent exactly once.
		retry := false
		switch {
		case method != http.MethodGet:
		case status == http.StatusTooManyRequests:
			// the next Apply sleeps until X-RateLimit-Reset (markExhausted).
			retry = attempt < c.maxRateRetries
		case status >= 500:
			retry = attempt < c.maxRetries
		case status == 0:
			retry = attempt < c.maxRetries && httpclient.ShouldRetryNetwork(method, err)
		}
		if retry && status != http.StatusTooManyRequests {
			if e := c.sleep(ctx, time.Duration(1<<attempt)*time.Second); e != nil {
				return nil, e
			}
		}
		if retry {
			continue
		}
		if status != 0 { // retries exhausted on 429/5xx
			return nil, &APIError{Status: status, Kind: kindOf(status),
				Detail: err.Error(), Hint: HintFor(method, status)}
		}
		return nil, err
	}
	if c.verbose {
		fmt.Fprintf(os.Stderr, "< %d (%s, %d bytes)\n", resp.StatusCode, time.Since(start).Round(time.Millisecond), len(respBody))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseError(method, resp.StatusCode, respBody)
	}
	return respBody, nil
}

func kindOf(status int) string { return parseError("", status, nil).Kind }

// getJSON GETs path and decodes the JSON response into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	b, err := c.do(ctx, http.MethodGet, path, q, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// GetRaw GETs an API path (relative to the base URL, e.g. "chats") and returns
// the raw JSON body. Intended for ad-hoc/raw output.
func (c *Client) GetRaw(ctx context.Context, path string, q url.Values) (json.RawMessage, error) {
	return c.do(ctx, http.MethodGet, path, q, nil)
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
