// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 53, 54 and 55. All three are BLOCKED per the qa-lead RED protocol.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestFolderCopy_AutoRenamesAndStripsProvenanceForEveryView$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestFolderRestore_AutoRenamesForEveryView$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryEntries_ViewCollectionIdMatchesBaseViewsCollectionId$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

// TestFolderCopy_AutoRenamesAndStripsProvenanceForEveryView is TDD Plan test
// 53 (R2-MAJ-007, FR-VA-019, Dataset G-6): copying a folder holding N views
// (some derived) through the real recursive `library.CopyInto` walk must
// auto-suffix every colliding name and strip `derived_from` where present,
// for every view the walk writes — not merely the single-file case.
//
// BLOCKED: library.CopyInto's full body (pkg/library/transfer.go) has no
// `.view`/`.base` special-casing at all — confirmed by direct read
// (copyFile/copyDirRecursive copy bytes verbatim) — and the planned shared
// per-file rewrite helper (pkg/records::RewriteCopiedViewIdentity, D-DUPLICATE)
// does not exist anywhere (`grep -rn "RewriteCopiedViewIdentity" pkg/` —
// zero matches). This is the same root cause as TDD test 27
// (TestLibraryCopy_AutoRenamesCollidingViewName), extended to the recursive
// folder case.
func TestFolderCopy_AutoRenamesAndStripsProvenanceForEveryView(t *testing.T) {
	t.Fatal("BLOCKED: library.CopyInto has no per-file view-identity rewrite step at all (single-file " +
		"or recursive), and the planned shared helper (pkg/records::RewriteCopiedViewIdentity, " +
		"D-DUPLICATE) does not exist — required before a recursive folder copy of N views can be " +
		"shown to auto-suffix and strip provenance for every one, per FR-VA-019 / Dataset G-6 / " +
		"TDD test 53.")
}

// TestFolderRestore_AutoRenamesForEveryView is TDD Plan test 54 (R2-MAJ-007,
// FR-VA-019, Dataset G-7): restoring a trashed folder of views through the
// agent tool's real `restoreFolder` recursive walk must produce the same
// per-file auto-rename result as the single-file restore case.
//
// BLOCKED: same root cause as test 53 — restoreFolder
// (pkg/knowledge/knowledge_restructure_trash_folder.go) has no per-file
// view-identity rewrite step, and the shared helper it would need to call
// does not exist.
func TestFolderRestore_AutoRenamesForEveryView(t *testing.T) {
	t.Fatal("BLOCKED: restoreFolder (pkg/knowledge/knowledge_restructure_trash_folder.go) has no " +
		"per-file view-identity rewrite step, and the planned shared helper " +
		"(pkg/records::RewriteCopiedViewIdentity, D-DUPLICATE) does not exist — required before a " +
		"recursive folder restore of N views can be shown to auto-suffix every one, per FR-VA-019 / " +
		"Dataset G-7 / TDD test 54.")
}

// TestLibraryEntries_ViewCollectionIdMatchesBaseViewsCollectionId is TDD
// Plan test 55 (R2-CRIT-003, FR-VA-009, SC-VA-013): a `.view` entry's
// `view.collection_id` must equal the SAME collection's
// `KnowledgeBaseViews.collection_id` for a `.base` file, byte-for-byte —
// both are meant to be produced by the SAME pure function,
// knowledgeCollectionID.
//
// BLOCKED: `LibraryEntry` has no `is_view`/`view` field at all (confirmed:
// `src/lib/api/generated/openapi-types.ts`'s LibraryEntry schema block has
// none), so there is no `view.collection_id` on the Go side either
// (generated.LibraryEntry has no such field, since it is generated from the
// same contract) — there is nothing yet to compare against
// KnowledgeBaseViews.collection_id.
func TestLibraryEntries_ViewCollectionIdMatchesBaseViewsCollectionId(t *testing.T) {
	t.Fatal("BLOCKED: LibraryEntry has no is_view/view field, so view.collection_id does not exist " +
		"anywhere — required before it can be compared, byte-for-byte, against " +
		"KnowledgeBaseViews.collection_id for the same collection, per FR-VA-009 / SC-VA-013 / " +
		"TDD test 55.")
}
