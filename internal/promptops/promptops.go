// Package promptops holds the pure safety logic of "zcx prompt push/restore".
// It has no I/O so every refusal path is unit-testable.
package promptops

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nicolasacchi/zcx/internal/store"
	"github.com/nicolasacchi/zcx/internal/textdiff"
)

// Refusal kinds, each mapped to a distinct exit code by the command layer.
type Kind string

const (
	KindBlank        Kind = "blank"
	KindBaseMismatch Kind = "base_sha_mismatch"
	KindMissingBase  Kind = "base_sha_missing"
	KindTooLong      Kind = "too_long"
)

// Refusal is a pre-flight rejection: nothing has been sent to the server.
type Refusal struct {
	Kind Kind
	Msg  string
	Hint string
}

func (r *Refusal) Error() string { return r.Msg }

// Normalize mirrors the server: leading and trailing whitespace is stripped.
func Normalize(s string) string { return strings.TrimSpace(s) }

// Plan is the outcome of a successful pre-flight check.
type Plan struct {
	New     string // normalized new prompt
	LiveSHA string
	Diff    string // unified diff live -> new, "" when NoOp
	NoOp    bool
}

// NormalizeSHA lower-cases and trims a user supplied sha256.
func NormalizeSHA(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Preflight runs the push safety checks in the mandated order, after the
// caller has read the file and fetched the live prompt:
// blank -> base-sha -> max length -> diff / no-op.
func Preflight(fileText, live string, maxLen int, baseSHA string) (*Plan, error) {
	nw := Normalize(fileText)
	if nw == "" {
		return nil, &Refusal{Kind: KindBlank, Msg: "the prompt file is blank after trimming whitespace; the server would reject it (422)",
			Hint: "check the file path and content"}
	}
	base := NormalizeSHA(baseSHA)
	if base == "" {
		return nil, &Refusal{Kind: KindMissingBase, Msg: "--base-sha is required",
			Hint: "run 'zcx prompt pull' and pass the printed sha256"}
	}
	liveSHA := store.SHA256(live)
	if liveSHA != base {
		return nil, &Refusal{Kind: KindBaseMismatch,
			Msg:  fmt.Sprintf("the live prompt changed since you pulled it (live sha256 %s, --base-sha %s); someone may have edited it in the dashboard", liveSHA, base),
			Hint: "re-run 'zcx prompt pull', then 'zcx prompt diff FILE' to merge the changes, and retry with the new sha"}
	}
	if maxLen > 0 {
		if n := utf8.RuneCountInString(nw); n > maxLen {
			return nil, &Refusal{Kind: KindTooLong, Msg: fmt.Sprintf("the prompt is %d characters, the maximum is %d", n, maxLen),
				Hint: "shorten the prompt"}
		}
	}
	p := &Plan{New: nw, LiveSHA: liveSHA}
	if nw == live {
		p.NoOp = true
		return p, nil
	}
	p.Diff = textdiff.Unified(live, nw, "live", "new")
	return p, nil
}
