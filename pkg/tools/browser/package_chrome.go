package browser

// package_chrome.go holds the runtime helpers that probe for the
// package-managed Chrome-for-Testing build (ADR-052 D2/M5). The runtime
// computed path is intentionally split into three layers:
//
//  1. packageChromeRootCandidates() returns every candidate package-root
//     path the resolver should inspect, in priority order (SPEC-001 /
//     install-path multi-root probe).
//  2. packageChromeRootProbe() validates each candidate and selects the first
//     one containing a usable Chrome payload; packageChromeRoot() exposes only
//     the usable path for simple runtime callers.
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
//   - <dir(exe)>/../chromium  [darwin]             — non-.app parity
//                                                    (homebrew / dev build,
//                                                    or Contents/chromium).
//   - <dir(exe)>/../../../chromium  [darwin]       — C3 option (ii):
//                                                    Google-signed sibling
//                                                    beside the gateway .app.
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
//   - Google Chrome for Testing.app/Contents/MacOS/   (macOS .app bundle,
//     Google Chrome for Testing                         C3 option ii)
//
// SEC-ADR052-005/006 hardening lives here (symlinked-root rejection,
// world-writable-root rejection, symlinked-binary rejection). The
// shaVerifyCache memoizes integrity results using both binary and manifest
// metadata, and single-flights concurrent misses.

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
)

// packageChromeRootForTest is a test-only seam for packageChromeRoot.
// When non-empty, packageChromeRoot validates the injected root using the same
// checks as production candidates. An empty value uses osExecutable and the
// real multi-root probe.
var packageChromeRootForTest string

// osExecutable is the seam for os.Executable — replaceable in tests to
// exercise the executable-resolution error path that production defends
// against.
var osExecutable = os.Executable

// packageChromeRootCandidates returns every candidate package-root path
// the resolver should inspect, in priority order. The list covers the three
// package layouts produced by the goreleaser archive, install.sh FHS layout,
// and deb/rpm nfpm mapping. The hand-cp InstallRootForProfileDir(profileDir)
// path is handled by the resolver's managed-download step, not this probe.
//
//  1. <dir(exe)>/../chromium                    — goreleaser archive layout
//  2. <dir(exe)>/../share/omnipus/chromium      — install.sh FHS layout
//  3. <dir(exe)>/../libexec/omnipus/chromium    — deb/rpm nfpm mapping
func packageChromeRootCandidates() []string {
	exe, err := osExecutable()
	if err != nil {
		return nil
	}
	exeDir := filepath.Dir(exe)
	if runtime.GOOS == "darwin" {
		// ADR-052 Phase 3 / C3 option (ii): on macOS the Google-signed full
		// Chrome ships as a sibling helper OUTSIDE the signed gateway .app
		// (re-signing Chrome would break Google's signature and Apple
		// notarization — see ADR-052-C3). The gateway binary lives at
		// <Gateway.app>/Contents/MacOS/<bin>, so the sibling chromium/ dir
		// beside the .app is three levels up from exeDir. The first
		// candidate (<exeDir>/../chromium) keeps parity with the linux
		// first candidate for non-.app darwin installs (homebrew, dev
		// builds) and an in-bundle Contents/chromium layout. install.sh's
		// macOS slice places the sibling Chrome at one of these so the
		// runtime multi-root probe finds it exactly as on linux.
		return []string{
			filepath.Join(exeDir, "..", "chromium"),
			filepath.Join(exeDir, "..", "..", "..", "chromium"),
		}
	}
	return []string{
		filepath.Join(exeDir, "..", "chromium"),
		filepath.Join(exeDir, "..", "share", "omnipus", "chromium"),
		filepath.Join(exeDir, "..", "libexec", "omnipus", "chromium"),
	}
}

// ProbeStatus classifies why a package-Chrome candidate was rejected (or
// accepted). It is intentionally separate from the returned path so doctor
// can distinguish a suspicious path from a missing payload.
type ProbeStatus int

const (
	ProbeUsable ProbeStatus = iota
	ProbeNotFound
	ProbeSymlinkedRoot
	ProbeWorldWritable
	ProbeEmptyRoot
	ProbeUnsupportedOS
)

