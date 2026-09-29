// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-033, TDD Plan
// rows 81, 82 (va-qa3 dispatch, P4, team-lead ruling 2026-09-29).
//
// Requirement: releasing a `.base` revokes ALL memberships in ONE record
// save before any marker strip or trash (row 81, assert ordering with an
// injected failure between the steps); the derived badge and read-only
// status follow the RECORD, not the marker — a view with a marker (e.g.
// `derived_from`) but NO record entry is NOT read-only (row 82).
//
// BLOCKED, both rows, for the same root cause: there is no pipeline-owned
// membership record anywhere in the codebase (confirmed by grep, same
// finding rest_library_view_tracked_transfer_refused_red_test.go's header
// and rest_library_retry_move_red_test.go's header both document — zero
// non-generated, non-test hits for "ViewMembership"/"view_membership").
// Row 81 needs a real "revoke ALL memberships in ONE record save" step to
// inject a failure AFTER — there is no revoke step of any kind (confirmed
// by reading handleLibraryEntryDelete in full: zero `.base` awareness,
// zero membership check) — so there is nothing to inject a failure into,
// let alone a single atomic save whose ordering relative to marker-strip/
// trash could be asserted. Row 82 needs the RECORD to exist as the
// authority a read-only/derived-badge check consults instead of the
// marker — no such record, and no such check, exists anywhere (confirmed:
// zero hits for "read-only"/"derived badge" as an implemented concept
// outside comments/spec-prose in generated code).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestBaseRelease_RevokesAllMembershipsInOneRecordSaveBeforeMarkerStripOrTrash$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestViewReadOnlyAndDerivedBadge_FollowTheRecordNotTheMarker$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

// TestBaseRelease_RevokesAllMembershipsInOneRecordSaveBeforeMarkerStripOrTrash
// is TDD Plan row 81 (FR-VA-033).
func TestBaseRelease_RevokesAllMembershipsInOneRecordSaveBeforeMarkerStripOrTrash(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-033 row 81 needs a real 'revoke ALL memberships in ONE record save' step " +
		"to inject a failure between it and the marker-strip/trash steps that follow — there is no " +
		"revoke step of any kind (handleLibraryEntryDelete has zero .base-awareness, confirmed by " +
		"reading pkg/gateway/rest_library_write.go in full) and no pipeline-owned membership record " +
		"to save atomically in the first place (zero hits anywhere for ViewMembership/" +
		"view_membership outside comments). There is nothing to plant the ordering assertion against.")
}

// TestViewReadOnlyAndDerivedBadge_FollowTheRecordNotTheMarker is TDD Plan
// row 82 (FR-VA-033).
func TestViewReadOnlyAndDerivedBadge_FollowTheRecordNotTheMarker(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-033 row 82 needs a real membership RECORD to be the authority a " +
		"read-only/derived-badge check consults (instead of the derived_from marker) — no such " +
		"record, and no such record-vs-marker check, exists anywhere in the codebase. There is no " +
		"way to construct 'a view with a marker but no record entry' as a test fixture when there " +
		"is no record type to have an entry (or lack one) in.")
}
