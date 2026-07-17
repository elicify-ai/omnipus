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

// TestVerifyGoogHashMD5_MultipleHeaderLines is the regression for the real GCS
// response shape: storage.googleapis.com sends the checksums as TWO SEPARATE
// X-Goog-Hash header lines, "crc32c=..." FIRST and "md5=..." second. An earlier
// verifier read only header.Get("X-Goog-Hash") (the first value), saw crc32c,
// found no md5, and HARD-REJECTED every legitimate Chrome-for-Testing download —
// silently breaking browser installation on every fresh install. The verifier
// must scan every X-Goog-Hash value.
func TestVerifyGoogHashMD5_MultipleHeaderLines(t *testing.T) {
	content := []byte("chrome-headless-shell.zip contents")
	sum := md5.Sum(content) //nolint:gosec // test fixture — mirrors the server's published md5.
	md5Hdr := googHashMD5Header(content)

	t.Run("crc32c line first then md5 line (real GCS order) -> accepted", func(t *testing.T) {
		h := http.Header{}
		h.Add("X-Goog-Hash", "crc32c=XqmS2Q==")
		h.Add("X-Goog-Hash", md5Hdr)
		if err := verifyGoogHashMD5(h, sum[:]); err != nil {
			t.Fatalf("md5 on the second X-Goog-Hash line must be found; got: %v", err)
		}
	})

	t.Run("md5 folded into one comma-joined line -> accepted", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-Goog-Hash", "crc32c=XqmS2Q==,"+md5Hdr)
		if err := verifyGoogHashMD5(h, sum[:]); err != nil {
			t.Fatalf("md5 in the comma-joined line must be found; got: %v", err)
		}
	})

	t.Run("only crc32c present (no md5 anywhere) -> rejected", func(t *testing.T) {
		h := http.Header{}
		h.Add("X-Goog-Hash", "crc32c=XqmS2Q==")
		if err := verifyGoogHashMD5(h, sum[:]); err == nil {
			t.Fatal("expected rejection when no X-Goog-Hash line carries an md5 checksum")
		}
	})

	t.Run("crc32c first + md5 second but content mismatches -> rejected", func(t *testing.T) {
		h := http.Header{}
		h.Add("X-Goog-Hash", "crc32c=XqmS2Q==")
		h.Add("X-Goog-Hash", md5Hdr)
		wrong := md5.Sum([]byte("different content")) //nolint:gosec // test fixture.
		if err := verifyGoogHashMD5(h, wrong[:]); err == nil {
			t.Fatal("expected a checksum-mismatch rejection")
		}
	})
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
	// EnsureChromium resolves selectDownloadBuild() (full chrome) via a
	// per-build lookup, not detect-either — seed the build it actually
	// requests so the cache hit short-circuits before any network call. Also
	// point the manifest at an unreachable local URL so a cache MISS would
	// fail fast with a connection error rather than silently (and slowly)
	// reaching the real chrome-for-testing endpoint over the network.
	withManifestURL(t, "http://127.0.0.1:1/unreachable-manifest")
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())

	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium with cached binary: %v", err)
	}
	if got != binPath {
		t.Fatalf("expected cached binary %q, got %q", binPath, got)
	}
}

