// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_supervision_tool_wiring_test.go covers the ADR-055 wiring slice:
// wirePlanToolsForAgent (loop.go) constructing REAL plan_correct / stop_plan
// instances whose engine hooks resolve the PlanEngine LATE, and
// wireJobRosterForAgent registering list_jobs over live store adapters.
//
// Every test here asserts an OUTCOME — what the plan store actually contains
// afterwards, and which error text actually came back — never merely that a
// setter was called. That distinction is the point: a hook installed with a
// captured-nil engine and a hook not installed at all BOTH produce "an error",
// and only one of them is a wiring bug. The tests separate them by asserting
// WHICH fail-closed message fires.
package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// newSupervisionWiringLoop builds the standard plan-tool wiring harness with a
// real plan store installed via the gateway's own late-binding call
// (SetPlanStore), returning the loop, the agent whose registry we execute
// against, and the store to assert against directly.
func newSupervisionWiringLoop(t *testing.T) (*AgentLoop, *AgentInstance, *plan.Store) {
	t.Helper()
	al, agentInst, home := newPlanToolWiringTestLoop(t)
	planStore := plan.New(filepath.Join(home, "plans"))
	al.SetPlanStore(planStore)
	return al, agentInst, planStore
}

// installRealPlanEngine wires a real PlanEngine through the SAME setter the
// gateway calls (SetPlanEngine), which runs strictly AFTER SetPlanStore in
// boot order and does NOT re-run wirePlanToolsForAgent. A tool that still
// works after this ordering is one whose hook resolved the engine late.
func installRealPlanEngine(t *testing.T, al *AgentLoop, planStore *plan.Store) *PlanEngine {
	t.Helper()
	pe := NewPlanEngine(al, planStore, GetTaskStore(al), nil)
	al.SetPlanEngine(pe)
	return pe
}

// --- 1. Unwired must REFUSE, and must refuse from the wired hook ----------

