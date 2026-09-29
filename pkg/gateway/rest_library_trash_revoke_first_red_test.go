// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-036, TDD Plan
// row 85 (va-qa3 dispatch, P4, team-lead ruling 2026-09-29).
//
// Requirement: Library folder trash AND the knowledge_restructure trash op
// revoke memberships FIRST, then trash — never using the pending-move plan
// or Retry. Test arc: revocation persisted (views released) -> trash fails
// (injected) -> response is a visible `trash_incomplete` naming the paths
// (REST: 409 LibraryMoveConflictError code trash_incomplete on
// DELETE /library/{workspace_id}/entries) -> trashing again succeeds
// idempotently. ALSO a nested-root folder trash: the subtree contains
// nested KB roots; the op takes sorted locks on the enclosing root and
// every nested root, and revokes memberships in ALL affected roots BEFORE
// trashing; recorded views inside the trashed folder are released even
// when their `.base` lives outside it. Assert NO authority remains at ANY
// point: a copy planted at a trashed path is never deleted or rewritten by
// a later `.base` save.
//
// Verified by reading (2026-09-29): handleLibraryEntryDelete
// (pkg/gateway/rest_library_write.go, full function read) has ZERO
// `.base`/view-membership awareness — a governed note/folder is routed to
// trashNoteInCollection with no revoke step; an ungoverned path just calls
// root.Delete. mapLibraryErr never emits a `trash_incomplete` code (its
// switch only knows library.Err* sentinels). There is no subtree-walk
// helper for NESTED knowledge-base roots anywhere (detectKnowledgeBaseInRoot
// answers only "is this ONE path a KB", never a recursive subtree walk;
// confirmed by grep for "nested.*[Rr]oot"/"discoverNested" — only
// ErrNestedKnowledgeBase, a KB-inside-KB CREATION refusal, unrelated).
// knowledge_restructure's execTrash (pkg/knowledge/knowledge_restructure.go)
// touches only the text/properties index (bumpIndexEpochOrWarn,
// RemoveFromIndexesForFolderTrash) — nothing membership-related. There is
// no pipeline-owned membership record anywhere (same finding every other
// P2-P4 test file in this dispatch documents).
//
// BLOCKED, every case, for the SAME root cause: there is no revoke step at
// all (nothing to persist "views released", nothing to inject a failure
// AFTER), no trash_incomplete code path, and no nested-root subtree-walk —
// none of the fixtures below can be constructed against real code.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTrash_RevokesBeforeTrashingAndReportsTrashIncompleteOnInjectedFailure$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTrash_RetryingAfterTrashIncompleteSucceedsIdempotently$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTrash_NestedRootFolderTakesSortedLocksAndRevokesAllRootsBeforeTrashing$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTrash_NoAuthorityRemainsAtAnyPointForACopyPlantedAtTheTrashedPath$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestLibraryTrash_RevokesBeforeTrashingAndReportsTrashIncompleteOnInjectedFailure(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-036 needs a real revoke-memberships-then-trash pipeline to inject a " +
		"trash-step failure into and observe a 409 LibraryMoveConflictError code trash_incomplete — " +
		"handleLibraryEntryDelete has zero .base/view-membership awareness at all (confirmed by " +
		"reading pkg/gateway/rest_library_write.go in full: a governed path goes straight to " +
		"trashNoteInCollection, no revoke step exists), and mapLibraryErr never emits trash_incomplete " +
		"for any error. There is no revoke step to persist 'views released' before, and no failure " +
		"injection seam.")
}

func TestLibraryTrash_RetryingAfterTrashIncompleteSucceedsIdempotently(t *testing.T) {
	t.Fatal("BLOCKED: this needs the SAME missing trash_incomplete outcome above to exist first — " +
		"there is no incomplete-trash state to retry against, since no revoke-then-trash pipeline " +
		"exists at all.")
}

func TestLibraryTrash_NestedRootFolderTakesSortedLocksAndRevokesAllRootsBeforeTrashing(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-036's nested-root case needs (a) a subtree-walk that discovers every " +
		"nested knowledge-base root inside a trashed folder — no such helper exists anywhere " +
		"(detectKnowledgeBaseInRoot only answers 'is this ONE path a KB', confirmed by grep for " +
		"nested-root/discovery helpers — zero hits beyond the unrelated ErrNestedKnowledgeBase " +
		"creation-refusal check), (b) sorted locks across all affected roots — no membership lock " +
		"exists (see FR-VA-038's finding), and (c) a revoke step per root — none exists. There is " +
		"nothing to plant a 'recorded view outside the trashed folder, .base inside it' fixture " +
		"against.")
}

func TestLibraryTrash_NoAuthorityRemainsAtAnyPointForACopyPlantedAtTheTrashedPath(t *testing.T) {
	t.Fatal("BLOCKED: asserting 'a copy planted at a trashed path is never deleted or rewritten by a " +
		"later .base save' presupposes a working revoke-then-trash pipeline whose trashed-path " +
		"authority state could be inspected at each step — none of that pipeline exists (see this " +
		"file's header). There is no authority-tracking concept to assert 'none remains' about.")
}
