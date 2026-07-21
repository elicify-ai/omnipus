package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // integrity check only (matches the GCS-published Content-MD5), not a security signature — see verifyGoogHashMD5.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

const (
	cftManifestURL = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions-with-downloads.json"
	cftChannel     = "Stable"

	// cftDownloadID is the CfT manifest key + zip/binary basename for the
	// chrome-headless-shell build (the graceful-degradation fallback build,
	// and the default on non-video-capable platforms). Kept as the original
	// single constant — this package's own tests (execpath_test.go, same
	// `package browser`, not a different package) reference it directly to
	// seed on-disk fixtures at the pre-dual-download layout, so its
	// name/value must not change even though EnsureChromium is now
	// build-aware.
	cftDownloadID = "chrome-headless-shell"

	// cftFullChromeDownloadID is the CfT manifest key for the full "chrome"
	// build — the video-capable (WebRTC tabCapture) default on linux.
	cftFullChromeDownloadID = "chrome"
)

// globalManifestURLForTesting overrides cftManifestURL when set. Tests use
// this to point the installer at a local httptest server.
var globalManifestURLForTesting string

type cftManifest struct {
	Channels map[string]struct {
		Version   string                              `json:"version"`
		Downloads map[string][]cftManifestDownloadRef `json:"downloads"`
	} `json:"channels"`
}

type cftManifestDownloadRef struct {
	Platform string `json:"platform"`
	URL      string `json:"url"`
}

var installMu sync.Mutex

// chromiumBuild is one of the two Chrome-for-Testing build flavors this
// installer manages (dual-download): the full "chrome" build (the
// WebRTC-tabCapture-capable default on linux — see selectDownloadBuild) and
// the "chrome-headless-shell" fallback used everywhere else and whenever the
// full build can't be resolved. Each carries its own manifest download key
// and its own on-disk layout via binaryPath (the executable's location
// relative to the build's extraction subdir).
type chromiumBuild struct {
	downloadID string
	binaryPath func() string
}

// headlessShellBuild describes the chrome-headless-shell CfT build.
func headlessShellBuild() chromiumBuild {
	return chromiumBuild{downloadID: cftDownloadID, binaryPath: headlessShellBinaryName}
}

// fullChromeBuild describes the full "chrome" CfT build — required for
// WebRTC tabCapture (video+audio) on linux; see selectDownloadBuild and
// ClassifyVideoCapability.
func fullChromeBuild() chromiumBuild {
	return chromiumBuild{downloadID: cftFullChromeDownloadID, binaryPath: fullChromeBinaryRelPath}
}

// subdir returns the extraction subdirectory name for b on platform — mirrors
// the CfT zip's own top-level folder naming (<downloadID>-<platform>), which
// holds uniformly for both builds (e.g. "chrome-linux64", "chrome-headless-
// shell-linux64").
func (b chromiumBuild) subdir(platform string) string {
	return b.downloadID + "-" + platform
}

// binaryFullPath resolves b's executable absolute path under versionDir for
// platform, honoring b's own on-disk layout.
func (b chromiumBuild) binaryFullPath(versionDir, platform string) string {
	return filepath.Join(versionDir, b.subdir(platform), b.binaryPath())
}

// sha256Path returns the path of b's companion integrity manifest file
// (chrome.sha256) under installRoot — the manifest the package-build
// pipeline writes alongside the package Chrome per ADR-052 M2, mirroring the
// SHA-pinned install contract the runtime-download path already verifies via
// verifyGoogHashMD5. The companion file is named chrome.sha256 (NOT
// per-build) on purpose — it is a per-binary integrity attestation written by
// the goreleaser post-step, not by the runtime, and one binary per
// installRoot is the supported layout. Returns the empty string if installRoot
// is empty (defensive — the path is just a Join and would land at "<root>/
// chrome.sha256" otherwise).
func (b chromiumBuild) sha256Path(installRoot string) string {
	if installRoot == "" {
		return ""
	}
	return filepath.Join(installRoot, "chrome.sha256")
}

