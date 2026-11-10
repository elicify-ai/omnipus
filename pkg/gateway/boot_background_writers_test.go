package gateway

// Regression for the TempDir-cleanup family (#1644/#1695/#1598): the
// boot-stage background writers — the browser cache-trim schedule, the
// managed-Chromium preprovision download, the provider-catalog refresh loop
// and the media-library orphan-GC goroutine — used to outlive every
// teardown. Measured 2026-11-08 on the boot-test shape: the preprovision
// download was still growing browser/chromium/<version>/*.zip.part-*
// seconds after the test's whole cleanup sequence had returned, which is
// exactly the CI failure "TempDir RemoveAll cleanup: ... browser: directory
// not empty"; the catalog pull's providers_catalog.json write and the
// orphan-GC repair's workspaces/ writes are the same class on the
// setupAndStartServices path.
//
// Two instruments per boot shape:
//   - the JOIN assertion: the teardown drain must return only after every
//     writer goroutine has exited (deterministic everywhere, hermetic too);
//   - the WRITE-WINDOW assertion: after the drain, no new entry may appear
//     in the test home for a bounded observation span — the direct
//     reproduction of the TempDir race, sensitive to a live writer whatever
//     its write latency. The window is an observation span for the
//     instrument, not tolerance: ANY entry appearing fails the test.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/stretchr/testify/require"
)

// bootQuiesceObservationWindow is how long the write-window instrument
// watches the home after the teardown drain before declaring it quiescent.
// Long enough that a live writer's first write lands inside it (the measured
// preprovision download writes continuously from its first byte), short
// enough to keep a -count=200 stress run affordable.
const bootQuiesceObservationWindow = 1200 * time.Millisecond

// bootQuiescePollEvery is the write-window poll interval.
const bootQuiescePollEvery = 50 * time.Millisecond

// homeTree snapshots the test home as a path -> size map, FAILING the test on
// any read or stat error (CHECK 1 finding F5): it runs after the teardown
// drains, when the tree must be stable, so an error means something is still
// creating, removing or replacing entries under the instrument — exactly the
// mutation the old marker degradation (-2/-3 sentinels) would have hidden.
func homeTree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("home tree unreadable at %s: %v", root, err)
	}
	for _, entry := range entries {
		full := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			out[full] = -1
			for k, v := range homeTree(t, full) {
				out[k] = v
			}
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			t.Fatalf("home tree entry %s did not survive its own stat: %v", full, infoErr)
		}
		out[full] = info.Size()
	}
	return out
}

// watchHomeQuiescent fails if the home changes AT ALL within
// bootQuiesceObservationWindow of the teardown drain: additions, removals
// and size changes are all live-writer evidence (CHECK 1 finding F5 — an
// addition-only diff cannot see a writer that DELETES or rewrites, e.g. the
// cache-trim sweep removing profile directories, or a download replacing a
// .part file). The baseline is taken where this helper is called — the
// caller takes it immediately after the drains return and BEFORE the home's
// own removal (t.TempDir), so the diff covers everything from the join
// boundary to the removal the family races.
func watchHomeQuiescent(t *testing.T, root string, before map[string]int64) {
	t.Helper()
	deadline := time.NewTimer(bootQuiesceObservationWindow)
	defer deadline.Stop()
	ticker := time.NewTicker(bootQuiescePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			after := homeTree(t, root)
			var added, removed, changed []string
			for k, v := range after {
				old, ok := before[k]
				if !ok {
					added = append(added, k)
				} else if old != v {
					changed = append(changed, fmt.Sprintf("%s (%d -> %d bytes)", k, old, v))
				}
			}
			for k := range before {
				if _, ok := after[k]; !ok {
					removed = append(removed, k)
				}
			}
			if len(added)+len(removed)+len(changed) > 0 {
				sort.Strings(added)
				sort.Strings(removed)
				sort.Strings(changed)
				t.Fatalf("home not quiescent after teardown drain (TempDir-cleanup family #1644/#1695/#1598): %d added %v; %d removed %v; %d resized %v",
					len(added), added, len(removed), removed, len(changed), changed)
			}
		}
	}
}

