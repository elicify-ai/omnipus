// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package heartbeat

import (
	"context"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// defaultTaskDrainInterval is the polling cadence for dispatchable (`next`)
// tasks. It is intentionally short relative to the heartbeat interval (which can
// be 30+ minutes) so a queued task does not wait a whole heartbeat cycle to
// dispatch.
const defaultTaskDrainInterval = time.Minute

// TaskDrainService periodically drains dispatchable (`next`) tasks by polling a
// TaskQueueChecker. It exists because the legacy global HeartbeatService — which
// historically owned the queued-task poll — is NOT started when a per-agent
// heartbeat is active. On those installs (which is every install after the
// global→per-agent heartbeat migration auto-creates a per-agent heartbeat) the
// queued-task poll would otherwise never run and `next` tasks would never
// dispatch. This service owns the queued-task drain unconditionally, decoupled
// from the HEARTBEAT.md execution path, so task dispatch survives regardless of
// which heartbeat path is active.
type TaskDrainService struct {
	checker  TaskQueueChecker
	interval time.Duration
	mu       sync.Mutex
	stopChan chan struct{}
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
	go ds.runLoop(ds.stopChan)

	logger.InfoCF("heartbeat", "Task-drain service started (owns queued-task draining)", map[string]any{
		"interval_seconds": ds.interval.Seconds(),
	})
}

// Stop halts the drain loop. Idempotent.
func (ds *TaskDrainService) Stop() {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if ds.stopChan == nil {
		return
	}
	close(ds.stopChan)
	ds.stopChan = nil
}

// IsRunning reports whether the drain loop is active.
func (ds *TaskDrainService) IsRunning() bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.stopChan != nil
}

func (ds *TaskDrainService) runLoop(stopChan chan struct{}) {
	ticker := time.NewTicker(ds.interval)
	defer ticker.Stop()

	// Drain once shortly after start so a task queued before boot dispatches
	// without waiting a full interval.
	time.AfterFunc(time.Second, func() {
		select {
		case <-stopChan:
			return
		default:
			ds.checker.CheckQueuedTasks(context.Background())
		}
	})

	for {
		select {
		case <-stopChan:
			return
		case <-ticker.C:
			ds.checker.CheckQueuedTasks(context.Background())
		}
	}
}
