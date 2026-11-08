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

// homeTree snapshots the test home as a path -> size-or-dir-marker map.
// os.ReadDir recursion instead of filepath.WalkDir: a mid-snapshot vanish of
// a single entry (an atomic write's temp renamed away) must degrade ONE
// entry to a marker, not abort or silently truncate the whole walk —
// this snapshot feeds an addition-diff, so a missed entry would be a missed
// writer. A vanished root means there is nothing left to watch.
func homeTree(root string) map[string]int64 {
	out := map[string]int64{}
	entries, err := os.ReadDir(root)
	if err != nil {
		out[root] = -3
		return out
	}
	for _, entry := range entries {
		full := filepath.Join(root, entry.Name())
		if entry.IsDir() {
			out[full] = -1
			for k, v := range homeTree(full) {
				out[k] = v
			}
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			out[full] = -2 // vanished between listing and stat: transient atomic-write temp
			continue
		}
		out[full] = info.Size()
	}
	return out
}

// assertQuiesceBeforeHomeRemoval fails if any NEW entry appears under root
// within bootQuiesceObservationWindow of the teardown drain. This is the
// condition t.TempDir's RemoveAll needs to hold: no writer may create
// entries under the home after cleanup has started (or after the drains it
// must have waited for returned).
func assertQuiesceBeforeHomeRemoval(t *testing.T, root string) {
	t.Helper()
	before := homeTree(root)
	deadline := time.NewTimer(bootQuiesceObservationWindow)
	defer deadline.Stop()
	ticker := time.NewTicker(bootQuiescePollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
			after := homeTree(root)
			var added []string
			for k := range after {
				if _, ok := before[k]; !ok {
					added = append(added, k)
				}
			}
			if len(added) > 0 {
				sort.Strings(added)
				t.Fatalf("home not quiescent after teardown drain: %d new entries appeared (TempDir-cleanup family #1644/#1695/#1598): %v",
					len(added), added)
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
func stopBootWritersOnCleanup(t *testing.T, rc *runContextWithOptions) {
	t.Helper()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(stopCancel)
	t.Cleanup(func() { rc.stopBootBackgroundWriters(stopCtx) })
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
	// then live limits, agent loop, credential store.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	rc.stopBootBackgroundWriters(stopCtx)
	if rc.liveLimits != nil {
		rc.liveLimits.Close()
		agent.SetLiveWindowLookup(nil)
	}
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
	assertQuiesceBeforeHomeRemoval(t, home)
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

	if h.rs.catalogRefreshDone != nil {
		select {
		case <-h.rs.catalogRefreshDone:
		default:
			t.Fatal("provider-catalog refresh loop was not joined by shutdown")
		}
	}
	if h.rs.orphanGCDone != nil {
		select {
		case <-h.rs.orphanGCDone:
		default:
			t.Fatal("media orphan-GC goroutine was not joined by shutdown")
		}
	}
	assertQuiesceBeforeHomeRemoval(t, h.home)
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
