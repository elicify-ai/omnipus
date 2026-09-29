// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-036, TDD Plan
// row 85's agent-path clause (va-qa3 dispatch, P4 — REST half is
// rest_library_trash_revoke_first_red_test.go, pkg/gateway).
//
// Requirement: the knowledge_restructure trash op revokes memberships
// BEFORE trashing.
//
// Verified by reading (2026-09-29): execTrash
// (pkg/knowledge/knowledge_restructure.go) touches only the text/properties
// index (bumpIndexEpochOrWarn, RemoveFromIndexesForFolderTrash /
// removeFromIndexesForNote) — nothing membership-related, because no
// pipeline-owned membership record exists anywhere in the codebase to
// revoke from (same finding this dispatch's other P3/P4 files document).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeRestructureTrash_RevokesMembershipsBeforeTrashing$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

func TestKnowledgeRestructureTrash_RevokesMembershipsBeforeTrashing(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-036 requires knowledge_restructure's trash op to revoke memberships " +
		"before trashing — execTrash (pkg/knowledge/knowledge_restructure.go) touches only the " +
		"text/properties index today (bumpIndexEpochOrWarn, RemoveFromIndexesForFolderTrash/" +
		"removeFromIndexesForNote), confirmed by reading it in full — nothing membership-related, " +
		"because no membership record exists to revoke from.")
}
