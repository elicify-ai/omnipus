package gateway

// T-02 + T-03 — Configure apply + outcome mapping (channels-spec-r8, T-02 with
// dataset S-52; T-03 = S-51; FR-050, FR-052, T-6; US-6).
//
// Oracles (spec tables, never code):
//   T-6/S-52 outcome mapping — apply_outcome per setup:
//     disabled instance, allow list changed          → no_reload_needed
//     enabled, unknown extra key only                → no_reload_needed
//     enabled, allow list changed, rebuild running   → applied
//     enabled, rebuilt Start fails                   → applied_not_running (reason = start error)
//     enabled, rebuilt fails to construct            → applied_not_running (reason = failed-channel entry)
//   plus FR-052: apply_note whenever a reload was attempted.
//   S-51 — token replaced under the same reference: RequestRestart + rebuilt
//   adapter receives B; apply_outcome ≠ no_reload_needed; neither A nor B in
//   any response. B's arrival at the adapter is observed through the real
//   LINE loopback fixture: token B is the fixture's connect credential, whose
//   bot-info probe increments f.connectProbes.
//
// Fixture: the real REST stack + real manager of newWU6Fixture (real LINE
// adapter against the loopback protocol fixture; no real platform).
//
// RED expectation: configureChannel today never reloads and returns no
// apply_outcome field, so every outcome assertion fails on the missing field;
// the S-51 rebuild assertion fails because no restart runs.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// t02Configure issues PUT /api/v1/channels/{id}/configure through the
// production route table and decodes the JSON body.
func t02Configure(t *testing.T, f *wu6Fixture, id, body string) (int, map[string]any) {
	t.Helper()
	rec := f.do(http.MethodPut, "/api/v1/channels/"+id+"/configure", body)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("configure response for %s is not a JSON object: %v\nbody: %s", id, err, rec.Body.String())
	}
	return rec.Code, out
}

// requireApplyOutcome asserts one T-6 outcome value, with the field-missing
// RED failure naming the spec table.
func requireApplyOutcome(t *testing.T, body map[string]any, want string, table string) {
	t.Helper()
	require.Contains(t, body, "apply_outcome",
		"%s: the Configure response must carry apply_outcome (T-6/FR-052); the field is absent entirely", table)
	require.Equal(t, want, body["apply_outcome"],
		"%s: apply_outcome must be %q", table, want)
}

// T-6/T-7 define this as an exact propagation invariant: Configure must copy
// the full manager failure entry for THIS instance. The manager record is an
// independent input to the projection, never a value read from Configure's body.
// Constructor diagnostic wording itself is outside this test's boundary.
func t02RequireFailedChannelReason(t *testing.T, f *wu6Fixture, body map[string]any, id, where string) {
	t.Helper()
	var reasons []string
	for _, failure := range f.svc.ChannelManager.FailedChannels() {
		if failure.Name == id {
			reasons = append(reasons, failure.Error())
		}
	}
	require.Len(t, reasons, 1, "%s: instrument must independently select exactly this instance's failed-channel record", where)
	require.Equal(t, reasons[0], body["apply_reason"], "%s: T-6/T-7 requires the complete selected failed-channel entry", where)
}

func TestT02_OutcomeMapping_DisabledInstanceIsNoReloadNeeded(t *testing.T) {
	// S-52 row 1: instance disabled, allow list changed → no_reload_needed.
	f := t02Fixture(t)
	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/line.healthy/disable", "").Code)

	status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "no_reload_needed", "S-52 row 1")
}

func TestT02_OutcomeMapping_UnknownExtraKeyIsNoReloadNeeded(t *testing.T) {
	// S-52 row 2: enabled, only an unknown extra key changed that does not
	// alter the adapter-view hash → no_reload_needed.
	f := t02Fixture(t)

	status, body := t02Configure(t, f, "line.healthy", `{"qa_unknown_extra_key":"x"}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "no_reload_needed", "S-52 row 2")
}

func TestT02_OutcomeMapping_RebuiltRunningIsApplied(t *testing.T) {
	// S-52 row 3: enabled, allow list changed, rebuilt adapter running →
	// applied, and FR-052's apply_note is present.
	f := t02Fixture(t)

	status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "applied", "S-52 row 3")
	require.Contains(t, body, "apply_note",
		"FR-052: apply_note must be present whenever a reload was attempted")
}

func TestT02_OutcomeMapping_StartFailureIsAppliedNotRunning(t *testing.T) {
	// S-52 row 4: the rebuilt adapter's Start fails → applied_not_running
	// with the start error as apply_reason. The fixture's controlled 401
	// credential makes the real LINE Start fail against the loopback server.
	f := t02Fixture(t)

	status, body := t02Configure(t, f, "line.healthy",
		`{"channel_access_token":"`+issue1277UnauthorizedAccess+`"}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "applied_not_running", "S-52 row 4")
	require.Contains(t, body["apply_reason"], "401",
		"T-6: applied_not_running's apply_reason carries the start error")
}

