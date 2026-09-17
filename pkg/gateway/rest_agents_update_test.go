// rest_agents_update_test.go: tests for update an agent

package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from rest.go tests 2026-09-15 ---

// TestUpdateAgent_SoulChange_DoesNotTriggerFullReload is the update-path
// counterpart: a soul change (one of the two updateAgent field changes that
// used to force a full reload — the other being a default-agent-ID flip)
// must also complete via the fast path with zero reload-trigger invocations
// when no reload is already in flight.
func TestUpdateAgent_SoulChange_DoesNotTriggerFullReload(t *testing.T) {
	api := buildExecutorTestAPI(t)

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	body := `{"soul":"an updated soul, no cascade expected"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a plain soul update must not carry a warning: %+v", resp.Warning)

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a soul-only PUT /agents/{id} (no concurrent reload in flight) must NEVER invoke the reload "+
			"trigger — see TestCreateAgent_DoesNotTriggerFullReload for the full restartServices-cascade "+
			"rationale")

	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok, "the updated agent must remain resolvable via the fast path alone, with zero reloads")
	require.NotNil(t, inst)
}

// TestUpdateAgent_JudgeSoulEditable verifies PUT /api/v1/agents/judge with a
// soul field is accepted (200), persists to the Judge's SOUL.md via the
// existing soul-write path, and is echoed back correctly by both the PUT
// response and subsequent GET/list reads.
func TestUpdateAgent_JudgeSoulEditable(t *testing.T) {
	api := newSeededJudgeAPI(t)
	const newSoul = "You are the Judge. Custom verification standard: reject any claim lacking a passing test run."

	body := `{"soul":"` + newSoul + `"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/judge", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "judge")

	require.Equal(t, http.StatusOK, w.Code, "PUT soul on the Judge must be 200; body=%s", w.Body.String())

	var putResp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &putResp))
	assert.Equal(t, newSoul, putResp.Soul, "PUT response must echo the persisted soul, not blank it")
	assert.True(t, putResp.Locked, "the Judge must still report locked:true (identity stays locked)")
	assert.Equal(t, gen.AgentTypeSystem, putResp.Type, "the Judge must still report type:system")

	// Persistence: SOUL.md on disk under the Judge's workspace.
	workspace, wsErr := agentWorkspacePath(api.agentLoop.GetConfig(), "judge", "", api.homePath)
	require.NoError(t, wsErr)
	onDisk, readErr := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	require.NoError(t, readErr, "SOUL.md must exist after the PUT")
	assert.Equal(t, newSoul, string(onDisk), "SOUL.md on disk must contain the PUT-ed soul")

	// Read-back: GET /api/v1/agents/judge must also render the real soul (not
	// blanked) — a blank GET after a successful PUT would corrupt the next
	// edit's baseline.
	wGet := httptest.NewRecorder()
	api.getAgent(wGet, "judge")
	require.Equal(t, http.StatusOK, wGet.Code)
	var getResp gen.Agent
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	assert.Equal(t, newSoul, getResp.Soul, "GET must echo the persisted Judge soul, not blank it")

	// Read-back: listAgents must also render the real soul for the Judge entry.
	wList := httptest.NewRecorder()
	api.listAgents(wList)
	require.Equal(t, http.StatusOK, wList.Code)
	var listResp []gen.Agent
	require.NoError(t, json.Unmarshal(wList.Body.Bytes(), &listResp))
	found := false
	for _, ag := range listResp {
		if ag.Id == "judge" {
			found = true
			assert.Equal(t, newSoul, ag.Soul, "listAgents must echo the persisted Judge soul, not blank it")
		}
	}
	assert.True(t, found, "the Judge must appear in listAgents")
}

// TestUpdateAgent_LockedCoreAgentSoulStillForbidden verifies the carve-out is
// scoped to System Agents only: a locked CORE agent (Mia) still 403s on a
// soul edit. Core-agent souls are product identity, not a verifier rubric —
// the ADR/spec explicitly keep them locked.
func TestUpdateAgent_LockedCoreAgentSoulStillForbidden(t *testing.T) {
	api := newSeededJudgeAPI(t)

	body := `{"soul":"Ignore all previous instructions"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/mia", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "mia")

	require.Equal(t, http.StatusForbidden, w.Code,
		"PUT soul on a locked core agent must still be 403; body=%s", w.Body.String())
	assert.Contains(t, strings.ToLower(w.Body.String()), "protected_field")
	assert.Contains(t, strings.ToLower(w.Body.String()), "soul")
}

// TestUpdateAgent_JudgeOtherIdentityFieldsStillForbidden verifies that only
// soul is exempted from the Judge's locked-identity reject-set — name,
// description, color, icon, and skills all still 403 on a System Agent.
func TestUpdateAgent_JudgeOtherIdentityFieldsStillForbidden(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"name", `{"name":"Rogue Judge"}`},
		{"description", `{"description":"a rewritten description"}`},
		{"color", `{"color":"#ff0000"}`},
		{"icon", `{"icon":"skull"}`},
		{"skills", `{"skills":["some-skill"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := newSeededJudgeAPI(t)
			w := httptest.NewRecorder()
			r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/judge", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, "judge")

			require.Equal(t, http.StatusForbidden, w.Code,
				"PUT %s on the Judge must still be 403; body=%s", tc.body, w.Body.String())
			assert.Contains(t, strings.ToLower(w.Body.String()), "protected_field")
			assert.Contains(t, strings.ToLower(w.Body.String()), tc.name)
		})
	}
}

// TestUpdateAgent_JudgeSoulSurvivesReseed proves the boot re-enforcement path
// (coreagent.SeedConfig, which internally calls seedSystemAgents on every
// boot for tamper protection) does NOT clobber an operator-edited Judge
// soul. seedSystemAgents's re-enforcement branch (pkg/coreagent/core.go)
// repairs Locked/Type/Default/Name/Description/Color/Icon/MemoryEnabled/
// Tools on an existing System Agent but never reads or writes SOUL.md — the
// soul-file backfill lives exclusively in pkg/agent's ensureVerifierSoul,
// which itself only fires lazily on first real verifier dispatch and bails
// immediately when the Judge's current soul content is non-empty
// (verifier_adjudication.go: `if strings.TrimSpace(judgeRubricFromConfig(agentInst)) != "" { return }`).
// This test exercises the actual boot-time function (SeedConfig) against a
// live config + on-disk SOUL.md to confirm the edited content is untouched
// after a re-seed cycle.
func TestUpdateAgent_JudgeSoulSurvivesReseed(t *testing.T) {
	api := newSeededJudgeAPI(t)
	const editedSoul = "Operator-edited judging standard: require a green CI run before PASS."

	// Edit the Judge's soul via the normal write path.
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/judge", strings.NewReader(`{"soul":"`+editedSoul+`"}`))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "judge")
	require.Equal(t, http.StatusOK, w.Code, "PUT soul on the Judge must be 200; body=%s", w.Body.String())

	workspace, wsErr := agentWorkspacePath(api.agentLoop.GetConfig(), "judge", "", api.homePath)
	require.NoError(t, wsErr)
	soulPath := filepath.Join(workspace, "SOUL.md")
	before, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	require.Equal(t, editedSoul, string(before), "sanity: the edit must have persisted before the re-seed")

	// Simulate a boot re-seed cycle on the SAME live config — this is exactly
	// what pkg/coreagent.SeedConfig does on every gateway boot for tamper
	// protection / identity repair.
	cfg := api.agentLoop.GetConfig()
	coreagent.SeedConfig(cfg)

	after, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after the re-seed cycle")
	assert.Equal(t, editedSoul, string(after),
		"a re-seed cycle (coreagent.SeedConfig/seedSystemAgents) must NOT overwrite an operator-edited Judge soul")

	// The real boot sequence (gateway.go's RunContextWithOptions) also runs
	// seedSystemAgentEagerSouls immediately after coreagent.SeedConfig on EVERY
	// boot, not just the first — it must respect the same backfill-only
	// rule as the config-mutation re-seed above, or every subsequent
	// restart would clobber an operator's edited Judge soul back to the
	// compiled default.
	seedSystemAgentEagerSouls(cfg)
	afterEager, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after the eager boot-time re-seed")
	assert.Equal(t, editedSoul, string(afterEager),
		"seedSystemAgentEagerSouls must NOT overwrite an operator-edited Judge soul on a restart either")

	// Confirm re-enforcement still ran (Locked/Type/etc. repaired if needed)
	// without touching soul — GET must still reflect the edited content.
	wGet := httptest.NewRecorder()
	api.getAgent(wGet, "judge")
	require.Equal(t, http.StatusOK, wGet.Code)
	var getResp gen.Agent
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &getResp))
	assert.Equal(t, editedSoul, getResp.Soul, "GET after re-seed must still reflect the operator-edited soul")
	assert.True(t, getResp.Locked, "the Judge must still be locked after re-seed")
}

