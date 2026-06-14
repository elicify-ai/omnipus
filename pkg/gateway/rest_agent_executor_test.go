//go:build !cgo

// REST executor round-trip tests for the sub-agent Executor (kind/cli) field.
//
// Regression coverage for the review finding that req.Executor was never mapped to
// config.AgentConfig.Subagents.Executor: it was write-dropped on create/update and
// never echoed on GET. A GET→edit→PUT round-trip would silently erase it.
//
// These tests prove the full path:
//  1. POST /agents with executor → response echoes it AND it persists to config.json
//  2. GET /agents/{id} → response shows the persisted executor
//  3. PUT /agents/{id} with an UNRELATED field → executor is preserved (round-trip)
//  4. PUT /agents/{id} with executor → updates it
//  5. Invalid executor (external-cli with no cli / bad cli / bad kind) → 400

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// buildExecutorTestAPI builds a minimal restAPI over a temp home with one mutable
// custom agent already present, so create/update/get all operate on a real
// config.json that safeUpdateConfigJSON can read-modify-write.
func buildExecutorTestAPI(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.json")
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[{"id":"test-agent","name":"Test Agent","type":"custom"}]}}`
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
				{ID: "test-agent", Name: "Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	return &restAPI{
		agentLoop: al,
		homePath:  tmpDir,
	}
}

// decodeAgentResp decodes an httptest response body as a gen.Agent.
func decodeAgentResp(t *testing.T, body []byte) gen.Agent {
	t.Helper()
	var ag gen.Agent
	require.NoError(t, json.Unmarshal(body, &ag), "decode Agent response: %s", string(body))
	return ag
}

// TestCreateAgent_ExecutorPersistsAndEchoes proves POST maps + persists + echoes
// the executor (the field was previously write-dropped).
func TestCreateAgent_ExecutorPersistsAndEchoes(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Delegator","executor":{"kind":"external-cli","cli":"codex"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, created.Executor, "create response must echo the executor")
	assert.Equal(t, gen.AgentExecutorKindExternalCli, created.Executor.Kind)
	require.NotNil(t, created.Executor.Cli)
	assert.Equal(t, gen.AgentExecutorCli("codex"), *created.Executor.Cli)

	// Persisted to config.json under subagents.executor.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, created.Id)
	require.NotNil(t, exec, "executor not persisted to config.json: %s", string(raw))
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "codex", exec["cli"])
}

// TestGetEditPut_ExecutorRoundTripPreserved is the core regression: create with an
// executor, GET shows it, then a PUT that does NOT touch the executor must preserve
// it (the GET→edit→PUT round-trip must not erase it).
func TestGetEditPut_ExecutorRoundTripPreserved(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// 1. Create with executor=external-cli/claude-code.
	createBody := `{"name":"Delegator","executor":{"kind":"external-cli","cli":"claude-code"}}`
	cw := httptest.NewRecorder()
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(createBody))
	cr.Header.Set("Content-Type", "application/json")
	api.HandleAgents(cw, cr)
	require.Equal(t, http.StatusCreated, cw.Code, "create body: %s", cw.Body.String())
	id := decodeAgentResp(t, cw.Body.Bytes()).Id

	// 2. GET shows the executor.
	gw := httptest.NewRecorder()
	gr := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+id, nil)
	api.HandleAgents(gw, gr)
	require.Equal(t, http.StatusOK, gw.Code, "get body: %s", gw.Body.String())
	got := decodeAgentResp(t, gw.Body.Bytes())
	require.NotNil(t, got.Executor, "GET must echo the persisted executor")
	assert.Equal(t, gen.AgentExecutorKindExternalCli, got.Executor.Kind)
	require.NotNil(t, got.Executor.Cli)
	assert.Equal(t, gen.AgentExecutorCli("claude-code"), *got.Executor.Cli)

	// 3. PUT an UNRELATED field (description) — must NOT erase the executor.
	putBody := `{"description":"now with a description"}`
	pw := httptest.NewRecorder()
	pr := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(putBody))
	pr.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw, pr)
	require.Equal(t, http.StatusOK, pw.Code, "put body: %s", pw.Body.String())
	afterPut := decodeAgentResp(t, pw.Body.Bytes())
	require.NotNil(t, afterPut.Executor, "executor erased by an unrelated PUT (round-trip regression)")
	assert.Equal(t, gen.AgentExecutorKindExternalCli, afterPut.Executor.Kind)
	require.NotNil(t, afterPut.Executor.Cli)
	assert.Equal(t, gen.AgentExecutorCli("claude-code"), *afterPut.Executor.Cli)

	// And it is still persisted on disk.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, id)
	require.NotNil(t, exec, "executor missing from config.json after unrelated PUT: %s", string(raw))
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "claude-code", exec["cli"])
}

// TestUpdateAgent_ExecutorChanges proves PUT can change the executor's cli.
func TestUpdateAgent_ExecutorChanges(t *testing.T) {
	api := buildExecutorTestAPI(t)

	createBody := `{"name":"Delegator","executor":{"kind":"external-cli","cli":"codex"}}`
	cw := httptest.NewRecorder()
	cr := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(createBody))
	cr.Header.Set("Content-Type", "application/json")
	api.HandleAgents(cw, cr)
	require.Equal(t, http.StatusCreated, cw.Code)
	id := decodeAgentResp(t, cw.Body.Bytes()).Id

	putBody := `{"executor":{"kind":"external-cli","cli":"opencode"}}`
	pw := httptest.NewRecorder()
	pr := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(putBody))
	pr.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw, pr)
	require.Equal(t, http.StatusOK, pw.Code, "put body: %s", pw.Body.String())
	updated := decodeAgentResp(t, pw.Body.Bytes())
	require.NotNil(t, updated.Executor)
	require.NotNil(t, updated.Executor.Cli)
	assert.Equal(t, gen.AgentExecutorCli("opencode"), *updated.Executor.Cli)
}

// TestCreateAgent_InvalidExecutor_400 proves the validator rejects bad executors.
func TestCreateAgent_InvalidExecutor_400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"external-cli with no cli", `{"name":"X","executor":{"kind":"external-cli"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "want 400; body: %s", w.Body.String())
		})
	}
}

