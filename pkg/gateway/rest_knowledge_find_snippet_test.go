// Regression tests for the snippet helpers of POST .../knowledge/find, covering
// two code-review findings (2026-09-07):
//   - vaultSearchQueryTerms dropped every non-ASCII rune, so a CJK/Cyrillic/
//     accented query yielded no terms and matching notes rendered snippet-less.
//   - vaultSearchSnippet indexed a strings.ToLower(body) byte offset straight
//     into the ORIGINAL body; case-folding that changes byte length (e.g.
//     U+0130) misaligned the window and, with enough expansion before the match,
//     drove the offset past len(body) and PANICKED.

package gateway

import (
	"strings"
	"testing"
)

func TestVaultSearchQueryTermsKeepsNonASCII(t *testing.T) {
	cases := map[string][]string{
		"日本語":       {"日本語"},
		"сборка":    {"сборка"},
		"café thé":  {"café", "thé"}, // longest-first; both retained
		"acme corp": {"acme", "corp"},
	}
	for query, want := range cases {
		got := vaultSearchQueryTerms(query)
		if len(got) != len(want) {
			t.Fatalf("query %q: got %d terms %v, want %d %v", query, len(got), got, len(want), want)
		}
		for _, w := range want {
			found := false
			for _, g := range got {
				if g == strings.ToLower(w) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("query %q: term %q missing from %v", query, w, got)
			}
		}
	}
}

// TestVaultSearchOrigOffsetMapsExpandingFold verifies a lowerBody byte offset is
// mapped back to a valid ORIGINAL-body offset when case-folding expanded the
// text before it (U+0130 'İ' → "i̇", 2 bytes → 3).
func TestVaultSearchOrigOffsetMapsExpandingFold(t *testing.T) {
	body := strings.Repeat("İ", 100) + "needle"
	lower := strings.ToLower(body)
	lowerPos := strings.Index(lower, "needle")
	if lowerPos < 0 {
		t.Fatal("setup: needle not in lowered body")
	}
	orig := vaultSearchOrigOffset(body, lowerPos)
	if orig < 0 || orig > len(body) {
		t.Fatalf("mapped offset %d out of range [0,%d]", orig, len(body))
	}
	if !strings.HasPrefix(body[orig:], "needle") {
		t.Fatalf("mapped offset %d does not land on the match", orig)
	}
}

// TestVaultSearchWindowNoPanicOnExpandingFold is the crash regression: before
// the fix, an offset taken from the (longer) lowered body exceeded len(body) and
// body[start:end] / body[start] panicked. The mapped offset must produce a real
// snippet containing the match, with no panic.
func TestVaultSearchWindowNoPanicOnExpandingFold(t *testing.T) {
	body := strings.Repeat("İ", 100) + "needle tail"
	lower := strings.ToLower(body)
	lowerPos := strings.Index(lower, "needle")
	orig := vaultSearchOrigOffset(body, lowerPos)
	snippet := vaultSearchWindow(body, orig) // must not panic
	if !strings.Contains(snippet, "needle") {
		t.Fatalf("snippet %q does not contain the match", snippet)
	}
}
