package browser

// #1730: the cache sweep observes the gateway's shutdown cancel between
// profiles, so the boot drain joins it promptly instead of running out its
// budget while the sweep keeps creating lock files and removing caches in a
// home the caller is removing.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func seedClosedProfiles(t *testing.T, f *poolFixture, workspaces ...string) []string {
	t.Helper()
	var caches []string
	for _, ws := range workspaces {
		dir, err := f.pool.ProfileDirFor(browserTestKey(ws))
		if err != nil {
			t.Fatalf("profile dir for %s: %v", ws, err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create profile %s: %v", ws, err)
		}
		seedProfileTree(t, dir)
		caches = append(caches, filepath.Join(dir, "Default", "Cache", "data_0"))
	}
	return caches
}

// TestTrimAllEligibleContext_CancelledTouchesNoProfile: a sweep whose context
// is already cancelled must not trim (or lock) a single profile.
func TestTrimAllEligibleContext_CancelledTouchesNoProfile(t *testing.T) {
	f := newPoolFixture(t)
	caches := seedClosedProfiles(t, f, "alpha", "beta", "gamma")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := f.pool.TrimAllEligibleContext(ctx)

	if len(results) != 0 {
		t.Errorf("cache sweep kept trimming after cancellation (#1730): %d profiles trimmed, want none", len(results))
	}
	for _, c := range caches {
		if _, err := os.Stat(c); err != nil {
			t.Errorf("cache sweep kept trimming after cancellation (#1730): %s is gone (%v)", c, err)
		}
	}
	for _, ws := range []string{"alpha", "beta", "gamma"} {
		requireNoLaunchLock(t, f, ws)
	}
}

// requireNoLaunchLock fails if the sweep created the profile's sibling launch
// lock file, i.e. it started trimming that profile.
func requireNoLaunchLock(t *testing.T, f *poolFixture, ws string) {
	t.Helper()
	dir, err := f.pool.ProfileDirFor(browserTestKey(ws))
	if err != nil {
		t.Fatalf("profile dir for %s: %v", ws, err)
	}
	lock := profileLaunchLockPath(dir)
	if _, statErr := os.Stat(lock); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("cache sweep kept trimming after cancellation (#1730): launch lock %s exists for %s (stat err %v)", lock, ws, statErr)
	}
}

// TestTrimAllEligibleContext_CancelledBetweenProfilesLeavesLaterProfilesUntouched
// is the in-flight witness: the cancel arrives only after the first profile
// (alpha; the sweep walks the profile root in name order) has been trimmed,
// the moment the sweep asks its context before the next profile. beta and
// gamma must keep their caches and get no launch lock. A sweep that checks its
// context once, before the walk, sees a live context there and trims them.
func TestTrimAllEligibleContext_CancelledBetweenProfilesLeavesLaterProfilesUntouched(t *testing.T) {
	f := newPoolFixture(t)
	caches := seedClosedProfiles(t, f, "alpha", "beta", "gamma")
	ctx := newProgressCancelCtx(t, func() bool {
		_, err := os.Stat(caches[0])
		return errors.Is(err, os.ErrNotExist)
	})

	results := f.pool.TrimAllEligibleContext(ctx)

	if !ctx.fired.Load() {
		t.Error("cache sweep never consulted its context after alpha was trimmed (#1730): no cancellation point between profiles, so the cancel was never delivered")
	}
	if len(results) != 1 || results[0].Key.String() != "ws:alpha" {
		t.Errorf("cache sweep cancelled after alpha trimmed %d profiles %v, want only ws:alpha (#1730)", len(results), results)
	}
	for i, ws := range []string{"beta", "gamma"} {
		if _, err := os.Stat(caches[i+1]); err != nil {
			t.Errorf("cache sweep kept trimming after a cancel between profiles (#1730): %s cache %s is gone (%v)", ws, caches[i+1], err)
		}
		requireNoLaunchLock(t, f, ws)
	}
}

// TestTrimAllEligibleContext_PositiveControl: the same fixture with a live
// context trims every profile, so the cancelled case cannot pass because the
// fixture has nothing eligible.
func TestTrimAllEligibleContext_PositiveControl(t *testing.T) {
	f := newPoolFixture(t)
	caches := seedClosedProfiles(t, f, "alpha", "beta", "gamma")

	results := f.pool.TrimAllEligibleContext(context.Background())

	if len(results) != len(caches) {
		t.Errorf("live sweep trimmed %d profiles, want %d", len(results), len(caches))
	}
	for _, c := range caches {
		if _, err := os.Stat(c); !os.IsNotExist(err) {
			t.Errorf("live sweep left %s in place (stat err %v)", c, err)
		}
	}
}
