// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), architect ruling
// 2026-09-29 (external moves) / Dataset F-10.
//
// A raw COPY (not a move) of a managed view — planted by something other
// than an Omnipus-mediated copy/restore/upload, e.g. a sync client or a
// person using the OS file manager — produces two candidates sharing the
// same `name`. Per D-DUPLICATE/records.LoadViews's existing dedup mechanism
// (`byName`, unchanged by this spec), Dataset F-10 disposes of that as
// "neither touched": no file may be picked as "the" managed one on
// ambiguous evidence.
//
// VERDICT: characterization, not a gap — this test PASSES today, verified
// empirically rather than assumed. Two files sharing `name: projects--open`
// are both rejected by the EXISTING, unchanged RejectViewDuplicateName
// mechanism the instant records.LoadViews (which RederiveBase calls first,
// to compute "mine") loads them, so NEITHER appears in the loaded ViewSet —
// re-derivation's own "mine" filter can never pick either one up. The
// original's bytes surviving is additionally, separately, a coincidence of
// fileTranslatedBase's own byte-identical short-circuit (the schema and
// .base are unchanged between runs, so the freshly-translated bytes match
// what is already on disk) rather than membership-record protection, and
// the copy in archive/ survives only because re-derivation never touches
// any path outside records.ViewsDir at all today — not because F-10's rule
// is implemented. Pinned here, exactly as run_import_collision_red_test.go's
// own characterization test states its own reasoning, so CHECK's mutation
// pass can verify the assertion watches something (kill the shared dedup or
// the byte-identical short-circuit and this test must die) rather than
// assume nothing here is exercised.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_TwoRawCopiesOfManagedViewBothUntouched$' ./pkg/vaultimport/
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

// TestRederive_TwoRawCopiesOfManagedViewBothUntouched is the architect's
// 2026-09-29 external-moves ruling's new test (Dataset F-10): a raw copy
// (NOT an Omnipus-mediated one, and not a move) of a managed view sits
// beside the original. The next re-derivation of the managing .base must
// touch NEITHER file — no rewrite, no delete, for either.
func TestRederive_TwoRawCopiesOfManagedViewBothUntouched(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	first, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, first.Status, "reason: %s", first.RefusedReason)
	require.Contains(t, first.Written, "projects--open")

	originalPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	originalBytesBefore, rerr := os.ReadFile(originalPath)
	require.NoError(t, rerr)

	// A RAW copy — same `name`, same bytes, planted by something other than
	// an Omnipus copy/restore/upload action (D-DUPLICATE's auto-suffix/strip
	// step never runs for this, by construction: it is not one of the four
	// write paths D-DUPLICATE covers).
	archiveDir := filepath.Join(root, "archive")
	require.NoError(t, os.MkdirAll(archiveDir, 0o755))
	copyPath := filepath.Join(archiveDir, "projects--open-raw-copy.yaml")
	require.NoError(t, os.WriteFile(copyPath, originalBytesBefore, 0o600))

	second, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, second.Status, "reason: %s", second.RefusedReason)

	originalBytesAfter, rerr := os.ReadFile(originalPath)
	require.NoError(t, rerr, "the original must still exist")
	if string(originalBytesAfter) != string(originalBytesBefore) {
		t.Errorf(
			"Dataset F-10: the original managed view at %s was rewritten after a raw copy of it "+
				"appeared elsewhere — a sync-conflict shape (two files sharing name+provenance) must "+
				"leave BOTH untouched, not just the copy",
			originalPath,
		)
	}

	copyBytesAfter, rerr := os.ReadFile(copyPath)
	require.NoError(t, rerr, "the raw copy must still exist")
	if string(copyBytesAfter) != string(originalBytesBefore) {
		t.Errorf(
			"Dataset F-10: the raw copy at %s was modified by re-derivation of the .base it happens to "+
				"share provenance with — no file may be picked as \"the\" managed one on ambiguous evidence",
			copyPath,
		)
	}

	for _, slug := range second.Deleted {
		if slug == "projects--open" {
			t.Fatalf("Dataset F-10: re-derivation reported \"projects--open\" as Deleted=%v while a raw "+
				"copy of it exists — neither the original nor the copy may be deleted on ambiguous evidence",
				second.Deleted)
		}
	}
}
