// plan_engine_play_test.go: tests for play a plan — admit it, pick the next runnable member and dispatch it (dispatch pass, promotions, stall diagnosis, Play/resume)

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// --- moved from plan_engine.go tests 2026-09-15 ---

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

// TestDispatchReadyMembers_BypassesRedundantPlanGate_EvenWithTaskExecutorPlanStoreUnset
// is DoD item 4: PlanEngine.dispatchReadyMembers' documented bypass
// (executeTaskPlanVerified) must let a legitimately-admitted plan member
// dispatch even when TaskExecutor's OWN plan store is left completely
// unwired — proving the bypass is REAL, not merely inert. Without it, this
// dispatch would fail closed on "no plan store wired" (planForGate's
// contract) despite the plan engine itself having already verified, via ITS
// OWN correctly-wired plan store under planDecisionMu, that the plan permits
// dispatch — exactly the boot-ordering risk executeTaskPlanVerified's doc
// comment describes.
func TestDispatchReadyMembers_BypassesRedundantPlanGate_EvenWithTaskExecutorPlanStoreUnset(t *testing.T) {
	provider := newClaimingWorker(turnClaimMet("verified the change directly"))
	al := newNativeTaskCompletionTestLoop(t, provider)
	// Deliberately do NOT call al.taskExecutor.SetPlanStore — the crux of
	// this test: TaskExecutor's own planStore field stays nil throughout.
	planStore := plan.New(filepath.Join(t.TempDir(), "plans"))
	pe := NewPlanEngine(al, planStore, al.taskStore, al.taskExecutor)

	p := newPlanGateTestPlan(t, planStore, plan.StateRunning)
	tk := newPlanGateTestTask(t, al, p.ID)

	dispatched := pe.dispatchReadyMembers(context.Background(), p.ID, []task.Task{*tk})
	if !dispatched {
		t.Fatal("dispatchReadyMembers reported no dispatch — the plan-verified bypass must let a " +
			"legitimately-admitted member through even when TaskExecutor's own plan store is unset")
	}

	final := waitForCompletionContractTerminal(t, al, tk.ID)
	if final.Status != task.StatusDone {
		t.Fatalf("final status = %q, want %q (result: %s)", final.Status, task.StatusDone, final.Result)
	}
	if calls := provider.turnsStarted(); calls == 0 {
		t.Fatal("provider was never called — dispatchReadyMembers' bypass path did not actually dispatch")
	}
}

// TestDispatchReadyMembers_BypassDoesNotWeakenExecuteTask_ForOtherCallers is a
// negative control proving the bypass is scoped to dispatchReadyMembers
// alone: a DIRECT ExecuteTask call for the SAME task, via the SAME
// TaskExecutor (whose own plan store is still unset), is refused exactly as
// requirePlanExecuting's fail-closed "no plan store wired" contract demands
// — the bypass is a documented, narrow exception (executeTaskPlanVerified),
// never a general weakening of ExecuteTask's own gate.
func TestDispatchReadyMembers_BypassDoesNotWeakenExecuteTask_ForOtherCallers(t *testing.T) {
	provider := newClaimingWorker(turnClaimMet("verified the change directly"))
	al := newNativeTaskCompletionTestLoop(t, provider)
	// TaskExecutor's own plan store is deliberately left unset, and this task
	// is dispatched via plain ExecuteTask — NOT through the plan engine's
	// bypass.
	tk := newPlanGateTestTask(t, al, "some-plan-id-with-no-store-on-executor")

	err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil)
	if err == nil {
		t.Fatal("ExecuteTask must fail closed when its own plan store is unset for a plan member task")
	}
	if !errors.Is(err, ErrPlanStateUnresolvable) {
		t.Fatalf("ExecuteTask error = %v, want errors.Is(_, ErrPlanStateUnresolvable)", err)
	}

	got, err := al.taskStore.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != task.StatusNext {
		t.Fatalf("status = %q, want unchanged %q — must not dispatch", got.Status, task.StatusNext)
	}
}
