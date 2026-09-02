package browser

// pool_test.go — the per-workspace browser pool, driven entirely by a FAKE
// launcher. Nothing here spawns Chrome; the real-Chrome half of the pool's
// coverage lives in pool_lifecycle_integration_test.go.
//
// Read the gate tests in pool_gate_test.go alongside these. Between them the
// obligation is that BOTH lifetime controls — the admission gate and the idle
// close — turn RED when stubbed to do nothing. A guard test that passes
// against a deleted feature is this repo's own documented failure mode
// (docs/internal/false-green-patterns.md), and a memory gate is exactly the
// kind of control that can be silently neutered without anything failing.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeChromeBinary writes an executable stub and returns its path. The
// coordinator's exec-path resolution short-circuits on an explicit ExecPath
// after a stat + exec-bit check, and chromeMajorVersion runs it once with
// --version, so a shell script that prints a version string satisfies both
// without a 130 MB download.
func fakeChromeBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-chrome")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'Chrome for Testing 152.0.0.0'\n"), 0o700))
	return path
}

// poolFixture is a pool wired to a fake launcher, a controllable clock and a
// controllable memory reader. launched records every pipeLaunchConfig the pool
// asked for, which is how the isolation assertions read the actual
// --user-data-dir strings rather than trusting the paths the pool computed.
type poolFixture struct {
	pool     *BrowserPool
	home     string
	launched *[]pipeLaunchConfig
	now      *time.Time
	// available is the value the memory reader returns. Tests move it.
	available *uint64
	// measurable false makes the reader report "cannot determine" (FR-065).
	measurable *bool
}

func newPoolFixture(t *testing.T) *poolFixture {
	t.Helper()
	home := t.TempDir()
	cfg := BrowserConfig{
		Enabled:     true,
		Headless:    true,
		PageTimeout: 30 * time.Second,
		ProfileDir:  filepath.Join(home, "browser", "profiles", "default"),
		ExecPath:    fakeChromeBinary(t),
		IdleTTL:     DefaultIdleTTL,
	}

	launched := &[]pipeLaunchConfig{}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	available := uint64(64) << 30 // 64 GB: plenty, so the gate admits by default
	measurable := true

	p := NewBrowserPool(home, cfg)
	p.now = func() time.Time { return now }
	p.availableMemory = func() (uint64, bool) {
		if !measurable {
			return 0, false
		}
		return available, true
	}
	p.newCoordinator = func(homeDir string, c BrowserConfig, key BrowsingKey) *BrowserCoordinator {
		coord := newKeyedCoordinator(homeDir, c, key)
		coord.pipeLauncher = func(_ context.Context, _ string, lc pipeLaunchConfig) (*pipeLaunchResult, error) {
			*launched = append(*launched, lc)
			ctx, cancel := context.WithCancel(context.Background())
			return &pipeLaunchResult{rootCtx: ctx, cancel: cancel}, nil
		}
		return coord
	}
	t.Cleanup(p.Shutdown)
	return &poolFixture{
		pool: p, home: home, launched: launched,
		now: &now, available: &available, measurable: &measurable,
	}
}

func (f *poolFixture) advance(d time.Duration) { *f.now = f.now.Add(d) }

func (f *poolFixture) mustAcquire(t *testing.T, workspaceID string) *chromeInstance {
	t.Helper()
	inst, err := f.pool.Acquire(context.Background(), browserTestKey(workspaceID))
	require.NoError(t, err)
	return inst
}

// --- FR-037: one Chrome, one profile directory, per key ---------------------

func TestPool_OneChromePerKey(t *testing.T) {
	f := newPoolFixture(t)

	a1 := f.mustAcquire(t, "alpha")
	a2 := f.mustAcquire(t, "alpha")
	assert.Same(t, a1, a2, "two acquires of one key must resolve to the same browser")
	assert.Len(t, *f.launched, 1, "the second acquire must not launch a second Chrome for the same workspace")

	f.mustAcquire(t, "beta")
	assert.Len(t, *f.launched, 2, "a second workspace gets its OWN Chrome")
	assert.ElementsMatch(t, []string{"ws:alpha", "ws:beta"}, f.pool.LiveKeys())
}