// stopBootWritersOnCleanup joins the boot-stage writer goroutines that
// initializeAgentLoop started, ahead of every other teardown and ahead of the
// TempDir removal (registered last, LIFO runs it first). TempDir-cleanup
// family #1644/#1695: without the join, the preprovision download kept
// writing browser/chromium/*.zip.part-* into the test home after the whole
// cleanup sequence had returned. Every test that drives initializeAgentLoop
// registers this — those goroutines are started on that path unconditionally.
//
// The shutdown budget starts when CLEANUP RUNS, not when it is registered
// (CHECK 1 finding F4): a test whose other cleanups or body keep it busy
// longer than the budget must not hand this drain an already-expired
// context, or the drain returns while the writers are still live. The
// budget value is the same bootBackgroundStopTimeout every drain uses —
// deliberately not enlarged.
func stopBootWritersOnCleanup(t *testing.T, rc *runContextWithOptions) {
	t.Helper()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), bootBackgroundStopTimeout)
		defer stopCancel()
		rc.stopBootBackgroundWriters(stopCtx)
	})
}

// TestBootBackgroundWriters_JoinedBeforeHomeRemoval drives the
// TestBootSkills_ActualStartupBeforeFirstLiveAgent boot shape end to end:
// real loadConfigAndProvider + initializeAgentLoop, then the same teardown
// order its cleanups run, then the join and write-window assertions.
func TestBootBackgroundWriters_JoinedBeforeHomeRemoval(t *testing.T) {
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	// The live-window lookup is a package-global the boot sets; reset it even
	// if a mid-test assertion fails, so this test cannot poison later ones.
	t.Cleanup(func() { agent.SetLiveWindowLookup(nil) })
	rc := &runContextWithOptions{ctx: context.Background(), homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err, "real credentials/config/roster startup must succeed")
	require.False(t, stop)
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)

	// Teardown in the boot test's LIFO order: the boot-stage writers first,
	// then live limits, agent loop, credential store. The unknown-window
	// primer's only DISK I/O is its fetch/cache write, and that is bounded by
	// the LiveLimits lifecycle — LiveLimits.Close cancels the fetch and joins
	// its goroutine (pkg/agent/live_limits.go::Close; pinned by
	// pkg/agent/live_limits_close_test.go::TestLiveLimits_CloseAbortsInflightFetchAndNeverWrites,
	// "once Close returns, the instance performs no further I/O") — not by
	// the boot WaitGroup, whose membership covers only the primer's
	// synchronous ResolveWindow pass (live_limits_boot.go::primeUnknownWindows
	// never blocks). So the boot drain is asserted on the join above, and the
	// primer's write boundary is asserted here: the real boot built the
	// instance and this teardown closed it BEFORE the home is watched.
	require.NotNil(t, rc.liveLimits, "real boot must build the live-limits instance the primer's fetches are bounded by")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), bootBackgroundStopTimeout)
	defer stopCancel()
	rc.stopBootBackgroundWriters(stopCtx)
	rc.liveLimits.Close()
	require.True(t, rc.liveLimits.Closed(), "primer's fetch boundary: live limits must be closed before the home is watched")
	agent.SetLiveWindowLookup(nil)
	if rc.agentLoop != nil {
		rc.agentLoop.Close()
	}
	if rc.credStore != nil {
		rc.credStore.Close()
	}

	// JOIN: the drain returned, so every boot-stage writer goroutine has
	// exited — not "will exit soon".
	select {
	case <-rc.bootBackgroundDone:
	default:
		t.Fatal("boot background writers were not joined by the teardown drain")
	}
	watchHomeQuiescent(t, home, homeTree(t, home))
}