// TestUpdateAgent_ContextWindowOverride_IsPersistedAndEchoed is the DoD test:
// it fails before the fix (the field is dropped and the response carries none
// of the four window fields) and passes after it.
func TestUpdateAgent_ContextWindowOverride_IsPersistedAndEchoed(t *testing.T) {
	api := newContextWindowAgentAPI(t)

	w := putAgentJSON(t, api, "agent-a", `{"context_window_override":32768}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var resp gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotNil(t, resp.ContextWindowOverride,
		"the PUT response must echo the override it just persisted (the form reads it back)")
	assert.Equal(t, 32768, *resp.ContextWindowOverride)
	require.NotNil(t, resp.ContextWindowEffective,
		"context_window_effective must be derived from ResolveWindow on every response")
	assert.Equal(t, 32768, *resp.ContextWindowEffective)
	require.NotNil(t, resp.ContextWindowSource)
	assert.Equal(t, gen.AgentContextWindowSourceOperator, *resp.ContextWindowSource)
	require.NotNil(t, resp.ContextWindowClamped)
	assert.False(t, *resp.ContextWindowClamped)

	// It actually reached AgentConfig — ResolveWindow's rung 1 reads this and
	// nothing else.
	live := api.agentLoop.GetConfig()
	require.NotNil(t, live)
	var stored *int
	for i := range live.Agents.List {
		if live.Agents.List[i].ID == "agent-a" {
			stored = live.Agents.List[i].ContextWindowOverride
		}
	}
	require.NotNil(t, stored, "AgentConfig.ContextWindowOverride must be persisted, not silently dropped")
	assert.Equal(t, 32768, *stored)

	// A subsequent GET round-trips the same four fields.
	g := httptest.NewRecorder()
	api.getAgent(g, "agent-a")
	require.Equal(t, http.StatusOK, g.Code, "body: %s", g.Body.String())
	var got gen.Agent
	require.NoError(t, json.Unmarshal(g.Body.Bytes(), &got))
	require.NotNil(t, got.ContextWindowOverride)
	assert.Equal(t, 32768, *got.ContextWindowOverride)
	require.NotNil(t, got.ContextWindowEffective)
	assert.Equal(t, 32768, *got.ContextWindowEffective)
	require.NotNil(t, got.ContextWindowSource)
	assert.Equal(t, gen.AgentContextWindowSourceOperator, *got.ContextWindowSource)
}

// TestUpdateAgent_ContextWindowOverride_NullClears covers "send null to clear"
// and the "absent leaves unchanged" half of the same contract sentence.
func TestUpdateAgent_ContextWindowOverride_NullClears(t *testing.T) {
	api := newContextWindowAgentAPI(t)

	require.Equal(t, http.StatusOK,
		putAgentJSON(t, api, "agent-a", `{"context_window_override":32768}`).Code)

	// An unrelated write must NOT clear it.
	keep := putAgentJSON(t, api, "agent-a", `{"max_tool_iterations":25}`)
	require.Equal(t, http.StatusOK, keep.Code, "body: %s", keep.Body.String())
	var kept gen.Agent
	require.NoError(t, json.Unmarshal(keep.Body.Bytes(), &kept))
	require.NotNil(t, kept.ContextWindowOverride, "an omitted field must leave the override untouched")
	assert.Equal(t, 32768, *kept.ContextWindowOverride)

	// Explicit null clears it, and the effective window falls back down the
	// ladder (no override → the cloud floor for an unsized cloud row).
	cleared := putAgentJSON(t, api, "agent-a", `{"context_window_override":null}`)
	require.Equal(t, http.StatusOK, cleared.Code, "body: %s", cleared.Body.String())
	var out gen.Agent
	require.NoError(t, json.Unmarshal(cleared.Body.Bytes(), &out))
	assert.Nil(t, out.ContextWindowOverride, "an explicit null must clear the override")
	require.NotNil(t, out.ContextWindowSource)
	assert.NotEqual(t, gen.AgentContextWindowSourceOperator, *out.ContextWindowSource,
		"with the override cleared the window must come from a lower rung")

	live := api.agentLoop.GetConfig()
	for i := range live.Agents.List {
		if live.Agents.List[i].ID == "agent-a" {
			assert.Nil(t, live.Agents.List[i].ContextWindowOverride)
		}
	}
}

// TestUpdateAgent_ContextWindowOverride_RejectsNonPositive — the contract's
// `minimum: 1`.
func TestUpdateAgent_ContextWindowOverride_RejectsNonPositive(t *testing.T) {
	api := newContextWindowAgentAPI(t)
	w := putAgentJSON(t, api, "agent-a", `{"context_window_override":0}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "context_window_override")
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
	pr1 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker", strings.NewReader(put1))
	pr1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(pw1, pr1)
	require.Equal(t, http.StatusOK, pw1.Code)

	// Attempt to change the cli to opencode — must be rejected 400 by the
	// cli-lock rule.
	put2 := `{"executor":{"kind":"external-cli","cli":"opencode"}}`
	pw2 := httptest.NewRecorder()
	pr2 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker", strings.NewReader(put2))
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

// TestUpdateAgent_NonWorkerRejectsExternalCLIExecutor verifies the
// native-only-for-non-workers rule at the update gate. A non-worker updated
// to kind=external-cli must be rejected with 400; a non-worker updated to
// kind=native (or omitting the field) stays allowed.
func TestUpdateAgent_NonWorkerRejectsExternalCLIExecutor(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// External-cli on the test-agent (a custom non-worker) → 400.
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"executor":{"kind":"external-cli","cli":"codex"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "only sub-agent workers",
		"the rejection must reference the worker-only rule")

	// Remote-a2a on the test-agent → 400.
	w = httptest.NewRecorder()
	r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker",
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker",
		strings.NewReader(`{"executor":{"kind":"remote-a2a"}}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	exec := agentExecutorFromStore(t, api.homePath, "test-worker")
	require.NotNil(t, exec, "worker remote-a2a executor must be persisted")
	assert.Equal(t, config.ExecutorKindRemoteA2A, exec.Kind)
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker",
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-worker",
		strings.NewReader(`{"voice":null}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
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
		r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(validPatch))
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
		r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(validPatch))
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id,
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
			r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(tc.body))
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "OMNIPUS_* is gateway-internal")
}

// TestUpdateAgent_UpdatedAtRejected verifies FR-007's single concurrency
// precondition: updated_at is display metadata and is rejected even when it
// happens to equal the stored value. Revision is the only write precondition.
func TestUpdateAgent_UpdatedAtRejected(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// 1. Establish a persisted updated_at via a successful PUT (config-only
	//    field change so no reload/model-apply side effects fire). This is the
	//    current display-only value used by the presence-rejection cases.
	w1 := httptest.NewRecorder()
	r1 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"color":"#FF0000"}`))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "establishing PUT body: %s", w1.Body.String())
	currentUpdatedAt := agentUpdatedAtFromConfig(t, api, "test-agent")
	require.NotEmpty(t, currentUpdatedAt,
		"updated_at must be persisted after a successful PUT")

	beforeRejectedWrites, err := agentstore.New(api.homePath).ReadState("test-agent")
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "stale timestamp", body: fmt.Sprintf(`{"color":"#00FF00","updated_at":%q}`, "2000-01-01T00:00:00Z")},
		{name: "current timestamp", body: fmt.Sprintf(`{"color":"#0000FF","updated_at":%q}`, currentUpdatedAt)},
		{name: "null", body: `{"color":"#00FFFF","updated_at":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			api.HandleAgents(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code,
				"updated_at presence must be rejected in favor of revision; body: %s", w.Body.String())
			var errResp gen.ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
			require.NotNil(t, errResp.Code)
			assert.Equal(t, "invalid_input", *errResp.Code)
			after, readErr := agentstore.New(api.homePath).ReadState("test-agent")
			require.NoError(t, readErr)
			assert.Equal(t, beforeRejectedWrites.Revision, after.Revision, "rejection must be zero-write")
			assert.Equal(t, currentUpdatedAt, agentUpdatedAtFromConfig(t, api, "test-agent"))
		})
	}
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
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
	r := httptest.NewRequest(http.MethodPut, "/api/v1/agents/test-agent", strings.NewReader(`{"revision":"`+strings.Repeat("0", 64)+`","color":"#123456"}`))
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
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