// TestWireSupervisionTools_StopPlanRefusesWhenEngineUnwired proves the
// containment control fails closed when no engine is installed, AND that it
// does so from the hook this wiring installed rather than from the tool's
// own "no hook at all" branch.
//
// The two are deliberately distinguished. tools.PlanStopTool reports
// "the plan engine is not wired (configuration error)" when SetStopPlan was
// never called; the closure wirePlanToolsForAgent installs reports "plan
// engine is not installed". Asserting the SECOND message and the ABSENCE of
// the first is what makes this a wiring test: if the wiring silently stopped
// happening, this test fails instead of passing on a superficially identical
// error.
func TestWireSupervisionTools_StopPlanRefusesWhenEngineUnwired(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)

	if GetPlanEngine(al) != nil {
		t.Fatal("precondition: no plan engine must be installed yet")
	}
	mustCreateRunningPlan(t, planStore, "p-unwired", "planner-agent")

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "stop_plan", map[string]any{"plan_id": "p-unwired"})

	if res == nil || !res.IsError {
		t.Fatalf("stop_plan with no engine must refuse, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "plan engine is not installed") {
		t.Errorf("expected the WIRED hook's fail-closed message, got %q", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not wired (configuration error)") {
		t.Errorf("stop_plan reports NO hook installed — wirePlanToolsForAgent did not call "+
			"SetStopPlan: %q", res.ForLLM)
	}

	// The refusal must be a refusal, not a silent no-op reported as one: the
	// plan is still running.
	after, err := planStore.Get("p-unwired")
	if err != nil {
		t.Fatalf("re-read plan: %v", err)
	}
	if after.State != plan.StateRunning {
		t.Errorf("plan state = %q, want running — a refused stop must not mutate the plan", after.State)
	}
}

// TestWireSupervisionTools_PlanCorrectRefusesWhenEngineUnwired is the
// plan_correct half of the same property. It runs AS the PlanSupervisor so the
// identity gate is passed and the engine hook is genuinely the thing under
// test.
func TestWireSupervisionTools_PlanCorrectRefusesWhenEngineUnwired(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)

	if GetPlanEngine(al) != nil {
		t.Fatal("precondition: no plan engine must be installed yet")
	}
	mustCreatePlan(t, planStore, &plan.Plan{
		ID: "p-correct-unwired", Title: "p-correct-unwired", WorkspaceID: "ws",
		OwnerAgentID: "planner-agent", State: plan.StateRunning,
		PlanPhase:     plan.PhaseAwaitingSupervision,
		SourceChannel: "telegram", SourceChatID: "chat-1",
	})

	ctx := tools.WithAgentID(context.Background(), tools.PlanSupervisorAgentID)
	res := agentInst.Tools.Execute(ctx, "plan_correct", map[string]any{
		"plan_id":              "p-correct-unwired",
		"verb":                 string(plan.RevisionAppend),
		"falsified_assumption": "the fixture was reachable",
		"tail_members": []any{map[string]any{
			"title":    "retry the fetch",
			"criteria": []any{map[string]any{"kind": "prose", "text": "the fetch succeeds"}},
		}},
	})

	if res == nil || !res.IsError {
		t.Fatalf("plan_correct with no engine must refuse, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "plan engine is not installed") {
		t.Errorf("expected the WIRED hook's fail-closed message, got %q", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "not wired (configuration error)") {
		t.Errorf("plan_correct reports NO hook installed — wirePlanToolsForAgent did not call "+
			"SetAppendCorrection: %q", res.ForLLM)
	}
}

// --- 2. A wired stop_plan by the owner really reaches the engine ---------

// TestWireSupervisionTools_StopPlanByOwnerReachesEngine is the positive
// end-to-end proof: the owner's call travels through the wired hook into the
// REAL PlanEngine and terminates the REAL plan.
//
// Three separate outcomes are asserted, because "it returned success" is not
// evidence that the right thing happened:
//   - the TARGET plan is terminal with failed_reason=stopped_by_user
//     (the correct plan id reached the engine);
//   - the handover text names the CALLING AGENT (the correct actor reached
//     the engine — StopPlan renders userID into it);
//   - a second, untouched plan owned by the same agent is STILL RUNNING
//     (the stop was scoped to one plan, not a fan-out).
//
// The engine is installed AFTER the tools were wired, mirroring gateway boot
// order exactly; a hook that captured the engine at wiring time would fail
// this test.
func TestWireSupervisionTools_StopPlanByOwnerReachesEngine(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)

	mustCreateRunningPlan(t, planStore, "p-target", "planner-agent")
	mustCreateRunningPlan(t, planStore, "p-bystander", "planner-agent")

	installRealPlanEngine(t, al, planStore)

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "stop_plan", map[string]any{"plan_id": "p-target"})

	if res == nil || res.IsError {
		t.Fatalf("stop_plan by the owner must succeed against the real engine, got %+v", res)
	}

	target, err := planStore.Get("p-target")
	if err != nil {
		t.Fatalf("re-read target plan: %v", err)
	}
	if target.State != plan.StateFailed {
		t.Errorf("target state = %q, want failed — the stop never reached the engine", target.State)
	}
	if target.FailedReason != plan.FailedReasonStoppedByUser {
		t.Errorf("target failed_reason = %q, want %q",
			target.FailedReason, plan.FailedReasonStoppedByUser)
	}
	// StopPlan renders its userID argument into the handover text. Finding the
	// caller's agent id there proves the ACTOR was threaded through the hook,
	// not defaulted or dropped.
	if !strings.Contains(target.HandoverText, "planner-agent") {
		t.Errorf("handover text %q does not name the calling agent — the actor did not "+
			"reach the engine", target.HandoverText)
	}

	bystander, err := planStore.Get("p-bystander")
	if err != nil {
		t.Fatalf("re-read bystander plan: %v", err)
	}
	if bystander.State != plan.StateRunning {
		t.Errorf("bystander state = %q, want running — stop_plan hit the wrong plan", bystander.State)
	}
}

// TestWireSupervisionTools_StopPlanDeniedForNonOwner proves stop_plan's
// authority really is the plan's OWNER (the inverse of plan_correct's), with a
// real engine wired: a non-owner gets the opaque denial and the plan survives.
func TestWireSupervisionTools_StopPlanDeniedForNonOwner(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)
	mustCreateRunningPlan(t, planStore, "p-owned", "someone-else")
	installRealPlanEngine(t, al, planStore)

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "stop_plan", map[string]any{"plan_id": "p-owned"})

	if res == nil || !res.IsError {
		t.Fatalf("stop_plan by a non-owner must be denied, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "not the owner of a stoppable plan") {
		t.Errorf("expected the opaque owner denial, got %q", res.ForLLM)
	}
	after, err := planStore.Get("p-owned")
	if err != nil {
		t.Fatalf("re-read plan: %v", err)
	}
	if after.State != plan.StateRunning {
		t.Errorf("plan state = %q, want running — a denied stop must not reach the engine", after.State)
	}
}

