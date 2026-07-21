package browser

// exec_resolver_phase1_test.go — coverage for ADR-052 Phase 1 step 3
// (package-managed Chrome resolution) and its integrity verification
// (ADR-052 M2 — chromeintegrity.VerifyChromeSHA256). Builds on execpath_test.go's harness
// style (temp installRoot, no real network, every chromium-on-$PATH stub is
// a temp shell script with the correct #!/bin/sh + exit 0).
//
// The motivating behavior (ADR-052 D2/M1 + Phase 1 security review applied):
// on a Linux package install the bundled Chrome-for-Testing lives at
// <os.Executable()>/../chromium/chrome (M5: computed at runtime, no
// ldflags). It must resolve without ever touching the network — the
// "guaranteed floor" claim the ADR is built on. chrome.sha256 is REQUIRED
// (SEC-ADR052-001 fail-closed: a missing or unreadable manifest is a
// refusal, not an unverified acceptance — the only legitimate causes of a
// missing manifest are pipeline failures or tampering, both of which are
// release blockers). When chrome.sha256 disagrees with the binary's actual
// digest (SEC-ADR052-004 hardened parser + constant-time compare), the
// floor is a hard fail (M2 — at-least-as-strong-as-runtime-download).
//
// SEC-ADR052-002 (security-hardened default): a system Chrome on $PATH
// is RECORDED at WARN-BROWSER-007 and DISCARDED by default — operators
// who actually want a custom Chrome MUST opt in by setting
// tools.browser.trust_path_chrome=true (or use cfg.ExecPath, which always
// wins).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
)

// seedPackageChrome creates a fake package-managed Chrome at
// <root>/<fullChromeBinaryRelPath()> (the same layout EnsureChromiumBuild /
// findInstalledBuild inspect on the managed installRoot, so the package
// Chrome is at the layout ClassifyVideoCapability already knows) plus an
// optional chrome.sha256 with the binary's actual hex digest. Pass an empty
// sha to skip writing the manifest; package-root discovery then refuses the
// payload under the fail-closed Phase 1 contract. Returns the
// absolute binary path and the absolute manifest path (or "" when sha is
// empty).
func seedPackageChrome(t *testing.T, root string, writeSHA bool) (binPath, shaPath string) {
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

// withPackageChromeRoot overrides packageChromeRoot for the duration of the
// test, restoring the previous value on cleanup. Tests that exercise the
// production candidate list replace osExecutable directly.
func withPackageChromeRoot(t *testing.T, root string) {
	t.Helper()
	prev := packageChromeRootForTest
	packageChromeRootForTest = root
	t.Cleanup(func() { packageChromeRootForTest = prev })
}

// --- packageChromeRoot ---

// TestPackageChromeRoot_RuntimeComputed proves the runtime-computed layout
// (M5: no ldflags, no per-package variant): packageChromeRootCandidates
// is always derived from os.Executable(), never a constant. The ".." walk
// lands at the binary's parent dir's parent — so the first candidate's
// grandparent equals filepath.Dir(os.Executable()). The
// withPackageChromeRoot seam is NOT used here (this verifies the real
// function's contract).
func TestPackageChromeRoot_RuntimeComputed(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	got := packageChromeRootCandidates()
	require.NotEmpty(t, got)
	// The first candidate must equal filepath.Join(filepath.Dir(exe), "..", "chromium").
	want := filepath.Join(filepath.Dir(exe), "..", "chromium")
	assert.Equal(t, want, got[0], "first packageChromeRootCandidate must be computed at runtime from os.Executable()")
	// Second candidate must be the FHS share/ layout.
	wantShare := filepath.Join(filepath.Dir(exe), "..", "share", "omnipus", "chromium")
	assert.Equal(t, wantShare, got[1], "second candidate must be the install.sh FHS share/ layout")
	// Third candidate must be the deb/rpm libexec/ layout.
	wantLibexec := filepath.Join(filepath.Dir(exe), "..", "libexec", "omnipus", "chromium")
	assert.Equal(t, wantLibexec, got[2], "third candidate must be the nfpms libexec/ layout")
}

// TestPackageChromeRoot_EmptySlot1Skipped_FindsSlot2 proves the SPEC-001
// multi-root probe skips an existing but empty first slot and selects the
// first candidate containing a valid, integrity-manifested Chrome payload.
func TestPackageChromeRoot_EmptySlot1Skipped_FindsSlot2(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Phase 1 deferral: packageChromeRoot returns empty on Windows")
	}
	base := t.TempDir()
	exe := filepath.Join(base, "bin", "omnipus")
	require.NoError(t, os.MkdirAll(filepath.Dir(exe), 0o755))
	require.NoError(t, os.WriteFile(exe, []byte("binary"), 0o755))
	slot1 := filepath.Join(base, "chromium")
	slot2 := filepath.Join(base, "share", "omnipus", "chromium")
	require.NoError(t, os.MkdirAll(slot1, 0o755))
	seedPackageChrome(t, slot2, true)

	previousExecutable := osExecutable
	osExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { osExecutable = previousExecutable })
	previousRoot := packageChromeRootForTest
	packageChromeRootForTest = ""
	t.Cleanup(func() { packageChromeRootForTest = previousRoot })

	gotRoot, gotStatus := packageChromeRootProbe()
	assert.Equal(t, slot2, gotRoot)
	assert.Equal(t, ProbeUsable, gotStatus)
}