// TestUpdateAgent_ExecutorCliPathMutable verifies that cli_path IS mutable on
// PUT for a subagent_3p agent (binary upgrade without re-creating the agent).
// Spec §9.2: "PUT executor.cli_path: /new/path → 200".
func TestUpdateAgent_ExecutorCliPathMutable(t *testing.T) {
	api := buildExecutorTestAPI(t)
	id := createSubagent3p(t, api) // cli_path: /usr/local/bin/codex

	body := `{"executor":{"cli_path":"/opt/codex-v2"}}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	exec := agentExecutorFromStore(t, api.homePath, id)
	require.NotNil(t, exec)
	assert.Equal(t, "/opt/codex-v2", exec.CLIPath,
		"cli_path must be updated to the new value")
}

// TestUpdateAgent_FallbackModels_PersistAndEchoOnPUT proves the PUT response
// itself now echoes fallback_models (previously always omitted — updateAgent's
// response-building tail never called applyAgentOverrides at all).
func TestUpdateAgent_FallbackModels_PersistAndEchoOnPUT(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"fallback_models":[{"model":"claude-sonnet-4.6","provider":"anthropic"},` +
		`{"model":"gpt-4o-mini","provider":"openai"}]}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	updated := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, updated.FallbackModels,
		"PUT response must echo fallback_models — was always omitted pre-fix")
	require.Len(t, *updated.FallbackModels, 2)
	assert.Equal(t, "claude-sonnet-4.6", (*updated.FallbackModels)[0].Model)
	require.NotNil(t, (*updated.FallbackModels)[0].Provider)
	assert.Equal(t, "anthropic", *(*updated.FallbackModels)[0].Provider)
	assert.Equal(t, "gpt-4o-mini", (*updated.FallbackModels)[1].Model)
	require.NotNil(t, (*updated.FallbackModels)[1].Provider)
	assert.Equal(t, "openai", *(*updated.FallbackModels)[1].Provider)

	// Persisted to the entity store (ADR-054 + the config.AgentsConfig.List
	// = `json:"-"` follow-up: agents are per-entity records now, and
	// agents.list can never be marshaled into config.json by any code path —
	// createAgent/updateAgent persist exclusively via agentstore.Store. This
	// is the save-race fix this test wave builds on, re-pointed at its real
	// persistence target.)
	rec, err := agentstore.New(api.homePath).Get("test-agent")
	require.NoError(t, err, "test-agent must be persisted")
	require.Len(t, rec.FallbackModels, 2, "fallback_models must be persisted to the entity store")
	assert.Equal(t, "claude-sonnet-4.6", rec.FallbackModels[0].Model)
	assert.Equal(t, "anthropic", rec.FallbackModels[0].Provider)
	assert.Equal(t, "gpt-4o-mini", rec.FallbackModels[1].Model)
	assert.Equal(t, "openai", rec.FallbackModels[1].Provider)
}

// TestUpdateAgent_UnresolvableModel_IsAppliedToRunningAgent is the E-7 defect
// itself: a model no configured provider offers used to be saved while the
// running agent silently kept its previous model. It must now be saved and
// applied, so the saved record and the running agent agree, and the response
// must say the model cannot run rather than carry a "saved but not applied"
// warning.
func TestUpdateAgent_UnresolvableModel_IsAppliedToRunningAgent(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)

	const model = "anthropic/omnipus-nonexistent-model-zzz"
	require.NotEqual(t, model, liveAgent(t, api, "test-agent").Model,
		"precondition: the running agent must start on a different model")

	w := putAgentJSON(t, api, "test-agent", `{"model":"`+model+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())

	assert.Equal(t, model, liveAgent(t, api, "test-agent").Model,
		"the running agent must serve the model that was just saved, not keep its previous one")
	assert.Equal(t, model, storedPrimaryModel(t, api, "test-agent"),
		"the saved record must hold the same model the running agent serves")
	assert.Nil(t, resp.Warning,
		"a change that was applied must not be reported as saved-but-not-applied; body=%s", w.Body.String())
	assert.True(t, resp.NeedsModel,
		"ADR-068 FR-014: a model whose provider is not configured must be flagged needs_model; body=%s", w.Body.String())
}

// TestUpdateAgent_UnknownProvider_RunningAgentMovesToIt reproduces the UAT E-7
// tester's action: point an agent's model and provider at values that do not
// exist. ADR-067 US-6 keeps such an agent saved and degraded (it refuses turns
// with needs_provider) rather than rejecting the edit, so the running agent
// must route through the new provider, and the response must show the degrade.
func TestUpdateAgent_UnknownProvider_RunningAgentMovesToIt(t *testing.T) {
	api := buildExecutorTestAPI(t)
	installFixtureCatalog(t, api)

	const model, provider = "nonexistent-vendor/no-such-model-e7", "no-such-provider-e7"
	w := putAgentJSON(t, api, "test-agent", `{"model":"`+model+`","provider":"`+provider+`"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())

	live := liveAgent(t, api, "test-agent")
	assert.Equal(t, model, live.Model, "the running agent must serve the saved model")
	require.NotEmpty(t, live.Candidates, "the running agent must have a primary candidate")
	assert.Equal(t, providers.FallbackCandidate{Provider: provider, Model: model}, live.Candidates[0],
		"a pinned provider routes the primary candidate directly (O3), so the running agent must route to the saved provider")
	require.NotNil(t, resp.DegradedReason,
		"an agent bound to an unknown provider must say so in the same response; body=%s", w.Body.String())
	assert.Equal(t, gen.AgentDegradedReasonNeedsProvider, *resp.DegradedReason)
}

// TestUpdateAgent_ModelChange_AppliedLiveWithoutFullReload covers the working
// case and the two changes the old in-place path never applied: a model and
// provider change, then a fallback-only change. Each must reach the running
// agent through the single-agent rebuild, never through the full reload that
// restarts channels and drops the WebSocket (#73, #571).
func TestUpdateAgent_ModelChange_AppliedLiveWithoutFullReload(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)
	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	w := putAgentJSON(t, api, "test-agent", `{"model":"gpt-4o","provider":"openai"}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())

	live := liveAgent(t, api, "test-agent")
	assert.Equal(t, "gpt-4o", live.Model)
	require.NotEmpty(t, live.Candidates)
	assert.Equal(t, providers.FallbackCandidate{Provider: "openai", Model: "gpt-4o"}, live.Candidates[0])
	assert.Empty(t, live.FallbackModels, "no fallback was saved")
	assert.False(t, resp.NeedsModel, "gpt-4o on the configured openai provider can run; body=%s", w.Body.String())
	assert.Nil(t, resp.Warning, "body=%s", w.Body.String())

	w = putAgentJSON(t, api, "test-agent", `{"fallback_models":[{"model":"gpt-4o-mini","provider":"openai"}]}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, []config.FallbackModel{{Model: "gpt-4o-mini", Provider: "openai"}},
		liveAgent(t, api, "test-agent").FallbackModels,
		"a fallback-only change must reach the running agent")

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a model change must rebuild only this agent, never trigger the full reload")
}

// TestUpdateAgent_UnchangedModelResent_DoesNotRebuildAgent: AgentProfile's
// autosave resends model, provider and fallback_models on every save. Only a
// real change may rebuild the running agent.
func TestUpdateAgent_UnchangedModelResent_DoesNotRebuildAgent(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)
	const body = `{"model":"gpt-4o","provider":"openai","fallback_models":[{"model":"gpt-4o-mini","provider":"openai"}]}`

	w := putAgentJSON(t, api, "test-agent", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	rebuilt := liveAgent(t, api, "test-agent")
	require.Equal(t, "gpt-4o", rebuilt.Model, "precondition: the first save must apply the model")

	w = putAgentJSON(t, api, "test-agent", body)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Same(t, rebuilt, liveAgent(t, api, "test-agent"),
		"resending the saved model unchanged must not rebuild the running agent")
}

// TestUpdateAgent_RebuildFailure_IsNotReportedAsSuccess: when the rebuilt agent
// cannot be published at all (the single-agent swap and its full-reload
// fallback both fail), the running agent still serves the previous model. A
// 200 would repeat the E-7 defect, so the request must fail and say why.
func TestUpdateAgent_RebuildFailure_IsNotReportedAsSuccess(t *testing.T) {
	api := buildExecutorTestAPI(t)
	pinOnlyOpenAIProvider(t, api)
	previous := liveAgent(t, api, "test-agent").Model
	api.testForceFastUpsertErr = errors.New("fast upsert boom")
	api.agentLoop.SetReloadFunc(func() error { return errors.New("reload boom") })

	w := putAgentJSON(t, api, "test-agent", `{"model":"gpt-4o","provider":"openai"}`)

	assert.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	require.NotNil(t, resp.ActivationStatus)
	assert.Equal(t, gen.AgentActivationStatusFailed, *resp.ActivationStatus)
	require.NotNil(t, resp.PersistenceStatus)
	assert.Equal(t, gen.AgentPersistenceStatusComplete, *resp.PersistenceStatus)
	assert.Contains(t, w.Body.String(), "saved but activation failed")
	assert.Contains(t, w.Body.String(), "reload boom", "the error must carry the underlying cause")
	assert.Equal(t, previous, liveAgent(t, api, "test-agent").Model,
		"the running agent really is still on the previous model, which is why this cannot be a success")
}

// TestUpdateAgent_ModelParams_PersistAndEcho reproduces the reported defect
// (PUT {"model_params":{"max_tokens":48}} -> 200, entity record unchanged,
// GET echoes model_params: null) and asserts the fixed round trip: the PUT
// response echoes the value, the entity record on disk carries it, and a
// SEPARATE GET (independent of the PUT response) echoes it too.
//
// Mutation check: reverting the `if req.ModelParams != nil { ... }` persist
// block in updateAgent (pkg/gateway/rest.go) makes this fail at the
// "persisted entity record" and "GET echo" assertions (PUT still returns
// 200 — that is exactly the silent-drop bug).
func TestUpdateAgent_ModelParams_PersistAndEcho(t *testing.T) {
	api, _, tmpDir := newModelParamsTestAPI(t)

	w, putResp := putAgentModelParams(t, api, "agent-a",
		`{"model_params":{"max_tokens":48,"temperature":0.25}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	require.NotNil(t, putResp.ModelParams, "PUT response must echo model_params, not null")
	require.NotNil(t, putResp.ModelParams.MaxTokens)
	assert.Equal(t, 48, *putResp.ModelParams.MaxTokens, "PUT response must echo the persisted max_tokens")
	require.NotNil(t, putResp.ModelParams.Temperature)
	assert.InDelta(t, 0.25, *putResp.ModelParams.Temperature, 0.0001, "PUT response must echo the persisted temperature")

	// Persisted entity record on disk (agentstore.Store, not config.json —
	// ADR-054 D2) must carry the same values, independent of what the PUT
	// handler's in-memory response happened to construct.
	store := agentstore.New(tmpDir)
	rec, err := store.Get("agent-a")
	require.NoError(t, err)
	require.NotNil(t, rec.ModelParams, "persisted entity record must carry model_params")
	require.NotNil(t, rec.ModelParams.MaxTokens)
	assert.Equal(t, 48, *rec.ModelParams.MaxTokens)
	require.NotNil(t, rec.ModelParams.Temperature)
	assert.InDelta(t, 0.25, *rec.ModelParams.Temperature, 0.0001)

	// A completely separate GET must echo the same values — this is the
	// half of the bug the repro in the task description called out
	// explicitly ("GET returns model_params: null").
	wGet, getResp := getAgent(t, api, "agent-a")
	require.Equal(t, http.StatusOK, wGet.Code)
	require.NotNil(t, getResp.ModelParams, "GET must echo model_params, not null")
	require.NotNil(t, getResp.ModelParams.MaxTokens)
	assert.Equal(t, 48, *getResp.ModelParams.MaxTokens)
	require.NotNil(t, getResp.ModelParams.Temperature)
	assert.InDelta(t, 0.25, *getResp.ModelParams.Temperature, 0.0001)
}

// TestUpdateAgent_ModelParams_PersistAndEcho_PartialPatch verifies the
// field-level merge semantics documented at the persist site: a PUT that
// sends only max_tokens must not clobber a temperature set by an earlier
// PUT (mirrors the existing ShellPolicy partial-patch behavior in the same
// handler).
func TestUpdateAgent_ModelParams_PersistAndEcho_PartialPatch(t *testing.T) {
	api, _, _ := newModelParamsTestAPI(t)

	w1, _ := putAgentModelParams(t, api, "agent-a", `{"model_params":{"temperature":0.9}}`)
	require.Equal(t, http.StatusOK, w1.Code)

	w2, resp2 := putAgentModelParams(t, api, "agent-a", `{"model_params":{"max_tokens":777}}`)
	require.Equal(t, http.StatusOK, w2.Code)

	require.NotNil(t, resp2.ModelParams)
	require.NotNil(t, resp2.ModelParams.MaxTokens)
	assert.Equal(t, 777, *resp2.ModelParams.MaxTokens)
	require.NotNil(t, resp2.ModelParams.Temperature, "a partial patch (max_tokens only) must not clobber the previously-set temperature")
	assert.InDelta(t, 0.9, *resp2.ModelParams.Temperature, 0.0001)
}

// TestUpdateAgent_ModelParams_AppliedToEffectiveParams is the DoD test for
// the "applied on the next turn" half of Q1: persistence alone is not
// enough (that would just move the ADR-037 anti-pattern from "not
// persisted" to "persisted but ignored"). It follows
// rest_default_agent_singleton_test.go's precedent exactly: this
// lightweight harness never wires a real reload function, so after the PUT
// it reads a.agentLoop.GetConfig() (already reflects the persisted write —
// updateConfigJSONLocked's refreshConfigAndRewireServices reloads
// config.json and swaps it in) and builds a FRESH agent.AgentRegistry from
// it — the same call (agent.NewAgentRegistry -> pkg/agent/instance.go's
// NewAgentInstance) a real reload/boot or updateAgent's own fastAgentUpsert
// path would make. The resulting AgentInstance's MaxTokens/Temperature are
// exactly what pkg/agent/loop.go's turn-time Chat call reads (ts.agent.MaxTokens/
// ts.agent.Temperature) — asserting on them here proves the override reaches
// the seam the next turn's provider call is built from, without needing to
// drive pkg/agent/loop.go itself (which is owned by a concurrent change in
// this working tree).
//
// Mutation check: reverting the agentCfg.ModelParams override read in
// pkg/agent/instance.go's NewAgentInstance (the maxTokens/temperature ladder
// right after the maxIter block) makes this fail — the resolved instance
// falls back to agents.defaults (4096 / 0.7) instead of the per-agent
// override.
func TestUpdateAgent_ModelParams_AppliedToEffectiveParams(t *testing.T) {
	api, _, _ := newModelParamsTestAPI(t)

	w, _ := putAgentModelParams(t, api, "agent-a",
		`{"model_params":{"max_tokens":321,"temperature":0.05}}`)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	freshCfg := api.agentLoop.GetConfig()
	require.NotNil(t, freshCfg)

	provider := &restMockProvider{}
	freshRegistry := agent.NewAgentRegistry(freshCfg, provider)
	t.Cleanup(freshRegistry.Close)

	inst, ok := freshRegistry.GetAgent("agent-a")
	require.True(t, ok, "agent-a must resolve in a freshly-built registry")
	require.NotNil(t, inst)

	assert.Equal(t, 321, inst.MaxTokens,
		"a freshly-constructed AgentInstance must honor the per-agent model_params.max_tokens override, "+
			"not silently fall back to agents.defaults.max_tokens (4096)")
	assert.InDelta(t, 0.05, inst.Temperature, 0.0001,
		"a freshly-constructed AgentInstance must honor the per-agent model_params.temperature override, "+
			"not silently fall back to agents.defaults.temperature (0.7)")
}

// TestUpdateAgent_ModelParams_TopPRejected asserts the top_p carve-out: the
// wire schema (AgentUpdateRequest.yaml) carries model_params.top_p, but no
// provider adapter in this codebase implements nucleus sampling and there is
// no agents.defaults equivalent to fall back to either. Persisting it
// anyway (200, silently never honored on any turn) would be exactly the
// ADR-037 anti-pattern this fix exists to close, just moved one layer down.
// The handler must refuse it outright instead.
func TestUpdateAgent_ModelParams_TopPRejected(t *testing.T) {
	api, _, tmpDir := newModelParamsTestAPI(t)

	w, _ := putAgentModelParams(t, api, "agent-a", `{"model_params":{"top_p":0.9}}`)
	require.Equal(t, http.StatusBadRequest, w.Code, "body: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "top_p")

	// Nothing must have been written — a rejected request must not
	// half-persist a sibling field it was never asked to touch.
	store := agentstore.New(tmpDir)
	rec, err := store.Get("agent-a")
	require.NoError(t, err)
	assert.Nil(t, rec.ModelParams, "a rejected top_p request must not persist anything")
}

// TestUpdateAgent_SoulChange_RegistryReloadCompletesBeforeResponse proves the
// same "handler returns before the async reload actually lands" defect on the
// PUT /api/v1/agents/{id} Soul-change path (gen.AgentUpdateRequest.Soul:
// "Writing this triggers a config reload."). IsReloadPending() is the direct
// observable of the SAME registry-swap mechanism createAgent races on: if the
// handler returns while a reload is still pending, any other in-flight
// request depending on the reloaded registry (or on updateAgent's own
// post-reload reads of a.agentLoop.GetConfig(), see rest.go) can still
// observe stale state.
func TestUpdateAgent_SoulChange_RegistryReloadCompletesBeforeResponse(t *testing.T) {
	api := buildExecutorTestAPI(t)
	wireAsyncReload(t, api, 30*time.Millisecond)

	newSoul := "updated soul content"
	body, err := json.Marshal(gen.AgentUpdateRequest{Soul: &newSoul})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")

	require.Equal(t, http.StatusOK, w.Code, "agent update failed: %s", w.Body.String())
	assert.False(t, api.agentLoop.IsReloadPending(),
		"updateAgent (Soul change) returned 200 while a config reload was still pending — "+
			"callers relying on the registry immediately after this response can still see "+
			"pre-update state")
}

// TestUpdateAgent_RejectsReservedDefaultName verifies updateAgent 400s on a
// name of "default", case-insensitively, for an existing agent.
func TestUpdateAgent_RejectsReservedDefaultName(t *testing.T) {
	for _, name := range []string{"default", "Default", "DEFAULT"} {
		t.Run("name="+name, func(t *testing.T) {
			api := buildExecutorTestAPI(t)

			body := `{"name":"` + name + `"}`
			w := httptest.NewRecorder()
			r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			api.updateAgent(w, r, "test-agent")

			require.Equal(t, http.StatusBadRequest, w.Code,
				"update to reserved name %q must be rejected 400, got body: %s", name, w.Body.String())
			assert.Contains(t, w.Body.String(), "reserved",
				"error body must explain the name is reserved")
		})
	}
}

// TestUpdateAgent_AllowsOrdinaryNameChange is the negative control — an
// ordinary name update must still succeed, proving the reserved-name check
// does not over-fire.
func TestUpdateAgent_AllowsOrdinaryNameChange(t *testing.T) {
	api := buildExecutorTestAPI(t)

	body := `{"name":"Renamed Agent"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.updateAgent(w, r, "test-agent")

	require.Equal(t, http.StatusOK, w.Code, "ordinary rename must succeed, got body: %s", w.Body.String())
	updated := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, "Renamed Agent", updated.Name)
}

// TestUpdateAgent_SandboxProfileOff_Rejected400 verifies that a PUT carrying
// the retired sandbox_profile="off" field is rejected with 400.
func TestUpdateAgent_SandboxProfileOff_Rejected400(t *testing.T) {
	runUpdateAgentSandboxProfileRejected400(t, "off")
}

// TestUpdateAgent_SandboxProfileWorkspace_Rejected400 verifies the same
// rejection for a different (also-retired) sandbox_profile value, confirming
// the guard is not accidentally scoped to just "off".
func TestUpdateAgent_SandboxProfileWorkspace_Rejected400(t *testing.T) {
	runUpdateAgentSandboxProfileRejected400(t, "workspace")
}

// TestUpdateAgent_NoSandboxProfile_StillSucceeds is a negative control:
// confirms the raw-body sniff does not false-positive on ordinary field
// names (e.g. a field whose name merely contains "sandbox" as a substring is
// not the trigger — only the literal "sandbox_profile" key is).
func TestUpdateAgent_NoSandboxProfile_StillSucceeds(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode */)

	body := `{"name":"Renamed Agent"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	assert.Equal(t, http.StatusOK, w.Code,
		"an ordinary update with no sandbox_profile key must still succeed; body: %s", w.Body.String())
}

// TestUpdateAgent_DoesNotChangeType proves Type is create-only. The
// AgentUpdateRequest contract has no `type` field, so a PUT cannot
// convert a custom into a worker or vice versa. The on-disk type is
// stable across updates.
func TestUpdateAgent_DoesNotChangeType(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a custom agent.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agents",
		strings.NewReader(`{"name":"Sticky Custom","type":"Main","soul":"sticky-custom-soul"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())

	// PUT an unrelated field. The on-disk type must stay "custom".
	w = httptest.NewRecorder()
	r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"soul":"sticky-custom-soul","description":"updated description"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())

	entry := findTypeTestAgentInStore(t, api.homePath, "Sticky Custom")
	require.NotNil(t, entry)
	assert.Equal(t, config.AgentTypeCustom, entry.Type,
		"update must not change Type — it is create-only")
}

// TestUpdateAgent_DoesNotChangeTypeOnWorker mirrors the above for a
// worker: PUT cannot change a worker into a custom (or vice versa).
func TestUpdateAgent_DoesNotChangeTypeOnWorker(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Create a worker.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/agents",
		strings.NewReader(
			`{"name":"Sticky Worker","type":"Subagent","description":"sticky worker regression","soul":"sticky-worker-soul"}`,
		),
	)
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusCreated, w.Code, "create body: %s", w.Body.String())
	created := decodeAgentResp(t, w.Body.Bytes())
	assert.Equal(t, gen.AgentTypeSubagent, created.Type)

	// PUT an unrelated field.
	w = httptest.NewRecorder()
	r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+created.Id,
		strings.NewReader(`{"soul":"sticky-worker-soul","description":"updated description"}`))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "put body: %s", w.Body.String())

	entry := findTypeTestAgentInStore(t, api.homePath, "Sticky Worker")
	require.NotNil(t, entry)
	assert.Equal(t, config.AgentTypeWorker, entry.Type,
		"update must not change Type on a worker either")
}

// TestUpdateAgent_ShellPolicy_InvalidRegex_Returns400 verifies that a
// shell_policy.custom_deny_patterns entry with an invalid regexp is rejected
// with 400 and the error message includes the bad pattern.
func TestUpdateAgent_ShellPolicy_InvalidRegex_Returns400(t *testing.T) {
	api := buildGodModeTestAPI(t, false /* allowGodMode — not relevant for this check */)

	body := `{"shell_policy":{"enable_deny_patterns":true,"custom_deny_patterns":["[invalid-regexp"]}}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
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
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
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
	r1 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body1))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "first PATCH must succeed; body: %s", w1.Body.String())

	// Second PATCH: send only custom_deny_patterns (no enable_deny_patterns key).
	body2 := `{"shell_policy":{"custom_deny_patterns":["curl\\s+evil"]}}`
	w2 := httptest.NewRecorder()
	r2 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body2))
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
	r1 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body1))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "seed PATCH must succeed; body: %s", w1.Body.String())

	// Clear with an explicit empty array.
	body2 := `{"shell_policy":{"custom_deny_patterns":[]}}`
	w2 := httptest.NewRecorder()
	r2 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body2))
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

