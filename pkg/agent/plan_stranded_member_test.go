// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// plan_stranded_member_test.go covers the third stall shape: a plan member
// that is `in_progress` on disk while NOTHING IS EXECUTING IT.
//
// WHY IT EXISTS. planStallReason read `in_progress` as "in flight"
// unconditionally, so the most eternal stall the system can produce was the
// one shape it was guaranteed to miss. That shape is reachable by the task
// run loop's silent-exit paths (task_run_loop.go) — adjudicateRunClaim's
// DoD-unreadable branch, finishRunTurn's claim-read-fault branch and
// consumeTaskAttempt's CAS-conflict branch — each of which ends the run with
// no terminal write and no restart, leaving boot reconciliation as the only
// backstop and no dedicated in-process reaper. For a plan member that is not a backstop: the
// plan renders "Running 5/6" for as long as the process lives.
//
// THE SIGNAL IS BINARY, NOT A TIMER, and that distinction is the whole safety
// argument. The engine asks the TaskExecutor whether it holds a dispatch slot
// for the member. A member that is genuinely working holds its slot for the
// entire run — it is deleted in runTask's OUTERMOST defer, after adjudication
// and after any redispatch — so the 40-minute-but-healthy member the UAT
// recorded is never once observed stranded, no matter how long it takes. The
// dwell (planMemberStrandedGrace) exists ONLY to outlast the narrow
// claim-before-slot and cold-boot windows; TestSlowMemberNeverStalls below is
// the guard that keeps it from drifting into a slowness judgement.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestStrandedMember_SurfacesAsStallInsteadOfRunningForever is the regression
// anchor.
//
// Given a running plan whose only non-terminal member is `in_progress` with no
//
//	run executing it
//
// When the engine keeps processing the plan past the stranded grace
// Then the plan reports a stall naming that member, instead of rendering as
//
//	running indefinitely.
func TestStrandedMember_SurfacesAsStallInsteadOfRunningForever(t *testing.T) {
	h := newTestPlanEngine(t)
	// Nothing is executing anything — the state a run goroutine leaves behind
	// when it exits without writing an outcome.
	h.pe.memberExecuting = func(string) bool { return false }

	mustCreateRunningPlan(t, h.plans, "p-stranded", "owner")
	done := mustCreateTask(t, h.tasks, &task.Task{
		Title: "finished member", WorkspaceID: "ws", PlanID: "p-stranded", Status: task.StatusDone,
	})
	stranded := mustCreateTask(t, h.tasks, &task.Task{
		Title: "wedged member", WorkspaceID: "ws", PlanID: "p-stranded", Status: task.StatusInProgress,
	})
	_ = done

	// First pass: inside the grace. A member observed stranded for the first
	// time must NOT be called stalled — that window is where the legitimate
	// claim-before-slot and cold-boot races live.
	h.pe.processPlan(context.Background(), "p-stranded")
	got, err := h.plans.Get("p-stranded")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanPhase == plan.PhaseStalled {
		t.Fatalf("a member observed stranded for the FIRST time must not be reported stalled yet — "+
			"executeTask writes next->in_progress (ClaimForRun) before inserting its dispatch slot, and a "+
			"fresh boot has an empty slot map, so a single observation is not evidence. phase = %q, note = %q",
			got.PlanPhase, got.HandoverText)
	}

	// Past the grace, still nothing executing it.
	h.clock.Set(h.clock.Now().Add(planMemberStrandedGrace + time.Minute))
	h.pe.processPlan(context.Background(), "p-stranded")

	got, err = h.plans.Get("p-stranded")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanPhase != plan.PhaseStalled {
		t.Fatalf("plan_phase = %q, want %q.\n\n"+
			"A plan whose only non-terminal member is recorded in_progress while NO run is executing it can "+
			"never make progress: the task run loop's silent-exit paths (task_run_loop.go: adjudicateRunClaim's "+
			"DoD-unreadable branch, finishRunTurn's claim-read-fault branch and consumeTaskAttempt's CAS-conflict branch) leave exactly this "+
			"state, with no terminal write and no redispatch. planStallReason must stop counting such a "+
			"member as in-flight — see PlanEngine.memberExecuting. Note was: %q",
			got.PlanPhase, plan.PhaseStalled, got.HandoverText)
	}
	if !strings.HasPrefix(got.HandoverText, stallHandoverNotePrefix) {
		t.Fatalf("HandoverText = %q, want a stall note (prefix %q)", got.HandoverText, stallHandoverNotePrefix)
	}
	if !strings.Contains(got.HandoverText, stranded.ID) {
		t.Fatalf("the stall note must NAME the stranded member so the diagnosis is actionable; "+
			"HandoverText = %q, member id = %q", got.HandoverText, stranded.ID)
	}
	if !strings.Contains(got.HandoverText, "in_progress") {
		t.Fatalf("the stall note must say WHAT is wrong, not just that something is; HandoverText = %q",
			got.HandoverText)
	}

	// It must also CLEAR itself the moment the condition resolves — a
	// diagnosis that cannot be withdrawn would climb the supervision ladder on
	// a member that recovered.
	h.pe.memberExecuting = func(string) bool { return true }
	h.clock.Set(h.clock.Now().Add(time.Minute))
	h.pe.processPlan(context.Background(), "p-stranded")
	got, err = h.plans.Get("p-stranded")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanPhase == plan.PhaseStalled {
		t.Fatalf("once a run is executing the member again the stall must clear; phase = %q, note = %q",
			got.PlanPhase, got.HandoverText)
	}
}

