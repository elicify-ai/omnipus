package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // integrity check only (matches the GCS-published Content-MD5), not a security signature — see verifyGoogHashMD5.
	"encoding/base64"
	"encoding/json"
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
func findInstalledBuild(installRoot, platform string, build chromiumBuild) string {
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		return ""
	}
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
		info, statErr := os.Stat(bin)
		if statErr != nil || info.Mode()&0o111 == 0 {
			continue
		}
		cands = append(cands, cand{path: bin, mod: info.ModTime()})
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
	return best.path
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
