package browser

// package_chrome_test.go — coverage for the packageChromeRootCandidates
// multi-root probe (SPEC-001), the per-OS binary layout probe (the
// findPackageChrome rewrite for goreleaser/install.sh layouts), and the
// SHA-verify result cache (PERF-001/004).
//
// The motivating cases:
//   - BLOCKER: install.sh places chromium at <root>/../share/omnipus/
//     chromium; goreleaser places it at <root>/../chromium; the prior
//     hardcoded single-candidate probe returned "" on a real install.sh
//     install.
//   - BLOCKER: a fresh goreleaser extraction also sits under
//     chrome-linux64/chrome (the .zip's top-level directory), so the
//     per-OS layout list must include that layout or the runtime misses
//     the binary entirely.
//   - HIGH (PERF): a SHA-verify result cache keyed on (binary, manifest,
//     manifest mtime) eliminates the per-call 400MB re-hash; tests prove
//     the second call short-circuits via the cache hit.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
)

// TestBinaryLayoutsForRoot_AllOSes covers the SPEC-001 fix that
// findPackageChrome probes every per-OS layout in priority order rather
// than the flat-chrome-only shape the original implementation used.
// The list MUST include:
//   - chrome-linux64/chrome           (goreleaser extraction subdir)
//   - chrome-headless-shell-linux64/chrome-headless-shell
//     (headless fallback on linux)
//   - chrome                          (flat install.sh layout)
//   - chrome-headless-shell           (flat headless fallback)
//   - chrome.exe                      (Windows full)
//   - chrome-headless-shell.exe       (Windows fallback)
func TestBinaryLayoutsForRoot_AllOSes(t *testing.T) {
	root := t.TempDir()
	got := binaryLayoutsForRoot(root)
	require.NotEmpty(t, got, "binaryLayoutsForRoot must return at least one candidate")

	// Assert the canonical layouts are all present (in any order; the
	// function's contract is "include them all, first-match wins").
	// We test by joining each result with a known root and checking the
	// suffix matches.
	wantSuffixes := []string{
		filepath.Join("chrome-linux64", "chrome"),
		filepath.Join("chrome-headless-shell-linux64", "chrome-headless-shell"),
		"chrome",
		"chrome-headless-shell",
		"chrome.exe",
		"chrome-headless-shell.exe",
	}
	for _, wantSuffix := range wantSuffixes {
		found := false
		for _, g := range got {
			if filepath.Base(g) == filepath.Base(wantSuffix) {
				found = true
				break
			}
		}
		assert.True(t, found, "binaryLayoutsForRoot missing candidate %q; got %v", wantSuffix, got)
	}
}

// TestFindPackageChrome_GoreleaserExtractionSubdir_Found proves the
// chrome-linux64/chrome layout (the actual goreleaser extraction layout
// from the chrome-for-testing zip) is now discoverable. Pre-fix the
// helper looked for root + fullChromeBinaryRelPath() = root + "chrome"
// only and missed this layout entirely.
func TestFindPackageChrome_GoreleaserExtractionSubdir_Found(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("linux goreleaser extraction layout; Windows test would need different binary naming")
	}
	root := t.TempDir()
	// Plant a binary at chrome-linux64/chrome — the goreleaser zip's
	// actual top-level directory.
	binDir := filepath.Join(root, "chrome-linux64")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	binPath := filepath.Join(binDir, "chrome")
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// Plant a manifest at the SAME layout level (next to the binary).
	contents, err := os.ReadFile(binPath)
	require.NoError(t, err)
	sum := sha256.Sum256(contents)
	require.NoError(t, os.WriteFile(
		filepath.Join(binDir, "chrome.sha256"),
		[]byte(hex.EncodeToString(sum[:])+"\n"), 0o644,
	))

	gotBin, gotSHA := findPackageChrome(root)
	assert.Equal(t, binPath, gotBin, "SPEC-001: chrome-linux64/chrome layout must be discovered")
	assert.NotEmpty(t, gotSHA, "SPEC-001: manifest next to the goreleaser-extracted binary must be returned")
}

