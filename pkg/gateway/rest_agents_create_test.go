// rest_agents_create_test.go: tests for create an agent, and decode the create request's wire variants

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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// ---------------------------------------------------------------------------
// Issue #571 — "property not mechanism" proof.
//
// The CI llm-conformance shard's Conformance_t3_PlanningReplanningE2E test
// failed deterministically (3/3: initial + both retries) with a severed
// socket on its very first setup step, POST /api/v1/agents. The gateway log
// showed the full-service restart cascade (channels, cron, the plan engine,
// task trigger scheduler, loop scheduler, queued-task drain, mailbox drain —
// all restarting) repeating on every create under shard load, severing the
// request before it completed.
//
// A test asserting only "the created agent is usable" does not prove the
// cascade is gone — a full reload that happens to complete fast enough would
// pass that assertion too. These tests assert the CASCADE ITSELF never runs
// for a plain create/update (no concurrent reload in flight): the reload
// trigger — the ONLY thing that can invoke handleConfigReload's
// restartServices — must be called ZERO times.
// ---------------------------------------------------------------------------

// TestCreateAgent_DoesNotTriggerFullReload proves a plain POST /agents (no
// concurrent reload in flight) never invokes the reload trigger at all, and
// therefore can never reach the restartServices cascade that severed
// requests under CI load. The agent must still be immediately resolvable via
// the fast path alone (AgentLoop.UpsertAgentFast), with zero reloads.
func TestCreateAgent_DoesNotTriggerFullReload(t *testing.T) {
	api := buildExecutorTestAPI(t)

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	body := `{"name":"NoCascadeTest","type":"Main","soul":"soul content"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a plain create must not carry a warning: %+v", resp.Warning)

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a plain POST /agents create (no concurrent reload in flight) must NEVER invoke the reload "+
			"trigger — invoking it is what reaches handleConfigReload's restartServices cascade "+
			"(channels/cron/plan-engine/task-trigger/loop-scheduler/task-drain/mailbox-drain all "+
			"restart), the exact mechanism that severed POST /agents requests under CI load "+
			"(Conformance_t3_PlanningReplanningE2E, llm-conformance shard, socket hang up)")

	_, ok := api.agentLoop.GetRegistry().GetAgent(resp.Id)
	require.True(t, ok, "the new agent must be resolvable in the registry via the fast path alone, with zero reloads")
}

// TestCreateAgent_DuringInFlightReload_IsImmediatelyTaskAssignable is the DoD
// test for the blocker. It goes RED with either half of the fix reverted:
//
//   - revert part 1 (trigger drops instead of coalescing): createAgent's
//     triggerReloadAndWait returns when the IN-FLIGHT reload ends, and that
//     reload's config snapshot predates the create's write, so the registry
//     never learns about the agent.
//   - revert part 2 (bare TriggerReload): the trigger coalesces and returns nil
//     immediately, so the handler answers 201 before the follow-up reload has
//     run at all.
func TestCreateAgent_DuringInFlightReload_IsImmediatelyTaskAssignable(t *testing.T) {
	h := newReloadHarness(t)

	entered, release := h.blockFirstReload()

	// Reload #1 starts and parks inside the executor: its config snapshot is
	// taken NOW, before the agent below exists anywhere on disk.
	require.NoError(t, h.svc.reloadTrigger(), "starting the first reload must succeed")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("the first reload never started executing")
	}

	done := make(chan createAgentResult, 1)
	go func() { done <- h.postAgent("Coalesce Target") }()

	// Only release reload #1 once the create's own reload request has landed
	// against it — otherwise the test would exercise a boring sequential
	// ordering instead of the mid-reload interleaving that breaks.
	coalesced := h.waitForCoalescedRequest(3 * time.Second)
	release()

	var res createAgentResult
	select {
	case res = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("createAgent never returned")
	}

	assert.True(t, coalesced,
		"the reload POST /agents requested while another reload was in flight must be "+
			"recorded for a follow-up reload, not dropped")
	assertUsable(t, res)

	// The follow-up reload must have re-read from disk, not replayed the
	// snapshot reload #1 already used.
	rosters := h.snapshotRosters()
	require.GreaterOrEqual(t, len(rosters), 2,
		"a coalesced request must produce a SECOND reload; only %d ran", len(rosters))
	assert.NotContains(t, rosters[0], res.id,
		"precondition: reload #1's snapshot must predate the create (else the test proves nothing)")
	assert.Contains(t, rosters[len(rosters)-1], res.id,
		"the coalesced follow-up reload must re-read config from disk and see the new agent")
}

// TestCreateAgent_TwoRapidCreatesDuringOneReload_BothTaskAssignable proves the
// fix is real coalescing and not a single retry: two creates that both land
// inside one long reload must BOTH be registry-visible when their handlers
// return. A one-shot retry would serve the first and lose the second.
func TestCreateAgent_TwoRapidCreatesDuringOneReload_BothTaskAssignable(t *testing.T) {
	h := newReloadHarness(t)

	entered, release := h.blockFirstReload()

	require.NoError(t, h.svc.reloadTrigger(), "starting the first reload must succeed")
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		release()
		t.Fatal("the first reload never started executing")
	}

	first := make(chan createAgentResult, 1)
	second := make(chan createAgentResult, 1)
	go func() { first <- h.postAgent("Coalesce Rapid One") }()
	// Sequencing only, and deliberately non-fatal: failing here would abandon
	// two in-flight handler goroutines mid-test. The outcome assertions below
	// are what decide the verdict.
	coalescedFirst := h.waitForCoalescedRequest(3 * time.Second)
	go func() { second <- h.postAgent("Coalesce Rapid Two") }()
	time.Sleep(100 * time.Millisecond)

	// Both creates are now outstanding against a single blocked reload.
	release()

	var resA, resB createAgentResult
	for i := 0; i < 2; i++ {
		select {
		case resA = <-first:
			first = nil
		case resB = <-second:
			second = nil
		case <-time.After(15 * time.Second):
			t.Fatal("a createAgent call never returned")
		}
	}

	assert.True(t, coalescedFirst,
		"the first create's reload request must be recorded against the in-flight reload")
	assertUsable(t, resA)
	assertUsable(t, resB)
	assert.NotEqual(t, resA.id, resB.id, "the two creates must produce distinct agents")
}

// TestCreateAgent_ReloadSlowerThanWaitDeadline_IsStillTaskAssignable is the
// regression test for the SECOND half of this release blocker — the half the
// coalescing fix did not touch, and which kept the llm-conformance e2e shard
// failing 3/9 with the original signature after coalescing had shipped.
//
// triggerReloadAndWait polled for a hard-coded 5 SECONDS and then `return nil`
// — reporting success it had not observed. A reload restarts every channel,
// cron, the plan engine and the provider before rebuilding the AgentRegistry;
// idle that is milliseconds, but under the conformance shard's load (live LLM
// turns + a busy plan engine) it runs to tens of seconds. Every reload that
// overran 5s therefore produced a 201 with NO warning and a registry that still
// had no such agent, so the next POST /tasks answered `agent "x" not found` and
// POST /workspaces answered `core_team member "x" is not a registered agent`.
//
// This is why coalescing alone was not enough: it guaranteed the right reload
// was QUEUED and would run, but the caller stopped waiting for it and lied
// about the result.
//
// The reload here takes 6s — longer than the old deadline, far inside the new
// one — and the assertion is the same user-visible outcome as every other test
// in this file.
func TestCreateAgent_ReloadSlowerThanWaitDeadline_IsStillTaskAssignable(t *testing.T) {
	h := newReloadHarness(t)

	// Every reload takes longer than the retired 5s deadline. Deliberately a
	// wall-clock sleep: the property under test IS a wall-clock deadline.
	const slowReload = 6 * time.Second
	require.Greater(t, slowReload, 5*time.Second,
		"the simulated reload must exceed the deadline this test exists to retire")
	require.Less(t, slowReload, reloadWaitTimeout,
		"...and must sit inside the current deadline, or this asserts the wrong thing")
	h.beforeExec = func(int) { time.Sleep(slowReload) }

	res := h.postAgent("Slow Reload Target")
	assertUsable(t, res)
}

// TestCreateAgent_ModelParams_Persisted_Main proves the create-path fix for
// a Main agent: POST with model_params -> 201, response echoes it, the
// persisted entity record on disk carries it, and an independent GET echoes
// it too.
//
// Mutation check: reverting the create-path persist (the modelParamsIn ->
// ac.ModelParams assignment in createAgent) makes this fail at the
// "persisted entity record" and "GET echo" assertions — the create would
// still return 201 (the exact silent-drop bug), just like PUT did before
// 2b057e15.
func TestCreateAgent_ModelParams_Persisted_Main(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Model Params Main","type":"Main","soul":"mp-main-soul","model_params":{"max_tokens":48,"temperature":0.2}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())

	require.NotNil(t, created.ModelParams, "create response must echo model_params, not null")
	require.NotNil(t, created.ModelParams.MaxTokens)
	assert.Equal(t, 48, *created.ModelParams.MaxTokens, "create response must echo max_tokens")
	require.NotNil(t, created.ModelParams.Temperature)
	assert.InDelta(t, 0.2, *created.ModelParams.Temperature, 0.0001, "create response must echo temperature")

	// Persisted entity record on disk (agentstore.Store, ADR-054 D2), not
	// just the in-memory create response.
	store := agentstore.New(api.homePath)
	rec, err := store.Get(created.Id)
	require.NoError(t, err)
	require.NotNil(t, rec.ModelParams, "persisted entity record must carry model_params")
	require.NotNil(t, rec.ModelParams.MaxTokens)
	assert.Equal(t, 48, *rec.ModelParams.MaxTokens)
	require.NotNil(t, rec.ModelParams.Temperature)
	assert.InDelta(t, 0.2, *rec.ModelParams.Temperature, 0.0001)

	// A completely separate GET must echo the same values.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	api.HandleAgents(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "get body: %s", wGet.Body.String())
	got := decodeAgentResp(t, wGet.Body.Bytes())
	require.NotNil(t, got.ModelParams, "GET must echo model_params, not null")
	require.NotNil(t, got.ModelParams.MaxTokens)
	assert.Equal(t, 48, *got.ModelParams.MaxTokens)
	require.NotNil(t, got.ModelParams.Temperature)
	assert.InDelta(t, 0.2, *got.ModelParams.Temperature, 0.0001)
}

// TestCreateAgent_ModelParams_Persisted_Subagent is the same round trip for
// the Subagent (native worker) variant — AgentCreateRequestSubagent also
// carries model_params on the wire.
func TestCreateAgent_ModelParams_Persisted_Subagent(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Model Params Sub","type":"Subagent","description":"mp subagent regression","soul":"mp-sub-soul","model_params":{"max_tokens":777,"temperature":0.6}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	require.Equal(t, gen.AgentTypeSubagent, created.Type)

	require.NotNil(t, created.ModelParams, "create response must echo model_params, not null")
	require.NotNil(t, created.ModelParams.MaxTokens)
	assert.Equal(t, 777, *created.ModelParams.MaxTokens)
	require.NotNil(t, created.ModelParams.Temperature)
	assert.InDelta(t, 0.6, *created.ModelParams.Temperature, 0.0001)

	store := agentstore.New(api.homePath)
	rec, err := store.Get(created.Id)
	require.NoError(t, err)
	require.NotNil(t, rec.ModelParams, "persisted entity record must carry model_params")
	require.NotNil(t, rec.ModelParams.MaxTokens)
	assert.Equal(t, 777, *rec.ModelParams.MaxTokens)
	require.NotNil(t, rec.ModelParams.Temperature)
	assert.InDelta(t, 0.6, *rec.ModelParams.Temperature, 0.0001)

	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+created.Id, nil)
	api.HandleAgents(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code, "get body: %s", wGet.Body.String())
	got := decodeAgentResp(t, wGet.Body.Bytes())
	require.NotNil(t, got.ModelParams, "GET must echo model_params, not null")
	require.NotNil(t, got.ModelParams.MaxTokens)
	assert.Equal(t, 777, *got.ModelParams.MaxTokens)
	require.NotNil(t, got.ModelParams.Temperature)
	assert.InDelta(t, 0.6, *got.ModelParams.Temperature, 0.0001)
}

// TestCreateAgent_Worker_PersistsSoul writes a worker with an initial soul and
// asserts both the response and the on-disk SOUL.md carry the value (the
// create-time write was previously dropped on the floor).
func TestCreateAgent_Worker_PersistsSoul(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// AgentCreateRequestSubagent has no executor property at all (native is
	// always server-derived for a Subagent with no executor block) — sending
	// one is now rejected 400 by createAgent's strict decode.
	body := `{"name":"Soulful Worker","type":"Subagent","description":"persists soul regression","soul":"worker-soul-X"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
	assert.Equal(t, "worker-soul-X", created.Soul, "response soul must echo the persisted value")

	// On disk — must match the value the caller sent.
	got := readSoulMDForAgent(t, api, created.Id)
	assert.Equal(t, "worker-soul-X", got, "SOUL.md must be persisted at create time")
}

// TestCreateAgent_Custom_PersistsSoul is the same round-trip for a custom
// agent — custom creates may also carry an initial soul (the FE profile
// flow writes it later; an initial soul is permitted).
func TestCreateAgent_Custom_PersistsSoul(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Soulful Custom","type":"Main","soul":"custom-soul-X"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)
	assert.Equal(t, "custom-soul-X", created.Soul, "response soul must echo the persisted value")

	got := readSoulMDForAgent(t, api, created.Id)
	assert.Equal(t, "custom-soul-X", got, "SOUL.md must be persisted at create time")
}

// TestCreateAgent_Worker_RequiresExecutor verifies that a Subagent create
// without an executor is accepted and derives kind=native server-side.
func TestCreateAgent_Worker_DerivesNativeExecutor(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker No Exec","type":"Subagent","description":"missing executor regression","soul":"worker-soul-noexec"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
	require.NotNil(t, created.Executor, "derived native executor must be echoed")
	require.NotNil(t, created.Executor.Kind, "derived executor.kind must be present")
	assert.Equal(t, gen.AgentExecutorKindNative, *created.Executor.Kind)
}

// TestCreateAgent_Worker_ExecutorFieldRejected proves the unconditional
// strict-decode enforcement: AgentCreateRequestSubagent has no `executor`
// property at all — a Subagent create can no longer request remote-a2a (or
// any other) executor kind directly at create time (the field matrix marks
// Subagent's executor row "— (native)"). With a DEFAULT config
// (ValidateInbound off — buildExecutorTestAPI does not opt in), an
// `executor` key present in the JSON body is now rejected 400 by
// createAgent's strict decode, not silently dropped. (remote-a2a is still
// reachable via PUT — AgentUpdateRequest remains one flat type shared by
// every agent type; see TestUpdateAgent_WorkerAllowsRemoteA2AExecutor in
// rest_agent_executor_test.go.)
func TestCreateAgent_Worker_ExecutorFieldRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker Remote A2A","type":"Subagent","description":"remote-a2a regression","executor":{"kind":"remote-a2a"},"soul":"remote-a2a-soul"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "executor")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent")
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