func TestT02_OutcomeMapping_ConstructorFailureIsAppliedNotRunning(t *testing.T) {
	// S-52 row 5: the rebuilt adapter fails to construct (referenced
	// credential missing — the supported #1277 degradation) →
	// applied_not_running with the failed-channel entry as apply_reason.
	f := t02Fixture(t)
	require.NoError(t, f.f1277.store.Delete(issue1277BrokenAccessRef), "instrument: drop the ref the line instance selects")

	status, body := t02Configure(t, f, "line", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "applied_not_running", "S-52 row 5")
	t02RequireFailedChannelReason(t, f, body, "line", "S-52 row 5")
}

func TestT02_OutcomeMapping_ScriptedOutcomesNeedSeams(t *testing.T) {
	t.Run("S52 row6 unconfirmed", func(t *testing.T) {
		f := t02Fixture(t)
		previous := reloadWaitTimeout
		reloadWaitTimeout = 0 // deterministic expiry; no sleep or enlarged timing tolerance
		t.Cleanup(func() { reloadWaitTimeout = previous; f.al.ClearReloadPending() })
		f.al.SetReloadFunc(func() error { f.al.MarkReloadPending(); return nil })
		status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
		require.Equal(t, http.StatusOK, status, body)
		requireApplyOutcome(t, body, "unconfirmed", "S-52 row 6")
		require.True(t, f.al.IsReloadPending(), "expiry is not completion")
		require.Contains(t, body, "apply_note", "FR-052: the attempted reload is disclosed")
	})
	t.Run("S52 row7 reload error", func(t *testing.T) {
		f := t02Fixture(t)
		f.al.SetReloadFunc(func() error { return errors.New("t02 scripted reload failure") })
		status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
		require.Equal(t, http.StatusOK, status, body)
		requireApplyOutcome(t, body, "failed", "S-52 row 7")
		require.NotEmpty(t, body["apply_reason"], "T-6: failure has a short reason")
		require.Equal(t, "the configuration reload failed; details are in the server log", body["apply_reason"],
			"T-6 amended 2026-11-01: row 7 uses the fixed secret-free reload phrase, not raw error text")
		require.Contains(t, body, "apply_note")
	})
	t.Run("S52 row8 unfinished teardown", func(t *testing.T) {
		f := t02Fixture(t)
		fault := t02FaultStop(t, f, "line.healthy")
		status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
		require.Equal(t, http.StatusOK, status, body)
		requireApplyOutcome(t, body, "failed", "S-52 row 8")
		require.NotEmpty(t, body["apply_reason"], "unfinished disposal must be explained")
		// S-52 amended row 8: "for a config-changing save, because the
		// applied-hash leg is checked before the disposal leg".
		require.Equal(t, "the configuration reload failed; details are in the server log", body["apply_reason"],
			"S-52 amended row 8: changed settings use the fixed reload phrase")
		require.Contains(t, body, "apply_note")
		require.Greater(t, fault.calls.Load(), int32(0), "instrument: real reload reached adapter Stop")
		require.False(t, fault.IsRunning(), "fixture's old adapter half-completed shutdown; do not claim it keeps running")
	})
	t.Run("S52 row8 identical save with pending disposal", func(t *testing.T) {
		f := t02Fixture(t)
		fault := t02FaultStop(t, f, "line.healthy")
		// Fail a real teardown WITHOUT changing the applied settings first.
		// Matching hash + pending disposal is the second amended row-8 case.
		f.svc.ChannelManager.RequestRestart("line.healthy", "", f.al.GetConfig())
		_, err := f.api.triggerReloadAndWaitOutcome()
		require.Error(t, err, "instrument: the first real teardown is unfinished")
		require.Greater(t, fault.calls.Load(), int32(0))
		status, body := t02Configure(t, f, "line.healthy", `{}`)
		require.Equal(t, http.StatusOK, status, body)
		requireApplyOutcome(t, body, "failed", "S-52 amended row 8, identical save with matching applied settings")
		require.Equal(t, "the previous connection did not finish shutting down; the next save retries it", body["apply_reason"],
			"T-6/S-52 amended row 8: the pending-disposal leg uses its fixed secret-free phrase")
		require.Contains(t, body, "apply_note")
		require.Greater(t, fault.calls.Load(), int32(1), "the identical save actually retried the retained adapter's disposal")
		require.False(t, fault.IsRunning())
	})
	t.Run("S52 row9 partial multi-connection reload", func(t *testing.T) {
		f := t02BootFixture(t, func(base *issue1277Fixture) {
			require.NoError(t, base.store.Set(issue1277BrokenRef, "qa1277-restored-peer-secret"))
		})
		before, exists := f.svc.ChannelManager.GetChannel("line")
		require.True(t, exists)
		fault := t02FaultStop(t, f, "line.healthy")
		f.svc.ChannelManager.RequestRestart("line", "", f.al.GetConfig())
		status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
		require.Equal(t, http.StatusOK, status, body)
		requireApplyOutcome(t, body, "failed", "S-52 row 9: overall reload with mixed results is failed")
		require.Equal(t, "the configuration reload failed; details are in the server log", body["apply_reason"],
			"T-6 amended 2026-11-01: partial reload failure uses the fixed reload phrase")
		require.Greater(t, fault.calls.Load(), int32(0))
		after, exists := f.svc.ChannelManager.GetChannel("line")
		require.True(t, exists)
		require.NotSame(t, before, after, "instrument: the other connection really applied a replacement")
		require.True(t, after.IsRunning(), "the other connection is now running while this disposal failed")
		require.Contains(t, body, "apply_note")
	})
}