// EnsureChromiumBuild ensures a managed Chromium binary is present under
// installRoot, downloading build when neither flavor is already installed.
// Returns the absolute path to the executable.
//
// Dual-download: two CfT builds are supported — the full "chrome" build
// (the WebRTC tabCapture video+audio capable build selectDownloadBuild
// defaults to on linux) and the "chrome-headless-shell" build (the
// graceful-degradation fallback, and the default everywhere else) — each
// with its own manifest download key, on-disk extraction layout, binary-name
// resolver, and integrity verification (verifyGoogHashMD5).
// EnsureChromiumBuild resolves the SPECIFICALLY requested build: if that build
// is already installed it returns it without hitting the network, otherwise it
// downloads that build. The two builds are NOT interchangeable — WebRTC
// tabCapture needs full Chrome, which chrome-headless-shell lacks — so
// resolution is deliberately per-build, not a "detect either" short-circuit
// that could hand a fresh install a cached shell binary instead of the
// video-capable full build.
//
// Layout: <installRoot>/<version>/<downloadID>-<platform>/<binary-per-build-layout>
//
// Concurrent calls are serialized; the second caller observes the freshly
// extracted binary and returns immediately.
func EnsureChromiumBuild(ctx context.Context, installRoot string, build chromiumBuild) (string, error) {
	installMu.Lock()
	defer installMu.Unlock()

	platform, err := cftPlatform()
	if err != nil {
		return "", err
	}

	// If the SPECIFICALLY requested build is already installed, return it without
	// hitting the network. The full "chrome" build and chrome-headless-shell are
	// DIFFERENT, non-interchangeable builds — WebRTC tabCapture needs full
	// Chrome, which chrome-headless-shell lacks entirely. A "detect-either"
	// short-circuit (findInstalledBinary) would hand a caller that explicitly
	// asked for the full build a cached headless-shell instead and silently
	// break video, so resolution MUST be per-build here.
	if path := findInstalledBuild(installRoot, platform, build); path != "" {
		return path, nil
	}

	logger.InfoCF("browser", "Chromium not installed — downloading from chrome-for-testing",
		map[string]any{
			"install_root": installRoot,
			"platform":     platform,
			"channel":      cftChannel,
			"build":        build.downloadID,
		})

	manifest, err := fetchCFTManifest(ctx)
	if err != nil {
		return "", fmt.Errorf("browser: fetch chrome-for-testing manifest: %w", err)
	}

	channel, ok := manifest.Channels[cftChannel]
	if !ok {
		return "", fmt.Errorf("browser: chrome-for-testing manifest missing %q channel", cftChannel)
	}

	downloads, ok := channel.Downloads[build.downloadID]
	if !ok && build.downloadID != cftDownloadID {
		// The preferred build has no manifest entry at all (unexpected on a
		// standard CfT feed) — fall back to chrome-headless-shell rather than
		// failing a fresh install outright (graceful-degradation fallback
		// semantics).
		logger.WarnCF(
			"browser",
			"preferred chromium build missing from manifest — falling back to chrome-headless-shell",
			map[string]any{"preferred_build": build.downloadID},
		)
		build = headlessShellBuild()
		downloads, ok = channel.Downloads[build.downloadID]
	}
	if !ok {
		return "", fmt.Errorf("browser: chrome-for-testing manifest missing %q downloads", build.downloadID)
	}

	zipURL := zipURLForPlatform(downloads, platform)
	if zipURL == "" && build.downloadID != cftDownloadID {
		// Same fallback rationale as above, for a platform-shaped miss (e.g. a
		// feed that only ships "chrome" for a subset of platforms).
		logger.WarnCF(
			"browser",
			"preferred chromium build has no build for this platform — falling back to chrome-headless-shell",
			map[string]any{"preferred_build": build.downloadID, "platform": platform},
		)
		build = headlessShellBuild()
		if hsDownloads, hsOK := channel.Downloads[build.downloadID]; hsOK {
			zipURL = zipURLForPlatform(hsDownloads, platform)
		}
	}
	if zipURL == "" {
		return "", fmt.Errorf("browser: chrome-for-testing has no %s build for platform %s", build.downloadID, platform)
	}

	versionDir := filepath.Join(installRoot, channel.Version)
	if mkdirErr := os.MkdirAll(versionDir, 0o700); mkdirErr != nil {
		return "", fmt.Errorf("browser: create install dir: %w", mkdirErr)
	}

	zipPath := filepath.Join(versionDir, build.downloadID+"-"+platform+".zip")
	if dlErr := downloadFile(ctx, zipURL, zipPath); dlErr != nil {
		_ = os.Remove(zipPath)
		return "", fmt.Errorf("browser: download %s: %w", zipURL, dlErr)
	}

	if extractErr := extractZip(zipPath, versionDir); extractErr != nil {
		return "", fmt.Errorf("browser: extract %s: %w", zipPath, extractErr)
	}
	_ = os.Remove(zipPath)

	binaryPath := build.binaryFullPath(versionDir, platform)
	info, err := os.Stat(binaryPath)
	if err != nil {
		return "", fmt.Errorf("browser: extracted archive missing %s: %w", binaryPath, err)
	}
	if info.Mode()&0o111 == 0 {
		if err := os.Chmod(binaryPath, info.Mode()|0o755); err != nil {
			return "", fmt.Errorf("browser: chmod +x %s: %w", binaryPath, err)
		}
	}

	logger.InfoCF("browser", "Chromium install complete",
		map[string]any{
			"version": channel.Version,
			"build":   build.downloadID,
			"binary":  binaryPath,
		})

	return binaryPath, nil
}

