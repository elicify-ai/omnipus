// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-031, TDD Plan
// row 76's agent-path clause (va-qa3 dispatch, P2).
//
// FR-VA-031 states the agent surface as: "agent move/rename paths
// (knowledge_restructure rename/move ...) return the same refusal" as the
// REST cross-collection transfer refusal (view_tracked_transfer_refused).
//
// FINDING (reported, not silently resolved): knowledge_restructure's "move"
// op has NO destination-collection parameter at all. Read in full
// (pkg/knowledge/knowledge_restructure.go):
//   - Parameters(): "op", "collection", "path", "new_name", "new_folder",
//     "allow_ambiguity", "trashed_at", "folder", "pending_move_id" — one
//     "collection" argument, never a destination collection.
//   - execRenameMove resolves `to` as
//     path.Join(normalizeMoveFolder(new_folder), newName) and then opens
//     `NewCollectionRoot(OSLinkFS(), target.col.Root)` — target.col is the
//     SAME collection `begin()` resolved from the single "collection"
//     argument. The destination is JOINED AND ROOTED inside that one
//     collection; there is no code path, argument, or combination of
//     arguments that can address a second, different collection.
//
// This means the "agent path gives the same refusal" claim cannot be
// exercised as a genuine cross-collection transfer at all — the tool
// structurally cannot attempt one, so there is no such capability's
// refusal behavior to test. BLOCKED per the qa-lead RED protocol (a spec
// element the brief cites — an agent-invocable cross-collection move — that
// does not exist anywhere in the codebase).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructureMove_HasNoCrossCollectionDestinationCapability$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

// TestKnowledgeRestructureMove_HasNoCrossCollectionDestinationCapability
// documents TDD Plan row 76's agent-path clause (FR-VA-031) as BLOCKED: see
// this file's header for the read-in-full finding that knowledge_restructure
// has no argument or code path capable of naming a destination collection
// different from its single "collection" argument, so there is no
// cross-collection move for a view_tracked_transfer_refused-style refusal to
// apply to.
func TestKnowledgeRestructureMove_HasNoCrossCollectionDestinationCapability(t *testing.T) {
	t.Fatal("BLOCKED: knowledge_restructure's move op (pkg/knowledge/knowledge_restructure.go:: " +
		"execRenameMove) takes a single 'collection' argument and resolves its destination inside " +
		"that SAME collection root (new_folder joins within target.col.Root) — there is no " +
		"destination-collection parameter anywhere in Parameters() or execRenameMove, so the tool " +
		"cannot even attempt a cross-collection move. FR-VA-031's claim that 'the agent " +
		"knowledge_restructure move gives the same refusal' as the REST cross-KB transfer refusal " +
		"(row 76) has no corresponding capability to test until such a parameter/path is added.")
}
