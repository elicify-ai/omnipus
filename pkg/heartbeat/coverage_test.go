// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package heartbeat — additional coverage tests.
// Targets: TaskDrainService (idempotent start/stop).
// Build tags: goolm,stdjson
package heartbeat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// TaskDrainService — idempotent stop and coverage gaps
// ---------------------------------------------------------------------------

// Traces to: task_drain.go — idempotent Stop
func TestTaskDrainService_IdempotentStop(t *testing.T) {
	// BDD: Given a stopped TaskDrainService
	// When Stop is called a second time
	// Then no panic occurs
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	ds.Stop()
	ds.Stop() // must not panic
	assert.False(t, ds.IsRunning(), "must remain stopped after double Stop")
}

// Traces to: task_drain.go — idempotent Start
func TestTaskDrainService_IdempotentStart(t *testing.T) {
	// BDD: Given a running TaskDrainService
	// When Start is called again
	// Then no second goroutine is spawned, IsRunning stays true
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()
	defer ds.Stop()

	ds.Start() // second call
	assert.True(t, ds.IsRunning())
}

// Traces to: task_drain.go — stop actually halts ticker (runLoop stopChan path)
func TestTaskDrainService_StopHaltsTicker(t *testing.T) {
	// BDD: Given a running TaskDrainService
	// When Stop is called after at least one tick
	// Then CheckQueuedTasks is not called further
	checker := &countingChecker{done: make(chan struct{})}
	ds := NewTaskDrainService(checker, 10*time.Millisecond)
	ds.Start()

	// Wait for at least one call so the ticker has fired at least once
	select {
	case <-checker.done:
	case <-time.After(3 * time.Second):
		t.Fatal("checker never called before stop test")
	}

	ds.Stop()
	countAtStop := checker.calls.Load()
	time.Sleep(50 * time.Millisecond)

	// Allow for the in-flight AfterFunc
	assert.LessOrEqual(t, checker.calls.Load()-countAtStop, int64(2),
		"CheckQueuedTasks must not keep firing after Stop")
}