// EnsureChromium ensures the agent's default managed Chromium binary is
// present under installRoot, per selectDownloadBuild: the full "chrome"
// build on linux (WebRTC tabCapture needs full Chrome's capture stack — see
// ClassifyVideoCapability), chrome-headless-shell everywhere else. Falls
// back to chrome-headless-shell (via EnsureChromiumBuild's own fallback)
// when the full build is unavailable on linux — graceful degradation: the
// host still browses, and ClassifyVideoCapability reports not-capable. See
// EnsureChromiumBuild.
func EnsureChromium(ctx context.Context, installRoot string) (string, error) {
	return EnsureChromiumBuild(ctx, installRoot, selectDownloadBuild())
}

// EnsureChromiumFullBuild ensures the full "chrome" build (required for
// WebRTC tabCapture video+audio) is present under installRoot regardless of
// platform. This is the same binary EnsureChromium/selectDownloadBuild
// resolves for the agent on linux. It is test-only today (execpath_test.go
// and installer_test.go call it directly to exercise the dual-download
// paths) — nothing in the boot/gateway path or ClassifyVideoCapability
// calls it: classification (capability.go's ClassifyVideoCapability) only
// INSPECTS what's already on disk via findInstalledBuild, it never
// downloads. Kept as a distinct, explicit entry point (rather than folding
// into EnsureChromium) because a caller that specifically wants the full
// build regardless of platform-based selectDownloadBuild fallback logic
// needs a way to ask for it directly.
func EnsureChromiumFullBuild(ctx context.Context, installRoot string) (string, error) {
	return EnsureChromiumBuild(ctx, installRoot, fullChromeBuild())
}

// zipURLForPlatform returns the download URL among downloads matching
// platform, or "" if none matches.
func zipURLForPlatform(downloads []cftManifestDownloadRef, platform string) string {
	for _, d := range downloads {
		if d.Platform == platform {
			return d.URL
		}
	}
	return ""
}

// selectDownloadBuild decides which CfT build the agent's default
// EnsureChromium download (nothing of either flavor cached yet) should
// fetch. WebRTC tabCapture (video+audio) requires the full "chrome" build
// and is only ever video-capable on linux (ClassifyVideoCapability) — so
// linux defaults to fullChromeBuild(), and every other platform (which will
// never be video-capable) defaults to the lighter headlessShellBuild()
// rather than paying for a full-Chrome download it can't use for capture.
// EnsureChromiumBuild's own fallback separately drops linux to
// headlessShellBuild() gracefully when the full build is missing from the
// manifest or unavailable for the current platform — that's a distinct,
// later fallback from this initial platform-based selection.
func selectDownloadBuild() chromiumBuild {
	if runtime.GOOS == "linux" {
		return fullChromeBuild()
	}
	return headlessShellBuild()
}

