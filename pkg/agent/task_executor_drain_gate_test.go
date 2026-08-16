package agent

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// A refused dispatch must not touch te.wg at all. Before the dispatchGate fix
// the Add(1) ran BEFORE the draining check, so a refusal drove the counter
// 0 -> 1; when that raced Drain's parked wg.Wait it panicked the process with
// "sync: WaitGroup misuse: Add called concurrently with Wait" during shutdown.
func TestEnterDispatchRefusedLeavesWaitGroupUntouched(t *testing.T) {
	te := &TaskExecutor{}
	te.draining.Store(true)

	if te.enterDispatch() {
		t.Fatal("enterDispatch admitted a dispatch while draining")
	}

	// If the refusal had added to the counter, this Wait would block forever
	// (nothing will ever call the matching Done).
	waited := make(chan struct{})
	go func() {
		te.wg.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("wg.Wait blocked after a REFUSED dispatch — the refusal leaked a counter increment")
	}
}

// The panic window is a race, so this drives the exact interleaving that used
// to hit it: one in-flight dispatch completing (counter -> 0) while Drain has
// a waiter parked, concurrently with a fresh dispatch attempt. Repeated,
// because a single pass would only sometimes land in the window.
func TestDrainRacingRefusedDispatchDoesNotPanic(t *testing.T) {
	for iter := 0; iter < 300; iter++ {
		te := &TaskExecutor{}

		// One in-flight dispatch so Drain's wg.Wait genuinely parks a waiter
		// rather than returning immediately.
		if !te.enterDispatch() {
			t.Fatalf("iteration %d: enterDispatch refused on a fresh executor", iter)
		}

		var racers sync.WaitGroup
		racers.Add(2)
		go func() {
			defer racers.Done()
			te.wg.Done() // the in-flight dispatch finishes: counter -> 0
		}()
		go func() {
			defer racers.Done()
			if te.enterDispatch() { // may be admitted or refused; both are legal
				te.wg.Done()
			}
		}()

		te.Drain(5 * time.Second)
		racers.Wait()
	}
}

// Once Drain has closed intake, every subsequent dispatch attempt must be
// refused — the gate is one-way, matching draining's "never reset" contract.
func TestDrainClosesIntakePermanently(t *testing.T) {
	te := &TaskExecutor{}
	te.Drain(time.Second)

	for i := 0; i < 3; i++ {
		if te.enterDispatch() {
			t.Fatalf("attempt %d: enterDispatch admitted work after Drain", i)
		}
	}
}

// Source-level guard for the exemption in dispatchGate's doc comment. That
// exemption ("goroutine-launch sites may Add directly") is only sound where
// the launching call already holds a wg entry, so the counter cannot go
// 0 -> 1. An adversarial review found notifyParentIfAllSiblingsDone violating
// exactly that: it is reached from the task_update tool via AgentLoop's
// SetOnComplete hook, an ordinary agent turn holding no count, so its bare
// Add could still panic a parked wg.Wait during shutdown.
//
// A behavioural test would need a full store/parent/sibling fixture; this
// pins the invariant directly and cheaply instead, and fails the moment a new
// unguarded Add appears anywhere in the file.
func TestNoUngatedWaitGroupAddOutsideHeldCountSites(t *testing.T) {
	src, err := os.ReadFile("task_executor.go")
	if err != nil {
		t.Fatalf("read task_executor.go: %v", err)
	}
	// The ONLY three sites allowed to Add directly:
	//   enterDispatch  — the gate itself; its Add is the guarded one.
	//   executeTask    — launches runTask while the caller already holds the
	//                    entry ExecuteTask/executeTaskPlanVerified took.
	//   StartTaskNow   — launches runTaskFromInProgress under its own entry.
	// In the latter two the counter is provably >= 1, so the Add can never be
	// the 0 -> 1 transition that panics. Everything else must route through
	// enterDispatch().
	const allowed = 3

	got := strings.Count(string(src), "te.wg.Add(1)")
	if got != allowed {
		t.Fatalf("found %d direct te.wg.Add(1) call(s) in task_executor.go, want exactly %d.\n"+
			"A new direct Add is only safe if its caller ALREADY holds a wg entry; "+
			"otherwise it can take the counter 0->1 while Drain has a waiter parked, "+
			"which panics the process (sync/waitgroup.go). Route it through te.enterDispatch() instead, "+
			"or update this test with the reason the new site is exempt.", got, allowed)
	}
}
