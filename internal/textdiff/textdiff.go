// Package textdiff renders unified diffs.
package textdiff

import "github.com/pmezard/go-difflib/difflib"

// Unified returns a unified diff of a -> b labelled nameA/nameB, or "" when the
// texts are identical.
func Unified(a, b, nameA, nameB string) string {
	if a == b {
		return ""
	}
	s, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(a),
		B:        difflib.SplitLines(b),
		FromFile: nameA,
		ToFile:   nameB,
		Context:  3,
	})
	if err != nil {
		return ""
	}
	return s
}