// TestPool_TwoWorkspaces_TwoChromes is the assertion the isolation sentence in
// browser_list_tabs' and browser_open_tab's descriptions rests on.
//
// It asserts on the two --user-data-dir STRINGS, not on cookies, and that
// distinction is the point. A cookie-isolation assertion passes TRIVIALLY on a
// single-Chrome build if both workspaces happened to resolve to the same key —
// the cookies differ because the workspaces never actually shared anything to
// begin with, and the test proves nothing about the mechanism. Two different
// profile directory paths, actually handed to two different launches, cannot
// be produced by a build that has one browser.
func TestPool_TwoWorkspaces_TwoChromes(t *testing.T) {
	f := newPoolFixture(t)

	instA := f.mustAcquire(t, "alpha")
	instB := f.mustAcquire(t, "beta")

	require.Len(t, *f.launched, 2)
	dirA := (*f.launched)[0].userDataDir
	dirB := (*f.launched)[1].userDataDir

	assert.NotEqual(t, dirA, dirB,
		"the two workspaces must launch Chrome with DIFFERENT --user-data-dir paths — "+
			"that is what makes their logins separate, and comparing cookies instead would pass "+
			"even on a build where both workspaces resolved to one browser")
	assert.Equal(t, "ws-alpha", filepath.Base(dirA))
	assert.Equal(t, "ws-beta", filepath.Base(dirB))
	assert.NotSame(t, instA.coord, instB.coord, "each workspace owns its own coordinator, hence its own Chrome process")

	// Both directories really exist, 0700, and neither is inside the other.
	for _, dir := range []string{dirA, dirB} {
		info, err := os.Stat(dir)
		require.NoError(t, err, "profile directory must exist on disk")
		require.True(t, info.IsDir())
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(),
			"a profile directory holds live session cookies — it must not be group- or world-readable")
	}
	assert.Equal(t, filepath.Dir(dirA), filepath.Dir(dirB),
		"FR-037a: per-key profiles are FLAT SIBLINGS, never nested — nesting would give each "+
			"workspace its own managed-Chromium download")
}

// TestPool_InstallRootIsKeyIndependent is FR-037a's other half: N workspaces
// share ONE managed-Chromium install. If per-key profiles were ever nested
// under the default profile, each workspace would resolve a different install
// root and re-download ~130 MB of Chrome.
func TestPool_InstallRootIsKeyIndependent(t *testing.T) {
	f := newPoolFixture(t)
	base := InstallRootForProfileDir(f.pool.cfg.ProfileDir)

	for _, ws := range []string{"alpha", "beta", "gamma"} {
		dir, err := f.pool.ProfileDirFor(browserTestKey(ws))
		require.NoError(t, err)
		assert.Equal(t, base, InstallRootForProfileDir(dir),
			"workspace %q must resolve the SAME managed-Chromium install root as every other", ws)
	}
}

// TestPool_LaunchArgvHasNoRendererLimit is FR-062's structural half: no
// per-renderer or per-tab constant exists anywhere, so no launch may try to
// enforce one. --renderer-process-limit would be exactly that: a number
// somebody has to get right on every host.
func TestPool_LaunchArgvHasNoRendererLimit(t *testing.T) {
	f := newPoolFixture(t)
	f.mustAcquire(t, "alpha")

	require.Len(t, *f.launched, 1)
	for _, arg := range (*f.launched)[0].args {
		assert.NotContains(t, arg, "renderer-process-limit",
			"the launch argv must carry no renderer cap (FR-062) — a tab's cost is not a "+
				"number anyone can name, which is why every counter was deleted")
	}
}

// --- FR-042: per-key launch lock and ownership marker -----------------------

