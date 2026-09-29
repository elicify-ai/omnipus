// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-032, TDD Plan
// rows 77, 78 (va-qa3 dispatch, P3).
//
// Oracle: FR-VA-032 (founder-direction amendment 2026-09-29, architect
// ruling Q-B = B; team-lead amendment 2026-09-29 on Retry mechanics).
// Contract: POST /library/{workspace_id}/retry-move (RetryMoveRequest ->
// RetryMoveResult; RetryMoveError codes retry_not_found 404, retry_expired
// 410, retry_identity_mismatch 409, retry_preflight_failed 409,
// retry_locked 503), agent surface knowledge_restructure op "retry_move"
// (arg pending_move_id). Pending moves and completed receipts live in the
// SAME view_membership.json record the revocation itself writes, keyed by
// pending_move_id, expiring 7 days after their timestamp (a named
// constant). Seven distinct outcomes: normal retry restores management;
// already-landed retry verifies+enrolls; retry_preflight_failed (occupied
// destination, or source changed/vanished); retry_expired (7-day window);
// repeat-after-success returns outcome already_complete (no-op via the
// receipt); unknown id -> retry_not_found; identity mismatch (a planted
// copy at the target path) -> retry_identity_mismatch, and that planted
// copy stays unmanaged.
//
// BLOCKED, every case, for the SAME root cause, verified by reading the
// code (2026-09-29):
//   - Route: pkg/gateway/rest_library.go::HandleLibrary's switch has no
//     "retry-move" case at all — POST /library/{workspace_id}/retry-move
//     falls through to http.NotFound (confirmed: `grep -n "retry-move"
//     pkg/gateway/rest_library.go` — zero matches in the switch).
//   - Handler: zero non-generated, non-schema Go hits anywhere in pkg/ for
//     "RetryMove" (confirmed: `grep -rln "RetryMove" pkg/ | grep -v
//     _test.go | grep -v /generated/ | grep -v inboundschemas` — zero).
//   - Agent op: pkg/knowledge/knowledge_restructure.go's restructureOps is
//     exactly {rename, move, trash, restore} — "retry_move" is not one of
//     them, and there is no case for it in Execute's switch.
//   - Pending-move / receipt record: zero hits anywhere in pkg/ for
//     "PendingMove"/"pending_move" outside contracts/generated code
//     (confirmed by the same grep pattern) — there is no
//     view_membership.json writer, reader, or 7-day-expiry constant to
//     construct a fixture against for ANY of the seven cases.
//
// The generated wire types (gen.RetryMoveRequest/Result/Error,
// gen.LibraryMoveConflictErrorCodeMoveIncomplete) DO exist on this branch
// (STEP 0 merge, commit 2d3387bcb) — it is the entire retry MECHANISM,
// route, op, and record that is missing, which is the "endpoint/function
// that does not exist anywhere in the codebase" case the qa-lead RED
// protocol reserves for a BLOCKED t.Fatal rather than a real HTTP call
// against a route that cannot exist yet.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_NormalRetryAfterPostRevocationFailureRestoresManagement$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_AlreadyLandedVerifiesAndEnrolls$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_PreflightFailedOnOccupiedDestination$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_PreflightFailedOnSourceChangedOrVanished$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_ExpiredAfterSevenDaysNamedConstantInjectedClock$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_RepeatAfterSuccessReturnsAlreadyComplete$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_UnknownIdReturnsRetryNotFound$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRetryMove_IdentityMismatchLeavesPlantedCopyUnmanaged$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestRetryMove_NormalRetryAfterPostRevocationFailureRestoresManagement(t *testing.T) {
	t.Fatal("BLOCKED: no retry-move route, handler, agent op, or pending-move record exists " +
		"anywhere in the codebase (see this file's header) — there is no revoke-before-rename " +
		"pipeline that could even produce a forced post-revocation failure to retry, per " +
		"FR-VA-032 / TDD row 77.")
}

func TestRetryMove_AlreadyLandedVerifiesAndEnrolls(t *testing.T) {
	t.Fatal("BLOCKED: Retry's 'if the rename already landed, verify and enroll' branch " +
		"(FR-VA-032 amendment) has no implementation to call — no retry-move route/handler/op/ " +
		"record exists anywhere in the codebase (see this file's header), per TDD row 77.")
}

func TestRetryMove_PreflightFailedOnOccupiedDestination(t *testing.T) {
	t.Fatal("BLOCKED: retry_preflight_failed (destination occupied) requires a saved pending-move " +
		"record to re-preflight against — no such record type, writer, or reader exists anywhere " +
		"in the codebase (see this file's header), per FR-VA-032 / TDD row 78.")
}

func TestRetryMove_PreflightFailedOnSourceChangedOrVanished(t *testing.T) {
	t.Fatal("BLOCKED: retry_preflight_failed (source changed or vanished) requires the same saved " +
		"pending-move record — absent anywhere in the codebase (see this file's header), per " +
		"FR-VA-032 / TDD row 78.")
}

func TestRetryMove_ExpiredAfterSevenDaysNamedConstantInjectedClock(t *testing.T) {
	t.Fatal("BLOCKED: retry_expired needs the named 7-day constant and the pending-move record's " +
		"timestamp comparison against an injected clock — neither the constant nor the record " +
		"exists anywhere in the codebase (see this file's header), per FR-VA-032 amendment / TDD " +
		"row 78.")
}

func TestRetryMove_RepeatAfterSuccessReturnsAlreadyComplete(t *testing.T) {
	t.Fatal("BLOCKED: the 'completed receipt' outcome (RetryMoveResultOutcomeAlreadyComplete) " +
		"requires the SAME view_membership.json record to hold an inert completed entry after a " +
		"successful retry — no writer for that record, nor the record itself, exists anywhere in " +
		"the codebase (see this file's header), per FR-VA-032 amendment / TDD row 78.")
}

func TestRetryMove_UnknownIdReturnsRetryNotFound(t *testing.T) {
	t.Fatal("BLOCKED: retry_not_found requires a retry-move handler to look an id up against and " +
		"fail to find it — there is no retry-move route in pkg/gateway/rest_library.go::HandleLibrary " +
		"at all (confirmed: no \"retry-move\" case in its switch — an actual HTTP POST to that path " +
		"404s via http.NotFound, not via a typed RetryMoveError), per FR-VA-032 / TDD row 78.")
}

func TestRetryMove_IdentityMismatchLeavesPlantedCopyUnmanaged(t *testing.T) {
	t.Fatal("BLOCKED: retry_identity_mismatch requires Retry to check the planted file's identity " +
		"against the pending-move record's saved identity before granting management — neither the " +
		"record nor any identity check exists anywhere in the codebase (see this file's header), " +
		"per FR-VA-032 amendment / TDD row 78.")
}