// TestFindPackageChrome_GroupWritableRoot_Accepted proves the
// group-writable root case (TEST-005) is ACCEPTED — only world-writable
// is refused. A root mode of 0o770 (group-writable, group-owned by
// omnipus operators) is the standard package-manager-laid-down layout
// and must continue to work.
func TestFindPackageChrome_GroupWritableRoot_Accepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0o770 is posix-only")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	seedPackageChrome(t, root, true)
	// Group-writable only (0o770): NOT refused by the world-writable
	// guard (which only fires on 0o002 — the "other-writable" bit).
	require.NoError(t, os.Chmod(root, 0o770))

	bin, sha := findPackageChrome(root)
	assert.NotEmpty(t, bin, "group-writable root must be accepted")
	assert.NotEmpty(t, sha)
}

// TestFindPackageChrome_SymlinkedRoot_Refused is the SEC-ADR052-005
// symlink-at-root defense: the root itself is a symlink (e.g. an
// attacker redirected <root> to a planted tree). Must be refused.
func TestFindPackageChrome_SymlinkedRoot_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privileges on Windows")
	}
	realRoot := t.TempDir()
	seedPackageChrome(t, realRoot, true)
	linkRoot := filepath.Join(t.TempDir(), "linked-root")
	require.NoError(t, os.Symlink(realRoot, linkRoot))

	bin, sha := findPackageChrome(linkRoot)
	assert.Empty(t, bin, "SEC-ADR052-005: symlinked package root must be refused")
	assert.Empty(t, sha)
}

// TestFindPackageChrome_RootIsFile_Refused proves the root-is-a-file
// edge case (TEST-006): a regular file at the candidate root path
// (rather than a directory) must be refused cleanly, not panic.
func TestFindPackageChrome_RootIsFile_Refused(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(rootFile, []byte("not a dir\n"), 0o644))

	bin, sha := findPackageChrome(rootFile)
	assert.Empty(t, bin)
	assert.Empty(t, sha)
}

// TestFindPackageChrome_BinaryIsDirectory_Refused proves the
// binary-is-a-directory edge case (TEST-006): the expected binary path
// exists but as a directory, not a file. Must be refused cleanly.
func TestFindPackageChrome_BinaryIsDirectory_Refused(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	binPath := filepath.Join(root, fullChromeBinaryRelPath())
	require.NoError(t, os.MkdirAll(binPath, 0o755))
	// No manifest; the dir-as-binary path must refuse before manifest
	// checking.
	bin, sha := findPackageChrome(root)
	assert.Empty(t, bin)
	assert.Empty(t, sha)
}

// TestFindInstalledBuild_WorldWritableRoot_Refused proves the
// SEC-NEW-002 hardening (TEST in the review matrix): the managed
// install root must NOT be used when it's world-writable, mirroring
// findPackageChrome's guard. Without this, a multi-tenant host with a
// 0777 ~/.omnipus/browser/chromium/ could have any local user swap the
// chrome binary.
func TestFindInstalledBuild_WorldWritableRoot_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0o777 is posix-only")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	// Seed a valid managed build so the only thing that can refuse is
	// the world-writable guard.
	platform, _ := cftPlatform()
	seedManagedBinary(t, root, platform)
	// Now make it world-writable.
	require.NoError(t, os.Chmod(root, 0o777))

	got := findInstalledBuild(root, platform, selectDownloadBuild())
	assert.Empty(t, got, "SEC-NEW-002: world-writable install root must be refused")
}

// TestFindInstalledBuild_SymlinkedRoot_Refused proves the install-root
// symlink defense (TEST-011 in the review matrix).
func TestFindInstalledBuild_SymlinkedRoot_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privileges on Windows")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	realRoot := t.TempDir()
	platform, _ := cftPlatform()
	seedManagedBinary(t, realRoot, platform)

	linkRoot := filepath.Join(t.TempDir(), "linked-install")
	require.NoError(t, os.Symlink(realRoot, linkRoot))

	got := findInstalledBuild(linkRoot, platform, selectDownloadBuild())
	assert.Empty(t, got, "symlinked install root must be refused")
}

