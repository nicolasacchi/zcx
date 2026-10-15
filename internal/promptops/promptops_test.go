package promptops

import (
	"errors"
	"strings"
	"testing"

	"github.com/nicolasacchi/zcx/internal/store"
)

func kindOf(err error) Kind {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Kind
	}
	return ""
}

func TestPreflight(t *testing.T) {
	live := "line one\nline two"
	sha := store.SHA256(live)
	cases := []struct {
		name, file, base string
		max              int
		kind             Kind
		noop             bool
	}{
		{"blank", " \n\t ", sha, 100, KindBlank, false},
		{"missing base", "x", "", 100, KindMissingBase, false},
		{"mismatch", "x", strings.Repeat("0", 64), 100, KindBaseMismatch, false},
		{"too long", strings.Repeat("a", 11), sha, 10, KindTooLong, false},
		{"noop", "\n" + live + "\n\n", strings.ToUpper(sha), 100, "", true},
		{"change", "line one\nline 2", sha, 100, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := Preflight(c.file, live, c.max, c.base)
			if kindOf(err) != c.kind {
				t.Fatalf("kind = %q err=%v, want %q", kindOf(err), err, c.kind)
			}
			if err == nil {
				if p.NoOp != c.noop {
					t.Fatalf("noop=%v", p.NoOp)
				}
				if !c.noop && !strings.Contains(p.Diff, "+line 2") {
					t.Fatalf("diff: %q", p.Diff)
				}
			}
		})
	}
}

func TestBlankCheckedBeforeBase(t *testing.T) {
	_, err := Preflight("", "live", 10, "bad")
	if kindOf(err) != KindBlank {
		t.Fatal(err)
	}
}