// TestCreateAgent_WorkerVoiceFieldRejected proves the unconditional
// strict-decode enforcement for the Main-only voice field:
// AgentCreateRequestSubagent (and AgentCreateRequestSubagent3p) structurally
// have no `voice` property at all (additionalProperties: false — the field
// matrix marks voice "Main-only among user types"). With a DEFAULT config
// (ValidateInbound off, the default in this harness), a Subagent create
// carrying a `voice` key is now rejected 400 by createAgent's strict decode,
// not silently dropped. (With ValidateInbound enabled the same body 400s at
// the schema gate instead.) The PUT-time guard (a worker cannot be given a
// non-empty voice) is unaffected — see TestUpdateAgent_RejectsVoiceOnWorker
// in rest_agent_executor_test.go.
func TestCreateAgent_WorkerVoiceFieldRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"W","type":"Subagent","description":"d","soul":"s","voice":"alloy"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "voice")
	assert.Contains(t, w.Body.String(), "AgentCreateRequestSubagent")
}

// TestCreateAgent_ImmediateSessionCreate_NoRace is the primary reproduction:
// create an agent, then — with no sleep at all — immediately try to open a
// chat session against it via the real POST /api/v1/sessions path
// (createSessionHTTP -> GetAgentStore -> GetRegistry -> the registry
// ReloadProviderAndConfig swaps). This must succeed: a 201 from POST
// /api/v1/agents is a durability+resolvability guarantee, not a "check back
// later" promise.
func TestCreateAgent_ImmediateSessionCreate_NoRace(t *testing.T) {
	api := buildExecutorTestAPI(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	body := `{"name":"RaceAgent","type":"Main","soul":"soul content"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader([]byte(body)))
	r.Header.Set("Content-Type", "application/json")
	api.createAgent(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "agent create failed: %s", w.Body.String())

	var created gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id, "created agent must have an id")

	// No sleep here — this is the exact sequence the bug report reproduced via
	// curl (create, then immediately use).
	sessBody, err := json.Marshal(gen.SessionCreateRequest{AgentId: &created.Id})
	require.NoError(t, err)
	sw := httptest.NewRecorder()
	sr := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", bytes.NewReader(sessBody))
	sr.Header.Set("Content-Type", "application/json")
	api.createSessionHTTP(sw, sr)

	assert.Equal(t, http.StatusCreated, sw.Code,
		"POST /api/v1/sessions immediately after POST /api/v1/agents returned %d (expected 201) — "+
			"the new agent must be resolvable via the runtime registry the instant createAgent "+
			"responds 201, not up to ~reloadDelay later: %s",
		sw.Code, sw.Body.String())
}

// TestCreateAgent_RejectsReservedDefaultName verifies createAgent 400s on a
// name of "default", case-insensitively.
func TestCreateAgent_RejectsReservedDefaultName(t *testing.T) {
	for _, name := range []string{"default", "Default", "DEFAULT", "  default  "} {
		t.Run("name="+name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)

			body := `{"name":"` + name + `","type":"Main","soul":"s"}`
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code,
				"create with reserved name %q must be rejected 400, got body: %s", name, w.Body.String())
			assert.Contains(t, w.Body.String(), "reserved",
				"error body must explain the name is reserved")
		})
	}
}

// TestCreateAgent_AllowsNonReservedNameContainingDefault verifies the check
// is an exact match (after trim/case-fold), not a substring match — a name
// like "Default Assistant" must NOT be rejected.
func TestCreateAgent_AllowsNonReservedNameContainingDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Default Assistant","type":"Main","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code,
		"a name merely containing \"default\" as a substring must be allowed, got body: %s", w.Body.String())
}

// TestCreateAgent_TypeOmitted_Rejected is the W1 behavior-change guard: the
// historical omit-type→Main default is RETIRED. type is now a required,
// single-value discriminator on every create variant — a body without it
// must 400 rather than silently defaulting to Main. Supersedes the old
// TestCreateAgent_TypeOmitted_DefaultsToCustom.
func TestCreateAgent_TypeOmitted_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Typed Omit","soul":"omitted-type-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "type is required")

	entry := findTypeTestAgentInStore(t, api.homePath, "Typed Omit")
	assert.Nil(t, entry, "a rejected create must not persist anything")
}

// TestCreateAgent_TypeCustom_Explicit verifies an explicit "custom" type
// still works (the case where the SPA always sends the field).
func TestCreateAgent_TypeCustom_Explicit(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Typed Custom","type":"Main","soul":"typed-custom-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeMain, created.Type)

	entry := findTypeTestAgentInStore(t, api.homePath, "Typed Custom")
	require.NotNil(t, entry)
	assert.Equal(t, config.AgentTypeCustom, entry.Type)
}

// TestCreateAgent_TypeWorker_PersistsAndEchoes proves the worker create
// path: the on-disk type is "worker", the response echoes "worker", and
// the agent is NOT marked default (the single-default invariant applies;
// a freshly-created worker is just a delegation leaf).
func TestCreateAgent_TypeWorker_PersistsAndEchoes(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			// AgentCreateRequestSubagent has no executor property at all —
			// native is always server-derived when the block is absent.
			`{"name":"My Worker","type":"Subagent","description":"create-worker regression","soul":"my-worker-soul"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)
	// Locked must be false: a freshly-created worker is editable. The seeded
	// default general-purpose worker is locked by coreagent.SeedConfig, but
	// that path is not the REST create path.
	assert.False(t, created.Locked, "newly-created worker must not be locked")
	assert.False(t, created.Default != nil && *created.Default,
		"newly-created worker must not be the default")

	// Persisted to config.json with type=worker and no default flag.
	entry := findTypeTestAgentInStore(t, api.homePath, "My Worker")
	require.NotNil(t, entry, "worker must be persisted")
	assert.Equal(t, config.AgentTypeWorker, entry.Type)
}

// TestCreateAgent_TypeSubagent3p_ExternalExecutorPersists proves the
// discriminated-union create path for an External CLI worker: type must be
// "subagent_3p" directly (W1 retired the old "Subagent + executor.kind=
// external-cli reclassifies as subagent_3p" behavior — AgentCreateRequestSubagent
// has no executor property at all any more, so there is nothing to
// reclassify from). Supersedes the old
// TestCreateAgent_TypeWorker_AllowsNonNativeExecutor.
func TestCreateAgent_TypeSubagent3p_ExternalExecutorPersists(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Worker External","type":"subagent_3p","description":"external-cli worker","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"},"soul":"external-cli-soul"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent3p, created.Type)
	require.NotNil(t, created.Executor, "subagent_3p response must echo executor")
	require.NotNil(t, created.Executor.Kind, "executor.kind must be present")
	assert.Equal(t, gen.AgentExecutorKindExternalCli, *created.Executor.Kind)
	require.NotNil(t, created.Executor.Cli, "executor.cli must be present")
	assert.Equal(t, gen.ExternalCliToolCodex, *created.Executor.Cli)

	entry := findTypeTestAgentInStore(t, api.homePath, "Worker External")
	require.NotNil(t, entry)
	require.NotNil(t, entry.Subagents, "executor subagents must be persisted for a worker")
	exec := entry.Subagents.Executor
	require.NotNil(t, exec)
	assert.Equal(t, config.ExecutorKindExternalCLI, exec.Kind)
	assert.Equal(t, "codex", exec.CLI)
}

// TestCreateAgent_NonWorker_ExecutorFieldRejected is the regression guard for
// the native-only-for-non-workers rule, updated for the unconditional
// strict-decode enforcement: AgentCreateRequestMain has no `executor`
// property at all, so a custom (Main) create that supplies one is rejected
// 400 by createAgent's strict decode — even with ValidateInbound OFF (the
// default in this harness). See TestCreateAgent_Main_ExecutorFieldRejected
// in rest_agent_executor_test.go for the full assertion; this test is the
// type-dispatch-focused sibling, and also confirms nothing is persisted.
func TestCreateAgent_NonWorker_ExecutorFieldRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Bad Custom","type":"Main","executor":{"kind":"external-cli","cli":"codex"},"soul":"soul-text"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "executor")

	entry := findTypeTestAgentInStore(t, api.homePath, "Bad Custom")
	assert.Nil(t, entry, "a rejected create must not persist anything")
}

