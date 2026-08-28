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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
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
	// gets an equivalent backfill automatically at gateway boot via
	// config.RepairIncompleteToolPolicyCoverage; this harness constructs the
	// agent loop directly (mustAgentLoop) and bypasses that boot sequence
	// entirely, so the fixture must seed a complete map itself — matching
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

// TestCreateAgent_ExecutorPersistsAndEchoes proves POST for a Main agent
// never carries an executor. W1 (discriminated union) removed the Executor
// property from AgentCreateRequestMain entirely — Main agents always run
// native and a client cannot even express an executor on create any more
// (the field matrix marks Main's executor row "—"). This supersedes the
// pre-W1 "kind=native collapses to nil" behavior: there is no executor input
// to collapse — it is structurally absent.
func TestCreateAgent_ExecutorPersistsAndEchoes(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"NativeAgent","type":"Main","soul":"native-soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, created.Executor, "Main agents never carry an executor")

	// Not persisted — Main never gets a subagents.executor block (ADR-054:
	// agents are per-entity records under entities/agents/<id>.json now, not
	// config.json's agents.list).
	exec := agentExecutorFromStore(t, api.homePath, created.Id)
	assert.Nil(t, exec, "Main agents must not persist an executor block")
}

// TestCreateAgent_ShellPolicy_PersistAndEcho is the Fix-3 regression test:
// createAgent previously never even read shell_policy from the create
// request at all (unlike updateAgent, which persists it). This proves the
// full round trip: create a Main agent with a shell_policy, then GET it back
// and confirm it is echoed AND persisted to config.json.
func TestCreateAgent_ShellPolicy_PersistAndEcho(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Sandboxed","type":"Main","soul":"s",` +
		`"shell_policy":{"enable_deny_patterns":true,"custom_deny_patterns":["rm\\s+-rf"]}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, created.ShellPolicy, "create response must echo shell_policy")
	require.NotNil(t, created.ShellPolicy.EnableDenyPatterns)
	assert.True(t, *created.ShellPolicy.EnableDenyPatterns)
	require.NotNil(t, created.ShellPolicy.CustomDenyPatterns)
	assert.Equal(t, []string{"rm\\s+-rf"}, *created.ShellPolicy.CustomDenyPatterns)

	// Persisted to the agent's entity record (ADR-054: agents are per-entity
	// records under entities/agents/<id>.json now, not config.json's
	// agents.list).
	rec, err := agentstore.New(api.homePath).Get(created.Id)
	require.NoError(t, err, "created agent must be persisted")
	require.NotNil(t, rec.ShellPolicy, "shell_policy must be persisted to the entity record")
	assert.True(t, rec.ShellPolicy.EnableDenyPatterns)
	assert.Equal(t, []string{"rm\\s+-rf"}, rec.ShellPolicy.CustomDenyPatterns)

	// GET /agents/{id} must independently reflect the persisted values (not
	// just the create response, which could echo a value that never landed).
	getW := httptest.NewRecorder()
	getR := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	api.HandleAgents(getW, getR)
	require.Equal(t, http.StatusOK, getW.Code, "get body: %s", getW.Body.String())
	got := decodeAgentResp(t, getW.Body.Bytes())
	require.NotNil(t, got.ShellPolicy, "GET must echo shell_policy")
	require.NotNil(t, got.ShellPolicy.EnableDenyPatterns)
	assert.True(t, *got.ShellPolicy.EnableDenyPatterns)
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
	pr1 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker", strings.NewReader(put1))
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
	pr2 := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker", strings.NewReader(put2))
	pr2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw2, pr2)
	require.Equal(t, http.StatusOK, pw2.Code, "put body: %s", pw2.Body.String())

	// And it is still persisted.
	exec = agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "executor missing from entity record after unrelated PUT")
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "claude-code", exec.CLI)
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

	// Persisted to the agent's entity record — original cli is preserved, not
	// overwritten (ADR-054: no longer config.json's agents.list).
	exec := agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "executor not persisted")
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "codex", exec.CLI,
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

	// And it is persisted to the agent's entity record (ADR-054) — the
	// write-time guard is the regression we are guarding against, so the
	// persistence is the load-bearing assertion. (The PUT response may not
	// echo the executor because the response reads from the live in-memory
	// config; the entity record is the source of truth.)
	exec := agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "worker executor must be persisted to the entity record")
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "codex", exec.CLI)
}

// TestUpdateAgent_WorkerAllowsRemoteA2AExecutor is the positive control for
// remote-a2a: a worker's executor.kind may be changed to remote-a2a
// (reserved/future — accepted and persisted without roster resolution or
// dispatch, per executorConfigFromRequest's "accepted but reserved" comment).
// TestUpdateAgent_NonWorkerRejectsExternalCLIExecutor only covers the
// negative (non-worker rejected with remote-a2a too); this is the missing
// worker-allowed positive case.
func TestUpdateAgent_WorkerAllowsRemoteA2AExecutor(t *testing.T) {
	api := buildExecutorTestAPIWithWorker(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-worker",
		strings.NewReader(`{"executor":{"kind":"remote-a2a"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	exec := agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "worker remote-a2a executor must be persisted")
	assert.Equal(t, config.ExecutorKindRemoteA2A, exec.Kind)
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
	exec := agentExecutorFromStore(t, api.homePath, created.Id)
	require.NotNil(t, exec, "worker with no executor must derive native")
	assert.Equal(t, config.ExecutorKindNative, exec.Kind)
}

// TestCreateAgent_Main_ExecutorFieldRejected proves the unconditional
// strict-decode rejection: AgentCreateRequestMain has no `executor` property
// at all (W1 discriminated union) — an `executor` key in the JSON body is now
// rejected 400 by createAgent's strict decode (json.Decoder with
// DisallowUnknownFields), even with ValidateInbound OFF (the default in this
// test harness). This supersedes the earlier "coerced to native with a
// warning" and, later, "silently unknown-field-ignored" behaviors — a caller
// can no longer smuggle the field past the create gate at all. (With
// ValidateInbound enabled, the same body 400s at the schema gate instead —
// see TestContract_AgentCreateRequestMain_ExecutorRejected in
// pkg/api/generated/contract_test.go.)
func TestCreateAgent_Main_ExecutorFieldRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"X","type":"Main","executor":{"kind":"external-cli","cli":"codex"},"soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "executor")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestMain")

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], `agent type "Main"`, "error must name the offending variant's wire type")
}

