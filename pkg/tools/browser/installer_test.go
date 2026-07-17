package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // test fixture only — computing the same X-Goog-Hash the production verifier checks.
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// --- fixture helpers -------------------------------------------------------

// seedBuildBinary creates a fake managed-install binary at build's on-disk
// layout under installRoot/<version>/<build.subdir(platform)>/<binary>, so
// EnsureChromium/findInstalledBinary treat it as already installed.
func seedBuildBinary(t *testing.T, installRoot, version, platform string, build chromiumBuild) string {
	t.Helper()
	versionDir := filepath.Join(installRoot, version, build.subdir(platform))
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(versionDir, build.binaryPath())
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binPath
}

// buildZipFixture returns zip bytes containing a single file at
// build.subdir(platform)+"/"+build.binaryPath() (the real CfT archive
// layout), plus its content, so extractZip lands the binary exactly where
// EnsureChromium expects it for that build.
func buildZipFixture(t *testing.T, build chromiumBuild, platform string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	header := &zip.FileHeader{Name: build.subdir(platform) + "/" + build.binaryPath()}
	header.SetMode(0o755)
	w, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// googHashMD5Header returns the GCS-style X-Goog-Hash header value carrying
// content's real MD5 digest — the header verifyGoogHashMD5 checks in
// production.
func googHashMD5Header(content []byte) string {
	sum := md5.Sum(content) //nolint:gosec // see import comment.
	return "md5=" + base64.StdEncoding.EncodeToString(sum[:])
}

// manifestFor builds a cftManifest JSON body with the given per-build zip
// URLs for platform, mirroring the real CfT feed's shape (both "chrome" and
// "chrome-headless-shell" keys can coexist under one channel).
func manifestFor(t *testing.T, version, platform string, urlsByDownloadID map[string]string) []byte {
	t.Helper()
	downloads := make(map[string][]cftManifestDownloadRef, len(urlsByDownloadID))
	for id, url := range urlsByDownloadID {
		downloads[id] = []cftManifestDownloadRef{{Platform: platform, URL: url}}
	}
	manifest := cftManifest{
		Channels: map[string]struct {
			Version   string                              `json:"version"`
			Downloads map[string][]cftManifestDownloadRef `json:"downloads"`
		}{
			cftChannel: {Version: version, Downloads: downloads},
		},
	}
	b, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withManifestURL points globalManifestURLForTesting at url for the duration
// of the test, restoring the previous value on cleanup.
func withManifestURL(t *testing.T, url string) {
	t.Helper()
	prev := globalManifestURLForTesting
	globalManifestURLForTesting = url
	t.Cleanup(func() { globalManifestURLForTesting = prev })
}

// withDisplaySidecarHealthy forces DisplaySidecarHealthyProbe (the AR-C1/
// GC-1/GC-2 Option-B dormant gate — NOT an Xvfb-binary-on-PATH probe) to
// return want for the duration of the test, restoring the previous probe on
// cleanup.
func withDisplaySidecarHealthy(t *testing.T, want bool) {
	t.Helper()
	prev := DisplaySidecarHealthyProbe
	DisplaySidecarHealthyProbe = func() bool { return want }
	t.Cleanup(func() { DisplaySidecarHealthyProbe = prev })
}

// --- tests -------------------------------------------------------------

func TestInstaller_PrefersInstalledBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	root := t.TempDir()
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, headlessShellBuild())

	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium with cached binary: %v", err)
	}
	if got != binPath {
		t.Fatalf("expected cached binary %q, got %q", binPath, got)
	}
}

// TestInstaller_DetectsEither_PrefersFullChromeWhenBothCached covers DS-5's
// "both cached (full + headless-shell) -> video-capable (prefer full)" row at
// the installer level: when both builds are already on disk, EnsureChromium
// must return the full "chrome" build without touching the network, since it
// is a strict superset of headless-shell's browsing capability.
func TestInstaller_DetectsEither_PrefersFullChromeWhenBothCached(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	root := t.TempDir()
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	seedBuildBinary(t, root, "131.0.6778.108", platform, headlessShellBuild())
	fullPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())

	// No manifest URL configured — if EnsureChromium tried to hit the
	// network it would fail closed (empty URL), proving detect-either
	// short-circuits before any fetch.
	withManifestURL(t, "")

	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium with both builds cached: %v", err)
	}
	if got != fullPath {
		t.Fatalf("expected full-chrome binary %q preferred, got %q", fullPath, got)
	}
}

