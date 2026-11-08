package gateway

// The boot cache-trim sweep's WaitGroup membership (TempDir-cleanup family
// #1644/#1695, CHECK 1 finding F5). The trim goroutine's exit after its
// cancel is near-instant, so the only observable of a MISSING join is the
// sweep still WALKING profiles when the drain has already returned: the
// sweep is ctx-unaware (TrimAllEligible runs to completion), and per
// profile it CREATES a sibling .chrome.lock file and REMOVES the allow-listed
// cache directories. This test plants enough real profiles that the sweep
// outlasts the drain, waits for the first profile to be touched (the sweep
// is provably in flight), drains, and then requires the home to be
// completely unchanged — additions, removals and resizes all fail. Post-fix
// the drain waits out the whole sweep inside the join; on the mutant that
// dropped the trim goroutine from the WaitGroup the drain returns early and
// the remaining profiles are trimmed after it did.
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

	stopCtx, stopCancel := context.WithTimeout(context.Background(), bootBackgroundStopTimeout)
	defer stopCancel()
	rc.stopBootBackgroundWriters(stopCtx)

	select {
	case <-rc.bootBackgroundDone:
	default:
		t.Fatal("boot background writers were not joined by the teardown drain")
	}
	// Post-drain the home must be EXACTLY as the drain left it. On the
	// unjoined mutant the remaining profiles' locks and cache removals land
	// here and fail this line.
	watchHomeQuiescent(t, home, homeTree(t, home))
}