// Note: agent-level heartbeat write-time guards (RejectsHeartbeatOnWorker /
// AllowsHeartbeatOffOnWorker) were removed in the workspace-heartbeat
// decommission (US-4 / FR-027): heartbeat is no longer an agent field. The
// "workers have no heartbeat" invariant is now enforced in
// workspace.ValidateMemberConfigs (see pkg/workspace/member_config_test.go).

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
// color, icon, description — plus max_tool_iterations for a NATIVE Subagent
// only) must succeed (200), not 400. Covers both a native Subagent and a
// subagent_3p.
func TestUpdateAgent_Worker_AcceptsValidPatch(t *testing.T) {
	t.Run("native Subagent", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		id := createNativeSubagent(t, api)
		validPatch := `{"model":"test-model","timeout_seconds":120,"max_tool_iterations":8,"color":"#d4af37","icon":"robot","description":"updated worker"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(validPatch))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		assert.Equal(t, http.StatusOK, w.Code, "valid worker patch must be accepted; body: %s", w.Body.String())
	})

	t.Run("subagent_3p", func(t *testing.T) {
		api := buildExecutorTestAPI(t)
		id := createSubagent3p(t, api)
		// max_tool_iterations is deliberately absent here — it is CLI-owned and
		// forbidden on a subagent_3p PUT (agent_field_rules.go
		// subagent3pForbiddenUpdateFields, extended in W2a). See
		// TestUpdateAgent_Subagent3p_ForbiddenFields for the 400 case.
		validPatch := `{"model":"test-model","timeout_seconds":120,"color":"#d4af37","icon":"robot","description":"updated worker"}`
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id, strings.NewReader(validPatch))
		r.Header.Set("Content-Type", "application/json")
		api.HandleAgents(w, r)
		assert.Equal(t, http.StatusOK, w.Code, "valid subagent_3p patch must be accepted; body: %s", w.Body.String())
	})
}

// Note: TestUpdateAgent_Worker_RejectsHeartbeat was removed in the
// workspace-heartbeat decommission (US-4 / FR-027). heartbeat_enabled,
// heartbeat_interval, and heartbeat (HEARTBEAT.md) are still accepted on the
// wire (AgentUpdateRequest retains them for backward compatibility) but are
// silently ignored — heartbeat is workspace-scoped (ADR-027). Worker-heartbeat
// rejection now lives in workspace.ValidateMemberConfigs.

// TestUpdateAgent_Subagent3p_RejectsDelegationPolicy proves ADR-037's wire
// retirement of delegation_policy: a subagent_3p PUT carrying it is now
// rejected 400 by updateAgent's raw-body sniff (rest.go) — the loud-400
// mirror of ADR-035's precedent for a retired field, rather than the old
// worker-PUT-400 loosening this test used to prove (which accepted and
// echoed the policy). Also proves no side effect: the persisted config is
// genuinely unchanged by the rejected PUT.
func TestUpdateAgent_Subagent3p_RejectsDelegationPolicy(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api)

	before, err := os.ReadFile(api.configPath())
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/"+id,
		strings.NewReader(`{"delegation_policy":{"modes":["await"],"depth":1}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"delegation_policy must be rejected on a subagent_3p PUT (ADR-037); body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "delegation_policy is retired")

	after, err := os.ReadFile(api.configPath())
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after),
		"config.json must be unchanged after a rejected delegation_policy PUT")
}

