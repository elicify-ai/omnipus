// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_tool_wiring_test.go covers ADR-052 Wave 2's "caller-int" slice: the
// single wiring site (wirePlanToolsForAgent, loop.go) that constructs REAL
// create_plan / execute_plan / run_task / inspect_session tool instances
// against real stores, and the late-binding SetPlanStore seam gateway.go's
// boot wiring region calls once the real *plan.Store exists (planStore is
// NOT available yet at the FIRST registerSharedTools pass, which runs
// inside NewAgentLoop, before setupAndStartServices constructs it).
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newPlanToolWiringTestLoop builds a minimal AgentLoop carrying one
// chat-target agent ("planner-agent", Type: Custom — NOT Worker/System, so
// it passes validatePlanOwnerAgentForTool's IsChatTarget gate) suitable for
// exercising create_plan/execute_plan/run_task/inspect_session end to end.
// cfg.Agents.Defaults.Home is deliberately anchored under `home` (rather
// than an unrelated t.TempDir(), as newGoalLoopTestLoop in judge_test.go
// uses) so that al.homePath (filepath.Dir(cfg.AgentHomeBasePath())) equals
// `home`, which is ALSO what omnipusHome() resolves to once
// config.EnvHome is set — the same directory
// ensureTestWorkspaceMembership (test_helpers_test.go) seeds
// testHarnessWorkspaceMembershipID under. This lets create_plan's
// SetHome-driven workspace resolution find that real, seeded workspace.
func newPlanToolWiringTestLoop(t *testing.T) (*AgentLoop, *AgentInstance, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: filepath.Join(home, "agents"), ModelName: "test-model"},
			List: []config.AgentConfig{
				{
					ID: "planner-agent", Name: "Planner", Type: config.AgentTypeCustom,
					Home: filepath.Join(home, "agents", "planner-agent"),
				},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	agentInst, ok := al.GetRegistry().GetAgent("planner-agent")
	if !ok {
		t.Fatal("planner-agent not registered")
	}
	return al, agentInst, home
}

// TestWirePlanTools_ExecutePlanFailsClosedBeforeSetPlanStore proves the
// DI seam really is unwired (fail-closed, never a silent no-op) until
// SetPlanStore installs the real store — the exact gap Wave 1 left and
// this wave's single wiring site (wirePlanToolsForAgent) closes.
func TestWirePlanTools_ExecutePlanFailsClosedBeforeSetPlanStore(t *testing.T) {
	al, agentInst, _ := newPlanToolWiringTestLoop(t)

	if al.GetPlanStore() != nil {
		t.Fatal("plan store must be nil before SetPlanStore is called")
	}

	result := agentInst.Tools.Execute(context.Background(), "execute_plan", map[string]any{"plan_id": "does-not-matter"})
	if result == nil || !result.IsError {
		t.Fatalf("execute_plan before SetPlanStore must fail closed, got %+v", result)
	}
	if !strings.Contains(result.ForLLM, "plan store is not available") {
		t.Errorf("expected the nil-store fail-closed message, got %q", result.ForLLM)
	}

	createResult := agentInst.Tools.Execute(context.Background(), "create_plan", map[string]any{
		"title": "x", "owner_agent_id": "planner-agent",
		"dod": []any{map[string]any{"kind": "prose", "text": "done"}},
	})
	if createResult == nil || !createResult.IsError {
		t.Fatalf("create_plan before SetPlanStore must fail closed, got %+v", createResult)
	}
	if !strings.Contains(createResult.ForLLM, "plan store is not available") {
		t.Errorf("expected the nil-store fail-closed message, got %q", createResult.ForLLM)
	}
}