// findInstalledBinary scans installRoot's version directories for an
// already-extracted, executable Chromium binary and returns its path, or ""
// if none exists. Detect-either: checks BOTH the full "chrome" build and the
// chrome-headless-shell fallback at each version dir, preferring the full
// build when both are present since it is a strict superset of
// headless-shell's browsing capability.
func findInstalledBinary(installRoot, platform string) string {
	if path := findInstalledBuild(installRoot, platform, fullChromeBuild()); path != "" {
		return path
	}
	return findInstalledBuild(installRoot, platform, headlessShellBuild())
}

// findInstalledBuild scans installRoot's version directories for an
// already-extracted, executable binary of the given build and returns the
// most-recently-modified match, or "" if none exists.
//
// ADR-052 M2 (integrity): when a sibling chrome.sha256 integrity manifest is
// present at installRoot (the package-build pipeline writes one alongside the
// package Chrome per ADR-052 D2), the candidate binary's actual SHA-256 is
// verified against the manifest before being returned. A present-and-
// mismatching manifest hard-fails (treated as "not installed") so the
// managed download path kicks in, exactly mirroring the runtime-download
// path's verifyGoogHashMD5 hard-fail behavior (SEC-ADR052-001 — no silent
// default fallback). A MISSING manifest stays permissive on this path —
// the runtime-download path doesn't ship one by default and older managed
// installs predate ADR-052 entirely; refusing them would be a back-compat
// regression. (The package Chrome path is stricter — see findPackageChrome
// in exec_resolver.go, which requires chrome.sha256.)
//
// SEC-ADR052-005 symlink defense: each candidate binary is checked with
// os.Lstat before being added to the candidate set, and the winning
// candidate's manifest verification re-lstats both the binary and the
// manifest (verifyChromeSHA256's own guards). A symlinked binary or a
// symlinked manifest is rejected.
func findInstalledBuild(installRoot, platform string, build chromiumBuild) string {
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return ""
	}
	shaPath := build.sha256Path(installRoot)
	// Walk version directories newest-first by ModTime so we pick the most
	// recently installed build when multiple coexist.
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bin := build.binaryFullPath(filepath.Join(installRoot, e.Name()), platform)
		// SEC-ADR052-005: refuse a symlinked binary at the leaf. os.Stat
		// follows symlinks (so info.Mode()&0o111 would reflect the
		// target's mode, not the leaf's), and an attacker who can write
		// the install root can substitute a symlink to an arbitrary
		// binary. Lstat gives us the leaf's mode directly.
		linfo, lerr := os.Lstat(bin)
		if lerr != nil {
			continue
		}
		if linfo.Mode()&os.ModeSymlink != 0 {
			logger.WarnCF("browser", "installed Chrome binary is a symlink — refusing to use it",
				map[string]any{"binary": bin})
			continue
		}
		if linfo.Mode()&0o111 == 0 {
			continue
		}
		cands = append(cands, cand{path: bin, mod: linfo.ModTime()})
	}
	if len(cands) == 0 {
		return ""
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if c.mod.After(best.mod) {
			best = c
		}
	}
	// ADR-052 M2 integrity verification (only when a manifest is present —
	// see this function's doc comment for why missing is permissive here).
	if shaPath != "" {
		if _, statErr := os.Lstat(shaPath); statErr == nil {
			if verr := verifyChromeSHA256(best.path, shaPath); verr != nil {
				// A present-but-malformed/mismatching manifest hard-fails
				// (SEC-ADR052-001 — no silent default). Missing manifests
				// are NOT a failure on this path (the runtime-download
				// path doesn't ship one); the sentinel
				// errSHA256ManifestMissing is what verifyChromeSHA256
				// returns, and we specifically accept it here so the
				// back-compat case stays permissive. Any other error is a
				// hard fail (mismatch, malformed, symlink).
				if !errors.Is(verr, errSHA256ManifestMissing) {
					logger.WarnCF("browser",
						"installed Chrome failed integrity verification — treating as not installed so managed download will retry",
						map[string]any{
							"binary":     best.path,
							"sha256_man": shaPath,
							"error":      verr.Error(),
						})
					return ""
				}
			}
		}
	}
	return best.path
}