// TestCreateAgent_Subagent3p_ForbiddenFields_ValidationEnabled proves the six
// CLI-owned fields are rejected 400 at create time when ValidateInbound is
// enabled: the discriminated-union AgentCreateRequestSubagent3p schema has no
// matching property for any of them (additionalProperties: false). This
// supersedes the old runtime firstForbiddenSubagent3pField check on create,
// which is now enforced structurally by the schema instead (W2a).
//
// delegation_policy is not in this table either — it is retired from the
// wire entirely (ADR-037) rather than being a subagent_3p-specific forbidden
// field; see TestCreateAgent_Subagent3p_DelegationPolicyRejected below, which
// proves the same additionalProperties:false rejection applies to it too.
func TestCreateAgent_Subagent3p_ForbiddenFields_ValidationEnabled(t *testing.T) {
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
			"shell_policy",
			`{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"shell_policy":{"enable_deny_patterns":true}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newTestRestAPIWithValidation(t)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r = withAdminRole(r)
			api.createAgent(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent3p")
		})
	}
}

// TestCreateAgent_Subagent3p_ForbiddenFields_RejectedUnconditionally proves
// that with ValidateInbound OFF (the default), a caller-supplied field that
// AgentCreateRequestSubagent3p has no matching Go struct field for is now
// REJECTED 400 by createAgent's strict decode (json.Decoder with
// DisallowUnknownFields) — not silently unknown-field-ignored by a plain
// json.Unmarshal. This closes the defense-in-depth gap the earlier version of
// this test (ValidationDisabled) documented: enforcement no longer depends on
// cfg.Gateway.ValidateInbound being turned on.
func TestCreateAgent_Subagent3p_ForbiddenFields_RejectedUnconditionally(t *testing.T) {
	api := buildExecutorTestAPI(t)
	body := `{"name":"DroppedFields","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"tools_cfg":{"builtin":{"default_policy":"deny"}},"skills":["summarize"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent3p")
}

// TestCreateAgent_Subagent3p_ForbiddenFieldFixture_RejectedByDefault drives
// the shared cross-package fixture
// (gen.FixtureAgentCreateRequestSubagent3p_ForbiddenFieldJSON,
// pkg/api/generated/fixtures.go — the same payload
// TestContract_AgentCreateRequestSubagent3p_ForbiddenFieldRejected in
// pkg/api/generated/contract_test.go validates at the schema level) through
// the real HTTP handler with a DEFAULT config (ValidateInbound off, the
// gateway's real-world default). Proves the two enforcement layers agree:
// the schema rejects the payload when ValidateInbound is on, and
// createAgent's unconditional strict decode rejects the same payload even
// when ValidateInbound is off.
func TestCreateAgent_Subagent3p_ForbiddenFieldFixture_RejectedByDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		bytes.NewReader(gen.FixtureAgentCreateRequestSubagent3p_ForbiddenFieldJSON()))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent3p")
}

