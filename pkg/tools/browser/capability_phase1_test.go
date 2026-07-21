package browser

// capability_phase1_test.go — coverage for ADR-052 Phase 1's capability-
// classifier changes: ClassifyVideoCapability and
// ClassifyVideoCapabilityWithExec learn the package-managed Chrome root
// (sibling chromium/ next to the binary) but the linux-only gate stays
// strict (M3/M6 — "only after per-OS audio verification"). Phase 1
// validates the linux layout only; the package Chrome is video-capable
// there, and the classifier returns Capable=true when its SHA-256 verifies
// (and its chrome.sha256 manifest verifies; missing metadata is refused).

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedPackageChromeAtRoot creates the package Chrome at the given root (a
// temp dir the test fully controls) with optional chrome.sha256. Used to
// stand up an "ADR-052 package install" fixture without symlinking a fake
// binary into the test binary's parent — same pattern as
// exec_resolver_phase1_test.go's seedPackageChrome but kept here for
// self-contained readability.
func seedPackageChromeAtRoot(t *testing.T, root string, writeSHA bool) (binPath, shaPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	binRel := fullChromeBinaryRelPath()
	binPath = filepath.Join(root, binRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0o755))
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	if !writeSHA {
		return binPath, ""
	}
	contents, err := os.ReadFile(binPath)
	require.NoError(t, err)
	sum := sha256.Sum256(contents)
	shaPath = filepath.Join(root, "chrome.sha256")
	require.NoError(t, os.WriteFile(shaPath, []byte(hex.EncodeToString(sum[:])+"\n"), 0o644))
	return binPath, shaPath
}

// TestClassifyVideoCapability_PackageChromeLinux_Capable is the Phase 1
// happy-path: linux + a package Chrome at the package root (sha256 matches)
// + an EMPTY installRoot (i.e. a fresh package install that hasn't yet
// created a per-profile managed install). Classifier MUST report Capable.
//
// This is the exact scenario the ADR's "A Linux package installs and
// resolves the bundled Chrome with no first-use download" goal describes.
func TestClassifyVideoCapability_PackageChromeLinux_Capable(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if runtime.GOOS != "linux" {
		t.Skipf("Phase 1 validates linux only; runtime=%s", runtime.GOOS)
	}
	withCapabilitySeams(t, "linux")

	pkgRoot := t.TempDir()
	seedPackageChromeAtRoot(t, pkgRoot, true)
	withPackageChromeRoot(t, pkgRoot)

	// Empty installRoot: the package Chrome is the ONLY full Chrome on
	// disk; the per-profile managed install hasn't been populated.
	got := ClassifyVideoCapability(t.TempDir())
	if !got.Capable {
		t.Fatalf("expected Capable=true on linux with a verified package Chrome, got %+v (reason=%q)", got, got.Reason)
	}
	if got.Reason != "" {
		t.Fatalf("Reason must be empty when Capable=true, got %q", got.Reason)
	}
}

// TestClassifyVideoCapability_PackageChromeLinux_NoSHA_Refused proves the
// SEC-ADR052-001 fail-closed contract on the capability classifier: linux
// + a package Chrome + NO chrome.sha256 manifest MUST classify not-capable
// (the resolver's fail-closed behavior propagates here — a missing
// manifest is a pipeline failure or tampering, both release blockers).
// The capability classifier must not advertise Capable=true when capture
// cannot succeed (ADR-048 condition 3) — and a package without integrity
// metadata cannot be verified, so it cannot succeed.
func TestClassifyVideoCapability_PackageChromeLinux_NoSHA_Refused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("Phase 1 validates linux only; runtime=%s", runtime.GOOS)
	}
	withCapabilitySeams(t, "linux")

	pkgRoot := t.TempDir()
	seedPackageChromeAtRoot(t, pkgRoot, false) // no chrome.sha256
	withPackageChromeRoot(t, pkgRoot)

	got := ClassifyVideoCapability(t.TempDir())
	if got.Capable {
		t.Fatalf("SEC-ADR052-001: package Chrome without chrome.sha256 must classify not-capable, got %+v (reason=%q)", got, got.Reason)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
}