// TestUpdateAgent_DefaultToggle_RegistryAndRoutingAgree is the DoD test for the
// ★ default-agent release blocker: it FAILS before the fix (registry and
// routing disagree, as described above) and PASSES after it (both ladders
// resolve the just-starred agent).
//
// BDD: Given two ordinary (non-worker) agents and NO configured default
// (agents.defaults.default_agent_id is empty — the exact precondition every
// fresh/existing install has before this fix, since nothing ever wrote it),
//
//	When PUT /api/v1/agents/agent-b with {"default": true} (the ★ toggle),
//	Then the settings singleton (agents.defaults.default_agent_id) is
//	persisted as "agent-b" — the write half of the bug — and a fresh registry
//	built from that persisted config resolves BOTH
//	pkg/agent.AgentRegistry.GetDefaultAgent() (the webchat ladder) AND
//	pkg/agent.AgentRegistry.ResolveRoute()'s fallback tier (the channel-routing
//	ladder, mirroring pkg/routing.RouteResolver.resolveDefaultAgentID) to the
//	SAME agent — agent-b.
func TestUpdateAgent_DefaultToggle_RegistryAndRoutingAgree(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
				// Deliberately no DefaultAgentID: this is the exact
				// precondition of the bug — no operator has ever starred an
				// agent, and (pre-fix) coreagent.SeedConfig never wrote this
				// either, so it is empty on every affected install.
			},
			// List order is deliberately "agent-b" THEN "agent-a" — the
			// opposite of lexicographic order. The retired "main" sentinel
			// used to be GetDefaultAgent's own Priority-2 fallback (disagreeing
			// with resolveDefaultAgentID's first-in-list-order fallback on
			// EVERY install, regardless of naming); with the sentinel gone,
			// GetDefaultAgent's Priority 2 is now "lexicographically first
			// registered non-worker agent" while resolveDefaultAgentID's is
			// still "first chat-target agent in cfg.Agents.List order" (see
			// both doc comments — pkg/agent/registry.go::GetDefaultAgent and
			// pkg/routing/route.go::resolveDefaultAgentID). The two orderings
			// only disagree when list order isn't already lexicographic, so
			// this fixture orders the list backwards on purpose to keep
			// reproducing the "the two ladders can disagree with no configured
			// override" precondition this test exists to guard.
			List: []config.AgentConfig{
				{ID: "agent-b", Name: "Agent B"},
				{ID: "agent-a", Name: "Agent A"},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	provider := &restMockProvider{}
	al := mustAgentLoop(t, cfg, msgBus, provider)
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	// The precondition INVERTED, deliberately.
	//
	// This block used to assert that the two ladders DISAGREE before the ★
	// toggle — that was the release blocker's exact claim, and the fixture
	// reverses cfg.Agents.List specifically to reproduce it: GetDefaultAgent
	// took the lexicographically-first agent while ResolveRoute took the first
	// in slice order.
	//
	// That divergence is now FIXED (ADR-064 §7): both ladders apply the same
	// eligibility rule and both sort, so they agree with or without an
	// override. Asserting they still disagree would pin a bug as if it were a
	// requirement, so this asserts the property that replaced it.
	//
	// The test's actual subject is unchanged and follows below: setting the
	// default via ★ must move BOTH ladders to the chosen agent.
	preRegistry := agent.NewAgentRegistry(al.GetConfig(), provider)
	preDefault := preRegistry.GetDefaultAgent()
	require.NotNil(t, preDefault)
	preResolved := preRegistry.ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "*"})
	preRegistry.Close()
	require.Equal(t, preDefault.ID, preResolved.AgentID,
		"with no configured default the two ladders must AGREE: they answer the same question "+
			"and both now fall back to the lexicographically-first chat-target agent. This "+
			"fixture reverses cfg.Agents.List precisely because slice order used to change "+
			"routing's answer — if that ever returns, this fails here")

	// The ★ toggle: PUT /api/v1/agents/agent-b {"default": true}.
	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-b", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "starring agent-b as default must succeed")

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default, "response.default must be non-nil")
	assert.True(t, *resp.Default, "PUT response must echo default=true for agent-b")

	// Write half of the bug: the toggle must persist the settings singleton,
	// not just the per-entity display flag. a.agentLoop.GetConfig() already
	// reflects the PUT's write (updateConfigJSONLocked calls
	// refreshConfigAndRewireServices, which reloads config.json and swaps it
	// in via SwapConfig) — no extra reload step is needed to observe it here.
	freshCfg := al.GetConfig()
	require.Equal(t, "agent-b", freshCfg.Agents.Defaults.DefaultAgentID,
		"the star toggle must persist agents.defaults.default_agent_id — this is the write half of the bug")

	// Read half of the bug: build a registry the same way the NEXT full
	// reload/boot would (pkg/gateway's gateway.go and pkg/agent's
	// ReloadProviderAndConfig both do exactly this: NewAgentRegistry, then
	// apply the configured override — see loop.go's NewAgentLoop /
	// ReloadProviderAndConfig, which this mirrors since this lightweight test
	// harness never wires a real reload function).
	freshRegistry := agent.NewAgentRegistry(freshCfg, provider)
	t.Cleanup(freshRegistry.Close)
	if freshCfg.Agents.Defaults.DefaultAgentID != "" {
		freshRegistry.SetDefaultAgentOverride(freshCfg.Agents.Defaults.DefaultAgentID)
	}

	// Ladder 1: webchat / GetDefaultAgent.
	defaultAgent := freshRegistry.GetDefaultAgent()
	require.NotNil(t, defaultAgent, "GetDefaultAgent must resolve some agent")

	// Ladder 2: channel routing, via ResolveRoute's fallback tier (no
	// bindings configured, so the whole cascade falls through to
	// resolveDefaultAgentID — matchedBy=="default").
	resolved := freshRegistry.ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "*"})
	assert.Equal(t, "default", resolved.MatchedBy, "no bindings are configured; must resolve via the default tier")

	assert.Equal(t, "agent-b", defaultAgent.ID,
		"registry.GetDefaultAgent must resolve to the agent that was just starred")
	assert.Equal(t, "agent-b", resolved.AgentID,
		"routing's resolveDefaultAgentID (via ResolveRoute) must resolve to the same just-starred agent")
	assert.Equal(t, defaultAgent.ID, resolved.AgentID,
		"THE BUG: webchat (GetDefaultAgent) and channel routing (ResolveRoute) must never disagree "+
			"on who the default agent is")
}

