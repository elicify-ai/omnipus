// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// These tests assert OUTCOMES, not mechanisms: what a caller can actually see
// and actually mutate through the privileged cross-workspace task surface.
// Each is written so that reverting the corresponding production change turns
// it red.
//
// The disclosure they close: list_tasks_in_workspace took no context at all, so
// every filter came from the model's own arguments, every filter is optional,
// and task.Filter treats each empty field as "filter OFF" — meaning
// `list_tasks_in_workspace {}` returned every task file in $OMNIPUS_HOME/tasks,
// marshalled as the whole task.Task struct. The tool resolves `allow` from the
// global seed, so that was reachable from any agent's turn.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// callerCtx returns a context carrying an agent principal, which is what
// production always supplies: pkg/agent/loop.go seeds every turn context via
// tools.WithAgentID before any tool executes. A bare context.Background() is
// not a realistic invocation of these tools.
func callerCtx(agentID string) context.Context {
	return tools.WithAgentID(context.Background(), agentID)
}

// workspaceTaskEnvelope is the shape list_tasks_in_workspace returns. Declared
// here rather than reusing the production type so a test cannot pass merely
// because the production struct changed shape underneath it.
type workspaceTaskEnvelope struct {
	Tasks     []map[string]any `json:"tasks"`
	Matched   int              `json:"matched"`
	Returned  int              `json:"returned"`
	Truncated bool             `json:"truncated"`
}

func runWorkspaceList(t *testing.T, deps *systools.Deps, ctx context.Context,
	args map[string]any) workspaceTaskEnvelope {
	t.Helper()
	res := systools.NewTaskListTool(deps).Execute(ctx, args)
	require.False(t, res.IsError, "list_tasks_in_workspace: %s", res.ForLLM)
	var env workspaceTaskEnvelope
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &env),
		"list_tasks_in_workspace: unmarshal %q", res.ForLLM)
	return env
}

// seedThreePrincipals writes one task per principal into a single store: one
// assigned to agent A, one assigned to agent B, one created by agent B for a
// third agent, and one created by a HUMAN whose username collides with agent
// A's id. Only the first two rows are ever agent A's.
func seedThreePrincipals(t *testing.T, home string) {
	t.Helper()
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_A_ASSIGNED0001", Title: "A-ASSIGNED", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "agent-a",
	})
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_A_DISPATCH0001", Title: "A-DISPATCHED", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "agent-c", CreatedByAgentID: "agent-a",
	})
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_B_ASSIGNED0001", Title: "B-ASSIGNED-SECRET", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "agent-b",
	})
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_B_DISPATCH0001", Title: "B-DISPATCHED-SECRET", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "agent-c", CreatedByAgentID: "agent-b",
	})
	// The REST/human shape: a username in Owner/CreatedBy, no agent attribution
	// anywhere, and the username happens to equal an agent id.
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_HUMAN000000001", Title: "HUMAN-PRIVATE-SECRET", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, Owner: "agent-a", CreatedBy: "agent-a",
	})
}

// TestListTasksInWorkspace_NoFilters_ReturnsOnlyCallersOwnTasks is the headline
// outcome: the bare `{}` call — every filter omitted — must not disclose a
// single row belonging to another agent or to a human.
func TestListTasksInWorkspace_NoFilters_ReturnsOnlyCallersOwnTasks(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	seedThreePrincipals(t, home)

	// Sanity: the store really does hold other principals' work, so a leak
	// would have something to leak.
	all, err := task.New(filepath.Join(home, "tasks")).List(task.Filter{})
	require.NoError(t, err)
	require.Len(t, all, 5, "the fixture must contain other principals' tasks")

	res := systools.NewTaskListTool(deps).Execute(callerCtx("agent-a"), map[string]any{})
	require.False(t, res.IsError, "list_tasks_in_workspace: %s", res.ForLLM)

	for _, secret := range []string{"B-ASSIGNED-SECRET", "B-DISPATCHED-SECRET", "HUMAN-PRIVATE-SECRET"} {
		assert.NotContains(t, res.ForLLM, secret,
			"a task belonging to another principal leaked into agent-a's result")
	}

	var env workspaceTaskEnvelope
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &env))
	assert.Equal(t, 2, env.Returned, "agent-a owns exactly 2 of the 5 seeded tasks")

	titles := map[string]bool{}
	for _, row := range env.Tasks {
		titles[fmt.Sprint(row["title"])] = true
	}
	assert.True(t, titles["A-ASSIGNED"], "the caller's own assigned task must still be listed")
	assert.True(t, titles["A-DISPATCHED"], "the caller's own dispatched task must still be listed")
}

