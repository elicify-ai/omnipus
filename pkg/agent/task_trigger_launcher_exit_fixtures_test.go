package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func (p *u2bLauncherProcess) beginClose() {
	p.closeOnce.Do(func() {
		go func() {
			p.loop.Close()
			close(p.closeDone)
		}()
	})
}

// Test-process cleanup, NOT the claimed graceful shutdown. The launcher's
// detached task context may outlive AgentLoop.Stop/Close on the pinned fix.
// Cancel only our old runtime slots so every writer can join. Crash/graceful
// exit images are captured BEFORE this, then restored byte-for-byte afterwards;
// cleanup cannot manufacture an outcome in the next physical boot's input.
// No person Stop/StopNote, task status or job Running flag is inserted.
func (p *u2bLauncherProcess) stop() {
	p.stopOnce.Do(func() {
		p.scheduler.Stop()
		p.loop.Stop()
		te := p.loop.taskExecutor
		te.mu.Lock()
		cancels := make([]context.CancelFunc, 0, len(te.running))
		for _, slot := range te.running {
			cancels = append(cancels, slot.cancel)
		}
		te.mu.Unlock()
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
		te.wg.Wait()
		p.beginClose()
		<-p.closeDone
	})
}

// Component-level model of gateway/shutdown.go::runGracefulShutdown when its
// bounded drain budget expires: stop trigger producers, Stop the loop, wait for
// active requests only within the remaining budget, initiate Close, then leave
// the process even if detached work is still live. Zero remaining wait is an
// explicit exhausted-budget input, not a stopwatch assertion about run speed.
// The real gateway function/70-second wall clock is outside this package test.
//
// A live launcher task cannot be left writing in THIS test process after its
// simulated physical exit. Capture exactly what the shutdown has saved at that
// exit boundary, join old writers as cleanup, then restore those unchanged
// POST-shutdown bytes. This is not the pre-shutdown crash image.
func (p *u2bLauncherProcess) gracefulProcessExit(t *testing.T, taskID string) {
	t.Helper()
	p.scheduler.Stop()
	p.loop.Stop()
	exitCtx, exitCancel := context.WithCancel(context.Background())
	exitCancel()
	drained := p.loop.WaitForActiveRequestsContext(exitCtx)
	p.loop.taskExecutor.Drain(0)
	stillExecuting := p.loop.taskExecutor.holdsRun(taskID)
	p.beginClose()
	if !stillExecuting {
		// A queued launch has no task writer: join the actual close and use
		// its files directly, without any snapshot/restore.
		p.stop()
		t.Logf("orderly process exit: active-request drain=%t; actual task execution=false", drained)
		return
	}

	image := u2bAsyncSnapshot(t, p.home)
	atExit, err := p.loop.taskStore.Get(taskID)
	if err != nil {
		t.Fatalf("fixture: cannot read task at orderly process exit: %v", err)
	}
	var statuses []task.Status
	for _, run := range p.runs(t, taskID) {
		statuses = append(statuses, run.Status)
	}
	t.Logf("orderly process exit: active-request drain=%t; launcher execution still live=%t; task=%q result=%q; run statuses=%v; jobs=%d",
		drained, stillExecuting, atExit.Status, atExit.Result, statuses, len(p.storedJobs(t, taskID)))
	p.stop()
	u2bAsyncRestore(t, p.home, image)
}
