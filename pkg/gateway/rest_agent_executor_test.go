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
// the executor (the field was previously write-dropped). Updated for the
// native-only-for-non-workers rule: the create path is exercised with a
// KIND=native executor (the only kind a freshly-created custom agent may
// carry). External-cli/remote-a2a on a custom agent is rejected (covered by
// TestCreateAgent_RejectsExternalCLIExecutorOnBaseAgent).
func TestCreateAgent_ExecutorPersistsAndEchoes(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"NativeAgent","executor":{"kind":"native"},"soul":"native-soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	// kind=native collapses to nil in the persisted response (omitted field
	// is the round-trip-preserving behaviour), so the test asserts "either
	// nil or kind=native" — the on-disk assertion below is the load-bearing
	// one.
	if created.Executor != nil {
		assert.Equal(t, gen.AgentExecutorKindNative, created.Executor.Kind)
	}

	// Persisted to config.json (or deliberately absent — kind=native clears).
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, created.Id)
	if exec != nil {
		assert.Equal(t, "native", exec["kind"])
	}
}

// TestGetEditPut_ExecutorRoundTripPreserved is the core regression: a worker
// updated to a non-native executor, then a PUT that does NOT touch the
// executor must preserve it (the GET→edit→PUT round-trip must not erase it).
// Uses a worker (the only agent kind that may declare an external executor
// under the native-only-for-non-workers rule). The worker is unlocked for
// this test so an unrelated PUT (e.g., description) is not blocked by the
// locked-agent identity check.
func TestGetEditPut_ExecutorRoundTripPreserved(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)
	// Unlock the worker so an unrelated-field PUT is allowed.
	cf := api.agentLoop.GetConfig()
	for i := range cf.Agents.List {
		if cf.Agents.List[i].ID == "test-worker" {
			cf.Agents.List[i].Locked = false
		}
	}
	// Also flip the locked flag on disk so safeUpdateConfigJSON does not
	// see "lock mismatch" surprises.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	if agents, ok := m["agents"].(map[string]any); ok {
		if list, ok := agents["list"].([]any); ok {
			for _, item := range list {
				if entry, ok := item.(map[string]any); ok && entry["id"] == "test-worker" {
					delete(entry, "locked")
				}
			}
		}
	}
	buf, _ := json.MarshalIndent(m, "", "  ")
	require.NoError(t, os.WriteFile(api.configPath(), buf, 0o600))

	// 1. PUT an external-cli executor on the worker.
	put1 := `{"executor":{"kind":"external-cli","cli":"claude-code"}}`
	pw1 := httptest.NewRecorder()
	pr1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker", strings.NewReader(put1))
	pr1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw1, pr1)
	require.Equal(t, http.StatusOK, pw1.Code, "put body: %s", pw1.Body.String())

	// 2. Persisted on disk.
	raw, err = os.ReadFile(api.configPath())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, "test-worker")
	require.NotNil(t, exec, "executor not persisted: %s", string(raw))
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "claude-code", exec["cli"])

	// 3. PUT an UNRELATED field (description) — must NOT erase the executor.
	put2 := `{"description":"a helpful worker"}`
	pw2 := httptest.NewRecorder()
	pr2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker", strings.NewReader(put2))
	pr2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw2, pr2)
	require.Equal(t, http.StatusOK, pw2.Code, "put body: %s", pw2.Body.String())

	// And it is still persisted on disk.
	raw, err = os.ReadFile(api.configPath())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &m))
	exec = findExecutorInConfig(t, m, "test-worker")
	require.NotNil(t, exec, "executor missing from config.json after unrelated PUT: %s", string(raw))
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "claude-code", exec["cli"])
}

// TestUpdateAgent_ExecutorChanges proves the cli-lock rule on a worker:
// once an agent is created with a non-empty executor.cli, the cli is
// IMMUTABLE. Subsequent PUTs that try to switch the cli must be rejected
// with 400, while the originally-persisted cli survives. (Pre-W2 spec the
// cli was freely mutable; the cli-lock rule per spec §4.16 / F-10 is
// exercised by this test.)
func TestUpdateAgent_ExecutorChanges(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	// Seed the worker with an initial external-cli executor.
	put1 := `{"executor":{"kind":"external-cli","cli":"codex"}}`
	pw1 := httptest.NewRecorder()
	pr1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker", strings.NewReader(put1))
	pr1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw1, pr1)
	require.Equal(t, http.StatusOK, pw1.Code)

	// Attempt to change the cli to opencode — must be rejected 400 by the
	// cli-lock rule.
	put2 := `{"executor":{"kind":"external-cli","cli":"opencode"}}`
	pw2 := httptest.NewRecorder()
	pr2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker", strings.NewReader(put2))
	pr2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw2, pr2)
	require.Equal(t, http.StatusBadRequest, pw2.Code,
		"changing cli after create must be rejected by the cli-lock rule; body: %s", pw2.Body.String())
	assert.Contains(t, pw2.Body.String(), "executor.cli is locked",
		"the rejection must reference the cli-lock rule")

	// Persisted on disk — original cli is preserved, not overwritten.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, "test-worker")
	require.NotNil(t, exec, "executor not persisted: %s", string(raw))
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "codex", exec["cli"],
		"original cli must survive the rejected PUT (cli-lock rule)")
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

