// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), combined-brief
// Priority 1b: revoke-before-rename counterexamples (from GREEN-security,
// approved by team-lead). Follow-up issue for full crash atomicity: #1042.
//
// THE PROPERTY UNDER TEST: when a managed view's identity changes (a rename
// or move of the view itself, or of the `.base`/folder that manages it), the
// pipeline-owned membership record must be updated to REVOKE the OLD path
// BEFORE the physical rename/move happens on disk — never the other order.
// If the save that persists the revoked record fails AFTER the physical
// rename already landed, the record must still not claim the OLD path (the
// revocation itself must have been durable before the rename, or the whole
// operation must roll back) — otherwise a stale record entry for `old.view`
// could later cause a re-derivation to delete or overwrite whatever a THIRD
// PARTY has since placed at that now-vacated path (a planted copy, or an
// unrelated file), which is exactly the R2-CRIT-001/R2-CRIT-002 attack shape
// this spec's D-PROVENANCE section exists to close.
//
// ROOT CAUSE, VERIFIED (same one D-PROVENANCE's other BLOCKED tests in
// pkg/vaultimport cite): there is no pipeline-owned membership record
// anywhere in the tree to revoke, no "record store" write seam to inject a
// failure into, and pkg/knowledge/rename.go's Renamer has no `.base`-file
// provenance branch at all (grep for ".base" across rename.go returns zero
// hits — confirmed by pkg/gateway's own test-50/51 BLOCKED comment, which
// this file does not duplicate). The nearest real entry point is
// (*Renamer).Rename itself — it renames/moves the file and rewrites inbound
// wikilinks/embeds, nothing else; there is no order of "revoke, then
// rename" to observe because there is no revoke step to order at all.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRenamer_RevokesRecordBeforePhysicalRenameOnInjectedFailure$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRenamer_RevokeBeforeRename_BaseMoveAndFolderMove$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_StaleRecordAfterFailedSaveNeverDeletesOrOverwrites$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

// TestRenamer_RevokesRecordBeforePhysicalRenameOnInjectedFailure is
// combined-brief Priority 1b item 1. Scenario: a managed view's enrollment
// save is forced to FAIL after the physical rename, via an injected write
// error on the record store at the seam the spec or GREEN code provides.
// Required outcome:
//   - the persisted record does NOT name old.view (the revocation was
//     persisted BEFORE the rename, or the whole step rolled back);
//   - a file later COPIED to old.view (same name and derived_from as the
//     original) is never deleted or overwritten by the next re-derivation of
//     the .base with that view's declaration removed;
//   - the "incomplete" error is visible at the entry point that returns it.
//
// BLOCKED: there is no record store to inject a write failure into (no
// membership record exists at all — see file header), so "the seam the spec
// or GREEN code provides" does not exist either. The nearest real entry
// point, (*Renamer).Rename, has no `.base`-provenance branch, so it cannot
// even be driven into the scenario this test names — calling it exercises
// wikilink-rewrite only, never a view's provenance record, and there is no
// "incomplete" error return anywhere on this path to make visible.
func TestRenamer_RevokesRecordBeforePhysicalRenameOnInjectedFailure(t *testing.T) {
	t.Fatal("BLOCKED: the pipeline-owned membership record (D-PROVENANCE §2) and its persistence seam do " +
		"not exist anywhere under pkg/knowledge, pkg/vaultimport or pkg/records — there is no record to " +
		"revoke, no write seam to inject a failure into, and (*Renamer).Rename (the nearest real entry " +
		"point for a rename/move) has no .base-file provenance branch at all (confirmed by grep — zero " +
		"'.base' hits in rename.go). Required before a forced enrollment-save failure after a rename can " +
		"be shown to have already persisted the revocation, and before a planted copy at the vacated old " +
		"path can be shown to survive re-derivation byte-for-byte, per combined-brief Priority 1b item 1.")
}

// TestRenamer_RevokeBeforeRename_BaseMoveAndFolderMove is combined-brief
// Priority 1b item 2: the SAME revoke-before-rename property, for (a) a
// direct `.base` move and (b) a same-collection FOLDER move, once with the
// managed view INSIDE the moved folder and once OUTSIDE it.
//
// BLOCKED: identical root cause to the test above — no membership record,
// no revoke step, and (*Renamer).Rename's real folder-move path (verified:
// it walks and rewrites links for every file under the moved folder, per
// its own doc comment) has the same total absence of a `.base`-provenance
// branch, so there is nothing to order "revoke, then physical move" against
// for either the .base-move or the folder-move shape.
func TestRenamer_RevokeBeforeRename_BaseMoveAndFolderMove(t *testing.T) {
	t.Fatal("BLOCKED: same root cause as TestRenamer_RevokesRecordBeforePhysicalRenameOnInjectedFailure — " +
		"no pipeline-owned membership record and no .base-provenance branch in (*Renamer).Rename for " +
		"either a direct .base move or a same-collection folder move (with the managed view inside or " +
		"outside the moved folder) — required before the revoke-before-move ordering can be exercised " +
		"for either shape, per combined-brief Priority 1b item 2.")
}

// TestRederive_StaleRecordAfterFailedSaveNeverDeletesOrOverwrites is
// combined-brief Priority 1b item 3: a stale record left behind by a failed
// save can never delete or overwrite anything.
//
// BLOCKED: there is no record, stale or otherwise, to leave behind — same
// root cause.
func TestRederive_StaleRecordAfterFailedSaveNeverDeletesOrOverwrites(t *testing.T) {
	t.Fatal("BLOCKED: the pipeline-owned membership record (D-PROVENANCE §2) does not exist, so there is " +
		"no stale-record state to construct or exercise — required before a stale record after a failed " +
		"save can be shown to never delete or overwrite anything, per combined-brief Priority 1b item 3.")
}