// TestCreateAgent_TypeCore_Rejected proves "core" is a seeded-only
// classification and is not creatable from the API.
func TestCreateAgent_TypeCore_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Fake Core","type":"core"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "must be one of Main, Subagent, subagent_3p",
		"the rejection must name the type-validity rule")
}

// TestCreateAgent_TypeSystem_Rejected proves "system" is a seeded-only
// classification and is not creatable from the API. ADR-049 D3: System Agents
// (the Judge category) are seeded exclusively via coreagent.SeedConfig; the REST
// create path rejects type:system with a precise 400.
func TestCreateAgent_TypeSystem_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Fake System","type":"system"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "system agents are not creatable",
		"ADR-049 D3: the rejection must name the seed-only System Agent rule")
}

// TestCreateAgent_TypeUnknown_Rejected proves any non-enum value is 400.
// The Zod/openapi-typescript validation should normally catch this before
// the handler runs, but the gateway still re-validates the enum and
// returns 400 if the JSON somehow reaches the handler unvalidated.
func TestCreateAgent_TypeUnknown_Rejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Bad Type","type":"bot"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
}

// TestCreateAgent_Subagent_DelegationPolicyRejected proves ADR-037's retirement
// of delegation_policy from the wire holds for the Subagent (native worker)
// variant too, not just subagent_3p (see
// TestCreateAgent_Subagent3p_DelegationPolicyRejected in
// rest_agent_executor_test.go and TestContract_AgentCreateRequestSubagent3p_DelegationPolicyRejected
// in pkg/api/generated/contract_test.go). This test replaces the three
// pre-ADR-037 tests that exercised create-time to[]/depth validation
// (TestCreateAgent_Worker_AllowsNonEmptyToList,
// TestCreateAgent_Worker_DepthExceededRejected,
// TestCreateAgent_Worker_AllowsEmptyToList) — that validation no longer
// exists; delegation_policy is now an unconditionally-rejected unknown field
// regardless of its content.
func TestCreateAgent_Subagent_DelegationPolicyRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Worker With To","type":"Subagent","description":"delegation_policy retired","soul":"worker-with-to-soul","delegation_policy":{"to":[{"kind":"local","id":"test-agent"}],"modes":["task"],"depth":1}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "delegation_policy")
}

