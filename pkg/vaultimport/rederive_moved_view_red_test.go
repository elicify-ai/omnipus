// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 22, 23 and 24.
//
// Oracle: FD-3 ("`.base` wins... any edit made to such a view... is
// overwritten the next time its `.base` is saved") plus D-PROVENANCE's round-
// 2 rewrite (a moved managed view must still be found via `derived_from`,
// never duplicated or silently orphaned — US-2 AS-3/AS-4), and FR-VA-008b/
// MIN-009 ("A re-derivation delete step MUST report a view as deleted only
// when the removal actually occurred").
//
// Today RederiveBase (pkg/vaultimport/rederive.go) locates "its own" views by
// re-loading the whole ViewSet and filtering on `v.DeclaredSource() ==
// baseRelPath` (a `source:` string match), then always WRITES to
// `records.ViewsDir(vaultRoot)/<slug>.yaml` and always DELETES at that same
// fixed path — confirmed by reading rederive.go::RederiveBase/
// fileTranslatedBase. There is no `derived_from` field and no persisted
// membership record (grep -rn "derived_from" pkg/vaultimport --include='*.go'
// — zero hits outside unrelated identifier names), so a view moved OUT of
// ViewsDir is invisible to the next re-derivation: the pipeline believes
// nothing is there yet and writes a fresh duplicate (tests 22/23 below), and
// separately, the delete step's `Deleted` report is appended to
// unconditionally even when os.Remove found nothing to remove (test 24 —
// this is a present-day defect the source itself documents as MIN-009 in the
// spec, not a hypothetical).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_FindsMovedManagedViewByDerivedFrom$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_DeletesMovedManagedViewWhenNoLongerDeclared$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_DoesNotReportDeletedWhenAlreadyGone$' ./pkg/vaultimport/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package vaultimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestRederive_FindsMovedManagedViewByDerivedFrom is TDD Plan test 22 /
// Dataset F-3 / US-2 AS-3: a derived view moved by a person to a new
// location must be rewritten IN PLACE at that new location on the next
// re-derivation, never duplicated beside the .base file.
//
// Today's fixed-directory targeting cannot see the moved file at all, so
// re-derivation writes a SECOND file back at the default slug path — the
// exact duplication FD-3/D-PROVENANCE forbids.
func TestRederive_FindsMovedManagedViewByDerivedFrom(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	first, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, first.Status, "reason: %s", first.RefusedReason)
	require.Contains(t, first.Written, "projects--open")

	// A person moves the managed view out of the hidden control directory,
	// into an ordinary collection folder — exactly what "a view lives
	// wherever an agent or human puts it" (US-1) promises is safe to do.
	oldPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	newDir := filepath.Join(root, "archive")
	require.NoError(t, os.MkdirAll(newDir, 0o755))
	newPath := filepath.Join(newDir, "projects--open.yaml")
	body, rerr := os.ReadFile(oldPath)
	require.NoError(t, rerr)
	require.NoError(t, os.WriteFile(newPath, body, 0o600))
	require.NoError(t, os.Remove(oldPath))

	// The .base is re-saved with that view's definition UNCHANGED.
	second, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, second.Status, "reason: %s", second.RefusedReason)

	if _, statErr := os.Stat(oldPath); statErr == nil {
		t.Errorf(
			"US-2 AS-3/FD-3: re-derivation wrote a SECOND file at the default slug path (%s) after the "+
				"managed view was moved to %s — a moved derived view must be found and rewritten IN PLACE "+
				"via its provenance, never duplicated",
			oldPath, newPath,
		)
	}
	if _, statErr := os.Stat(newPath); statErr != nil {
		t.Errorf("the moved file at %s must still exist after re-derivation (rewritten in place), stat error: %v",
			newPath, statErr)
	}
}