// TestBootWriters_JoinHoldsForUnknownWindowPrimer kills the primer WaitGroup
// mutant (CHECK 1 finding F5's survivor, CHECK 2's weak test): the mutant
// drops the primer from the boot WaitGroup, and every previously passing
// assertion — join-before-home-removal, quiescence — stayed green with it,
// because the primer's synchronous pass is microseconds and its fetch is
// bounded by the LiveLimits lifecycle instead of the WaitGroup. This test
// holds the primer pass MID-FLIGHT through the seam the production goroutine
// calls (live_limits_boot.go::primeUnknownWindowsFn), so the drain's join is
// observable: a correct drain cannot return while the pass is still running,
// and the verdict needs no timing — "drain returned AND pass still held" is
// a proven violation the moment both facts hold. It also pins the pairing
// with defect 1's fix: the drain joins the primer BEFORE it closes the
// rung, so a boot teardown can never cut the primer's priming short by
// closing the rung under it.
func TestBootWriters_JoinHoldsForUnknownWindowPrimer(t *testing.T) {
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	t.Cleanup(func() { agent.SetLiveWindowLookup(nil) })

	primerStarted := make(chan struct{})
	primerDone := make(chan struct{})
	release := make(chan struct{})
	oldFn := primeUnknownWindowsFn
	primeUnknownWindowsFn = func(al windowReloader) {
		close(primerStarted)
		<-release
		primeUnknownWindows(al)
		close(primerDone)
	}
	t.Cleanup(func() {
		primeUnknownWindowsFn = oldFn
		select {
		case <-release:
		default:
			close(release) // never leak a held primer into a later test
		}
	})

	rc := &runContextWithOptions{ctx: context.Background(), homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err, "real credentials/config/roster startup must succeed")
	require.False(t, stop)
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)
	stopBootWritersOnCleanup(t, rc)

	select {
	case <-primerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: the unknown-window primer pass never started")
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), bootBackgroundStopTimeout)
	defer stopCancel()
	drainReturned := make(chan struct{})
	go func() {
		rc.stopBootBackgroundWriters(stopCtx)
		close(drainReturned)
	}()

	// A correct drain is still joined on the held pass here; a primer-dropping
	// drain is back in microseconds. If it IS back already, the pass must be
	// provably done — while the pass is still held, an early return is the
	// proven violation, with no sleep and no threshold in the verdict.
	select {
	case <-drainReturned:
		select {
		case <-primerDone:
		default:
			t.Fatal("boot drain returned while the unknown-window primer pass was still running: the primer was not joined by the boot WaitGroup")
		}
	case <-time.After(250 * time.Millisecond):
		// still waiting on the held pass — the join is holding it
	}
	close(release)
	select {
	case <-drainReturned:
	case <-time.After(10 * time.Second):
		t.Fatal("boot drain never returned after the held primer pass completed")
	}
	select {
	case <-primerDone:
	default:
		t.Fatal("the held primer pass did not complete")
	}
	select {
	case <-rc.bootBackgroundDone:
	default:
		t.Fatal("boot background writers were not joined by the teardown drain")
	}
}

// TestWU15Boot_QuiesceBeforeHomeRemoval drives the setupAndStartServices
// boot shape every WU15 rooms test uses (wu15NewHarness), including a real
// room exchange, then the harness shutdown and the same assertions for the
// writers that path starts: the provider-catalog refresh loop and the media
// orphan-GC goroutine (#1695's root-level and #1598's workspaces/-level
// TempDir failures).
func TestWU15Boot_QuiesceBeforeHomeRemoval(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Quiesce", "mia", "jim")
	h.post(room.Id, "quiesce-1", "@Mia ping")

	h.shutdown() // the same shutdown t.Cleanup registered (sync.Once)

	// CHECK 1 finding F5: the real boot is EXPECTED to create these handles —
	// a nil handle here means the writer never got a lifecycle at all, which
	// must fail this test, not be skipped by a nil check.
	require.NotNil(t, h.rs.catalogRefreshCancel, "real boot must attach the catalog refresh cancel handle")
	require.NotNil(t, h.rs.catalogRefreshDone, "real boot must attach the catalog refresh done handle")
	require.NotNil(t, h.rs.orphanGCCancel, "real boot must attach the orphan-GC cancel handle")
	require.NotNil(t, h.rs.orphanGCDone, "real boot must attach the orphan-GC done handle")

	select {
	case <-h.rs.catalogRefreshDone:
	default:
		t.Fatal("provider-catalog refresh loop was not joined by shutdown")
	}
	select {
	case <-h.rs.orphanGCDone:
	default:
		t.Fatal("media orphan-GC goroutine was not joined by shutdown")
	}
	watchHomeQuiescent(t, h.home, homeTree(t, h.home))
}

// TestStopAndCleanupServices_WaitsForBootBackgroundWriters proves the WAIT
// itself, not a fortunate exit timing: a fake boot-stage writer that exits
// only 150 ms after its cancel makes a non-waiting teardown deterministically
// red — stopAndCleanupServices would return long before the writer had
// exited, and the join check below would fire. With the real drain in place
// the function cannot return before done is closed, whatever the writer's
// exit latency. This is the same fake-loop shape the graceful-shutdown
// budget tests use for stopBackgroundWriters' waits.
func TestStopAndCleanupServices_WaitsForBootBackgroundWriters(t *testing.T) {
	cancelCalled := make(chan struct{})
	done := make(chan struct{})
	rs := &services{
		bootBackgroundCancel: func() { close(cancelCalled) },
		bootBackgroundDone:   done,
	}
	go func() {
		<-cancelCalled
		// The artificial exit latency makes the mutant's early return
		// observable: without the join, stopAndCleanupServices is back in
		// microseconds, this writer is still running, and the assertion
		// below fails deterministically.
		time.Sleep(150 * time.Millisecond)
		close(done)
	}()

	stopAndCleanupServices(rs, 5*time.Second, false)

	select {
	case <-done:
	default:
		t.Fatal("stopAndCleanupServices returned while a boot background writer was still running")
	}
}
