package agent

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// TestDrainSteeredTurns_ReturnsAtItsBudgetWhileATurnIsStillRunning pins the
// timeout branch of the steered-turn drain AgentLoop.Close runs last: a turn
// that never finishes must not freeze shutdown. The drain waits out its
// budget, logs that it gave up, and returns while the turn is still running;
// the turn is then released and joined so nothing outlives the test. The
// production budget (30 s in Close) is unchanged — this drives the waiter
// directly with a small one.
func TestDrainSteeredTurns_ReturnsAtItsBudgetWhileATurnIsStillRunning(t *testing.T) {
	readLog := captureLogFile(t, logger.WARN)
	al, closeAL := newAL(t)

	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseTurn := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseTurn()
		closeAL()
	})

	running := make(chan struct{})
	al.goSteeredTurn(func() {
		close(running)
		<-release
	})
	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("SETUP: the held steered turn never started")
	}

	const budget = 200 * time.Millisecond
	start := time.Now()
	drained := make(chan struct{})
	go func() {
		al.drainSteeredTurns(budget)
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drainSteeredTurns did not return within 5s while a steered turn was held: the drain is unbounded and a stuck turn freezes Close")
	}
	if elapsed := time.Since(start); elapsed < budget {
		t.Fatalf("drainSteeredTurns returned after %v, before its %v budget, while the held turn was still running", elapsed, budget)
	}
	logged := readLog()
	if !strings.Contains(logged, "Close: steered-turn drain budget exceeded; proceeding with teardown") ||
		!strings.Contains(logged, budget.String()) {
		t.Fatalf("the drain gave up without logging that it exceeded its %v budget; captured log:\n%s", budget, logged)
	}

	releaseTurn()
	joined := make(chan struct{})
	go func() {
		al.steerAdmission().turns.Wait()
		close(joined)
	}()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("the held steered turn did not join after release")
	}
}