// TestCreateAgent_SteeringModeFieldRetiredFromWire proves steering_mode is
// retired from the wire entirely (2026-07-17, dead config removal): it is no
// longer a property on ANY create variant — not even Main, which used to be
// the one variant that carried it. Sending it on a Main create is now
// rejected 400 (strict decode, additionalProperties: false) rather than
// silently accepted-and-dropped. Mirrors
// TestCreateAgent_Subagent3p_DelegationPolicyRejected's "retired from the
// wire entirely" pattern (ADR-037), except steering_mode's runtime steering
// engine (pkg/agent/steering.go) stays global-only, driven exclusively by
// agents.defaults.steering_mode — there was never a per-agent override to
// preserve.
func TestCreateAgent_SteeringModeFieldRetiredFromWire(t *testing.T) {
	api := buildExecutorTestAPI(t)
	body := `{"name":"W","type":"Main","soul":"s","steering_mode":"one-at-a-time"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "steering_mode")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestMain")
}

// TestCreateAgent_Subagent3p_DelegationPolicyRejected proves the ADR-037
// behavior: delegation_policy is retired from the wire entirely, so a
// subagent_3p create sending it is now rejected (400, additionalProperties:
// false) rather than accepted (this test used to prove the opposite — the W2a
// field-matrix unlock that made delegation_policy valid on ALL variants
// including subagent_3p — before the field itself was retired).
func TestCreateAgent_Subagent3p_DelegationPolicyRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)
	body := `{"name":"X","type":"subagent_3p","description":"d","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"delegation_policy":{"to":[{"kind":"local","id":"*"}],"modes":["task"],"depth":1}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "delegation_policy")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent3p")
}

// TestUpdateAgent_Subagent3p_ForbiddenFields rejects the CLI-owned fields on PUT.
// delegation_policy is covered separately by
// TestUpdateAgent_Subagent3p_RejectsDelegationPolicy above (rest.go's
// raw-body sniff) — ADR-037 retired it from the wire entirely, so it is no
// longer part of this per-variant forbidden-field matrix at all; it 400s
// unconditionally for every agent type via the sniff, not via this
// executor-specific gate. The remaining 5 stay forbidden here because the
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
		{"shell_policy", `{"shell_policy":{"enable_deny_patterns":true}}`},
		// W2a: max_tool_iterations joins the forbidden set on subagent_3p PUT
		// (the external CLI runs its own turn loop — Omnipus cannot cap its
		// per-turn tool-call budget). subagent3pForbiddenUpdateFields in
		// agent_field_rules.go is the single source of truth for this list.
		{"max_tool_iterations", `{"max_tool_iterations":8}`},
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

	exec := agentExecutorFromStore(t, api.homePath, id)
	require.NotNil(t, exec)
	assert.Equal(t, "/opt/codex", exec.CLIPath)
	assert.Equal(t, map[string]string{"FOO": "bar"}, exec.EnvOverrides)
	assert.Equal(t, "--verbose", exec.CLIArgs)
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

