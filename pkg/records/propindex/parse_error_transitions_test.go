// Omnipus — regression coverage for the 2026-09-14 Codex review, finding 5:
// `parse_error` was written on INSERT only, so a note's malformed-frontmatter
// status froze at whatever it was the first time the row was written.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package propindex

import "testing"

// parseErrorOf reads the stored parse_error for one path through the same
// candidate stream every consumer reads.
func parseErrorOf(t *testing.T, store Store, path string) string {
	t.Helper()
	for _, c := range collect(t, store, Selector{}) {
		if c.Path == path {
			return c.ParseError
		}
	}
	t.Fatalf("no row for %s", path)
	return ""
}

// TestUpsert_ParseErrorFollowsEveryTransition — healthy -> malformed ->
// repaired. Each upsert must leave the row carrying the CURRENT status: a
// repaired note must not stay flagged, and a freshly broken one must not read
// as healthy.
func TestUpsert_ParseErrorFollowsEveryTransition(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := plantSchema(t)
	const path = "garden/plant-0001.md"
	healthy := "---\ntype: plant\nid: PL-0001\nspecies: Monstera\ncondition: growing\n---\n# Plant\n"
	malformed := "---\ntype: plant\nid: PL-0001\nspecies: Monstera\n\n# Plant\n\nfence never closed body\n"

	mustUpsert(t, store, note(t, path, sc, healthy))
	if got := parseErrorOf(t, store, path); got != "" {
		t.Fatalf("healthy note must carry no parse_error, got %q", got)
	}

	mustUpsert(t, store, note(t, path, sc, malformed))
	if got := parseErrorOf(t, store, path); got == "" {
		t.Fatalf("a note whose frontmatter broke must carry its parse error after the UPDATE, got none")
	}

	mustUpsert(t, store, note(t, path, sc, healthy))
	if got := parseErrorOf(t, store, path); got != "" {
		t.Fatalf("a repaired note must have its parse_error cleared after the UPDATE, still carries %q", got)
	}
}

// TestUpsert_ParseErrorIsSetOnFirstWriteToo — the insert half still works
// (the review's "only exercises initial indexing" tests cover this; kept
// here so the transition test above cannot pass by an insert accident).
func TestUpsert_ParseErrorIsSetOnFirstWriteToo(t *testing.T) {
	store, _ := openIndex(t, Options{})
	sc := plantSchema(t)
	const path = "garden/broken.md"
	mustUpsert(t, store, note(t, path, sc, "---\ntype: plant\nid: PL-0009\n\n# Plant\n\nfence never closed body\n"))
	if got := parseErrorOf(t, store, path); got == "" {
		t.Fatalf("a malformed note inserted for the first time must carry its parse error")
	}
}