// errSHA256ManifestMissing is the sentinel returned by verifyChromeSHA256
// when shaPath is empty OR the manifest cannot be read. The package Chrome
// resolution path (exec_resolver.go's resolve step 3) uses this sentinel to
// fail closed on a missing manifest (SEC-ADR052-001 — no silent default
// fallback when integrity metadata is absent; per ADR-052 M2, "an attacker
// who can write <installRoot>/chrome can also write <installRoot>/chrome.sha256,
// so accepting the binary when the manifest is missing is identical to
// accepting an unverifiable binary"). The managed install path
// (findInstalledBuild) handles this sentinel differently — see its doc —
// because the runtime-download path doesn't ship a manifest by default and
// refusing those would be a back-compat regression on pre-Phase-1 installs.
var errSHA256ManifestMissing = fmt.Errorf("chrome.sha256 integrity manifest missing or unreadable")

// verifyChromeSHA256 hashes binaryPath with crypto/sha256 and compares the
// digest against the value in shaPath using crypto/subtle.ConstantTimeCompare
// (constant-time to defeat timing-side-channel observation of the digest).
//
// SEC-ADR052-001 fail-closed contract: returns errSHA256ManifestMissing when
// shaPath is empty OR the manifest cannot be read for any reason
// (os.IsNotExist, permission denied, EIO, ...). The caller decides what to
// do with that sentinel — for the package Chrome path (Phase 1 floor), the
// resolver falls through to the managed download path; for the managed
// install path, the verifier is only invoked when chrome.sha256 is present.
//
// SEC-ADR052-004 parser hardening: the manifest parser tolerates the
// well-formed-but-noisy shapes real CfT / goreleaser / sha256sum pipelines
// emit — leading UTF-8 BOM, CRLF line endings, leading "sha256:" / "SHA-256:"
// prefix, leading "#"-prefixed comment lines, a single trailing whitespace
// run, and the sha256sum(1) two-field "<digest>  <filename>" format.
// Adversarial shapes that DO NOT survive the parser and produce explicit
// errors: uppercase hex (toolchain mismatch — surfaced rather than
// silently lowercased), digest length != 64 chars, NUL bytes / CR-only
// separators INSIDE the digest, embedded binary garbage after the digest,
// empty manifest, and any non-hex character outside a comment line.
//
// SEC-ADR052-005 TOCTOU + symlink defense: the binary is opened with leaf
// symlink rejection via os.Lstat (refuses a symlink at binaryPath's leaf).
// The manifest is read with the same guard. A TOCTOU swap between Lstat
// and Open is bounded by the install-root-mode check at the call sites
// (findPackageChrome in exec_resolver.go, findInstalledBuild below) —
// those refuse to operate on a world-writable install root, so the
// attacker who could race the open needs write access to the install
// root's contents, which the mode check disallows.
func verifyChromeSHA256(binaryPath, shaPath string) error {
	if shaPath == "" {
		return errSHA256ManifestMissing
	}

	// SEC-ADR052-005: refuse a symlinked manifest at the leaf.
	if info, lerr := os.Lstat(shaPath); lerr != nil {
		// Any error here — IsNotExist, EACCES, EIO, ... — is a refusal.
		// Per SEC-ADR052-001, a manifest we can't read is the same as a
		// manifest we can't trust.
		return fmt.Errorf("%w: %s: %v", errSHA256ManifestMissing, shaPath, lerr)
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", errSHA256ManifestMissing, shaPath)
	} else if info.IsDir() {
		return fmt.Errorf("%w: %s is a directory, not a file", errSHA256ManifestMissing, shaPath)
	}

	raw, readErr := os.ReadFile(shaPath)
	if readErr != nil {
		return fmt.Errorf("%w: read %s: %v", errSHA256ManifestMissing, shaPath, readErr)
	}

	want, parseErr := parseSHA256Manifest(raw)
	if parseErr != nil {
		return fmt.Errorf("parse sha256 manifest %s: %w", shaPath, parseErr)
	}

	// SEC-ADR052-005: refuse a symlinked binary at the leaf.
	binInfo, lerr := os.Lstat(binaryPath)
	if lerr != nil {
		return fmt.Errorf("lstat binary %s: %w", binaryPath, lerr)
	}
	if binInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to verify a symlinked binary at %s", binaryPath)
	}
	if binInfo.IsDir() {
		return fmt.Errorf("binary path %s is a directory", binaryPath)
	}

	f, openErr := os.Open(binaryPath)
	if openErr != nil {
		return fmt.Errorf("open binary %s: %w", binaryPath, openErr)
	}
	defer f.Close()
	hasher := sha256.New()
	if _, copyErr := io.Copy(hasher, f); copyErr != nil {
		return fmt.Errorf("hash binary %s: %w", binaryPath, copyErr)
	}
	got := hex.EncodeToString(hasher.Sum(nil))

	// SEC-ADR052-004: constant-time comparison. Both sides are 64 hex chars
	// by construction (parseSHA256Manifest enforces length), so this is a
	// fixed-size compare — ConstantTimeCompare returns 1 iff equal and 0
	// otherwise, with no early-exit on first-mismatch byte.
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return fmt.Errorf(
			"sha256 mismatch: binary hashes to %s… but manifest %s declares %s…",
			got[:16],
			shaPath,
			want[:16],
		)
	}
	return nil
}

