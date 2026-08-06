package browser

// exec_resolver_cache_replay_test.go — regression coverage for the B2c(ii)
// fix (fix/uat-v0.1.1-defects): the negative exec-path cache used to replay
// a stale resolution failure verbatim, with no re-check, for the full 60s
// TTL (execPathNegativeCacheTTL). In the field this meant an agent could
// tell a user a Chromium binary "does not exist" while `find` showed it
// present with a complete install — the binary had finished
// downloading/being fixed AFTER the failure was cached, but the cache never
// noticed. resolve() now cheaply re-os.Stat's the specific binary path a
// cached failure was about before replaying it, and evicts + re-resolves
// when that binary now exists; any error actually replayed from the cache
// is annotated with its age so it is never presented as a fresh,
// present-tense fact.
//
// Also covers B2c(i): BrowserManager.InvalidateExecPathCache, the per-agent
// counterpart to BrowserCoordinator.ApplyRuntimeConfig's cache invalidation
// — see that method's doc comment in manager.go for why it is currently a
// defensive no-op against production's wholesale-manager-replacement-on-
// reload behavior, and what future refactor it guards against.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolve_NegativeCache_StaleFailure_EvictedOnceBinaryFixed is the core
// REVERT-PROOF test for B2c(ii). It drives resolve() through its normal
// public entry point (BrowserManager.resolveExecPath) — no direct access to
// execPathCaches' internals — so it compiles and runs unmodified against
// both the pre-fix and post-fix exec_resolver.go.
//
// Sequence:
//  1. $PATH and the package-Chrome root are both made to miss cleanly, so
//     resolution falls to step 4 (managed install).
//  2. A managed binary is seeded at the exact layout EnsureChromium expects,
//     but BROKEN (exits 1) — the first resolve() call fails the
//     probeChromiumBinary --version check and negative-caches the failure,
//     recording this exact binary path.
//  3. The SAME on-disk file is then fixed in place (rewritten to exit 0),
//     simulating "the install finished" or "an operator fixed a corrupt
//     install" — the scenario the RCA's field repro describes.
//  4. A second resolve() call, still well within the 60s negative-cache
//     TTL, must succeed and return the (now-working) managed binary path —
//     NOT replay the stale "did not execute successfully" error.
//
// Pre-fix, step 4 fails: the negative cache returns the ORIGINAL cached
// error verbatim for the full TTL with no re-check, so the second resolve()
// call still errors even though the binary genuinely now works.
func TestResolve_NegativeCache_StaleFailure_EvictedOnceBinaryFixed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	// Step 2 (PATH) must miss cleanly: point PATH at an empty directory so
	// exec.LookPath finds none of the four candidate names.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	// Step 3 (package Chrome) must miss cleanly: point the package-root seam
	// at a directory that does not exist, mirroring the existing
	// TestResolve_Step4_* tests' pattern (exec_resolver_phase1_test.go).
	withPackageChromeRoot(t, filepath.Join(t.TempDir(), "does-not-exist"))

	cfg := newExecPathTestConfig(t, t.TempDir())
	installRoot := installRootFor(cfg)
	platform, err := cftPlatform()
	require.NoError(t, err)

	// Seed the managed binary BROKEN — probeChromiumBinary's --version exec
	// fails, exactly like a mid-download/corrupt-extraction binary would.
	binPath := seedManagedBinary(t, installRoot, platform)
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	m := &BrowserManager{cfg: cfg}

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err, "first resolve() must fail: the seeded managed binary exits non-zero")
	assert.Contains(t, err.Error(), "did not execute successfully")

	// Fix the SAME on-disk binary in place — no new download, no cache
	// manipulation, just the file changing underneath the cached failure.
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err,
		"second resolve(), still within the negative-cache TTL, must re-verify and succeed once the "+
			"binary is fixed — not replay the stale failure verbatim (B2c(ii) regression)")
	assert.Equal(t, binPath, got)
}

