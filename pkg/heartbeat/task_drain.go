// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package heartbeat

import (
	"context"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// defaultTaskDrainInterval is the polling cadence for dispatchable (`next`)
// tasks. It is intentionally short relative to the heartbeat interval (which can
// be 30+ minutes) so a queued task does not wait a whole heartbeat cycle to
// dispatch.
const defaultTaskDrainInterval = time.Minute

// TaskQueueChecker is the interface for polling dispatchable queued tasks.
// Implemented by agent.TaskExecutor; defined here to avoid an import cycle.
type TaskQueueChecker interface {
	CheckQueuedTasks(ctx context.Context)
}

// TaskDrainService periodically drains dispatchable (`next`) tasks by polling a
// TaskQueueChecker. It is the unconditional owner of the queued-task drain,
// decoupled from the HEARTBEAT.md execution path, so task dispatch survives
// regardless of the per-agent heartbeat schedule state.
type TaskDrainService struct {
	checker  TaskQueueChecker
	interval time.Duration
	mu       sync.Mutex
	stopChan chan struct{}
	// done closes when the loop goroutine has exited; Stop waits on it (see
	// pollerStopJoinBudget).
	done chan struct{}
}

// NewTaskDrainService creates a drain service polling checker every interval.
// A non-positive interval falls back to defaultTaskDrainInterval. A nil checker
// makes Start a no-op (nothing to drain).
func NewTaskDrainService(checker TaskQueueChecker, interval time.Duration) *TaskDrainService {
	if interval <= 0 {
		interval = defaultTaskDrainInterval
	}
	return &TaskDrainService{checker: checker, interval: interval}
}

// Start begins the drain loop. Idempotent; a nil checker is a no-op.
func (ds *TaskDrainService) Start() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if ds.checker == nil {
		logger.InfoC("heartbeat", "Task-drain service: no task checker — queued-task drain disabled")
		return
	}
	if ds.stopChan != nil {
		return
	}

	ds.stopChan = make(chan struct{})
	ds.done = make(chan struct{})
	go ds.runLoop(ds.stopChan, ds.done)

	logger.InfoCF("heartbeat", "Task-drain service started (owns queued-task draining)", map[string]any{
		"interval_seconds": ds.interval.Seconds(),
	})
}

// Stop halts the drain loop. Idempotent. It returns
// ErrPollerCycleStillRunning (wrapped) when a cycle in flight outlives the
// bounded wait; nil otherwise.
func (ds *TaskDrainService) Stop() error {
	ds.mu.Lock()
	if ds.stopChan == nil {
		ds.mu.Unlock()
		return nil
	}
	close(ds.stopChan)
	ds.stopChan = nil
	done := ds.done
	ds.done = nil
	ds.mu.Unlock()

	// Wait for a check already in flight to finish, so it cannot claim or
	// dispatch a task after Stop returns (failed-boot teardown and shutdown
	// both call Stop before draining the task executor). Deliberately not
	// cancelled: the check passes its context to ExecuteTask, and a cancel
	// between claiming and dispatching would fail a queued task - Stop also
	// runs on every config reload. The check itself is local and short.
	return joinPollerLoop(done, "task-drain", "its queued-task check may still claim or start a task")
}

// IsRunning reports whether the drain loop is active.
func (ds *TaskDrainService) IsRunning() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.stopChan != nil
}

// FireOnce is a TEST SEAM. It synchronously executes exactly one drain tick
// (calls checker.CheckQueuedTasks(ctx)) without waiting for the real ticker to
// fire. This lets tests verify checker invocation deterministically, in
// milliseconds, with no timing dependency. Production code never calls this
// method; the runtime drain path remains solely in runLoop. A nil checker is
// a safe no-op, matching Start's behavior.
func (ds *TaskDrainService) FireOnce(ctx context.Context) {
	if ds.checker == nil {
		return
	}
	ds.checker.CheckQueuedTasks(ctx)
}

func (ds *TaskDrainService) runLoop(stopChan, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(ds.interval)
	defer ticker.Stop()
	// Drain once shortly after start so a task queued before boot dispatches
	// without waiting a full interval. It runs on this goroutine, so Stop
	// joins it like every later pass.
	first := time.NewTimer(time.Second)
	defer first.Stop()

	for {
		select {
		case <-stopChan:
			return
		case <-first.C:
			ds.checker.CheckQueuedTasks(context.Background())
		case <-ticker.C:
			ds.checker.CheckQueuedTasks(context.Background())
		}
	}
}