// TestListTasksInWorkspace_HumanUsernameCollisionNotDisclosed isolates the
// namespace half of the same defect: agent-a must not receive a human's task
// merely because that human's username is "agent-a".
func TestListTasksInWorkspace_HumanUsernameCollisionNotDisclosed(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_HUMAN000000002", Title: "HUMAN-PRIVATE-SECRET", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, Owner: "agent-a", CreatedBy: "agent-a",
	})

	env := runWorkspaceList(t, deps, callerCtx("agent-a"), map[string]any{})
	assert.Equal(t, 0, env.Returned,
		"a task attributed only to the mixed-namespace created_by must belong to no agent")
}

// TestListTasksInWorkspace_EmptyPrincipal_ErrorsAndLeaksNothing proves the
// fail-closed guard: with no resolvable calling agent the tool ERRORS and
// returns no rows — never an empty success, which would be indistinguishable
// from genuinely having no tasks and would hide the misconfiguration.
func TestListTasksInWorkspace_EmptyPrincipal_ErrorsAndLeaksNothing(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	seedThreePrincipals(t, home)

	for name, ctx := range map[string]context.Context{
		"absent":     context.Background(),
		"whitespace": tools.WithAgentID(context.Background(), "   "),
	} {
		res := systools.NewTaskListTool(deps).Execute(ctx, map[string]any{})
		require.True(t, res.IsError, "ctx=%s: an unresolvable principal must be an error, got: %s",
			name, res.ForLLM)
		for _, title := range []string{
			"A-ASSIGNED", "A-DISPATCHED", "B-ASSIGNED-SECRET", "B-DISPATCHED-SECRET",
			"HUMAN-PRIVATE-SECRET",
		} {
			assert.NotContains(t, res.ForLLM, title, "ctx=%s: task title leaked on the error path", name)
		}
	}
}

// TestListTasksInWorkspace_ProjectionOmitsDiskOnlyFields proves the response is
// an allowlist PROJECTION, not a marshal of the on-disk task.Task. task.Task
// documents four fields as DISK-ONLY — CreatedByAgentID ("MUST NOT be added to
// any schema in contracts/"), Scratchpad, PendingJudgeClaim, DelegationDepth —
// and a whole-struct marshal shipped every one of them, plus prompt and result.
func TestListTasksInWorkspace_ProjectionOmitsDiskOnlyFields(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	writeTask(t, home, task.Task{
		ID: "01JXSCOPE_PROJECTION0001", Title: "PROJECTED", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "agent-a",
		CreatedByAgentID:  "agent-a",
		Scratchpad:        true,
		PendingJudgeClaim: "PRIVATE-CLAIM-SECRET",
		DelegationDepth:   7,
		Prompt:            "PRIVATE-PROMPT-SECRET",
		Result:            "PRIVATE-RESULT-SECRET",
	})

	res := systools.NewTaskListTool(deps).Execute(callerCtx("agent-a"), map[string]any{})
	require.False(t, res.IsError, "list_tasks_in_workspace: %s", res.ForLLM)
	require.Contains(t, res.ForLLM, "PROJECTED",
		"the seeded task did not come back at all; the rest of this test would prove nothing")

	for _, forbidden := range []string{
		"scratchpad", "pending_judge_claim", "created_by_agent_id", "delegation_depth",
		"PRIVATE-CLAIM-SECRET", "PRIVATE-PROMPT-SECRET", "PRIVATE-RESULT-SECRET",
	} {
		assert.NotContains(t, res.ForLLM, forbidden, "disk-only content crossed the tool boundary")
	}
}

// TestListTasksInWorkspace_BoundedAndReportsTruncation proves the response is
// bounded AND that a bounded response says so. A short list that looks complete
// is the worst possible output for a tool whose job is "find the id to act on".
func TestListTasksInWorkspace_BoundedAndReportsTruncation(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)

	const seeded = 105
	for i := 0; i < seeded; i++ {
		writeTask(t, home, task.Task{
			ID: fmt.Sprintf("01JXSCOPE_BULK%010d", i), Title: fmt.Sprintf("BULK-%03d", i),
			Status: task.StatusNext, WorkspaceID: testWorkspaceID, AgentID: "agent-a",
		})
	}

	env := runWorkspaceList(t, deps, callerCtx("agent-a"), map[string]any{})
	assert.Equal(t, 100, env.Returned, "the response must be bounded")
	assert.Len(t, env.Tasks, 100, "the row array must honour the same bound it reports")
	assert.Equal(t, seeded, env.Matched, "matched must report the true population, not the bounded one")
	assert.True(t, env.Truncated,
		"a truncated response must say so, or a caller reads a partial list as the complete one")
}

