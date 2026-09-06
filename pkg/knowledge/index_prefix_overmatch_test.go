// Round-2 regression guard for A2(b) prefix matching: the prefix pass must
// tokenize a query the same way the prose ("en") analyzer tokenizes text for
// indexing. foldTokens split on the underscore the analyzer keeps inside a
// token, so a prefix token "keyword" (from the query "keyword_new") matched the
// unrelated indexed term "keyword_old" — an unrelated term becoming findable,
// and an edited note findable by its NEW term before the edit.
//
// Correct semantics: a query token is a prefix of an INDEXED term (query
// "compos" matches indexed "composio"), never a fragment of the query matching a
// different compound term.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//   -run '^TestSearch_Prefix' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import "testing"

// TestSearch_PrefixDoesNotOverMatchAcrossUnderscore pins the direction: an
// indexed compound "keyword_old" must NOT be found by a query for a DIFFERENT
// compound "keyword_new" that merely shares a leading fragment. The shared
// fragment "keyword" is not a prefix of "keyword_old" as a whole query token —
// it is only a fragment foldName produced by breaking on the underscore, which
// the prose analyzer does not do.
func TestSearch_PrefixDoesNotOverMatchAcrossUnderscore(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "note.md", "alpha keyword_old body text\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	// The whole compound the note DOES contain is findable (exact + prefix).
	if hits, err := ix.Search("keyword_old", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "note.md") {
		t.Fatalf("keyword_old must be findable, got %v", b2HitPaths(hits))
	}

	// A different compound sharing only the "keyword" fragment must NOT match.
	if hits, err := ix.Search("keyword_new", 10); err != nil {
		t.Fatal(err)
	} else if len(hits) != 0 {
		t.Fatalf("keyword_new must NOT match a note containing only keyword_old (over-match across underscore), got %v", b2HitPaths(hits))
	}

	// The bare shared token, typed on its own, is a legitimate prefix of the
	// indexed compound and SHOULD match — this is the A2(b) behaviour, and it is
	// what distinguishes "do not fragment the query" from "do not prefix-match".
	if hits, err := ix.Search("keyword", 10); err != nil {
		t.Fatal(err)
	} else if !containsPath(hits, "note.md") {
		t.Fatalf("the bare token keyword must prefix-match indexed keyword_old, got %v", b2HitPaths(hits))
	}
}