// parseSHA256Manifest extracts the canonical 64-char lowercase hex SHA-256
// digest from raw. Tolerant of the well-formed-but-noisy shapes CfT /
// goreleaser / sha256sum pipelines emit; refuses everything else as an
// explicit parse error. See SEC-ADR052-004 for the full grammar.
//
// Recognized shapes:
//
//   - "<64-hex>\n"
//   - "<64-hex>  chrome\n"                       — sha256sum(1) two-field form
//   - "  <64-hex>  chrome\n"                      — leading whitespace
//   - "# comment\n<64-hex>\n"                     — BusyBox-style comment prefix
//   - "sha256:<64-hex>\n" or "SHA-256:<64-hex>\n" — algo-prefixed
//   - "\xEF\xBB\xBF<64-hex>\n"                    — UTF-8 BOM at start of file
//   - "<64-hex>\r\n"                              — CRLF line endings
//
// Rejected shapes (return explicit errors, never silent-coerce):
//
//   - uppercase hex digits                         — toolchain mismatch
//   - digest length != 64 chars                    — corrupt / partial
//   - NUL bytes or CR-only separators INSIDE digest
//   - non-hex characters outside a comment line
//   - empty manifest                               — no digest
//   - embedded binary garbage after the digest line
func parseSHA256Manifest(raw []byte) (string, error) {
	// Strip a leading UTF-8 BOM (3 bytes) — present in some Windows-port
	// emitters and harmless once removed.
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		raw = raw[3:]
	}
	// Normalize CRLF and lone CR to LF before line-splitting.
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))

	var digest string
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == '#' {
			continue // comment line
		}
		// Tolerate a leading "sha256:" / "SHA-256:" prefix.
		trimmed := line
		if bytes.HasPrefix(trimmed, []byte("sha256:")) {
			trimmed = trimmed[len("sha256:"):]
		} else if bytes.HasPrefix(trimmed, []byte("SHA-256:")) {
			trimmed = trimmed[len("SHA-256:"):]
		}
		// Tolerate the sha256sum(1) two-field "<digest>  <filename>" format.
		// A single whitespace boundary between two whitespace-separated
		// fields is treated as the separator; if the right field is the
		// binary name and the left is a 64-hex digest, take the left.
		fields := bytes.Fields(trimmed)
		if len(fields) == 2 {
			if isLowerHex(fields[0]) && !isLowerHex(fields[1]) {
				trimmed = fields[0]
			} else {
				return "", fmt.Errorf("two-field line is not a <hex>  <name> pair: %q", line)
			}
		} else if len(fields) != 1 {
			return "", fmt.Errorf("expected a single digest field, got %d: %q", len(fields), line)
		} else {
			trimmed = fields[0]
		}
		candidate := string(trimmed)
		if len(candidate) != 64 {
			return "", fmt.Errorf("digest length %d != 64: %q", len(candidate), candidate)
		}
		// SEC-ADR052-004: refuse uppercase hex — toolchain mismatch.
		if !isLowerHex(trimmed) {
			return "", fmt.Errorf("digest is not lowercase hex (toolchain mismatch): %q", candidate)
		}
		if digest != "" {
			return "", fmt.Errorf("manifest declares multiple digests: %q and %q", digest, candidate)
		}
		digest = candidate
	}
	if digest == "" {
		return "", fmt.Errorf("manifest contains no SHA-256 digest line")
	}
	return digest, nil
}