// TestCreateAgent_Bash_NewCustomAgentDeniedByDefault proves that a fresh
// custom agent created via the REAL REST POST /api/v1/agents handler, with
// no explicit bash policy entry supplied by the caller, resolves the bash
// tool to "deny" by default.
func TestCreateAgent_Bash_NewCustomAgentDeniedByDefault(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Deliberately no tools_cfg override — proving the DEFAULT seed, not a
	// caller-supplied one.
	body := `{"name":"Research Bot","type":"Main","description":"A research assistant","soul":"You are a research bot."}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())

	// Load-bearing assertion: read the persisted agent back out of the LIVE
	// config (createAgent's TriggerReload() has already run by the time the
	// handler returns), the same way the frontend's subsequent GET/list would.
	cfg := api.agentLoop.GetConfig()
	var newAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == created.Id {
			newAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, newAgent, "created agent must be present in the reloaded live config")
	require.NotNil(t, newAgent.Tools, "created agent must have a Tools config")

	// Sanity: the seed actually landed in the persisted policy map.
	require.Equal(t, config.ToolPolicyDeny, newAgent.Tools.Builtin.Policies["bash"],
		`expected seeded Tools.Builtin.Policies["bash"] = deny`)

	// Resolve through the single authoritative primitive
	// (pkg/tools/compositor.go's EffectiveToolPolicy), built the same way
	// pkg/agent/instance.go's agentToolsCfgToPolicy converts
	// AgentBuiltinToolsCfg for a non-god-mode agent. There is no
	// DefaultPolicy/GlobalDefaultPolicy field any more (CLAUDE.md hard
	// constraint 6) — an uncovered tool now fails closed to "deny" inside
	// EffectiveToolPolicy itself, so this test's seeded "bash": deny entry is
	// asserted directly rather than via a fallback field.
	policies := make(map[string]config.ToolPolicy, len(newAgent.Tools.Builtin.Policies))
	for k, v := range newAgent.Tools.Builtin.Policies {
		policies[k] = v
	}
	polCfg := &tools.ToolPolicyCfg{
		Policies: policies,
	}
	agentType := string(newAgent.ResolveType(nil))
	require.Equal(t, string(config.AgentTypeCustom), agentType,
		"test setup invariant: a REST-created Main agent resolves to AgentTypeCustom")

	got := tools.EffectiveToolPolicy(polCfg, tools.ScopeCore, agentType, "bash")
	require.Equal(t, "deny", got,
		`EffectiveToolPolicy(bash, ScopeCore, %q) must be "deny"`, agentType)
}

// TestCreateAgent_Bash_CallerOverrideRespected proves the seed is a
// default, not a hard rail: a caller that explicitly sets bash:allow via
// tools_cfg on create gets that value, not the seeded deny.
func TestCreateAgent_Bash_CallerOverrideRespected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// There is no default_policy field on the wire any more (CLAUDE.md hard
	// constraint 6) — createAgent's strict decode (decodeAgentCreateVariant,
	// DisallowUnknownFields, independent of ValidateInbound) rejects a stray
	// "default_policy" key inside tools_cfg.builtin with 400, so it must not
	// appear here.
	//
	// CONTRACT CHANGE (2026-09-02): this body used to be the SPARSE
	// `{"bash":"allow"}` map, relying on createAgent merging it on top of the
	// deny-seeded baseline. That merge ran BEFORE validation, which is exactly
	// how a caller-side gap became structurally undetectable — a live UAT round
	// created an agent with `bash` omitted entirely and got 201, with the
	// server silently filling a value nobody sent. A submitted policy map is
	// now validated for completeness before any merge, so the caller must
	// enumerate the whole static catalog explicitly.
	//
	// The claim this test makes is UNCHANGED and still exercised: the seed is a
	// default, not a hard rail — a caller who explicitly says bash:allow gets
	// allow, and every tool they left at deny stays denied.
	policies := fullBuiltinPolicyMap("deny")
	policies["bash"] = "allow"
	body := `{"name":"Bash Enabled Bot","type":"Main","description":"Needs bash","soul":"s",` +
		`"tools_cfg":{"builtin":{"policies":` + mustPolicyJSON(t, policies) + `}}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	created := decodeAgentResp(t, w.Body.Bytes())

	cfg := api.agentLoop.GetConfig()
	var newAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == created.Id {
			newAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, newAgent, "created agent must be present in the reloaded live config")
	require.NotNil(t, newAgent.Tools, "created agent must have a Tools config")
	require.Equal(t, config.ToolPolicyAllow, newAgent.Tools.Builtin.Policies["bash"],
		"explicit caller override must win over the default deny seed")
	// An unrelated seed entry must still be present at its seeded "deny"
	// value (the caller only overrode "bash"). "system.*" was the OLD,
	// retired wildcard mechanism (matched zero real tool names) this test
	// used to check — the current seed (coreagent.NewCustomAgentToolsCfg,
	// denyAllThenOverride) is a fully-enumerated, wildcard-free map, so the
	// equivalent "untouched" check is any other real tool name that stays at
	// its seeded deny default.
	require.Equal(t, config.ToolPolicyDeny, newAgent.Tools.Builtin.Policies["delete_agent"],
		"a tool the caller left at deny must stay denied")
}

// ── Handler integration tests — createAgent ───────────────────────────────────

// TestCreateAgent_ValidateInbound_MissingType asserts POST /agents returns 400
// for a body with no "type" field at all — type is now a required
// discriminator (W1) and createAgent's peek-the-type dispatch rejects this
// BEFORE it would even reach schema validation. Replaces the old
// TestCreateAgent_ValidateInbound_InvalidBody (which asserted a
// missing-"name" 400 against the retired flat "AgentCreateRequest" schema).
func TestCreateAgent_ValidateInbound_MissingType(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "type is required")
}