// TestWirePlanTools_SetPlanStoreWiresRealStoreForRegisteredAgent proves
// SetPlanStore re-wires an ALREADY-registered agent's create_plan tool with
// the real store (mirrors SetPlanEngine/SetMediaStore's late-binding
// discipline) — the gateway.go boot call happens strictly AFTER
// NewAgentLoop (and therefore after registerSharedTools's first pass) has
// already run.
func TestWirePlanTools_SetPlanStoreWiresRealStoreForRegisteredAgent(t *testing.T) {
	al, agentInst, home := newPlanToolWiringTestLoop(t)

	planStore := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(planStore)
	if al.GetPlanStore() != planStore {
		t.Fatal("SetPlanStore did not install the store on AgentLoop")
	}

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	result := agentInst.Tools.Execute(ctx, "create_plan", map[string]any{
		"title": "Ship the thing", "owner_agent_id": "planner-agent",
		"workspace": testHarnessWorkspaceMembershipID,
		"dod":       []any{map[string]any{"kind": "prose", "text": "the thing ships"}},
	})
	if result == nil || result.IsError {
		t.Fatalf("create_plan after SetPlanStore must succeed against the real store, got %+v", result)
	}

	var payload struct {
		PlanID string `json:"plan_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &payload); err != nil {
		t.Fatalf("could not parse create_plan result %q: %v", result.ForLLM, err)
	}
	if payload.State != string(plan.StateDraft) {
		t.Fatalf("state = %q, want draft", payload.State)
	}

	// Confirm it is REALLY the same store: reading it back directly through
	// planStore (not through the tool) must find the same plan.
	stored, err := planStore.Get(payload.PlanID)
	if err != nil {
		t.Fatalf("plan %q not found in the real store: %v", payload.PlanID, err)
	}
	if stored.Title != "Ship the thing" {
		t.Errorf("stored.Title = %q, want %q", stored.Title, "Ship the thing")
	}
}

// TestWirePlanTools_RunTaskAndInspectSessionWiredFromFirstPass proves
// run_task (TaskStartNowFunc) and inspect_session (InspectSessionStore) are
// wired with REAL, non-nil dependencies from the very FIRST
// registerSharedTools pass (inside NewAgentLoop) — unlike create_plan/
// execute_plan, neither depends on the gateway's later SetPlanStore call,
// since al.taskStore/al.taskExecutor/al.GetSessionStore() are all already
// populated before registerSharedTools ever runs.
func TestWirePlanTools_RunTaskAndInspectSessionWiredFromFirstPass(t *testing.T) {
	al, agentInst, _ := newPlanToolWiringTestLoop(t)

	// run_task: dispatch a real standalone task and confirm the fail-closed
	// "task executor not wired" message never fires — a session_id must come
	// back, proving TaskStartNowFunc really is al.taskExecutor.StartTaskNow.
	taskStore := GetTaskStore(al)
	tk := &task.Task{
		ID: "t-run-task-wiring", AgentID: "planner-agent", WorkspaceID: testHarnessWorkspaceMembershipID,
		Title: "run_task wiring smoke", Status: task.StatusNext,
	}
	if err := taskStore.Create(tk); err != nil {
		t.Fatalf("create task: %v", err)
	}
	runResult := agentInst.Tools.Execute(context.Background(), "run_task", map[string]any{"task_id": tk.ID})
	if runResult == nil || runResult.IsError {
		t.Fatalf("run_task must dispatch via the real StartTaskNow, got %+v", runResult)
	}
	if strings.Contains(runResult.ForLLM, "not wired") {
		t.Errorf("run_task result still reports an unwired dispatcher: %q", runResult.ForLLM)
	}
	var runPayload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(runResult.ForLLM), &runPayload); err != nil {
		t.Fatalf("could not parse run_task result %q: %v", runResult.ForLLM, err)
	}
	if runPayload.SessionID == "" {
		t.Error("run_task result carries no session_id — dispatch did not really fire")
	}

	// inspect_session: a nil InspectSessionStore always fails with "session
	// store is not available" regardless of scope; confirm that message
	// does NOT fire (proving the real *session.UnifiedStore was wired), by
	// authorizing the run_task session's id via the same engine-set ctx
	// value the verifier turn uses (WithVerifierSessionScope) and reading it
	// back.
	inspectCtx := tools.WithVerifierSessionScope(context.Background(), []string{runPayload.SessionID})
	inspectResult := agentInst.Tools.Execute(inspectCtx, "inspect_session", map[string]any{
		"session_id": runPayload.SessionID,
	})
	if inspectResult == nil {
		t.Fatal("inspect_session returned a nil result")
	}
	if strings.Contains(inspectResult.ForLLM, "session store is not available") {
		t.Errorf("inspect_session reports an unwired store: %q", inspectResult.ForLLM)
	}
}

// TestPlanExecuteWired_CreatePlanAttachMemberExecutePlanEndToEnd is the
// full tool-wiring smoke test: create_plan -> attach a criteria-complete
// member task -> execute_plan, ALL against real stores wired via the
// gateway's exact boot sequence (plan.New + AgentLoop.SetPlanStore), with
// no test-only shortcuts or mocked stores. Proves execute_plan is callable
// end-to-end and drives the SAME single gated draft->approved transition
// the human REST approve path uses (ADR-052 FR-003).
func TestPlanExecuteWired_CreatePlanAttachMemberExecutePlanEndToEnd(t *testing.T) {
	al, agentInst, home := newPlanToolWiringTestLoop(t)

	planStore := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(planStore)

	ctx := tools.WithAgentID(context.Background(), "planner-agent")

	createResult := agentInst.Tools.Execute(ctx, "create_plan", map[string]any{
		"title":          "Ship the thing",
		"owner_agent_id": "planner-agent",
		"workspace":      testHarnessWorkspaceMembershipID,
		"dod":            []any{map[string]any{"kind": "prose", "text": "the thing ships"}},
	})
	if createResult == nil || createResult.IsError {
		t.Fatalf("create_plan failed: %+v", createResult)
	}
	var createPayload struct {
		PlanID string `json:"plan_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal([]byte(createResult.ForLLM), &createPayload); err != nil {
		t.Fatalf("could not parse create_plan result %q: %v", createResult.ForLLM, err)
	}
	if createPayload.State != string(plan.StateDraft) {
		t.Fatalf("create_plan state = %q, want draft", createPayload.State)
	}

	// FR-030: execute_plan on an empty plan (zero members) must be rejected
	// — verify this BEFORE attaching a member, proving the tool's own gate
	// (not merely "no members configured yet") is really running.
	emptyExecResult := agentInst.Tools.Execute(ctx, "execute_plan", map[string]any{"plan_id": createPayload.PlanID})
	if emptyExecResult == nil || !emptyExecResult.IsError {
		t.Fatalf("execute_plan on a zero-member plan must be rejected, got %+v", emptyExecResult)
	}
	if unchanged, err := planStore.Get(createPayload.PlanID); err != nil || unchanged.State != plan.StateDraft {
		t.Fatalf("plan must remain draft after the empty-plan rejection, got state=%v err=%v", unchanged, err)
	}

	// Attach a member task carrying an acceptance criterion directly via
	// the real task store (create_task's own plan_id/blocked_by linkage is
	// a DIFFERENT wave's slice — FR-002 — this test only needs a
	// criteria-complete member so execute_plan's FR-004 gate passes).
	taskStore := GetTaskStore(al)
	member := &task.Task{
		ID: "member-1", Title: "do the thing", AgentID: "planner-agent",
		WorkspaceID: testHarnessWorkspaceMembershipID, PlanID: createPayload.PlanID,
		Status:   task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "did it")},
	}
	if err := taskStore.Create(member); err != nil {
		t.Fatalf("create member task: %v", err)
	}

	execResult := agentInst.Tools.Execute(ctx, "execute_plan", map[string]any{"plan_id": createPayload.PlanID})
	if execResult == nil || execResult.IsError {
		t.Fatalf("execute_plan failed: %+v", execResult)
	}

	updated, err := planStore.Get(createPayload.PlanID)
	if err != nil {
		t.Fatalf("could not re-read plan: %v", err)
	}
	if updated.State != plan.StateApproved {
		t.Fatalf("plan state after execute_plan = %q, want %q (this test's minimal harness runs no "+
			"PlanEngine, so approved->running promotion is out of scope here)", updated.State, plan.StateApproved)
	}

	// A second execute_plan call on the now-approved plan must be an
	// idempotent no-op (ADR-052 spec edge case), not an error.
	idempotentResult := agentInst.Tools.Execute(ctx, "execute_plan", map[string]any{"plan_id": createPayload.PlanID})
	if idempotentResult == nil || idempotentResult.IsError {
		t.Fatalf("re-calling execute_plan on an already-approved plan must be an idempotent no-op, got %+v", idempotentResult)
	}
}

