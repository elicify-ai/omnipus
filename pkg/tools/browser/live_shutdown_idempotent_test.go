package browser

import (
	"sync"
	"testing"
)

// newShutdownTestRegistry builds a registry through the REAL constructor, so
// the sweeper goroutine these tests stop is the same goroutine production
// starts. It must not be hand-built as a bare &LiveViewRegistry{}: sweepTick
// opens with `r.mgr.cfg.TakeControlEnabled`, so a registry with a nil mgr
// panics the moment a tick lands — a self-inflicted failure that has nothing
// to do with the defect under test.
func newShutdownTestRegistry() *LiveViewRegistry {
	return newLiveViewRegistry(&BrowserManager{})
}

// TestLiveViewRegistryShutdownIsIdempotent pins the fix for a gateway PANIC.
//
// THE DEFECT: LiveViewRegistry.Shutdown guarded only `sweepStop == nil` and
// then closed the channel unconditionally, so a SECOND call hit
// "close of closed channel" and killed the whole process. It fired on the
// ordinary config-reload path — rewireBrowserManagerForKey called
// pool.Release (which reaches Shutdown via coordinator.Release ->
// dropConnection) and then called prior.Shutdown() again. A Settings save
// crashed the gateway.
//
// Found by the ADR-084/085/086 CI run, where it killed the ui-heavy e2e shard
// mid-run and turned eleven later tests into cascade casualties reporting
// ECONNREFUSED — a failure that looks nothing like its cause.
//
// This test calls Shutdown twice on a STARTED registry: before the fix the
// second call panics and the test dies.
func TestLiveViewRegistryShutdownIsIdempotent(t *testing.T) {
	r := newShutdownTestRegistry()

	r.Shutdown()
	r.Shutdown() // the one that used to panic
	r.Shutdown() // and a third, because "twice" is not a special number
}

// TestLiveViewRegistryShutdownIsSafeConcurrently covers the same channel from
// several goroutines at once — the reload path and the sweeper teardown can
// race, and exactly one caller must win the close.
func TestLiveViewRegistryShutdownIsSafeConcurrently(t *testing.T) {
	r := newShutdownTestRegistry()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.Shutdown() }()
	}
	wg.Wait()
}

// TestLiveViewRegistryShutdownOnNeverStartedRegistry keeps the original
// contract Shutdown's doc comment states: a hand-built registry that never
// called startControlIdleSweeper must still be safe, repeatedly.
func TestLiveViewRegistryShutdownOnNeverStartedRegistry(t *testing.T) {
	r := &LiveViewRegistry{}
	r.Shutdown()
	r.Shutdown()
}
