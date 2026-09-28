// Omnipus — RED items for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 17 and 41.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

// BenchmarkViewDiscovery_100kFiles is TDD Plan test 17 (SC-VA-004, Q2): "a
// wall-clock benchmark... is still run BEFORE landing, as a human-readable
// sanity check and to size D-VIEW-INDEX's cold-cache cost, but it is not
// itself the CI gate" (§12, FR-VA-018). Test 72
// (TestDiscovery_OperationCountWithin100kFixture, already written and
// BLOCKED in knowledge_view_lock_and_discovery_red_test.go) is §10's own
// named deterministic companion — this file supplies the Benchmark row
// itself, not a duplicate of that companion.
//
// The spec's stated bound is a MEASURED number against a real fixture, not
// a number this document invents ("the resulting number becomes an
// absolute CI-checked bound... a wall-clock benchmark... is still run
// BEFORE landing... to size D-VIEW-INDEX's cold-cache cost" — §10 row 17's
// own Description column). There is no discovery helper to benchmark yet
// (pkg/knowledge.DiscoverViewFiles, §4 step 1, does not exist — same root
// cause as test 72), so this Benchmark function is written in the form b.*
// gives a Benchmark for stating that fact loudly rather than silently
// skipping or measuring nothing meaningful.
func BenchmarkViewDiscovery_100kFiles(b *testing.B) {
	b.Fatal("BLOCKED: the pkg/knowledge discovery helper (DiscoverViewFiles, §4 step 1) does not " +
		"exist — there is nothing to benchmark yet. Once it exists, this benchmark must run over a " +
		"100,000-file fixture (F12's own standing scale assumption) and its measured number becomes " +
		"the CI-checked bound test 72 (TestDiscovery_OperationCountWithin100kFixture) asserts " +
		"deterministically, per SC-VA-004 / TDD test 17.")
}

// TestKnowledgeRestructure_RenameThenResolveByUnchangedName is TDD Plan
// test 41 (FR-VA-016, EC-5): after a rename/move, a HAND-MADE view must
// still resolve by its unchanged Def.Name.
//
// BLOCKED (dependency-order, not a missing symbol per se): the rename
// itself is broken for a .view path today — TestKnowledgeRestructure_
// MovesDotViewPathUnchanged (TDD test 32, already written and RED) shows
// ensureMarkdown mangles the .view SOURCE into a .md-suffixed lookup, so
// the rename is REFUSED ("rename source not found") before any file
// actually moves. This row's own claim — "the view still resolves by its
// unchanged name AFTER a rename" — cannot be shown without first landing
// test 32's fix; testing it against today's broken rename would either
// vacuously pass (nothing moved, so of course the original name still
// resolves at the original path) or fail for test 32's reason again, never
// for FR-VA-016/EC-5's own reason. Discovery-anywhere (TDD tests 1-3) is
// also required to resolve a view that moved to a genuinely new location.
func TestKnowledgeRestructure_RenameThenResolveByUnchangedName(t *testing.T) {
	t.Fatal("BLOCKED: verifying this row meaningfully requires TDD test 32's rename fix (ensureMarkdown " +
		"currently mangles a .view source, refusing every rename before any move happens) AND " +
		"discovery-anywhere (TDD tests 1-3) to resolve the view at its new location — testing it " +
		"against today's code would either vacuously pass (nothing moved) or fail for test 32's " +
		"reason, never for FR-VA-016/EC-5's own reason, per TDD test 41.")
}