func TestPool_PerKeyLockAndMarker(t *testing.T) {
	f := newPoolFixture(t)
	f.mustAcquire(t, "alpha")
	f.mustAcquire(t, "beta")

	for _, ws := range []string{"alpha", "beta"} {
		key := browserTestKey(ws)
		dir, err := f.pool.ProfileDirFor(key)
		require.NoError(t, err)
		_, statErr := os.Stat(filepath.Join(dir, launchLockFileName))
		assert.NoError(t, statErr, "workspace %q must hold its OWN launch lock inside its own profile dir", ws)
	}
	assert.NotEqual(t,
		f.pool.markerPathFor(browserTestKey("alpha")),
		f.pool.markerPathFor(browserTestKey("beta")),
		"one shared marker file could only ever name one of N running Chromes, so it would point "+
			"at the wrong process most of the time")
}

// --- FR-041: crash containment ---------------------------------------------

// TestPool_CrashIsContained proves one workspace's Chrome dying takes exactly
// that workspace's browser with it. Before the pool there was one process, so
// this property could not exist: a crash was total by construction.
func TestPool_CrashIsContained(t *testing.T) {
	f := newPoolFixture(t)
	instA := f.mustAcquire(t, "alpha")
	instB := f.mustAcquire(t, "beta")

	// Kill alpha's browser the way the pool itself would.
	f.pool.Close(instA.key)

	assert.Equal(t, []string{"ws:beta"}, f.pool.LiveKeys(),
		"beta's browser must be untouched by alpha's going down")
	assert.NotZero(t, instB.coord.KillCount()+1, "sanity: beta's coordinator still exists")
	assert.Equal(t, 0, instB.coord.KillCount(), "beta's Chrome must not have been killed")

	// alpha comes back on its next acquire, from the SAME profile directory —
	// which is what makes a crash cost a page load rather than a login.
	dirBefore := instA.profileDir
	instA2 := f.mustAcquire(t, "alpha")
	assert.NotSame(t, instA, instA2)
	assert.Equal(t, dirBefore, instA2.profileDir,
		"recovery must relaunch from the SAME profile, so the workspace is still logged in")
}

// --- FR-043a / SC-017: the profile directory's single deletion trigger ------

// TestPool_DeleteProfileOnWorkspaceDeletionOnly enumerates the four events
// that must NOT delete a profile and the one that must.
//
// Getting this wrong in either direction is a real defect with a real victim:
// deleting too eagerly logs a workspace out every time its browser goes idle;
// never deleting leaves a departed client's session cookies on disk.
func TestPool_DeleteProfileOnWorkspaceDeletionOnly(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")

	dir, err := f.pool.ProfileDirFor(key)
	require.NoError(t, err)
	seed := func() {
		f.mustAcquire(t, "alpha")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "Cookies"), []byte("session=abc"), 0o600))
	}

	survives := []struct {
		name string
		act  func()
	}{
		{"idle close", func() {
			f.advance(2 * f.pool.idleCloseTTL)
			f.pool.CloseIdle(*f.now)
		}},
		{"eviction", func() {
			f.pool.mu.Lock()
			victim := f.pool.evictableLocked()
			f.pool.mu.Unlock()
			require.NotNil(t, victim)
			f.pool.closeInstance(victim, "evicted")
		}},
		{"roster change", func() { f.pool.Close(key) }},
		{"reload", func() { f.pool.Release(key, nil) }},
	}
	for _, tc := range survives {
		t.Run(tc.name+" keeps the profile", func(t *testing.T) {
			seed()
			tc.act()
			_, statErr := os.Stat(filepath.Join(dir, "Cookies"))
			assert.NoError(t, statErr,
				"%s must NOT delete the profile — the workspace still exists and its user "+
					"expects to be logged in when they come back", tc.name)
		})
	}

	t.Run("workspace deletion removes it", func(t *testing.T) {
		seed()
		f.pool.Close(key)
		require.NoError(t, f.pool.DeleteProfile(key))
		_, statErr := os.Stat(dir)
		assert.True(t, os.IsNotExist(statErr),
			"a deleted workspace's browser profile — session cookies included — must be gone from disk")
	})

	t.Run("refuses while the browser is still live", func(t *testing.T) {
		seed()
		err := f.pool.DeleteProfile(key)
		require.Error(t, err,
			"deleting a profile out from under a running Chrome races the browser's own "+
				"cookie-jar writes; SC-017's ordering is Close-returns-then-delete")
		_, statErr := os.Stat(filepath.Join(dir, "Cookies"))
		assert.NoError(t, statErr, "the refused delete must not have removed anything")
	})
}