// TestPackageChromeRoot_NoCandidatePresent_ReturnsEmpty proves the
// SPEC-001 "no candidate" fallback: when nothing in the candidate list
// exists, packageChromeRoot returns "" — the resolver treats that as
// "no package Chrome available" and falls through to the managed
// download path cleanly.
func TestPackageChromeRoot_NoCandidatePresent_ReturnsEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Phase 1 deferral: packageChromeRoot returns empty on Windows")
	}
	base := t.TempDir()
	exe := filepath.Join(base, "bin", "omnipus")
	require.NoError(t, os.MkdirAll(filepath.Dir(exe), 0o755))
	require.NoError(t, os.WriteFile(exe, []byte("binary"), 0o755))
	previousExecutable := osExecutable
	osExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { osExecutable = previousExecutable })
	previousRoot := packageChromeRootForTest
	packageChromeRootForTest = ""
	t.Cleanup(func() { packageChromeRootForTest = previousRoot })

	gotRoot, gotStatus := packageChromeRootProbe()
	assert.Empty(t, gotRoot)
	assert.Equal(t, ProbeNotFound, gotStatus)
}

// TestPackageChromeRoot_ExecutableErrorReturnsEmpty proves the defensive
// contract: if os.Executable() ever fails (a theoretical on a stripped
// binary on a weird FS), packageChromeRoot returns "" rather than
// panicking. We exercise this by replacing the osExecutable seam with a failing function.
func TestPackageChromeRoot_ExecutableErrorReturnsEmpty(t *testing.T) {
	previousExecutable := osExecutable
	osExecutable = func() (string, error) { return "", errors.New("simulated executable failure") }
	t.Cleanup(func() { osExecutable = previousExecutable })
	previousRoot := packageChromeRootForTest
	packageChromeRootForTest = ""
	t.Cleanup(func() { packageChromeRootForTest = previousRoot })

	gotRoot, gotStatus := packageChromeRootProbe()
	assert.Empty(t, gotRoot)
	assert.Equal(t, ProbeNotFound, gotStatus)
}

// --- findPackageChrome ---

// TestFindPackageChrome_RootMissing_ReturnsEmpty verifies the "no package
// root" early-out: any os.Stat failure on root returns ("", "") without
// panicking, so callers (resolve step 3, ClassifyVideoCapability) fall
// through to the managed-download / not-capable paths cleanly.
func TestFindPackageChrome_RootMissing_ReturnsEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")
	bin, sha := findPackageChrome(root)
	assert.Empty(t, bin)
	assert.Empty(t, sha)
}

// TestFindPackageChrome_RootEmpty_ReturnsEmpty verifies the contract that
// an empty root never produces a path — used by the resolve() step-3
// guard when packageChromeRoot() itself returns "" (os.Executable error).
func TestFindPackageChrome_RootEmpty_ReturnsEmpty(t *testing.T) {
	bin, sha := findPackageChrome("")
	assert.Empty(t, bin)
	assert.Empty(t, sha)
}

