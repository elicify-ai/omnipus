package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// runnerTestAgents builds an AgentLoop whose config carries agents with distinct
// executors so the runner-test endpoint exercises each branch.
func runnerTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native", Type: config.AgentTypeCustom},
				{
					ID: "ext-no-cli", Name: "ExtNoCLI", Type: config.AgentTypeCustom,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: ""},
					},
				},
				{
					ID: "ext-claude", Name: "ExtClaude", Type: config.AgentTypeCustom,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

func postRunnerTest(t *testing.T, api *restAPI, agentID string) (int, gen.RunnerTestResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents/"+agentID+"/runner/test", nil)
	api.HandleAgents(w, r)
	var resp gen.RunnerTestResponse
	if w.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	}
	return w.Code, resp
}

// TestRunnerTestEndpoint_NotExternalCLI: a native agent has no runner to test.
func TestRunnerTestEndpoint_NotExternalCLI(t *testing.T) {
	api := runnerTestAPI(t)
	code, resp := postRunnerTest(t, api, "native-agent")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok)
	assert.Equal(t, gen.NotExternalCli, resp.Reason)
}

// TestRunnerTestEndpoint_UnknownCLI: external-cli with an empty cli value.
func TestRunnerTestEndpoint_EmptyCLI(t *testing.T) {
	api := runnerTestAPI(t)
	code, resp := postRunnerTest(t, api, "ext-no-cli")
	require.Equal(t, http.StatusOK, code)
	assert.False(t, resp.Ok)
	assert.Equal(t, gen.UnknownCli, resp.Reason)
}

// TestRunnerTestEndpoint_MissingBinary: claude-code configured but the binary is
// not on PATH in CI → the distinct missing-binary reason (or unauthenticated if a
// claude binary happens to exist on the runner). Both are valid non-OK states; the
// key contract is that the test ran and produced a distinct, actionable reason.
func TestRunnerTestEndpoint_ExternalCLI_RunsCheck(t *testing.T) {
	api := runnerTestAPI(t)
	code, resp := postRunnerTest(t, api, "ext-claude")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "claude-code", deref(resp.Cli))
	// In CI the claude binary is absent → missing-binary. If a developer has it
	// installed, the result is handshake/unauthenticated/ok — never not-external-cli.
	assert.NotEqual(t, gen.NotExternalCli, resp.Reason)
}

// TestRunnerTestEndpoint_NotFound: unknown agent → 404.
func TestRunnerTestEndpoint_NotFound(t *testing.T) {
	api := runnerTestAPI(t)
	code, _ := postRunnerTest(t, api, "does-not-exist")
	require.Equal(t, http.StatusNotFound, code)
}

// TestRunnerTestEndpoint_MethodNotAllowed: GET is rejected.
func TestRunnerTestEndpoint_MethodNotAllowed(t *testing.T) {
	api := runnerTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/ext-claude/runner/test", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
