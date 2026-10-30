// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 6a delta review I2 (code) and finding 2 (tests): the scheduler stops were
// bounded, but the plan-engine stop (30 s drain), the live-limits close and a
// fresh 10 s catalog wait were not, so shutdown could pass its 70 s budget or
// reach the agent-loop close with the budget already spent. These tests drive
// the real sequence, runGracefulShutdown, with slow or stuck stand-ins for the
// waits and a scaled budget. Oracle: the whole sequence ends inside the
// budget; every stop is still started, in order; the active-turn wait ends at
// the budget minus the margin; the agent-loop close starts with the margin
// still left.

// sequenceTestBudget is the scaled budget: 3 s in all, 0.5 s kept for the
// agent-loop close. The production budget is 70 s / 5 s.
var sequenceTestBudget = shutdownBudget{total: 3 * time.Second, margin: 500 * time.Millisecond}

// sequenceSlack absorbs scheduling delays on a busy machine.
const sequenceSlack = 250 * time.Millisecond

// sequenceRecorder notes, relative to the start of shutdown, when each step
// was entered and returned.
type sequenceRecorder struct {
	mu      sync.Mutex
	start   time.Time
	entered map[string]time.Duration
	left    map[string]time.Duration
	order   []string
}

func newSequenceRecorder() *sequenceRecorder {
	return &sequenceRecorder{entered: map[string]time.Duration{}, left: map[string]time.Duration{}}
}

func (r *sequenceRecorder) enter(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entered[name] = time.Since(r.start)
	r.order = append(r.order, name)
}

func (r *sequenceRecorder) leave(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.left[name] = time.Since(r.start)
}

func (r *sequenceRecorder) snapshot() (entered, left map[string]time.Duration, order []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entered, left = map[string]time.Duration{}, map[string]time.Duration{}
	for k, v := range r.entered {
		entered[k] = v
	}
	for k, v := range r.left {
		left[k] = v
	}
	return entered, left, append([]string(nil), r.order...)
}

// step returns a stop that records itself and returns after d, or blocks
// until release closes when d is negative.
func (r *sequenceRecorder) step(name string, d time.Duration, release <-chan struct{}) func() {
	return func() {
		r.enter(name)
		defer r.leave(name)
		if d < 0 {
			<-release
			return
		}
		time.Sleep(d)
	}
}

// sequenceLoop stands in for the agent loop: one active turn never finishes,
// so the active-turn wait always runs to its bound.
type sequenceLoop struct{ r *sequenceRecorder }

func (l sequenceLoop) Stop() { l.r.enter("loop_stop"); l.r.leave("loop_stop") }
func (l sequenceLoop) WaitForActiveRequestsContext(ctx context.Context) bool {
	l.r.enter("turn_wait")
	defer l.r.leave("turn_wait")
	<-ctx.Done()
	return false
}
func (l sequenceLoop) Close() { l.r.enter("loop_close"); l.r.leave("loop_close") }

// runSequence runs the real shutdown sequence and returns its duration. A
// sequence that does not end well past the budget fails the test.
func runSequence(t *testing.T, r *sequenceRecorder, stops shutdownBackgroundStops) time.Duration {
	t.Helper()
	done := make(chan time.Duration, 1)
	r.start = time.Now()
	go func() {
		runGracefulShutdown(&services{}, sequenceLoop{r: r}, nil, sequenceTestBudget, stops)
		done <- time.Since(r.start)
	}()
	select {
	case elapsed := <-done:
		return elapsed
	case <-time.After(sequenceTestBudget.total + 10*time.Second):
		_, _, order := r.snapshot()
		t.Fatalf("shutdown did not end within %s of its %s budget; steps entered: %v", 10*time.Second, sequenceTestBudget.total, order)
		return 0
	}
}

// requireBudgetKept checks the bounds every run must keep.
func requireBudgetKept(t *testing.T, elapsed time.Duration, entered, left map[string]time.Duration) {
	t.Helper()
	b := sequenceTestBudget
	require.LessOrEqual(t, elapsed, b.total+sequenceSlack, "the whole sequence must end inside the shutdown budget")
	require.Contains(t, entered, "loop_close", "the agent loop must be closed")
	require.LessOrEqual(t, entered["loop_close"], b.total-b.margin+sequenceSlack,
		"the agent-loop close must start with the margin still left, not with an expired budget")
	require.InDelta(t, float64(b.total-b.margin), float64(left["turn_wait"]), float64(sequenceSlack),
		"the active-turn wait must end at the budget minus the margin: it gets all the budget left, no more")
}

