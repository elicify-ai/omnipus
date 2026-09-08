package browser

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ADR-075 FR-037: reload must preserve each workspace's launch/profile identity.
func TestPoolReloadPreservesWorkspaceProfiles(t *testing.T) {
	f := newPoolFixture(t)
	a := f.mustAcquire(t, "alpha")
	b := f.mustAcquire(t, "beta")
	cfg := f.pool.cfg
	cfg.IdleTTL = 7 * time.Minute
	cfg.ProfileDir = filepath.Join(t.TempDir(), "relocated", "default")
	f.pool.ApplyRuntimeConfig(cfg)
	for _, inst := range []*chromeInstance{a, b} {
		inst.coord.mu.Lock()
		got := inst.coord.cfg
		inst.coord.mu.Unlock()
		require.Equal(t, inst.profileDir, got.ProfileDir, "a reload must retain the workspace cookie jar")
		require.Equal(t, cfg.IdleTTL, got.IdleTTL, "runtime settings still apply")
		dir, err := f.pool.ProfileDirFor(inst.key)
		require.NoError(t, err)
		require.Equal(t, inst.profileDir, dir, "cleanup must still address the original profile until restart")
	}
	require.NotEqual(t, a.profileDir, b.profileDir)
}

// Deletion must not race a launcher that can still recreate the cookie jar.
func TestPoolCloseDrainsPendingStartupBeforeProfileDeletion(t *testing.T) {
	f := newPoolFixture(t)
	key := browserTestKey("pending-delete")
	entered, unblock := make(chan context.Context, 1), make(chan struct{})
	f.pool.newCoordinator = func(home string, cfg BrowserConfig, key BrowsingKey) *BrowserCoordinator {
		c := newKeyedCoordinator(home, cfg, key)
		c.pipeLauncher = func(ctx context.Context, _ string, _ pipeLaunchConfig) (*pipeLaunchResult, error) {
			entered <- ctx
			<-unblock
			root, cancel := context.WithCancel(context.Background())
			return &pipeLaunchResult{rootCtx: root, cancel: cancel}, nil
		}
		return c
	}
	done := make(chan error, 1)
	go func() { _, err := f.pool.Acquire(context.Background(), key); done <- err }()
	launch := <-entered
	closed := make(chan struct{})
	go func() { f.pool.Close(key); close(closed) }()
	canceled := false
	select {
	case <-launch.Done():
		canceled = true
	case <-time.After(200 * time.Millisecond):
	}
	premature := false
	select {
	case <-closed:
		premature = true
	default:
	}
	close(unblock)
	<-closed
	err := <-done
	require.True(t, canceled, "closing a key must retire its pending launcher")
	require.False(t, premature, "Close must wait until startup cleanup finishes")
	require.Error(t, err, "a retired startup cannot succeed")
	require.Empty(t, f.pool.LiveKeys())
	require.NoError(t, f.pool.DeleteProfile(key))
}

func TestPoolCloseJoinsConcurrentTeardown(t *testing.T) {
	f := newPoolFixture(t)
	inst := f.mustAcquire(t, "closing")
	entered, unblock := make(chan struct{}), make(chan struct{})
	inst.coord.mu.Lock()
	cancel := inst.coord.rootCancel
	inst.coord.rootCancel = func() { close(entered); <-unblock; cancel() }
	inst.coord.mu.Unlock()
	first := make(chan struct{})
	go func() { f.pool.Close(inst.key); close(first) }()
	<-entered
	second := make(chan struct{})
	go func() { f.pool.Close(inst.key); close(second) }()
	premature := false
	select {
	case <-second:
		premature = true
	case <-time.After(100 * time.Millisecond):
	}
	close(unblock)
	<-first
	<-second
	require.False(t, premature, "a repeated Close must join the running process teardown")
	// Ordinary close is reversible; the next request may reopen the workspace.
	next, err := f.pool.Acquire(context.Background(), inst.key)
	require.NoError(t, err)
	require.NotSame(t, inst, next)
}

