// rest_tasks_external_cli_assignment_test.go — gateway-level coverage for the
// "Fix B/C" task-assignment change: validateTaskAgentID (rest_tasks.go) no
// longer rejects a subagent_3p (external-CLI) worker outright — it is a
// legitimate task assignee IFF it is a member of the task's workspace TEAM
// (workspace.TeamSet: core_team ∪ delegation edge endpoints), exactly like a
// native agent. The TeamSet rejection itself (an agent absent from that set,
// worker or not) must still fire — this is the guard that survives the
// subagent_3p-specific carve-out's removal.
//
// This closes a gap left by the existing suites:
//   - pkg/gateway/board_task_hardening_test.go's TestTask_Post_RejectsUnknownAgentID
//     covers unknown-agent-ID rejection, not team-membership for a KNOWN
//     subagent_3p agent.
//   - pkg/gateway/rest_tasks_test.go's TestValidateTaskAgentID_* tests only
//     exercise the nil-agentLoop/empty-agent-ID guard clauses directly.
//   - pkg/tools/task_test.go and pkg/sysagent/tools/task_delegation_test.go's
//     flipped Allows* tests cover the agent-tool-call surface
//     (create_task/update_task), not the human/SPA REST surface
//     (POST/PATCH /api/v1/tasks) this file exercises.
//
// Traces to: docs/internal/architecture/ADR-042-external-cli-task-execution.md

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// nativeTestAgentID / extCLITestAgentID are the two agents registered by
// newTestRestAPIWithNativeAndExternalCLIAgent below.
const (
	nativeTestAgentID = "01JXNATIVEASSIGNEETEST01"
	extCLITestAgentID = "01JXEXTCLIASSIGNEETEST1"
)

// newTestRestAPIWithNativeAndExternalCLIAgent builds a restAPI whose registry
// has TWO agents: a native custom agent and a subagent_3p (external-CLI)
// worker (config.AgentTypeWorker + Subagents.Executor.Kind ==
// ExecutorKindExternalCLI — the exact predicate config.IsExternalCLIWorkerID
// checks, mirroring pkg/agent/task_executor_external_cli_test.go's fixture).
// Neither agent is placed on any workspace's team automatically — callers use
// setWorkspaceCoreTeam to control exactly which agents are "on team" for a
// given assertion.
func newTestRestAPIWithNativeAndExternalCLIAgent(t *testing.T) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	nativeWorkspace := filepath.Join(tmpDir, "agents", nativeTestAgentID)
	extCLIWorkspace := filepath.Join(tmpDir, "agents", extCLITestAgentID)
	require.NoError(t, os.MkdirAll(nativeWorkspace, 0o700))
	require.NoError(t, os.MkdirAll(extCLIWorkspace, 0o700))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{
				{
					ID:      nativeTestAgentID,
					Name:    "Native Assignee",
					Default: true,
					Type:    config.AgentTypeCustom,
					Home:    nativeWorkspace,
				},
				{
					ID:   extCLITestAgentID,
					Name: "External CLI Assignee",
					Type: config.AgentTypeWorker,
					Home: extCLIWorkspace,
					Subagents: &config.SubagentsConfig{
						Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
					},
				},
			},
		},
	}
	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	// Sanity: confirm the fixture actually IS what validateTaskAgentID's
	// removed carve-out used to key off, so this suite fails loudly (not
	// silently no-ops) if config.IsExternalCLIWorkerID's predicate ever
	// changes shape out from under this fixture.
	require.True(t, al.GetConfig().IsExternalCLIWorkerID(extCLITestAgentID),
		"fixture agent %q must satisfy IsExternalCLIWorkerID for this suite to test what it claims to test",
		extCLITestAgentID)
	require.False(t, al.GetConfig().IsExternalCLIWorkerID(nativeTestAgentID),
		"fixture agent %q must NOT be an external-CLI worker (control case)", nativeTestAgentID)

	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(tmpDir + "/tasks"),
		taskLock:      task.TaskFileLock,
	}
	t.Cleanup(func() { api.agentLoop.WaitForActiveRequests() })
	return api
}