// Stuck later waits: every background stop hangs. The sequence still ends in
// the budget, and each stop is still started (its first act closes admission).
func TestGracefulShutdown_StuckBackgroundStopsStayInsideTheBudget(t *testing.T) {
	r := newSequenceRecorder()
	stuck := make(chan struct{})
	t.Cleanup(func() { close(stuck) })
	catalogDone := make(chan struct{}) // a wedged catalog transport: never closes
	elapsed := runSequence(t, r, shutdownBackgroundStops{
		schedulers:    []func(){r.step("cron", -1, stuck), r.step("task_trigger", -1, stuck), r.step("loop", -1, stuck)},
		plan:          r.step("plan", -1, stuck),
		liveLimits:    r.step("live_limits", -1, stuck),
		catalogCancel: func() { r.enter("catalog_cancel"); r.leave("catalog_cancel") },
		catalogDone:   catalogDone,
	})
	entered, left, _ := r.snapshot()
	requireBudgetKept(t, elapsed, entered, left)
	// Once the budget is spent, each later stop is launched in order but not
	// awaited, so the moment its goroutine starts is not ordered; only that
	// every one of them starts.
	require.Eventually(t, func() bool {
		entered, _, _ := r.snapshot()
		for _, name := range []string{"cron", "task_trigger", "loop", "plan", "live_limits", "catalog_cancel", "loop_stop", "turn_wait"} {
			if _, ok := entered[name]; !ok {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "every stop must still be started when the budget is spent")
}

// Slow, finishing later waits: the three scheduler stops run side by side,
// the later stops follow in order, and the active-turn wait gets exactly the
// budget that is left. Run one after another, the scheduler stops alone would
// take 2.4 s of the 2.5 s available for stops; a fixed active-turn allowance
// would end the turn wait past the budget.
func TestGracefulShutdown_SlowBackgroundStopsLeaveTheRestForActiveTurns(t *testing.T) {
	r := newSequenceRecorder()
	catalogDone := make(chan struct{})
	elapsed := runSequence(t, r, shutdownBackgroundStops{
		schedulers:  []func(){r.step("cron", 800*time.Millisecond, nil), r.step("task_trigger", 800*time.Millisecond, nil), r.step("loop", 800*time.Millisecond, nil)},
		plan:        r.step("plan", 400*time.Millisecond, nil),
		liveLimits:  r.step("live_limits", 200*time.Millisecond, nil),
		catalogDone: catalogDone,
		catalogCancel: func() {
			r.enter("catalog_cancel")
			r.leave("catalog_cancel")
			time.AfterFunc(200*time.Millisecond, func() { close(catalogDone) })
		},
	})
	entered, left, order := r.snapshot()
	requireBudgetKept(t, elapsed, entered, left)
	for _, name := range []string{"cron", "task_trigger", "loop"} {
		require.Contains(t, left, name, "order %v", order)
		require.LessOrEqual(t, entered[name], sequenceSlack, "%s must start at once, beside the other scheduler stops", name)
		require.GreaterOrEqual(t, entered["plan"], left[name], "the plan engine stops after %s finished: order kept", name)
	}
	require.LessOrEqual(t, entered["plan"], 800*time.Millisecond+sequenceSlack, "the scheduler stops ran side by side, not one after another")
	require.GreaterOrEqual(t, entered["live_limits"], left["plan"], "live limits after the plan engine")
	require.GreaterOrEqual(t, entered["catalog_cancel"], left["live_limits"], "the catalog after live limits")
	require.GreaterOrEqual(t, entered["loop_stop"], left["catalog_cancel"]+150*time.Millisecond, "the catalog loop's exit is waited for")
	require.Less(t, entered["loop_stop"], 1800*time.Millisecond+sequenceSlack, "the stops finished early, so the turns get the rest")
}
