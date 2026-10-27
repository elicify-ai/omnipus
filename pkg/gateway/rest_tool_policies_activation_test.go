package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Oracle: fix-round-1 dispatch item 4 / SFH F1. The requested 503 and exact
// recovery copy follow SFH's recommendation, pending backend contract alignment.
// Persistence, handler, reload wait/outcome, registry and request assembly are
// real. Only the paid provider is scripted; a file blocks the new session-store
// location on the first rebuild, so the failure is produced, not pre-recorded.
func TestPutToolPoliciesSavedButNotActiveThenRetryRefreshesGuidance(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Tools.Exec.EnableProxy = false
	cfg.Sandbox.ToolPolicies["set_todos"] = "allow"
	cfg.Agents.List = []config.AgentConfig{{ID: "activation-agent", Name: "Activation Agent", Type: config.AgentTypeCustom}}
	provider := testutil.NewScenario().WithText("Before save").WithText("After failed activation").WithText("After retry")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	loop := mustAgentLoop(t, cfg, msgBus, provider)
	api := &restAPI{agentLoop: loop, homePath: home, reloadOutcome: &reloadOutcomeTracker{}}
	require.NoError(t, config.SaveConfig(api.configPath(), cfg))
	seedAgentEntities(t, home, cfg.Agents.List)

	oldRegistry := loop.GetRegistry()
	oldAgent, ok := oldRegistry.GetAgent("activation-agent")
	require.True(t, ok)
	_, registered := oldAgent.Tools.Get("set_todos")
	require.True(t, registered, "positive control: real shared task-tool registration")
	assertPolicyActivationRequestGuidance(t, api, provider, "before-save", "Before save", true)

	blockedHome := filepath.Join(home, "blocked-rebuild-home")
	require.NoError(t, os.WriteFile(blockedHome, []byte("not a directory"), 0o600))
	attempts := 0
	var rebuildErr error
	loop.SetReloadFunc(func() error {
		attempts++
		loop.MarkReloadPending()
		defer loop.ClearReloadPending()
		seq := api.reloadOutcome.begin()
		next, err := config.LoadConfig(api.configPath())
		if err == nil {
			err = populateAgentsListFromEntityStoreStrict(next, home)
		}
		if err == nil {
			if attempts == 1 {
				// The registry derives its shared-store home from the parent of
				// Defaults.Home. This private path is a regular file, so opening
				// its sessions directory must fail without touching the live store.
				next.Agents.Defaults.Home = filepath.Join(blockedHome, "agents")
			}
			err = loop.ReloadProviderAndConfig(context.Background(), provider, next)
		}
		rebuildErr = err
		api.reloadOutcome.finish(seq, err, api.reloadOutcome.markConfigRead())
		// Same asynchronous contract as the gateway queue: enqueue succeeds;
		// execution failure is observed through the real outcome tracker.
		return nil
	})

	policies := make(map[string]gen.GlobalToolPoliciesPolicies, len(cfg.Sandbox.ToolPolicies))
	for name, policy := range cfg.Sandbox.ToolPolicies {
		policies[name] = gen.GlobalToolPoliciesPolicies(policy)
	}
	policies["set_todos"] = gen.GlobalToolPoliciesPolicies("deny")
	body, err := json.Marshal(gen.GlobalToolPolicies{Policies: policies})
	require.NoError(t, err)
	put := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/tool-policies", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		api.HandleToolPolicies(w, withAdminRole(r))
		return w
	}
	failed := put()
	require.Equal(t, 1, attempts, "the HTTP save must actually attempt activation")
	require.ErrorContains(t, rebuildErr, "registry creation failed", "inject an actual failed registry construction")
	require.ErrorContains(t, rebuildErr, "shared session store", "failure must be at the filesystem boundary")
	require.True(t, api.reloadOutcome.lastFailed(), "failed execution must be recorded before pending clears")
	require.False(t, loop.IsReloadPending())
	require.Same(t, oldRegistry, loop.GetRegistry(), "failed rebuild must not publish a partial registry")
	persisted, err := config.LoadConfig(api.configPath())
	require.NoError(t, err)
	require.Equal(t, "deny", persisted.Sandbox.ToolPolicies["set_todos"], "failed activation is not a rolled-back save")
	assertPolicyActivationRequestGuidance(t, api, provider, "after-failure", "After failed activation", true)

	// Nonfatal assertions let the retry and next-request oracle execute on the
	// unfixed handler too. They remain real failures, not swallowed errors.
	assert.Equal(t, http.StatusServiceUnavailable, failed.Code, "saved-but-not-active must not be ordinary HTTP 200: %s", failed.Body.String())
	assert.Equal(t, "application/json", failed.Header().Get("Content-Type"))
	var failure gen.ErrorResponse
	require.NoError(t, json.Unmarshal(failed.Body.Bytes(), &failure))
	assert.Equal(t, "The settings were saved, but could not be applied to running agents. Retry applying the change, or restart Omnipus.", failure.Error)

	retried := put()
	require.Equal(t, http.StatusOK, retried.Code, "successful retry: %s", retried.Body.String())
	require.Equal(t, 2, attempts)
	require.NoError(t, rebuildErr, "retry must perform a successful real rebuild")
	require.False(t, api.reloadOutcome.lastFailed(), "successful retry must replace the previous failed outcome")
	require.NotSame(t, oldRegistry, loop.GetRegistry(), "successful retry must publish the rebuilt registry")
	var response gen.GlobalToolPolicies
	require.NoError(t, json.Unmarshal(retried.Body.Bytes(), &response))
	require.Equal(t, gen.GlobalToolPolicies{Policies: policies}, response)
	assertPolicyActivationRequestGuidance(t, api, provider, "after-retry", "After retry", false)
}

func assertPolicyActivationRequestGuidance(t *testing.T, api *restAPI, provider *testutil.ScenarioProvider, sessionID, answer string, enabled bool) {
	t.Helper()
	before := provider.CallCount()
	got, err := api.agentLoop.ProcessDirectAs(context.Background(), "activation-agent", "Give a one-line answer.", "policy-activation-"+sessionID)
	require.NoError(t, err, "real next-request routing must reach the provider")
	require.Equal(t, answer, got)
	require.Equal(t, before+1, provider.CallCount(), "exactly one real provider request must be assembled")
	messages := provider.LastMessages()
	require.NotEmpty(t, messages)
	require.Equal(t, "system", messages[0].Role)
	// SPEC CHANGE 2026-10-24 (Q74): the approved rule-3 text now asks for the
	// required active_form on every item (prompt text merged at 82d204711).
	const rule = "3. For every request, including one-line answers, call set_todos with your own checklist, giving every item its required active_form (a present-participle phrase) and marking the current item in_progress, before your first reply or other tool call, then update the list as work advances."
	if enabled {
		require.Contains(t, messages[0].Content, rule, "approved Q2 guidance must be visible to the actual request")
	} else {
		require.NotContains(t, messages[0].Content, "set_todos", "first request after successful retry must not receive denied-tool guidance")
		require.Contains(t, messages[0].Content, "3. Perform actions with the appropriate tool.", "remaining rules must renumber without a gap")
	}
}
