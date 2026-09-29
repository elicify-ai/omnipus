// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-032, team-lead
// addenda 2026-09-29 (va-qa3 dispatch, P3 addendum — added after the rest of
// P3 was written).
//
// Four new Retry lookup/classification cases, each its own test:
//
//  1. A CORRUPT, UNRELATED membership record does not block a valid Retry —
//     the valid Retry succeeds and a Warn is logged.
//  2. When the TARGET record for the pending_move_id is corrupt, Retry
//     returns retry_not_found (never a 500) and restores no authority.
//  3. The source view path absent or unsafe at Retry time -> retry_
//     preflight_failed (no mutation, no authority restored).
//  4. The source view present but its parsed name or derived_from no
//     longer matches the trusted record -> retry_identity_mismatch (no
//     mutation, no authority restored).
//
// BLOCKED, all four, for the SAME root cause
// rest_library_retry_move_red_test.go's header documents in full: there is
// no retry-move route, handler, agent op, or pending-move/
// view_membership.json record anywhere in the codebase to construct EITHER
// a valid-but-corrupted-sibling-record fixture, a corrupt-target-record
// fixture, an absent/unsafe-source-path fixture, or a
// name/derived_from-mismatch fixture against. Case 1's "a Warn is logged"
// half additionally needs a real Retry code path to capture a logger
// around — none exists.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_CorruptUnrelatedRecordDoesNotBlockAValidRetryAndLogsWarn$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_CorruptTargetRecordReturnsRetryNotFoundNeverA500$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_SourcePathAbsentOrUnsafeReturnsPreflightFailed$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_SourceIdentityMismatchAgainstTrustedRecordReturnsIdentityMismatch$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestRetryMove_CorruptUnrelatedRecordDoesNotBlockAValidRetryAndLogsWarn(t *testing.T) {
	t.Fatal("BLOCKED: needs a real retry-move handler and a real view_membership.json record " +
		"format to plant TWO records against — one corrupt and unrelated, one valid and pending — " +
		"and observe the valid one still retry successfully with a Warn logged for the corrupt " +
		"sibling. Neither the handler, the record type, nor a logger call anywhere near it exists " +
		"(see rest_library_retry_move_red_test.go's header for the full grep evidence).")
}

func TestRetryMove_CorruptTargetRecordReturnsRetryNotFoundNeverA500(t *testing.T) {
	t.Fatal("BLOCKED: needs a real retry-move handler to look the TARGET pending_move_id's record " +
		"up, find it corrupt, and classify that as retry_not_found rather than a 500 — no such " +
		"handler, and no record format to corrupt in the first place, exists anywhere in the " +
		"codebase (see rest_library_retry_move_red_test.go's header).")
}

func TestRetryMove_SourcePathAbsentOrUnsafeReturnsPreflightFailed(t *testing.T) {
	t.Fatal("BLOCKED: needs a real retry-move preflight step that re-checks the source view path's " +
		"presence/safety and classifies an absent-or-unsafe source as retry_preflight_failed with no " +
		"mutation — no preflight step, and no pending-move record to preflight-check against, exists " +
		"anywhere in the codebase (see rest_library_retry_move_red_test.go's header).")
}

func TestRetryMove_SourceIdentityMismatchAgainstTrustedRecordReturnsIdentityMismatch(t *testing.T) {
	t.Fatal("BLOCKED: needs a real retry-move identity check that compares the source view's parsed " +
		"name/derived_from against the trusted pending-move record and classifies a mismatch as " +
		"retry_identity_mismatch with no mutation and no authority restored — no such identity check, " +
		"and no trusted record to compare against, exists anywhere in the codebase (see " +
		"rest_library_retry_move_red_test.go's header).")
}
