// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-035, TDD Plan
// row 84 (va-qa3 dispatch, P4, team-lead ruling 2026-09-29).
//
// Requirement: an UNEXPIRED pending-move path blocks transfers and root
// moves — a path with a pending Retry window open must refuse a NEW
// transfer/move landing on (or originating from) it, rather than racing
// the pending move.
//
// BLOCKED for the same root cause rest_library_retry_move_red_test.go's
// header documents in full: there is no pending-move/view_membership.json
// record anywhere in the codebase (confirmed by grep) — there is nothing
// to construct "an unexpired pending-move path" fixture against, and no
// transfer/move-time check that would even consult such a record if it
// existed (handleLibraryTransfer has zero membership-record awareness,
// per rest_library_view_tracked_transfer_refused_red_test.go's header).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryTransfer_RefusesWhenSourceOrDestinationHasAnUnexpiredPendingMove$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestLibraryTransfer_RefusesWhenSourceOrDestinationHasAnUnexpiredPendingMove(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-035 row 84 needs an unexpired pending-move record to plant against a " +
		"transfer/root-move path and observe it get refused — no pending-move/view_membership.json " +
		"record exists anywhere in the codebase (see rest_library_retry_move_red_test.go's header for " +
		"the full grep evidence), and handleLibraryTransfer has no membership-record awareness at " +
		"all to consult one even if it existed (see " +
		"rest_library_view_tracked_transfer_refused_red_test.go's header). There is nothing to plant " +
		"the pending-move fixture against.")
}