// TestCreateAgent_ValidateInbound_InvalidBody asserts POST /agents with
// validate_inbound=true returns 400 for a body missing the required "name"
// and "soul" fields, once type dispatch has already resolved to the Main
// variant schema (contracts/components/schemas/AgentCreateRequestMain.yaml).
func TestCreateAgent_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(`{"type":"Main"}`))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
}

// TestCreateAgent_ValidateInbound_ValidBody asserts POST /agents with
// validate_inbound=true accepts a body with the required "type"/"name"/"soul" fields.
func TestCreateAgent_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"Main","name":"Test Agent","description":"desc","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	// 200 or 201 — agent created successfully
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusCreated,
		"expected 200 or 201, got %d: %s", w.Code, w.Body.String())
}

// TestCreateAgent_ValidateInbound_MainWithExecutorRejected asserts that a
// Main create carrying an `executor` property is rejected 400 at the schema
// gate when ValidateInbound is enabled: AgentCreateRequestMain has no
// executor property at all (additionalProperties: false) — Main agents
// always run native and cannot express one.
func TestCreateAgent_ValidateInbound_MainWithExecutorRejected(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"Main","name":"Bad Main","soul":"s","executor":{"kind":"external-cli","cli":"codex","cli_path":"/usr/local/bin/codex"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestMain")
}

// TestCreateAgent_ValidateInbound_SubagentWithExecutorRejected asserts that a
// Subagent (native worker) create carrying an `executor` property is
// rejected 400 at the schema gate when ValidateInbound is enabled:
// AgentCreateRequestSubagent has no executor property either — kind=native
// is always server-derived for this variant.
func TestCreateAgent_ValidateInbound_SubagentWithExecutorRejected(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"Subagent","name":"Bad Subagent","description":"d","soul":"s","executor":{"kind":"native"}}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestSubagent")
}

