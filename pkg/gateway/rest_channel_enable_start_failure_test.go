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
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
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

	// ORACLE (issue #1568 Expected + round-3 F4, strengthening only): the
	// documented envelope is 500 ErrorResponse exactly — not merely
	// "not 200" (the check's 1568-m3 mutant answered 400 and survived the
	// looser form), and the reason rides ErrorResponse.error, not arbitrary
	// response text.
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"issue #1568/F4: the enable must answer the contract's documented 500 ErrorResponse while the adapter failed to start; body: %s", rec.Body.String())
	var errBody gen.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errBody),
		"F4: the 500 body must decode as the generated ErrorResponse; body: %s", rec.Body.String())
	require.NotEmpty(t, errBody.Error,
		"F4: the ErrorResponse must carry the failure reason in its error field; body: %s", rec.Body.String())
	// The reason is the same controlled credential rejection the manager
	// recorded — not a fixed sentence that hides the cause.
	require.Contains(t, errBody.Error, "401",
		"F4: the ErrorResponse error field must carry the start-failure reason; got %q", errBody.Error)

	// The enabled flag stays saved (the fix's honest saved-enabled /
	// runtime-stopped split): the connector shows enabled in the list, where
	// its degraded reason is the operator's diagnostic trail.
	rows := issue1277ChannelRows(t, f.api)
	row, exists := rows["line.healthy"]
	require.True(t, exists, "the enabled connector must still be listed")
	require.True(t, row.Enabled,
		"a reported start failure must not roll the saved enabled flag back (it stays saved for the retry)")
}

// TestEnableChannelOrdinary_UnconfirmedRestart_IsNotPlainSuccess covers
// round-3 D3 (check finding F2): an ordinary NON-pairing enable whose
// start/reload is not confirmed within the wait must not answer a plain 200
// success either — that is the same false-success class #1568 removes. The
// contract already documents this 500 ("the connection is enabled but its
// restart could not be confirmed yet", openapi.yaml /channels/{id}/enable
// 500); team-lead decision D3 pins it for the non-pairing path, which today
// still falls through to jsonOK (the F2 gap).
//
// Oracle (D3 + contract): status 500 exactly, the body is the generated
// ErrorResponse, and its error field says the connector is enabled but its
// start was not confirmed. The saved enabled setting stays saved.
//
// The unconfirmed window is forced deterministically through the same seam
// the Configure outcome tests use (reloadWaitTimeout = 0 plus a reload
// trigger that marks the reload pending and never completes) — no wall-clock
// tolerance, no sleeping.
func TestEnableChannelOrdinary_UnconfirmedRestart_IsNotPlainSuccess(t *testing.T) {
	f := t02Fixture(t)

	// A real re-enable: turn the running connector off first (its reload
	// still runs on the real consumer, so the disable completes normally).
	rec := f.do(http.MethodPut, "/api/v1/channels/line.healthy/disable", "")
	require.Equal(t, http.StatusOK, rec.Code, "instrument: disable the running connector first")

	// Force the unconfirmed window deterministically.
	previous := reloadWaitTimeout
	reloadWaitTimeout = 0
	t.Cleanup(func() { reloadWaitTimeout = previous; f.al.ClearReloadPending() })
	f.al.SetReloadFunc(func() error { f.al.MarkReloadPending(); return nil })

	// The enable under test: the save commits, the reload never confirms.
	rec = f.do(http.MethodPut, "/api/v1/channels/line.healthy/enable", "")

	// ORACLE (D3): the contract's documented 500, never a plain success.
	require.Equal(t, http.StatusInternalServerError, rec.Code,
		"D3/F2: an unconfirmed non-pairing restart must not answer plain 200 success; body: %s", rec.Body.String())
	var errBody gen.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errBody),
		"D3: the 500 body must decode as the generated ErrorResponse; body: %s", rec.Body.String())
	require.NotEmpty(t, errBody.Error,
		"D3: the ErrorResponse must explain the unconfirmed start; body: %s", rec.Body.String())
	// The reason says the connector IS enabled (the save persists) but its
	// start was not confirmed — the contract's enabled-but-unconfirmed
	// wording family, never a bare generic failure.
	require.Contains(t, errBody.Error, "enabled",
		"D3: the reason must say the connector is enabled but unconfirmed; got %q", errBody.Error)
	require.Regexp(t, `(?i)not (yet )?confirmed|could not be confirmed`, errBody.Error,
		"D3: the reason must say the start was not confirmed; got %q", errBody.Error)

	// The enabled setting stays saved for the operator's retry.
	rows := issue1277ChannelRows(t, f.api)
	row, exists := rows["line.healthy"]
	require.True(t, exists, "the connector must still be listed")
	require.True(t, row.Enabled,
		"an unconfirmed restart must not roll the saved enabled flag back")
}
