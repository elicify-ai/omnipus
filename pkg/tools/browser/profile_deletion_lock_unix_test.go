//go:build unix

package browser

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func profileDeletionFixture(t *testing.T) (*poolFixture, BrowsingKey, string, *BrowserCoordinator) {
	t.Helper()
	f := newPoolFixture(t)
	key := browserTestKey("profile-delete")
	dir, err := f.pool.ProfileDirFor(key)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Cookies"), []byte("saved-login"), 0o600))
	cfg := f.pool.cfg
	cfg.ProfileDir = dir
	return f, key, dir, newKeyedCoordinator(f.home, cfg, key)
}

func TestProfileDeletionRefusesAnotherCoordinatorLock(t *testing.T) {
	f, key, dir, other := profileDeletionFixture(t)
	held, err := other.takeLaunchLock()
	require.NoError(t, err)
	defer releaseLaunchLock(held)
	assert.Error(t, f.pool.DeleteProfile(key), "local absence must not authorize deleting another coordinator's profile")
	body, err := os.ReadFile(filepath.Join(dir, "Cookies"))
	require.NoError(t, err)
	assert.Equal(t, "saved-login", string(body))
}

func TestProfileDeletionRefusesHeldLegacyLock(t *testing.T) {
	f, key, dir, other := profileDeletionFixture(t)
	held, ok, err := acquireLaunchLock(filepath.Join(dir, "chrome.lock"))
	require.NoError(t, err)
	require.True(t, ok)
	defer func() { releaseLaunchLock(held) }()
	second, err := other.takeLaunchLock()
	if second != nil {
		releaseLaunchLock(second)
	}
	assert.Error(t, err, "a running older coordinator must still exclude a new launcher")
	writeTestMarker(t, f.pool.markerPathFor(key), os.Getpid())
	assert.True(t, f.pool.TrimProfile(key).Skipped, "trimming must honor an existing legacy owner")
	assert.Equal(t, []string{key.String()}, f.pool.ReconcileMarkers(), "reconciliation must retain an existing legacy owner")
	assert.Error(t, f.pool.DeleteProfile(key), "a held legacy lock must still protect its profile")
	body, err := os.ReadFile(filepath.Join(dir, "Cookies"))
	require.NoError(t, err)
	assert.Equal(t, "saved-login", string(body))
	releaseLaunchLock(held)
	held = nil
	probe, err := other.takeLaunchLock()
	require.NoError(t, err, "legacy-owner refusals must not leak the new sibling lock")
	releaseLaunchLock(probe)
}

func TestProfileDeletionExcludesLaunchAfterDirectoryRemoval(t *testing.T) {
	f, key, dir, coord := profileDeletionFixture(t)
	removed, unblock := make(chan error, 1), make(chan struct{})
	f.pool.removeProfileDir = func(path string) error {
		err := os.RemoveAll(path)
		removed <- err
		<-unblock
		return err
	}
	done := make(chan error, 1)
	go func() { done <- f.pool.DeleteProfile(key) }()
	rmErr := <-removed
	other := NewBrowserPool(f.home, f.pool.cfg)
	other.newCoordinator = f.pool.newCoordinator
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	inst, launchErr := other.launch(ctx, key, coord.cfg)
	cancel()
	if inst != nil {
		inst.coord.Shutdown()
	}
	close(unblock)
	deleteErr := <-done
	require.NoError(t, rmErr)
	require.NoError(t, deleteErr)
	assert.ErrorContains(t, launchErr, "launch lock", "deletion must retain exclusion even after removing the old in-profile lock")
	assert.Nil(t, inst)
	assert.NoDirExists(t, dir, "a refused concurrent launch must not recreate the profile before taking its lock")
}

func TestProfileDeletionPreservesSiblingLockIdentity(t *testing.T) {
	f, key, dir, coord := profileDeletionFixture(t)
	held, err := coord.takeLaunchLock()
	require.NoError(t, err)
	before, err := held.Stat()
	require.NoError(t, err)
	releaseLaunchLock(held)
	assert.Equal(t, filepath.Dir(dir), filepath.Dir(coord.lockPath()), "the deletion guard must live outside the removed tree")
	require.NoError(t, f.pool.DeleteProfile(key))
	assert.NoDirExists(t, dir)
	after, err := os.Stat(coord.lockPath())
	require.NoError(t, err, "deletion must not unlink the shared lock")
	assert.True(t, os.SameFile(before, after))
	assert.Empty(t, f.pool.TrimAllEligible(), "a sibling lock is not a profile to sweep")
	assert.Empty(t, f.pool.ReconcileMarkers(), "a sibling lock is not an ownership marker")
	probe, err := coord.takeLaunchLock()
	require.NoError(t, err, "completed deletion must release its lock")
	releaseLaunchLock(probe)
}

func TestProfileDeletionFailureReleasesLockForRetry(t *testing.T) {
	f, key, dir, coord := profileDeletionFixture(t)
	fault := errors.New("injected filesystem refusal")
	f.pool.removeProfileDir = func(string) error { return fault }
	require.ErrorIs(t, f.pool.DeleteProfile(key), fault)
	body, err := os.ReadFile(filepath.Join(dir, "Cookies"))
	require.NoError(t, err)
	require.Equal(t, "saved-login", string(body))
	probe, err := coord.takeLaunchLock()
	require.NoError(t, err, "a failed delete must release its launch lock")
	releaseLaunchLock(probe)
	f.pool.removeProfileDir = nil
	require.NoError(t, f.pool.DeleteProfile(key))
	assert.NoDirExists(t, dir)
}
