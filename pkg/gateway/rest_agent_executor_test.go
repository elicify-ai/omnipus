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
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildExecutorTestAPI builds a minimal restAPI over a temp home with one mutable
// custom agent already present, so create/update/get all operate on a real
// config.json that safeUpdateConfigJSON can read-modify-write.
//
// ADR-054: agents are per-entity records under entities/agents/<id>.json, not
// config.json's agents.list — config.LoadConfig unconditionally STRIPS any
// agents.list content it finds on disk (pkg/config/legacy_agents_list.go),
// and createAgent/updateAgent persist exclusively via agentstore now. Every
// createAgent/updateAgent call also runs updateConfigJSONLocked, which
// UNCONDITIONALLY calls refreshConfigAndRewireServices → populates
// cfg.Agents.List by *replacing it wholesale* with agentstore.New(homePath).List()
// (see populateAgentsListFromEntityStore, pkg/gateway/gateway.go). So after the
// FIRST create/update in a test, any pre-seeded fixture agent that exists only
// in the in-memory cfg.Agents.List literal (never a real entity record) simply
// vanishes from the live config. "test-agent" is therefore seeded via BOTH the
// in-memory cfg.Agents.List literal (needed for mustAgentLoop's initial
// AgentRegistry construction / workspace-membership seeding, and for any
// pre-first-write read) AND a real agentstore entity record (needed for it to
// survive every subsequent write and for updateAgent's persist step, which
// resolves the target via the entity store, to find it at all).
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

	// "test-agent" needs a COMPLETE, explicit tools.builtin.policies map:
	// CLAUDE.md hard constraint 6 (config.ValidateToolPolicyCoverage) rejects
	// createAgent/updateAgent/updateAgentTools whenever ANY agent already in
	// the live config — not just the one being written — has an uncovered
	// static tool. A bare {"id":...,"type":"custom"} fixture (no tools field
	// at all) has zero policy entries, so every test in this file that goes
	// through one of those 3 endpoints would 400 on the coverage check
	// before ever reaching the behavior under test. A real installation
	// gets an equivalent completeness automatically at gateway boot via
	// config.ReconcileToolPolicyCeiling (ADR-076/ADR-077's two-layer model —
	// the reconciled global ceiling covers any tool this agent's own map
	// omits); this harness constructs the agent loop directly (mustAgentLoop)
	// and bypasses that boot sequence entirely, so the fixture must seed a
	// complete map itself — matching
	// what a real post-migration (or freshly-created) agent looks like via
	// coreagent.NewCustomAgentToolsCfg(), the same seed createAgent itself
	// uses for a caller that sends no tools_cfg.
	//
	// config.json on disk carries only agents.defaults now — agents.list is
	// never read from disk by any production code path (ADR-054), so seeding
	// it here would only assert a stale, misleading shape.
	cfgOnDisk := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"workspace": tmpDir, "model_name": "test-model", "max_tokens": 4096},
		},
	}
	cfgJSON, err := json.Marshal(cfgOnDisk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	testAgent := config.AgentConfig{
		ID:    "test-agent",
		Name:  "Test Agent",
		Type:  config.AgentTypeCustom,
		Tools: coreagent.NewCustomAgentToolsCfg(),
	}
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
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	// ADR-054: persist a matching entity record so "test-agent" survives the
	// first write-triggered refresh (see doc comment above) and so
	// updateAgent's agentstore-backed persist step can find it.
	testAgentForStore := testAgent
	require.NoError(t, agentstore.New(tmpDir).Create("test-agent", &testAgentForStore))

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

// TestGetEditPut_ExecutorRoundTripPreserved is the core regression: a worker
// updated to a non-native executor, then a PUT that does NOT touch the
// executor must preserve it (the GET→edit→PUT round-trip must not erase it).
// Uses a worker (the only agent kind that may declare an external executor
// under the native-only-for-non-workers rule). The worker is unlocked for
// this test so an unrelated PUT (e.g., description) is not blocked by the
// locked-agent identity check.
func TestGetEditPut_ExecutorRoundTripPreserved(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)
	// Unlock the worker so an unrelated-field PUT is allowed. ADR-054: the
	// locked-agent identity check in updateAgent reads the LIVE in-memory
	// cfg.Agents.List (mutating it in place here works exactly as before —
	// GetConfig returns the same *config.Config the AgentLoop holds), but the
	// worker's PERSISTED entity record must also be unlocked so a later
	// write-triggered refresh (populateAgentsListFromEntityStore, which
	// replaces cfg.Agents.List wholesale from the entity store) does not
	// silently re-lock it.
	cf := api.agentLoop.GetConfig()
	for i := range cf.Agents.List {
		if cf.Agents.List[i].ID == "test-worker" {
			cf.Agents.List[i].Locked = false
		}
	}
	_, err := agentstore.New(api.homePath).Update("test-worker", func(a *config.AgentConfig) error {
		a.Locked = false
		return nil
	})
	require.NoError(t, err)

	// 1. PUT an external-cli executor on the worker.
	put1 := `{"executor":{"kind":"external-cli","cli":"claude-code"}}`
	pw1 := httptest.NewRecorder()
	pr1 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker", strings.NewReader(put1))
	pr1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw1, pr1)
	require.Equal(t, http.StatusOK, pw1.Code, "put body: %s", pw1.Body.String())

	// 2. Persisted to the agent's entity record (ADR-054 — no longer
	// config.json's agents.list).
	exec := agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "executor not persisted")
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "claude-code", exec.CLI)

	// 3. PUT an UNRELATED field (description) — must NOT erase the executor.
	put2 := `{"description":"a helpful worker"}`
	pw2 := httptest.NewRecorder()
	pr2 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker", strings.NewReader(put2))
	pr2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw2, pr2)
	require.Equal(t, http.StatusOK, pw2.Code, "put body: %s", pw2.Body.String())

	// And it is still persisted.
	exec = agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "executor missing from entity record after unrelated PUT")
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "claude-code", exec.CLI)
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