// TestInstaller_HeadlessShellDefault_WhenDisplaySidecarNotHealthy is the F-08
// regression case: with no healthy display sidecar wired (the pre-Gate-0 /
// non-video-capable default posture — AR-C1/GC-1/GC-2's Option-B dormant
// gate), a fresh EnsureChromium download must still fetch
// chrome-headless-shell, matching pre-dual-download behavior byte for byte.
func TestInstaller_HeadlessShellDefault_WhenDisplaySidecarNotHealthy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withDisplaySidecarHealthy(t, false)

	build := headlessShellBuild()
	content := []byte("#!/bin/sh\nexit 0\n")
	zipBytes := buildZipFixture(t, build, platform, content)

	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Goog-Hash", googHashMD5Header(zipBytes))
		_, _ = w.Write(zipBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestFor(t, "131.0.6778.999", platform, map[string]string{
			cftDownloadID:           srv.URL + "/zip",
			cftFullChromeDownloadID: srv.URL + "/should-not-be-fetched",
		}))
	})
	withManifestURL(t, srv.URL+"/manifest")

	root := t.TempDir()
	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium download path: %v", err)
	}
	if !strings.HasSuffix(got, headlessShellBinaryName()) {
		t.Fatalf("expected chrome-headless-shell binary path, got %q", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected executable bit on %s", got)
	}
}

// TestInstaller_FullChromeDefault_DetectsEither_VerifiesIntegrity is the F-08
// / FR-009 / Test 15 / DS-5 centerpiece: it proves, in one flow —
//  1. a fresh install with a healthy display sidecar wired downloads the full
//     "chrome" build (not chrome-headless-shell) — its own download key,
//     binary name, and on-disk layout;
//  2. the downloaded archive's integrity is verified against the
//     GCS-published X-Goog-Hash checksum before the binary is trusted;
//  3. a second EnsureChromium call against the same install root detects the
//     already-extracted binary and re-downloads nothing (F-08 detect-either);
//  4. a corrupted/mismatched checksum on a fresh install is rejected outright
//     — no binary is ever extracted or left on disk (DS-5 "bad hash -> reject
//     download").
func TestInstaller_FullChromeDefault_DetectsEither_VerifiesIntegrity(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("full-chrome-default selection only applies on linux (Platform Matrix, M-3)")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withDisplaySidecarHealthy(t, true)

	fullBuild := fullChromeBuild()
	content := []byte("#!/bin/sh\nexit 0\n# full chrome fixture\n")
	goodZip := buildZipFixture(t, fullBuild, platform, content)

	var zipHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&zipHits, 1)
		w.Header().Set("X-Goog-Hash", googHashMD5Header(goodZip))
		_, _ = w.Write(goodZip)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	// The manifest body embeds srv.URL, which isn't known until after
	// NewServer returns; the mux is mutable so registering the /manifest
	// route now (after start) still takes effect, exactly like the original
	// EnsureChromium download test's pattern.
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestFor(t, "131.0.6778.999", platform, map[string]string{
			cftFullChromeDownloadID: srv.URL + "/zip",
			cftDownloadID:           srv.URL + "/should-not-be-fetched",
		}))
	})
	withManifestURL(t, srv.URL+"/manifest")

	root := t.TempDir()

	// Step 1+2: fresh download picks the full build and verifies integrity.
	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium full-chrome download: %v", err)
	}
	if strings.Contains(got, "chrome-headless-shell") {
		t.Fatalf("expected the full chrome build to be selected, got headless-shell path %q", got)
	}
	wantSubdir := fullBuild.subdir(platform)
	if !strings.Contains(got, wantSubdir) {
		t.Fatalf("expected binary under %q layout, got %q", wantSubdir, got)
	}
	if filepath.Base(got) != filepath.Base(fullBuild.binaryPath()) {
		t.Fatalf("expected binary name %q, got %q", filepath.Base(fullBuild.binaryPath()), filepath.Base(got))
	}
	info, statErr := os.Stat(got)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected executable bit on %s", got)
	}
	if hits := atomic.LoadInt32(&zipHits); hits != 1 {
		t.Fatalf("expected exactly 1 zip fetch on fresh install, got %d", hits)
	}

	// Step 3: detect-either — a second call against the same root must not
	// touch the network again.
	got2, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium second call: %v", err)
	}
	if got2 != got {
		t.Fatalf("expected the same cached binary on re-resolve, got %q vs %q", got2, got)
	}
	if hits := atomic.LoadInt32(&zipHits); hits != 1 {
		t.Fatalf("expected no additional zip fetch on cache hit, got %d total", hits)
	}

	// Step 4: bad hash on a fresh (empty) install root must be rejected
	// before any binary is extracted or left on disk.
	badZipRoot := t.TempDir()
	var badZipHits int32
	badMux := http.NewServeMux()
	badMux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&badZipHits, 1)
		// Declare a checksum that does not match the body we actually send.
		w.Header().Set("X-Goog-Hash", googHashMD5Header([]byte("not the real content")))
		_, _ = w.Write(goodZip)
	})
	badSrv := httptest.NewServer(badMux)
	defer badSrv.Close()
	badMux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestFor(t, "131.0.6778.999", platform, map[string]string{
			cftFullChromeDownloadID: badSrv.URL + "/zip",
		}))
	})
	withManifestURL(t, badSrv.URL+"/manifest")

	_, err = EnsureChromium(context.Background(), badZipRoot)
	if err == nil {
		t.Fatal("expected EnsureChromium to reject a bad-hash download, got nil error")
	}
	if !strings.Contains(err.Error(), "integrity") && !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected an integrity/checksum error, got: %v", err)
	}
	if atomic.LoadInt32(&badZipHits) != 1 {
		t.Fatalf("expected exactly 1 zip fetch attempt on the bad-hash install")
	}
	// No binary must have been extracted anywhere under badZipRoot.
	expectBadBin := fullBuild.binaryFullPath(filepath.Join(badZipRoot, "131.0.6778.999"), platform)
	if _, statErr := os.Stat(expectBadBin); !os.IsNotExist(statErr) {
		t.Fatalf("expected no binary at %s after a bad-hash rejection, stat err: %v", expectBadBin, statErr)
	}
	// No leftover .part-* temp files or the .zip itself either.
	versionDir := filepath.Join(badZipRoot, "131.0.6778.999")
	entries, _ := os.ReadDir(versionDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part-") || strings.HasSuffix(e.Name(), ".zip") {
			t.Fatalf("expected no leftover temp/zip files after a rejected download, found %q", e.Name())
		}
	}
}

