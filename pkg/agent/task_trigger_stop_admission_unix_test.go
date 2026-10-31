//go:build linux || darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// 6a delta silent review I1: the gateway's shutdown waits for the scheduler
// stops only until its budget ends, assuming each Stop has already closed
// admission. TaskTriggerScheduler.Stop waited for an in-flight recovery sweep
// BEFORE stopping its cron engine, so a stuck sweep left the engine running:
// a trigger coming due after the budget still started a turn. Oracle: once
// Stop has been called, a due trigger is not dispatched, even while a
// recovery sweep is still running; Stop still waits for that sweep.
func TestTaskTriggerStop_ClosesAdmissionWhileARecoverySweepIsStuck(t *testing.T) {
	sched, store, clk, rec := newTriggerSchedulerForTest(t)

	// Positive control: while running, a due trigger is dispatched.
	control := makeTask(t, store, "agent-a", &task.Trigger{
		Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: int64P(clk.Now().Add(time.Minute).UnixMilli())},
	})
	sched.OnTaskUpserted(control)
	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()
	if calls := rec.calls(); len(calls) != 1 || calls[0] != control.ID {
		t.Fatalf("instrument: a due trigger on the running engine must dispatch once, got %v", calls)
	}

	late := makeTask(t, store, "agent-a", &task.Trigger{
		Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: int64P(clk.Now().Add(time.Minute).UnixMilli())},
	})
	sched.OnTaskUpserted(late)

	// A real stuck sweep: the task directory holds a FIFO named like a task
	// file, and reading it blocks until a writer opens it.
	fifo := filepath.Join(store.Dir(), "stuck-sweep.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	unblock := func() {
		if w, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close() // the blocked reader sees EOF; the sweep moves on
		}
	}
	t.Cleanup(unblock)
	lists := store.ListCallCount()
	// The sweep goroutine's own accounting: Stop waits on sweepWG.
	sched.sweepWG.Add(1)
	go func() {
		defer sched.sweepWG.Done()
		sched.RunRecoverySweep()
	}()
	deadline := time.Now().Add(5 * time.Second)
	for store.ListCallCount() == lists {
		if time.Now().After(deadline) {
			t.Fatal("SETUP: the recovery sweep never started listing tasks")
		}
		time.Sleep(time.Millisecond)
	}

	stopped := make(chan struct{})
	go func() {
		sched.Stop()
		close(stopped)
	}()

	deadline = time.Now().Add(5 * time.Second)
	for sched.cs.Status()["enabled"] != false {
		if time.Now().After(deadline) {
			t.Fatal("Stop did not close the cron engine's admission while the recovery sweep was still running")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned while the recovery sweep was still running: it must still wait for the sweep")
	default:
	}

	clk.Advance(2 * time.Minute)
	sched.RunDueJobs(clk.Now())
	sched.WaitForLane()
	if calls := rec.calls(); len(calls) != 1 {
		t.Fatalf("a trigger due after Stop began was dispatched: %v", calls)
	}

	unblock()
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return after the recovery sweep finished")
	}
}