// TestUpdateAgent_DefaultToggle_WireResponseDerivedFromSingleton verifies the
// read half of the fix in isolation: the wire `default` field on GET/PUT
// responses must be DERIVED from the settings singleton
// (agents.defaults.default_agent_id), never echoed from the per-entity
// AgentConfig.Default bool — so a GET on an agent whose entity record still
// carries a stale Default:true (e.g. from before this fix, or simply never
// cleared now that the N-write fan-out is retired) does NOT falsely report
// default=true once another agent holds the singleton.
//
// BDD: Given agent-a's entity record has Default:true (a stale/legacy value)
// but the settings singleton points at agent-b,
//
//	When GET /api/v1/agents/agent-a,
//	Then the response's default field is false (derived from the singleton,
//	not the stale per-entity bool).
func TestUpdateAgent_DefaultToggle_WireResponseDerivedFromSingleton(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
				// The singleton names agent-b as the real default...
				DefaultAgentID: "agent-b",
			},
			List: []config.AgentConfig{
				// ...but agent-a's entity record still carries a stale
				// Default:true (simulating a record written by an older
				// build, or simply never reconciled now that the racy
				// clear-others fan-out is retired).
				{ID: "agent-a", Name: "Agent A", Default: true},
				{ID: "agent-b", Name: "Agent B"},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil)
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.False(t, *resp.Default,
		"agent-a's stale per-entity Default:true must NOT leak onto the wire; "+
			"the response must be derived from the settings singleton (which names agent-b)")

	// Control: agent-b (named by the singleton, zero-value per-entity Default)
	// must report true.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-b", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respB gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respB))
	require.NotNil(t, respB.Default)
	assert.True(t, *respB.Default,
		"agent-b has no per-entity Default flag set at all, but IS named by the singleton, "+
			"so the derived wire value must be true")
}