// TestFindPackageChrome_BinaryPresentSHAProvided is the happy-path probe:
// when root has a binary AND a chrome.sha256, findPackageChrome returns
// both. The helper does NOT verify the digest — that's the caller's job
// (chromeintegrity.VerifyChromeSHA256) — so the SHA is returned verbatim for inspection.
func TestFindPackageChrome_BinaryPresentSHAProvided(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exec-bit layout; full Chrome binary path on Windows differs")
	}
	root := t.TempDir()
	wantBin, wantSHA := seedPackageChrome(t, root, true)

	bin, sha := findPackageChrome(root)
	assert.Equal(t, wantBin, bin)
	assert.Equal(t, wantSHA, sha)
}

// TestFindPackageChrome_BinaryPresentSHAMissing_Refused is the SEC-ADR052-001
// fail-closed contract: when root has a binary but no chrome.sha256, the
// helper MUST refuse (return ("", "")) — missing integrity metadata at the
// package root is a pipeline failure or tampering, both of which are
// release blockers (the only legitimate causes). Degraded-but-accept is
// NOT acceptable here; the resolver falls through to the managed download
// path. (Contrast: findInstalledBuild at the managed installRoot stays
// permissive on a missing manifest, because the runtime-download path
// doesn't ship one and pre-Phase-1 installs predate the manifest
// entirely — refusing them would be a back-compat regression.)
func TestFindPackageChrome_BinaryPresentSHAMissing_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix exec-bit layout")
	}
	root := t.TempDir()
	seedPackageChrome(t, root, false) // no chrome.sha256

	bin, sha := findPackageChrome(root)
	assert.Empty(
		t,
		bin,
		"SEC-ADR052-001: missing chrome.sha256 at the package root must cause findPackageChrome to refuse the binary",
	)
	assert.Empty(t, sha)
}

// TestFindPackageChrome_NotExecutable_ReturnsEmpty proves the POSIX
// exec-bit guard: a non-executable file at the expected layout is
// rejected. Skipped on Windows where FileMode carries no Unix exec bits.
func TestFindPackageChrome_NotExecutable_ReturnsEmpty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod exec-bit is posix-only; Go FileMode carries no Unix exec bits on Windows")
	}
	root := t.TempDir()
	binRel := fullChromeBinaryRelPath()
	bin := filepath.Join(root, binRel)
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o644))

	gotBin, gotSHA := findPackageChrome(root)
	assert.Empty(t, gotBin)
	assert.Empty(t, gotSHA)
}

// --- resolve: step 3 wiring ---

// TestResolve_Step3_UsesPackageChromeWhenPATHMisses is the core Phase 1
// behavior: every $PATH candidate broken AND a package Chrome present —
// resolution returns the package Chrome without ever probing for a managed
// download (no manifest server configured). This is "the floor on disk,
// not a fetch" — the ADR-052 D2 claim.
func TestResolve_Step3_UsesPackageChromeWhenPATHMisses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		writeExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	pkgRoot := t.TempDir()
	pkgBin, _ := seedPackageChrome(t, pkgRoot, true)
	withPackageChromeRoot(t, pkgRoot)

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(
		t,
		pkgBin,
		got,
		"step 3 must return the package Chrome when $PATH misses and a valid package Chrome exists",
	)
}

// TestResolve_Step3_PreferPackagedOutranksPATH is the M1 toggle test:
// with cfg.PreferPackaged=true AND cfg.TrustPathChrome=true (operator has
// opted in to trusting $PATH AND wants the package Chrome to override it
// for reproducibility), the package Chrome wins. This is the only
// override combination where a verified package Chrome beats a
// deliberately-allowed $PATH Chrome.
func TestResolve_Step3_PreferPackagedOutranksPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}

	// Working PATH chrome — this is what $PATH would resolve to when
	// TrustPathChrome=true.
	pathDir := t.TempDir()
	pathBin := filepath.Join(pathDir, "chromium")
	writeExecutable(t, pathBin, "#!/bin/sh\necho 'Chromium 131.0.6778.108'\nexit 0\n")
	t.Setenv("PATH", pathDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	pkgRoot := t.TempDir()
	pkgBin, _ := seedPackageChrome(t, pkgRoot, true)
	withPackageChromeRoot(t, pkgRoot)

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.PreferPackaged = true
	cfg.TrustPathChrome = true // must opt in to even consider $PATH
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(
		t,
		pkgBin,
		got,
		"PreferPackaged=true (with TrustPathChrome=true) must let the package Chrome outrank $PATH",
	)
}

