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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestVaultSearchReadNoteHead_ToleratesAShortReadWithoutEOF is F8's direct
// regression: a single f.Read call legally returns fewer bytes than
// requested WITHOUT io.EOF — reproduced here with a FIFO, whose Read()
// returns whatever is CURRENTLY buffered in the pipe rather than waiting to
// fill the caller's buffer. The writer delivers the note in two separate
// writes with a pause between them, so a single Read only ever sees the
// first write; io.ReadFull must keep reading until the second write (or a
// real EOF) arrives.
func TestVaultSearchReadNoteHead_ToleratesAShortReadWithoutEOF(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("syscall.Mkfifo is POSIX-only")
	}

	dir := t.TempDir()
	const name = "slow-note.md"
	fifoPath := filepath.Join(dir, name)
	require.NoError(t, syscall.Mkfifo(fifoPath, 0o600))

	go func() {
		wf, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer func() { _ = wf.Close() }()
		// First write: whatever is available when the reader's (possibly
		// single) Read() call fires.
		_, _ = wf.WriteString("head-marker ")
		// Give a single-Read implementation time to have already returned
		// with just the first write before the rest arrives — reproducing
		// the exact "short read, no EOF yet" window F8 is about.
		time.Sleep(150 * time.Millisecond)
		_, _ = wf.WriteString("tail-marker")
	}()

	body, ok := vaultSearchReadNoteHead(dir, name)
	require.True(t, ok, "a legitimately short-but-not-EOF read must still succeed")
	assert.Contains(t, body, "head-marker")
	assert.Contains(t, body, "tail-marker",
		"a short read that returns fewer bytes than the scan window without EOF must not be "+
			"treated as the whole file — the reader must keep reading until the writer catches up")
}

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
