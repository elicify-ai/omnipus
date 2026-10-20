package heartbeat

import (
	"errors"
	"fmt"
	"time"
)

// pollerStopJoinBudget bounds how long Stop waits for a cycle already in
// flight to return. A slow or wedged cycle must not freeze shutdown or a
// config reload: after the budget Stop returns ErrPollerCycleStillRunning
// and the caller reports it. A variable only so tests can shorten it.
var pollerStopJoinBudget = 10 * time.Second

// ErrPollerCycleStillRunning reports that Stop's bounded wait ran out while a
// cycle was still in flight. The cycle was not cancelled; it finishes on its
// own and can still do the work the error names after Stop returned.
var ErrPollerCycleStillRunning = errors.New("a poller cycle is still running after the stop wait")

// pollerJoinStartedTestHook is a test-only seam fired when Stop has closed the
// loop's stop channel and is about to wait for the loop. nil in production.
var pollerJoinStartedTestHook func(service string)

// joinPollerLoop waits for a poller's loop goroutine to exit, bounded by
// pollerStopJoinBudget. stillRunning names, for the caller's warning, what
// an unfinished cycle may still be doing.
func joinPollerLoop(done <-chan struct{}, service, stillRunning string) error {
	if hook := pollerJoinStartedTestHook; hook != nil {
		hook(service)
	}
	budget := pollerStopJoinBudget
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("%s: %w (waited %s); %s", service, ErrPollerCycleStillRunning, budget, stillRunning)
	}
}