// TestCreateAgent_ValidateInbound_Subagent3pMaxToolIterationsRejected asserts
// that a subagent_3p create carrying `max_tool_iterations` is rejected 400 at
// the schema gate when ValidateInbound is enabled: AgentCreateRequestSubagent3p
// has no max_tool_iterations property (the field matrix's "exclude" decision
// for this variant — the external CLI runs its own turn loop, so Omnipus
// cannot cap its per-turn tool-call budget).
func TestCreateAgent_ValidateInbound_Subagent3pMaxToolIterationsRejected(t *testing.T) {
	api := newTestRestAPIWithValidation(t)

	body := `{"type":"subagent_3p","name":"Bad 3p","soul":"s","executor":{"cli":"codex","cli_path":"/usr/local/bin/codex"},"max_tool_iterations":50}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createAgent(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentCreateRequestSubagent3p")
}

// --- Fix #5: validateSkillIDs fail-open when no skills are installed ---

// TestCreateAgent_WithSkillID_NoInstalledSkills verifies the deliberate
// fail-open behavior: when the installed-skills set is empty (fresh install,
// test environment with no skills directory), POST /api/v1/agents with an
// arbitrary skill id returns 201. The runtime filter in the agent loop is the
// final enforcement gate; the REST layer must not block agents on unknown skill
// ids when it cannot tell what is installed.
//
// BDD:
//
//	Given a running gateway with no installed skills directory,
//	When POST /api/v1/agents with skills=["some-unknown-skill"],
//	Then 201 is returned (fail-open: no installed set → accept any id).
func TestCreateAgent_WithSkillID_NoInstalledSkills(t *testing.T) {
	// Isolate from any real ~/.omnipus/skills directory and from the working-dir
	// skills (if any). This gives the "no skills installed" fail-open scenario.
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", t.TempDir())
	// newTestRestAPIWithHome uses an empty tmpDir as agentLoop's config, which
	// provides no skills — exactly the "no skills directory" scenario.
	api := newTestRestAPIWithHome(t)

	skillID := "some-unknown-skill"
	skills := []string{skillID}
	body := map[string]any{
		"name":   "skill-test-agent",
		"type":   "Main",
		"soul":   "s",
		"skills": skills,
	}
	bodyBytes, err := json.Marshal(body)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", bytes.NewReader(bodyBytes))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusCreated, w.Code,
		"createAgent must return 201 (fail-open) when no skills are installed: %s", w.Body.String())
}

// TestCreateAgent_WithToolsCfg verifies POST /api/v1/agents with tools_cfg persists the tools config.
// BDD: Given a create-agent request with tools_cfg,
// When POST /api/v1/agents is called,
// Then the response includes the agent and the tools config is accepted.
// Traces to: parsed-inventing-gem.md — createAgent accepts tools_cfg
func TestCreateAgent_WithToolsCfg(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// There is no default_policy field on the wire any more (CLAUDE.md hard
	// constraint 6) — createAgent's strict decode (decodeAgentCreateVariant,
	// DisallowUnknownFields, independent of ValidateInbound) rejects a stray
	// "default_policy" key inside tools_cfg.builtin with 400, so it must not
	// appear here.
	//
	// CONTRACT CHANGE (2026-09-02): the policies map below used to name only
	// the 3 tools this test cares about, relying on createAgent merging them
	// onto the deny-seeded baseline. That merge ran BEFORE
	// config.ValidateToolPolicyCoverage, so a caller-side gap could never be
	// observed on this path — a live UAT round created agents with tools
	// omitted, and with a literal "*" key, and got 201 for both. A submitted
	// map is now validated for completeness and key legitimacy before any
	// merge, so the caller enumerates the whole static catalog. The claims
	// below are unchanged: the caller's values win, and a tool left at deny
	// stays denied.
	reqPolicies := fullBuiltinPolicyMap("deny")
	reqPolicies["read_file"] = "allow"
	reqPolicies["search_web"] = "allow"
	reqPolicies["fetch_url"] = "allow"
	body := `{
		"name": "Research Bot",
		"type": "Main",
		"description": "A researcher",
		"soul": "Research Bot soul",
		"color": "#22C55E",
		"icon": "magnifying-glass",
		"tools_cfg": {
			"builtin": {
				"policies": ` + mustPolicyJSON(t, reqPolicies) + `
			},
			"mcp": {
				"servers": [{"id": "my-server"}]
			}
		}
	}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body: %s", w.Body.String())

	var resp struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Research Bot", resp.Name)
	assert.Equal(t, "Main", resp.Type)
	assert.NotEmpty(t, resp.ID)

	// Verify the agent entity record (entities/agents/<id>.json) was
	// persisted with the tools config — createAgent persists via the agent
	// store, not config.json's agents.list (ADR-054 D2). There is no
	// default_policy field on config.AgentConfig any more (CLAUDE.md hard
	// constraint 6 — the field was removed project-wide), so the persisted
	// builtin map carries only a complete "policies" map by construction.
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get(resp.ID)
	require.NoError(t, err, "created agent must exist as a real entity-store record")
	assert.Equal(t, "#22C55E", savedAgent.Color)
	assert.Equal(t, "magnifying-glass", savedAgent.Icon)
	require.NotNil(t, savedAgent.Tools, "tools config must be persisted")
	policies := savedAgent.Tools.Builtin.Policies
	// The caller's explicit allow entries win...
	assert.Equal(t, config.ToolPolicyAllow, policies["read_file"])
	assert.Equal(t, config.ToolPolicyAllow, policies["search_web"])
	assert.Equal(t, config.ToolPolicyAllow, policies["fetch_url"])
	// ...and every other tool the caller enumerated at deny is persisted
	// explicitly at deny — bash specifically (CRIT-001/FR-B12).
	assert.Equal(t, config.ToolPolicyDeny, policies["bash"])

	// config.json itself must carry no agents.list content — agents are
	// per-entity records now, never config.json entries (ADR-054).
	savedCfgRaw, err := os.ReadFile(cfgPath)
	require.NoError(t, err)
	var savedMap map[string]any
	require.NoError(t, json.Unmarshal(savedCfgRaw, &savedMap))
	if agentsSection, ok := savedMap["agents"].(map[string]any); ok {
		if list, ok := agentsSection["list"].([]any); ok {
			assert.Empty(t, list, "config.json agents.list must stay empty — agents persist only to entities/agents/")
		}
	}
}

