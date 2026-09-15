//go:build unix

package browser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SEC-ADR052-002 forbids execution, including discovery probes, before opt-in.
func TestLaunchSecurityPATHProbeRequiresTrust(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		name := "untrusted"
		if trusted {
			name = "trusted"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "google-chrome")
			marker := filepath.Join(dir, "executed")
			writeExecutable(t, bin, "#!/bin/sh\nprintf invoked > \"$OMNIPUS_PROBE_TEST_MARKER\"\nprintf 'Chromium 151.0.0.0\\n'\n")
			t.Setenv("OMNIPUS_PROBE_TEST_MARKER", marker)
			t.Setenv("PATH", dir)
			t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")
			pkgRoot := t.TempDir()
			pkgBin, _ := seedPackageChrome(t, pkgRoot, true)
			withPackageChromeRoot(t, pkgRoot)
			cfg := newExecPathTestConfig(t, t.TempDir())
			cfg.TrustPathChrome = trusted
			var cache execPathCaches
			got, err := cache.resolve(context.Background(), cfg)
			require.NoError(t, err)
			if trusted {
				require.Equal(t, bin, got)
				body, err := os.ReadFile(marker)
				require.NoError(t, err)
				require.Equal(t, "invoked", string(body))
			} else {
				require.Equal(t, pkgBin, got)
				_, err := os.Stat(marker)
				require.ErrorIs(t, err, os.ErrNotExist, "an untrusted executable must never run")
			}
		})
	}
}

// A live Unix flock is authoritative even before Chrome writes its PID marker.
func TestLaunchSecurityMarkerlessHeldLockRemainsExclusive(t *testing.T) {
	// Only filesystem locking is exercised; resolving Chrome could download it.
	cfg, home := budgetTestConfig(t)
	c := NewBrowserCoordinator(home, cfg)
	require.NoError(t, os.MkdirAll(cfg.ProfileDir, 0o700))
	held, acquired, err := acquireLaunchLock(c.lockPath())
	require.NoError(t, err)
	require.True(t, acquired)
	defer releaseLaunchLock(held)
	before, err := os.Stat(c.lockPath())
	require.NoError(t, err)
	second, err := c.takeLaunchLock()
	if second != nil {
		defer releaseLaunchLock(second)
	}
	require.ErrorContains(t, err, "launch lock")
	require.Nil(t, second)
	after, err := os.Stat(c.lockPath())
	require.NoError(t, err)
	require.True(t, os.SameFile(before, after), "held lock pathname must still identify the locked inode")
}