// TestInstaller_EnsureChromiumBuild_ResolvesTheSpecificallyRequestedBuild is
// the Option-A (ADR-044) regression case superseding the pre-Option-A
// "detect-either, prefer full when both cached" behavior: the agent build
// (chrome-headless-shell) and the encoder build (full "chrome") are
// NON-INTERCHANGEABLE — the encoder needs full Chrome's WebCodecs
// VideoEncoder, which chrome-headless-shell entirely lacks, so handing the
// encoder a cached headless-shell binary would silently break video. With
// BOTH builds already cached on disk, EnsureChromiumBuild must resolve
// EXACTLY the build it was asked for, never substituting the other flavor
// even though it too is available — proven in both directions so this cannot
// pass by coincidentally always returning the same one build.
func TestInstaller_EnsureChromiumBuild_ResolvesTheSpecificallyRequestedBuild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	root := t.TempDir()
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	shellPath := seedBuildBinary(t, root, "131.0.6778.108", platform, headlessShellBuild())
	fullPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())

	// No manifest URL configured — if EnsureChromiumBuild tried to hit the
	// network it would fail closed (empty URL), proving the cache hit
	// short-circuits before any fetch in either direction.
	withManifestURL(t, "")

	// Requesting the full build, with headless-shell ALSO cached, must return
	// the full build — not the other cached flavor.
	gotFull, err := EnsureChromiumBuild(context.Background(), root, fullChromeBuild())
	if err != nil {
		t.Fatalf("EnsureChromiumBuild(fullChromeBuild()) with both builds cached: %v", err)
	}
	if gotFull != fullPath {
		t.Fatalf("expected the full-chrome binary %q, got %q", fullPath, gotFull)
	}

	// Requesting headless-shell, with the full build ALSO cached, must return
	// headless-shell — the differentiation half of this proof: swapping only
	// the requested build flips the resolved binary, so the first assertion
	// wasn't just "always returns whatever's first."
	gotShell, err := EnsureChromiumBuild(context.Background(), root, headlessShellBuild())
	if err != nil {
		t.Fatalf("EnsureChromiumBuild(headlessShellBuild()) with both builds cached: %v", err)
	}
	if gotShell != shellPath {
		t.Fatalf("expected the chrome-headless-shell binary %q, got %q", shellPath, gotShell)
	}
	if gotShell == gotFull {
		t.Fatalf("expected the two requested builds to resolve to DIFFERENT binaries, both resolved to %q", gotShell)
	}
}

// TestInstaller_FullChromeDefault_Always is the single-Chrome collapse
// (ADR-044 Amendment) regression case: the agent's own browser is now the
// full "chrome" build, since it also hosts the live-view WebCodecs encoder
// tab in its default browser context, so a fresh EnsureChromium download
// must always fetch full chrome — never chrome-headless-shell (that build
// is only ever reached via the F-08 fallback when full chrome can't be
// resolved, exercised separately).
func TestInstaller_FullChromeDefault_Always(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	build := fullChromeBuild()
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
			cftFullChromeDownloadID: srv.URL + "/zip",
			cftDownloadID:           srv.URL + "/should-not-be-fetched",
		}))
	})
	withManifestURL(t, srv.URL+"/manifest")

	root := t.TempDir()
	got, err := EnsureChromium(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromium download path: %v", err)
	}
	if !strings.HasSuffix(got, fullChromeBinaryRelPath()) {
		t.Fatalf("expected full chrome binary path, got %q", got)
	}
	info, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("expected executable bit on %s", got)
	}
}

// TestInstaller_SelectDownloadBuild_MissingFromManifest_FallsBackToHeadlessShell
// is the F-08 graceful-degradation regression: when the manifest carries no
// "chrome" (full build) entry at all — e.g. a feed that only ships
// chrome-headless-shell — EnsureChromiumBuild(fullChromeBuild()) (what
// EnsureChromium now requests via selectDownloadBuild) must fall back to
// chrome-headless-shell rather than failing the install outright. Under the
// single-Chrome collapse this fallback is no longer an encoder-only nicety —
// it's the path that keeps a fresh install browsing at all when the full
// build can't be resolved (Constraint #4).
func TestInstaller_SelectDownloadBuild_MissingFromManifest_FallsBackToHeadlessShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only path layout")
	}
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	shellBuild := headlessShellBuild()
	content := []byte("#!/bin/sh\nexit 0\n")
	zipBytes := buildZipFixture(t, shellBuild, platform, content)

	mux := http.NewServeMux()
	mux.HandleFunc("/zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Goog-Hash", googHashMD5Header(zipBytes))
		_, _ = w.Write(zipBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/manifest", func(w http.ResponseWriter, _ *http.Request) {
		// Deliberately no cftFullChromeDownloadID key at all.
		_, _ = w.Write(manifestFor(t, "131.0.6778.999", platform, map[string]string{
			cftDownloadID: srv.URL + "/zip",
		}))
	})
	withManifestURL(t, srv.URL+"/manifest")

	root := t.TempDir()
	got, err := EnsureChromiumBuild(context.Background(), root, selectDownloadBuild())
	if err != nil {
		t.Fatalf("expected fallback to chrome-headless-shell to succeed, got: %v", err)
	}
	if !strings.HasSuffix(got, headlessShellBinaryName()) {
		t.Fatalf("expected the fallback chrome-headless-shell binary path, got %q", got)
	}
	if _, statErr := os.Stat(got); statErr != nil {
		t.Fatalf("expected the fallback binary to exist on disk: %v", statErr)
	}
}