// TestResolve_Step3_TrustPathChromeFalse_DiscardsPATH proves the
// SEC-ADR052-002 security-hardened default: with cfg.TrustPathChrome=false
// (the default), a working $PATH Chrome is RECORDED at WARN-BROWSER-007
// and DISCARDED — resolution falls through to the verified package Chrome.
// Operators who actually want a custom Chrome MUST opt in by setting
// tools.browser.trust_path_chrome=true (or use cfg.ExecPath, which always
// wins).
func TestResolve_Step3_TrustPathChromeFalse_DiscardsPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}

	pathDir := t.TempDir()
	pathBin := filepath.Join(pathDir, "chromium")
	writeExecutable(t, pathBin, "#!/bin/sh\necho 'Chromium 131.0.6778.108'\nexit 0\n")
	t.Setenv("PATH", pathDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	pkgRoot := t.TempDir()
	pkgBin, _ := seedPackageChrome(t, pkgRoot, true) // valid package chrome available
	withPackageChromeRoot(t, pkgRoot)

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.TrustPathChrome = false // explicit, matches the security-hardened default
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, pkgBin, got,
		"SEC-ADR052-002: TrustPathChrome=false must discard $PATH and use the verified package Chrome")
	assert.NotEqual(t, pathBin, got,
		"SEC-ADR052-002: $PATH Chrome must NOT be returned without TrustPathChrome=true")
}

// TestResolve_Step3_TrustPathChromeTrue_AllowsPATH proves the opt-in
// path: with cfg.TrustPathChrome=true, a working $PATH Chrome wins above
// the package Chrome. This is the legacy M1 "operator autonomy" path,
// explicitly opted into.
func TestResolve_Step3_TrustPathChromeTrue_AllowsPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}

	pathDir := t.TempDir()
	pathBin := filepath.Join(pathDir, "chromium")
	writeExecutable(t, pathBin, "#!/bin/sh\necho 'Chromium 131.0.6778.108'\nexit 0\n")
	t.Setenv("PATH", pathDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	pkgRoot := t.TempDir()
	seedPackageChrome(t, pkgRoot, true) // package chrome present but PATH is allowed
	withPackageChromeRoot(t, pkgRoot)

	cfg := newExecPathTestConfig(t, t.TempDir())
	cfg.TrustPathChrome = true // explicit opt-in
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, pathBin, got,
		"TrustPathChrome=true must allow $PATH Chrome to win above the package Chrome")
}

// TestResolve_Step3_SHAMismatchFallsThroughToManaged proves the M2
// hard-fail-in-the-safe-direction behavior: package chrome present with a
// chrome.sha256 whose digest disagrees with the binary's actual SHA-256 —
// step 3 logs WARN and falls through to step 4 (managed download). The
// fixture here has nothing on $PATH and no pre-installed managed binary,
// so step 4 will fail in this test environment too; the test asserts the
// PACKAGE binary was NOT returned (its mismatch rejected it).
func TestResolve_Step3_SHAMismatchFallsThroughToManaged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		writeExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	pkgRoot := t.TempDir()
	pkgBin, _ := seedPackageChrome(t, pkgRoot, true)
	// Overwrite chrome.sha256 with a wrong digest.
	require.NoError(t, os.WriteFile(filepath.Join(pkgRoot, "chrome.sha256"),
		[]byte("deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef\n"), 0o644))
	withPackageChromeRoot(t, pkgRoot)

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	// step 4 will fail (no manifest server, no pre-installed binary) — but
	// the relevant assertion is that the returned value is NOT pkgBin (the
	// mismatched binary was rejected).
	if err == nil {
		assert.NotEqual(t, pkgBin, got, "package chrome with sha256 mismatch must NOT be returned")
	} else {
		// Any error is acceptable here — what's NOT acceptable is pkgBin
		// being returned. Test passes by definition.
		assert.NotContains(t, err.Error(), pkgBin,
			"resolution error must not surface the rejected mismatched binary")
	}
}

