// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-032/034
// interaction, va-qa3 brief P7 (a)(b)(c) — new team-lead test requests added
// after P1 started, not yet written anywhere.
//
// Each case is its own test, independent of the P3 cases (a different
// pending-move interaction class), using the same pending_move_id-keyed
// record shape P3 assumes (see rest_library_retry_move_red_test.go's
// header for the full root-cause finding this file shares).
//
// (a) A tracked nested KB is ADDED to a folder's subtree AFTER that
//     folder's move went pending (FR-VA-032/034 interaction). Retry on that
//     pending move MUST return retry_preflight_failed; the membership
//     record and file bytes must be unchanged (no partial replay).
// (b) TWO pending folder-move journal entries exist at once; the OTHER one
//     (not the one being retried) gains a tracked nested KB in the
//     meantime. Retry of the first pending move MUST refuse and replay
//     NEITHER move — both records remain pending/untouched.
// (c) Retry of a case-only folder rename (Foo -> foo) on a case-insensitive
//     filesystem (APFS) MUST fail CLOSED: retry_preflight_failed, no
//     mutation to the record or the filesystem.
//
// BLOCKED, all three, for the same root cause
// rest_library_retry_move_red_test.go's header documents in full: there is
// no retry-move route, handler, agent op, or pending-move/
// view_membership.json record anywhere in the codebase to construct EITHER
// a single pending move or two concurrent ones against, and no
// preflight-check seam that could detect "a nested KB was added since" or
// "the destination now differs only by case" at all.
//
// Case (c) additionally needs a filesystem-case-collision fixture; per the
// brief, this run's environment is checked below and the finding stated
// honestly rather than assumed.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_NestedKBAddedToPendingFolderMoveSubtreeAfterPending_RefusesPreflight$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_OtherConcurrentPendingMoveGainsNestedKB_NeitherMoveReplayed$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_CaseOnlyFolderRenameFailsClosedOnCaseInsensitiveFilesystem$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRetryMove_NestedKBAddedToPendingFolderMoveSubtreeAfterPending_RefusesPreflight
// is P7(a).
func TestRetryMove_NestedKBAddedToPendingFolderMoveSubtreeAfterPending_RefusesPreflight(t *testing.T) {
	t.Fatal("BLOCKED: P7(a) needs a real pending folder-move record to add a nested KB against and " +
		"then retry — no pending-move/view_membership.json record, no retry-move route/handler/op, " +
		"and no preflight re-check step exist anywhere in the codebase (see " +
		"rest_library_retry_move_red_test.go's header for the full grep evidence). There is nothing " +
		"to plant the interaction against.")
}

// TestRetryMove_OtherConcurrentPendingMoveGainsNestedKB_NeitherMoveReplayed
// is P7(b).
func TestRetryMove_OtherConcurrentPendingMoveGainsNestedKB_NeitherMoveReplayed(t *testing.T) {
	t.Fatal("BLOCKED: P7(b) needs TWO concurrent pending-move records in the SAME " +
		"view_membership.json to exist at once, plus a retry-move handler that inspects one while " +
		"leaving the other alone — none of the record type, its writer, or the handler exist " +
		"anywhere in the codebase (see rest_library_retry_move_red_test.go's header). There is no " +
		"'first pending move' or 'other one' to construct.")
}

// TestRetryMove_CaseOnlyFolderRenameFailsClosedOnCaseInsensitiveFilesystem
// is P7(c). The brief requires an honest statement of the test
// environment's case sensitivity rather than an assumed one.
func TestRetryMove_CaseOnlyFolderRenameFailsClosedOnCaseInsensitiveFilesystem(t *testing.T) {
	dir := t.TempDir()
	upper := filepath.Join(dir, "Foo")
	if err := os.MkdirAll(upper, 0o755); err != nil {
		t.Fatalf("fixture setup failed: %v", err)
	}
	lowerStat, statErr := os.Stat(filepath.Join(dir, "foo"))
	caseInsensitive := statErr == nil && lowerStat != nil
	envNote := "this test run's temp filesystem is NOT case-insensitive (stat(\"foo\") after " +
		"creating \"Foo\" failed) — this is NOT run on real case-insensitive APFS behavior; the " +
		"case-collision condition below is asserted directly rather than observed from the OS."
	if caseInsensitive {
		envNote = "this test run's temp filesystem IS case-insensitive (stat(\"foo\") after " +
			"creating \"Foo\" succeeded) — the case-collision condition is real APFS/case-insensitive " +
			"behavior, observed, not simulated."
	}
	t.Fatal("BLOCKED: P7(c) needs a real retry-move preflight check to run against a case-only " +
		"rename (Foo -> foo) and observe it fail closed — no retry-move route/handler/op or " +
		"pending-move record exists anywhere in the codebase (see " +
		"rest_library_retry_move_red_test.go's header), so there is no preflight step to exercise " +
		"regardless of filesystem case sensitivity. Environment note (per the brief's honesty " +
		"requirement): " + envNote)
}
