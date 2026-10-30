package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// errPlanEngineStopped is returned by every engine plan/task write and member
// or plan-turn dispatch refused because Stop has begun on a started engine.
// The next Start reopens the gate.
var errPlanEngineStopped = errors.New("plan_engine: stopped; plan and task writes are refused until the next Start")

// planEngineWriteGate fences the engine's durable writes and dispatches
// against Stop. A pass of Tick or processPlan that is already running when
// Stop begins cannot be interrupted (it runs on context.Background), and the
// gateway's shutdown stops waiting for Stop once its budget is spent. Without
// this gate such a pass kept promoting, dispatching and writing plan state
// after the gateway had torn down what it writes to.
//
// halt refuses every later enter at once, then waits for the writes already
// inside the gate: each holds it for one store call or one dispatch
// admission, never for a whole pass. A refused enter returns
// errPlanEngineStopped to the caller's existing error path.
//
// The zero value is open, so a never-started struct-literal engine behaves as
// before. An enter nested inside another (a dispatch whose admission writes
// through the engine) is counted again rather than locked twice, so it cannot
// deadlock against a waiting halt; once halted it is refused like any other.
type planEngineWriteGate struct {
	mu      sync.Mutex
	drained *sync.Cond
	halted  bool
	active  int
}

func (g *planEngineWriteGate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.halted {
		return false
	}
	g.active++
	return true
}

func (g *planEngineWriteGate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.active--
	if g.active == 0 && g.drained != nil {
		g.drained.Broadcast()
	}
}

// close refuses every later enter. It does not wait; call awaitDrained.
func (g *planEngineWriteGate) close() {
	g.mu.Lock()
	g.halted = true
	g.mu.Unlock()
}

// awaitDrained waits until no write admitted before close is still running.
func (g *planEngineWriteGate) awaitDrained() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.drained == nil {
		g.drained = sync.NewCond(&g.mu)
	}
	for g.active > 0 {
		g.drained.Wait()
	}
}

func (g *planEngineWriteGate) reopen() {
	g.mu.Lock()
	g.halted = false
	g.mu.Unlock()
}

func (g *planEngineWriteGate) isClosed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.halted
}

// gatedPlanEngineWrite runs one store call inside the write gate.
func gatedPlanEngineWrite[T any](pe *PlanEngine, write func() (T, error)) (T, error) {
	if !pe.writeGate.enter() {
		var zero T
		return zero, errPlanEngineStopped
	}
	defer pe.writeGate.leave()
	return write()
}

func (pe *PlanEngine) updatePlan(id string, patch plan.Patch) (*plan.Plan, error) {
	return gatedPlanEngineWrite(pe, func() (*plan.Plan, error) { return pe.planStore.Update(id, patch) })
}

func (pe *PlanEngine) updateTask(id string, patch task.Patch) (*task.Task, error) {
	return gatedPlanEngineWrite(pe, func() (*task.Task, error) { return pe.taskStore.Update(id, patch) })
}

func (pe *PlanEngine) createTask(t *task.Task) error {
	_, err := gatedPlanEngineWrite(pe, func() (struct{}, error) { return struct{}{}, pe.taskStore.Create(t) })
	return err
}

func (pe *PlanEngine) addTaskDependency(id, blockerID string) (*task.Task, bool, error) {
	type added struct {
		task  *task.Task
		added bool
	}
	out, err := gatedPlanEngineWrite(pe, func() (added, error) {
		t, ok, err := pe.taskStore.AddDependency(id, blockerID)
		return added{task: t, added: ok}, err
	})
	return out.task, out.added, err
}

func (pe *PlanEngine) advanceBlockedDependents(completedID string) ([]string, error) {
	return gatedPlanEngineWrite(pe, func() ([]string, error) { return pe.taskStore.AdvanceBlockedDependents(completedID) })
}

func (pe *PlanEngine) advanceUnblocked(id string) (*task.Task, error) {
	return gatedPlanEngineWrite(pe, func() (*task.Task, error) { return pe.taskStore.AdvanceUnblocked(id) })
}

func (pe *PlanEngine) restartResetTask(id string) (*task.Task, error) {
	return gatedPlanEngineWrite(pe, func() (*task.Task, error) { return pe.taskStore.RestartReset(id) })
}

// dispatchPlanMember admits one member task through the dispatcher inside the
// gate, so Stop waits for an admission already under way and refuses the
// next. The task run itself is the executor's (its own Drain owns it).
func (pe *PlanEngine) dispatchPlanMember(ctx context.Context, taskID string, initiator *task.Initiator, initiated planInitiatedDispatcher) error {
	_, err := gatedPlanEngineWrite(pe, func() (struct{}, error) {
		if initiator == nil {
			return struct{}{}, pe.dispatcher.executeTaskPlanVerified(ctx, taskID)
		}
		return struct{}{}, initiated.executeTaskPlanInitiated(ctx, taskID, initiator)
	})
	return err
}
