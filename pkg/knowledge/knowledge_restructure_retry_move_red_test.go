// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-032, agent
// surface argument validation (va-qa3 dispatch, P3 addendum — team-lead
// request 2026-09-29, added after the rest of P3 was written).
//
// Requirement (team-lead, 2026-09-29): the agent tool knowledge_restructure
// op "retry_move" given "collection", "path", or any argument besides
// "pending_move_id" must return a CLEAR tool error ("retry_move takes only
// pending_move_id") and change NOTHING.
//
// FINDING (reported, not silently resolved): "retry_move" is not one of
// knowledge_restructure's ops at all. Read in full
// (pkg/knowledge/knowledge_restructure.go):
//
//	var restructureOps = []string{restructureOpRename, restructureOpMove,
//	    restructureOpTrash, restructureOpRestore}
//
// — exactly {rename, move, trash, restore}. There is no
// restructureOpRetryMove constant, no case for it in Execute's op-dispatch
// switch, and Parameters() advertises no "pending_move_id" argument at all.
// Calling Execute with op: "retry_move" today hits the tool's generic
// "unsupported op" refusal for an op string outside restructureOps — NOT
// the argument-validation behavior the team-lead is asking for, because
// there is no retry_move op yet for an argument-validation rule to attach
// to. BLOCKED per the qa-lead RED protocol (a spec element the dispatch
// cites — an agent-invocable retry_move op — that does not exist anywhere
// in the codebase); this is the SAME root cause
// rest_library_retry_move_red_test.go's header documents for the REST side
// (no retry-move route/handler/op/record exists anywhere).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructureRetryMove_RejectsExtraArgumentsAndChangesNothing$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

// TestKnowledgeRestructureRetryMove_RejectsExtraArgumentsAndChangesNothing
// documents the team-lead's 2026-09-29 addendum to FR-VA-032/P3 as BLOCKED:
// see this file's header — "retry_move" is not a member of restructureOps,
// so there is no op-specific argument-validation branch ("takes only
// pending_move_id") for a test to invoke, let alone observe leaving
// filesystem/record state unchanged.
func TestKnowledgeRestructureRetryMove_RejectsExtraArgumentsAndChangesNothing(t *testing.T) {
	t.Fatal("BLOCKED: knowledge_restructure has no \"retry_move\" op at all (restructureOps is " +
		"exactly {rename, move, trash, restore} — pkg/knowledge/knowledge_restructure.go) — there is " +
		"no op-specific argument-validation branch to exercise for \"retry_move given collection, " +
		"path, or any argument besides pending_move_id must return a clear tool error ('retry_move " +
		"takes only pending_move_id') and change nothing\" (team-lead request, 2026-09-29). Calling " +
		"Execute with op: \"retry_move\" today hits the tool's generic unsupported-op refusal, not " +
		"the requested argument-shape validation, because the op itself does not exist. Same missing " +
		"seam as the REST side (rest_library_retry_move_red_test.go's header).")
}