// --- 3. plan_correct is denied BEFORE the engine for a non-supervisor ----

// TestWireSupervisionTools_PlanCorrectDeniedBeforeEngineForNonSupervisor
// proves plan_correct's authority is PlanSupervisor BY IDENTITY, and that the
// denial happens before the engine is consulted.
//
// "Before the engine" is established by evidence rather than assertion: a real
// PlanEngine IS installed, the plan IS in a correctable state, and the payload
// IS well-formed — so the only thing standing between this call and a mutation
// is the identity gate. The plan coming back byte-identical (same generation,
// same state, same phase) is the proof the engine was never reached.
//
// The plan is owned by the CALLER here, which makes the test strictly
// stronger: even the plan's own owner — the one principal the engine's own
// gate would admit — cannot correct it, because correction authority is the
// adjudicator's alone.
func TestWireSupervisionTools_PlanCorrectDeniedBeforeEngineForNonSupervisor(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)

	mustCreatePlan(t, planStore, &plan.Plan{
		ID: "p-corr", Title: "p-corr", WorkspaceID: "ws",
		OwnerAgentID: "planner-agent", State: plan.StateRunning,
		PlanPhase:     plan.PhaseAwaitingSupervision,
		SourceChannel: "telegram", SourceChatID: "chat-corr",
	})
	installRealPlanEngine(t, al, planStore)

	before, err := planStore.Get("p-corr")
	if err != nil {
		t.Fatalf("read plan: %v", err)
	}

	args := map[string]any{
		"plan_id":              "p-corr",
		"verb":                 string(plan.RevisionAppend),
		"falsified_assumption": "the fixture was reachable",
		"tail_members": []any{map[string]any{
			"title":    "retry the fetch",
			"criteria": []any{map[string]any{"kind": "prose", "text": "the fetch succeeds"}},
		}},
	}

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "plan_correct", args)

	if res == nil || !res.IsError {
		t.Fatalf("plan_correct by a non-supervisor must be denied, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "not permitted to correct plans") {
		t.Errorf("expected the opaque correction denial, got %q", res.ForLLM)
	}

	after, err := planStore.Get("p-corr")
	if err != nil {
		t.Fatalf("re-read plan: %v", err)
	}
	if after.State != before.State || after.EffectivePlanPhase() != before.EffectivePlanPhase() {
		t.Errorf("plan changed (state %q->%q, phase %q->%q) — the denial did not precede the engine",
			before.State, after.State, before.EffectivePlanPhase(), after.EffectivePlanPhase())
	}
	if after.JudgeRounds != before.JudgeRounds {
		t.Errorf("plan judge_rounds %d -> %d — a denied correction reached the engine",
			before.JudgeRounds, after.JudgeRounds)
	}

	// No member task may have been created by the rejected correction.
	members, err := GetTaskStore(al).List(task.Filter{PlanID: "p-corr"})
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("denied correction created %d member task(s) — it reached the engine", len(members))
	}

	// Identity-conditioning: the SAME payload as the PlanSupervisor must NOT
	// produce the denial. (What it produces instead is the subject of
	// TestWireSupervisionTools_PlanCorrectBySupervisorAppliesToTheRealPlan.)
	supervisorRes := agentInst.Tools.Execute(
		tools.WithAgentID(context.Background(), tools.PlanSupervisorAgentID), "plan_correct", args)
	if supervisorRes == nil {
		t.Fatal("plan_correct as the supervisor returned nil")
	}
	if strings.Contains(supervisorRes.ForLLM, "not permitted to correct plans") {
		t.Errorf("the PlanSupervisor was denied by the identity gate — the gate is not "+
			"keyed on tools.PlanSupervisorAgentID (%q): %q",
			tools.PlanSupervisorAgentID, supervisorRes.ForLLM)
	}
}

