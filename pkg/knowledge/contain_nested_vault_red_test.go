// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan test
// 71.
//
// Oracle: D-WALK's "Nested-vault rule" (new in round 2, resolves
// R2-MIN-012's second bullet) — "the discovery helper... stops descending at
// any directory that itself carries a .obsidian or .omnipus-vault marker,
// OTHER than the collection root it started from... This is one added
// stop-condition on an existing walk, not a new walk or a new containment
// mechanism" — and EC-11 ("A hand-copied nested vault inside a knowledge
// base... is not descended into by the outer collection's discovery walk").
//
// scanSkippedDirNames already skips a directory literally NAMED ".obsidian"
// or ".omnipus-vault" at any depth (F10/F17, unchanged) — but that is a
// DIFFERENT rule than D-WALK's new one: it does not stop the walk from
// descending into the folder that HOSTS such a marker (e.g. "foo/" itself,
// sibling to "foo/.obsidian/"), only from entering the marker directory's
// own contents. A file placed directly inside "foo/" (not inside
// "foo/.obsidian/") is still found by WalkContained today, exactly the gap
// R2-MIN-012 identifies.
//
// This is real and testable directly against WalkContained AS IT EXISTS
// TODAY (D-WALK's own text: "unmodified except for the nested-vault stop
// condition") — no new discovery helper is needed to demonstrate the gap.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDiscovery_StopsAtNestedVaultMarker$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDiscovery_StopsAtNestedVaultMarker is TDD Plan test 71 (R2-MIN-012,
// D-WALK, Dataset A13): a folder hand-copied into a knowledge base that
// carries its OWN .obsidian marker must not be descended into by the outer
// collection's walk — its sibling content (here, a plain file standing in
// for a future .view file) must be invisible to the outer walk, exactly as
// the inner vault's own boundary requires.
func TestDiscovery_StopsAtNestedVaultMarker(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "imported-vault")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, ".obsidian"), 0o755))
	// Content sitting directly inside the nested vault's own root — sibling
	// to its ".obsidian" marker, not inside it. This is exactly where a
	// hand-copied vault's own .view files would live, per EC-11.
	nestedContentPath := filepath.Join(nested, "inner-notes.view")
	require.NoError(t, os.WriteFile(nestedContentPath, []byte("name: inner\n"), 0o644))
	// Content at the outer collection's own top level, for a sanity check
	// that the walk still finds ordinary files elsewhere.
	require.NoError(t, os.WriteFile(filepath.Join(root, "outer.view"), []byte("name: outer\n"), 0o644))

	fsys := OSLinkFS()
	cr, err := NewCollectionRoot(fsys, root)
	require.NoError(t, err)

	res, err := WalkContained(fsys, cr)
	require.NoError(t, err)

	for _, f := range res.Files {
		if f == "imported-vault/inner-notes.view" {
			t.Fatalf(
				"R2-MIN-012/D-WALK/EC-11: the outer collection's walk found %q, a file inside a "+
					"folder that carries its OWN .obsidian marker (imported-vault/.obsidian/) — the walk "+
					"must stop descending at any directory that itself hosts a nested-vault marker, "+
					"other than the collection root it started from. Files found: %v",
				f, res.Files,
			)
		}
	}
	found := false
	for _, f := range res.Files {
		if f == "outer.view" {
			found = true
		}
	}
	require.True(t, found, "the outer collection's own top-level file must still be found (sanity check)")
}