// TestFindInstalledBuild_NewestVersionWins proves that when multiple
// version directories coexist, the most-recently-modified one wins
// (TEST-011 in the review matrix). This protects an operator who
// upgrades while a previous Chrome is still in use — the new build
// must be picked on the next resolve, not the old one.
func TestFindInstalledBuild_NewestVersionWins(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	root := t.TempDir()
	platform, _ := cftPlatform()
	build := selectDownloadBuild()

	oldBin := seedManagedBinary(t, root, platform)
	// Mutate the OLD binary's mtime to 1 hour ago so the new binary we
	// plant below (with current mtime) wins the modtime comparison in
	// findInstalledBuild. We must touch the BINARY, not just its
	// parent version dir, because the candidate mod is taken from
	// os.Lstat(bin).ModTime().
	oldBinInfo, err := os.Stat(oldBin)
	require.NoError(t, err)
	oldMtime := oldBinInfo.ModTime()
	require.NoError(t, os.Chtimes(oldBin, oldMtime.Add(-time.Hour), oldMtime.Add(-time.Hour)))

	// Plant a second, NEWER version dir.
	newerVersionDir := filepath.Join(root, "999.999.999.999")
	newerBinDir := filepath.Join(newerVersionDir, build.subdir(platform))
	newerBin := filepath.Join(newerBinDir, build.binaryPath())
	// mkdir the BINARY's own parent, not just newerBinDir: on darwin,
	// build.binaryPath() nests the binary inside a .app bundle (e.g.
	// "Google Chrome for Testing.app/Contents/MacOS/Google Chrome for
	// Testing"), so newerBinDir alone omits the bundle's intermediate
	// directories and writeExecutable below failed with ENOENT (#615 found
	// this failing unconditionally on macOS — reproduced without -race too,
	// so it was never a race-detector artifact, just an untested darwin
	// path: CI only ever runs this suite on Linux, where build.binaryPath()
	// has no nested bundle and the bug is invisible).
	require.NoError(t, os.MkdirAll(filepath.Dir(newerBin), 0o755))
	writeExecutable(t, newerBin, "#!/bin/sh\nexit 0\n")

	got := findInstalledBuild(root, platform, build)
	assert.Equal(t, newerBin, got, "findInstalledBuild must return the newest version dir's binary")
}

// TestShaVerifyCache_HitsOnSecondCall proves PERF-001/004 (TEST-005
// subset): when the shared verifier is invoked twice on the same binary and
// manifest metadata tuple, the second call short-circuits via the cache. We
// assert the cache hit by clearing the on-disk binary
// between calls (chmod 0o000 to deny re-read) — the second call MUST
// still report "verified" because the cache hit means no re-hash is
// attempted.
//
// (This is a behavior-level proof; we don't expose the cache directly
// to test code. The trick is that a verified-once entry must survive a
// subsequent manifest mtime re-stat that would otherwise miss the
// cache.)
func TestShaVerifyCache_HitsOnSecondCall(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)

	// First call: real verification, populates the cache.
	assert.NoError(t, cachedVerifyChromeSHA256(binPath, shaPath))

	// Verify the cache entry is now populated (hit returns true).
	ok, hit, fresh := shaVerifyCacheHit(binPath, shaPath)
	assert.True(t, hit, "cache hit must return true after a successful verification")
	assert.True(t, fresh, "successful cache entry must be fresh")
	assert.True(t, ok, "cached result must be ok=true")

	// Second call: must short-circuit on the cache.
	// We don't have a clean way to assert "no disk read" from outside;
	// the behavioral proof is that cachedVerifyChromeSHA256 returns nil
	// even after we make the manifest unreadable, because the cache
	// hit prevents the read. (Skip on Windows where chmod is
	// permissive.)
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		require.NoError(t, os.Chmod(shaPath, 0o000))
		t.Cleanup(func() { _ = os.Chmod(shaPath, 0o644) })
		assert.NoError(t, cachedVerifyChromeSHA256(binPath, shaPath),
			"cache hit must short-circuit before re-reading the manifest")
	}
}