// TestResolve_Step3_SHAMissingFallsThrough proves the SEC-ADR052-001
// fail-closed contract on the resolve path: a package Chrome without a
// sibling chrome.sha256 is REFUSED at step 3 (not an unverified acceptance).
// Resolution falls through to step 4 (managed download). In this test
// environment step 4 will also fail (no manifest server, no pre-installed
// binary) — the relevant assertion is that pkgBin is NOT the returned
// path.
func TestResolve_Step3_SHAMissingFallsThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix shell-script test double")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}

	binDir := t.TempDir()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		writeExecutable(t, filepath.Join(binDir, name), "#!/bin/sh\nexit 1\n")
	}
	t.Setenv("PATH", binDir)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "")

	pkgRoot := t.TempDir()
	pkgBin, _ := seedPackageChrome(t, pkgRoot, false) // no chrome.sha256
	withPackageChromeRoot(t, pkgRoot)

	cfg := newExecPathTestConfig(t, t.TempDir())
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	if err == nil {
		assert.NotEqual(t, pkgBin, got,
			"SEC-ADR052-001: package Chrome without chrome.sha256 must NOT be returned (fail closed)")
	} else {
		assert.NotContains(t, err.Error(), pkgBin,
			"resolution error must not surface the refused package binary")
	}
}

// TestResolve_Step3_PackageRootMissingFallsThroughToManaged proves the
// "no package" case: a host with only $PATH Chrome (or nothing at all) and
// no package root — resolution falls through cleanly to step 4 (managed
// download) without any error from step 3. Demonstrates the package step
// is purely additive: removing it never breaks existing behavior.
func TestResolve_Step3_PackageRootMissingFallsThroughToManaged(t *testing.T) {
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

	withPackageChromeRoot(t, filepath.Join(t.TempDir(), "does-not-exist"))

	cfg := newExecPathTestConfig(t, t.TempDir())
	binPath := seedManagedBinary(t, installRootFor(cfg), platform)
	m := &BrowserManager{cfg: cfg}

	got, err := m.resolveExecPath(context.Background())
	require.NoError(t, err)
	assert.Equal(t, binPath, got, "missing package root must NOT interfere with the managed-install path")
}

// --- chromeintegrity.VerifyChromeSHA256 direct coverage ---

// TestVerifyChromeSHA256_AcceptsCorrectDigest proves the happy path:
// when the binary's actual SHA-256 matches the manifest, no error is
// returned. The seeded fixture writes the correct digest by construction;
// this test confirms the shared verifier reads + hashes + compares correctly.
func TestVerifyChromeSHA256_AcceptsCorrectDigest(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)

	assert.NoError(t, chromeintegrity.VerifyChromeSHA256(binPath, shaPath))
}

// TestVerifyChromeSHA256_RejectsMismatch proves the hard-fail direction:
// when the manifest declares a digest that disagrees with the binary's
// actual SHA-256, chromeintegrity.VerifyChromeSHA256 returns a descriptive error mentioning
// the mismatch.
func TestVerifyChromeSHA256_RejectsMismatch(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)
	// Overwrite with a definitely-wrong digest.
	require.NoError(t, os.WriteFile(shaPath,
		[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))

	err := chromeintegrity.VerifyChromeSHA256(binPath, shaPath)
	require.Error(t, err)
	// The error must be specific enough to triage — expected/actual prefix.
	assert.Contains(t, err.Error(), "sha256 mismatch")
	assert.True(t, strings.Contains(err.Error(), "0000") || strings.Contains(err.Error(), "manifest"),
		"error must reference the manifest or its declared digest, got %q", err.Error())
}

// TestVerifyChromeSHA256_EmptySHA_Refuses proves the SEC-ADR052-001
// fail-closed contract: an empty shaPath is the same as a missing manifest,
// and chromeintegrity.VerifyChromeSHA256 returns chromeintegrity.ErrSHA256ManifestMissing (not nil). The
// caller (resolve step 3) uses errors.Is to detect this sentinel and fall
// through to the managed download path. findInstalledBuild (the managed
// installRoot path) explicitly accepts this sentinel because the runtime-
// download path doesn't ship a manifest and pre-Phase-1 installs predate
// the contract.
func TestVerifyChromeSHA256_EmptySHA_Refuses(t *testing.T) {
	root := t.TempDir()
	binPath, _ := seedPackageChrome(t, root, false)

	err := chromeintegrity.VerifyChromeSHA256(binPath, "")
	require.Error(
		t,
		err,
		"SEC-ADR052-001: empty shaPath must surface chromeintegrity.ErrSHA256ManifestMissing, not be a silent no-op",
	)
	assert.ErrorIs(t, err, chromeintegrity.ErrSHA256ManifestMissing)
}