// TestCreateAgent_WithSkills verifies POST /api/v1/agents with skills persists and
// returns the skill list.
//
// BDD: Given a create-agent request with a skills list,
// When POST /api/v1/agents is called,
// Then the response includes the skills list and config.json persists it.
//
// Traces to: US-E6 (nontech-ux-hardening-spec §6.5), F-06.
func TestCreateAgent_WithSkills(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Install the skills used by this test so validation passes. These MUST match
	// the skills requested in the POST body below (daily-briefing, summarize) —
	// the create-agent handler rejects any skill id not present under
	// OMNIPUS_BUILTIN_SKILLS with a 400 "unknown skill id".
	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	for _, id := range []string{"daily-briefing", "summarize"} {
		require.NoError(t, os.MkdirAll(skillsRoot+"/"+id, 0o755))
		require.NoError(
			t,
			os.WriteFile(skillsRoot+"/"+id+"/SKILL.md", []byte("---\nname: "+id+"\ndescription: d\n---\n"), 0o600),
		)
	}

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"name":"Skill Agent","type":"Main","soul":"s","skills":["daily-briefing","summarize"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code)

	var resp struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Skills []string `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "Skill Agent", resp.Name)
	assert.Equal(t, []string{"daily-briefing", "summarize"}, resp.Skills)

	// Verify the agent entity record persisted the skill list — createAgent
	// persists via the agent store, not config.json's agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get(resp.ID)
	require.NoError(t, err, "created agent must exist as a real entity-store record")
	require.Len(t, savedAgent.Skills, 2)
	assert.Equal(t, "daily-briefing", savedAgent.Skills[0])
	assert.Equal(t, "summarize", savedAgent.Skills[1])
}

// TestCreateAgent_NoSkills verifies that a new agent with no skills field has
// no skills in the response and none persisted to config.json (default none).
//
// BDD: Given a create-agent request without a skills field,
// When POST /api/v1/agents is called,
// Then the response has no skills field and config.json has no skills key.
//
// Traces to: US-E6 AC1 — new agent grants no skills until one is added.
func TestCreateAgent_NoSkills(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"name":"Skillless Agent","type":"Main","soul":"s"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code)

	// The response JSON must not contain a "skills" key (or it is null/absent).
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	skills, hasSkills := raw["skills"]
	// Either absent entirely, or explicitly null — both mean no skills.
	if hasSkills {
		assert.Nil(t, skills, "skills must be null or absent when not provided on create")
	}
	id, _ := raw["id"].(string)
	require.NotEmpty(t, id, "create response must include an id")

	// The agent entity record (entities/agents/<id>.json) must have no
	// skills — createAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get(id)
	require.NoError(t, err, "created agent must exist as a real entity-store record")
	assert.Empty(t, savedAgent.Skills, "a new agent with no skills field must have no skills persisted")
}

// TestCreateAgent_UnknownSkillIDRejected verifies that POST /api/v1/agents with a
// skill ID not in the installed registry is rejected with 400 when skills are installed.
//
// BDD: Given a gateway with one installed skill "web-research",
// When POST /api/v1/agents is called with skills=["unknown-skill"],
// Then the response is 400 Bad Request with "unknown skill id" in the error.
//
// Traces to: US-E6, MINOR (backend) — referential validation for skill IDs.
func TestCreateAgent_UnknownSkillIDRejected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Create a temp skills directory with one known skill "web-research".
	// OMNIPUS_BUILTIN_SKILLS env makes the agent loop pick it up on construction.
	skillsRoot := t.TempDir()
	t.Setenv("OMNIPUS_BUILTIN_SKILLS", skillsRoot)
	skillMD := skillsRoot + "/web-research/SKILL.md"
	require.NoError(t, os.MkdirAll(skillsRoot+"/web-research", 0o755))
	require.NoError(
		t,
		os.WriteFile(
			skillMD,
			[]byte("---\nname: web-research\ndescription: web search skill\n---\n# Web Research\n"),
			0o600,
		),
	)

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfgJSON := `{"agents":{"defaults":{"workspace":"` + tmpDir + `","model_name":"test-model","max_tokens":4096},"list":[]}}`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfgJSON), 0o600))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// "unknown-skill" is not installed — must be rejected 400.
	body := `{"name":"Test Agent","type":"Main","soul":"s","skills":["unknown-skill"]}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"unknown skill ID must be rejected with 400; body: %s",
		w.Body.String(),
	)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "unknown skill id", "error must name the unknown skill")

	// Known skill "web-research" must be accepted.
	body = `{"name":"Test Agent 2","type":"Main","soul":"s","skills":["web-research"]}`
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "known skill ID must be accepted; body: %s", w.Body.String())
}

