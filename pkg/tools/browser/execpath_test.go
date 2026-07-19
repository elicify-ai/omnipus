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

// --- resolveExecPath: PATH-candidate validation ---

// TestResolveExecPath_SkipsBrokenPATHCandidate reproduces the snap-stub bug:
// the first candidate name (google-chrome) resolves via LookPath but exits
// non-zero when actually run; resolution must skip it and pick the next,
// genuinely-working candidate (chromium) rather than committing to the
// broken one.
func TestResolveExecPath_SkipsBrokenPATHCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "google-chrome"), "#!/bin/sh\nexit 1\n")
	chromiumPath := filepath.Join(binDir, "chromium")
	writeExecutable(t, chromiumPath, "#!/bin/sh\necho 'Chromium 131.0.6778.108'\nexit 0\n")

	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, chromiumPath, got)
}

// TestResolveExecPath_AllBrokenFallsThroughToManaged covers every PATH
// candidate name failing its probe: resolution must fall through to the
// managed install path and find the pre-seeded binary there, never touching
// the network (no manifest server configured for this test).
func TestResolveExecPath_AllBrokenFallsThroughToManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		writeExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	binPath := seedManagedBinary(t, installRootFor(cfg), platform)

	m := &BrowserManager{cfg: cfg}
	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, binPath, got)
}

// TestResolveExecPath_CachesAfterFirstProbe verifies the second
// resolveExecPath call on the same manager reuses the cached path instead of
// re-shelling-out to probe the candidate again — proven via a counter file
// the fake script increments on every real invocation.
func TestResolveExecPath_CachesAfterFirstProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	binDir := t.TempDir()
	counterFile := filepath.Join(t.TempDir(), "probe-count")
	t.Setenv("BROWSER_TEST_PROBE_COUNTER", counterFile)
	script := "#!/bin/sh\necho x >> \"$BROWSER_TEST_PROBE_COUNTER\"\necho 'Chromium 131.0.6778.108'\nexit 0\n"
	candidatePath := filepath.Join(binDir, "google-chrome")
	writeExecutable(t, candidatePath, script)

	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	first, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, candidatePath, first)

	second, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, first, second)

	data, err := os.ReadFile(counterFile)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(data), "x"),
		"expected exactly one real probe invocation across two resolveExecPath calls")
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
// that when a validated system Chromium is present, Preprovision resolves
// to it and never creates the managed install directory (no download
// attempted when nothing is needed).
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
	m := &BrowserManager{cfg: cfg}

	got, err := m.Preprovision(context.Background())
	require.NoError(t, err)
	assert.Equal(t, chromiumPath, got)

	_, statErr := os.Stat(installRootFor(cfg))
	assert.True(t, os.IsNotExist(statErr),
		"Preprovision must not create the managed install dir when a PATH candidate resolves")
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

	// Deliberately no cftFullChromeDownloadID entry: on linux this exercises
	// EnsureChromiumBuild's "preferred build missing from manifest" fallback
	// to chrome-headless-shell (selectDownloadBuild requests full chrome
	// first); off linux, selectDownloadBuild already requests
	// chrome-headless-shell directly. Either way resolution lands on the
	// headless-shell zip fixture below.
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

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	got, err := m.Preprovision(context.Background())
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(got, headlessShellBinaryName()))
	info, statErr := os.Stat(got)
	require.NoError(t, statErr)
	assert.NotZero(t, info.Mode()&0o111, "expected executable bit on downloaded binary")
}

// --- resolveExecPath: ExecPath override (operator trust) ---

// TestResolveExecPath_ExecPathOverride_ReturnedVerbatim_NoProbe verifies that
// when cfg.ExecPath is set to a real executable file, resolveExecPath returns
// it verbatim and NEVER probes $PATH — proven via a decoy google-chrome on
// PATH that records itself (mkdir is atomic, so concurrent-safe) if its probe
// ever ran. The override is an operator trust: honored as-is after the
// stat/dir/exec-bit check instead of being silently replaced by a discovered
// binary.
func TestResolveExecPath_ExecPathOverride_ReturnedVerbatim_NoProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix executable-bit layout")
	}
	overrideDir := t.TempDir()
	overrideBin := filepath.Join(overrideDir, "my-chrome")
	writeExecutable(t, overrideBin, "#!/bin/sh\nexit 0\n")

	// Decoy google-chrome on a separate PATH: records itself under probeDir if
	// its probe ever runs. Empty probeDir after the call proves no probe ran.
	probeDir := t.TempDir()
	t.Setenv("BROWSER_TEST_PROBE_DIR", probeDir)
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "google-chrome"),
		"#!/bin/sh\nmkdir \"$BROWSER_TEST_PROBE_DIR/$$\" 2>/dev/null\necho 'Chromium 131.0.6778.108'\nexit 0\n")
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.ExecPath = overrideBin
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, overrideBin, got)

	entries, _ := os.ReadDir(probeDir)
	assert.Empty(t, entries, "PATH probe must NOT run when ExecPath override is set and valid")
}

// TestResolveExecPath_ExecPathOverride_Directory_IsClearError verifies the L3
// directory guard: an exec_path that points at a directory fails fast with a
// clear message naming exec_path and "directory", instead of the downstream
// chromedp exec error ("permission denied"/"exec format error") that never
// names the real cause.
func TestResolveExecPath_ExecPathOverride_Directory_IsClearError(t *testing.T) {
	dir := t.TempDir()
	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.ExecPath = dir // a directory, not a file
	m := &BrowserManager{cfg: cfg}

	_, err := m.resolveExecPath(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec_path")
	assert.Contains(t, err.Error(), "directory")
}

// TestResolveExecPath_ExecPathOverride_NotExecutable_IsClearError verifies the
// L3 exec-bit guard (POSIX only): an exec_path that exists and is a regular
// file but lacks any execute bit fails fast with a clear message, instead of
// the downstream "permission denied" that never names exec_path. Skipped on
// Windows where Go's os.FileMode carries no Unix execute bits.
func TestResolveExecPath_ExecPathOverride_NotExecutable_IsClearError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod exec-bit is posix-only; Go FileMode carries no Unix exec bits on Windows")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "not-exec")
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o644))

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.ExecPath = binPath
	m := &BrowserManager{cfg: cfg}

	_, err := m.resolveExecPath(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exec_path")
	assert.Contains(t, err.Error(), "not executable")
}