// agentExecutorFromStore reads the persisted agent record via the agent store
// (ADR-054 — agents are per-entity records under entities/agents/<id>.json,
// not config.json's agents.list; createAgent/updateAgent persist exclusively
// there now) and returns its Subagents.Executor. Returns nil when the record
// is absent, has no Subagents block, or has no Executor — mirroring the old
// findExecutorInConfig's "absent means nil" contract so every call site's
// require.NotNil / assert.Nil assertions carry over unchanged.
func agentExecutorFromStore(t *testing.T, homePath, id string) *config.ExecutorConfig {
	t.Helper()
	rec, err := agentstore.New(homePath).Get(id)
	if err != nil {
		return nil
	}
	if rec.Subagents == nil {
		return nil
	}
	return rec.Subagents.Executor
}

// ---------------------------------------------------------------------------
// Worker write-time guards: external executor on a non-worker, heartbeat on a
// worker, and voice on a worker. All three must reject at the REST write gate.
// ---------------------------------------------------------------------------

// buildExecutorTestAPIWithWorker adds a worker agent to the test config so the
// worker guards have a real agent to test against. ADR-054: the worker is
// added to BOTH the live in-memory config (needed for the fast-path existence
// check the REST handlers run against a.agentLoop.GetConfig() before any
// write has happened) AND a real agentstore entity record (needed for it to
// survive the wholesale cfg.Agents.List replacement every subsequent
// create/update triggers — see buildExecutorTestAPI's doc comment — and for
// updateAgent's agentstore-backed persist step to find it at all).
func buildExecutorTestAPIWithWorker(t *testing.T) *restAPI {
	t.Helper()
	api := buildExecutorTestAPI(t)
	// "test-worker" needs the same complete tools.builtin.policies map as
	// "test-agent" above (see buildExecutorTestAPI's doc comment) — a bare
	// worker fixture would otherwise reintroduce the exact coverage gap
	// buildExecutorTestAPI was just fixed to avoid.
	testWorker := config.AgentConfig{
		ID:     "test-worker",
		Name:   "Worker",
		Type:   config.AgentTypeWorker,
		Locked: true,
		Tools:  coreagent.NewCustomAgentToolsCfg(),
	}
	// Live config: append the worker.
	cfg := api.agentLoop.GetConfig()
	cfg.Agents.List = append(cfg.Agents.List, testWorker)

	// Entity store: persist a matching record.
	testWorkerForStore := testWorker
	require.NoError(t, agentstore.New(api.homePath).Create("test-worker", &testWorkerForStore))
	return api
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

// The model-change-that-cannot-be-applied case (UAT E-7) lives in
// rest_agent_model_live_apply_test.go. The test that used to sit here pinned
// the defect itself (a 200 with a "saved but not applied" warning while the
// running agent kept its previous model).

// agentUpdatedAtFromConfig reads the persisted agent record via the agent
// store (ADR-054 — agents are per-entity records under
// entities/agents/<id>.json now, not config.json's agents.list) and returns
// the persisted updated_at as an RFC3339 string. This is the authoritative
// source the optimistic-concurrency check (updateAgent's agentstore.Update
// mutate closure) compares against, so tests capture it here rather than from
// the PUT response (which may lag the persisted value when no reload fires).
func agentUpdatedAtFromConfig(t *testing.T, api *restAPI, id string) string {
	t.Helper()
	rec, err := agentstore.New(api.homePath).Get(id)
	require.NoError(t, err, "agent entity record missing")
	if rec.UpdatedAt == nil {
		return ""
	}
	return rec.UpdatedAt.Format(time.RFC3339Nano)
}
