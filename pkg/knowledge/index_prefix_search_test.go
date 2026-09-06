// Reproduction/regression harness for A2(b) — vault-tools-test-report F2 and
// harness-test-issues Issue 14: a prefix of an indexed term must match it, so a
// caller typing a partial word ("compos") finds the fuller indexed term
// ("composio") rather than getting zero hits.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"fmt"
	"testing"
)

// TestSearch_PrefixOfIndexedTermMatches reproduces "words=\"compos\" returns 0
// though \"composio\" is a listed indexed term (no prefix match)".
func TestSearch_PrefixOfIndexedTermMatches(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	const n = 12
	for i := 0; i < n; i++ {
		b2WriteFile(t, root, fmt.Sprintf("notes/note-%04d.md", i),
			fmt.Sprintf("Note %d discusses the composio integration in passing.", i))
	}
	ix := b2Open(t, home, root)
	if stats := b2Sync(t, ix); stats.Indexed != n {
		t.Fatalf("Indexed = %d, want %d", stats.Indexed, n)
	}

	hits, err := ix.Search("compos", 1000)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != n {
		t.Fatalf("Search(\"compos\") returned %d hits, want %d (a prefix must match the indexed term \"composio\")", len(hits), n)
	}
}

// TestSearch_PrefixBelowMinLengthDoesNotMatch pins the deliberate floor: a
// 1–2 character fragment is NOT treated as a prefix, so it cannot pull a large
// fraction of the dictionary into a result set. "co" must not match "composio"
// on the strength of the prefix pass alone.
func TestSearch_PrefixBelowMinLengthDoesNotMatch(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "notes/only.md", "This note mentions composio exactly once.")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	hits, err := ix.Search("co", 1000)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("Search(\"co\") returned %d hits, want 0 (a 2-char fragment is below the prefix floor)", len(hits))
	}
}
