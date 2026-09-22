package browser

// execpath_test.go — coverage for resolveExecPath's PATH-candidate
// validation (probeChromiumBinary), its in-process cache (execPathCache/
// execPathMu), and Preprovision's boot-time resolution. Mirrors
// installer_test.go's harness style (globalManifestURLForTesting +
// httptest, findInstalledBinary's on-disk layout) so the "falls through to
// managed" and "downloads" cases never touch the real network.
//
// The motivating bug (the live repro on the devpod this fix shipped from):
// /usr/bin/chromium-browser is an Ubuntu snap redirector script — a valid,
// executable file that passes exec.LookPath — which exits with "... requires
// the chromium snap" the moment it actually runs, on any host with no snapd
// (e.g. a Fly.io machine). The pre-fix resolveExecPath trusted LookPath alone
// and committed to that broken candidate; every browser tool then failed at
// first use instead of falling through to the next PATH candidate or the
// managed chrome-for-testing install.

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeExecutable writes contents to path with the executable bit set —
// used to fabricate fake `google-chrome`/`chromium`/etc. shell-script test
// doubles on a temp $PATH directory.
func writeExecutable(t *testing.T, path, contents string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o755))
}

// newExecPathTestConfig returns a BrowserConfig with ProfileDir laid out
// exactly like browser.DefaultConfig()'s real "<home>/browser/profiles/
// default" shape (rooted under root instead of $OMNIPUS_HOME), so
// resolveExecPath's installRoot computation
// (filepath.Join(filepath.Dir(ProfileDir), "..", "chromium")) lands where
// these tests expect: <root>/browser/chromium.
func newExecPathTestConfig(t *testing.T, root string) BrowserConfig {
	t.Helper()
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.ProfileDir = filepath.Join(root, "browser", "profiles", "default")
	return cfg
}

// seedManagedBinary creates a fake managed-install binary at the on-disk
// layout EnsureChromium/findInstalledBuild expect for the current
// platform's default build (selectDownloadBuild — full "chrome" on linux,
// since EnsureChromium resolves a SPECIFIC requested build per-build, not a
// detect-either lookup across both flavors) so resolveExecPath's managed
// fallback finds it without ever touching the network. Returns the seeded
// binary's absolute path.
func seedManagedBinary(t *testing.T, installRoot, platform string) string {
	t.Helper()
	build := selectDownloadBuild()
	versionDir := filepath.Join(installRoot, "131.0.6778.108", build.subdir(platform))
	require.NoError(t, os.MkdirAll(versionDir, 0o755))
	binPath := filepath.Join(versionDir, build.binaryPath())
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0o755))
	writeExecutable(t, binPath, "#!/bin/sh\nexit 0\n")
	return binPath
}

func installRootFor(cfg BrowserConfig) string {
	return filepath.Clean(filepath.Join(filepath.Dir(filepath.Clean(cfg.ProfileDir)), "..", "chromium"))
}

// --- Preprovision ---

// TestPreprovision_RemoteCDP_NoOp verifies Preprovision is a true no-op
// (empty path, nil error) when the manager is configured for remote CDP —
// there is no local binary to provision.
func TestPreprovision_RemoteCDP_NoOp(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)
	cfg.CDPURL = "ws://127.0.0.1:1/unreachable-by-design"
	m := &BrowserManager{cfg: cfg}

	got, err := m.Preprovision(context.Background())
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestPreprovision_ValidPATHCandidate_NoManagedInstallDirCreated verifies
// that when a validated system Chromium is present AND the operator has
// opted in to trusting a non-package binary (SEC-ADR052-002 —
// cfg.TrustPathChrome=true), Preprovision resolves to it and never creates
// the managed install directory (no download attempted when nothing is
// needed). With the security-hardened default TrustPathChrome=false the
// $PATH result is recorded at WARN-BROWSER-007 and discarded, so this
// specific scenario requires the explicit opt-in.
func TestPreprovision_ValidPATHCandidate_NoManagedInstallDirCreated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	binDir := t.TempDir()
	chromiumPath := filepath.Join(binDir, "chromium")
	writeExecutable(t, chromiumPath, "#!/bin/sh\necho 'Chromium 131.0.6778.108'\nexit 0\n")

	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.TrustPathChrome = true // SEC-ADR052-002: opt in to trust $PATH
	m := &BrowserManager{cfg: cfg}

	got, err := m.Preprovision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, chromiumPath, got)

	_, statErr := os.Stat(installRootFor(cfg))
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "Preprovision must not create the managed install dir when a PATH candidate resolves")
}