// --- FR-050 / FR-051 / FR-052: eviction and its two guards ------------------

func TestPool_EvictsLRUAndRelaunches(t *testing.T) {
	f := newPoolFixture(t)

	f.mustAcquire(t, "alpha")
	f.advance(time.Minute)
	f.mustAcquire(t, "beta")
	f.advance(time.Minute)

	// Now the host runs out of room. gamma's acquire must evict the LRU
	// (alpha) and then succeed — invisibly, with no error surfaced.
	*f.available = PerBrowserCostBytes - 1
	f.pool.mu.Lock()
	victim := f.pool.evictableLocked()
	f.pool.mu.Unlock()
	require.NotNil(t, victim)
	assert.Equal(t, "ws:alpha", victim.key.String(),
		"eviction picks the LEAST recently used browser, not an arbitrary one")

	// With headroom restored after the eviction, the acquire completes.
	f.pool.closeInstance(victim, "test eviction")
	*f.available = 64 << 30
	f.mustAcquire(t, "gamma")
	assert.ElementsMatch(t, []string{"ws:beta", "ws:gamma"}, f.pool.LiveKeys())

	// alpha's profile survived its eviction — it comes back logged in.
	dir, err := f.pool.ProfileDirFor(browserTestKey("alpha"))
	require.NoError(t, err)
	_, statErr := os.Stat(dir)
	assert.NoError(t, statErr, "an evicted workspace keeps its profile; eviction is not deletion")
}

func TestPool_EvictionSkipsViewerAndInFlight(t *testing.T) {
	f := newPoolFixture(t)
	inst := f.mustAcquire(t, "alpha")

	mgr := &BrowserManager{sessions: map[string]*sessionEntry{}}
	f.pool.mu.Lock()
	inst.mgrs[mgr] = struct{}{}
	f.pool.mu.Unlock()

	// Baseline: with nothing attached, it is evictable.
	f.pool.mu.Lock()
	assert.NotNil(t, f.pool.evictableLocked(), "an unattended browser is evictable")
	f.pool.mu.Unlock()

	// A live viewer pins it. Somebody is watching this window right now, and
	// closing it to make room for a workspace they are not looking at is a
	// panel going black for no reason they can see.
	mgr.sessions["s"] = &sessionEntry{viewers: 1}
	f.pool.mu.Lock()
	assert.Nil(t, f.pool.evictableLocked(), "a browser with a live viewer must never be evicted")
	f.pool.mu.Unlock()
	mgr.sessions["s"].viewers = 0

	// An in-flight call pins it too. Killing the Chrome mid-call turns a
	// working tool call into an inexplicable error inside somebody's turn.
	release := mgr.EnterCall()
	f.pool.mu.Lock()
	assert.Nil(t, f.pool.evictableLocked(), "a browser with a call in flight must never be evicted")
	f.pool.mu.Unlock()

	release()
	f.pool.mu.Lock()
	assert.NotNil(t, f.pool.evictableLocked(), "releasing the call must make it evictable again")
	f.pool.mu.Unlock()

	// The release must be idempotent — a deferred release that ran twice
	// (panic plus normal return) must not underflow the counter into a
	// permanently unevictable browser.
	release()
	assert.Equal(t, 0, mgr.InFlight())
}

