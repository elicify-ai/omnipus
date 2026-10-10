// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Founder ruling 2026-10-10 (session-core spec FR-014/015/016): the delegation
// policy always applies. An AGENT-initiated run_task starts the task's assignee,
// so it needs a caller->assignee edge in the governing workspace graph, or the
// agent runs its own task. A start with no calling agent (the scheduler, a
// person in the UI) is not delegation and needs no edge; the distinction is the
// entry point, never "the stored creator happens to be empty".
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const (
	rtCaller   = "rt-caller"
	rtAssignee = "rt-assignee"
)

// newRunTaskPolicyLoop builds a loop with two ordinary agents over a workspace
// graph holding exactly edges (the default workspace, so an unbound agent turn
// resolves it), and returns the caller's agent instance.
func newRunTaskPolicyLoop(t *testing.T, edges []graphEdge) (*AgentLoop, *AgentInstance) {
	t.Helper()
	return newRunTaskPolicyLoopAt(t, seedWorkspaceGraph(t, testWS, true, edges))
}

// newRunTaskPolicyLoopAt is newRunTaskPolicyLoop over an already-seeded home.
func newRunTaskPolicyLoopAt(t *testing.T, home string) (*AgentLoop, *AgentInstance) {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: filepath.Join(home, "agents"), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: rtCaller, Name: "Caller", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", rtCaller)},
				{ID: rtAssignee, Name: "Assignee", Type: config.AgentTypeCustom, Home: filepath.Join(home, "agents", rtAssignee)},
			},
		},
	}
	cfg.Sandbox.ToolPolicies = map[string]string{"goal_claim": "allow"}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })
	inst, ok := al.GetRegistry().GetAgent(rtCaller)
	if !ok {
		t.Fatal("caller agent not registered")
	}
	return al, inst
}

func createRunnableTask(t *testing.T, al *AgentLoop, id, assignee string) *task.Task {
	t.Helper()
	tk := &task.Task{
		ID: id, AgentID: assignee, WorkspaceID: testWS, Title: "policy " + id,
		Status: task.StatusNext, // OriginSessionID deliberately empty: the stored creator is unknown
	}
	if err := GetTaskStore(al).Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	return tk
}

func runTaskAs(inst *AgentInstance, taskID string) (isErr bool, out string) {
	res := inst.Tools.Execute(context.Background(), "run_task", map[string]any{"task_id": taskID})
	if res == nil {
		return true, "<nil result>"
	}
	return res.IsError, res.ForLLM
}

func assertNotStarted(t *testing.T, al *AgentLoop, taskID string) {
	t.Helper()
	got, err := GetTaskStore(al).Get(taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got.Status != task.StatusNext || got.SessionID != "" {
		t.Fatalf("refused run_task must leave the task untouched, got status=%q session=%q", got.Status, got.SessionID)
	}
}

// An agent with no caller->assignee edge cannot start another agent's task,
// even though the task's stored creator is empty (which selects an
// ordinary-root launch that never consulted the graph).
func TestRunTask_AgentWithoutEdge_Refused(t *testing.T) {
	al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)}) // reverse edge only
	tk := createRunnableTask(t, al, "t-rt-noedge", rtAssignee)

	isErr, out := runTaskAs(caller, tk.ID)
	if !isErr || !strings.Contains(out, "trust_set") {
		t.Fatalf("run_task without an edge = (err=%v) %q; want a delegation denial", isErr, out)
	}
	assertNotStarted(t, al, tk.ID)
}

// The same agent with the edge is allowed.
func TestRunTask_AgentWithEdge_Allowed(t *testing.T) {
	al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	tk := createRunnableTask(t, al, "t-rt-edge", rtAssignee)

	isErr, out := runTaskAs(caller, tk.ID)
	if isErr {
		t.Fatalf("run_task with a caller->assignee edge refused: %s", out)
	}
	var p struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(out), &p); err != nil || p.SessionID == "" {
		t.Fatalf("run_task with an edge returned no session (%v): %s", err, out)
	}
}

// An agent runs its own task without any edge.
func TestRunTask_OwnTask_Allowed(t *testing.T) {
	al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
	tk := createRunnableTask(t, al, "t-rt-own", rtCaller)

	isErr, out := runTaskAs(caller, tk.ID)
	if isErr {
		t.Fatalf("an agent's own task must run without an edge: %s", out)
	}
}

// A start with no calling agent (a person in the UI via StartTaskNow, the
// scheduler via ExecuteTask) is not delegation and needs no edge.
func TestRunTask_StartWithoutCallingAgent_Allowed(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})
	ui := createRunnableTask(t, al, "t-rt-ui", rtAssignee)
	sess, err := al.taskExecutor.StartTaskNow(context.Background(), ui.ID)
	if err != nil || sess == "" {
		t.Fatalf("a person's start (StartTaskNow) with no edge = (%q, %v); want allowed", sess, err)
	}

	sched := createRunnableTask(t, al, "t-rt-sched", rtAssignee)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := al.taskExecutor.ExecuteTask(ctx, sched.ID, nil); err != nil {
		t.Fatalf("a scheduler start (ExecuteTask) with no edge refused: %v", err)
	}
}

// A stale or missing creator does not turn the check off: removing the edge
// after creation, with the creator unset, still refuses the agent start.
func TestRunTask_EmptyCreator_EdgeRemoved_Refused(t *testing.T) {
	home := seedWorkspaceGraph(t, testWS, true, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	al, caller := newRunTaskPolicyLoopAt(t, home)
	tk := createRunnableTask(t, al, "t-rt-removed", rtAssignee)
	rewriteWorkspaceGraph(t, home, testWS, true, []graphEdge{edge(rtAssignee, rtCaller, nil, nil)})

	isErr, out := runTaskAs(caller, tk.ID)
	if !isErr || !strings.Contains(out, "trust_set") {
		t.Fatalf("run_task after the edge was removed = (err=%v) %q; want a delegation denial", isErr, out)
	}
	assertNotStarted(t, al, tk.ID)
}