// TestSlowMemberNeverStalls is the false-positive guard, and it is the reason
// the detection is a dispatch-slot test rather than an elapsed-time one.
//
// One real UAT member took 37 minutes and SUCCEEDED. Any rule that reads
// "in_progress for a long time" as "stuck" would have failed that plan. A
// working member holds its dispatch slot for the whole run, so no amount of
// elapsed time produces a single stranded observation.
func TestSlowMemberNeverStalls(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.memberExecuting = func(string) bool { return true } // a real run, still going

	mustCreateRunningPlan(t, h.plans, "p-slow", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "slow but healthy member", WorkspaceID: "ws", PlanID: "p-slow", Status: task.StatusInProgress,
	})

	// Far longer than the 37-minute member the UAT recorded.
	for elapsed := time.Duration(0); elapsed <= 4*time.Hour; elapsed += 10 * time.Minute {
		h.pe.processPlan(context.Background(), "p-slow")
		got, err := h.plans.Get("p-slow")
		if err != nil {
			t.Fatal(err)
		}
		if got.PlanPhase == plan.PhaseStalled {
			t.Fatalf("after %s a member that is STILL EXECUTING was reported stalled (note: %q).\n"+
				"The stranded test must be the executor's dispatch slot, never elapsed time: a real UAT "+
				"member took 37 minutes and succeeded, and a rule that kills it is worse than the wedge "+
				"it is trying to catch.", elapsed, got.HandoverText)
		}
		h.clock.Set(h.clock.Now().Add(10 * time.Minute))
	}
}

// TestStrandedDetectionInertWithoutAnExecutorReader pins the fail-quiet
// default: an engine with no way to ask "is anything running this" must behave
// exactly as it did before the stranded term existed. "Cannot tell" must never
// read as "stranded" — otherwise every struct-literal test engine, and every
// boot that wires no task executor, would start failing healthy plans.
func TestStrandedDetectionInertWithoutAnExecutorReader(t *testing.T) {
	h := newTestPlanEngine(t)
	if h.pe.memberExecuting != nil {
		t.Fatal("arrange: this harness must have no executor reader")
	}

	mustCreateRunningPlan(t, h.plans, "p-unknown", "owner")
	mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p-unknown", Status: task.StatusInProgress,
	})

	for i := 0; i < 5; i++ {
		h.pe.processPlan(context.Background(), "p-unknown")
		h.clock.Set(h.clock.Now().Add(planMemberStrandedGrace))
	}

	got, err := h.plans.Get("p-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanPhase == plan.PhaseStalled {
		t.Fatalf("with no executor reader wired the stranded term must be inert; phase = %q, note = %q",
			got.PlanPhase, got.HandoverText)
	}
}

// TestTaskExecutorHoldsDispatchSlot pins the raw read the whole detection
// rests on, against the REAL map rather than a stand-in: a reserved slot
// (claimed, goroutine not yet launched) and a live slot both count as
// executing, and only an absent entry does not.
func TestTaskExecutorHoldsDispatchSlot(t *testing.T) {
	te := &TaskExecutor{running: map[string]*taskSlot{}}

	if taskExecutorHoldsDispatchSlot(nil, "t1") {
		t.Fatal("a nil executor must not claim to be running anything")
	}
	if taskExecutorHoldsDispatchSlot(te, "") {
		t.Fatal("an empty task id must not match")
	}
	if taskExecutorHoldsDispatchSlot(te, "t1") {
		t.Fatal("an absent entry must read as not executing")
	}

	te.running["t1"] = &taskSlot{reserved: true}
	if !taskExecutorHoldsDispatchSlot(te, "t1") {
		t.Fatal("a RESERVED slot must count as executing — the goroutine is launching, and treating that " +
			"window as stranded is exactly the race the dwell exists to avoid")
	}

	te.running["t1"] = &taskSlot{cancel: func() {}}
	if !taskExecutorHoldsDispatchSlot(te, "t1") {
		t.Fatal("a live slot must count as executing")
	}

	delete(te.running, "t1")
	if taskExecutorHoldsDispatchSlot(te, "t1") {
		t.Fatal("once the slot is deleted (runTask's outermost defer, after adjudication AND after any " +
			"redispatch) the member is no longer executing")
	}
}

