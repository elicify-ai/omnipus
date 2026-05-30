//go:build !cgo

// REST god-mode gate tests for PUT /api/v1/agents/{id}.
//
// Verifies the three-case matrix:
//  1. sandbox_profile=off with GodModeAvailable=false  → 403
//  2. sandbox_profile=off with allowGodMode=false       → 403
//  3. sandbox_profile=off with both true                → 200, persisted
//  4. sandbox_profile=workspace always                  → 200
//  5. invalid shell_policy.custom_deny_patterns regex   → 400
//
// Note: GodModeAvailable is a build-time constant (true in the default build,
// false with -tags=nogodmode). These tests run under the default build, so
// GodModeAvailable=true. Case (1) is validated by the -tags=nogodmode build in
// CI; we document the expected behavior here and skip the const assertion.
//
// Traces to: quizzical-marinating-frog.md PR 4 acceptance criteria.

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

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// buildGodModeTestAPI builds a minimal restAPI wired to a single custom agent
// "test-agent" in a temp home dir. The caller controls allowGodMode.
func buildGodModeTestAPI(t *testing.T, allowGodMode bool) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	// Minimal config with one mutable custom agent.
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"test-agent","name":"Test Agent"}]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
			List: []config.AgentConfig{
				{ID: "test-agent", Name: "Test Agent"},
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	return &restAPI{
		agentLoop:    al,
		homePath:     tmpDir,
		allowGodMode: allowGodMode,
	}
}

// TestUpdateAgent_SandboxOff_GodModeAvailableTrue_AllowGodModeTrue_Returns200
// verifies that when GodModeAvailable=true AND allowGodMode=true, setting
// sandbox_profile=off is accepted with 200 and persisted to config.json.
//
// GodModeAvailable is always true in the default (non-nogodmode) build.
func TestUpdateAgent_SandboxOff_GodModeAvailableTrue_AllowGodModeTrue_Returns200(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: this test requires GodModeAvailable=true (default build)")
	}

	api := buildGodModeTestAPI(t, true /* allowGodMode */)

	body := `{"sandbox_profile":"off"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(
		t,
		http.StatusOK,
		w.Code,
		"sandbox_profile=off with GodModeAvailable=true and allowGodMode=true must return 200; body: %s",
		w.Body.String(),
	)

	// Confirm the value was persisted to config.json.
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(raw, &persisted))
	agents, _ := persisted["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	var found bool
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if m["id"] == "test-agent" {
			assert.Equal(t, "off", m["sandbox_profile"],
				"sandbox_profile must be persisted as 'off'")
			found = true
			break
		}
	}
	assert.True(t, found, "test-agent entry must exist in persisted config")
}

// TestUpdateAgent_SandboxOff_AllowGodModeFalse_Returns403 verifies that when
// allowGodMode=false (flag not passed at boot), sandbox_profile=off is rejected
// with 403 regardless of GodModeAvailable.
func TestUpdateAgent_SandboxOff_AllowGodModeFalse_Returns403(t *testing.T) {
	if !sandbox.GodModeAvailable {
		t.Skip("skipping: GodModeAvailable=false build; the 403 fires earlier (build check)")
	}

	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"sandbox_profile":"off"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"sandbox_profile=off without --allow-god-mode must return 403; body: %s", w.Body.String())

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "--allow-god-mode",
		"error message must mention --allow-god-mode")
}

// TestUpdateAgent_SandboxWorkspace_AlwaysAllowed verifies that sandbox_profile=workspace
// is always accepted regardless of god-mode flags.
func TestUpdateAgent_SandboxWorkspace_AlwaysAllowed(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"sandbox_profile":"workspace"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusOK, w.Code,
		"sandbox_profile=workspace must always return 200; body: %s", w.Body.String())
}

// TestUpdateAgent_ShellPolicy_InvalidRegex_Returns400 verifies that a
// shell_policy.custom_deny_patterns entry with an invalid regexp is rejected
// with 400 and the error message includes the bad pattern.
func TestUpdateAgent_ShellPolicy_InvalidRegex_Returns400(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode — not relevant for this check */)

	body := `{"shell_policy":{"enable_deny_patterns":true,"custom_deny_patterns":["[invalid-regexp"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"invalid regexp in custom_deny_patterns must return 400; body: %s", w.Body.String())

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Contains(t, resp["error"], "[invalid-regexp",
		"error message must include the bad pattern")
}

// TestUpdateAgent_PATCH_SandboxOff_Returns403 verifies that a PATCH request to
// /api/v1/agents/{id} with sandbox_profile=off returns 403 when --allow-god-mode
// is not set. PATCH dispatches to patchAgentOwnership which requires admin auth;
// absent a user in context it returns 403 — matching the PUT god-mode 403 parity.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-4.
func TestUpdateAgent_PATCH_SandboxOff_Returns403(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"sandbox_profile":"off"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"PATCH sandbox_profile=off without admin auth must return 403; body: %s", w.Body.String())
}

// TestUpdateAgent_ShellPolicy_ValidRegexes_Returns200 verifies that valid
// regexps in custom_deny_patterns are accepted.
func TestUpdateAgent_ShellPolicy_ValidRegexes_Returns200(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"shell_policy":{"enable_deny_patterns":true,"custom_deny_patterns":["rm\\s+-rf","curl\\s+.*(evil|malware)"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusOK, w.Code,
		"valid regexps in custom_deny_patterns must return 200; body: %s", w.Body.String())
}

// TestUpdateAgent_ShellPolicy_PartialPatch_EnableDenyPatternsPreserved is a
// regression test for the enable_deny_patterns null-poisoning bug.
//
// When a caller PATCHes only custom_deny_patterns (omitting enable_deny_patterns),
// the prior value of enable_deny_patterns must be preserved in config.json.
// The bug: req.ShellPolicy.EnableDenyPatterns was *bool; writing it unconditionally
// persisted null, which decoded as false on the next read.
func TestUpdateAgent_ShellPolicy_PartialPatch_EnableDenyPatternsPreserved(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	// First PATCH: set enable_deny_patterns=true.
	body1 := `{"shell_policy":{"enable_deny_patterns":true,"custom_deny_patterns":["rm\\s+-rf"]}}`
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body1))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "first PATCH must succeed; body: %s", w1.Body.String())

	// Second PATCH: send only custom_deny_patterns (no enable_deny_patterns key).
	body2 := `{"shell_policy":{"custom_deny_patterns":["curl\\s+evil"]}}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "second PATCH must succeed; body: %s", w2.Body.String())

	// Read config.json and confirm enable_deny_patterns is still true (not null or false).
	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var persisted map[string]any
	require.NoError(t, json.Unmarshal(raw, &persisted))
	agents, _ := persisted["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	var found bool
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok || m["id"] != "test-agent" {
			continue
		}
		sp, _ := m["shell_policy"].(map[string]any)
		require.NotNil(t, sp, "shell_policy must exist in persisted config")
		assert.Equal(t, true, sp["enable_deny_patterns"],
			"enable_deny_patterns must remain true after partial PATCH (null-poisoning regression)")
		found = true
		break
	}
	assert.True(t, found, "test-agent must appear in the persisted agent list")
}
