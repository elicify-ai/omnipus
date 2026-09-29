// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-039, TDD Plan
// rows 88, 89 (va-qa3 dispatch, P4, team-lead ruling 2026-09-29).
//
// Oracle, verbatim: "Permanently deleting a knowledge base's ROOT folder
// (the plain root.Delete path) and permanently deleting its marker folder
// (.obsidian / .omnipus-vault — KB demotion: the folder stops being a
// knowledge base) MUST both remove that collection's outside-vault
// membership record (view_membership.json entry), including its pending
// moves and completed receipts, under the dedicated membership lock
// (FR-VA-038). If that removal fails, the delete/demotion itself fails
// with a visible error; it never succeeds while leaving the record
// behind." Row 88: delete the KB root, recreate a KB at the SAME path,
// plant a view with the same name and derived_from; assert NO authority
// (not derived, not read-only, no managed rewrite). Row 89: same via
// marker-folder delete + recreate; plus inject a record-removal failure
// and assert the delete fails visibly and the root/marker is still there.
//
// Verified by reading (2026-09-29): both delete paths go through the SAME
// handleLibraryEntryDelete (pkg/gateway/rest_library_write.go) — a plain
// KB-root delete is just `root.Delete(rel)`; a marker-folder delete
// additionally calls releaseKnowledgeBaseIfDemoted (rest_library_knowledge_
// cascade.go), which only calls ReleaseDemotedCollection — an in-memory
// KnowledgeLifecycle.byRoot unregister (stops indexing), NOT a membership-
// record removal. There is no DemoteKnowledgeBase/RemoveMarker symbol
// (confirmed by grep). There is no pipeline-owned membership record
// anywhere (zero hits for "ViewMembership"/"view_membership" outside
// comments/generated spec-prose — the SAME finding this dispatch's other
// P3/P4 files document) — so there is nothing for either delete path to
// remove, no membership lock to remove it under (FR-VA-038, also absent),
// and no removal-failure seam to inject a failure into for row 89's
// second half.
//
// BLOCKED per the qa-lead RED protocol: both rows depend on a membership
// record, and its removal-on-delete step, that do not exist anywhere.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKBRootDelete_RemovesMembershipRecordSoRecreatedKBHasNoAuthority$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestMarkerFolderDelete_RemovesMembershipRecordAndFailsVisiblyIfRemovalFails$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestKBRootDelete_RemovesMembershipRecordSoRecreatedKBHasNoAuthority(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-039/row 88 needs a real membership record whose removal (on KB-root " +
		"delete) could be asserted, then a recreated KB at the SAME path with a same-name/" +
		"same-derived_from view checked for NO authority — no such record exists anywhere in the " +
		"codebase (confirmed by grep), and handleLibraryEntryDelete's plain root.Delete path has no " +
		"membership-record-removal step to observe (see this file's header). There is nothing to " +
		"plant the 'recreate at same path' fixture against.")
}

func TestMarkerFolderDelete_RemovesMembershipRecordAndFailsVisiblyIfRemovalFails(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-039/row 89 needs the SAME missing membership record above, PLUS a " +
		"removal-failure injection seam whose failure must make the marker-folder delete/demotion " +
		"itself fail visibly — releaseKnowledgeBaseIfDemoted only calls ReleaseDemotedCollection " +
		"(an in-memory index-lifecycle unregister), confirmed by reading it in full; there is no " +
		"membership-record removal step there to inject a failure into, and no membership lock " +
		"(FR-VA-038) to remove it under.")
}
