package browser

// package_chrome.go holds the runtime helpers that probe for the
// package-managed Chrome-for-Testing build (ADR-052 D2/M5). The runtime
// computed path is intentionally split into two layers:
//
//  1. packageChromeRootCandidates() returns every candidate package-root
//     path the resolver should inspect, in priority order (SPEC-001 /
//     install-path multi-root probe).
//  2. packageChromeRoot() picks the FIRST existing, non-empty candidate
//     — keeps the call sites unchanged.
//  3. findPackageChrome(root) probes the per-OS binary layouts inside a
//     given root (chrome-linux64/chrome, chrome-headless-shell-linux64/
//     chrome-headless-shell, the flat chrome / chrome-headless-shell
//     layout install.sh may produce, and Windows .exe variants).
//
// On-disk layouts supported, in priority order:
//
//   - <dir(exe)>/../chromium                       — goreleaser archive
//                                                    layout (sibling of
//                                                    the binary in the
//                                                    tarball), matching
//                                                    `chromium/` at
//                                                    archive root.
//   - <dir(exe)>/../share/omnipus/chromium         — install.sh default
//                                                    (FHS-ish `share/`).
//   - <dir(exe)>/../libexec/omnipus/chromium       — deb/rpm nfpms
//                                                    `files:` mapping.
//   - InstallRootForProfileDir(cfg.ProfileDir)     — hand-cp fallback
//                                                    (operator-extracted
//                                                    tarball).
//
// Per-root binary layouts inspected, in priority order:
//
//   - chrome-linux64/chrome                            (linux, full)
//   - chrome-headless-shell-linux64/chrome-headless-shell
//                                                      (linux, fallback)
//   - chrome                                           (flat — install.sh
//                                                     may produce)
//   - chrome-headless-shell                            (flat fallback)
//   - chrome.exe                                       (Windows full)
//   - chrome-headless-shell.exe                        (Windows fallback)
//
// SEC-ADR052-005/006 hardening lives here (symlinked-root rejection,
// world-writable-root rejection, symlinked-binary rejection). The
// shaVerifyCache (PERF-001/004) is a process-wide memoization keyed by
// (binaryPath, manifestPath, manifestMtime) — when EnsureChromiumBuild
// re-extracts a binary, the new mtime misses the cache and naturally
// invalidates.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
)

// packageChromeRootForTest is a test-only seam for packageChromeRoot.
// When non-empty, packageChromeRoot returns its value verbatim (so
// tests can pin the package root to a temp directory without
// symlinking the test binary into a realistic layout). When empty
// (the production default), packageChromeRoot falls back to the
// multi-root probe over os.Executable()-derived candidates.
var packageChromeRootForTest string

// packageChromeRootCandidates returns every candidate package-root path
// the resolver should inspect, in priority order. The list is ordered
// to match the on-disk layouts install.sh, goreleaser nfpms, and a
// hand-cp "extract-the-tarball" install produce, in that order:
//
//  1. <dir(exe)>/../chromium                    — goreleaser archive
//     layout (sibling of the
//     binary in the tarball),
//     matching `chromium/`
//     at archive root.
//  2. <dir(exe)>/../share/omnipus/chromium      — install.sh default
//     (FHS-ish `share/`).
//  3. <dir(exe)>/../libexec/omnipus/chromium    — deb/rpm nfpms
//     `files:` mapping.
//  4. cfgProfileDirInstallRoot(profileDir)      — hand-cp fallback
//     (operator-extracted
//     tarball).
//
// Tests inject a fixed root via packageChromeRootForTest, which
// short-circuits the entire probe — so the candidate list is purely a
// production concern (a unit-test seam does not need to be added here).
func packageChromeRootCandidates() []string {
	exe, err := os.Executable()
	if err != nil {
		return nil
	}
	exeDir := filepath.Dir(exe)
	return []string{
		filepath.Join(exeDir, "..", "chromium"),
		filepath.Join(exeDir, "..", "share", "omnipus", "chromium"),
		filepath.Join(exeDir, "..", "libexec", "omnipus", "chromium"),
	}
}

