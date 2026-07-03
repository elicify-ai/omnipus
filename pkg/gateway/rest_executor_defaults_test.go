// rest_executor_defaults_test.go — coverage for GET /api/v1/agents/executor-defaults
// (Agent System ghost-text bug fix: exposes the REAL, byte-accurate per-CLI
// auto-applied flags that pkg/agent/runner/driver_{claude,codex,opencode}.go's
// buildArgs() actually appends, instead of leaving operators with only static
// HTML placeholder ghost-text in the Agent Profile UI).

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// executorDefaultsTestAPI builds a minimal restAPI. listExecutorDefaults is
// static reference data with no config/agent dependency, so an empty agent
// list is sufficient.
func executorDefaultsTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{ModelName: "test-model", MaxTokens: 4096},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

func getExecutorDefaults(t *testing.T, api *restAPI) (int, []gen.ExecutorDefaults) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/executor-defaults", nil)
	api.HandleAgents(w, r)
	var resp []gen.ExecutorDefaults
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w.Code, resp
}

// TestListExecutorDefaults_ReturnsAllThreeCLIs proves the endpoint returns
// exactly one entry per supported subagent_3p external CLI, each with a
// non-empty, ordered flag list and a non-empty note.
func TestListExecutorDefaults_ReturnsAllThreeCLIs(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	code, entries := getExecutorDefaults(t, api)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, entries, 3)

	seen := map[gen.ExecutorDefaultsCli]gen.ExecutorDefaults{}
	for _, e := range entries {
		seen[e.Cli] = e
		assert.NotEmpty(t, e.AutoAppliedFlags, "cli %q must have a non-empty flag list", e.Cli)
		assert.NotEmpty(t, e.Notes, "cli %q must have a non-empty notes field", e.Cli)
	}
	for _, cli := range []gen.ExecutorDefaultsCli{
		gen.ExecutorDefaultsCliClaudeCode,
		gen.ExecutorDefaultsCliCodex,
		gen.ExecutorDefaultsCliOpencode,
	} {
		_, ok := seen[cli]
		assert.True(t, ok, "expected an entry for cli %q", cli)
	}
}

// TestListExecutorDefaults_ClaudeFlagsByteAccurate cross-checks the claude
// entry against driver_claude.go's buildArgs() (ADR-032 fix C/D): -p,
// --output-format stream-json, --verbose, --no-chrome, a conditional
// --model, --permission-mode acceptEdits, and a conditional --max-turns —
// in that order. --dangerously-skip-permissions must never appear (FR-5.3).
func TestListExecutorDefaults_ClaudeFlagsByteAccurate(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	var claude gen.ExecutorDefaults
	for _, e := range entries {
		if e.Cli == gen.ExecutorDefaultsCliClaudeCode {
			claude = e
		}
	}
	require.NotEmpty(t, claude.AutoAppliedFlags)

	want := []string{"-p", "--output-format stream-json", "--verbose", "--no-chrome"}
	require.GreaterOrEqual(t, len(claude.AutoAppliedFlags), len(want))
	for i, w := range want {
		assert.Equal(t, w, claude.AutoAppliedFlags[i], "flag order must match buildArgs")
	}
	joined := ""
	for _, f := range claude.AutoAppliedFlags {
		joined += f + "\n"
	}
	assert.Contains(t, joined, "--permission-mode acceptEdits")
	assert.NotContains(t, joined, "--dangerously-skip-permissions",
		"FR-5.3/US-5: claude driver never passes --dangerously-skip-permissions")
}

// TestListExecutorDefaults_CodexFlagsByteAccurate cross-checks the codex
// entry against driver_codex.go's buildArgs(): --ask-for-approval never
// MUST precede the exec subcommand (a global codex flag), --sandbox
// workspace-write, --skip-git-repo-check, --color never all follow.
// --dangerously-bypass-approvals-and-sandbox must never appear (FR-5.3).
func TestListExecutorDefaults_CodexFlagsByteAccurate(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	var codex gen.ExecutorDefaults
	for _, e := range entries {
		if e.Cli == gen.ExecutorDefaultsCliCodex {
			codex = e
		}
	}
	require.NotEmpty(t, codex.AutoAppliedFlags)

	want := []string{"--ask-for-approval never", "exec", "--json", "--sandbox workspace-write",
		"--skip-git-repo-check", "--color never"}
	require.GreaterOrEqual(t, len(codex.AutoAppliedFlags), len(want))
	for i, w := range want {
		assert.Equal(t, w, codex.AutoAppliedFlags[i], "flag order must match buildArgs")
	}
	joined := ""
	for _, f := range codex.AutoAppliedFlags {
		joined += f + "\n"
	}
	assert.NotContains(t, joined, "--dangerously-bypass-approvals-and-sandbox",
		"FR-5.3: codex driver never passes --dangerously-bypass-approvals-and-sandbox")
}

// TestListExecutorDefaults_OpencodeFlagsByteAccurate cross-checks the
// opencode entry against driver_opencode.go's buildArgs(): run --format
// json, a conditional --model, --dangerously-skip-permissions (the
// deliberate ADR-032 fix D posture for THIS CLI only), and the trailing "--"
// end-of-options separator (N-1) as the LAST entry.
func TestListExecutorDefaults_OpencodeFlagsByteAccurate(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	_, entries := getExecutorDefaults(t, api)
	var opencode gen.ExecutorDefaults
	for _, e := range entries {
		if e.Cli == gen.ExecutorDefaultsCliOpencode {
			opencode = e
		}
	}
	require.NotEmpty(t, opencode.AutoAppliedFlags)

	want := []string{"run", "--format json"}
	require.GreaterOrEqual(t, len(opencode.AutoAppliedFlags), len(want))
	for i, w := range want {
		assert.Equal(t, w, opencode.AutoAppliedFlags[i], "flag order must match buildArgs")
	}
	joined := ""
	for _, f := range opencode.AutoAppliedFlags {
		joined += f + "\n"
	}
	assert.Contains(t, joined, "--dangerously-skip-permissions")
	last := opencode.AutoAppliedFlags[len(opencode.AutoAppliedFlags)-1]
	assert.Equal(t, "--", last, "the -- end-of-options separator must be the last auto-applied flag (N-1)")
}

// TestListExecutorDefaults_MethodNotAllowed proves POST is rejected — this
// is read-only reference data, not a writable resource.
func TestListExecutorDefaults_MethodNotAllowed(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/executor-defaults", nil)
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestListExecutorDefaults_DoesNotShadowAgentLookup proves "executor-defaults"
// as a reserved static path segment does not accidentally swallow requests
// for a real agent whose ID happens to be a normal (different) string — i.e.
// the generic agent GET path still works for an unrelated agent ID.
func TestListExecutorDefaults_DoesNotShadowAgentLookup(t *testing.T) {
	api := executorDefaultsTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/some-other-agent-id", nil)
	api.HandleAgents(w, r)
	// Not found (no such agent) — proves this fell through to the generic
	// getAgent path rather than being intercepted as executor-defaults.
	assert.Equal(t, http.StatusNotFound, w.Code)
}
