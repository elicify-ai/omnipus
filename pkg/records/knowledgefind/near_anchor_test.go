// Omnipus — A2(c) reproduction/regression: `near`'s anchor note counts as
// hop 0, so `near=<note> + words=<term>` returns the anchor itself when it
// contains the term, even when the anchor is an ordinary note with no declared
// record type (vault-tools-report Issue 7 companion / F reports: "near=<Composio
// note> + words=composio returns 0 even though the anchor note contains the
// word").
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"testing"
)

// TestNear_PlainNoteAnchorCountsAsHopZeroWithWords is the direct reproduction.
// The anchor is an ORDINARY note (no `type:`), so it is not a node in the typed
// relation graph near/hops walks — but "near this note" must still include the
// note itself at hop 0, and a `words` intersection over it must return it.
//
// The narrow record resolver (Deps.Resolve) deliberately returns !ok for a note
// with no record identity, which is exactly why the anchor used to vanish; the
// anchor's own PATH is resolved through Deps.ResolveNear instead.
func TestNear_PlainNoteAnchorCountsAsHopZeroWithWords(t *testing.T) {
	f := gardenCorpus(t)

	// An ordinary note — no frontmatter, no record type — that mentions the
	// search term. It exists on disk (so ResolveNear can place it) but has no
	// record identity (so the graph resolver cannot).
	anchorPath := "garden/notes/composio.md"
	f.write(anchorPath, "The composio integration lives here, described in plain prose.")

	// Deps.Resolve (record identity) does NOT know this note — it is not a
	// record. Deps.ResolveNear DOES: it maps the near reference to the note's
	// own path, which is how the anchor becomes hop 0.
	f.text.only = []string{anchorPath}

	d := f.deps()
	d.ResolveNear = func(near string) (string, bool) {
		if near == anchorPath || near == "composio" {
			return anchorPath, true
		}
		return "", false
	}

	resp := mustFind(t, d, req(withNear(anchorPath, 1), withWords("composio")))
	got := rowPaths(resp)
	want := []string{anchorPath}
	if !stringSliceEqual(got, want) {
		t.Fatalf("near(plain-note anchor)+words: got %v, want %v (the anchor note contains the word and must be returned at hop 0)", got, want)
	}
}

// TestNear_PlainNoteAnchorAloneReturnsItself pins the hop-0 rule without a
// words half: near of a plain note, hops=1, returns the anchor itself (it has
// no typed relations, so nothing else), never a silent empty answer.
func TestNear_PlainNoteAnchorAloneReturnsItself(t *testing.T) {
	f := gardenCorpus(t)
	anchorPath := "garden/notes/loose.md"
	f.write(anchorPath, "A loose note with no record type at all.")

	d := f.deps()
	d.ResolveNear = func(near string) (string, bool) {
		if near == anchorPath {
			return anchorPath, true
		}
		return "", false
	}

	resp := mustFind(t, d, req(withNear(anchorPath, 1)))
	got := rowPaths(resp)
	want := []string{anchorPath}
	if !stringSliceEqual(got, want) {
		t.Fatalf("near(plain-note anchor) alone: got %v, want %v (the anchor is hop 0 and must be returned)", got, want)
	}
}