// TestStallNoteIsClampedForLikeForLikeDedup covers the handover-clamp
// follow-up: pkg/plan's Store.write clamps handover text unconditionally, so a
// stall writer that builds a RAW over-bound note compares a raw string against
// the clamped value on disk, never matches its own dedup guard, and therefore
// re-writes the plan and re-wakes the supervisor on every tick.
func TestStallNoteIsClampedForLikeForLikeDedup(t *testing.T) {
	huge := strings.Repeat("x", 40000)
	note := stallHandoverNote(huge)

	if note == stallHandoverNotePrefix+huge {
		t.Fatal("stallHandoverNote must clamp; an unclamped note never equals the clamped value the store " +
			"keeps, so the dedup guard in surfaceStallIfAny/surfaceJudgeUnavailableStall never fires and the " +
			"plan re-wakes its supervisor every tick")
	}
	if !strings.HasPrefix(note, stallHandoverNotePrefix) {
		t.Fatalf("the clamp is head-preserving, so the stall prefix must survive it — the stale-note CLEARING "+
			"path matches on that prefix. Got: %.40q", note)
	}
	if got := plan.ClampHandoverText(note); got != note {
		t.Fatal("the clamp must be idempotent: the store clamping again on the way to disk must not produce a " +
			"different value than the one the writer deduped against")
	}
}

// TestWakePromptIsBoundedWithoutBreakingPlanCorrect covers the second half of
// the handover-clamp follow-up: the supervision WAKE PROMPT embeds the same
// unbounded provider/judge text as the persisted note, but never touches the
// plan store, so pkg/plan's write-path clamp does not cover it.
//
// The oracle is deliberately two-sided, because the obvious fix — clamp the
// whole prompt — is a regression. plan.ClampHandoverText is head-preserving
// and every wake builder puts the target block LAST, so a flat clamp cuts off
// `plan_id:` and reproduces ADR-055 fix-wave finding 2: a wake that asks for a
// correction PlanSupervisor is structurally incapable of issuing, while still
// spending an attempt from its budget. So this test requires BOTH that the
// prompt is bounded AND that the adjudicator can still read out of it
// everything plan_correct needs — parsed exactly the way
// TestSupervisionWakes_CarryEverythingPlanCorrectNeeds parses it.
func TestWakePromptIsBoundedWithoutBreakingPlanCorrect(t *testing.T) {
	// A provider error body of the kind that made the persisted note
	// unwritable in the first place.
	verboseProviderError := strings.Repeat("upstream returned 502 with an HTML error page; ", 900)
	targets := buildSupervisionTargetsText("p-wake-clamp", []task.Task{
		{ID: "m-done-1", Status: task.StatusDone, Title: "first member"},
		{ID: "m-failed-1", Status: task.StatusFailed, Title: "second member"},
	})
	prompt := "Plan is stalled: " + verboseProviderError + "\n\n" + targets +
		"\nDiagnose the stall and, if it is correctable, apply a correction."

	clamped := clampWakePrompt(prompt)

	if len([]rune(clamped)) >= len([]rune(prompt)) {
		t.Fatalf("the wake prompt was not bounded at all: %d runes in, %d runes out. The unbounded provider "+
			"text reaches the supervisor's context by this route even though the persisted handover is clamped",
			len([]rune(prompt)), len([]rune(clamped)))
	}
	if got := wakePlanID(clamped); got != "p-wake-clamp" {
		t.Fatalf("the clamped wake names plan_id %q, want %q.\n\n"+
			"plan_id lives in the TAIL (buildSupervisionTargetsText) and ClampHandoverText is "+
			"HEAD-preserving, so clamping the whole prompt deletes the one field plan_correct cannot be "+
			"called without — ADR-055 fix-wave finding 2. Bound the diagnosis, keep the target block "+
			"(clampWakePrompt).", got, "p-wake-clamp")
	}
	if got := wakeMemberID(clamped, string(task.StatusDone)); got != "m-done-1" {
		t.Fatalf("the clamped wake lists no `done` member id (%q) — supersede becomes unissuable", got)
	}
	if got := wakeMemberID(clamped, string(task.StatusFailed)); got != "m-failed-1" {
		t.Fatalf("the clamped wake lists no `failed` member id (%q) — targeted_retry becomes unissuable", got)
	}
	if !strings.Contains(clamped, "Diagnose the stall") {
		t.Fatalf("the instruction text that follows the target block must survive too; got tail: %.200q",
			clamped[max(0, len(clamped)-200):])
	}

	// A prompt that already fits must pass through untouched — the clamp is a
	// ceiling, not a reformatter.
	small := "short diagnosis\n\n" + targets
	if got := clampWakePrompt(small); got != small {
		t.Fatalf("an in-bounds prompt must be returned verbatim; got %q", got)
	}
}