// isLowerHex reports whether b is non-empty and contains only lowercase hex
// digits ([0-9a-f]). Used by parseSHA256Manifest — uppercase is an explicit
// error (SEC-ADR052-004 — toolchain mismatch, surfaced rather than silently
// lowercased).
func isLowerHex(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func cftPlatform() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if runtime.GOARCH != "amd64" {
			return "", fmt.Errorf("chrome-for-testing has no linux/%s build; install chromium manually", runtime.GOARCH)
		}
		return "linux64", nil
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return "mac-arm64", nil
		}
		return "mac-x64", nil
	case "windows":
		if runtime.GOARCH == "386" {
			return "win32", nil
		}
		return "win64", nil
	default:
		return "", fmt.Errorf("unsupported platform %s/%s for managed chromium install", runtime.GOOS, runtime.GOARCH)
	}
}

// headlessShellBinaryName returns the chrome-headless-shell executable's
// filename for the current GOOS. Referenced directly by other packages' test
// fixtures (execpath_test.go) — keep this exact name and signature.
func headlessShellBinaryName() string {
	if runtime.GOOS == "windows" {
		return "chrome-headless-shell.exe"
	}
	return "chrome-headless-shell"
}

// fullChromeBinaryRelPath returns the full "chrome" CfT build's executable
// path, relative to its extraction subdir (the build's own on-disk layout):
// a flat binary on linux/windows, but a macOS .app bundle on darwin
// (chrome-mac-{x64,arm64}/Google Chrome for Testing.app/Contents/MacOS/Google
// Chrome for Testing — the real CfT "chrome" macOS layout, distinct from
// chrome-headless-shell's flat binary).
func fullChromeBinaryRelPath() string {
	switch runtime.GOOS {
	case "windows":
		return "chrome.exe"
	case "darwin":
		return filepath.Join("Google Chrome for Testing.app", "Contents", "MacOS", "Google Chrome for Testing")
	default:
		return "chrome"
	}
}

func fetchCFTManifest(ctx context.Context) (*cftManifest, error) {
	url := cftManifestURL
	if globalManifestURLForTesting != "" {
		url = globalManifestURLForTesting
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}
	var m cftManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// downloadFile fetches url to dest atomically (temp file + rename), verifying
// the downloaded content's integrity (verifyGoogHashMD5) before the rename —
// i.e. before it becomes visible to extractZip / the runtime. A response with
// a mismatched hash OR with no X-Goog-Hash checksum header at all is rejected
// the same way — verify integrity before an unverified download ever becomes
// the agent's Chrome runtime, never fail open. On any failure, including a
// failed integrity check, the partial/rejected temp file is removed and dest
// is never created.
func downloadFile(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".part-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	hasher := md5.New() //nolint:gosec // see the import comment: integrity check, not a security signature.
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), resp.Body); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := verifyGoogHashMD5(resp.Header, hasher.Sum(nil)); err != nil {
		_ = os.Remove(tmpPath)
		logger.ErrorCF("browser", "chromium download failed integrity verification — refusing to install",
			map[string]any{"url": url, "error": err.Error()})
		return fmt.Errorf("integrity check failed: %w", err)
	}

	return os.Rename(tmpPath, dest)
}

// allowHeaderlessDownloadForTesting lets verifyGoogHashMD5 accept a download
// response that carries no X-Goog-Hash header at all, instead of the default
// hard rejection. Production code never sets this true: it exists only so a
// hermetic test can exercise a headerless response deliberately.
// Chrome-for-Testing's storage backend (storage.googleapis.com) always
// publishes X-Goog-Hash on every real response, so this stays a
// same-package-test-only opt-in, never a default or an exported switch.
var allowHeaderlessDownloadForTesting = false

