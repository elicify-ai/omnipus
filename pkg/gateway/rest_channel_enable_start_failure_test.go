// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Issue #1568 RED — an ordinary (Connectors) enable of a connector whose
// adapter start fails must report that failure truthfully: never a plain
// success while the adapter is not running, and the response must carry the
// start-failure reason.
//
// Oracles (derived from the issue and the contract, never current behaviour):
//   - Issue #1568 "Expected": "Enable reports a start failure truthfully
//     (for example the same 'not running: <reason>' wording the Configure
//     outcome uses), never a plain success while the adapter is not running."
//   - Contract ChannelEnabledResponse {id, enabled} with
//     additionalProperties: false — the 200 schema has NO start-failure
//     field, so the only contract-legal carrier for the reason is an error
//     status whose ErrorResponse body names it. Contract gap (for the
//     architect): a start-failure field on the 200 body would be a contract
//     change; this test asserts only what the contract allows today and must
//     never be "fixed" by inventing a wire field.
//   - The reason instrument is the same one T-6 already uses for Configure's
//     applied_not_running (channel_configure_outcome_test.go
//     TestT02_OutcomeMapping_StartFailureIsAppliedNotRunning): the fixture's
//     controlled 401 credential makes the REAL LINE adapter's Start fail
//     against the loopback platform fixture, and the recorded failure text
//     carries "401".
//
// Fixture: the #1277/WU-6 real stack (t02Fixture) — real channel manager,
// real LINE adapter, the production reload consumer and the production route
// table. Only the LINE HTTP edge and the model provider are simulated; no
// real Discord or LINE is contacted.
//
// RED expectation (today's code): enableChannelOrdinary answers
// 200 {"enabled": true} whenever the reload confirmed, even though the
// adapter start failed (rest_channel_enable.go::enableChannelOrdinary), so
// the not-a-plain-success assertion fails on the pre-fix code.

import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/stretchr/testify/require"
)

// TestEnableChannelOrdinary_StartFailureIsReportedNotPlainSuccess walks the
// issue's exact operator flow: the configured connector is disabled, the
// failing credential is saved (a save on a disabled connection answers
// no_reload_needed per channels spec T-6c — the issue's stated premise), and
// the connector is then enabled again. Its adapter start fails with the
// controlled 401, and the enable response must say so.
func TestEnableChannelOrdinary_StartFailureIsReportedNotPlainSuccess(t *testing.T) {
	f := t02Fixture(t)

	// The operator's connector starts this fixture running and healthy; turn
	// it off first so the later enable is a real start attempt, not a no-op.
	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/disable", "")
	require.Equal(t, http.StatusOK, rec.Code, "instrument: disable the running connector first")

	// Save the failing credential while disabled: T-6c says this save answers
	// no_reload_needed and the panel then calls enable "as today".
	rec = f.do(http.MethodPut, "/api/v1/channels/line.healthy/configure",
		`{"channel_access_token":"`+issue1277UnauthorizedAccess+`"}`)
	require.Equal(t, http.StatusOK, rec.Code,
		"instrument: the failing credential must save on the disabled connector")
	require.Contains(t, rec.Body.String(), `"apply_outcome":"no_reload_needed"`,
		"instrument: T-6c — a save on a disabled connection answers no_reload_needed (issue #1568 premise); body: %s", rec.Body.String())

	// The enable under test. Its reload rebuilds the adapter with the saved
	// failing credential and the adapter's Start fails.
	rec = f.do(http.MethodPut, "/api/v1/channels/line.healthy/enable", "")

	// Instrument, independent of the response: the start really was attempted
	// and really failed — the manager's availability read (the same read
	// Configure's outcome uses) must say not running with the controlled
	// credential rejection as the cause. This keeps the response assertions
	// below honest: they must fail because the response hides the failure,
	// never because no failure happened.
	avail := f.svc.ChannelManager.Availability("line.healthy")
	require.Equal(t, channels.AvailabilityNotRunning, avail.State,
		"instrument: the enable's reload must attempt the start and record its failure (got state %q, reason %q)", avail.State, avail.Reason)
	require.Contains(t, avail.Reason, "401",
		"instrument: the recorded start failure is the controlled credential rejection (reason %q)", avail.Reason)

	// ORACLE (issue #1568 Expected): never a plain success while the adapter
	// is not running.
	require.NotEqual(t, http.StatusOK, rec.Code,
		"issue #1568: the enable must not answer a plain 200 success while the adapter failed to start; body: %s", rec.Body.String())
	// ORACLE: the response carries the start-failure reason. Under the
	// contract the only legal carrier is the error body; the reason is the
	// same controlled rejection the manager recorded.
	require.Contains(t, rec.Body.String(), "401",
		"issue #1568: the enable response must carry the start-failure reason; body: %s", rec.Body.String())
}