// TestCreateAgent_ReloadFailure_ReturnsWarning verifies that a failure is
// still surfaced as a warning (not a hard failure) after a successful
// persistence, now that createAgent's primary publish mechanism is the
// ADR-054 fast path (issue #571, fastAgentUpsert/AgentLoop.UpsertAgentFast)
// rather than a full TriggerReload on every create.
//
// The fast path itself is the common case and does not fail here (nothing
// about this harness's registry/provider is broken), so SetReloadFunc alone
// — mocking the OLD primary mechanism — no longer reaches the warning path:
// fastAgentUpsert only falls back to the full reload (and thus to the mocked
// reload func) when the fast path itself reports an error first.
// testForceFastUpsertErr (rest.go) is the deterministic seam for that: it
// simulates "the fast path failed for some internal reason" without needing
// to reverse-engineer a real provider/registry/CAS failure from a black-box
// REST test. With BOTH the fast path and the reload fallback failing, the
// warning must still surface — proving createAgent never silently drops a
// failure that leaves the registry not-yet-updated (the same "reports
// success it never achieved" defect class ADR-037 already bans elsewhere).
func TestCreateAgent_ReloadFailure_ReturnsWarning(t *testing.T) {
	api := buildExecutorTestAPI(t)
	api.testForceFastUpsertErr = errors.New("fast upsert boom")
	api.agentLoop.SetReloadFunc(func() error { return errors.New("reload boom") })

	body := `{"name":"ReloadTest","type":"Main","soul":"soul content"}`
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

// TestCreateAgent_FastUpsertFailure_FallsBackAndSucceedsSilently is the
// companion positive case: when the ADR-054 fast path fails but the full
// reload fallback SUCCEEDS, the create must still report 201 with NO
// warning — the operation genuinely completed correctly (registry and
// config both reflect the new agent by the time the handler returns), so
// surfacing an internal "the fast path had to fall back" detail as a
// user-facing warning would be noise, not signal. This is the counterpart to
// TestCreateAgent_ReloadFailure_ReturnsWarning (which proves the OTHER half:
// a warning WHEN something is genuinely still wrong).
func TestCreateAgent_FastUpsertFailure_FallsBackAndSucceedsSilently(t *testing.T) {
	api := buildExecutorTestAPI(t)
	api.testForceFastUpsertErr = errors.New("fast upsert boom")
	// buildExecutorTestAPI's bare AgentLoop never wires SetReloadFunc on its
	// own (AgentLoop.Run() — which does that in production — is never
	// started in these unit tests): without wiring a real one here,
	// triggerReloadAndWait's "reload not configured" branch would treat the
	// fallback as a no-op SUCCESS that never actually rebuilds the registry,
	// which would make this test pass for the wrong reason. Wire the actual
	// registry-rebuilding call (mirrors gateway.go's production reload path
	// closely enough to prove the fallback genuinely succeeds). By the time
	// this closure runs, api.agentLoop.GetConfig() already reflects the new
	// agent — createAgent's entity-store persist (via
	// refreshConfigAndRewireServices/SwapConfig) runs BEFORE fastAgentUpsert.
	api.agentLoop.SetReloadFunc(func() error {
		defer api.agentLoop.ClearReloadPending()
		return api.agentLoop.ReloadProviderAndConfig(
			context.Background(), &restMockProvider{}, api.agentLoop.GetConfig())
	})

	body := `{"name":"FallbackTest","type":"Main","soul":"soul content"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a fast-path failure that the full-reload fallback recovers from must not surface a warning")

	// The registry must genuinely reflect the new agent — the fallback must
	// have actually run and succeeded, not merely been skipped.
	_, ok := api.agentLoop.GetRegistry().GetAgent(resp.Id)
	require.True(t, ok, "the new agent must be resolvable in the registry after the full-reload fallback succeeds")
}

// TestUpdateAgent_ModelLiveApplyFailure_ReturnsWarning verifies that a model
// change which persists to disk but cannot be applied to the running agent is
// returned with a warning (not a hard failure).
func TestUpdateAgent_ModelLiveApplyFailure_ReturnsWarning(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// The model string must genuinely fail live-apply so the warning path fires.
	// After the aggregator-resolver fix, a passthrough provider (openrouter /
	// vivgrid) resolves ANY vendor-prefixed slug (that's the correct behavior —
	// OpenRouter serves "anthropic/…"/"openai/…" from its own catalog), so no
	// model NAME triggers a resolution failure while a passthrough is present.
	// To reach the persist-but-cannot-apply path we pin the on-disk config to a
	// single DEDICATED (non-passthrough) provider: an explicit providers list
	// suppresses the default passthrough-catalog seeding, so an unmatched slug
	// like "anthropic/…" has no route and fails in ApplyAgentModel →
	// resolvedModelConfig — exactly the live-apply failure this test asserts
	// surfaces as a (soft) warning rather than a hard error.
	{
		raw, err := os.ReadFile(api.configPath())
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		m["providers"] = []map[string]any{
			{
				"provider":   "openai",
				"model_name": "gpt-4o",
				"model":      "openai/gpt-4o",
				"api_base":   "https://api.openai.com/v1",
				"api_key":    "sk-test-dummy",
			},
		}
		out, err := json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(api.configPath(), out, 0o600))
	}

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

// TestUpdateAgent_ToolsCfg_PersistsUnderToolsKey proves the general PUT
// /api/v1/agents/{id} endpoint persists a tools_cfg body under the on-disk
// "tools" key — config.AgentConfig.Tools' real `json:"tools"` tag — and NOT
// the dead "tools_cfg" key (the wire request field name) this endpoint used
// to write before the fix (see the "NOTE ON THE KEY NAME" comment in
// updateAgent). Before the fix, every tools_cfg update sent through the
// general agent PUT was silently persisted to an orphaned key that
// config.LoadConfig's json.Unmarshal ignores on the next load/reload — the
// write appeared to succeed (200 OK) but never actually took effect.
func TestUpdateAgent_ToolsCfg_PersistsUnderToolsKey(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// updateAgent's tools_cfg branch is a full replacement, not a merge (see
	// the "whole per-tool policies map is a full replacement" doc comment on
	// the coverage-validation block), so the body must carry a COMPLETE
	// policy map — build it by copying the seed and overriding one entry.
	seedPolicies := coreagent.NewCustomAgentToolsCfg().Builtin.Policies
	policies := make(map[string]string, len(seedPolicies))
	for k, v := range seedPolicies {
		policies[k] = string(v)
	}
	policies["bash"] = "allow"
	policiesJSON, err := json.Marshal(policies)
	require.NoError(t, err)
	body := `{"tools_cfg":{"builtin":{"policies":` + string(policiesJSON) + `}}}`

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	// Read the actual persisted agent entity record (ADR-054:
	// entities/agents/<id>.json, not config.json's agents.list) and assert
	// the policies landed under the real "tools" key, never the dead
	// "tools_cfg" key. Read the raw JSON bytes directly (not via
	// agentstore.Get, which would only ever populate the real Tools field) so
	// a future regression that writes a stray "tools_cfg" key alongside it
	// would still be caught.
	entityPath := filepath.Join(api.homePath, "entities", "agents", "test-agent.json")
	raw, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	var agentMap map[string]any
	require.NoError(t, json.Unmarshal(raw, &agentMap))

	_, hasDeadKey := agentMap["tools_cfg"]
	assert.False(t, hasDeadKey, "must never persist under the dead 'tools_cfg' key")

	toolsMap, ok := agentMap["tools"].(map[string]any)
	require.True(t, ok, "tools_cfg must persist under the real 'tools' key")
	builtin, ok := toolsMap["builtin"].(map[string]any)
	require.True(t, ok, "tools.builtin must be present")
	persistedPolicies, ok := builtin["policies"].(map[string]any)
	require.True(t, ok, "tools.builtin.policies must be present")
	assert.Equal(t, "allow", persistedPolicies["bash"])
	assert.Equal(t, "deny", persistedPolicies["delete_agent"], "unrelated seed entries must survive")
}

// TestUpdateAgent_ConcurrentDeleteRace_Returns404NotPhantom200 proves the fix
// to updateAgent's phantom-success bug. The fast-path existence check at the
// top of updateAgent runs against the config snapshot fetched at the start
// of the handler (a real, live gateway can have this go stale between fetch
// and the locked critical section running — e.g. a concurrent
// DELETE /agents/{id} that has already durably removed the agent's entity
// record (ADR-054: entities/agents/<id>.json) but whose TriggerReload hasn't
// yet swapped the in-memory registry this test's fixture stands in for). The
// persist closure's fresh, ID-based lookup against the ACTUAL agent store —
// not the stale pre-lock cfg.Agents.List snapshot — must catch this and
// return errAgentVanishedDuringUpdate (mapped to 404), never silently return
// nil and report 200 for an update that touched nothing.
func TestUpdateAgent_ConcurrentDeleteRace_Returns404NotPhantom200(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfgPath := filepath.Join(tmpDir, "config.json")

	// config.json carries no agent data at all (ADR-054) — deliberately NOT
	// seeding an agentstore entity record for "test-agent" below is what
	// simulates "a concurrent DELETE already removed the durable record".
	cfgOnDisk := map[string]any{
		"version": config.CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{"workspace": tmpDir, "model_name": "test-model", "max_tokens": 4096},
		},
	}
	cfgJSON, err := json.Marshal(cfgOnDisk)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	// In-memory live config: "test-agent" STILL present — the fast-path
	// existence check reads this stale snapshot, not the agent store. No
	// matching agentstore.Create call follows, so the durable record is
	// genuinely absent.
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{
				{
					ID:    "test-agent",
					Name:  "Test Agent",
					Type:  config.AgentTypeCustom,
					Tools: coreagent.NewCustomAgentToolsCfg(),
				},
			},
		},
	}
	al := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(`{"color":"#123456"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusNotFound, w.Code,
		"a concurrent-delete race must 404, not phantom-succeed with 200: body=%s", w.Body.String())

	// Nothing must have been persisted for the vanished agent: no phantom
	// entity record was created by the 404'd update.
	_, getErr := agentstore.New(tmpDir).Get("test-agent")
	assert.True(t, errors.Is(getErr, entity.ErrNotFound),
		"a 404'd update must not create a phantom agent entity record; got err=%v", getErr)
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
	exec := agentExecutorFromStore(t, api.homePath, id)
	require.NotNil(t, exec)
	assert.Equal(t, "codex", exec.CLI,
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
	exec := agentExecutorFromStore(t, api.homePath, created.Id)
	require.NotNil(t, exec)
	assert.Equal(t, "/usr/local/bin/claude", exec.CLIPath)
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

	exec := agentExecutorFromStore(t, api.homePath, id)
	require.NotNil(t, exec)
	assert.Equal(t, "/opt/codex-v2", exec.CLIPath,
		"cli_path must be updated to the new value")
}
