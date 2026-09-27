// Regression tests for the snippet helpers of POST .../knowledge/find, covering
// two code-review findings (2026-09-07):
//   - vaultSearchQueryTerms dropped every non-ASCII rune, so a CJK/Cyrillic/
//     accented query yielded no terms and matching notes rendered snippet-less.
//   - vaultSearchSnippet indexed a strings.ToLower(body) byte offset straight
//     into the ORIGINAL body; case-folding that changes byte length (e.g.
//     U+0130) misaligned the window and, with enough expansion before the match,
//     drove the offset past len(body) and PANICKED.
//
// Plus two 2026-09-08 code-review findings on the same helpers:
//   - F8: vaultSearchReadNoteHead trusted a single f.Read call as "the whole
//     scan window", but Read may legally return fewer bytes than requested
//     WITHOUT io.EOF (a network/FUSE-backed root, or — reproduced here — a
//     pipe). A term past that short read was invisible, and the hit was
//     stamped excerpt_unavailable: true, telling the reader the excerpt could
//     not be produced when the file was simply never fully read.
//   - F9: vaultSearchOrigOffset paid a string(r) + strings.ToLower(...)
//     allocation for EVERY rune, ASCII included, walking up to 128 KiB per
//     note hit — ~256k allocations for one snippet. Only a non-ASCII rune can
//     have its strings.ToLower byte length differ from its own; ASCII always
//     folds 1 byte to 1 byte.

package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVaultSearchQueryTermsKeepsNonASCII(t *testing.T) {
	cases := map[string][]string{
		"日本語":       {"日本語"},
		"сборка":    {"сборка"},
		"café thé":  {"cafe", "the"}, // both retained, folded the way the matcher folds them
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
				if g == w {
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

// TestVaultSearchReadNoteHead_ToleratesAShortReadWithoutEOF (F8's FIFO
// regression) lives in rest_knowledge_find_snippet_fifo_unix_test.go
// (build tag unix): syscall.Mkfifo does not exist on GOOS=windows, so a
// runtime-only t.Skip there still fails `go vet`/`go build` for windows —
// the test must not compile on that platform at all.

// TestVaultSearchOrigOffset_ASCIIBodyAllocatesNothing is F9's allocation
// regression: walking an all-ASCII body must not allocate per rune. Every
// ASCII rune's strings.ToLower byte length is provably identical to its own
// (1 byte to 1 byte, upper or not) — only a non-ASCII rune can legitimately
// differ, which is the case TestVaultSearchOrigOffsetMapsExpandingFold above
// still exercises through the UNCHANGED slow path.
func TestVaultSearchOrigOffset_ASCIIBodyAllocatesNothing(t *testing.T) {
	body := strings.Repeat("The Quick Brown Fox Jumps Over The Lazy Dog. ", 3000) + "needle"
	lowerPos := strings.Index(strings.ToLower(body), "needle")
	require.GreaterOrEqual(t, lowerPos, 0, "setup: needle not found in lowered body")

	allocs := testing.AllocsPerRun(5, func() {
		_ = vaultSearchOrigOffset(body, lowerPos)
	})

	const budget = 1.0
	assert.LessOrEqualf(t, allocs, budget,
		"vaultSearchOrigOffset over an all-ASCII %d-byte body allocated %.1f times per call "+
			"(budget %.0f) — every ASCII rune must skip the string(r)+strings.ToLower(...) "+
			"allocation entirely; the pre-fix version allocated roughly once per rune here",
		len(body), allocs, budget)
}