// verifyGoogHashMD5 verifies a just-downloaded archive's MD5 digest (got)
// against the MD5 checksum the server published for that response via the
// GCS-standard X-Goog-Hash header. This is the only integrity signal
// Chrome-for-Testing's storage backend (storage.googleapis.com) publishes
// today — the CfT manifest itself carries no checksum or signature field at
// all (each download entry is only {platform, url}), so this is the
// strongest per-build integrity check available without inventing an
// out-of-band source. It guards against truncated, corrupted, or
// in-transit-tampered downloads reaching disk as a trusted runtime; it is
// not a substitute for a supply-chain-trusted, out-of-band signature, which
// upstream does not provide.
//
// A response that carries an X-Goog-Hash header whose declared and actual
// digests disagree (or whose md5 value is malformed) is always rejected. A
// response with NO X-Goog-Hash header at all is likewise a hard reject by
// default: verify integrity BEFORE a downloaded binary becomes the agent's
// Chrome runtime, and an absent header from the expected GCS host (a
// non-GCS mirror, a stripped proxy, or a tampered response) is exactly the
// case that check exists to catch — silently trusting it would fail open.
// If a headerless mirror must ever be tolerated, that requires an explicit
// opt-in (allowHeaderlessDownloadForTesting), never the default.
func verifyGoogHashMD5(header http.Header, got []byte) error {
	// GCS publishes the checksum(s) via X-Goog-Hash. A single object commonly
	// carries TWO SEPARATE header lines — "crc32c=..." AND "md5=..." — and for
	// real Chrome-for-Testing objects crc32c is listed FIRST. Go's Header.Get
	// returns only the first value, so reading it alone finds crc32c and misses
	// the md5, hard-rejecting a legitimate download. Read EVERY X-Goog-Hash
	// value via Header.Values (and split each on ',' in case a proxy folds them
	// into one comma-joined line) and search all of them for the md5 checksum.
	values := header.Values("X-Goog-Hash")
	if len(values) == 0 {
		if allowHeaderlessDownloadForTesting {
			return nil
		}
		return fmt.Errorf(
			"response carried no X-Goog-Hash checksum header — refusing to trust an unverified chromium download",
		)
	}
	var want []byte
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			v, ok := strings.CutPrefix(part, "md5=")
			if !ok {
				continue
			}
			decoded, err := base64.StdEncoding.DecodeString(v)
			if err != nil {
				return fmt.Errorf("malformed X-Goog-Hash md5 value %q: %w", v, err)
			}
			want = decoded
			break
		}
		if want != nil {
			break
		}
	}
	if want == nil {
		return fmt.Errorf("X-Goog-Hash header(s) present but carry no md5 checksum: %q", values)
	}
	if !bytes.Equal(want, got) {
		return fmt.Errorf("checksum mismatch: server declared md5 %x, downloaded content hashes to %x", want, got)
	}
	return nil
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Pre-resolve destDir once so the per-entry guard below is a cheap
	// prefix compare instead of a per-entry Abs call.
	absDest, absErr := filepath.Abs(destDir)
	if absErr != nil {
		return fmt.Errorf("resolve dest dir: %w", absErr)
	}
	absDestPrefix := absDest + string(os.PathSeparator)

	for _, f := range r.File {
		// First-pass: reject the obvious "../escape" patterns and absolute
		// paths in the entry name before we touch the filesystem at all.
		clean := filepath.Clean(f.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") ||
			strings.Contains(clean, string(os.PathSeparator)+".."+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes archive root: %q", f.Name)
		}
		outPath := filepath.Join(destDir, clean)
		// Second-pass: the Clean+Join above resolves intra-name traversal,
		// but a symlink already on disk (from an earlier entry in the same
		// archive) could still redirect a write outside destDir. Take the
		// absolute path of outPath and require it to live strictly under
		// absDest. This is the zip-slip canonical guard (CodeQL go/zipslip).
		absOut, absOutErr := filepath.Abs(outPath)
		if absOutErr != nil {
			return fmt.Errorf("resolve entry path: %w", absOutErr)
		}
		if absOut != absDest && !strings.HasPrefix(absOut, absDestPrefix) {
			return fmt.Errorf("zip entry escapes archive root: %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			return err
		}
		mode := f.Mode()
		if mode == 0 {
			mode = 0o644
		}
		out, err := os.OpenFile(outPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		if err := out.Close(); err != nil {
			return err
		}
	}
	return nil
}