// TestListTasksInWorkspace_ArgumentsOnlyNarrow proves a caller-supplied
// agent_id cannot widen the scope: naming another agent shows only the work
// THIS caller dispatched to them, never that agent's own tasks.
func TestListTasksInWorkspace_ArgumentsOnlyNarrow(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	seedThreePrincipals(t, home)

	env := runWorkspaceList(t, deps, callerCtx("agent-a"), map[string]any{"agent_id": "agent-c"})
	require.Equal(t, 1, env.Returned, "expected only the row agent-a dispatched to agent-c")
	assert.Equal(t, "A-DISPATCHED", fmt.Sprint(env.Tasks[0]["title"]))

	// agent-b's own task must remain invisible however it is asked for.
	res := systools.NewTaskListTool(deps).Execute(callerCtx("agent-a"),
		map[string]any{"agent_id": "agent-b"})
	require.False(t, res.IsError, "%s", res.ForLLM)
	assert.NotContains(t, res.ForLLM, "B-ASSIGNED-SECRET")
}

// --- mutation gates ---

// TestUpdateTaskInWorkspace_HumanUsernameCollisionDenied proves the
// authorization half: a task created by the HUMAN username "jim" is not
// mutable by the AGENT "jim". The old gate compared an agent id against
// Task.CreatedBy, which is mixed-namespace — and this file's own create path
// writes a username into it — so the collision handed the agent a pass that
// skipped the delegation check entirely.
func TestUpdateTaskInWorkspace_HumanUsernameCollisionDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("ray")

	writeTask(t, home, task.Task{
		ID: "01JXCOLLIDE_UPDATE000001", Title: "Human's task", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID,
		AgentID:     "ray",
		// The REST path's shape: a human username, no agent attribution.
		Owner: "jim", CreatedBy: "jim",
	})

	res := systools.NewTaskUpdateTool(deps).Execute(callerCtx("jim"), map[string]any{
		"id": "01JXCOLLIDE_UPDATE000001", "status": "in_progress",
	})
	assertDelegationDenied(t, res)

	store := task.New(filepath.Join(home, "tasks"))
	got, err := store.Get("01JXCOLLIDE_UPDATE000001")
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, got.Status, "a denied update must not reach disk")
}

// TestDeleteTaskInWorkspace_HumanUsernameCollisionDenied is the delete-path
// twin of the update test above.
func TestDeleteTaskInWorkspace_HumanUsernameCollisionDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("ray")

	writeTask(t, home, task.Task{
		ID: "01JXCOLLIDE_DELETE000001", Title: "Human's task", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "ray", Owner: "jim", CreatedBy: "jim",
	})

	res := systools.NewTaskDeleteTool(deps).Execute(callerCtx("jim"), map[string]any{
		"id": "01JXCOLLIDE_DELETE000001", "confirm": true,
	})
	assertDelegationDenied(t, res)

	_, statErr := os.Stat(filepath.Join(home, "tasks", "01JXCOLLIDE_DELETE000001.json"))
	assert.NoError(t, statErr, "a denied delete must not remove the task file")
}

// TestUpdateTaskInWorkspace_EmptyPrincipalDenied proves the empty/empty bypass
// is closed.
//
// The old condition was `AgentID != "" && AgentID != caller && CreatedBy !=
// caller`. With an unattributed task (CreatedBy == "", the documented normal
// state for every agent-created or scheduled task) and no caller principal, the
// final clause evaluated `"" != ""` → false, so the WHOLE condition was false
// and delegationDenied — the only authorization on this path — was never
// reached. Both preconditions were reachable at once.
func TestUpdateTaskInWorkspace_EmptyPrincipalDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	// A delegation gate that DENIES EVERYTHING: if it is ever consulted the
	// call is denied, so a success here can only mean it was never consulted.
	deps.DelegationDeny = func(context.Context, string, string) *tools.DelegationDenial {
		return &tools.DelegationDenial{
			Reason: "denied", Policy: tools.DenyTrustSet, TargetAgentID: "ray",
		}
	}

	writeTask(t, home, task.Task{
		ID: "01JXNOPRINCIPAL_UPD00001", Title: "Unattributed, assigned to ray",
		Status: task.StatusNext, WorkspaceID: testWorkspaceID, AgentID: "ray",
		// CreatedBy deliberately empty — an agent-created or scheduled task with
		// no human creator (see tools.WithSessionOwner).
	})

	res := systools.NewTaskUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id": "01JXNOPRINCIPAL_UPD00001", "status": "done",
	})
	require.True(t, res.IsError, "a caller with no principal must not mutate another agent's task")
	assert.True(t,
		strings.Contains(res.ForLLM, "cannot resolve the calling agent") ||
			strings.Contains(res.ForLLM, "delegation_denied"),
		"expected a principal or delegation rejection, got: %s", res.ForLLM)

	store := task.New(filepath.Join(home, "tasks"))
	got, err := store.Get("01JXNOPRINCIPAL_UPD00001")
	require.NoError(t, err)
	assert.Equal(t, task.StatusNext, got.Status, "a denied update must not reach disk")
}