// ── Handler integration tests — updateAgent ───────────────────────────────────

// TestUpdateAgent_ValidateInbound_InvalidBody asserts that PATCH /agents/{id}
// with validate_inbound=true rejects a body where "name" is not a string.
//
// BDD:
//
//	Given validate_inbound=true and agent "test-agent-001" exists,
//	When PATCH /agents/test-agent-001 body contains {"name": 42},
//	Then the handler returns 400 with a schema error referencing AgentUpdateRequest.
//
// Traces to: fix-Q / fix-Y — handler integration test for AgentUpdateRequest validation.
func TestUpdateAgent_ValidateInbound_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithValidationAndAgent(t)

	// "name" must be a string — sending a number violates the type constraint.
	body := `{"name": 42}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent-001", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.updateAgent(w, r, "test-agent-001")

	assert.Equal(t, http.StatusBadRequest, w.Code,
		"wrong type for 'name' must return 400")
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentUpdateRequest",
		"error message must reference the schema name")
}

// TestUpdateAgent_ValidateInbound_ValidBody asserts that PATCH /agents/{id}
// with validate_inbound=true accepts a body with a valid field.
//
// BDD:
//
//	Given validate_inbound=true and agent "test-agent-001" exists,
//	When PATCH /agents/test-agent-001 body contains {"model":"gpt-4o"},
//	Then the schema validation passes (200 or business-logic response, not a 400 schema error).
//
// Traces to: fix-Q / fix-Y — handler integration test for AgentUpdateRequest validation.
func TestUpdateAgent_ValidateInbound_ValidBody(t *testing.T) {
	api := newTestRestAPIWithValidationAndAgent(t)

	body := `{"model":"gpt-4o"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent-001", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.updateAgent(w, r, "test-agent-001")

	// Schema validation must not reject this body.
	if w.Code == http.StatusBadRequest {
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
			assert.NotContains(t, resp["error"], "AgentUpdateRequest",
				"valid body must not produce a schema validation 400")
		}
	}
}

// TestUpdateAgent_ValidateInbound_EmptyPatchRejected asserts the minProperties:1
// invariant in the AgentUpdateRequest inbound schema (fix-V).
func TestUpdateAgent_ValidateInbound_EmptyPatchRejected(t *testing.T) {
	api := newTestRestAPIWithValidationAndAgent(t)

	body := `{}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/agents/test-agent-001", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.updateAgent(w, r, "test-agent-001")

	assert.Equal(
		t,
		http.StatusBadRequest,
		w.Code,
		"empty patch body {} must be rejected 400 by minProperties:1 in AgentUpdateRequest inbound schema; body: %s",
		w.Body.String(),
	)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "AgentUpdateRequest",
		"error message must reference the schema name")
	assert.Contains(t, resp["error"], "minProperties",
		"error message must reference the minProperties constraint")
}

// TestUpdateAgent_InvalidatesPreamble verifies T1: after a config change the
// ContextBuilderRegistry's InvalidateAllContextBuilders is callable and does
// not panic, and the agentLoop exposes a non-nil registry (wiring check).
//
// The deeper invariant — that InvalidateAllContextBuilders clears each builder's
// cache so the next turn rebuilds the system prompt — is covered by
// TestContextBuilderRegistry_InvalidateAll in pkg/agent/context_cache_test.go.
// This test focuses on the REST → registry call path being wired correctly.
func TestUpdateAgent_InvalidatesPreamble(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{}
	cfg.Agents.Defaults = config.AgentDefaults{
		Home:         tmpDir,
		DefaultModel: config.DefaultModel{Model: "test-model"},
		MaxTokens:    4096,
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Verify the registry is wired.
	reg := al.ContextBuilderRegistry()
	if reg == nil {
		t.Fatal("ContextBuilderRegistry must not be nil after NewAgentLoop (wiring check)")
	}

	// Register a builder, warm its cache, then invalidate via the registry.
	cb := agent.NewContextBuilder(tmpDir).WithAgentInfo("test-agent", "Test Agent")
	reg.Register("test-agent", cb)
	_ = cb.BuildSystemPromptWithCache() // warm the cache

	// InvalidateAllContextBuilders must not panic and must clear the builder's
	// cache — subsequent call to BuildSystemPromptWithCache will rebuild.
	// We cannot observe the cache state directly, but we verify no panic occurs.
	reg.InvalidateAllContextBuilders()

	// The builder must still be registered after invalidation.
	reg.Register("test-agent2", agent.NewContextBuilder(tmpDir))
	reg.InvalidateAllContextBuilders() // must tolerate multiple calls
}

// --- updateAgent default-flag tests ---

// TestUpdateAgent_SetDefaultClearsOthers verifies the single-default invariant:
// setting default=true on agent-b must clear default on agent-a.
//
// BDD: Given agent-a is default=true and agent-b is default=false,
//
//	When PUT /api/v1/agents/agent-b with {"default": true},
//	Then agent-b.Default becomes true,
//	And agent-a.Default becomes false (cleared).
func TestUpdateAgent_SetDefaultClearsOthers(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-b", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "setting default on agent-b must succeed")

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default, "response.default must be non-nil")
	assert.True(t, *resp.Default, "agent-b must now be default=true")

	// Verify agent-a is no longer default.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respA gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respA))
	require.NotNil(t, respA.Default, "agent-a.default must be non-nil after invariant enforcement")
	assert.False(t, *respA.Default, "agent-a must be default=false after agent-b became default")
}

// TestUpdateAgent_SetDefaultFalseOnlyAffectsTarget verifies that setting
// default=false on agent-a does not change agent-b.
//
// BDD: Given agent-a is default=true and agent-b is default=false,
//
//	When PUT /api/v1/agents/agent-a with {"default": false},
//	Then agent-a.Default becomes false,
//	And agent-b.Default remains false (untouched).
func TestUpdateAgent_SetDefaultFalseOnlyAffectsTarget(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": false}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.False(t, *resp.Default, "agent-a must be default=false")

	// agent-b must remain false (untouched).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-b", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respB gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respB))
	require.NotNil(t, respB.Default)
	assert.False(t, *respB.Default, "agent-b must remain false when only agent-a was updated")
}

// TestUpdateAgent_AbsentDefaultFieldChangesNothing verifies that omitting the
// default field from the request leaves all Default flags unchanged.
//
// BDD: Given agent-a is default=true,
//
//	When PUT /api/v1/agents/agent-a with {"model": "gpt-4"} (no default field),
//	Then agent-a.Default remains true.
func TestUpdateAgent_AbsentDefaultFieldChangesNothing(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"model": "gpt-4"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// agent-a must still be default.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-a", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.True(t, *resp.Default, "agent-a must remain default=true when default field was absent")
}

// TestUpdateAgent_RejectsWorkerAsDefault verifies that PUT {"default":true} on a
// worker agent is rejected with 400 — a worker can never be the routing default
// (it is not a chat target).
//
// BDD: Given a worker agent and a base default agent,
//
//	When PUT /api/v1/agents/<worker> with {"default": true},
//	Then the request fails with 400 and the base agent remains default.
func TestUpdateAgent_RejectsWorkerAsDefault(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Default: true},
				{ID: "worker", Name: "Worker", Type: config.AgentTypeWorker},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/worker", strings.NewReader(`{"default": true}`))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code,
		"setting a worker as default must be rejected with 400")
	assert.Contains(t, strings.ToLower(w.Body.String()), "worker",
		"the error must explain a worker cannot be the default")
}

// TestUpdateAgent_SetDefaultAlreadyDefault verifies that setting default=true
// on an agent that is already the default is idempotent: it stays default=true
// and no other agent changes.
//
// BDD: Given agent-a is default=true and agent-b is default=false,
//
//	When PUT /api/v1/agents/agent-a with {"default": true} (agent-a is already default),
//	Then agent-a.Default remains true,
//	And agent-b.Default remains false (invariant check: no spurious clear).
//
// Traces to: sprint/258-jun-2026 — single-default invariant, idempotent case.
func TestUpdateAgent_SetDefaultAlreadyDefault(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "setting default on already-default agent must succeed")

	var resp gen.Agent
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.NotNil(t, resp.Default)
	assert.True(t, *resp.Default, "agent-a must still be default=true")

	// agent-b must still be false (idempotent PUT must not disturb other agents).
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/agents/agent-b", nil)
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respB gen.Agent
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&respB))
	require.NotNil(t, respB.Default)
	assert.False(t, *respB.Default, "agent-b must remain false (idempotent default on agent-a)")
}

// TestUpdateAgent_DiskSingleDefault verifies the disk-level single-default
// invariant: after PUT /api/v1/agents/{id} with {default:true}, re-reading
// config.json from disk must show the settings singleton
// (agents.defaults.default_agent_id) naming exactly agent-b.
//
// BDD: Given agent-a is default=true and agent-b is default=false (per the
// settings singleton — see newRoutingTestAPI),
//
//	When PUT /api/v1/agents/agent-b with {"default": true},
//	Then re-reading config.json shows agents.defaults.default_agent_id is
//	"agent-b".
//
// Traces to: sprint/258-jun-2026 — updateAgent single-default invariant,
// disk-level. RELEASE BLOCKER fix follow-up: the durable source of "the one
// default" moved from an aggregate invariant over N per-entity Default bools
// (each its own independently-locked write — the exact race ADR-054 D6.4
// retired RepairMultipleDefaults over) to a single settings-singleton string
// behind the existing config-write lock, which cannot have two winners by
// construction. This test used to count agentstore.List() entries with
// Default==true and require exactly one; that invariant is no longer
// maintained by anything (the N-write fan-out that used to clear every OTHER
// agent's per-entity flag is retired — see rest.go's updateAgent) because
// nothing reads it anymore, so asserting on it here would just pin dead
// behavior back in.
func TestUpdateAgent_DiskSingleDefault(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-b", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT agent-b default=true must succeed")

	// Re-read the live config (already reflects the just-written config.json —
	// updateConfigJSONLocked calls refreshConfigAndRewireServices, which
	// reloads from disk and swaps it in via SwapConfig).
	liveCfg := api.agentLoop.GetConfig()
	assert.Equal(t, "agent-b", liveCfg.Agents.Defaults.DefaultAgentID,
		"agents.defaults.default_agent_id must name agent-b on disk after the PUT")

	// The per-entity Default bool on agent-b's OWN entity record is still
	// written too (kept for backward display compatibility, per config.go's
	// ADR-054 D6.4 note) — but only ITS record, not an aggregate invariant
	// across every agent.
	agents, _, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	var agentBRec *config.AgentConfig
	for i := range agents {
		if agents[i].ID == "agent-b" {
			agentBRec = &agents[i]
		}
	}
	require.NotNil(t, agentBRec, "agent-b's entity record must exist")
	assert.True(t, agentBRec.Default, "agent-b's own entity record must reflect the PUT's default:true")
}

// TestUpdateAgent_IdempotentDefault verifies that PUT default=true on an agent that is
// already the default is a no-op: the settings singleton still names that
// same agent, and no other agent is disturbed.
//
// BDD: Given agent-a is already default=true (the settings singleton names
// agent-a),
//
//	When PUT /api/v1/agents/agent-a with {"default": true} again,
//	Then agents.defaults.default_agent_id still names agent-a,
//	And agent-b's entity record remains default=false (untouched).
//
// Traces to: sprint/258-jun-2026 — updateAgent single-default invariant,
// idempotent case. RELEASE BLOCKER fix follow-up: the durable "is this THE
// default" signal is the settings singleton (agents.defaults.default_agent_id),
// not an aggregate count of per-entity Default==true records — see
// TestUpdateAgent_DiskSingleDefault's doc comment for why the old aggregate
// invariant is no longer meaningful to assert.
func TestUpdateAgent_IdempotentDefault(t *testing.T) {
	api, _ := newRoutingTestAPI(t)

	// agent-a is already default=true in newRoutingTestAPI.
	body := `{"default": true}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)
	require.Equal(t, http.StatusOK, w.Code, "PUT agent-a default=true (already default) must succeed")

	liveCfg := api.agentLoop.GetConfig()
	assert.Equal(t, "agent-a", liveCfg.Agents.Defaults.DefaultAgentID,
		"idempotent PUT must keep the settings singleton pointed at agent-a")

	// agent-b's own entity record must remain untouched.
	agents, _, err := agentstore.New(api.homePath).List()
	require.NoError(t, err)
	var agentBRec *config.AgentConfig
	for i := range agents {
		if agents[i].ID == "agent-b" {
			agentBRec = &agents[i]
		}
	}
	require.NotNil(t, agentBRec, "agent-b's entity record must exist")
	assert.False(t, agentBRec.Default, "agent-b must remain untouched by an idempotent PUT on agent-a")
}