// TestInstaller_MissingGoogHashHeader_RejectedByDefault is the SF1
// regression test: a download response that carries NO X-Goog-Hash header at
// all (e.g. a stripped proxy, a non-GCS mirror, or a tampered response) must
// be rejected outright by default — never installed "unverified" — matching
// ADR §6.5/§6.6's "verify integrity before it becomes the runtime". Before
// the fix, downloadFile only WARNed and installed the binary anyway.
func TestInstaller_MissingGoogHashHeader_RejectedByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withDisplaySidecarHealthy(t, false) // exercise the headless-shell download path

	build := headlessShellBuild()
	content := []byte("#!/bin/sh\nexit 0\n")
	zipBytes := buildZipFixture(t, build, platform, content)

	var zipHits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&zipHits, 1)
		// Deliberately no X-Goog-Hash header at all.
		_, _ = w.Write(zipBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestFor(t, "131.0.6778.999", platform, map[string]string{
			cftDownloadID: srv.URL + "/zip",
		}))
	})
	withManifestURL(t, srv.URL+"/manifest")

	root := t.TempDir()
	_, err = EnsureChromium(context.Background(), root)
	if err == nil {
		t.Fatal("expected EnsureChromium to reject a headerless download, got nil error")
	}
	if !strings.Contains(err.Error(), "X-Goog-Hash") {
		t.Fatalf("expected an X-Goog-Hash-related rejection error, got: %v", err)
	}
	if atomic.LoadInt32(&zipHits) != 1 {
		t.Fatalf("expected exactly 1 zip fetch attempt, got %d", zipHits)
	}
	// No binary must have been extracted anywhere under root.
	expectBin := build.binaryFullPath(filepath.Join(root, "131.0.6778.999"), platform)
	if _, statErr := os.Stat(expectBin); !os.IsNotExist(statErr) {
		t.Fatalf("expected no binary at %s after a headerless-download rejection, stat err: %v", expectBin, statErr)
	}
}

// TestInstaller_MissingGoogHashHeader_AcceptedWhenExplicitlyOptedIn proves
// the opt-in escape hatch works and is a same-package-test-only seam, not a
// production default: with allowHeaderlessDownloadForTesting forced true, a
// headerless response is accepted.
func TestInstaller_MissingGoogHashHeader_AcceptedWhenExplicitlyOptedIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withDisplaySidecarHealthy(t, false)

	prev := allowHeaderlessDownloadForTesting
	allowHeaderlessDownloadForTesting = true
	t.Cleanup(func() { allowHeaderlessDownloadForTesting = prev })

	build := headlessShellBuild()
	content := []byte("#!/bin/sh\nexit 0\n")
	zipBytes := buildZipFixture(t, build, platform, content)

	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(zipBytes) // no X-Goog-Hash header
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(manifestFor(t, "131.0.6778.999", platform, map[string]string{
			cftDownloadID: srv.URL + "/zip",
		}))
	})
	withManifestURL(t, srv.URL+"/manifest")

	root := t.TempDir()
	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("expected the headerless download to be accepted under the opt-in, got: %v", err)
	}
	if _, statErr := os.Stat(got); statErr != nil {
		t.Fatalf("expected the installed binary to exist: %v", statErr)
	}
}

// TestInstaller_SelectDownloadBuild_DormantByDefault_OptionB is the AR-C1/
// GC-1/GC-2 coherent-dormant regression guard for the installer side of the
// same defect capability_test.go's TestVideoCapability_DormantByDefault_
// OptionB guards on the classifier side: with DisplaySidecarHealthyProbe left
// UNTOUCHED (the shipped default, always false), selectDownloadBuild must
// choose chrome-headless-shell on linux regardless of what's on PATH — it
// must never regress into reading an Xvfb-binary-on-PATH signal.
func TestInstaller_SelectDownloadBuild_DormantByDefault_OptionB(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip(
			"this guard only matters on linux — selectDownloadBuild's only branch that could ever pick the full chrome build",
		)
	}
	got := selectDownloadBuild()
	if got.downloadID != cftDownloadID {
		t.Fatalf(
			"expected selectDownloadBuild to default to chrome-headless-shell (downloadID %q) with no display sidecar wired, got %q",
			cftDownloadID,
			got.downloadID,
		)
	}
}