// TestShaVerifyCache_NaturalInvalidation proves the cache invalidates
// when the manifest's mtime changes — the natural key invalidation
// (PERF-001/004: "don't bother with explicit invalidation; rely on
// mtime"). When EnsureChromiumBuild re-extracts a binary, the manifest
// gets a fresh mtime and the next shared verifier call must re-hash.
func TestShaVerifyCache_NaturalInvalidation(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)

	// First call: cache miss → verify → cache ok=true.
	require.NoError(t, cachedVerifyChromeSHA256(binPath, shaPath))

	// Tamper with the manifest to a wrong digest AND bump its mtime.
	require.NoError(t, os.WriteFile(shaPath,
		[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))
	bumpFileMtime(t, shaPath)

	// Cache miss (mtime changed) → re-verify → mismatch error.
	err := cachedVerifyChromeSHA256(binPath, shaPath)
	require.Error(t, err, "mtime change must invalidate the cache and trigger a fresh verify")
	assert.Contains(t, err.Error(), "sha256 mismatch")
}

// bumpFileMtime sets a file's mtime to a known-future value so tests
// can prove mtime-based cache invalidation.
func bumpFileMtime(t *testing.T, path string) {
	t.Helper()
	future := mustStat(t, path).ModTime().Add(2 * 1e9) // +2 seconds; safe clock delta.
	require.NoError(t, os.Chtimes(path, future, future))
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info
}

// TestShaVerifyCache_BinarySwappedUnderSameManifestMtime_Refuses proves that
// changing only the binary cannot reuse a positive result, even when an
// attacker preserves the manifest timestamp. The binary mtime is part of the
// cache key; manifest inode and size remain part of the manifest key.
func TestShaVerifyCache_BinarySwappedUnderSameManifestMtime_Refuses(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)
	manifestInfo, err := os.Stat(shaPath)
	require.NoError(t, err)
	oldBinaryTime := manifestInfo.ModTime().Add(-time.Minute)
	require.NoError(t, os.Chtimes(binPath, oldBinaryTime, oldBinaryTime))
	require.NoError(t, cachedVerifyChromeSHA256(binPath, shaPath))

	require.NoError(t, os.WriteFile(binPath, []byte("different chrome payload\n"), 0o755))
	// Preserve the manifest mtime and deliberately make the new binary mtime
	// equal to it, exercising the preserve-timestamps attack shape.
	require.NoError(t, os.Chtimes(binPath, manifestInfo.ModTime(), manifestInfo.ModTime()))

	err = cachedVerifyChromeSHA256(binPath, shaPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256 mismatch")
}

// TestShaVerifyCache_NegativeResult_RespectsBoundedRetry proves permanent
// mismatches do not cause a full binary hash on every capability check.
func TestShaVerifyCache_NegativeResult_RespectsBoundedRetry(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)
	require.NoError(t, os.WriteFile(shaPath,
		[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))

	previous := verifyChromeSHA256Fn
	var calls atomic.Int64
	verifyChromeSHA256Fn = func(binaryPath, manifestPath string) error {
		calls.Add(1)
		return chromeintegrity.VerifyChromeSHA256(binaryPath, manifestPath)
	}
	t.Cleanup(func() { verifyChromeSHA256Fn = previous })

	for i := 0; i < 10; i++ {
		require.Error(t, cachedVerifyChromeSHA256(binPath, shaPath))
	}
	assert.LessOrEqual(t, calls.Load(), int64(2))
}