// TestDeleteTaskInWorkspace_EmptyPrincipalDenied is the delete-path twin.
func TestDeleteTaskInWorkspace_EmptyPrincipalDenied(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = func(context.Context, string, string) *tools.DelegationDenial {
		return &tools.DelegationDenial{
			Reason: "denied", Policy: tools.DenyTrustSet, TargetAgentID: "ray",
		}
	}

	writeTask(t, home, task.Task{
		ID: "01JXNOPRINCIPAL_DEL00001", Title: "Unattributed, assigned to ray",
		Status: task.StatusNext, WorkspaceID: testWorkspaceID, AgentID: "ray",
	})

	res := systools.NewTaskDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id": "01JXNOPRINCIPAL_DEL00001", "confirm": true,
	})
	require.True(t, res.IsError, "a caller with no principal must not delete another agent's task")

	_, statErr := os.Stat(filepath.Join(home, "tasks", "01JXNOPRINCIPAL_DEL00001.json"))
	assert.NoError(t, statErr, "a denied delete must not remove the task file")
}

// TestUpdateTaskInWorkspace_AgentCreatorStillAllowed proves the legitimate
// creator case survives the namespace fix: an agent that created a task through
// the AGENT path (which stamps the agent-id-namespaced CreatedByAgentID) can
// still mutate it even when it is assigned to an agent it may not delegate to.
func TestUpdateTaskInWorkspace_AgentCreatorStillAllowed(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("ray")

	writeTask(t, home, task.Task{
		ID: "01JXCREATOR_ALLOWED00001", Title: "Dispatched by jim", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "ray", CreatedByAgentID: "jim",
	})

	res := systools.NewTaskUpdateTool(deps).Execute(callerCtx("jim"), map[string]any{
		"id": "01JXCREATOR_ALLOWED00001", "status": "in_progress",
	})
	require.False(t, res.IsError, "the dispatching agent must still be able to update: %s", res.ForLLM)
}

// TestDeleteTaskInWorkspace_AgentCreatorStillAllowed is the delete-path twin of
// the update test above. The ownership union gate at pkg/sysagent/tools/task.go
// (the delete tool's `existing.AgentID != "" && existing.AgentID != caller &&
// !existing.CreatedByAgent(caller)` predicate) shares the exact same
// task.Task.CreatedByAgent helper as the update path, so the legitimate creator
// case must survive on delete too: an agent that dispatched a task through the
// AGENT path can delete it even when delegation policy denies it access to the
// assignee. Mirrors pkg/tools TestTaskUpdate_AgentCreatorAllowed /
// TestTaskDelete_AgentCreatorAllowed (task_scope_test.go) for the privileged
// cross-workspace surface.
func TestDeleteTaskInWorkspace_AgentCreatorStillAllowed(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	seedWorkspace(t, home, testWorkspaceID)
	deps.DelegationDeny = denyDelegationTo("ray")

	writeTask(t, home, task.Task{
		ID: "01JXCREATOR_DEL_ALLOWED1", Title: "Dispatched by jim", Status: task.StatusNext,
		WorkspaceID: testWorkspaceID, AgentID: "ray", CreatedByAgentID: "jim",
	})

	res := systools.NewTaskDeleteTool(deps).Execute(callerCtx("jim"), map[string]any{
		"id": "01JXCREATOR_DEL_ALLOWED1", "confirm": true,
	})
	require.False(t, res.IsError, "the dispatching agent must still be able to delete: %s", res.ForLLM)

	_, statErr := os.Stat(filepath.Join(home, "tasks", "01JXCREATOR_DEL_ALLOWED1.json"))
	assert.True(t, os.IsNotExist(statErr),
		"a creator-allowed delete must remove the task file, got statErr=%v", statErr)
}