// TestExecutorConfigFromRequest_Validation unit-tests the mapping/validation helper
// directly (no HTTP), covering each kind + cli combination.
func TestExecutorConfigFromRequest_Validation(t *testing.T) {
	t.Run("native returns nil cleanly", func(t *testing.T) {
		ec, msg := executorConfigFromRequest("native", "")
		assert.Empty(t, msg)
		assert.Nil(t, ec)
	})
	t.Run("empty kind defaults to native (nil)", func(t *testing.T) {
		ec, msg := executorConfigFromRequest("", "")
		assert.Empty(t, msg)
		assert.Nil(t, ec)
	})
	t.Run("external-cli requires cli", func(t *testing.T) {
		ec, msg := executorConfigFromRequest("external-cli", "")
		assert.NotEmpty(t, msg)
		assert.Nil(t, ec)
	})
	t.Run("external-cli rejects unknown cli", func(t *testing.T) {
		ec, msg := executorConfigFromRequest("external-cli", "not-a-cli")
		assert.NotEmpty(t, msg)
		assert.Nil(t, ec)
	})
	t.Run("external-cli accepts each supported cli", func(t *testing.T) {
		for _, cli := range []string{"claude-code", "codex", "opencode"} {
			ec, msg := executorConfigFromRequest("external-cli", cli)
			assert.Empty(t, msg, cli)
			require.NotNil(t, ec, cli)
			assert.Equal(t, config.ExecutorKindExternalCLI, ec.Kind)
			assert.Equal(t, cli, ec.CLI)
		}
	})
	t.Run("remote-a2a accepted but reserved", func(t *testing.T) {
		ec, msg := executorConfigFromRequest("remote-a2a", "")
		assert.Empty(t, msg)
		require.NotNil(t, ec)
		assert.Equal(t, config.ExecutorKindRemoteA2A, ec.Kind)
	})
	t.Run("unknown kind rejected", func(t *testing.T) {
		ec, msg := executorConfigFromRequest("bogus", "")
		assert.NotEmpty(t, msg)
		assert.Nil(t, ec)
	})
}

// findExecutorInConfig digs agents.list[id].subagents.executor out of a parsed
// config.json map. Returns nil when absent.
func findExecutorInConfig(t *testing.T, m map[string]any, id string) map[string]any {
	t.Helper()
	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		return nil
	}
	list, _ := agents["list"].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		if entry == nil {
			continue
		}
		if entry["id"] != id {
			continue
		}
		sub, _ := entry["subagents"].(map[string]any)
		if sub == nil {
			return nil
		}
		exec, _ := sub["executor"].(map[string]any)
		return exec
	}
	return nil
}