// TestResolve_NegativeCache_GenuineFailure_StillReplayedWithinTTL is the
// companion "did not over-fix" check: when the cached failure's binary
// genuinely still does not exist — e.g. an operator followed the error
// message's OWN advice ("the install may be corrupt — remove %s and
// retry") but has not yet supplied a working replacement — a second
// resolve() call within the TTL must still report a failure (never
// silently succeed against a binary that is not there), and per B2c(ii)
// the replayed message must now carry a cache-age annotation rather than
// presenting the original diagnosis as a fresh, present-tense fact.
func TestResolve_NegativeCache_GenuineFailure_StillReplayedWithinTTL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")
	withPackageChromeRoot(t, filepath.Join(t.TempDir(), "does-not-exist"))

	cfg := newExecPathTestConfig(t, t.TempDir())
	installRoot := installRootFor(cfg)
	platform, err := cftPlatform()
	require.NoError(t, err)

	binPath := seedManagedBinary(t, installRoot, platform)
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	m := &BrowserManager{cfg: cfg}

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not execute successfully")

	// Operator follows the error's own remediation advice and removes the
	// corrupt install, but has not (yet) supplied a working replacement —
	// binPath is now genuinely absent, not just broken.
	require.NoError(t, os.RemoveAll(installRoot))

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err, "a genuinely still-missing binary must still fail on replay")
	assert.Contains(t, err.Error(), "did not execute successfully",
		"the underlying diagnosis must survive the replay")
	assert.Contains(t, err.Error(), "cached result from",
		"a replayed negative-cache error must be labeled as a cached result, not a fresh present-tense fact (B2c(ii))")
}

// TestResolve_NegativeCache_UnexpectedStatError_SymlinkLoop is the
// UID-INDEPENDENT regression test for the 7-reviewer-gate Finding 3 fix: the
// negative-cache replay branch's re-stat of the cached-about binary used to
// discard a non-not-exist statErr silently, with NO log at all — unlike its
// two siblings (the "stale, now exists" branch logs InfoCF; the no-failPath
// branch logs DebugCF). If the TRUE CAUSE changed after the failure was
// cached (permissions revoked, an I/O error, a symlink loop — anything
// other than plain ENOENT), an operator saw only the STALE original
// diagnosis, age-annotated, for the rest of the TTL: exactly the
// misleading-cached-diagnosis pattern this branch already fixed once for
// the "binary now exists" direction.
//
// A self-referential symlink at the cached failure's path makes os.Stat
// fail with ELOOP ("too many levels of symbolic links") rather than a
// plain not-exist. Symlink-loop detection is a loop-prevention mechanism,
// NOT a DAC permission check, so — unlike a chmod-based permission-denied
// repro — root does not bypass it: this test needs no root/uid skip guard
// and exercises the identical branch as
// TestResolve_NegativeCache_UnexpectedStatError_PermissionDenied below on
// any POSIX host, including as root in CI.
func TestResolve_NegativeCache_UnexpectedStatError_SymlinkLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix symlink-loop test double")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")
	withPackageChromeRoot(t, filepath.Join(t.TempDir(), "does-not-exist"))

	cfg := newExecPathTestConfig(t, t.TempDir())
	installRoot := installRootFor(cfg)
	platform, err := cftPlatform()
	require.NoError(t, err)

	binPath := seedManagedBinary(t, installRoot, platform)
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	m := &BrowserManager{cfg: cfg}

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not execute successfully")

	// Replace the cached-about binary with a symlink pointing at itself —
	// os.Stat on it fails with ELOOP, a re-stat error that is neither a
	// plain not-exist NOR ever bypassed by root/DAC-override, unlike a
	// chmod-based permission-denied repro.
	require.NoError(t, os.Remove(binPath))
	require.NoError(t, os.Symlink(filepath.Base(binPath), binPath))

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err, "an unexpected (non-not-exist) re-stat error must still fail closed, never silently succeed")
	assert.Contains(t, err.Error(), "did not execute successfully",
		"the original diagnosis must still be present")
	assert.Contains(t, err.Error(), "cached result from",
		"still a replayed negative-cache result, must carry the standard age annotation")
	assert.Contains(t, err.Error(), "unexpectedly",
		"an unexpected (non-not-exist) re-stat failure must now be surfaced, not silently dropped like before this fix")
	assert.NotContains(t, err.Error(), "no such file or directory",
		"this is NOT the plain-ENOENT genuine-failure case -- asserting the wrong branch fired would be a false pass")
}