func TestT03_TokenReplacedUnderSameReference_RestartDeliversNewToken(t *testing.T) {
	previousCaptureHook := reloadCredentialCaptureHook
	// Register before the fixture: restoration runs after its reload worker
	// joins, including failure cleanup, so no active reload races hook removal.
	t.Cleanup(func() { reloadCredentialCaptureHook = previousCaptureHook })
	// S-51: token B stored under the same reference → RequestRestart, the
	// rebuilt adapter's constructor received B (observed: the fixture's
	// connect-credential probe fires), apply_outcome ≠ no_reload_needed, and
	// neither A nor B appears in the response.
	f := t02Fixture(t)
	// Establish A through Configure first: the fixture's initial reference is
	// not this form's key. Otherwise saving B would test a reference change,
	// not S-51's same-reference replacement (the public hash must stay equal).
	status, first := t02Configure(t, f, "line.healthy",
		`{"channel_access_token":"`+issue1277HealthyAccess+`"}`)
	require.Equal(t, http.StatusOK, status, first)
	requireApplyOutcome(t, first, "applied", "S-51: establish A under the selected Configure reference")
	selectedRef := f.al.GetConfig().Channels["line.healthy"].ChannelAccessTokenRef
	require.Equal(t, "channel_line.healthy_channel_access_token", selectedRef)

	before := f.f1277.connectProbes.Load()
	var rebuilds atomic.Int32
	reloadCredentialCaptureHook = func(stage string) {
		if stage == "rebuild" {
			rebuilds.Add(1)
		}
		if previousCaptureHook != nil {
			previousCaptureHook(stage)
		}
	}
	status, body := t02Configure(t, f, "line.healthy",
		`{"channel_access_token":"`+issue1277ConnectAccess+`"}`)
	require.Equal(t, http.StatusOK, status, body)

	// The response carries apply_outcome and it is NOT no_reload_needed.
	require.Contains(t, body, "apply_outcome",
		"S-51: the Configure response must carry apply_outcome")
	require.NotEqual(t, "no_reload_needed", body["apply_outcome"],
		"S-51/SC-005: token replacement must not answer no_reload_needed")

	// The saved config now selects a reference whose stored value is B.
	cfg := f.al.GetConfig()
	inst := cfg.Channels["line.healthy"]
	require.Equal(t, selectedRef, inst.ChannelAccessTokenRef,
		"S-51: B replaces the same selected reference, not a public reference change")
	stored, err := f.store.Get(inst.ChannelAccessTokenRef)
	require.NoError(t, err)
	require.Equal(t, issue1277ConnectAccess, stored,
		"S-51: the credential store holds B under the reference the saved config selects")

	// The rebuilt adapter actually started with B: the LINE loopback fixture
	// counted a bot-info probe carrying B (SC-005: 1 reload, new token live).
	require.Greater(t, f.f1277.connectProbes.Load(), before,
		"S-51: the rebuilt adapter must probe the platform with the new token (RequestRestart ran)")
	require.Equal(t, int32(1), rebuilds.Load(), "SC-005: the same-reference token replacement applies B in exactly one real reload")

	// FR-053/NB-8: neither the old nor the new secret value appears in the response.
	raw, _ := json.Marshal(body)
	require.NotContains(t, string(raw), issue1277ConnectAccess)
	require.NotContains(t, string(raw), "qa1277-healthy-access")
	require.False(t, strings.Contains(string(raw), issue1277ConnectAccess),
		"S-51: no secret value in the response")
}
