package browser

// pool_launch_gate_race_test.go — P-3 ("never launch past the gate, not even
// by one") against the condition that defeats it: a second workspace whose
// first browser call lands while the first workspace's Chrome is still
// starting, and is therefore absent from p.instances.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Defect 2: the gate must count launches already in flight ---------------

// blockingLaunchFixture rewires the fixture's coordinator builder so that ONE
// named key's launch parks until the returned channel is closed, while every
// other key launches instantly. It returns a func that reports how many
// Chromes were actually started.
//
// It replaces newPoolFixture's launcher rather than extending it because the
// fixture's own `launched` slice is appended without a lock, and this test
// deliberately runs two launches concurrently — sharing that slice would be a
// data race, i.e. the test itself would be the bug.
func blockingLaunchFixture(t *testing.T, f *poolFixture, blockKey string) (release func(), started <-chan struct{}, count func() int) {
	t.Helper()
	gate := make(chan struct{})
	begun := make(chan struct{})
	var mu sync.Mutex
	n := 0
	var beganOnce sync.Once

	f.pool.newCoordinator = func(homeDir string, c BrowserConfig, key BrowsingKey) *BrowserCoordinator {
		coord := newKeyedCoordinator(homeDir, c, key)
		blocking := key.String() == blockKey
		coord.pipeLauncher = func(_ context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
			mu.Lock()
			n++
			mu.Unlock()
			if blocking {
				beganOnce.Do(func() { close(begun) })
				<-gate
			}
			ctx, cancel := context.WithCancel(context.Background())
			return &pipeLaunchResult{rootCtx: ctx, cancel: cancel}, nil
		}
		return coord
	}
	var releaseOnce sync.Once
	return func() { releaseOnce.Do(func() { close(gate) }) },
		begun,
		func() int { mu.Lock(); defer mu.Unlock(); return n }
}

// TestPool_GateCountsLaunchesAlreadyInFlight is P-3 — "never launch past the
// gate, not even by one" — under the one condition that actually defeats it.
//
// The gate counted browsers already RUNNING (len(p.instances)) and not
// browsers currently STARTING. A key is absent from p.instances for the whole
// duration of its launch, and a launch is not instantaneous: it can resolve a
// binary, and on a cold install it downloads Chrome for Testing first, which
// is tens of seconds to minutes. Two workspaces whose first browser call lands
// anywhere inside that window both read the same "nothing is running", both
// pass, and both launch.
//
// The unmeasurable subtest is the one that matters most in the field. On
// Windows and the BSDs free memory cannot be read at all, so FR-082's floor is
// permanently ONE browser per host regardless of how much RAM the machine has
// — and one is precisely the limit this race steps over. Two workspaces with
// scheduled jobs on the same minute is all it takes.
func TestPool_GateCountsLaunchesAlreadyInFlight(t *testing.T) {
	t.Run("unmeasurable host: the floor of one is not two", func(t *testing.T) {
		f := newPoolFixture(t)
		*f.measurable = false
		release, started, launches := blockingLaunchFixture(t, f, "ws:alpha")
		defer release()

		alphaDone := make(chan error, 1)
		go func() {
			_, err := f.pool.Acquire(context.Background(), browserTestKey("alpha"))
			alphaDone <- err
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("alpha's launch never began; the fixture is not wired up")
		}

		// alpha is mid-launch and holds no entry in p.instances. beta arrives.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		beta, betaErr := f.pool.Acquire(ctx, browserTestKey("beta"))

		require.Error(t, betaErr,
			"the host cannot report its memory, so the limit is ONE browser — and a browser is "+
				"already starting. Admitting beta here is the whole overrun: the gate looked at "+
				"p.instances, which a starting Chrome is absent from")
		assert.Nil(t, beta)
		assert.Contains(t, strings.ToLower(betaErr.Error()), "memory",
			"the refusal must still name memory as the constraint")

		release()
		require.NoError(t, <-alphaDone, "alpha's own launch must be unaffected")
		assert.Equal(t, 1, launches(),
			"exactly ONE Chrome may ever have been started here — P-3 says not even by one, and "+
				"the reason it says so is that the overshoot gets the whole gateway OOM-killed")
	})

	t.Run("measured host: a starting Chrome has not paid for itself yet", func(t *testing.T) {
		f := newPoolFixture(t)
		// Room for exactly one browser. The launch in flight has not yet
		// allocated anything the host will report, so this reading still looks
		// like room for one AFTER alpha has been admitted.
		*f.available = PerBrowserCostBytes
		release, started, launches := blockingLaunchFixture(t, f, "ws:alpha")
		defer release()

		alphaDone := make(chan error, 1)
		go func() {
			_, err := f.pool.Acquire(context.Background(), browserTestKey("alpha"))
			alphaDone <- err
		}()

		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("alpha's launch never began; the fixture is not wired up")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		beta, betaErr := f.pool.Acquire(ctx, browserTestKey("beta"))

		require.Error(t, betaErr,
			"the host reported room for ONE browser and one is already starting. The reading "+
				"cannot see the pending Chrome, so the gate has to reserve for it")
		assert.Nil(t, beta)

		release()
		require.NoError(t, <-alphaDone)
		assert.Equal(t, 1, launches(), "exactly one Chrome was started")
	})

	t.Run("a pending launch does not block a host with room for both", func(t *testing.T) {
		// The counterweight: reserving for pending launches must not turn into
		// a pool that serialises every launch on a roomy machine. A gate that
		// refused whenever anything was starting would pass both subtests
		// above while making two workspaces unable to start browsers at once.
		f := newPoolFixture(t)
		*f.available = 64 << 30
		release, started, launches := blockingLaunchFixture(t, f, "ws:alpha")
		defer release()

		alphaDone := make(chan error, 1)
		go func() {
			_, err := f.pool.Acquire(context.Background(), browserTestKey("alpha"))
			alphaDone <- err
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("alpha's launch never began")
		}

		beta, betaErr := f.pool.Acquire(context.Background(), browserTestKey("beta"))
		require.NoError(t, betaErr,
			"64 GB is room for both — a pending launch must reserve headroom, not take a lock")
		require.NotNil(t, beta)

		release()
		require.NoError(t, <-alphaDone)
		assert.Equal(t, 2, launches())
	})
}