// packageChromeRootProbe returns the first candidate containing a usable,
// integrity-manifested Chrome payload and its classification. Invalid
// candidates are skipped so an empty or suspicious earlier slot cannot hide a
// valid later package layout. When no candidate is usable, the path and status
// identify the first existing problem for diagnostics.
func packageChromeRootProbe() (string, ProbeStatus) {
	if runtime.GOOS == "windows" {
		windowsPackageChromeDeferralLogged.Do(func() {
			logger.WarnCF(
				"browser",
				"Windows package-chrome path is deferred to Phase 4 (ADR-052); runtime falls through to managed download",
				nil,
			)
		})
		return "", ProbeUnsupportedOS
	}
	if packageChromeRootForTest != "" {
		return packageChromeRootForTest, packageChromeRootCandidateStatus(packageChromeRootForTest)
	}
	var firstIssuePath string
	firstIssueStatus := ProbeNotFound
	for _, candidate := range packageChromeRootCandidates() {
		status := packageChromeRootCandidateStatus(candidate)
		if status == ProbeUsable {
			return candidate, status
		}
		if status != ProbeNotFound && firstIssuePath == "" {
			firstIssuePath = candidate
			firstIssueStatus = status
		}
	}
	return firstIssuePath, firstIssueStatus
}

// packageChromeRoot returns the usable package-Chrome root path, or "" when
// no candidate has a valid integrity-manifested payload. Runtime callers use
// this single-value form when they do not need the diagnostic classification.
func packageChromeRoot() string {
	root, status := packageChromeRootProbe()
	if status != ProbeUsable {
		return ""
	}
	return root
}

// PackageChromeRoot returns the runtime-computed package-Chrome root for the
// running binary, or "" when no candidate layout contains a usable payload.
func PackageChromeRoot() string {
	return packageChromeRoot()
}

// PackageChromeRootProbe returns a candidate path and its classification.
// Unlike PackageChromeRoot, this preserves the first existing invalid
// candidate so doctor can emit WARN-BROWSER-008 for a symlinked or
// world-writable root and WARN-BROWSER-009 for an empty payload root.
func PackageChromeRootProbe() (string, ProbeStatus) {
	if runtime.GOOS == "windows" {
		windowsPackageChromeDeferralLogged.Do(func() {
			logger.WarnCF(
				"browser",
				"Windows package-chrome path is deferred to Phase 4 (ADR-052); runtime falls through to managed download",
				nil,
			)
		})
		return "", ProbeUnsupportedOS
	}
	if packageChromeRootForTest != "" {
		return packageChromeRootForTest, packageChromeRootCandidateStatus(packageChromeRootForTest)
	}
	for _, candidate := range packageChromeRootCandidates() {
		status := packageChromeRootCandidateStatus(candidate)
		if status != ProbeNotFound {
			return candidate, status
		}
	}
	return "", ProbeNotFound
}

var windowsPackageChromeDeferralLogged sync.Once

func packageChromeRootCandidateStatus(candidate string) ProbeStatus {
	if candidate == "" {
		return ProbeNotFound
	}
	info, err := os.Lstat(candidate)
	if err != nil {
		return ProbeNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ProbeSymlinkedRoot
	}
	if !info.IsDir() {
		return ProbeNotFound
	}
	if info.Mode()&0o002 != 0 {
		return ProbeWorldWritable
	}
	if bin, _ := findPackageChrome(candidate); bin != "" {
		return ProbeUsable
	}
	return ProbeEmptyRoot
}

// isUsablePackageRoot reports whether candidate is an existing,
// non-world-writable, non-symlinked directory. Payload validation is separate
// and is performed by packageChromeRootCandidateStatus.
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
	layouts := []string{
		filepath.Join(root, "chrome-linux64", "chrome"),
		filepath.Join(root, "chrome-headless-shell-linux64", "chrome-headless-shell"),
		filepath.Join(root, "chrome"),
		filepath.Join(root, "chrome-headless-shell"),
		filepath.Join(root, "chrome.exe"),                // Windows
		filepath.Join(root, "chrome-headless-shell.exe"), // Windows
	}
	if runtime.GOOS == "darwin" {
		// macOS .app bundle layout (C3 option ii): the Google-signed full
		// Chrome sits at <root>/Google Chrome for Testing.app/Contents/
		// MacOS/Google Chrome for Testing — fullChromeBinaryRelPath()
		// encodes that darwin-specific relpath. Appended on darwin only:
		// on linux/windows fullChromeBinaryRelPath returns the flat binary
		// name already covered above, so no duplicate is appended there.
		layouts = append(layouts, filepath.Join(root, fullChromeBinaryRelPath()))
	}
	return layouts
}