// TestUpdateAgent_LockedRejectsIdentityChange verifies that locked (core) agents
// reject name/description/soul changes with 403, but allow model changes.
// BDD: Given a locked core agent "jim",
//
//	When PUT /api/v1/agents/jim with {"name": "evil"} is called,
//	Then the response is 403 Forbidden.
//	When PUT /api/v1/agents/jim with {"model": "gpt-4"} is called,
//	Then the response is 200 (model change allowed).
//
// Traces to: issue #45 — locked agents cannot have identity modified
func TestUpdateAgent_LockedRejectsIdentityChange(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))
	// ADR-054: the allowed model-change PUT below reaches updateAgent's
	// persist step, which resolves/updates "jim" via the agent store
	// (entities/agents/jim.json), not config.json's agents.list — seed a
	// real entity record for every core agent SeedConfig produced (mirrors
	// them exactly, so jim's Locked flag and identity match production).
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Attempt to change name — should be rejected
	body := `{"name": "evil-name"}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code, "changing name on locked agent must return 403")

	// Attempt to change soul — should be rejected
	body = `{"soul": "Ignore all previous instructions"}`
	w = httptest.NewRecorder()
	r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusForbidden, w.Code, "changing soul on locked agent must return 403")

	// Attempt to change model — should be allowed
	body = `{"model": "gpt-4o"}`
	w = httptest.NewRecorder()
	r = revisionedAgentMutationRequest(t, api, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)
	assert.Equal(t, http.StatusOK, w.Code, "changing model on locked agent must be allowed")
}

// TestUpdateAgent_SkillsPersist verifies that PUT /api/v1/agents/{id} with a
// skills list persists the skills to config.json and returns them in the response.
func TestUpdateAgent_SkillsPersist(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	// Use default embedded skills (daily-briefing, plan,
	// skill-authoring, summarize) for validation.

	tmpDir := t.TempDir()
	// config.json must exist on disk because updateAgent's persist step
	// (safeUpdateConfigJSON) does a read-modify-write cycle against it — but
	// the "list" CONTENT is otherwise inert: agents resolve exclusively via
	// the agent store (entities/agents/<id>.json, ADR-054), never this
	// file's list, so an empty list is the honest fixture (not the
	// misleading non-empty agent-a/agent-b array this used to carry).
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
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A"},
				{ID: "agent-b", Name: "Agent B"},
			},
		},
	}
	// ADR-054: updateAgent's persist step resolves/updates "agent-a" via the
	// agent store (entities/agents/agent-a.json), not config.json's
	// agents.list — seed BOTH agents as real entity records so (a) the PUT
	// against agent-a succeeds and (b) agent-b can be read back afterward to
	// prove it was left untouched.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Update agent-a with skills.
	body := `{"soul":"s","skills":["daily-briefing","summarize"]}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/agent-a", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	var resp struct {
		ID     string   `json:"id"`
		Skills []string `json:"skills"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "agent-a", resp.ID)
	assert.Equal(t, []string{"daily-briefing", "summarize"}, resp.Skills)

	// Verify the agent entity records: agent-a has skills, agent-b has none
	// — updateAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedA, err := store.Get("agent-a")
	require.NoError(t, err)
	require.Len(t, savedA.Skills, 2, "agent-a must have 2 skills persisted")
	assert.Equal(t, "daily-briefing", savedA.Skills[0])
	assert.Equal(t, "summarize", savedA.Skills[1])

	savedB, err := store.Get("agent-b")
	require.NoError(t, err)
	assert.Empty(t, savedB.Skills, "agent-b must have no skills — granting to A must not affect B")
}

// TestUpdateAgent_SkillsClear verifies that sending an empty skills array
// removes all skills from the agent (opt-out by explicit empty list).
//
// BDD: Given an agent exists with skills in config,
// When PUT /api/v1/agents/{id} is called with skills=[],
// Then the config.json has no skills key for that agent.
//
// Traces to: US-E6.
func TestUpdateAgent_SkillsClear(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	// config.json must exist on disk for updateAgent's safeUpdateConfigJSON
	// read-modify-write cycle; the "list" content itself is inert (agents
	// resolve via the agent store, ADR-054) so an empty list is the honest
	// fixture.
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
			List: []config.AgentConfig{
				{ID: "skilled-agent", Name: "Skilled Agent", Skills: []string{"web-research"}},
			},
		},
	}
	// ADR-054: updateAgent's persist step resolves/updates "skilled-agent"
	// via the agent store (entities/agents/skilled-agent.json), not
	// config.json's agents.list.
	seedAgentEntities(t, tmpDir, cfg.Agents.List)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// Send empty skills array to clear all skills.
	body := `{"skills":[]}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/skilled-agent", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "response body: %s", w.Body.String())

	// The agent entity record must have no skills key after clear —
	// updateAgent persists via the agent store, not config.json's
	// agents.list (ADR-054 D2).
	store := agentstore.New(tmpDir)
	savedAgent, err := store.Get("skilled-agent")
	require.NoError(t, err)
	assert.Empty(t, savedAgent.Skills, "skills must be empty after clearing with an empty array")
}

