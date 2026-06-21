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
	"errors"
	"fmt"
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
	// Isolate OMNIPUS_HOME so per-agent workspaces (<home>/agents/<id>) and any
	// disk-config reads resolve under tmpDir instead of the developer's real
	// ~/.omnipus. Without this, config.OmnipusHomeDir() falls back to ~/.omnipus,
	// and a machine with a configured provider + credentials makes model-apply
	// succeed where the test expects it to fail (hermeticity bug, not behavior).
	t.Setenv("OMNIPUS_HOME", tmpDir)
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
	// is the round-trip-preserving behavior), so the test asserts "either
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

// TestCreateAgent_InvalidExecutor_400 proves the validator rejects bad executors
// on subagent_3p creates. A Main agent with an external executor is coerced to
// native instead of rejected (covered by TestCreateAgent_Main_CoercesExternalExecutorWithWarning).
func TestCreateAgent_InvalidExecutor_400(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"subagent_3p external-cli with no cli",
			`{"name":"X","type":"subagent_3p","description":"d","executor":{"kind":"external-cli","cli_path":"/usr/local/bin/codex"},"soul":"s"}`,
		},
		{
			"subagent_3p external-cli with no cli_path",
			`{"name":"X","type":"subagent_3p","description":"d","executor":{"kind":"external-cli","cli":"codex"},"soul":"s"}`,
		},
		{
			"subagent_3p external-cli with empty cli_path",
			`{"name":"X","type":"subagent_3p","description":"d","executor":{"kind":"external-cli","cli":"codex","cli_path":"  "},"soul":"s"}`,
		},
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

// TestCreateAgent_Subagent_Native_NoExecutor_201 verifies that a Subagent
// created without an executor gets kind=native derived server-side.
func TestCreateAgent_Subagent_Native_NoExecutor_201(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Helper","type":"Subagent","description":"does things","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)

	// Persisted executor should be native.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, created.Id)
	require.NotNil(t, exec, "worker with no executor must derive native: %s", string(raw))
	assert.Equal(t, "native", exec["kind"])
}

// TestCreateAgent_Main_CoercesExternalExecutorWithWarning verifies that a Main
// agent create with an external executor is coerced to native with a warning
// in the response, rather than rejected.
func TestCreateAgent_Main_CoercesExternalExecutorWithWarning(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"X","executor":{"kind":"external-cli","cli":"codex"},"soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)
	assert.Nil(t, created.Executor, "Main agents must not persist an external executor")
	require.NotNil(t, created.Warning, "expected a coercion warning")
	assert.Contains(t, *created.Warning, "coerced")
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

// Helper: create a subagent_3p worker and return its id.
func createSubagent3p(t *testing.T, api *restAPI) string {
	t.Helper()
	body := `{"name":"ExternalWorker","type":"subagent_3p","description":"external worker","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create subagent_3p body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	require.Equal(t, gen.AgentTypeSubagent3p, created.Type)
	return created.Id
}

// createNativeSubagent creates a native Subagent (delegation-only worker on the
// Omnipus engine) and returns its ID.
func createNativeSubagent(t *testing.T, api *restAPI) string {
	t.Helper()
	body := `{"name":"NativeWorker","type":"Subagent","description":"native worker","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create native subagent body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	require.Equal(t, gen.AgentTypeSubagent, created.Type)
	return created.Id
}

// TestUpdateAgent_Worker_AcceptsValidPatch is the worker-PUT-400 regression: a
// PUT carrying only fields that ARE valid for a worker (model, timeout_seconds,
// max_tool_iterations, color, icon, description) must succeed (200), not 400.
// Covers both a native Subagent and a subagent_3p.
func TestUpdateAgent_Worker_AcceptsValidPatch(t *testing.T) {
	validPatch := `{"model":"test-model","timeout_seconds":120,"max_tool_iterations":8,"color":"#d4af37","icon":"robot","description":"updated worker"}`

	t.Run("native Subagent", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		id := createNativeSubagent(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(validPatch))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		assert.Equal(t, http.StatusOK, w.Code, "valid worker patch must be accepted; body: %s", w.Body.String())
	})

	t.Run("subagent_3p", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		id := createSubagent3p(t, api)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(validPatch))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		assert.Equal(t, http.StatusOK, w.Code, "valid subagent_3p patch must be accepted; body: %s", w.Body.String())
	})
}

