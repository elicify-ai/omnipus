//go:build !cgo

// REST gate test for PUT /api/v1/agents/{id}: sandbox_profile is retired
// (ADR-035-remove-per-agent-sandbox-profile) and must be rejected loudly
// rather than silently dropped.
//
// Before the SandboxProfile removal, updateAgent had an unconditional
// Go-level guard rejecting sandbox_profile=off with 400. That guard (and the
// field it read) were deleted along with gen.AgentUpdateRequest.SandboxProfile.
// decodeAndValidate's fast path (validate_inbound defaults false) is a plain
// json.Decode with no DisallowUnknownFields, so a client that still sends
// sandbox_profile would have it silently dropped and the PUT would report 200
// with the agent otherwise unchanged — a downgrade from "loud 400" to "silent
// no-op". This test pins the raw-body sniff added to updateAgent that
// restores the loud rejection.
//
// Traces to: 7-reviewer gate on the SandboxProfile-removal diff, Fix 2
// (silent-failure-hunter, cross-referenced by pr-test-analyzer).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const wantSandboxProfileRetiredMsg = "sandbox_profile is retired — use the global god-mode switch (POST /api/v1/gateway/god-mode)"

// runUpdateAgentSandboxProfileRejected400 is the shared assertion for both
// retired sandbox_profile values: a PUT carrying sandbox_profile=value is
// rejected with 400 and the exact message pointing callers at the global
// god-mode switch, and the agent's persisted config is genuinely unchanged
// (not just that the response was 400 — this asserts no side effect occurred
// either way).
func runUpdateAgentSandboxProfileRejected400(t *testing.T, value string) {
	t.Helper()
	api := buildGodModeTestAPI(t, false /* allowGodMode — not relevant for this check */)

	before, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)

	body := `{"sandbox_profile":"` + value + `"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"sandbox_profile=%s must be rejected with 400; body: %s", value, w.Body.String())

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, wantSandboxProfileRetiredMsg, resp["error"])

	// No side effect: config.json on disk must be byte-identical to before.
	after, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"config.json must be unchanged after a rejected sandbox_profile PUT")

	// No side effect: the in-memory agent must also be unchanged.
	cfg := api.agentLoop.GetConfig()
	require.Len(t, cfg.Agents.List, 1)
	assert.Equal(t, "Test Agent", cfg.Agents.List[0].Name)
	assert.Nil(t, cfg.Agents.List[0].UpdatedAt, "UpdatedAt must not be set by a rejected PUT")
}

// TestUpdateAgent_SandboxProfileOff_Rejected400 verifies that a PUT carrying
// the retired sandbox_profile="off" field is rejected with 400.
func TestUpdateAgent_SandboxProfileOff_Rejected400(t *testing.T) {
	runUpdateAgentSandboxProfileRejected400(t, "off")
}

// TestUpdateAgent_SandboxProfileWorkspace_Rejected400 verifies the same
// rejection for a different (also-retired) sandbox_profile value, confirming
// the guard is not accidentally scoped to just "off".
func TestUpdateAgent_SandboxProfileWorkspace_Rejected400(t *testing.T) {
	runUpdateAgentSandboxProfileRejected400(t, "workspace")
}

// TestUpdateAgent_NoSandboxProfile_StillSucceeds is a negative control:
// confirms the raw-body sniff does not false-positive on ordinary field
// names (e.g. a field whose name merely contains "sandbox" as a substring is
// not the trigger — only the literal "sandbox_profile" key is).
func TestUpdateAgent_NoSandboxProfile_StillSucceeds(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"name":"Renamed Agent"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusOK, w.Code,
		"an ordinary update with no sandbox_profile key must still succeed; body: %s", w.Body.String())
}