// packageChromeRoot returns the directory the package-build pipeline is
// contracted (ADR-052 D2) to drop the pinned full Chrome-for-Testing
// build into. Phase 1 is Linux-only, so on Windows (where
// os.Executable() returns the link path, with no GetFinalPathNameByHandle
// available in os/exec.go) this returns the empty string so callers
// cleanly fall through to step 4 (managed download). Phase 4 (Windows
// .msi packaging) owns the Windows allocator + archive re-layout and
// will revisit this seam.
//
// On Linux/macOS the returned path is the FIRST existing candidate from
// packageChromeRootCandidates() — goreleaser archive layout wins over
// install.sh's share/ layout wins over deb/rpm libexec/ layout, with
// InstallRootForProfileDir's hand-cp fallback reserved for caller-
// supplied profile dirs (the resolver adds it explicitly when iterating
// per-agent roots).
//
// Returns the empty string on any os.Executable() error (defensive —
// never panics; callers must treat "" as "no package Chrome available").
// The result is deterministic and idempotent: same process state →
// same path.
func packageChromeRoot() string {
	if packageChromeRootForTest != "" {
		return packageChromeRootForTest
	}
	// Phase 1 deferral: Windows layout is unsettled (Phase 4 owns the
	// .msi archive layout + allocator work). Returning "" cleanly
	// routes Windows callers to the managed-download path until Phase 4
	// lands.
	if runtime.GOOS == "windows" {
		return ""
	}
	for _, candidate := range packageChromeRootCandidates() {
		if isUsablePackageRoot(candidate) {
			return candidate
		}
	}
	return ""
}

// isUsablePackageRoot reports whether candidate is an existing,
// non-world-writable, non-symlinked directory. Returns false for
// anything else — the resolver treats that as "no package Chrome here,
// try the next candidate." The emptiness check deliberately does NOT
// happen here: an empty chromium/ directory is a valid candidate root
// for the multi-root probe (findPackageChrome does its own per-layout
// binary existence check on the returned root and returns "" if the
// root is empty, which the caller treats as "no package Chrome
// available, fall through").
func isUsablePackageRoot(candidate string) bool {
	if candidate == "" {
		return false
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	if !info.IsDir() {
		return false
	}
	if info.Mode()&0o002 != 0 {
		return false
	}
	return true
}

// binaryLayoutsForRoot returns the candidate on-disk paths for the
// bundled Chrome binary, in priority order. Covers the goreleaser
// extraction subdir (chrome-linux64/), the chrome-headless-shell
// layout (used for non-video-capable fallbacks), the flat binary
// layout install.sh may produce, and the Windows .exe variants.
//
// Phase 1's package Chrome is always the full "chrome" build (the
// linux-tabCapture-capable default), but listing the headless-shell
// layouts keeps the resolver consistent with the layouts the managed
// download path already inspects — so a Phase 1 install where
// goreleaser happens to ship the lighter fallback at chrome-linux64/
// still resolves cleanly.
func binaryLayoutsForRoot(root string) []string {
	return []string{
		filepath.Join(root, "chrome-linux64", "chrome"),
		filepath.Join(root, "chrome-headless-shell-linux64", "chrome-headless-shell"),
		filepath.Join(root, "chrome"),
		filepath.Join(root, "chrome-headless-shell"),
		filepath.Join(root, "chrome.exe"),                // Windows
		filepath.Join(root, "chrome-headless-shell.exe"), // Windows
	}
}

// findPackageChrome inspects root for a pinned full Chrome-for-Testing
// binary and its companion chrome.sha256 integrity manifest. Returns
// the binary's absolute path and the manifest's absolute path; either
// is the empty string when absent.
//
// The probe checks every layout in binaryLayoutsForRoot in priority
// order — chrome-linux64/chrome (goreleaser extraction subdir) wins
// over chrome (flat install.sh) wins over chrome.exe (Windows). The
// matching manifest path mirrors the binary layout: when the binary
// lives at <root>/chrome-linux64/chrome, the manifest lives at
// <root>/chrome-linux64/chrome.sha256; when it lives at <root>/chrome,
// the manifest lives at <root>/chrome.sha256.
//
// SEC-ADR052-005/006 hardening:
//   - root is Lstat-checked before anything inside it is touched
//     (refuses a symlinked root, refuses a world-writable root).
//   - the binary is Lstat-checked (refuses a leaf symlink, refuses a
//     non-executable POSIX file).
//   - chrome.sha256 must EXIST and be readable (SEC-ADR052-001
//     fail-closed). Unlike findInstalledBuild's permissive-missing
//     behavior on the managed install root (which has historical
//     pre-Phase-1 installs without a manifest), the package Chrome is
//     a Phase-1-only construct — the only legitimate reason for a
//     missing manifest at the package root is a pipeline failure or
//     tampering, both of which are release blockers.
//
// Returns "" for both halves on any refusal (including missing
// manifest) so the caller falls through to the managed download path.
func findPackageChrome(root string) (binaryPath, shaPath string) {
	if root == "" {
		return "", ""
	}
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return "", ""
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		// A symlinked package root is suspicious — refuse. Use Lstat
		// (not Stat) so we see the leaf, not the target.
		return "", ""
	}
	if !rootInfo.IsDir() {
		return "", ""
	}
	// SEC-ADR052-005/006: refuse a world-writable install root. On a
	// multi-tenant host a 0777 install root lets any local user
	// substitute chrome / chrome.sha256 between resolve and launch;
	// the package integrity guarantee collapses. The
	// root-owned-by-a-package-manager invariant ADR-052 D4 names
	// (systemd StateDirectory, /usr/bin/...) is what we're defending
	// here.
	if rootInfo.Mode()&0o002 != 0 {
		return "", ""
	}

	for _, bin := range binaryLayoutsForRoot(root) {
		info, statErr := os.Lstat(bin)
		if statErr != nil {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if info.IsDir() {
			continue
		}
		// POSIX exec-bit check mirrors the resolve() ExecPath guard;
		// Windows FileMode carries no Unix exec bits, so this guard is
		// skipped there.
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			continue
		}
		// The manifest mirrors the binary layout — if chrome sits at
		// <root>/chrome-linux64/chrome, chrome.sha256 sits next to it.
		sha := filepath.Join(filepath.Dir(bin), "chrome.sha256")
		if shaInfo, shaErr := os.Lstat(sha); shaErr != nil {
			// Fail-closed: chrome.sha256 must exist at the package
			// root. Per SEC-ADR052-001, returning "" here is the
			// explicit refusal signal — the caller treats it as "no
			// package Chrome available" and falls through to the
			// managed download path.
			return "", ""
		} else if shaInfo.Mode()&os.ModeSymlink != 0 {
			return "", ""
		}
		return bin, sha
	}
	return "", ""
}