// TestPlanExecuteWired_RejectsMemberWithNoAcceptanceCriteria proves FR-004:
// execute_plan enforces the SAME per-member-criteria gate the human
// POST /approve path enforces (rest_plans.go, out of this wave's scope) —
// a member with zero acceptance criteria blocks execution and the plan
// stays draft.
func TestPlanExecuteWired_RejectsMemberWithNoAcceptanceCriteria(t *testing.T) {
	al, agentInst, home := newPlanToolWiringTestLoop(t)

	planStore := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(planStore)
	ctx := tools.WithAgentID(context.Background(), "planner-agent")

	createResult := agentInst.Tools.Execute(ctx, "create_plan", map[string]any{
		"title":          "Ship the thing",
		"owner_agent_id": "planner-agent",
		"workspace":      testHarnessWorkspaceMembershipID,
		"dod":            []any{map[string]any{"kind": "prose", "text": "the thing ships"}},
	})
	if createResult == nil || createResult.IsError {
		t.Fatalf("create_plan failed: %+v", createResult)
	}
	var createPayload struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal([]byte(createResult.ForLLM), &createPayload); err != nil {
		t.Fatalf("could not parse create_plan result: %v", err)
	}

	taskStore := GetTaskStore(al)
	member := &task.Task{
		ID: "member-no-criteria", Title: "do the thing", AgentID: "planner-agent",
		WorkspaceID: testHarnessWorkspaceMembershipID, PlanID: createPayload.PlanID,
		Status: task.StatusNext, // deliberately NO Criteria
	}
	if err := taskStore.Create(member); err != nil {
		t.Fatalf("create member task: %v", err)
	}

	execResult := agentInst.Tools.Execute(ctx, "execute_plan", map[string]any{"plan_id": createPayload.PlanID})
	if execResult == nil || !execResult.IsError {
		t.Fatalf("execute_plan must reject a plan whose member has no acceptance criteria, got %+v", execResult)
	}
	if !strings.Contains(execResult.ForLLM, "no acceptance criteria") {
		t.Errorf("expected a task_errors payload naming the criteria gap, got %q", execResult.ForLLM)
	}
	if unchanged, err := planStore.Get(createPayload.PlanID); err != nil || unchanged.State != plan.StateDraft {
		t.Fatalf("plan must remain draft after the criteria-gate rejection, got state=%v err=%v", unchanged, err)
	}
}