// TestRederive_DeletesMovedManagedViewWhenNoLongerDeclared is TDD Plan test
// 23 / Dataset F-4 / US-2 AS-4: the same moved view, once its .base stops
// declaring it, must be deleted at its NEW (moved) location — FD-3's ".base
// wins" surviving a move.
//
// Today's delete step only ever targets the fixed ViewsDir path, so the
// moved file is left on disk untouched, orphaned and undeletable by the
// pipeline that is supposed to own it.
func TestRederive_DeletesMovedManagedViewWhenNoLongerDeclared(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	first, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, first.Status, "reason: %s", first.RefusedReason)

	oldPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	newDir := filepath.Join(root, "archive")
	require.NoError(t, os.MkdirAll(newDir, 0o755))
	newPath := filepath.Join(newDir, "projects--open.yaml")
	body, rerr := os.ReadFile(oldPath)
	require.NoError(t, rerr)
	require.NoError(t, os.WriteFile(newPath, body, 0o600))
	require.NoError(t, os.Remove(oldPath))

	// The .base is edited to DROP the "Open" view entirely.
	edited := `
filters:
  and:
    - type == "widget"
views:
  - type: table
    name: Closed
    filters:
      and:
        - status == "closed"
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "Projects.base"), []byte(edited), 0o644))

	second, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, second.Status, "reason: %s", second.RefusedReason)

	if _, statErr := os.Stat(newPath); statErr == nil {
		t.Errorf(
			"US-2 AS-4/FD-3: %s no longer declares \"Open\", but the moved managed view at %s still exists — "+
				"re-derivation must delete a managed view at its CURRENT (moved) location when the .base "+
				"stops declaring it",
			"Projects.base", newPath,
		)
	}
}

// TestRederive_DoesNotReportDeletedWhenAlreadyGone is TDD Plan test 24 /
// Dataset F-5 / FR-VA-008b (MIN-009): a managed view's file already removed
// by some other means before re-derivation's delete step runs must NOT be
// named in the result's Deleted list — Deleted must only ever name a file
// this run actually removed.
//
// This is a present-day defect in fileTranslatedBase's own delete loop
// (verified by reading it): it tolerates os.ErrNotExist from os.Remove (does
// not fail the call) but then unconditionally appends the slug to
// res.Deleted on the very next line, regardless of whether anything was
// actually removed:
//
//	if derr := os.Remove(delPath); derr != nil && !errors.Is(derr, os.ErrNotExist) {
//	        return nil, fmt.Errorf(...)
//	}
//	res.Deleted = append(res.Deleted, slug)   // <- runs even on ErrNotExist
//
// RederiveBase's own "mine" is recomputed from the ViewSet on every call
// (v.DeclaredSource() == baseRelPath over freshly-loaded files), so a file
// deleted before RederiveBase starts is invisible to "mine" and never
// reaches this loop at all — that would make the bug untestable through the
// public entry point. This test instead calls fileTranslatedBase directly
// (same package) with a "mine" slug whose file does not exist on disk,
// exactly the state a genuine TOCTOU race (a person deleting the file, or a
// concurrent os.Remove, between "mine" being computed and this loop running)
// would produce.
func TestRederive_DoesNotReportDeletedWhenAlreadyGone(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	schemaSet, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	schemaIdx := schemaIndexFromSchemas(schemaSet)

	// A .base that declares only "Closed" — "ghost-slug" is not declared, so
	// it goes to the delete loop as a member of "mine" that fileTranslatedBase
	// must reconcile away.
	pb, perr := ParseBaseFile([]byte(`
filters:
  and:
    - type == "widget"
views:
  - type: table
    name: Closed
    filters:
      and:
        - status == "closed"
`))
	require.NoError(t, perr)

	// No file exists at records.ViewsDir(root)/ghost-slug.yaml — simulating
	// a managed view already removed by some other means before this run's
	// delete step executes.
	res, ferr := fileTranslatedBase(root, "Projects.base", pb, schemaIdx, NewSlugRegistry(), []string{"ghost-slug"})
	require.NoError(t, ferr)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	for _, slug := range res.Deleted {
		if slug == "ghost-slug" {
			t.Fatalf(
				"FR-VA-008b/MIN-009: Deleted=%v names %q, but no file existed at that path for this run to "+
					"remove — Deleted MUST only name a file THIS run actually removed",
				res.Deleted, slug,
			)
		}
	}
}
