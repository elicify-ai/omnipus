// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 61, 64, 66, 67 and 72. All five are BLOCKED per the qa-lead RED protocol.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_RepairsDuplicateRejectedName$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_UpsertRefusesOnMovedTargetUnderLock$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDiscovery_ReadIsOneNoFollowOperationNotTwoSteps$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeDescribe_ReportsSkippedUnreadableSubfolderOnEverySurface$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDiscovery_OperationCountWithin100kFixture$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

// TestWriteView_RepairsDuplicateRejectedName is TDD Plan test 61
// (R2-MIN-005, FR-VA-020a): write_view/delete_view given a `name` that
// resolves to nothing in the current ViewSet must ALSO check the discovery
// result's rejection report for that name before falling to ordinary
// "no such view"/"create new" behavior — a match refuses (never silently
// creates a third colliding file) and names the colliding paths.
//
// BLOCKED: this is a round-2-new repair path with no discovery-anywhere
// rejection report to consult yet (LoadViews only ever scans the fixed
// ViewsDir, TDD test 1/2's own finding) — confirmed by reading
// execWriteView/execDeleteView in full: neither references
// ViewLoadReport.Rejections/RejectedNames at all today, only the loaded
// ViewSet's Get/Resolve.
func TestWriteView_RepairsDuplicateRejectedName(t *testing.T) {
	t.Fatal("BLOCKED: neither execWriteView nor execDeleteView consults the discovery result's " +
		"rejection report for a name match — required before a repair-path refusal naming the " +
		"colliding paths can be shown, per FR-VA-020a / TDD test 61.")
}

// TestWriteView_UpsertRefusesOnMovedTargetUnderLock is TDD Plan test 64
// (R2-MIN-002, FR-VA-024a, SC-VA-008): write_view's upsert must, under
// D-LOCK's lock and BEFORE writing, re-verify the target path still exists
// and still parses to the same name — a mismatch refuses, naming the
// discrepancy, rather than recreating the file at a stale location.
//
// BLOCKED: write_view has no SourcePath-based targeting at all today (TDD
// test 19's own finding — it always writes to the deterministic
// ViewsDir(root)/<name>.yaml path), so there is no "target" to re-verify
// under a lock in the first place, and no re-verify-under-lock step exists
// in execWriteView (confirmed by reading its full body).
func TestWriteView_UpsertRefusesOnMovedTargetUnderLock(t *testing.T) {
	t.Fatal("BLOCKED: write_view has no SourcePath-based upsert targeting (D-WRITE-IDENTITY, TDD " +
		"test 19) and no re-verify-under-lock step — required before a moved-target-during-the-race " +
		"refusal can be shown, per FR-VA-024a / TDD test 64.")
}

// TestDiscovery_ReadIsOneNoFollowOperationNotTwoSteps is TDD Plan test 66
// (R2-MIN-001, FR-VA-022/023): the discovery helper's containment check,
// symlink re-check and byte read must happen in ONE function call with no
// gap a second process can win — and a file that grows past 256 KiB between
// the walk and the read must still be capped.
//
// BLOCKED: the discovery helper itself (pkg/knowledge.DiscoverViewFiles,
// spec §4 step 1) does not exist yet (`grep -rn "DiscoverViewFiles"
// pkg/knowledge` — zero matches), so there is no unified read operation to
// exercise this race against at all.
func TestDiscovery_ReadIsOneNoFollowOperationNotTwoSteps(t *testing.T) {
	t.Fatal("BLOCKED: the pkg/knowledge discovery helper (DiscoverViewFiles, §4 step 1) does not " +
		"exist — required before the containment-check/symlink-recheck/read can be shown to happen " +
		"as one operation with no TOCTOU gap, per FR-VA-022/023 / TDD test 66.")
}

// TestKnowledgeDescribe_ReportsSkippedUnreadableSubfolderOnEverySurface is
// TDD Plan test 67 (R2-MIN-007, FR-VA-025): knowledge_find, the Library
// entries listing, and both rest_knowledge_views.go/
// rest_knowledge_base_views.go must ALL surface a SkipUnreadable entry from
// the discovery walk, not only knowledge_describe/knowledge_configure.
//
// BLOCKED: today's LoadViews does a single os.ReadDir of one fixed
// directory (no subfolder walk, no WalkContained integration at all — TDD
// test 1's own finding), so there is no SkipUnreadable-reporting concept in
// the view-loading path for ANY surface to propagate yet.
func TestKnowledgeDescribe_ReportsSkippedUnreadableSubfolderOnEverySurface(t *testing.T) {
	t.Fatal("BLOCKED: LoadViews does not walk subfolders at all (no WalkContained integration) — " +
		"there is no SkipUnreadable entry produced anywhere in the view-loading path for " +
		"knowledge_find, the Library entries listing, or rest_knowledge_views.go/" +
		"rest_knowledge_base_views.go to surface, per FR-VA-025 / TDD test 67.")
}

// TestDiscovery_OperationCountWithin100kFixture is TDD Plan test 72
// (R2-MIN-004, FR-VA-018, SC-VA-004): over the repo's standing 100,000-file
// benchmark fixture, discovery must perform exactly one WalkContained pass
// and at most one open+read per discovered `.view` file — the deterministic,
// CI-runnable form of the bound BenchmarkViewDiscovery_100kFiles (test 17)
// measures but cannot itself gate on.
//
// BLOCKED: the discovery helper this instrumented count would wrap
// (pkg/knowledge.DiscoverViewFiles, §4 step 1) does not exist, and neither
// does a 100,000-file benchmark fixture for it — there is nothing to
// instrument or count yet.
func TestDiscovery_OperationCountWithin100kFixture(t *testing.T) {
	t.Fatal("BLOCKED: the pkg/knowledge discovery helper (DiscoverViewFiles, §4 step 1) and the " +
		"100,000-file benchmark fixture do not exist — required before a deterministic " +
		"one-walk/at-most-one-read-per-file operation count can be asserted, per FR-VA-018 / " +
		"SC-VA-004 / TDD test 72.")
}