// TestResolve_NegativeCache_UnexpectedStatError_PermissionDenied is the
// real-world-cause companion to the symlink-loop test above: the parent
// directory of the cached failure's binary loses search (execute)
// permission after the failure was cached (an operator's own permission
// hardening, a package manager reinstall, etc.), so os.Stat(failPath) fails
// with EACCES rather than ENOENT.
//
// This pod runs as uid 1000, so a chmod'd parent directory genuinely
// reproduces EACCES here — but the SAME test would SILENTLY SKIP THE REAL
// CHECK under root, because root bypasses the standard DAC execute-
// permission check entirely (CAP_DAC_OVERRIDE) and the chmod'd directory
// would simply remain readable. CI for this repo runs as root, so this
// guards explicitly with a t.Skip rather than repeating the exact
// root/uid trap that already broke a test on this branch once (see the
// project's "No Force Merge"-adjacent lesson in the branch history). The
// symlink-loop test above is the uid-independent variant that gives real
// coverage of this same branch under root too.
func TestResolve_NegativeCache_UnexpectedStatError_PermissionDenied(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix chmod test double")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: DAC execute-permission checks are bypassed (CAP_DAC_OVERRIDE), so a " +
			"chmod'd parent directory would NOT reproduce EACCES here -- see the uid-independent " +
			"TestResolve_NegativeCache_UnexpectedStatError_SymlinkLoop for coverage that holds under root too")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")
	withPackageChromeRoot(t, filepath.Join(t.TempDir(), "does-not-exist"))

	cfg := newExecPathTestConfig(t, t.TempDir())
	installRoot := installRootFor(cfg)
	platform, err := cftPlatform()
	require.NoError(t, err)

	binPath := seedManagedBinary(t, installRoot, platform)
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 1\n"), 0o755))

	m := &BrowserManager{cfg: cfg}

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not execute successfully")

	// Strip search permission from the binary's immediate parent directory
	// AFTER the failure was cached -- os.Stat(binPath) now fails with
	// EACCES, not ENOENT.
	versionDir := filepath.Dir(binPath)
	require.NoError(t, os.Chmod(versionDir, 0o000))
	t.Cleanup(func() {
		// Restore BEFORE t.TempDir()'s own removal cleanup runs (that was
		// registered earlier in the test; t.Cleanup runs LIFO, so this
		// later registration runs first) so the temp tree can actually be
		// deleted afterward.
		_ = os.Chmod(versionDir, 0o755)
	})

	_, err = m.resolveExecPath(context.Background())
	require.Error(t, err, "an unexpected (non-not-exist) re-stat error must still fail closed, never silently succeed")
	assert.Contains(t, err.Error(), "did not execute successfully",
		"the original diagnosis must still be present")
	assert.Contains(t, err.Error(), "cached result from",
		"still a replayed negative-cache result, must carry the standard age annotation")
	assert.Contains(t, err.Error(), "unexpectedly",
		"an unexpected (non-not-exist) re-stat failure must now be surfaced, not silently dropped like before this fix")
	assert.Contains(t, err.Error(), "permission denied",
		"the concrete EACCES detail must reach the caller, not just a generic annotation")
}

// TestBrowserManager_InvalidateExecPathCache_ClearsSuccessAndFailure is the
// unit-level regression test for B2c(i): InvalidateExecPathCache must clear
// BOTH the success and negative-cache halves of the manager's exec-path
// cache, mirroring execPathCaches.invalidate()'s own contract (already
// covered indirectly via the coordinator's ApplyRuntimeConfig tests, but
// never for the manager, which had no invalidation call path at all before
// this fix).
func TestBrowserManager_InvalidateExecPathCache_ClearsSuccessAndFailure(t *testing.T) {
	m := &BrowserManager{}

	m.execPath.cacheSuccess("/some/cached/chrome")
	require.Equal(t, "/some/cached/chrome", m.execPath.cachedPath())

	m.InvalidateExecPathCache()
	assert.Empty(t, m.execPath.cachedPath(), "InvalidateExecPathCache must clear the success cache")

	m.execPath.cacheFailure(errSyntheticCacheFailure, "/some/failed/path")
	m.execPath.mu.Lock()
	failErrSet := m.execPath.failErr != nil
	m.execPath.mu.Unlock()
	require.True(t, failErrSet, "precondition: failure cache must be populated before invalidating")

	m.InvalidateExecPathCache()
	m.execPath.mu.Lock()
	defer m.execPath.mu.Unlock()
	assert.Nil(t, m.execPath.failErr, "InvalidateExecPathCache must clear the negative cache")
	assert.True(t, m.execPath.failUntil.IsZero())
	assert.Empty(t, m.execPath.failPath)
}

var errSyntheticCacheFailure = errors.New("synthetic cached failure for InvalidateExecPathCache test")