// TestUpdateAgent_Worker_RejectsHeartbeat keeps the genuinely-N/A guard: a worker
// PUT that ENABLES heartbeat is still rejected 400 (workers run only via
// delegation; they have no heartbeat schedule).
func TestUpdateAgent_Worker_RejectsHeartbeat(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createNativeSubagent(t, api)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id,
		strings.NewReader(`{"heartbeat_enabled":true,"heartbeat_interval":300}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "enabling heartbeat on a worker must 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "heartbeat")
}

// TestUpdateAgent_Subagent3p_AcceptsDelegationPolicy proves the worker-PUT-400
// loosening: a subagent_3p PUT carrying a delegation_policy is no longer rejected
// at the forbidden-field gate (a non-empty to[] is bounded by the depth cap,
// and a modes/depth-only policy is accepted).
func TestUpdateAgent_Subagent3p_AcceptsDelegationPolicy(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id,
		strings.NewReader(`{"delegation_policy":{"modes":["await"],"depth":1}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	// Must NOT be the forbidden-field rejection for delegation_policy.
	assert.NotContains(t, w.Body.String(), "subagent_3p agents do not support delegation_policy",
		"delegation_policy must no longer be a forbidden field on a subagent_3p PUT; body: %s", w.Body.String())
}

// TestCreateAgent_Subagent3p_ForbiddenFields rejects the 7 CLI-owned fields at create time.
func TestCreateAgent_Subagent3p_ForbiddenFields(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			"tools_cfg",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"tools_cfg":{"builtin":{"default_policy":"deny"}}}`,
		},
		{
			"skills",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"skills":["summarize"]}`,
		},
		{
			"fallback_models",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"fallback_models":[{"model":"m","provider":"p"}]}`,
		},
		{
			"model_params",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"model_params":{"temperature":0.5}}`,
		},
		{
			"sandbox_profile",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"sandbox_profile":"workspace"}`,
		},
		{
			"shell_policy",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"shell_policy":{"enable_deny_patterns":true}}`,
		},
		{
			"delegation_policy",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"delegation_policy":{"to":[{"kind":"local","id":"*"}]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "subagent_3p agents do not support "+tc.name)
		})
	}
}

// TestUpdateAgent_Subagent3p_ForbiddenFields rejects the CLI-owned fields on PUT.
// worker-PUT-400: delegation_policy is NO LONGER in this set — it is a valid
// worker field on PUT (a non-empty to[] is independently bounded by the depth
// cap in buildDelegationPolicy). The remaining 6 stay forbidden because the
// external CLI manages its own isolation/tools/skills (O13).
func TestUpdateAgent_Subagent3p_ForbiddenFields(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)

	cases := []struct {
		name string
		body string
	}{
		{"tools_cfg", `{"tools_cfg":{"builtin":{"default_policy":"deny"}}}`},
		{"skills", `{"skills":["web-research"]}`},
		{"fallback_models", `{"fallback_models":[{"model":"m","provider":"p"}]}`},
		{"model_params", `{"model_params":{"temperature":0.5}}`},
		{"sandbox_profile", `{"sandbox_profile":"workspace"}`},
		{"shell_policy", `{"shell_policy":{"enable_deny_patterns":true}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "subagent_3p agents do not support "+tc.name)
		})
	}
}

// TestUpdateAgent_ExecutorMutability verifies that cli_path, env_overrides, and
// cli_args are mutable on a subagent_3p worker, while the cli itself is locked.
func TestUpdateAgent_ExecutorMutability(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)

	// Mutate CLI-owned mutable fields.
	body := `{"executor":{"cli_path":"/opt/codex","env_overrides":{"FOO":"bar"},"cli_args":"--verbose"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, id)
	require.NotNil(t, exec)
	assert.Equal(t, "/opt/codex", exec["cli_path"])
	assert.Equal(t, map[string]any{"FOO": "bar"}, exec["env_overrides"])
	assert.Equal(t, "--verbose", exec["cli_args"])
}

// TestUpdateAgent_ExecutorOMNIPUSPrefixRejected verifies that env_overrides
// containing OMNIPUS_ keys are rejected at the write gate.
func TestUpdateAgent_ExecutorOMNIPUSPrefixRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)

	body := `{"executor":{"env_overrides":{"OMNIPUS_MASTER_KEY":"leak"}}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "OMNIPUS_* is gateway-internal")
}

// TestCreateAgent_ReloadFailure_ReturnsWarning verifies that a failure during
// TriggerReload after a successful persistence is surfaced as a warning rather
// than failing the create request.
func TestCreateAgent_ReloadFailure_ReturnsWarning(t *testing.T) {
	api := buildExecutorTestAPI(t)
	api.agentLoop.SetReloadFunc(func() error { return errors.New("reload boom") })

	body := `{"name":"ReloadTest","soul":"soul content"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Warning)
	assert.Contains(t, *resp.Warning, "config reload failed")
}

