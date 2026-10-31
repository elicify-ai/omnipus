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
	"net/http"
	"strings"
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

func TestT02_OutcomeMapping_DisabledInstanceIsNoReloadNeeded(t *testing.T) {
	// S-52 row 1: instance disabled, allow list changed → no_reload_needed.
	f, _, _ := orderingWriterFixture(t)
	require.Equal(t, http.StatusOK, f.do(http.MethodPut, "/api/v1/channels/line.healthy/disable", "").Code)

	status, body := t02Configure(t, f, "line.healthy", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "no_reload_needed", "S-52 row 1")
}

func TestT02_OutcomeMapping_UnknownExtraKeyIsNoReloadNeeded(t *testing.T) {
	// S-52 row 2: enabled, only an unknown extra key changed that does not
	// alter the adapter-view hash → no_reload_needed.
	f, _, _ := orderingWriterFixture(t)

	status, body := t02Configure(t, f, "line.healthy", `{"qa_unknown_extra_key":"x"}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "no_reload_needed", "S-52 row 2")
}

func TestT02_OutcomeMapping_RebuiltRunningIsApplied(t *testing.T) {
	// S-52 row 3: enabled, allow list changed, rebuilt adapter running →
	// applied, and FR-052's apply_note is present.
	f, _, _ := orderingWriterFixture(t)

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
	f, _, _ := orderingWriterFixture(t)

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
	f, _, _ := orderingWriterFixture(t)
	require.NoError(t, f.f1277.store.Delete(issue1277BrokenAccessRef), "instrument: drop the ref the line instance selects")

	status, body := t02Configure(t, f, "line", `{"allow_from":["qa1277-user"]}`)
	require.Equal(t, http.StatusOK, status, body)
	requireApplyOutcome(t, body, "applied_not_running", "S-52 row 5")
}

func TestT02_OutcomeMapping_ScriptedOutcomesNeedSeams(t *testing.T) {
	// S-52 rows 6–9 (unconfirmed; failed; failed during teardown; failed
	// partial multi-connection reload) cannot be scripted against the real
	// stack yet: the manager's restart-outcome seam (triggerReloadAndWaitOutcome)
	// has no test hook for a timeout, a reload error, a disposal error or a
	// mixed multi-connection result. WP-1 must expose the seam named in the
	// RED report; until then this row fails loudly instead of passing vacuously.
	t.Fatalf("BLOCKED: no test seam to script Manager restart outcomes (unconfirmed/failed/teardown/partial) — required by S-52 rows 6-9, T-6, FR-052; WP-1 must provide an injectable reload-outcome hook named in the RED report")
}

func TestT03_TokenReplacedUnderSameReference_RestartDeliversNewToken(t *testing.T) {
	// S-51: token B stored under the same reference → RequestRestart, the
	// rebuilt adapter's constructor received B (observed: the fixture's
	// connect-credential probe fires), apply_outcome ≠ no_reload_needed, and
	// neither A nor B appears in the response.
	f, _, _ := orderingWriterFixture(t)

	before := f.f1277.connectProbes.Load()
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
	require.NotEmpty(t, inst.ChannelAccessTokenRef, "S-51: the saved token_ref must exist")
	stored, err := f.store.Get(inst.ChannelAccessTokenRef)
	require.NoError(t, err)
	require.Equal(t, issue1277ConnectAccess, stored,
		"S-51: the credential store holds B under the reference the saved config selects")

	// The rebuilt adapter actually started with B: the LINE loopback fixture
	// counted a bot-info probe carrying B (SC-005: 1 reload, new token live).
	require.Greater(t, f.f1277.connectProbes.Load(), before,
		"S-51: the rebuilt adapter must probe the platform with the new token (RequestRestart ran)")

	// FR-053/NB-8: neither the old nor the new secret value appears in the response.
	raw, _ := json.Marshal(body)
	require.NotContains(t, string(raw), issue1277ConnectAccess)
	require.NotContains(t, string(raw), "qa1277-healthy-access")
	require.False(t, strings.Contains(string(raw), issue1277ConnectAccess),
		"S-51: no secret value in the response")
}
