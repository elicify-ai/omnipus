package browser

// #1730: the cache sweep observes the gateway's shutdown cancel between
// profiles, so the boot drain joins it promptly instead of running out its
// budget while the sweep keeps creating lock files and removing caches in a
// home the caller is removing.

import (
	"context"
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