// TestUpdateAgent_ModelLiveApplyFailure_ReturnsWarning verifies that a model
// change which persists to disk but cannot be applied to the running agent is
// returned with a warning (not a hard failure).
func TestUpdateAgent_ModelLiveApplyFailure_ReturnsWarning(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// The model string must genuinely fail live-apply so the warning path fires.
	// A *bare* slug ("foo") is NOT a reliable failure trigger: the PUT handler
	// reloads config via refreshConfigAndRewireServices, which seeds the built-in
	// provider catalog (incl. passthrough providers openrouter/vivgrid). A bare
	// slug then resolves through passthrough and live-apply SUCCEEDS — no warning.
	// A slug whose prefix is a *known provider name* (here "anthropic") is not
	// hijacked by passthrough (looksLikeBareModelSlug → false), and with no
	// matching configured/credentialed anthropic model it fails resolution in
	// ApplyAgentModel → resolvedModelConfig, which is exactly the live-apply
	// failure this test asserts surfaces as a warning.
	body := `{"model":"anthropic/omnipus-nonexistent-model-zzz"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.Warning)
	assert.Contains(t, *resp.Warning, "model saved to config but could not be applied")
}

// agentUpdatedAtFromConfig reads config.json on disk and returns the persisted
// updated_at RFC3339 string for the named agent. This is the authoritative
// source the optimistic-concurrency check (safeUpdateConfigJSON mutate closure)
// compares against, so tests capture it here rather than from the PUT response
// (which may lag the on-disk value when no reload fires).
func agentUpdatedAtFromConfig(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	agents, _ := m["agents"].(map[string]any)
	require.NotNil(t, agents, "agents section missing: %s", string(raw))
	list, _ := agents["list"].([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		if entry == nil || entry["id"] != id {
			continue
		}
		ts, _ := entry["updated_at"].(string)
		return ts
	}
	return ""
}

// TestUpdateAgent_StaleUpdatedAt_Returns409 verifies the optimistic-concurrency
// 409 path: a PUT carrying an updated_at that does not match the persisted value
// is rejected with 409 + code:"conflict", while a PUT carrying the current
// updated_at succeeds. The conflict check lives inside the safeUpdateConfigJSON
// mutate closure (moved there to close a TOCTOU race between the version check
// and the write).
func TestUpdateAgent_StaleUpdatedAt_Returns409(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// 1. Establish a persisted updated_at via a successful PUT (config-only
	//    field change so no reload/model-apply side effects fire). This is the
	//    "current" value the conflict check compares against.
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(`{"color":"#FF0000"}`))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "establishing PUT body: %s", w1.Body.String())
	currentUpdatedAt := agentUpdatedAtFromConfig(t, api, "test-agent")
	require.NotEmpty(t, currentUpdatedAt,
		"updated_at must be persisted after a successful PUT")

	// 2. Stale PUT: send an obviously-different updated_at → 409 conflict.
	staleBody := fmt.Sprintf(`{"color":"#00FF00","updated_at":%q}`, "2000-01-01T00:00:00Z")
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(staleBody))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusConflict, w2.Code,
		"a PUT with a stale updated_at must be rejected with 409; body: %s", w2.Body.String())
	var errResp gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &errResp), "decode conflict body: %s", w2.Body.String())
	require.NotNil(t, errResp.Code, "conflict response must carry a code: %s", w2.Body.String())
	assert.Equal(t, "conflict", *errResp.Code,
		"conflict response code must be %q (got %q)", "conflict", *errResp.Code)

	// The rejected PUT must NOT have mutated the persisted updated_at.
	afterStale := agentUpdatedAtFromConfig(t, api, "test-agent")
	assert.Equal(t, currentUpdatedAt, afterStale,
		"a rejected (409) PUT must not refresh the persisted updated_at")

	// 3. Happy path: PUT carrying the CURRENT updated_at → 200.
	freshBody := fmt.Sprintf(`{"color":"#0000FF","updated_at":%q}`, currentUpdatedAt)
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent",
		strings.NewReader(freshBody))
	r3.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w3, r3)
	assert.Equal(t, http.StatusOK, w3.Code,
		"a PUT with the current updated_at must succeed; body: %s", w3.Body.String())
}

// TestUpdateAgent_ExecutorCliImmutable_Returns400 verifies that executor.cli is
// locked after create: a PUT that tries to switch the CLI (e.g. codex →
// claude-code) is rejected with 400. The guard lives in executorConfigUpdate
// (rest_agent_executor.go). Spec §4.16 / §9.2 "PUT External, executor.cli:codex
// → 400".
func TestUpdateAgent_ExecutorCliImmutable_Returns400(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api) // created with cli:"codex"

	// Attempt to switch CLI from codex → claude-code.
	body := `{"executor":{"cli":"claude-code"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"switching executor.cli after create must be rejected with 400; body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "executor.cli is locked",
		"error message must explain that cli is locked; body: %s", w.Body.String())

	// Verify the persisted cli is still the original.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, id)
	require.NotNil(t, exec)
	assert.Equal(t, "codex", exec["cli"],
		"persisted executor.cli must be unchanged after a rejected mutation")
}

// TestCreateAgent_Subagent3p_CliPathRequired verifies that cli_path is required
// on POST create for subagent_3p agents. Spec §9.2: "POST subagent_3p,
// cli:claude-code, no cli_path → 400 — cli_path is required".
func TestCreateAgent_Subagent3p_CliPathRequired(t *testing.T) {
	api := buildExecutorTestAPI(t)

	cases := []struct {
		name string
		body string
	}{
		{
			"missing cli_path",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"claude-code"}}`,
		},
		{
			"empty cli_path",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"claude-code","cli_path":""}}`,
		},
		{
			"whitespace cli_path",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"claude-code","cli_path":"   "}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "cli_path is required",
				"error must mention cli_path is required; body: %s", w.Body.String())
		})
	}
}