// TestWireSupervisionTools_PlanCorrectBySupervisorAppliesToTheRealPlan is the
// end-to-end proof that ADR-055's headline feature is REACHABLE: the
// PlanSupervisor issues a correction through the real registered tool, against
// a real PlanEngine and real stores, and the plan's members actually change.
//
// It replaces a test that PINNED THE OPPOSITE — that the correction was
// rejected — because the two authority gates in this path used to be mutually
// exclusive:
//
//	tools.PlanCorrectTool   admitted ONLY caller == tools.PlanSupervisorAgentID
//	PlanEngine.requireOwner admitted ONLY caller == plan.OwnerAgentID
//
// and a System Agent can never be a plan's OwnerAgentID
// (validatePlanOwnerAgentForTool rejects any owner that is not a chat target).
// The admissible sets were disjoint, so plan_correct could not correct ANY
// plan. The engine's gate is now requireCorrectionAuthority, which admits the
// adjudicator by identity — this test is what stops that regressing.
//
// It deliberately asserts the OUTCOME (a new member exists on the plan, the
// plan left the parked phase), not that a gate returned nil: every one of the
// controls that shipped broken on this branch had a passing gate-level test.
func TestWireSupervisionTools_PlanCorrectBySupervisorAppliesToTheRealPlan(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)

	mustCreatePlan(t, planStore, &plan.Plan{
		ID: "p-gate", Title: "p-gate", WorkspaceID: "ws",
		OwnerAgentID: "planner-agent", State: plan.StateRunning,
		PlanPhase:     plan.PhaseAwaitingSupervision,
		SourceChannel: "telegram", SourceChatID: "chat-gate",
	})
	installRealPlanEngine(t, al, planStore)

	ctx := tools.WithAgentID(context.Background(), tools.PlanSupervisorAgentID)
	res := agentInst.Tools.Execute(ctx, "plan_correct", map[string]any{
		"plan_id":              "p-gate",
		"verb":                 string(plan.RevisionAppend),
		"falsified_assumption": "the fixture was reachable",
		"tail_members": []any{map[string]any{
			"title":    "retry the fetch",
			"criteria": []any{map[string]any{"kind": "prose", "text": "the fetch succeeds"}},
		}},
	})
	if res == nil {
		t.Fatal("plan_correct returned nil")
	}
	if res.IsError {
		t.Fatalf("the PlanSupervisor's correction was rejected — the correction loop is unreachable: %q", res.ForLLM)
	}

	// OUTCOME 1: the correction added real work to the real plan.
	members, err := al.taskStore.List(task.Filter{PlanID: "p-gate"})
	if err != nil {
		t.Fatalf("list plan members: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("plan has %d members after the correction; want 1 (the appended tail member)", len(members))
	}
	if members[0].Title != "retry the fetch" {
		t.Errorf("appended member title = %q, want %q", members[0].Title, "retry the fetch")
	}
	if members[0].PlanID != "p-gate" {
		t.Errorf("appended member belongs to plan %q, want p-gate", members[0].PlanID)
	}

	// OUTCOME 2: the plan left the parked phase, so the engine will dispatch
	// the new work rather than sit waiting for another adjudication.
	p, err := planStore.Get("p-gate")
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	if p.EffectivePlanPhase() == plan.PhaseAwaitingSupervision {
		t.Error("plan is still parked awaiting supervision after an applied correction")
	}
}

