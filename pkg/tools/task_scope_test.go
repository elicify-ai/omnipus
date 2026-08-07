package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// These tests assert OUTCOMES, not mechanisms: what a caller can actually see
// and actually mutate. Each one is written so that reverting the corresponding
// production change turns it red.

// listTasksEnvelope is the shape list_tasks returns. Declared here rather than
// reusing the production type so a test cannot pass merely because the
// production struct changed shape underneath it.
type listTasksEnvelope struct {
	Tasks           []map[string]any `json:"tasks"`
	WorkspaceScoped bool             `json:"workspace_scoped"`
	Matched         int              `json:"matched"`
	Returned        int              `json:"returned"`
	Truncated       bool             `json:"truncated"`
}

func runListTasks(t *testing.T, tool *TaskListTool, ctx context.Context, role string) listTasksEnvelope {
	t.Helper()
	res := tool.Execute(ctx, map[string]any{"role": role})
	if res.IsError {
		t.Fatalf("list_tasks(role=%s): %s", role, res.ForLLM)
	}
	var env listTasksEnvelope
	if err := json.Unmarshal([]byte(res.ForLLM), &env); err != nil {
		t.Fatalf("list_tasks(role=%s): unmarshal %q: %v", role, res.ForLLM, err)
	}
	return env
}