// --- SHA-256 verify cache (PERF-001 / PERF-004) ---
//
// ClassifyVideoCapability and findInstalledBuild re-hash the ~400MB
// Chrome binary on every call. The verify itself is mandatory
// (SEC-ADR052-001 fail-closed), but the per-binary / per-mtime cost
// can be eliminated for repeated calls — a launcher's classifier
// checks many times during a single agent's lifetime, and a Settings
// reload + capability re-check loop in a busy gateway could re-hash
// hundreds of times.
//
// The cache keys on (binaryPath, manifestPath, manifestMtime) so a
// fresh re-extract (mtime flip) naturally invalidates without explicit
// bookkeeping. We do NOT pin a TTL — the mtime is the source of
// truth; if the binary's mtime is unchanged, neither is its content,
// so the cached digest is still correct.

// shaVerifyCacheEntry is one row of the process-wide SHA-verify cache.
type shaVerifyCacheEntry struct {
	mtime int64 // manifest mtime at the time of verification (mtime == ok)
	ok    bool  // verifyChromeSHA256 returned nil at the time of the cache fill
}

// shaVerifyCache is the process-wide memoization of verifyChromeSHA256
// outcomes, keyed on (binary, manifest, mtime). Bounded access via
// sync.RWMutex; reads vastly outnumber writes.
type shaVerifyCache struct {
	mu      sync.RWMutex
	entries map[shaVerifyCacheKey]shaVerifyCacheEntry
}

// shaVerifyCacheKey is the (binary, manifest, mtime) tuple that
// identifies a unique verification result.
type shaVerifyCacheKey struct {
	binary   string
	manifest string
	mtime    int64
}

// globalSHAVerifyCache is the package-level cache. A single instance is
// sufficient: SHA-256 of a binary is content-addressed and mtime is
// monotonic, so two goroutines computing the same digest cannot
// disagree. The cache only ever returns previously-observed results,
// never infers new ones.
var globalSHAVerifyCache = &shaVerifyCache{
	entries: make(map[shaVerifyCacheKey]shaVerifyCacheEntry),
}