// findPackageChrome inspects root for a pinned full Chrome-for-Testing
// binary and its companion chrome.sha256 integrity manifest. Returns
// the binary's absolute path and the manifest's absolute path; either
// is the empty string when absent.
//
// The probe checks every layout in binaryLayoutsForRoot in priority
// order — chrome-linux64/chrome (goreleaser extraction subdir) wins
// over chrome (flat install.sh) wins over chrome.exe (Windows). The
// matching manifest paths try the binary's directory first and then its
// parent. Thus chrome-linux64/chrome accepts both
// <root>/chrome-linux64/chrome.sha256 (canonical cft-bundle layout) and
// <root>/chrome.sha256 (older root-level packages); flat chrome similarly
// resolves its root-level companion.
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
		// Manifest candidates per binary: try alongside the binary first
		// (the canonical goreleaser/cft-bundle layout), then one level up
		// (the older root-level layout retained for hand-cp and flat
		// installs), then the package root. cft-bundle writes chrome.sha256
		// next to the binary; accepting the parent location preserves
		// compatibility with older packages; the package-root location is
		// required on macOS, where the .app bundle nests the binary three
		// levels deep (.../Google Chrome for Testing.app/Contents/MacOS/
		// <bin>) so neither Dir(bin) nor Dir(Dir(bin)) reaches the root
		// where chrome.sha256 lives (beside the .app, per C3 option ii).
		// The root candidate collapses to a harmless duplicate of an
		// earlier candidate on the flat linux/windows layouts. Integrity
		// verification stays fail-closed regardless of which candidate
		// supplies the manifest.
		manifestCandidates := []string{
			filepath.Join(filepath.Dir(bin), "chrome.sha256"),
			filepath.Join(filepath.Dir(filepath.Dir(bin)), "chrome.sha256"),
			filepath.Join(root, "chrome.sha256"),
		}
		for _, sha := range manifestCandidates {
			shaInfo, shaErr := os.Lstat(sha)
			if shaErr != nil {
				continue
			}
			if shaInfo.Mode()&os.ModeSymlink != 0 || shaInfo.IsDir() {
				return "", ""
			}
			return bin, sha
		}
		// A binary with no manifest in either supported location is
		// refused; package Chrome is a Phase-1 integrity-verified floor.
		return "", ""
	}
	return "", ""
}

// --- SHA-256 verify cache (PERF-001 / PERF-004) ---
//
// ClassifyVideoCapability and findInstalledBuild can re-hash a roughly 400MB
// Chrome binary on every call. The cache is keyed by binary and manifest
// metadata so a replacement cannot reuse a positive result. Negative results
// cache for 60s to bound per-call cost on permanent mismatches; after the TTL,
// allow re-verification to recover from transient I/O errors.

const shaVerifyNegativeTTL = 60 * time.Second

// shaVerifyCacheEntry is one row of the process-wide SHA-verify cache.
type shaVerifyCacheEntry struct {
	binaryMtime   int64
	manifestMtime int64
	manifestInode uint64
	manifestSize  int64
	err           error
	cachedAt      time.Time
}

// shaVerifyCache is the process-wide memoization of integrity outcomes.
type shaVerifyCache struct {
	mu      sync.RWMutex
	entries map[shaVerifyCacheKey]shaVerifyCacheEntry
}

// shaVerifyCacheKey identifies a verification result by both paths and the
// observed metadata. Manifest inode and size detect overlay/bind-mount swaps;
// binary mtime detects replacing the payload while leaving the manifest alone.
type shaVerifyCacheKey struct {
	binary        string
	manifest      string
	binaryMtime   int64
	manifestMtime int64
	manifestInode uint64
	manifestSize  int64
}

// shaVerifyFlight coordinates concurrent verification misses for one key.
type shaVerifyFlight struct {
	done chan struct{}
}

var globalSHAVerifyCache = &shaVerifyCache{
	entries: make(map[shaVerifyCacheKey]shaVerifyCacheEntry),
}

var globalSHAVerifyFlights sync.Map // map[shaVerifyCacheKey]*shaVerifyFlight

// verifyChromeSHA256Fn is a narrow test seam around the shared verifier. The
// production value is chromeintegrity.VerifyChromeSHA256; tests use it to
// count calls while retaining the real parser and hashing behavior.
var verifyChromeSHA256Fn = chromeintegrity.VerifyChromeSHA256