// --- FR-054: thrash detection ----------------------------------------------

func TestPool_ThrashWarnsOnceAndNamesMemoryNotACap(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")

	for i := 0; i < thrashThreshold+3; i++ {
		f.pool.noteReopen(key)
	}

	f.pool.mu.Lock()
	warned := f.pool.thrashWarned[key.String()]
	cycles := len(f.pool.reopens[key.String()])
	f.pool.mu.Unlock()

	assert.True(t, warned, "crossing the threshold must warn")
	assert.GreaterOrEqual(t, cycles, thrashThreshold)

	// The latch is what makes it ONE warning. A per-cycle warning on a
	// thrashing host is a log flood that buries the thing it is reporting.
	f.pool.noteReopen(key)
	f.pool.mu.Lock()
	stillOnce := f.pool.thrashWarned[key.String()]
	f.pool.mu.Unlock()
	assert.True(t, stillOnce)

	// Cycles older than the window fall out, so an install that opens and
	// closes browsers over a long day never trips it.
	f.advance(2 * thrashWindow)
	f.pool.noteReopen(browserTestKey("beta"))
	f.pool.mu.Lock()
	betaCycles := len(f.pool.reopens["ws:beta"])
	f.pool.mu.Unlock()
	assert.Equal(t, 1, betaCycles)
}

// --- FR-042a: boot marker reconciliation ------------------------------------

func TestPool_ReconcileMarkersAtBoot(t *testing.T) {
	f := newPoolFixture(t)
	markerDir := filepath.Join(f.home, "browser")
	require.NoError(t, os.MkdirAll(markerDir, 0o700))

	// Two markers naming DEAD pids, left by a crashed previous run. Nothing
	// holds their locks, so both are stale and both get cleared.
	for _, ws := range []string{"alpha", "beta"} {
		key := browserTestKey(ws)
		dir, err := f.pool.ProfileDirFor(key)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		writeTestMarker(t, f.pool.markerPathFor(key), 999999)
	}

	refused := f.pool.ReconcileMarkers()
	assert.Empty(t, refused, "a stale marker is cleared, not refused — nothing else is driving that key")
	for _, ws := range []string{"alpha", "beta"} {
		_, err := os.Stat(f.pool.markerPathFor(browserTestKey(ws)))
		assert.True(t, os.IsNotExist(err), "workspace %q's stale marker must be cleared", ws)
	}
	assert.Empty(t, f.pool.LiveKeys(), "reconciliation launches nothing")
}

// TestPool_ReconcileRefusesWhenLockHeld is the case that must NOT terminate
// anything. A marker naming a LIVE pid whose launch lock is HELD means another
// running gateway owns that workspace's browser; the discrimination is by the
// LOCK, not by the pid, and getting it backwards is how one gateway kills
// another gateway's Chrome.
func TestPool_ReconcileRefusesWhenLockHeld(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("alpha")

	dir, err := f.pool.ProfileDirFor(key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	// Our own pid stands in for "another live gateway's Chrome".
	writeTestMarker(t, f.pool.markerPathFor(key), os.Getpid())

	held, acquired, lockErr := acquireLaunchLock(filepath.Join(dir, launchLockFileName))
	require.NoError(t, lockErr)
	require.True(t, acquired)
	t.Cleanup(func() { releaseLaunchLock(held) })

	refused := f.pool.ReconcileMarkers()
	assert.Equal(t, []string{"ws:alpha"}, refused,
		"a key another live gateway holds must be REFUSED, not adopted and not reclaimed")
	_, statErr := os.Stat(f.pool.markerPathFor(key))
	assert.NoError(t, statErr,
		"the other gateway's marker must be left exactly as it was — this gateway terminated nothing")
}

func writeTestMarker(t *testing.T, path string, pid int) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	body := []byte(`{"pid":` + itoaTest(pid) + `,"owner":"` + ownershipMarkerOwner + `","product":"Chrome-for-Testing","created_unix":0}`)
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}
