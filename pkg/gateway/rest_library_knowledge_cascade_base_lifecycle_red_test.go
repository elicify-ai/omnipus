// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 50 and 51.
//
// Oracle: FD-7 / D-PROVENANCE's ".base lifecycle" subsection, resolving
// R2-MAJ-002. Renaming or moving a `.base` file through the Library's real
// rename/move handler MUST rewrite `derived_from` (and `source`) on every
// view in that `.base`'s membership record to the NEW path, in the same
// operation (FR-VA-008f, Dataset F-11). Deleting a `.base` file MUST clear
// `derived_from` on every view in its membership record — never trash or
// delete those views — and remove the record, releasing them to ordinary
// hand-made status (FR-VA-008g, Dataset F-12).
//
// Today pkg/knowledge/rename.go's Renamer (invoked by
// pkg/gateway/rest_library_knowledge_cascade.go::renameNoteInCollection) only
// rewrites inbound wikilinks/embeds — it has no `.base`-file branch and no
// notion of a view it "manages." There is no `derived_from` field and no
// pipeline-owned membership record anywhere in pkg/knowledge, pkg/vaultimport
// or pkg/gateway (grep -rn "derived_from\|membership record" across all
// three packages returns zero matches for the concept). Both tests below are
// BLOCKED per the qa-lead RED protocol: renaming/deleting a `.base` through
// the real HTTP handlers today simply never touches any view's provenance,
// because that provenance does not yet exist to touch.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryKnowledgeCascade_RenameUpdatesDerivedFromOnManagedViews$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

// TestLibraryKnowledgeCascade_RenameUpdatesDerivedFromOnManagedViews is TDD
// Plan test 50 (FD-7, R2-MAJ-002, FR-VA-008f, Dataset F-11): renaming or
// moving a `.base` through the real Library rename handler
// (POST /api/v1/library/{ws}/rename, which drives knowledge.Renamer) must
// rewrite `derived_from` (and `source`) on every managed view to the .base's
// new path, in the same operation, and update the membership record's key.
//
// BLOCKED: knowledge.Renamer has no `.base`-file branch at all today (it
// only rewrites wikilinks/embeds in markdown), and there is no
// `derived_from` field or membership record for it to update — verified via
// grep over pkg/knowledge/rename.go and pkg/gateway/rest_library_knowledge_cascade.go
// (no `.base`, no `derived_from`, no "membership" hit).
func TestLibraryKnowledgeCascade_RenameUpdatesDerivedFromOnManagedViews(t *testing.T) {
	t.Fatal("BLOCKED: knowledge.Renamer (pkg/knowledge/rename.go) has no .base-file provenance branch, " +
		"and ViewDef.derived_from / the pipeline-owned membership record (D-PROVENANCE) do not exist " +
		"— required before a real Library rename/move of a .base file can be shown to rewrite " +
		"derived_from (and source) on every managed view to the new path, per FR-VA-008f / " +
		"Dataset F-11 / TDD test 50.")
}

// TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews is TDD Plan test 51
// (FD-7, R2-MAJ-002, FR-VA-008g, Dataset F-12): deleting a `.base` through
// the real Library delete/trash path must clear `derived_from` on every view
// in its membership record (never trash or delete those views themselves)
// and remove the membership record — releasing each view to ordinary
// hand-made status.
//
// BLOCKED: same root cause — there is no `derived_from`/membership record
// for a `.base` delete to release, and no code path in the trash/delete
// handlers that even looks for one.
func TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from and the pipeline-owned membership record (D-PROVENANCE) do " +
		"not exist, and no Library delete/trash path has a release step for a .base's managed views " +
		"— required before deleting a .base can be shown to clear derived_from on every managed view " +
		"and remove the membership record, per FR-VA-008g / Dataset F-12 / TDD test 51.")
}