// TestVerifyChromeSHA256_TolerantOfSHA256Prefix proves the format
// tolerance: a manifest with a "sha256:" prefix is accepted (some
// goreleaser emitters prefix the digest). The "<digest>  <filename>"
// sha256sum(1) format is also accepted.
func TestVerifyChromeSHA256_TolerantOfSHA256Prefix(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)
	contents, err := os.ReadFile(shaPath)
	require.NoError(t, err)
	digest := strings.TrimSpace(string(contents))

	// Write the manifest in three tolerated shapes.
	for _, shape := range []string{
		"sha256:" + digest + "\n",
		"SHA-256:" + digest + "\n",
		digest + "  chrome\n", // sha256sum format
	} {
		require.NoError(t, os.WriteFile(shaPath, []byte(shape), 0o644))
		assert.NoError(t, chromeintegrity.VerifyChromeSHA256(binPath, shaPath),
			"shape %q must be accepted", shape)
	}
}

// TestVerifyChromeSHA256_RejectsNonHexDigest proves the malformed-manifest
// guard: a digest string containing non-hex characters returns an explicit
// error rather than silently accepting or panicking. The error reports
// whichever invariant the parser hit first — length OR character class —
// both are clear-enough to triage from a log line.
func TestVerifyChromeSHA256_RejectsNonHexDigest(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)

	require.NoError(t, os.WriteFile(shaPath, []byte("not-a-hex-digest-at-all\n"), 0o644))
	err := chromeintegrity.VerifyChromeSHA256(binPath, shaPath)
	require.Error(t, err)
	// Length check fires first (16 chars); content-class check fires for
	// 64-char non-hex. Both produce a parse error that names "sha256 manifest"
	// — that's the assertion target (proves we reached the parser, didn't
	// silently accept).
	assert.Contains(t, err.Error(), "sha256 manifest")
}