// TestUpdateAgent_UnknownSkillIDRejected verifies that PUT /api/v1/agents/{id} with
// an unknown skill ID is rejected with 400 when skills are installed.
//
// BDD: Given an agent and one installed skill "web-research",
// When PUT /api/v1/agents/{id} is called with skills=["bogus-skill"],
// Then the response is 400 Bad Request.
//
// Traces to: US-E6, MINOR (backend) — referential validation for skill IDs.
func TestUpdateAgent_UnknownSkillIDRejected(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

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

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{ID: "my-agent", Name: "My Agent"},
			},
		},
	}
	// This request is rejected (400, unknown skill id) at validateSkillIDs,
	// before updateAgent ever reaches its agent-store persist step, so a real
	// entity record is not strictly required for THIS test to pass — seeded
	// anyway so the fixture matches production shape (a "my-agent" a caller
	// could legitimately PUT against), rather than an in-memory-only agent.
	// (FIXTURE-VACUITY fix: this used to instead os.WriteFile a raw
	// config.json blob with a non-empty "agents.list" array — dead weight,
	// since nothing in this test reads that raw file back.)
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	// "bogus-skill" is not installed — must be rejected 400.
	body := `{"skills":["bogus-skill"]}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/my-agent", strings.NewReader(body))
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
}

// TestUpdateAgent_LockedAllowsSkills verifies ADR-090 FR-002's capability
// carve-out: locked protects built-in identity, while ordinary built-in skill
// assignments remain editable.
//
// BDD: Given a locked core agent "jim",
// When PUT /api/v1/agents/jim is called with {"skills": ["web-research"]},
// Then the response is 200 and the skill assignment is persisted.
//
// Traces to: B-2 (#332 / US-D5) extended to Skills field.
func TestUpdateAgent_LockedAllowsSkills(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	cfgJSON, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(cfgPath, cfgJSON, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}

	body := `{"skills": ["web-research"]}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/jim", strings.NewReader(body))
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "assigning skills to an ordinary locked built-in must succeed: %s", w.Body.String())
	state, err := agentstore.New(tmpDir).ReadState("jim")
	require.NoError(t, err)
	assert.Equal(t, []string{"web-research"}, state.Agent.Skills)
}

// --- PUT /api/v1/agents/{id} (tools_cfg) ---

// TestUpdateAgent_ToolsCfgIncomplete_Rejected400 is UAT batch 4 S83 leg 2: a
// tools_cfg sent to the general agent-update endpoint REPLACES the agent's
// builtin map wholesale, so an omitted key silently drops that agent's own
// posture for the omitted tool.
func TestUpdateAgent_ToolsCfgIncomplete_Rejected400(t *testing.T) {
	const agentID = "01JXTESTAGENTSTARTTEST001"
	api := newTestRestAPIWithAgent(t)
	seedGlobalCeiling(t, api)
	store := seedAgentWithFullPolicy(t, api, agentID)

	policies := fullBuiltinPolicyMap("allow")
	delete(policies, "stop_plan")
	body := fmt.Sprintf(`{"tools_cfg":{"builtin":{"policies":%s}}}`, mustPolicyJSON(t, policies))

	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/"+agentID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"a tools_cfg omitting a static builtin tool must be rejected; body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "tools_cfg.builtin.policies",
		"the rejection must come from the caller-side submitted-map check")
	assert.Contains(t, w.Body.String(), "stop_plan", "and it must name the omitted tool")

	got, err := store.Get(agentID)
	require.NoError(t, err)
	assert.Equal(t, config.ToolPolicyDeny, got.Tools.Builtin.Policies["bash"],
		"the rejected write must have persisted nothing")
}

// ---------------------------------------------------------------------------
// ADR-072 Finding A — stale skill-grant cache on a Skills-only config edit.
//
// ContextBuilder.skillAllowed (pkg/agent/context.go) is enforced from a
// skillAllowlist snapshot installed ONCE at agent-instance construction
// (instance.go's contextBuilder.WithSkillAllowlist(agentCfg.Skills)). Before
// this fix, a PUT /api/v1/agents/{id} request that changed ONLY the `skills`
// field was not in updateAgent's needsReload set, so it persisted to config
// (agentRec.Skills is written unconditionally) but never rebuilt the running
// AgentInstance — the Skill tool and the /<skill> command (both gated via
// skillAllowed) kept enforcing the OLD grant list until some unrelated field
// (e.g. soul) forced a reload. grantPredicateFor (pkg/sysagent/tools/skill.go)
// masked this because it re-reads config live per call, so list_skills looked
// correct while Skill/`/<skill>` silently disagreed.
//
// These tests prove a Skills-only edit (a) takes effect on the live,
// already-constructed AgentInstance without requiring any other field to
// change or the process to restart, and (b) does so via the SAME fast,
// no-cascade path (fastAgentUpsert/AgentLoop.UpsertAgentFast) that a soul
// change already used — see agent_fast_upsert_no_restart_test.go for why the
// full restartServices cascade must not fire on a plain update.
// ---------------------------------------------------------------------------

// TestUpdateAgent_SkillsOnlyChange_UpdatesLiveAllowlistWithoutRestart is the
// direct regression test for Finding A: editing only `skills` must refresh
// the live instance's skill grant (mirrored on AgentInstance.SkillsFilter,
// the same source ContextBuilder.WithSkillAllowlist was built from) via the
// fast upsert path, with zero full-reload-trigger invocations.
func TestUpdateAgent_SkillsOnlyChange_UpdatesLiveAllowlistWithoutRestart(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// Sanity: the fixture agent starts with no skill grants at all.
	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	require.Empty(t, inst.SkillsFilter, "fixture agent must start with no skill grants")

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	body := `{"skills":["summarize","daily-briefing"]}`
	w := httptest.NewRecorder()
	r := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	resp := decodeAgentResp(t, w.Body.Bytes())
	assert.Nil(t, resp.Warning, "a plain skills-only update must not carry a warning: %+v", resp.Warning)

	// The fix: this must be zero, exactly like the soul-change case in
	// agent_fast_upsert_no_restart_test.go — a skills-only edit is now folded
	// into needsReload and goes through fastAgentUpsert, never the cascading
	// full reload trigger.
	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a skills-only PUT /agents/{id} (no concurrent reload in flight) must NEVER invoke the reload "+
			"trigger — see TestCreateAgent_DoesNotTriggerFullReload for the full restartServices-cascade "+
			"rationale")

	// The regression itself: without the fix, this instance would still be the
	// STALE one constructed before the PUT, with SkillsFilter empty and its
	// ContextBuilder's skillAllowlist unrefreshed — Skill/`/<skill>` would keep
	// denying "summarize" and "daily-briefing" despite the grant having just
	// been persisted.
	inst, ok = api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok, "agent must remain resolvable via the fast path alone")
	require.NotNil(t, inst)
	assert.ElementsMatch(t, []string{"summarize", "daily-briefing"}, inst.SkillsFilter,
		"the live AgentInstance's skill grant (SkillsFilter, mirroring the source "+
			"ContextBuilder.skillAllowlist was built from) must reflect the just-persisted grant "+
			"immediately, with no other field changed and no restart")
}

// TestUpdateAgent_SkillsOnlyChange_ClearingGrantsTakesEffectImmediately
// covers the opposite direction: revoking every skill grant (an explicit
// empty array, per updateAgent's "Skills: replace the agent's skill list"
// comment) must also reach the live instance immediately, not just a config
// file nobody re-reads until the next unrelated reload.
func TestUpdateAgent_SkillsOnlyChange_ClearingGrantsTakesEffectImmediately(t *testing.T) {
	api := buildExecutorTestAPI(t)

	// First grant a skill (also exercises the fix on the way up).
	w1 := httptest.NewRecorder()
	r1 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"skills":["summarize"]}`))
	r1.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w1, r1)
	require.Equal(t, http.StatusOK, w1.Code, "body: %s", w1.Body.String())

	inst, ok := api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	require.ElementsMatch(t, []string{"summarize"}, inst.SkillsFilter)

	var reloadCalls atomic.Int32
	api.agentLoop.SetReloadFunc(func() error {
		reloadCalls.Add(1)
		return nil
	})

	// Now revoke it with an explicit empty array.
	w2 := httptest.NewRecorder()
	r2 := revisionedAgentMutationRequest(t, api, "/api/v1/agents/test-agent",
		strings.NewReader(`{"skills":[]}`))
	r2.Header.Set("Content-Type", "application/json")
	api.HandleAgents(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "body: %s", w2.Body.String())

	assert.Equal(t, int32(0), reloadCalls.Load(),
		"a skills-only revocation must also go through the fast path, never the full reload trigger")

	inst, ok = api.agentLoop.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	assert.Empty(t, inst.SkillsFilter,
		"revoking all skill grants must clear the live instance's grant immediately, with no restart")
}