// TestClassifyVideoCapability_PackageChromeLinux_SHAMismatch_NotCapable
// proves the M2 hard-fail-in-the-safe-direction behavior: a package Chrome
// with a chrome.sha256 whose digest disagrees. Classifier returns
// not-capable, the same way it would for an empty installRoot with no
// package Chrome — refuses to advertise Capable when integrity can't be
// confirmed.
func TestClassifyVideoCapability_PackageChromeLinux_SHAMismatch_NotCapable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("Phase 1 validates linux only; runtime=%s", runtime.GOOS)
	}
	withCapabilitySeams(t, "linux")

	pkgRoot := t.TempDir()
	_, _ = seedPackageChromeAtRoot(t, pkgRoot, true)
	// Overwrite chrome.sha256 with a wrong digest.
	require.NoError(t, os.WriteFile(filepath.Join(pkgRoot, "chrome.sha256"),
		[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))
	withPackageChromeRoot(t, pkgRoot)

	got := ClassifyVideoCapability(t.TempDir())
	if got.Capable {
		t.Fatalf("expected not-capable when package chrome.sha256 mismatches, got Capable=true (reason=%q)", got.Reason)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
	assert.Contains(t, got.Reason, "chrome.sha256 verification failed")
}

// TestClassifyVideoCapability_PackageChromeNonLinux_NotCapable proves the
// M3/M6 invariant: the linux-only gate stays strict in Phase 1 even when
// the package Chrome is present. Phase 3 (macOS audio) and Phase 4
// (Windows allocator) are the only paths that can relax this. Until then,
// a non-linux host with a package Chrome is still not-capable — refusing
// to advertise Capable when capture cannot succeed (ADR-048 condition 3).
func TestClassifyVideoCapability_PackageChromeNonLinux_NotCapable(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "android"} {
		t.Run(goos, func(t *testing.T) {
			withCapabilitySeams(t, goos)

			pkgRoot := t.TempDir()
			seedPackageChromeAtRoot(t, pkgRoot, true)
			withPackageChromeRoot(t, pkgRoot)

			got := ClassifyVideoCapability(t.TempDir())
			if got.Capable {
				t.Fatalf("expected not-capable on GOOS=%s even with a valid package Chrome (Phase 1 gate stays strict), got %+v", goos, got)
			}
			if got.Reason == "" {
				t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
			}
		})
	}
}

// TestClassifyVideoCapabilityWithExec_PreferPackaged_NoEffectOnLinuxGate
// proves the Phase 1 contract for the WithExec variant: PreferPackaged only
// changes the resolution-order priority in exec_resolver.go (which binary
// resolve() returns when both $PATH and the package root have a Chrome).
// It does NOT relax the linux-only video-capability gate here — that's
// sequenced for Phase 3/4 (ADR-052 M3/M6). Even with PreferPackaged=true
// and a non-linux exec path, ClassifyVideoCapabilityWithExec stays
// not-capable.
func TestClassifyVideoCapabilityWithExec_PreferPackaged_NoEffectOnLinuxGate(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			withCapabilitySeams(t, goos)

			// Simulate a non-linux exec path that is "the package Chrome"
			// — its basename is "chrome" (or the OS-appropriate binary
			// name), not headless-shell, so the basename heuristic is
			// satisfied. Only the GOOS seam can block capability here.
			execPath := filepath.Join(t.TempDir(), "chrome")
			got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
			if got.Capable {
				t.Fatalf("expected not-capable on GOOS=%s regardless of PreferPackaged (Phase 1 linux-gate stays strict), got %+v", goos, got)
			}
		})
	}
}

// TestClassifyVideoCapabilityWithExec_PreferPackaged_RespectsHeadlessShell
// proves the unchanged invariant: even with the new toggle, a headless-
// shell exec path stays not-capable on linux (no tabCapture surface).
// The toggle changes priority, not the "no tabCapture in shell" fact.
func TestClassifyVideoCapabilityWithExec_PreferPackaged_RespectsHeadlessShell(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withCapabilitySeams(t, "linux")

	execPath := filepath.Join(t.TempDir(), "chrome-headless-shell")
	got := ClassifyVideoCapabilityWithExec(execPath, t.TempDir())
	if got.Capable {
		t.Fatalf("expected not-capable for chrome-headless-shell basename, got %+v", got)
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
}

// TestClassifyVideoCapability_NoPackageChrome_NoInstallRoot_NotCapable
// proves the regression guard: with no package Chrome AND an empty
// installRoot (the "fresh install, nothing yet" case), the classifier
// stays not-capable. Phase 1 doesn't add a new default — it just lets a
// present package Chrome count.
func TestClassifyVideoCapability_NoPackageChrome_NoInstallRoot_NotCapable(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	withCapabilitySeams(t, "linux")

	// No package root — pin to a non-existent path so findPackageChrome's
	// os.Stat gate returns ("", "") immediately.
	withPackageChromeRoot(t, filepath.Join(t.TempDir(), "no-such-dir"))

	got := ClassifyVideoCapability(t.TempDir())
	if got.Capable {
		t.Fatalf("expected not-capable with no package Chrome and empty installRoot, got Capable=true")
	}
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason (O-3) on not-capable")
	}
}