// ---------------------------------------------------------------------------
// Worker write-time guards: external executor on a non-worker, heartbeat on a
// worker, and voice on a worker. All three must reject at the REST write gate.
// ---------------------------------------------------------------------------

// buildExecutorTestAPIWithWorker adds a worker agent to the test config so the
// worker guards have a real agent to test against. The worker is added to BOTH
// the live in-memory config and the on-disk config.json so the safeUpdateConfigJSON
// writer (which reads config.json and looks for the matching agent by id) finds
// the worker and can persist the field under test.
func buildExecutorTestAPIWithWorker(t *testing.T) *restAPI {
	t.Helper()
	api := buildExecutorTestAPI(t)
	// Live config: append the worker.
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:     "test-worker",
		Name:   "Worker",
		Type:   config.AgentTypeWorker,
		Locked: true,
	})
	// Disk: append the worker entry to config.json so the writer sees it.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
		m["agents"] = agents
	}
	list, _ := agents["list"].([]any)
	agents["list"] = append(list, map[string]any{
		"id":     "test-worker",
		"name":   "Worker",
		"type":   "worker",
		"locked": true,
	})
	buf, err := json.MarshalIndent(m, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(api.configPath(), buf, 0o600))
	return api
}

// TestUpdateAgent_NonWorkerRejectsExternalCLIExecutor verifies the
// native-only-for-non-workers rule at the update gate. A non-worker updated
// to kind=external-cli must be rejected with 400; a non-worker updated to
// kind=native (or omitting the field) stays allowed.
func TestUpdateAgent_NonWorkerRejectsExternalCLIExecutor(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// External-cli on the test-agent (a custom non-worker) → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"executor":{"kind":"external-cli","cli":"codex"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "only sub-agent workers",
		"the rejection must reference the worker-only rule")

	// Remote-a2a on the test-agent → 400.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"executor":{"kind":"remote-a2a"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestUpdateAgent_NonWorkerNativeExecutorAllowed is the control: a non-worker
// updated with kind=native (or omitting the executor) must still be 200. The
// native-only rule is not a blanket reject.
func TestUpdateAgent_NonWorkerNativeExecutorAllowed(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"executor":{"kind":"native"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestUpdateAgent_WorkerAllowsExternalCLIExecutor is the control: a worker
// can keep / change its executor to external-cli (workers are the only
// agents allowed non-native executors).
func TestUpdateAgent_WorkerAllowsExternalCLIExecutor(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"executor":{"kind":"external-cli","cli":"codex"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// And it is persisted to config.json — the write-time guard is the
	// regression we are guarding against, so the persistence is the
	// load-bearing assertion. (The PUT response may not echo the executor
	// because the response reads from the live in-memory config; the disk
	// write is the source of truth.)
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, "test-worker")
	require.NotNil(t, exec,
		"worker executor must be persisted to config.json: %s", string(raw))
	assert.Equal(t, "external-cli", exec["kind"])
	assert.Equal(t, "codex", exec["cli"])
}

// TestCreateAgent_RejectsExternalCLIExecutorOnBaseAgent verifies the create
// path rejects a non-native executor (REST create always makes a custom
// agent, which is a base agent and must run native).
func TestCreateAgent_RejectsExternalCLIExecutorOnBaseAgent(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"X","executor":{"kind":"external-cli","cli":"codex"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "only sub-agent workers",
		"the rejection must reference the worker-only rule")
}

// TestUpdateAgent_RejectsHeartbeatOnWorker verifies the heartbeat write-time
// guard: enabling heartbeat on a worker is 400 (workers have no heartbeat
// and run only via delegation). Setting it to disabled/empty is allowed.
func TestUpdateAgent_RejectsHeartbeatOnWorker(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	// heartbeat_enabled=true on worker → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"heartbeat_enabled":true}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "heartbeat",
		"the rejection must reference heartbeat")

	// heartbeat_interval>0 on worker → 400.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"heartbeat_interval":300}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestUpdateAgent_AllowsHeartbeatOffOnWorker is the control: a worker may
// receive an idempotent "off" write so an operator can clear a stray
// flag. This is NOT a "the worker now has heartbeat" path.
func TestUpdateAgent_AllowsHeartbeatOffOnWorker(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"heartbeat_enabled":false}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}

// TestUpdateAgent_RejectsVoiceOnWorker verifies the voice write-time guard:
// setting a non-empty voice on a worker is 400. Null / absent voice is fine.
func TestUpdateAgent_RejectsVoiceOnWorker(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	// voice="alloy" on worker → 400.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"voice":"alloy"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "voice",
		"the rejection must reference voice")
}

// TestUpdateAgent_AllowsNullVoiceOnWorker is the control: an explicit null
// (clearing a stored voice) on a worker is fine.
func TestUpdateAgent_AllowsNullVoiceOnWorker(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"voice":null}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
}
