package client

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/nicolasacchi/clicore/cierrors"
)

// APIError is the zcx error envelope. HTTP failures set Status; client-side
// guards set Kind (e.g. "write_locked") and leave Status 0.
type APIError struct {
	Status int
	Kind   string
	Detail string
	Hint   string
}

func (e *APIError) Error() string {
	if e.Status == 0 {
		msg := e.Detail
		if msg == "" {
			msg = e.Kind
		}
		if e.Hint != "" {
			return fmt.Sprintf("%s — %s", msg, e.Hint)
		}
		return msg
	}
	if e.Detail != "" {
		return fmt.Sprintf("%d: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("API error %d", e.Status)
}

// ExitCode follows the fleet table (auth=2, validation=3, not_found=4,
// rate_limited=5, write_locked=6, else 1). Zipchat returns 422 where the table
// expects 400, so 422 maps to 3.
func (e *APIError) ExitCode() int {
	if e.Status == http.StatusUnprocessableEntity {
		return 3
	}
	return cierrors.ExitCodeFor(e.Status, e.Kind)
}

// HintFor returns the actionable hint for an HTTP failure. method matters for
// 403: on a PATCH it means the user token's role is read-only.
func HintFor(method string, status int) string {
	switch status {
	case 401:
		return "token missing/invalid, or the chat id is not accessible with it; run 'zcx config doctor'"
	case 403:
		if method != http.MethodGet {
			return "forbidden — the token's user has a read-only (member) role in the dashboard; configuration writes need a non-member role"
		}
		return "forbidden — this token cannot access the resource"
	case 404:
		return "not found — check the chat/conversation/tool id"
	case 422:
		return "unprocessable — the server rejected a parameter or the body (blank/too long/malformed timestamp)"
	case 429:
		return "rate limited — 120 requests per 60 s per credential; wait for the window to reset"
	}
	return ""
}

func parseError(method string, status int, body []byte) *APIError {
	e := &APIError{Status: status, Kind: cierrors.KindForStatus(status)}
	if status == http.StatusUnprocessableEntity {
		e.Kind = "validation"
	}
	var env struct {
		Error   json.RawMessage `json:"error"`
		Errors  json.RawMessage `json:"errors"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &env) == nil {
		for _, raw := range []json.RawMessage{env.Error, env.Errors} {
			if len(raw) == 0 {
				continue
			}
			var s string
			var list []string
			switch {
			case json.Unmarshal(raw, &s) == nil && s != "":
				e.Detail = s
			case json.Unmarshal(raw, &list) == nil && len(list) > 0:
				e.Detail = strings.Join(list, "; ")
			}
			if e.Detail != "" {
				break
			}
		}
		if e.Detail == "" {
			e.Detail = env.Message
		}
	}
	if e.Detail == "" {
		e.Detail = strings.ToLower(http.StatusText(status))
	}
	e.Hint = HintFor(method, status)
	return e
}