// TestWireSupervisionTools_PlanCorrectByOwnerIsDeniedEndToEnd is the other
// half of the authority rule (ADR-055 FR-4): the plan's OWNER receives
// outcomes and may stop/resume, but has NO correction role. Fixing the
// supervisor's access must not have opened it to everyone.
func TestWireSupervisionTools_PlanCorrectByOwnerIsDeniedEndToEnd(t *testing.T) {
	al, agentInst, planStore := newSupervisionWiringLoop(t)

	mustCreatePlan(t, planStore, &plan.Plan{
		ID: "p-owner", Title: "p-owner", WorkspaceID: "ws",
		OwnerAgentID: "planner-agent", State: plan.StateRunning,
		PlanPhase:     plan.PhaseAwaitingSupervision,
		SourceChannel: "telegram", SourceChatID: "chat-owner",
	})
	installRealPlanEngine(t, al, planStore)

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "plan_correct", map[string]any{
		"plan_id":              "p-owner",
		"verb":                 string(plan.RevisionAppend),
		"falsified_assumption": "it is my plan",
		"tail_members": []any{map[string]any{
			"title":    "sneak work in",
			"criteria": []any{map[string]any{"kind": "prose", "text": "it lands"}},
		}},
	})
	if res == nil {
		t.Fatal("plan_correct returned nil")
	}
	if !res.IsError {
		t.Fatalf("the plan's OWNER corrected its own plan: %q", res.ForLLM)
	}
	// The denial must leak nothing about who IS authorised.
	if strings.Contains(strings.ToLower(res.ForLLM), tools.PlanSupervisorAgentID) {
		t.Errorf("denial names the authorised holder: %q", res.ForLLM)
	}
	// And it must have changed nothing.
	members, err := al.taskStore.List(task.Filter{PlanID: "p-owner"})
	if err != nil {
		t.Fatalf("list plan members: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("a denied correction created %d member(s)", len(members))
	}
}

// --- 4. list_jobs is registered and reads live stores --------------------

// TestWireJobRoster_ListJobsRegisteredAndReadsLiveStores proves
// wireJobRosterForAgent registered list_jobs and that its store adapters
// resolve the LIVE plan store — the property that matters, because the
// lifecycle store and plan engine are installed after this wiring runs.
//
// The plan created below is owned by the calling agent, so a working plan
// adapter MUST surface it as a row. An adapter that captured a nil store, or
// one that swallowed a missing store into an empty slice, returns no rows and
// fails here.
func TestWireJobRoster_ListJobsRegisteredAndReadsLiveStores(t *testing.T) {
	_, agentInst, planStore := newSupervisionWiringLoop(t)

	if _, ok := agentInst.Tools.Get("list_jobs"); !ok {
		t.Fatal("list_jobs was not registered by wireJobRosterForAgent")
	}
	mustCreateRunningPlan(t, planStore, "p-roster", "planner-agent")

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "list_jobs", map[string]any{"kind": "plan"})
	if res == nil || res.IsError {
		t.Fatalf("list_jobs must succeed, got %+v", res)
	}

	var payload struct {
		Rows []struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		} `json:"rows"`
		CapActive *int `json:"cap_active"`
		CapMax    *int `json:"cap_max"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		t.Fatalf("could not parse list_jobs result %q: %v", res.ForLLM, err)
	}

	var found bool
	for _, r := range payload.Rows {
		if r.Kind == "plan" && r.ID == "p-roster" {
			found = true
		}
	}
	if !found {
		t.Errorf("list_jobs did not surface the caller's running plan — the plan store "+
			"adapter is not reading the live store: %q", res.ForLLM)
	}

	// The cap pair MUST be absent: PlanEngine has no CapSnapshot method, so
	// SetCapSnapshotSource is deliberately left unwired. A value here means
	// something fabricated a snapshot rather than omitting it.
	if payload.CapActive != nil || payload.CapMax != nil {
		t.Errorf("cap fields must be omitted while no CapSnapshot source exists, got "+
			"cap_active=%v cap_max=%v", payload.CapActive, payload.CapMax)
	}
}

// TestWireJobRoster_ListJobsReportsMissingStoreAsError proves the adapters
// fail LOUDLY rather than silently short. Before SetPlanStore runs there is no
// plan store, and the `plan` kind must come back as an explicit per-kind error
// — never as an empty roster, which would be indistinguishable from "you have
// no plans".
func TestWireJobRoster_ListJobsReportsMissingStoreAsError(t *testing.T) {
	al, agentInst, _ := newPlanToolWiringTestLoop(t)
	if al.GetPlanStore() != nil {
		t.Fatal("precondition: plan store must be nil before SetPlanStore")
	}

	ctx := tools.WithAgentID(context.Background(), "planner-agent")
	res := agentInst.Tools.Execute(ctx, "list_jobs", map[string]any{"kind": "plan"})
	if res == nil {
		t.Fatal("list_jobs returned nil")
	}
	if !strings.Contains(res.ForLLM, "plan store is not installed") {
		t.Errorf("a missing plan store must surface as an explicit error, not a silent "+
			"empty roster: %q", res.ForLLM)
	}
}
