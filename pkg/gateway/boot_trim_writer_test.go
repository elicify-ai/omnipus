package gateway

// The boot cache-trim sweep's WaitGroup membership (TempDir-cleanup family
// #1644/#1695, CHECK 1 finding F5). The trim goroutine's exit after its
// cancel is near-instant, so the only observable of a MISSING join is the
// sweep still WALKING profiles when the drain has already returned: the
// sweep is ctx-unaware (TrimAllEligible runs to completion), and per
// profile it CREATES a sibling .chrome.lock file and REMOVES the allow-listed
// cache directories. This test plants enough real profiles that the sweep
// outlasts the drain, waits for the first profile to be touched (the sweep
// is provably in flight), drains, and then requires the sweep to have
// finished every planted profile and the home to be completely unchanged —
// additions, removals and resizes all fail. Post-fix the drain waits out the
// whole sweep inside the join; on the mutant that dropped the trim goroutine
// from the WaitGroup the drain returns early and the remaining profiles are
// trimmed after it did.
//
// The drain's budget is controlled by this test, not a wall clock (CHECK 4
// finding R3, 2026-11-08). The test used to hand the drain a real
// bootBackgroundStopTimeout (10 s) deadline: under a loaded -race run the
// 1,200-profile sweep outlasted it, the drain returned by design (a budgeted
// stop continues without a writer that is still running) and the join
// assertion failed although nothing was wrong with the join. This test proves
// the JOIN; whether a drain that runs out of budget continues is a separate
// contract that the real budget keeps in production. So the drain's context
// is cancelled only by the test, and only on its liveness guard — the go test
// deadline, the same bound that would otherwise kill a hung test — which
// fails the test as a hang, never passes it.
import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// trimKillProfiles is the planted-profile count: enough that the ctx-unaware
// boot sweep is still mid-walk when the drain starts, whatever the machine.
const trimKillProfiles = 1200

