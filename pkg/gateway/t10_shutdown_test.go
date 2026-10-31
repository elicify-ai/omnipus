package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 695, 702, 1672; TDD 9, step 0 (#1490).
// The release base ALREADY has #1490's three-copy stop fix. These are honest
// passing characterizations, not a deliberately wrong oracle made RED.
func TestShutdown_EngineStopsBeforeDrain(t *testing.T) {
	t.Run("real-timer-stores-do-not-change-after-shutdown", TestGracefulShutdown_StopsTaskTriggerAndLoopTimers)
	t.Run("real-task-and-loop-copies-stopped-while-turn-drain-held", TestGracefulShutdown_StopsTimerCopiesBeforeTheTurnDrain)
	t.Run("existing-budget-cancels-wait-and-closes-loop", func(t *testing.T) {
		loop := &t10ShutdownLoop{}
		var mu sync.Mutex
		stopped := map[string]bool{}
		mark := func(name string) func() { return func() { mu.Lock(); stopped[name] = true; mu.Unlock() } }
		loop.onStop = func() {
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, map[string]bool{"engine": true, "plan": true}, stopped, "timer and plan stops precede active-turn intake closure")
		}
		begin := time.Now()
		runGracefulShutdown(&services{}, loop, nil, shutdownBudget{total: 250 * time.Millisecond, margin: 50 * time.Millisecond}, shutdownBackgroundStops{schedulers: []func(){mark("engine")}, plan: mark("plan")})
		require.True(t, loop.expired, "held turn must reach budget cancellation, not a fabricated successful drain")
		require.True(t, loop.closed, "resources must close after cancellation")
		assert.Less(t, time.Since(begin), 2*time.Second, "shutdown must stay bounded")
	})
}

type t10ShutdownLoop struct {
	onStop          func()
	expired, closed bool
}

func (l *t10ShutdownLoop) Stop() { l.onStop() }
func (l *t10ShutdownLoop) WaitForActiveRequestsContext(ctx context.Context) bool {
	<-ctx.Done()
	l.expired = ctx.Err() == context.DeadlineExceeded
	return false
}
func (l *t10ShutdownLoop) Close() { l.closed = true }