// TestInstaller_EnsureChromiumFullBuild_DetectsEither_VerifiesIntegrity is
// the Option-A (ADR-044) centerpiece for the dedicated live-view encoder's
// install path: it proves, in one flow —
//  1. EnsureChromiumFullBuild downloads the full "chrome" build (not
//     chrome-headless-shell) — its own download key, binary name, and
//     on-disk layout;
//  2. the downloaded archive's integrity is verified against the
//     GCS-published X-Goog-Hash checksum before the binary is trusted;
//  3. a second EnsureChromiumFullBuild call against the same install root
//     detects the already-extracted binary and re-downloads nothing (F-08
//     detect-either);
//  4. a corrupted/mismatched checksum on a fresh install is rejected outright
//     — no binary is ever extracted or left on disk (DS-5 "bad hash -> reject
//     download").
func TestInstaller_EnsureChromiumFullBuild_DetectsEither_VerifiesIntegrity(t *testing.T) {
	platform, err := cftPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

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
	got, err := EnsureChromiumFullBuild(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromiumFullBuild full-chrome download: %v", err)
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
	got2, err := EnsureChromiumFullBuild(context.Background(), root)
	if err != nil {
		t.Fatalf("EnsureChromiumFullBuild second call: %v", err)
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

	_, err = EnsureChromiumFullBuild(context.Background(), badZipRoot)
	if err == nil {
		t.Fatal("expected EnsureChromiumFullBuild to reject a bad-hash download, got nil error")
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

	// The manifest below deliberately carries no full-chrome (cftFullChromeDownloadID)
	// entry, so EnsureChromium's selectDownloadBuild()==fullChromeBuild() request
	// takes the F-08 "missing from manifest" fallback to chrome-headless-shell —
	// exercising the integrity check on whichever build is actually resolved,
	// same as production.
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

	prev := allowHeaderlessDownloadForTesting
	allowHeaderlessDownloadForTesting = true
	t.Cleanup(func() { allowHeaderlessDownloadForTesting = prev })

	// No full-chrome manifest entry below -> EnsureChromium's
	// selectDownloadBuild() request falls back (F-08) to chrome-headless-shell.
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

// TestInstaller_SelectDownloadBuild_AlwaysFullChrome is the single-Chrome
// collapse (ADR-044 Amendment) regression guard: the agent's own browser is
// now the full "chrome" build, since it also hosts the live-view WebCodecs
// encoder tab in its default browser context — selectDownloadBuild must
// return it unconditionally on every platform. EnsureChromiumBuild's own
// F-08 fallback (exercised separately by
// TestInstaller_SelectDownloadBuild_MissingFromManifest_FallsBackToHeadlessShell)
// still drops to chrome-headless-shell when the full build can't be
// resolved — that's the graceful-degradation path, not this selector.
func TestInstaller_SelectDownloadBuild_AlwaysFullChrome(t *testing.T) {
	got := selectDownloadBuild()
	if got.downloadID != cftFullChromeDownloadID {
		t.Fatalf(
			"expected selectDownloadBuild to always default to full chrome (downloadID %q), got %q",
			cftFullChromeDownloadID,
			got.downloadID,
		)
	}
}