// TestVerifyChromeSHA256_RejectsNonHexAtLength64 specifically exercises
// the 64-char non-hex content path (the case where length matches but
// content is wrong) — this is what trips the isLowerHex guard.
func TestVerifyChromeSHA256_RejectsNonHexAtLength64(t *testing.T) {
	root := t.TempDir()
	binPath, shaPath := seedPackageChrome(t, root, true)

	// 64 underscores — right length, wrong content (no hex digits at all).
	require.NoError(t, os.WriteFile(shaPath, []byte(strings.Repeat("_", 64)+"\n"), 0o644))
	err := chromeintegrity.VerifyChromeSHA256(binPath, shaPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lowercase hex")
}

// --- SEC-ADR052-004 parser hardening (table-driven) ---

// TestParseSHA256Manifest_Table exercises every accepted and rejected
// shape per SEC-ADR052-004's grammar. The accepted shapes round-trip
// through chromeintegrity.VerifyChromeSHA256 (when matched against a real binary); the
// rejected shapes fail with a descriptive error rather than a silent
// accept or panic.
func TestParseSHA256Manifest_Table(t *testing.T) {
	const validHex = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	type tc struct {
		name       string
		input      []byte
		wantDigest string
		wantErr    bool
		errSubstr  string
	}
	cases := []tc{
		{
			name:       "plain 64-char lowercase hex with trailing newline",
			input:      []byte(validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "sha256sum two-field format <hex>  chrome",
			input:      []byte(validHex + "  chrome\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading sha256: prefix",
			input:      []byte("sha256:" + validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading SHA-256: prefix",
			input:      []byte("SHA-256:" + validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading comment line then digest",
			input:      []byte("# SHA256\n" + validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading UTF-8 BOM",
			input:      append([]byte{0xEF, 0xBB, 0xBF}, []byte(validHex+"\n")...),
			wantDigest: validHex,
		},
		{
			name:       "CRLF line endings",
			input:      []byte(validHex + "\r\n"),
			wantDigest: validHex,
		},
		{
			name:      "uppercase hex is REJECTED (toolchain mismatch, SEC-004)",
			input:     []byte("ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789\n"),
			wantErr:   true,
			errSubstr: "lowercase hex",
		},
		{
			name:      "wrong digest length is REJECTED",
			input:     []byte("abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234567\n"), // 63 chars
			wantErr:   true,
			errSubstr: "digest length",
		},
		{
			name:      "64-char non-hex content is REJECTED (length matches, content wrong)",
			input:     []byte(strings.Repeat("_", 64) + "\n"),
			wantErr:   true,
			errSubstr: "lowercase hex",
		},
		{
			name:      "empty manifest is REJECTED",
			input:     []byte("\n"),
			wantErr:   true,
			errSubstr: "no SHA-256",
		},
		{
			name:      "non-hex garbage line is REJECTED",
			input:     []byte("not-a-hex-digest\n"),
			wantErr:   true,
			errSubstr: "digest length",
		},
		{
			name:      "two distinct digests is REJECTED",
			input:     []byte(validHex + "\n" + validHex + "\n"),
			wantErr:   true,
			errSubstr: "multiple digests",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := chromeintegrity.ParseChromeSHA256Manifest(c.input)
			if c.wantErr {
				require.Error(t, err)
				if c.errSubstr != "" {
					assert.Contains(t, err.Error(), c.errSubstr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantDigest, got)
		})
	}
}

// --- SEC-ADR052-005 symlink + world-writable install root ---

// TestFindPackageChrome_SymlinkedBinary_Refused proves that a symlinked
// binary at the expected layout is rejected (refuse-the-leaf defense).
// Uses os.Symlink to plant the symlink, then asserts findPackageChrome
// returns ("", "").
func TestFindPackageChrome_SymlinkedBinary_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privileges on Windows; POSIX-only defense here")
	}
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	// Plant a real chrome.sha256 so the manifest gate isn't what fails.
	require.NoError(t, os.WriteFile(filepath.Join(root, "chrome.sha256"),
		[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))
	// Symlink the expected binary path to /bin/true (a real executable on
	// every linux). findPackageChrome's Lstat gate must reject this.
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(fullChromeBinaryRelPath())), 0o755))
	require.NoError(t, os.Symlink("/bin/true", filepath.Join(root, fullChromeBinaryRelPath())))

	bin, sha := findPackageChrome(root)
	assert.Empty(t, bin, "SEC-ADR052-005: symlinked binary must be refused")
	assert.Empty(t, sha)
}

// TestFindPackageChrome_SymlinkedManifest_Refused proves the symmetric
// case: the binary is a real file but chrome.sha256 is a symlink —
// findPackageChrome refuses (because the manifest is the integrity anchor
// and a symlink at its leaf is exactly the "manifest elsewhere on disk"
// attack the security review flagged).
func TestFindPackageChrome_SymlinkedManifest_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privileges on Windows; POSIX-only defense here")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	// Plant a real binary at the expected layout.
	require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.Dir(fullChromeBinaryRelPath())), 0o755))
	binPath := filepath.Join(root, fullChromeBinaryRelPath())
	require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	// Symlink chrome.sha256 to a file elsewhere on disk.
	otherFile := filepath.Join(t.TempDir(), "decoy-sha256")
	require.NoError(t, os.WriteFile(otherFile, []byte("anything\n"), 0o644))
	require.NoError(t, os.Symlink(otherFile, filepath.Join(root, "chrome.sha256")))

	bin, sha := findPackageChrome(root)
	assert.Empty(t, bin, "SEC-ADR052-005: symlinked chrome.sha256 must be refused")
	assert.Empty(t, sha)
}

// TestFindPackageChrome_WorldWritableRoot_Refused proves the
// SEC-ADR052-005/006 install-root mode check: a 0777 install root lets
// any local user substitute chrome / chrome.sha256 between resolve and
// launch. findPackageChrome must refuse and return ("", "") so the
// resolver falls through to managed download.
func TestFindPackageChrome_WorldWritableRoot_Refused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0777 is posix-only; Go FileMode carries no Unix mode bits on Windows")
	}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(root, 0o755))
	// Plant a real binary + manifest so the only gate that fails is the
	// world-writable root check.
	seedPackageChrome(t, root, true)
	// Force the install root to be world-writable.
	require.NoError(t, os.Chmod(root, 0o777))

	bin, sha := findPackageChrome(root)
	assert.Empty(t, bin, "SEC-ADR052-005/006: world-writable install root must be refused")
	assert.Empty(t, sha)
}
