// rest_tasks_run_task_gate_test.go — REST-level regression test for a
// reviewer-confirmed fail-open bypass in
// pkg/agent/task_executor.go's requireRunTaskAutoDispatchApproved (the
// run_task ask-policy gate added alongside the S1 plan-state gate).
//
// The gate used to treat Task.StartedAt != "" ALONE as proof "this run has
// already been claimed/approved" and skip re-consulting the agent's run_task
// tool policy. That premise was false: `started_at` is a directly
// wire-settable TaskUpdateRequest field
// (contracts/components/schemas/TaskUpdateRequest.yaml), and handleTaskPatch
// (rest_tasks.go) applies it UNCONDITIONALLY — regardless of whether
// `status` is also present in the same PATCH. The in_progress-dispatch
// launch block a few lines below is gated on req.Status != nil alone, so a
// PATCH carrying ONLY started_at writes the field, leaves the task at
// `next`, and launches nothing.
//
// Attack: agent has run_task: ask; a standalone task sits `next` with
// StartedAt=="". Any authenticated task-write caller sends
// PATCH /api/v1/tasks/{id} with body {"started_at": "..."} and no status. On
// the next CheckQueuedTasks tick, the (buggy) gate sees PlanID=="" but
// StartedAt!="" and returns nil — the task dispatches with NO human approval
// ever, and NO trace (the gate's own Debug log never fires when it exempts).
//
// Fix: requireRunTaskAutoDispatchApproved now requires BOTH
// StartedAt != "" AND SessionID != "". SessionID has no corresponding field
// on TaskUpdateRequest at all — handleTaskPatch never derives
// task.Patch.SessionID from any request field (grep confirms it is written
// only from pkg/agent/task_executor.go's own internal claim path,
// createTaskSessionSync/StartTaskNow) — so it cannot be forged through this
// endpoint. This test drives the REAL PATCH path (not a harness-seeded
// field, which is precisely the gap that hid the original bug — see
// pkg/agent/task_executor_run_task_policy_gate_test.go's
// TestExecuteTask_AskPolicyRetryContinuesWithoutReapproval, which only ever
// seeded StartedAt directly).
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// runTaskGateTestAgentID is the ask-policy test agent id used by
// newTestRestAPIWithAskPolicyAgent below.
const runTaskGateTestAgentID = "ask-gated-agent"

// newTestRestAPIWithAskPolicyAgent mirrors newTestRestAPIAlignedStores
// (rest_tasks_start_test.go) — api.taskStore and the internal taskExecutor's
// own store must resolve to the SAME on-disk directory (the agent loop
// places its task store at filepath.Dir(Agents.Defaults.Home)/tasks) or
// CheckQueuedTasks/ExecuteTask can never find a task the REST handler
// created, silently no-op'ing regardless of the run_task gate under test —
// but additionally registers ONE real custom agent (runTaskGateTestAgentID)
// whose run_task builtin tool policy is "ask", the minimum config needed to
// exercise requireRunTaskAutoDispatchApproved's gate through the REAL REST
// PATCH surface end to end (AgentLoop.ResolveApprovalToolPolicy resolves
// against this agent's real config, not a stub).
func newTestRestAPIWithAskPolicyAgent(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	// The agent loop places its task store at filepath.Dir(Agents.Defaults.Home)/tasks
	// (pkg/agent/loop.go). Point Defaults.Home at tmpDir/workspace so that
	// resolves to tmpDir/tasks — the same path task.New below uses for
	// api.taskStore.
	workspaceDir := tmpDir + "/workspace"
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: workspaceDir, ModelName: "test-model", MaxTokens: 4096},
			List: []config.AgentConfig{
				{
					ID:   runTaskGateTestAgentID,
					Name: "Ask Policy Gate Test Agent",
					Type: config.AgentTypeWorker,
					Home: t.TempDir(),
					Tools: &config.AgentToolsCfg{
						Builtin: config.AgentBuiltinToolsCfg{
							Policies: map[string]config.ToolPolicy{"run_task": config.ToolPolicyAsk},
						},
					},
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Both the executor and the API use the same tasks directory.
	sharedTaskStore := task.New(tmpDir + "/tasks")
	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     sharedTaskStore,
		taskLock:      task.TaskFileLock,
		taskExecutor:  agent.GetTaskExecutor(al),
	}
}

// TestHandleTaskPatch_BareStartedAtDoesNotBypassRunTaskAskGate is the
// end-to-end reproduction/regression test for the bypass described in this
// file's header comment: a bare PATCH {"started_at": ...} — no status field
// at all — must not let a standalone task assigned to an ask-policy agent
// slip past requireRunTaskAutoDispatchApproved on the next unattended
// dispatch tick.
//
// BDD:
//
//	Given a standalone `next` task assigned to an agent whose run_task
//	  policy is "ask", never claimed (started_at empty, session_id empty),
//	When PATCH /api/v1/tasks/{id} is sent with ONLY {"started_at": "..."}
//	  (no status field),
//	Then the PATCH succeeds (200) and started_at is persisted, but status
//	  remains `next` (the PATCH itself never dispatches anything);
//	And when the next CheckQueuedTasks tick runs,
//	Then the task must NOT have auto-dispatched (status still `next`) —
//	  the forged started_at, with no real session ever created, must not be
//	  mistaken for an already-approved continuation.
func TestHandleTaskPatch_BareStartedAtDoesNotBypassRunTaskAskGate(t *testing.T) {
	api := newTestRestAPIWithAskPolicyAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{runTaskGateTestAgentID})

	tsk := createTaskViaAPI(t, api, "forged started_at attack", wsID)
	advanceTaskToNext(t, api, tsk.Id)

	wAgent := patchTask(t, api, tsk.Id, `{"agent_id":"`+runTaskGateTestAgentID+`"}`)
	require.Equal(t, http.StatusOK, wAgent.Code,
		"assign agent_id must succeed; body=%s", wAgent.Body.String())

	// The attack: a bare started_at-only PATCH, deliberately no status field.
	wAttack := patchTask(t, api, tsk.Id, `{"started_at":"2020-01-01T00:00:00Z"}`)
	require.Equal(t, http.StatusOK, wAttack.Code,
		"bare started_at PATCH must succeed (it is a legal field write on its own); body=%s",
		wAttack.Body.String())

	var afterAttack gen.Task
	require.NoError(t, json.Unmarshal(wAttack.Body.Bytes(), &afterAttack))
	require.Equal(t, gen.TaskStatusNext, afterAttack.Status,
		"the bare started_at PATCH carries no status field and must not change status on its own")
	require.NotNil(t, afterAttack.StartedAt, "started_at must have been persisted by the PATCH")
	require.Nil(t, afterAttack.SessionId,
		"a bare started_at PATCH must NOT create or set a session — TaskUpdateRequest has no "+
			"session_id field, and this is exactly the signal the fix relies on being unforgeable")

	// Simulate the next unattended dispatch tick (what the heartbeat drives
	// in production).
	api.taskExecutor.CheckQueuedTasks(context.Background())

	final := getTaskStatus(t, api, tsk.Id)
	require.Equal(t, gen.TaskStatusNext, final,
		"task must NOT have auto-dispatched — a forged started_at with no real claim/session must "+
			"still require an explicit run_task approval under the agent's ask policy")
}