// TestListTasks_ProjectionOmitsDiskOnlyFields proves the response is an
// allowlist PROJECTION, not a marshal of the on-disk task.Task.
//
// task.Task documents four fields as DISK-ONLY — CreatedByAgentID ("the REST
// mapper does NOT copy it to the wire type, and it MUST NOT be added to any
// schema in contracts/"), Scratchpad, PendingJudgeClaim and DelegationDepth —
// and marshalling the whole struct shipped every one of them into the model's
// context.
func TestListTasks_ProjectionOmitsDiskOnlyFields(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	tk := &task.Task{
		Title:             "PROJECTED",
		Action:            task.ActionLLM,
		Status:            task.StatusNext,
		WorkspaceID:       "ws-1",
		AgentID:           "mia",
		Scratchpad:        true,
		PendingJudgeClaim: "SCRATCHPAD-CLAIM-SECRET",
		DelegationDepth:   7,
	}
	if err := store.CreateByAgent(tk, "mia"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := NewTaskListTool(store)
	res := tool.Execute(WithAgentID(context.Background(), "mia"), map[string]any{"role": "assignee"})
	if res.IsError {
		t.Fatalf("list_tasks: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "PROJECTED") {
		t.Fatalf("the seeded task did not come back at all; the rest of this test proves nothing: %s", res.ForLLM)
	}

	for _, forbidden := range []string{
		"scratchpad", "pending_judge_claim", "created_by_agent_id", "delegation_depth",
		"SCRATCHPAD-CLAIM-SECRET",
	} {
		if strings.Contains(res.ForLLM, forbidden) {
			t.Errorf("disk-only field %q crossed the tool boundary: %s", forbidden, res.ForLLM)
		}
	}
}

// TestListTasks_BoundedAndReportsTruncation proves the response is bounded AND
// that a bounded response says so — a short list that looks complete is worse
// than a long one.
func TestListTasks_BoundedAndReportsTruncation(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	const seeded = maxTaskListRows + 5
	for i := 0; i < seeded; i++ {
		tk := &task.Task{
			Title:       fmt.Sprintf("BULK-%03d", i),
			Action:      task.ActionLLM,
			Status:      task.StatusNext,
			WorkspaceID: "ws-1",
			AgentID:     "mia",
		}
		if err := store.CreateByAgent(tk, "mia"); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	env := runListTasks(t, NewTaskListTool(store), WithAgentID(context.Background(), "mia"), "assignee")
	if env.Returned != maxTaskListRows || len(env.Tasks) != maxTaskListRows {
		t.Errorf("expected the response bounded to %d rows, got returned=%d len=%d",
			maxTaskListRows, env.Returned, len(env.Tasks))
	}
	if env.Matched != seeded {
		t.Errorf("expected matched=%d (the true population), got %d", seeded, env.Matched)
	}
	if !env.Truncated {
		t.Error("a truncated response must say so; truncated=false would let a caller read a partial " +
			"list as the complete one")
	}
}

// TestListTasks_WorkspaceScoped proves a workspace-bound turn sees only that
// workspace's tasks, and that the response reports which of the two regimes
// produced it.
func TestListTasks_WorkspaceScoped(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())
	for _, ws := range []string{"ws-1", "ws-2"} {
		tk := &task.Task{
			Title: "IN-" + ws, Action: task.ActionLLM, Status: task.StatusNext,
			WorkspaceID: ws, AgentID: "mia",
		}
		if err := store.CreateByAgent(tk, "mia"); err != nil {
			t.Fatalf("seed %s: %v", ws, err)
		}
	}
	tool := NewTaskListTool(store)

	scoped := runListTasks(t, tool,
		WithWorkspaceID(WithAgentID(context.Background(), "mia"), "ws-1"), "assignee")
	if !scoped.WorkspaceScoped {
		t.Error("a turn carrying a workspace must report workspace_scoped=true")
	}
	if scoped.Returned != 1 {
		t.Fatalf("expected only ws-1's task, got %d rows: %+v", scoped.Returned, scoped.Tasks)
	}
	if got := scoped.Tasks[0]["title"]; got != "IN-ws-1" {
		t.Errorf("workspace scoping returned the wrong workspace's task: %v", got)
	}

	// No workspace on the turn is a legitimate state: widen, but say so.
	wide := runListTasks(t, tool, WithAgentID(context.Background(), "mia"), "assignee")
	if wide.WorkspaceScoped {
		t.Error("a turn with no workspace must report workspace_scoped=false, not present a " +
			"cross-workspace list as a scoped one")
	}
	if wide.Returned != 2 {
		t.Errorf("expected both workspaces' tasks when unscoped, got %d", wide.Returned)
	}
}

// TestTaskDelete_MixedNamespaceCreatedByGrantsNothing proves the namespace
// collision is closed: a task whose CreatedBy holds a human USERNAME is not
// deletable by the AGENT that happens to share that id.
//
// This is the reachable case, not a contrived one: pkg/gateway/rest_tasks.go
// writes c.Username into CreatedBy, and the base roster ids (mia/jim/ava/ray)
// are all plausible usernames.
func TestTaskDelete_MixedNamespaceCreatedByGrantsNothing(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	// Exactly what the REST path writes: a username in Owner/CreatedBy, no
	// agent attribution, and no assignee.
	human := &task.Task{
		Title: "HUMAN-PRIVATE", Action: task.ActionLLM, Status: task.StatusNext,
		WorkspaceID: "ws-1", Owner: "jim", CreatedBy: "jim",
	}
	if err := store.Create(human); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := NewTaskDeleteTool(store)
	res := tool.Execute(WithAgentID(context.Background(), "jim"),
		map[string]any{"task_id": human.ID})
	if !res.IsError {
		t.Fatal("agent \"jim\" deleted a task created by the HUMAN username \"jim\" — the gate is " +
			"still comparing an agent id against the mixed-namespace created_by")
	}
	if _, err := store.Get(human.ID); err != nil {
		t.Errorf("the human's task must survive a denied delete, got: %v", err)
	}
}

// TestTaskDelete_AgentCreatorAllowed proves the legitimate creator case still
// works: an agent that created a task through the AGENT path (which stamps the
// agent-id-namespaced CreatedByAgentID) can still delete it even though it is
// assigned to somebody else.
func TestTaskDelete_AgentCreatorAllowed(t *testing.T) {
	t.Parallel()
	store := task.New(t.TempDir())

	tk := &task.Task{
		Title: "DELEGATED", Action: task.ActionLLM, Status: task.StatusNext,
		WorkspaceID: "ws-1", AgentID: "agent-owner",
	}
	if err := store.CreateByAgent(tk, "creator-a"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	tool := NewTaskDeleteTool(store)
	res := tool.Execute(WithAgentID(context.Background(), "creator-a"),
		map[string]any{"task_id": tk.ID})
	if res.IsError {
		t.Fatalf("the agent that created the task must still be able to delete it: %s", res.ForLLM)
	}
}