func TestPoolDeletedProfileCannotBeReopenedByRetainedCaller(t *testing.T) {
	f := newPoolFixture(t)
	inst := f.mustAcquire(t, "deleted")
	f.pool.Close(inst.key)
	require.NoError(t, f.pool.DeleteProfile(inst.key))
	got, err := f.pool.Acquire(context.Background(), inst.key)
	require.ErrorContains(t, err, "deleted")
	require.Nil(t, got)
	require.Empty(t, f.pool.LiveKeys())
}

func TestPoolEvictionNewCallCannotUseRetiringBrowser(t *testing.T) {
	f := newPoolFixture(t)
	m := newTestManagerWithFakeTabs(t)
	m.memoryPressureFn = func(int) (bool, bool) { return false, true }
	old, err := m.Session(testSessionID)
	require.NoError(t, err)
	key := browserTestKey("retiring")
	m.AttachPool(f.pool, key)
	c, _, err := f.pool.Register(context.Background(), key, m)
	require.NoError(t, err)
	entered, unblock := make(chan struct{}), make(chan struct{})
	c.mu.Lock()
	cancel := c.rootCancel
	c.rootCancel = func() { close(entered); <-unblock; cancel() }
	c.mu.Unlock()
	closed := make(chan struct{})
	go func() { f.pool.Close(key); close(closed) }()
	<-entered
	leave := m.EnterCall()
	caller, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	got, callErr := m.SessionContext(caller, testSessionID)
	stop()
	leave()
	close(unblock)
	<-closed
	require.ErrorIs(t, callErr, context.DeadlineExceeded, "new calls wait for teardown, honoring their own deadline")
	require.Nil(t, got)
	require.Error(t, old.Err(), "the old tab lifetime was retired")
}

// FR-082's one-browser floor counts processes still exiting, not just map entries.
func TestPoolRetiringBrowserCountsTowardUnmeasurableFloor(t *testing.T) {
	f := newPoolFixture(t)
	inst := f.mustAcquire(t, "exiting")
	entered, unblock := make(chan struct{}), make(chan struct{})
	inst.coord.mu.Lock()
	cancel := inst.coord.rootCancel
	inst.coord.rootCancel = func() { close(entered); <-unblock; cancel() }
	inst.coord.mu.Unlock()
	closed := make(chan struct{})
	go func() { f.pool.Close(inst.key); close(closed) }()
	<-entered
	*f.measurable = false
	got, err := f.pool.Acquire(context.Background(), browserTestKey("other"))
	close(unblock)
	<-closed
	require.ErrorIs(t, err, ErrBrowserMemoryRefused)
	require.Nil(t, got)
	require.Empty(t, f.pool.LiveKeys())
}

func TestPoolRetirementClaimRechecksNewActivity(t *testing.T) {
	for _, kind := range []string{"call", "viewer", "tab"} {
		t.Run(kind, func(t *testing.T) {
			f := newPoolFixture(t)
			inst := f.mustAcquire(t, "selected")
			m := newTestManagerWithFakeTabs(t)
			m.memoryPressureFn = func(int) (bool, bool) { return false, true }
			t.Cleanup(m.Shutdown)
			f.pool.mu.Lock()
			inst.mgrs[m] = struct{}{}
			selected := f.pool.evictableLocked()
			f.pool.mu.Unlock()
			require.Same(t, inst, selected)
			mode := poolCloseEviction
			switch kind {
			case "call":
				leave := m.EnterCall()
				defer leave()
			case "viewer":
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				m.ViewerAttached(testSessionID)
			case "tab":
				_, err := m.Session(testSessionID)
				require.NoError(t, err)
				mode = poolCloseIdle
			}
			f.pool.mu.Lock()
			retirement := f.pool.claimInstanceLocked(selected, mode)
			f.pool.mu.Unlock()
			if retirement != nil {
				f.pool.finishRetirement(inst.key, retirement, "test cleanup after incorrect claim")
			}
			require.Nil(t, retirement, "activity after selection must prevent retirement")
			require.Equal(t, []string{inst.key.String()}, f.pool.LiveKeys())
			require.True(t, m.Started())
		})
	}
}
