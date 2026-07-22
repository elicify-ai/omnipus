package browser

// package_chrome_darwin_layout_test.go — linux-runnable regression coverage
// for the macOS .app package-Chrome layout (ADR-052 Phase 3 / C3 option ii).
//
// Phase 2's lesson: cross-script layout disagreements (producer vs runtime)
// are caught only by a test that exercises the REAL producer layout. The
// darwin .app layout is GOOS-gated in binaryLayoutsForRoot, so these tests
// inject layoutsGOOS="darwin" via the seam (no macOS runner needed) and
// stage the exact layout cft-bundle.sh + install.sh produce:
//
//	<root>/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing
//	<root>/chrome-mac-arm64/chrome.sha256   (beside the .app, outside the signed bundle)
//
// findPackageChrome must resolve BOTH the nested binary and the
// extract-subdir-level manifest. Before the fix, binaryLayoutsForRoot probed
// <root>/.app/... (no extract subdir) and the manifest probe never reached
// <root>/chrome-mac-arm64/chrome.sha256 — so the runtime would have missed
// the bundled Chrome on macOS and fallen through to a managed download.

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeDarwinChromeSHAHex is a placeholder sha256sum-format line for the staged
// chrome.sha256 manifest. findPackageChrome only Lstat's the manifest (it does
// not hash), so the digest need not match the fake binary.
const fakeDarwinChromeSHAHex = "0000000000000000000000000000000000000000000000000000000000000000  chrome\n"

// withDarwinLayouts flips the layoutsGOOS seam to "darwin" for one test and
// restores it on cleanup.
func withDarwinLayouts(t *testing.T) {
	t.Helper()
	prev := layoutsGOOS
	layoutsGOOS = "darwin"
	t.Cleanup(func() { layoutsGOOS = prev })
}

// seedDarwinPackageChrome stages the real producer+install layout under root:
// the nested .app binary + the chrome.sha256 manifest beside the .app. Returns
// the expected binary + manifest paths. The manifest is valid (matches the
// binary) so a full verify would also pass, though findPackageChrome itself
// only Lstat's the manifest.
func seedDarwinPackageChrome(t *testing.T, root, subdir string) (binPath, shaPath string) {
	t.Helper()
	binPath = filepath.Join(root, subdir, darwinChromeAppRel)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir .app/Contents/MacOS: %v", err)
	}
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\necho fake-chrome\n"), 0o755); err != nil {
		t.Fatalf("write fake chrome binary: %v", err)
	}
	shaPath = filepath.Join(root, subdir, "chrome.sha256")
	if err := os.WriteFile(shaPath, []byte(fakeDarwinChromeSHAHex), 0o644); err != nil {
		t.Fatalf("write chrome.sha256: %v", err)
	}
	return binPath, shaPath
}

func TestFindPackageChrome_DarwinAppBundledLayout_Found(t *testing.T) {
	withDarwinLayouts(t)
	root := t.TempDir()
	wantBin, wantSha := seedDarwinPackageChrome(t, root, "chrome-mac-arm64")

	gotBin, gotSha := findPackageChrome(root)
	if gotBin != wantBin {
		t.Errorf(
			"binary: got %q, want %q (must reach the .app nested under the chrome-mac-arm64 extract subdir)",
			gotBin,
			wantBin,
		)
	}
	if gotSha != wantSha {
		t.Errorf(
			"manifest: got %q, want %q (must be beside the .app at <root>/chrome-mac-arm64/chrome.sha256)",
			gotSha,
			wantSha,
		)
	}
}

func TestFindPackageChrome_DarwinAppBundledLayout_x64_Found(t *testing.T) {
	withDarwinLayouts(t)
	root := t.TempDir()
	wantBin, wantSha := seedDarwinPackageChrome(t, root, "chrome-mac-x64")

	gotBin, gotSha := findPackageChrome(root)
	if gotBin != wantBin {
		t.Errorf("binary: got %q, want %q", gotBin, wantBin)
	}
	if gotSha != wantSha {
		t.Errorf("manifest: got %q, want %q", gotSha, wantSha)
	}
}

// TestFindPackageChrome_DarwinApp_ManifestBesideApp_NotAtRoot proves the
// runtime finds the manifest where the producer WRITES it (beside the .app,
// in the extract subdir), NOT at the chromium root — the exact Phase-2-style
// cross-script disagreement this guards against.
func TestFindPackageChrome_DarwinApp_ManifestBesideApp_NotAtRoot(t *testing.T) {
	withDarwinLayouts(t)
	root := t.TempDir()
	// Binary present, manifest ONLY beside the .app (not at root).
	binPath, shaPath := seedDarwinPackageChrome(t, root, "chrome-mac-arm64")
	// Deliberately do NOT create <root>/chrome.sha256.

	gotBin, gotSha := findPackageChrome(root)
	if gotBin != binPath {
		t.Fatalf("binary: got %q, want %q", gotBin, binPath)
	}
	if gotSha != shaPath {
		t.Errorf(
			"manifest must resolve to the beside-.app location %q, got %q — the runtime would miss the producer's manifest and fall through to a managed download",
			shaPath,
			gotSha,
		)
	}
}

// TestBinaryLayoutsForRoot_DarwinIncludesExtractSubdirs pins the candidate
// list so a refactor can't silently drop the chrome-mac-{arm64,x64} probes.
func TestBinaryLayoutsForRoot_DarwinIncludesExtractSubdirs(t *testing.T) {
	withDarwinLayouts(t)
	root := "/r"
	got := binaryLayoutsForRoot(root)
	wantArm64 := filepath.Join(root, "chrome-mac-arm64", darwinChromeAppRel)
	wantX64 := filepath.Join(root, "chrome-mac-x64", darwinChromeAppRel)
	has := func(p string) bool {
		for _, g := range got {
			if g == p {
				return true
			}
		}
		return false
	}
	if !has(wantArm64) {
		t.Errorf("darwin layouts missing %q; got %v", wantArm64, got)
	}
	if !has(wantX64) {
		t.Errorf("darwin layouts missing %q; got %v", wantX64, got)
	}
}
