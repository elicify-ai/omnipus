package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// 6a re-check I2: the gateway's shutdown stops waiting for PlanEngine.Stop
// once its budget is spent, and closing stopCh never interrupts a Tick pass
// that is already running. That pass must not dispatch or write anything
// after Stop has begun.

func planEngineStopBegan(pe *PlanEngine) bool {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	return !pe.started
}

func TestPlanEngineStop_InFlightTickCannotWriteOrDispatchAfterStop(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.tickInterval = time.Hour // every pass in this test is explicit
	require.NoError(t, h.pe.Start(context.Background()))

	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	first := mustCreateTask(t, h.tasks, &task.Task{
		Title: "first", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})
	second := mustCreateTask(t, h.tasks, &task.Task{
		Title: "second", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})

	entered := make(chan string, 2)
	release := make(chan struct{})
	h.disp.onDispatch = func(taskID string) error {
		entered <- taskID
		<-release
		return nil
	}

	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		h.pe.Tick(context.Background())
	}()

	var paused string
	select {
	case paused = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the tick never reached member dispatch")
	}
	require.Contains(t, []string{first.ID, second.ID}, paused)

	planBefore, err := h.plans.Get("p1")
	require.NoError(t, err)
	firstBefore, err := h.tasks.Get(first.ID)
	require.NoError(t, err)
	secondBefore, err := h.tasks.Get(second.ID)
	require.NoError(t, err)

	stopDone := make(chan struct{})
	go func() {
		defer close(stopDone)
		h.pe.Stop()
	}()
	require.Eventually(t, func() bool { return planEngineStopBegan(h.pe) },
		10*time.Second, 5*time.Millisecond, "Stop never began")

	close(release)
	select {
	case <-tickDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight tick never finished after release")
	}
	select {
	case <-stopDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop never returned after the in-flight dispatch finished")
	}

	require.Equal(t, []string{paused}, h.disp.callList(),
		"no member may be dispatched after Stop began")
	planAfter, err := h.plans.Get("p1")
	require.NoError(t, err)
	require.Equal(t, planBefore, planAfter, "the plan record changed after Stop began")
	firstAfter, err := h.tasks.Get(first.ID)
	require.NoError(t, err)
	require.Equal(t, firstBefore, firstAfter, "a member record changed after Stop began")
	secondAfter, err := h.tasks.Get(second.ID)
	require.NoError(t, err)
	require.Equal(t, secondBefore, secondAfter, "a member record changed after Stop began")
}

func TestPlanEngineStop_RefusesNewTickUntilNextStart(t *testing.T) {
	h := newTestPlanEngine(t)
	h.pe.tickInterval = time.Hour
	require.NoError(t, h.pe.Start(context.Background()))
	h.pe.Stop()

	mustCreateRunningPlan(t, h.plans, "p1", "owner")
	member := mustCreateTask(t, h.tasks, &task.Task{
		Title: "member", WorkspaceID: "ws", PlanID: "p1", Status: task.StatusNext,
	})
	planBefore, err := h.plans.Get("p1")
	require.NoError(t, err)

	h.pe.Tick(context.Background())
	require.Empty(t, h.disp.callList(), "a stopped engine dispatched a member")
	planAfter, err := h.plans.Get("p1")
	require.NoError(t, err)
	require.Equal(t, planBefore, planAfter, "a stopped engine wrote the plan")

	// A reload restarts the same engine: Start reopens the gate.
	require.NoError(t, h.pe.Start(context.Background()))
	t.Cleanup(h.pe.Stop)
	h.pe.Tick(context.Background())
	require.Equal(t, []string{member.ID}, h.disp.callList(), "the restarted engine must dispatch again")
}