// TestPreprovision_BrokenPATH_PreSeededManagedBinary_NoNetwork verifies
// that with every PATH candidate broken but a managed binary already
// installed, Preprovision finds and returns it without any network call
// (globalManifestURLForTesting is left at its zero value — the real
// internet manifest URL — proving findInstalledBinary short-circuits before
// any HTTP fetch is attempted).
func TestPreprovision_BrokenPATH_PreSeededManagedBinary_NoNetwork(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "google-chrome"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	binPath := seedManagedBinary(t, installRootFor(cfg), platform)

	m := &BrowserManager{cfg: cfg}
	got, err := m.Preprovision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, binPath, got)
}

// TestPreprovision_BrokenPATH_EmptyInstallRoot_Downloads verifies the full
// download path: every PATH candidate broken, nothing pre-installed —
// Preprovision must download and extract via the (test-server-backed)
// chrome-for-testing manifest, mirroring
// TestEnsureChromium_DownloadsAndExtracts's fixture-zip technique.
func TestPreprovision_BrokenPATH_EmptyInstallRoot_Downloads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "google-chrome"), "#!/bin/sh\nexit 1\n")
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	// Fake CfT zip containing one chrome-headless-shell file.
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	subdir := cftDownloadID + "-" + platform
	header := &zip.FileHeader{Name: subdir + "/" + headlessShellBinaryName()}
	header.SetMode(0o755)
	w, err := zw.CreateHeader(header)
	require.NoError(t, err)
	_, err = w.Write([]byte("#!/bin/sh\nexit 0\n"))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	zipBytes := zipBuf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		// downloadFile now verifies the GCS-standard X-Goog-Hash checksum
		// before trusting the download (verifyGoogHashMD5) — set it here so
		// this fixture's fake zip response passes integrity verification,
		// same as installer_test.go's googHashMD5Header helper.
		w.Header().Set("X-Goog-Hash", googHashMD5Header(zipBytes))
		_, _ = w.Write(zipBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Squad K (founder ruling 2026-09-19): selectDownloadBuild defaults to
	// full chrome on linux AND darwin; on any other platform it picks
	// headless-shell. The test forces headless-shell selection via the
	// selectDownloadBuildGOOS seam (just below) so the test exercises the
	// DOWNLOAD path on a headless-shell zip fixture, host-agnostically.
	// The manifest therefore only needs the headless-shell entry — the
	// (prior commit's) full-chrome entry was cosmetic dead config because
	// the test never asks for the full build, and (per the A4 advisory)
	// it has been removed.
	manifest := cftManifest{
		Channels: map[string]struct {
			Version   string                              `json:"version"`
			Downloads map[string][]cftManifestDownloadRef `json:"downloads"`
		}{
			cftChannel: {
				Version: "131.0.6778.999",
				Downloads: map[string][]cftManifestDownloadRef{
					cftDownloadID: {{Platform: platform, URL: srv.URL + "/zip"}},
				},
			},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestBytes)
	})

	prev := globalManifestURLForTesting
	globalManifestURLForTesting = srv.URL + "/manifest"
	defer func() { globalManifestURLForTesting = prev }()

	// Squad K (founder ruling 2026-09-19): selectDownloadBuild defaults to
	// full chrome on linux AND darwin; on any other platform it picks
	// headless-shell. This test's zip fixture is a headless-shell zip, so
	// force headless-shell via the selectDownloadBuildGOOS seam
	// (mirrors goosForCapability / layoutsGOOS usage in the same file).
	// The DOWNLOAD mechanism is what this test exercises, not the
	// build-resolution policy; the seam is the documented way to make
	// this test pass on any host.
	prevBuildOS := selectDownloadBuildGOOS
	selectDownloadBuildGOOS = "windows"
	t.Cleanup(func() { selectDownloadBuildGOOS = prevBuildOS })

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	got, err := m.Preprovision(context.Background())
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, headlessShellBinaryName()))
	info, statErr := os.Stat(got)
	require.NoError(t, statErr)
	assert.NotZero(t, info.Mode()&0o111, "expected executable bit on downloaded binary")
}