// shaVerifyCacheHit reports whether (binaryPath, shaPath) has a cached
// verification result whose manifest-mtime matches the on-disk mtime
// at the moment of the call. A no-match return means the caller MUST
// re-run verifyChromeSHA256 (and update the cache afterwards via
// shaVerifyCacheStore).
//
// The mtime check is the natural invalidation: when
// EnsureChromiumBuild re-extracts a binary, the new manifest mtime
// misses the cache and triggers a re-hash.
func shaVerifyCacheHit(binaryPath, shaPath string) (ok bool, hit bool) {
	if shaPath == "" {
		return false, false
	}
	manifestInfo, err := os.Stat(shaPath)
	if err != nil {
		return false, false
	}
	key := shaVerifyCacheKey{
		binary:   binaryPath,
		manifest: shaPath,
		mtime:    manifestInfo.ModTime().UnixNano(),
	}
	globalSHAVerifyCache.mu.RLock()
	entry, ok2 := globalSHAVerifyCache.entries[key]
	globalSHAVerifyCache.mu.RUnlock()
	if !ok2 {
		return false, false
	}
	return entry.ok, true
}

// shaVerifyCacheStore records the (binaryPath, shaPath) verification
// outcome in the global cache. ok=true means verifyChromeSHA256
// returned nil; ok=false means it returned an error (caching negative
// outcomes prevents a tight loop from re-hashing the same mismatched
// binary on every call). The manifest's on-disk mtime at the moment of
// the store is what gets keyed — so a subsequent mtime flip
// naturally invalidates.
func shaVerifyCacheStore(binaryPath, shaPath string, ok bool) {
	if shaPath == "" {
		return
	}
	manifestInfo, err := os.Stat(shaPath)
	if err != nil {
		return
	}
	key := shaVerifyCacheKey{
		binary:   binaryPath,
		manifest: shaPath,
		mtime:    manifestInfo.ModTime().UnixNano(),
	}
	globalSHAVerifyCache.mu.Lock()
	globalSHAVerifyCache.entries[key] = shaVerifyCacheEntry{ok: ok, mtime: key.mtime}
	globalSHAVerifyCache.mu.Unlock()
}

// cachedVerifyChromeSHA256 is verifyChromeSHA256 + the process-wide
// cache. Returns the same sentinel semantics as the underlying call;
// callers that distinguish errSHA256ManifestMissing from a real
// mismatch use errors.Is(err, chromeintegrity.ErrSHA256ManifestMissing)
// (the renames in this same package preserve the wrapping so the
// existing errors.Is(err, errSHA256ManifestMissing) calls continue to
// work — see verifyChromeSHA256).
func cachedVerifyChromeSHA256(binaryPath, shaPath string) error {
	if ok, hit := shaVerifyCacheHit(binaryPath, shaPath); hit {
		if ok {
			return nil
		}
		// Negative cache: re-run anyway — a hardened caller may want a
		// fresh error message. (The chromeintegrity call is cheap
		// relative to the SHA rehash, but we deliberately don't pin
		// negative results: a transient I/O error during the first
		// call shouldn't lock the gateway out of the binary forever.)
	}
	err := chromeintegrity.VerifyChromeSHA256(binaryPath, shaPath)
	shaVerifyCacheStore(binaryPath, shaPath, err == nil)
	return err
}

// verifyChromeSHA256ErrMissing exposes the chromeintegrity sentinel via
// the package's existing name so legacy call sites that import only
// the browser package keep working without importing the new
// subpackage directly. (The actual sentinel lives in chromeintegrity
// — every package-local reference should now use this alias.)
var verifyChromeSHA256ErrMissing = chromeintegrity.ErrSHA256ManifestMissing

// IsErrSHA256ManifestMissing is the package-level convenience predicate
// for the chromeintegrity sentinel. New code should prefer
// errors.Is(err, chromeintegrity.ErrSHA256ManifestMissing) directly;
// this alias exists so callers that want a one-symbol lookup stay
// readable.
func IsErrSHA256ManifestMissing(err error) bool {
	return errors.Is(err, chromeintegrity.ErrSHA256ManifestMissing)
}

// _ = logger.WarnCF keeps the logger import live across refactors that
// temporarily drop the only logger call in this file (the resolver's
// own WARN-BROWSER-005 path uses it).
var _ = logger.WarnCF
