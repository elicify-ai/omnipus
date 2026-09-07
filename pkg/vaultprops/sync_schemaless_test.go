// Omnipus — FR-021e, the two cases the plain untyped-note test cannot reach.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS — AND WHY IT IS NOT A SECOND COPY OF THE OBVIOUS TEST
//
// sync_fr021e_test.go already proves the headline case: a note with no `type:`
// key keeps its frontmatter through Sync, so a file saying `status: open` never
// answers TRUE to `status IS NULL`. That case is covered and is not repeated
// here.
//
// Two things it cannot establish on its own, and both matter:
//
//   1. FR-005's ordinary note — one that DOES declare a type, for which no
//      schema exists. That is the normal state of a vault mid-authoring, and it
//      takes a different branch in sync.go: `rec.TypeName()` is non-empty, the
//      schema lookup misses, and `schema` stays nil. A regression that made
//      only a MISSING type key fall through to raw storage would leave the
//      other test green and lose every half-authored note in a real vault.
//
//   2. THE FALSIFIABILITY GUARD. "Every note's frontmatter is indexed" is
//      trivially satisfiable by an implementation that invents rows. If a note
//      with no frontmatter at all acquired properties, `status IS NULL` would
//      start answering FALSE everywhere, and the ruling FR-021e implements —
//      that a note's file decides what it holds — would be broken in the
//      opposite direction, with every "is the value there?" assertion in the
//      suite still green. Absent must still mean absent.
// ---------------------------------------------------------------------------

package vaultprops

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// candidateAt returns the indexed candidate for one collection-relative path.
func candidateAt(t *testing.T, home, root, relPath string) propindex.Candidate {
	t.Helper()
	store := openStoreForTest(t, home, root)
	defer func() { _ = store.Close() }()

	var found *propindex.Candidate
	if err := store.Candidates(context.Background(), propindex.Selector{}, func(c propindex.Candidate) (propindex.Verdict, error) {
		if c.Path == relPath {
			cc := c
			found = &cc
		}
		return propindex.Rejected, nil
	}); err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if found == nil {
		t.Fatalf("%s was not indexed at all", relPath)
	}
	return *found
}

// TestSync_UnknownTypeIsAnOrdinaryNoteThatStillKeepsItsFrontmatter is case 1.
func TestSync_UnknownTypeIsAnOrdinaryNoteThatStillKeepsItsFrontmatter(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		// A real schema exists, so the lookup is a genuine MISS rather than an
		// empty schema set — the vault-mid-authoring case.
		".omnipus-vault/records/plant.yaml": plantSchema,
		"Notes/HalfAuthored.md":             "---\ntype: nothing-defines-this\nstatus: blocked\nowner: mia\n---\n# Half authored\n",
	})
	syncOnce(t, home, root, SyncOptions{})

	got := candidateAt(t, home, root, "Notes/HalfAuthored.md")
	if got.RecordType != "" {
		t.Errorf("a note whose declared type matches no schema is an ORDINARY note (FR-005) and must carry "+
			"no record type, got %q", got.RecordType)
	}
	assertRawProp(t, got, "status", "blocked")
	assertRawProp(t, got, "owner", "mia")
}

// TestSync_NoteWithoutFrontmatterStillContributesNoProperties is case 2 — the
// guard that stops every other FR-021e assertion from passing vacuously.
func TestSync_NoteWithoutFrontmatterStillContributesNoProperties(t *testing.T) {
	skipWithoutSQLite(t)

	home := syncHome(t)
	root := syncVault(t, map[string]string{
		"Notes/Bare.md": "# Bare\nNothing is declared here.\n",
	})
	syncOnce(t, home, root, SyncOptions{})

	got := candidateAt(t, home, root, "Notes/Bare.md")
	if len(got.PropOrder) != 0 {
		t.Errorf("a note with no frontmatter was given %d propert(ies) %v — FR-021e stores what the FILE "+
			"holds, and a note that declares nothing must stay distinguishable from one that declares a "+
			"value, or `status IS NULL` stops meaning anything", len(got.PropOrder), got.PropOrder)
	}
	if _, ok := got.Prop("status"); ok {
		t.Errorf("a note with no frontmatter reports a status property: %+v", got.Props)
	}
}

// assertRawProp requires that a property reached the index carrying the text
// the file actually held.
//
// Both halves matter. A row that exists but holds nothing would answer
// `status IS NULL` correctly and `status = "blocked"` wrongly — the ruling is
// about the VALUE being there, not merely a row.
func assertRawProp(t *testing.T, c propindex.Candidate, name, want string) {
	t.Helper()
	p, ok := c.Prop(name)
	if !ok {
		t.Errorf("%s: the file says %s: %s, but the index holds no %q property at all — the exact state "+
			"FR-021e forbids; indexed properties were %v", c.Path, name, want, name, c.PropOrder)
		return
	}
	for _, e := range p.Elems {
		if e.Text == want || e.Raw == want {
			return
		}
	}
	t.Errorf("%s: the %q property reached the index without its value; file says %q, stored elems are %+v",
		c.Path, name, want, p.Elems)
}