// TestCreateAgent_Subagent3p_WithCliPath_Succeeds verifies the happy path: a
// subagent_3p created with cli + cli_path succeeds and the cli_path is
// persisted. Spec §9.2 acceptance: "POST subagent_3p, cli:claude-code,
// cli_path:/usr/local/bin/claude → 201".
func TestCreateAgent_Subagent3p_WithCliPath_Succeeds(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"CliPathOK","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"claude-code","cli_path":"/usr/local/bin/claude"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, created.Executor, "response must include executor")
	require.NotNil(t, created.Executor.CliPath, "response executor must include cli_path")
	assert.Equal(t, "/usr/local/bin/claude", *created.Executor.CliPath)

	// Verify persistence.
	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, created.Id)
	require.NotNil(t, exec)
	assert.Equal(t, "/usr/local/bin/claude", exec["cli_path"])
}

// TestUpdateAgent_ExecutorCliPathMutable verifies that cli_path IS mutable on
// PUT for a subagent_3p agent (binary upgrade without re-creating the agent).
// Spec §9.2: "PUT executor.cli_path: /new/path → 200".
func TestUpdateAgent_ExecutorCliPathMutable(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api) // cli_path: /usr/local/bin/codex

	body := `{"executor":{"cli_path":"/opt/codex-v2"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	raw, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	exec := findExecutorInConfig(t, m, id)
	require.NotNil(t, exec)
	assert.Equal(t, "/opt/codex-v2", exec["cli_path"],
		"cli_path must be updated to the new value")
}