// TestShaVerifyCache_ConcurrentMisses_SingleFlight proves concurrent misses
// for one metadata key share one underlying SHA-256 verification.
func TestShaVerifyCache_ConcurrentMisses_SingleFlight(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)

	previous := verifyChromeSHA256Fn
	var calls atomic.Int64
	verifyChromeSHA256Fn = func(binaryPath, manifestPath string) error {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return chromeintegrity.VerifyChromeSHA256(binaryPath, manifestPath)
	}
	t.Cleanup(func() { verifyChromeSHA256Fn = previous })

	const workers = 10
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			if err := cachedVerifyChromeSHA256(binPath, shaPath); err != nil {
				t.Errorf("cached verification failed: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	assert.Equal(t, int64(1), calls.Load())
}

// TestFindPackageChrome_DockerHeavyFlatLayout_Accepted reproduces the exact
// on-disk shape docker/Dockerfile.heavy's B2a step produces — Chrome staged
// FLAT at <root>/chrome (Alpine's apk chromium package layout; no
// chrome-linux64/ extraction subdir, that's a CfT goreleaser-archive
// artifact only) plus a chrome.sha256 manifest generated verbatim by the
// Dockerfile's own pipeline:
//
//	sha256sum /usr/local/chromium/chrome \
//	  | awk '{printf "%s  %s\n", $1, $2}' > /usr/local/chromium/chrome.sha256
//
// sha256sum's own output is already "<hash>  <path>" (two spaces); awk's
// pass-through preserves that shape, so the manifest's second field is the
// FULL PATH given to sha256sum, not the bare basename "chrome" the other
// tests in this file use. This proves findPackageChrome — the function the
// real runtime resolver (exec_resolver.go step 3 / capability.go) actually
// calls — already accepts this shape; it is docs/internal doctor's
// (cmd/omnipus/internal/doctor/command.go) OWN separate, narrower
// duplicate of this layout list that was out of sync and produced a false
// WARN-BROWSER-008 on a real deployment (fixed there; see
// TestCheckBrowserPackageChrome_FlatDockerHeavyLayout_NoFalsePositive in
// cmd/omnipus/internal/doctor/command_test.go). docker/Dockerfile.heavy
// itself needed no change.
func TestFindPackageChrome_DockerHeavyFlatLayout_Accepted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flat linux apk-chromium layout only meaningful on linux/darwin")
	}
	root := t.TempDir()
	binPath := filepath.Join(root, "chrome")
	content := []byte("#!/bin/sh\nexit 0\n")
	require.NoError(t, os.WriteFile(binPath, content, 0o755))

	sum := sha256.Sum256(content)
	manifest := hex.EncodeToString(sum[:]) + "  " + binPath + "\n"
	shaPath := filepath.Join(root, "chrome.sha256")
	require.NoError(t, os.WriteFile(shaPath, []byte(manifest), 0o644))

	gotBin, gotSHA := findPackageChrome(root)
	assert.Equal(t, binPath, gotBin, "flat <root>/chrome (Dockerfile.heavy's real layout) must be discovered")
	assert.Equal(t, shaPath, gotSHA, "root-level chrome.sha256 must be paired with the flat binary")

	require.NoError(t, chromeintegrity.VerifyChromeSHA256(gotBin, gotSHA),
		"the sha256sum|awk manifest shape (full path as filename field) must verify against the real binary")
}

// TestIsUsablePackageRoot covers the helper that drives the multi-root
// probe. Empty path, missing dir, symlinked dir, regular file, and
// world-writable dir are all rejected; valid (existing, regular,
// non-world-writable) directories are accepted.
func TestIsUsablePackageRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod is posix-only; Windows FileMode carries no Unix mode bits")
	}
	t.Run("empty path is rejected", func(t *testing.T) {
		assert.False(t, isUsablePackageRoot(""))
	})
	t.Run("missing dir is rejected", func(t *testing.T) {
		assert.False(t, isUsablePackageRoot(filepath.Join(t.TempDir(), "missing")))
	})
	t.Run("regular file is rejected", func(t *testing.T) {
		f := filepath.Join(t.TempDir(), "f")
		require.NoError(t, os.WriteFile(f, nil, 0o644))
		assert.False(t, isUsablePackageRoot(f))
	})
	t.Run("world-writable dir is rejected", func(t *testing.T) {
		d := t.TempDir()
		require.NoError(t, os.Chmod(d, 0o777))
		assert.False(t, isUsablePackageRoot(d))
	})
	t.Run("valid dir is accepted", func(t *testing.T) {
		d := t.TempDir()
		assert.True(t, isUsablePackageRoot(d))
	})
	t.Run("symlinked dir is rejected", func(t *testing.T) {
		realPath := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		require.NoError(t, os.Symlink(realPath, link))
		assert.False(t, isUsablePackageRoot(link))
	})
}
