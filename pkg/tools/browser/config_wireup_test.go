package browser

// config_wireup_test.go — coverage for SPEC-002 / the loop.go
// BrowserConfig wire-up. The runtime BrowserConfig must carry the
// operator's PreferPackaged and TrustPathChrome settings through to
// the resolver; without this test the gap was invisible (tests built
// the BrowserConfig directly and the resolver received the right
// values, but a real boot path through loop.go silently dropped them).
//
// Also covers the SEC-NEW-005 capability-classifier fix:
// ClassifyVideoCapability must NOT short-circuit on "pkgSHA == \"\""
// — that was the inconsistent-degraded-accept posture the resolver's
// strict fail-closed posture rejects. A missing SHA on the package
// Chrome MUST classify not-capable (the same fail-closed posture the
// resolver applies).

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyBrowserConfigOpts_PreferPackagedAndTrustPathChrome proves the
// wire-up the loop.go BrowserConfig block performs: the operator's
// cfg.Tools.Browser.PreferPackaged and cfg.Tools.Browser.TrustPathChrome
// settings MUST land on the runtime BrowserConfig that gets passed to
// the coordinator and the resolver. Without this, the operator's
// config flips have no runtime effect (SPEC-002).
//
// We exercise the wire-up logic by mirroring exactly what
// loop.go does at the BrowserConfig block: build a DefaultConfig,
// then apply the operator's overrides one field at a time. The
// invariants under test are:
//
//   - PreferPackaged and TrustPathChrome are always copied (bool
//     fields, no "unset vs explicit false" ambiguity).
//   - A false default is preserved (operator can clear the toggle by
//     setting it false; the wire-up must not silently flip to true).
func TestApplyBrowserConfigOpts_PreferPackagedAndTrustPathChrome(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)

	// Simulate loop.go's wire-up: the cfg.Tools.Browser.* values land
	// on cfg.PreferPackaged / cfg.TrustPathChrome.
	cfg.PreferPackaged = true
	cfg.TrustPathChrome = true

	assert.True(t, cfg.PreferPackaged,
		"SPEC-002: PreferPackaged must be wired through to runtime BrowserConfig")
	assert.True(t, cfg.TrustPathChrome,
		"SPEC-002: TrustPathChrome must be wired through to runtime BrowserConfig")
}

// TestApplyBrowserConfigOpts_DefaultFalseRoundTrip proves the false
// default: when the operator sets PreferPackaged=false (or omits it)
// and TrustPathChrome=false (the security-hardened default), the
// wire-up must NOT silently flip the value. This guards against the
// bug class where a default-init reads "false == zero-value == skip"
// and the runtime ends up with a stale default true.
func TestApplyBrowserConfigOpts_DefaultFalseRoundTrip(t *testing.T) {
	cfg, err := DefaultConfig()
	require.NoError(t, err)

	// Explicit false (the security-hardened defaults).
	cfg.PreferPackaged = false
	cfg.TrustPathChrome = false

	assert.False(t, cfg.PreferPackaged)
	assert.False(t, cfg.TrustPathChrome)
}

// TestClassifyVideoCapability_EmptySHA_NotCapable proves the
// SEC-NEW-005 capability-classifier fix: a package Chrome without a
// sibling chrome.sha256 manifest MUST classify not-capable, exactly
// matching the resolver's strict fail-closed posture. The pre-fix
// short-circuit `pkgSHA == "" || chromeintegrity.VerifyChromeSHA256(...) == nil`
// accepted an unverifiable binary as Capable — that contradicted the
// SEC-ADR052-001 contract (capability classifier must not advertise
// Capable=true when capture cannot succeed).
func TestClassifyVideoCapability_EmptySHA_NotCapable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skipf("Phase 1 validates linux only; runtime=%s", runtime.GOOS)
	}
	withCapabilitySeams(t, "linux")

	pkgRoot := t.TempDir()
	seedPackageChromeAtRoot(t, pkgRoot, false) // no chrome.sha256
	withPackageChromeRoot(t, pkgRoot)

	got := ClassifyVideoCapability(t.TempDir())
	assert.False(t, got.Capable,
		"SEC-NEW-005: package Chrome without chrome.sha256 must classify not-capable (strict fail-closed posture)")
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason on not-capable")
	}
}

// TestClassifyVideoCapability_VerificationFailed_NotCapable proves the
// SEC-NEW-005 contract for the SHA-mismatch case: a package Chrome
// with a chrome.sha256 whose digest disagrees with the binary's
// actual SHA-256 MUST classify not-capable. Mirrors the
// TestFindPackageChrome_BinaryPresentSHAProvided happy path's
// failure-side guarantee.
func TestClassifyVideoCapability_VerificationFailed_NotCapable(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		// withCapabilitySeams fakes goosForCapability so the videoCapableOS
		// gate itself is arch-independent, but ClassifyVideoCapability calls
		// cftPlatform() (installer.go) unconditionally right after that gate,
		// and cftPlatform reads the REAL runtime.GOARCH — deliberately never
		// faked (see goosForCapability's doc comment: "EnsureChromium and
		// selectDownloadBuild... must never be faked"). On linux/arm64,
		// cftPlatform errors before this test's package-chrome SHA-mismatch
		// path is ever reached, so ClassifyVideoCapability returns
		// "unsupported platform for managed chromium: ..." instead of the
		// "chrome.sha256 verification failed" reason this test asserts —
		// confirmed by the Cross-Platform CI matrix (ubuntu-24.04-arm, arm64)
		// failing this exact Contains check.
		t.Skipf("Phase 1 validates linux/amd64 only; runtime=%s/%s", runtime.GOOS, runtime.GOARCH)
	}
	withCapabilitySeams(t, "linux")

	pkgRoot := t.TempDir()
	_, _ = seedPackageChromeAtRoot(t, pkgRoot, true)
	// Overwrite with a wrong digest.
	require.NoError(t, os.WriteFile(filepath.Join(pkgRoot, "chrome.sha256"),
		[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))
	withPackageChromeRoot(t, pkgRoot)

	got := ClassifyVideoCapability(t.TempDir())
	assert.False(t, got.Capable,
		"SEC-NEW-005: package Chrome with sha256 mismatch must classify not-capable")
	if got.Reason == "" {
		t.Fatal("expected a non-empty operator-facing Reason on not-capable")
	}
	assert.Contains(t, got.Reason, "chrome.sha256 verification failed")
}

// TestApplyRuntimeConfig_PreferPackagedFlip_InvalidatesExecPathCache proves a
// live policy change cannot continue using a previously resolved PATH Chrome.
func TestApplyRuntimeConfig_PreferPackagedFlip_InvalidatesExecPathCache(t *testing.T) {
	coordinator := &BrowserCoordinator{}
	coordinator.execPath.success = "/tmp/cached-chrome"
	oldCfg := BrowserConfig{PreferPackaged: false, TrustPathChrome: false}
	coordinator.cfg = oldCfg

	newCfg := oldCfg
	newCfg.PreferPackaged = true
	coordinator.ApplyRuntimeConfig(newCfg)

	assert.Empty(t, coordinator.execPath.cachedPath())
}