// TestCreateAgent_IncompletePolicyMap_Rejected400 is UAT batch 3 S48 / batch 4
// S83. Pre-fix this returned 201: createAgent copied the caller's entries on top
// of coreagent.NewCustomAgentToolsCfg()'s fully-enumerated deny seed BEFORE
// config.ValidateToolPolicyCoverage ran, so the omitted key was silently
// backfilled to "deny" — a value nobody sent — and the coverage check was dead
// code on this path.
func TestCreateAgent_IncompletePolicyMap_Rejected400(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	policies := fullBuiltinPolicyMap("allow")
	delete(policies, "bash")
	body := fmt.Sprintf(`{"type":"Main","name":"GapAgent","soul":"s","tools_cfg":{"builtin":{"policies":%s}}}`,
		mustPolicyJSON(t, policies))

	w := postAgent(t, api, body)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a create whose policy map omits a static builtin tool must be rejected, not silently seeded; body=%s",
		w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg.builtin.policies",
		"the rejection must come from the CALLER-side submitted-map check, not the older "+
			"roster-wide coverage guard (which the seeded ceiling satisfies)")
	assert.Contains(t, w.Body.String(), "bash", "and it must name the omitted tool")
}

// TestCreateAgent_WildcardKey_Rejected400: the merge loop copied every caller
// key verbatim with no check that it named a real tool, so a literal "*" was
// stored inertly alongside the real entries and the create returned 201.
func TestCreateAgent_WildcardKey_Rejected400(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	policies := fullBuiltinPolicyMap("allow")
	policies["*"] = "allow"
	body := fmt.Sprintf(`{"type":"Main","name":"WildcardAgent","soul":"s","tools_cfg":{"builtin":{"policies":%s}}}`,
		mustPolicyJSON(t, policies))

	w := postAgent(t, api, body)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a wildcard key in a create body must be rejected; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg.builtin.policies",
		"the rejection must come from the caller-side submitted-map check")
	assert.Contains(t, w.Body.String(), "wildcards are not valid",
		"and it must explain that a wildcard key is not a policy entry for the static catalog")
}

// TestCreateAgent_CompletePolicyMap_StillAccepted / and a create with NO
// tools_cfg at all: the guard must fire only on a caller-supplied map. A request
// that omits tools_cfg legitimately falls through to the server-generated,
// complete, deny-seeded baseline — that is not a caller gap, and rejecting it
// would break every create the SPA makes for an inheriting subagent.
func TestCreateAgent_CompleteOrAbsentPolicyMap_StillAccepted(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)

	t.Run("complete map accepted", func(t *testing.T) {
		policies := fullBuiltinPolicyMap("allow")
		policies["bash"] = "deny"
		body := fmt.Sprintf(`{"type":"Main","name":"CompleteAgent","soul":"s","tools_cfg":{"builtin":{"policies":%s}}}`,
			mustPolicyJSON(t, policies))
		w := postAgent(t, api, body)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	})

	t.Run("absent tools_cfg accepted", func(t *testing.T) {
		w := postAgent(t, api, `{"type":"Main","name":"SeededAgent","soul":"s"}`)
		require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	})
}