// TestHandleTasks_Post_Subagent3pAssignee_OnWorkspaceTeam_Returns201 proves
// the Fix B/C behavior on the human/SPA REST surface: a subagent_3p
// (external-CLI) worker that IS a member of the task's workspace team is now
// a legitimate task assignee — POST /api/v1/tasks succeeds (201), and the
// created task actually carries that agent_id (not silently dropped or
// substituted).
//
// BDD:
//
//	Given a workspace whose team includes a subagent_3p worker,
//	When POST /api/v1/tasks with agent_id=<that worker>,
//	Then 201, and the response task's agent_id equals the worker's ID.
//
// Differentiation: the same workspace/team also accepts the native agent as
// assignee, and the two created tasks carry two DIFFERENT agent_ids — proving
// the 201 is not a hardcoded response that ignores agent_id.
//
// Traces to: docs/internal/architecture/ADR-042-external-cli-task-execution.md;
// pkg/gateway/rest_tasks.go validateTaskAgentID (subagent_3p carve-out removed).
func TestHandleTasks_Post_Subagent3pAssignee_OnWorkspaceTeam_Returns201(t *testing.T) {
	api := newTestRestAPIWithNativeAndExternalCLIAgent(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{nativeTestAgentID, extCLITestAgentID})

	postTask := func(agentID string) *httptest.ResponseRecorder {
		body := fmt.Sprintf(
			`{"title":"AssignTo-%s","action":"llm","workspace_id":%q,"prompt":"do it","agent_id":%q}`,
			agentID, wsID, agentID,
		)
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.URL.Path = "/api/v1/tasks"
		api.HandleTasks(w, r)
		return w
	}

	wExtCLI := postTask(extCLITestAgentID)
	require.Equal(t, http.StatusCreated, wExtCLI.Code,
		"POST with an on-team subagent_3p agent_id must return 201; body=%s", wExtCLI.Body.String())
	var extCLITask gen.Task
	require.NoError(t, json.Unmarshal(wExtCLI.Body.Bytes(), &extCLITask))
	require.NotNil(t, extCLITask.AgentId, "created task must carry a non-nil agent_id")
	assert.Equal(t, extCLITestAgentID, *extCLITask.AgentId,
		"created task's agent_id must be the assigned subagent_3p worker, not dropped or substituted")

	// Differentiation: a native agent_id on the SAME team/workspace also
	// succeeds and produces a task carrying its OWN (different) agent_id —
	// proving the 201 above reflects real agent_id-specific handling, not a
	// response that would be identical regardless of which agent was assigned.
	wNative := postTask(nativeTestAgentID)
	require.Equal(t, http.StatusCreated, wNative.Code,
		"POST with an on-team native agent_id must also return 201; body=%s", wNative.Body.String())
	var nativeTask gen.Task
	require.NoError(t, json.Unmarshal(wNative.Body.Bytes(), &nativeTask))
	require.NotNil(t, nativeTask.AgentId)
	assert.Equal(t, nativeTestAgentID, *nativeTask.AgentId)
	assert.NotEqual(t, *extCLITask.AgentId, *nativeTask.AgentId,
		"the two created tasks must carry two DIFFERENT agent_ids")

	// Read back via GET to prove the assignment was actually persisted, not
	// just echoed in the create response.
	wGet := httptest.NewRecorder()
	rGet := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+extCLITask.Id, nil)
	rGet.URL.Path = "/api/v1/tasks/" + extCLITask.Id
	api.HandleTasks(wGet, rGet)
	require.Equal(t, http.StatusOK, wGet.Code)
	var reread gen.Task
	require.NoError(t, json.Unmarshal(wGet.Body.Bytes(), &reread))
	require.NotNil(t, reread.AgentId)
	assert.Equal(t, extCLITestAgentID, *reread.AgentId,
		"GET after create must reflect the persisted subagent_3p assignment")
}

