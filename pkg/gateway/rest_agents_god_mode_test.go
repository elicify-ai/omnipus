// REST shell-policy gate tests for PUT /api/v1/agents/{id}.
//
// Verifies:
//  1. invalid shell_policy.custom_deny_patterns regex   → 400
//  2. valid shell_policy.custom_deny_patterns regexes   → 200
//
// Traces to: docs/internal/uat/remediation-decisions.md O14.

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

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// buildGodModeTestAPI builds a minimal restAPI wired to a single custom agent
// "test-agent" in a temp home dir. The caller controls allowGodMode.
//
// ADR-054: agents are per-entity records under entities/agents/<id>.json, not
// config.json's agents.list — updateAgent's persist step resolves and
// mutates the target exclusively via agentstore.Store.Update, which 404s
// with errAgentVanishedDuringUpdate ("agent no longer exists") when no
// matching entity file exists. "test-agent" is therefore seeded via BOTH the
// in-memory cfg.Agents.List literal (needed for mustAgentLoop's initial
// AgentRegistry construction and the pre-persist "does this agent exist /
// is it locked" checks, which read a.agentLoop.GetConfig()) AND a real
// agentstore entity record (needed for it to survive an actual write),
// mirroring the pattern proven in rest_test.go's seedAgentEntities and
// rest_agent_executor_test.go's buildExecutorTestAPI.
func buildGodModeTestAPI(t *testing.T, allowGodMode bool) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	// config.json on disk carries only agents.defaults — agents.list is
	// never read from disk by any production code path (ADR-054), so seeding
	// it here would only assert a stale, misleading shape.
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096}}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	testAgent := config.AgentConfig{ID: "test-agent", Name: "Test Agent"}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{testAgent},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// ADR-054: persist a matching entity record so updateAgent's
	// agentstore-backed persist step can find "test-agent" at all.
	testAgentForStore := testAgent
	require.NoError(t, agentstore.New(tmpDir).Create("test-agent", &testAgentForStore))

	return &restAPI{
		agentLoop:    al,
		homePath:     tmpDir,
		allowGodMode: allowGodMode,
	}
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

// TestUpdateAgent_PATCH_Returns405 verifies that a PATCH request to
// /api/v1/agents/{id} returns 405 Method Not Allowed. PATCH used to dispatch
// to patchAgentOwnership (the agent-ownership admin endpoint); that handler
// and its route registration were deleted with the rest of the multi-account
// scaffolding (single-user model), so PATCH now falls through HandleAgents'
// method switch (GET/PUT/DELETE only) to the default 405 case.
//
// Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-4.
func TestUpdateAgent_PATCH_Returns405(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"name":"irrelevant — method rejected before body is parsed"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code,
		"PATCH /api/v1/agents/{id} must return 405 (no PATCH handler registered anymore); body: %s", w.Body.String())
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

	// Read the entity store and confirm enable_deny_patterns is still true
	// (not null or false). ADR-054 + the config.AgentsConfig.List = `json:"-"`
	// follow-up: agents.list can never be marshaled into config.json, so the
	// persisted value must be read from the agentstore entity record instead.
	rec, err := agentstore.New(api.homePath).Get("test-agent")
	require.NoError(t, err, "test-agent must be persisted")
	require.NotNil(t, rec.ShellPolicy, "shell_policy must exist in persisted config")
	assert.True(t, rec.ShellPolicy.EnableDenyPatterns,
		"enable_deny_patterns must remain true after partial PATCH (null-poisoning regression)")
}

// TestUpdateAgent_ShellPolicy_EmptyArrayClearsPatterns is a regression test
// for the clear-path silent drop: an explicitly-sent EMPTY
// custom_deny_patterns array must overwrite (clear) the persisted list.
// The bug: a `len(...) > 0` guard skipped empty arrays, so deleting the last
// pattern in the SPA produced a 200 PUT whose delete was silently ignored —
// the stale pattern list resurfaced on the next read (found live 2026-07-03).
// Field-absent (nil) still preserves, per the partial-PATCH test above.
func TestUpdateAgent_ShellPolicy_EmptyArrayClearsPatterns(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	// Seed a pattern.
	body1 := `{"shell_policy":{"enable_deny_patterns":true,"custom_deny_patterns":["rm\\s+-rf"]}}`
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body1))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "seed PATCH must succeed; body: %s", w1.Body.String())

	// Clear with an explicit empty array.
	body2 := `{"shell_policy":{"custom_deny_patterns":[]}}`
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body2))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "clear PATCH must succeed; body: %s", w2.Body.String())

	rec, err := agentstore.New(api.homePath).Get("test-agent")
	require.NoError(t, err, "test-agent must be persisted")
	require.NotNil(t, rec.ShellPolicy, "shell_policy must exist in persisted config")
	assert.Empty(t, rec.ShellPolicy.CustomDenyPatterns,
		"custom_deny_patterns must be cleared by an explicit empty array")
	assert.True(t, rec.ShellPolicy.EnableDenyPatterns,
		"enable_deny_patterns must be untouched by the patterns-only clear")
}