func fileInfoInode(info os.FileInfo) uint64 {
	if info == nil || info.Sys() == nil {
		return 0
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Ptr {
		if value.IsNil() {
			return 0
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return 0
	}
	inode := value.FieldByName("Ino")
	if inode.IsValid() && inode.CanUint() {
		return inode.Uint()
	}
	return 0
}

func shaVerifyCacheKeyFor(binaryPath, shaPath string) (shaVerifyCacheKey, bool) {
	if binaryPath == "" || shaPath == "" {
		return shaVerifyCacheKey{}, false
	}
	binaryInfo, binaryErr := os.Lstat(binaryPath)
	if binaryErr != nil || binaryInfo.IsDir() || binaryInfo.Mode()&os.ModeSymlink != 0 {
		return shaVerifyCacheKey{}, false
	}
	manifestInfo, manifestErr := os.Lstat(shaPath)
	if manifestErr != nil || manifestInfo.IsDir() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return shaVerifyCacheKey{}, false
	}
	return shaVerifyCacheKey{
		binary:        binaryPath,
		manifest:      shaPath,
		binaryMtime:   binaryInfo.ModTime().UnixNano(),
		manifestMtime: manifestInfo.ModTime().UnixNano(),
		manifestInode: fileInfoInode(manifestInfo),
		manifestSize:  manifestInfo.Size(),
	}, true
}

func shaVerifyCacheLookup(binaryPath, shaPath string) (shaVerifyCacheEntry, bool, bool) {
	key, ok := shaVerifyCacheKeyFor(binaryPath, shaPath)
	if !ok {
		return shaVerifyCacheEntry{}, false, false
	}
	globalSHAVerifyCache.mu.RLock()
	entry, hit := globalSHAVerifyCache.entries[key]
	globalSHAVerifyCache.mu.RUnlock()
	if !hit {
		return shaVerifyCacheEntry{}, false, false
	}
	if entry.err == nil {
		return entry, true, true
	}
	return entry, true, time.Since(entry.cachedAt) < shaVerifyNegativeTTL
}

// shaVerifyCacheHit reports the cached verification result, whether its
// metadata key matched, and whether the result is fresh enough to use. Positive
// entries remain fresh while the metadata key matches; negative entries become
// retryable after the bounded TTL.
func shaVerifyCacheHit(binaryPath, shaPath string) (ok bool, hit bool, fresh bool) {
	entry, hit, fresh := shaVerifyCacheLookup(binaryPath, shaPath)
	return entry.err == nil, hit, fresh
}

// shaVerifyCacheStore records the verification result using current metadata.
// A negative result retains its error so a fresh negative hit returns the same
// verifier semantics without re-reading or re-hashing the binary.
func shaVerifyCacheStore(binaryPath, shaPath string, verifyErr error) {
	key, ok := shaVerifyCacheKeyFor(binaryPath, shaPath)
	if !ok {
		return
	}
	entry := shaVerifyCacheEntry{
		binaryMtime:   key.binaryMtime,
		manifestMtime: key.manifestMtime,
		manifestInode: key.manifestInode,
		manifestSize:  key.manifestSize,
		err:           verifyErr,
		cachedAt:      time.Now(),
	}
	globalSHAVerifyCache.mu.Lock()
	globalSHAVerifyCache.entries[key] = entry
	globalSHAVerifyCache.mu.Unlock()
}

// cachedVerifyChromeSHA256 is chromeintegrity.VerifyChromeSHA256 plus the
// process-wide cache and per-key single-flight coordination.
func cachedVerifyChromeSHA256(binaryPath, shaPath string) error {
	if _, hit, fresh := shaVerifyCacheHit(binaryPath, shaPath); hit && fresh {
		entry, _, _ := shaVerifyCacheLookup(binaryPath, shaPath)
		return entry.err
	}
	key, keyOK := shaVerifyCacheKeyFor(binaryPath, shaPath)
	if !keyOK {
		return verifyChromeSHA256Fn(binaryPath, shaPath)
	}
	flight := &shaVerifyFlight{done: make(chan struct{})}
	actual, loaded := globalSHAVerifyFlights.LoadOrStore(key, flight)
	if loaded {
		<-actual.(*shaVerifyFlight).done
		if _, hit, fresh := shaVerifyCacheHit(binaryPath, shaPath); hit && fresh {
			entry, _, _ := shaVerifyCacheLookup(binaryPath, shaPath)
			return entry.err
		}
		// Metadata changed during the first flight. Re-evaluate under the
		// new key rather than returning a result for a different payload.
		return cachedVerifyChromeSHA256(binaryPath, shaPath)
	}
	defer func() {
		close(flight.done)
		globalSHAVerifyFlights.Delete(key)
	}()
	// A concurrent caller may have filled the cache between the initial
	// lookup and LoadOrStore; use that result instead of hashing twice.
	if _, hit, fresh := shaVerifyCacheHit(binaryPath, shaPath); hit && fresh {
		entry, _, _ := shaVerifyCacheLookup(binaryPath, shaPath)
		return entry.err
	}
	verifyErr := verifyChromeSHA256Fn(binaryPath, shaPath)
	shaVerifyCacheStore(binaryPath, shaPath, verifyErr)
	return verifyErr
}
