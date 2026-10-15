package textdiff

import (
	"strings"
	"testing"
)

func TestUnified(t *testing.T) {
	if Unified("a\n", "a\n", "x", "y") != "" {
		t.Fatal("identical must be empty")
	}
	d := Unified("one\ntwo\n", "one\nthree\n", "live", "file")
	for _, w := range []string{"--- live", "+++ file", "-two", "+three"} {
		if !strings.Contains(d, w) {
			t.Errorf("missing %q in:\n%s", w, d)
		}
	}
}