// firstProfileTouch waits until the boot sweep has touched its FIRST profile
// — a sibling .chrome.lock appearing, or a planted cache directory going
// away. It returns the moment the sweep is provably in flight.
func firstProfileTouch(t *testing.T, profileRoot string, plantedDirs []string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		locks, _ := filepath.Glob(filepath.Join(profileRoot, ".*.chrome.lock"))
		if len(locks) > 0 {
			return
		}
		for _, dir := range plantedDirs {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("instrument: the boot cache-trim sweep never touched any of the %d planted profiles (looked for %v and .*.chrome.lock siblings)",
		trimKillProfiles, plantedDirs[:min(3, len(plantedDirs))])
}

// TestTrimSweep_JoinedBeforeHomeRemoval kills the writer mutant that drops
// the cache-trim goroutine from the boot WaitGroup, with the REAL pool and
// the REAL sweep: the drain must return only after the in-flight sweep has
// finished walking the profiles, so the home is unchanged afterwards.
func TestTrimSweep_JoinedBeforeHomeRemoval(t *testing.T) {
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	// Hermetic: this test needs the REAL pool and the REAL trim sweep, but
	// not the managed-Chromium fetch the same boot kicks off — on a networked
	// machine that would start a real download into the very home the
	// assertions below watch. The swap is this test's only transport effect
	// and is restored on cleanup.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = bootDownloadTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("trim-sweep instrument forbids external request: " + r.URL.Host)
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	profileRoot := filepath.Join(home, "browser", "profiles")
	var plantedDirs []string
	for i := 0; i < trimKillProfiles; i++ {
		profile := filepath.Join(profileRoot, "ws-tw"+fmt.Sprintf("%04d", i))
		cache := filepath.Join(profile, "Default", "Cache")
		require.NoError(t, os.MkdirAll(cache, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(cache, "blob"), []byte("cache-bytes"), 0o600))
		plantedDirs = append(plantedDirs, cache)
	}

	rc := &runContextWithOptions{ctx: context.Background(), homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err)
	require.False(t, stop)
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)
	stopBootWritersOnCleanup(t, rc)

	// The sweep is provably mid-flight here: at least one profile has been
	// locked or already trimmed, and the sweep never re-checks its context
	// until the whole walk is done.
	firstProfileTouch(t, profileRoot, plantedDirs)

	// The drain's budget is test-controlled (CHECK 4 R3, see the file
	// comment): no wall-clock deadline races the sweep's real workload.
	stopCtx, stopCancel := context.WithCancel(context.Background())
	defer stopCancel()
	drained := make(chan struct{})
	// The drain goroutine itself records the state on both sides of the
	// call, so the observations are taken at the moment the drain starts and
	// at the moment it returns — not whenever this goroutine gets scheduled
	// afterwards (CHECK 5 finding F1: an observer that resumes after a sweep
	// that outlived the drain sees a quiet home and cannot tell the order).
	// The fields are written before close(drained) and read after it.
	var atStart, atReturn []string
	var joinedAtReturn bool
	go func() {
		defer close(drained)
		atStart = plantedCachesRemaining(plantedDirs)
		rc.stopBootBackgroundWriters(stopCtx)
		atReturn = plantedCachesRemaining(plantedDirs)
		select {
		case <-rc.bootBackgroundDone:
			joinedAtReturn = true
		default:
		}
	}()
	awaitTrimDrainReturn(t, drained, stopCancel, profileRoot, plantedDirs)

	// Fixture activation: the join is only exercised if the sweep is still
	// walking when the drain begins. A sweep that had already finished by
	// then would make any early return legitimate and prove nothing.
	if len(atStart) == 0 {
		t.Fatalf("instrument: the boot cache-trim sweep was not in flight when the teardown drain started (all %d planted caches already trimmed); the join was not exercised",
			trimKillProfiles)
	}
	// Ordering at the moment of return: every planted profile must already be
	// trimmed and the done channel closed when stopBootBackgroundWriters
	// returned. A drain that returned while the sweep was still walking
	// fails here even if the sweep finishes before anything else looks.
	if !joinedAtReturn {
		t.Fatal("boot background writers were not joined by the teardown drain (checked at the moment it returned)")
	}
	if len(atReturn) > 0 {
		t.Fatalf("boot cache-trim sweep still had %d of %d planted profiles untrimmed at the moment the teardown drain returned (first: %v)",
			len(atReturn), trimKillProfiles, atReturn[:min(3, len(atReturn))])
	}

	select {
	case <-rc.bootBackgroundDone:
	default:
		t.Fatal("boot background writers were not joined by the teardown drain")
	}
	// The join covers the WHOLE boot sweep: when the drain returns, every
	// planted profile has been walked and its allow-listed cache removed
	// (FR-072 trigger 2 — none of them has a live Chrome or a held lock). A
	// drain that returned while the sweep was still walking leaves planted
	// caches behind and fails here, before the write-window check below.
	if remaining := plantedCachesRemaining(plantedDirs); len(remaining) > 0 {
		t.Fatalf("boot cache-trim sweep still had %d of %d planted profiles untrimmed when the teardown drain returned (first: %v)",
			len(remaining), trimKillProfiles, remaining[:min(3, len(remaining))])
	}
	// Post-drain the home must be EXACTLY as the drain left it. On the
	// unjoined mutant the remaining profiles' locks and cache removals land
	// here and fail this line.
	watchHomeQuiescent(t, home, homeTree(t, home))
}

// plantedCachesRemaining returns the planted cache directories that still
// exist. A stat error other than not-exist counts as remaining: the
// instrument must not read an unreadable path as "trimmed".
func plantedCachesRemaining(plantedDirs []string) []string {
	var remaining []string
	for _, dir := range plantedDirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			remaining = append(remaining, dir)
		}
	}
	return remaining
}

// awaitTrimDrainReturn waits for the teardown drain to return. Its only
// clock is a liveness guard: the go test deadline (minus a margin to report
// before the harness kills the binary), or none when the run has no
// deadline. The guard can only FAIL the test — it cancels the drain's
// context, waits for the drain to return, and reports a hang with the
// sweep's progress — so no load level can turn a slow sweep into a pass,
// and none can turn a correct join into the R3 false "not joined" failure.
func awaitTrimDrainReturn(t *testing.T, drained <-chan struct{}, cancel context.CancelFunc, profileRoot string, plantedDirs []string) {
	t.Helper()
	var guard <-chan time.Time
	if deadline, ok := t.Deadline(); ok {
		margin := 30 * time.Second
		timer := time.NewTimer(max(time.Until(deadline)-margin, time.Second))
		defer timer.Stop()
		guard = timer.C
	}
	select {
	case <-drained:
	case <-guard:
		cancel()
		<-drained
		t.Fatalf("teardown drain did not return before the test deadline: a hang, not a budget expiry; %d of %d planted profiles under %s still untrimmed",
			len(plantedCachesRemaining(plantedDirs)), trimKillProfiles, profileRoot)
	}
}