// TestHandleTasks_Patch_ReassignToOffTeamAgent_Returns400 proves the
// team-membership guard in validateTaskAgentID still fires for an agent that
// is NOT on the task's workspace team — regardless of whether that agent is a
// subagent_3p worker or a native agent. This is the guard the Fix B/C change
// deliberately preserved (only the subagent_3p-specific carve-out was
// removed; the TeamSet check is untouched).
//
// BDD:
//
//	Given a task in a workspace whose team does NOT include a known,
//	  registered subagent_3p agent,
//	When PATCH /api/v1/tasks/{id} reassigns agent_id to that off-team agent,
//	Then 400, the error names the offending agent, and the task's agent_id on
//	  disk is UNCHANGED (the rejected reassignment must not partially apply).
//	Given the SAME agent is then added to the workspace team,
//	When the identical PATCH is retried,
//	Then 200, and the task's agent_id IS now updated —
//	  proving the 400 above was a real team-membership check, not a hardcoded
//	  rejection of this agent_id/endpoint.
//
// Traces to: docs/internal/architecture/ADR-042-external-cli-task-execution.md;
// pkg/gateway/rest_tasks.go validateTaskAgentID (workspace TeamSet guard).
func TestHandleTasks_Patch_ReassignToOffTeamAgent_Returns400(t *testing.T) {
	api := newTestRestAPIWithNativeAndExternalCLIAgent(t)
	wsID := ensureTestWorkspace(t, api)
	// Only the native agent is on-team; the subagent_3p agent is registered
	// (exists) but deliberately excluded from this workspace's team.
	setWorkspaceCoreTeam(t, api, wsID, []string{nativeTestAgentID})

	created := createTaskViaAPI(t, api, "ReassignOffTeamTest", wsID)
	require.Nil(t, created.AgentId, "task must start unassigned")

	patchBody := fmt.Sprintf(`{"agent_id":%q}`, extCLITestAgentID)
	wPatch := patchTask(t, api, created.Id, patchBody)
	require.Equal(t, http.StatusBadRequest, wPatch.Code,
		"PATCH reassigning to an off-team agent must return 400; body=%s", wPatch.Body.String())
	assert.Contains(t, wPatch.Body.String(), extCLITestAgentID,
		"400 body must name the rejected agent so the caller can diagnose the problem")
	assert.Contains(t, wPatch.Body.String(), "not a member of this workspace's team",
		"400 body must explain the rejection is a team-membership failure, not a generic error")

	// Persistence check: the rejected PATCH must not have partially applied —
	// the task must still be unassigned on disk.
	unchanged, err := api.taskStore.Get(created.Id)
	require.NoError(t, err)
	assert.Empty(t, unchanged.AgentID,
		"a rejected reassignment must leave the task's agent_id unchanged (still empty)")

	// Differentiation / real-check proof: add the SAME agent to the team and
	// retry the IDENTICAL PATCH — it must now succeed. This proves the 400
	// above was a genuine team-membership evaluation of THIS agent_id, not a
	// hardcoded/blanket rejection of subagent_3p or of this specific ID.
	setWorkspaceCoreTeam(t, api, wsID, []string{nativeTestAgentID, extCLITestAgentID})
	wPatch2 := patchTask(t, api, created.Id, patchBody)
	require.Equal(t, http.StatusOK, wPatch2.Code,
		"the identical PATCH must succeed once the agent is added to the workspace team; body=%s",
		wPatch2.Body.String())

	afterFix, err := api.taskStore.Get(created.Id)
	require.NoError(t, err)
	assert.Equal(t, extCLITestAgentID, afterFix.AgentID,
		"once on-team, the subagent_3p reassignment must actually persist")
}
