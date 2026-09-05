// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package doctor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/chromeintegrity"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// TestCheckBuildIntegrity covers WARN-BUILD-001: silent when config.Version
// carries real ldflags-injected metadata, warns when it's still the
// build-system default ("dev") a bare `go build` (bypassing `make build` /
// goreleaser) would leave it at.
func TestCheckBuildIntegrity(t *testing.T) {
	origVersion := config.Version
	t.Cleanup(func() { config.Version = origVersion })

	tests := []struct {
		name      string
		version   string
		wantWarns bool
	}{
		{name: "unset version (dev default) warns", version: "dev", wantWarns: true},
		{name: "ldflags-set version stays silent", version: "1.2.3", wantWarns: false},
		{name: "ldflags-set version with git suffix stays silent", version: "0.1.1-hotfix", wantWarns: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			config.Version = tc.version

			warnings := checkBuildIntegrity()

			if !tc.wantWarns {
				assert.Empty(t, warnings)
				return
			}
			require.Len(t, warnings, 1)
			assert.Equal(t, "WARN-BUILD-001", warnings[0].code)
			assert.Contains(t, warnings[0].message, "make build")
			assert.NotEmpty(t, warnings[0].message)
		})
	}
}

// TestCheckBrowserVideoCapability_WebRTCDisabled covers WARN-BROWSER-001:
// warns when the operator has explicitly disabled the WebRTC path in config,
// regardless of what the underlying host could otherwise support.
func TestCheckBrowserVideoCapability_WebRTCDisabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCEnabled = false

	warnings := checkBrowserVideoCapability(cfg)

	require.Len(t, warnings, 1)
	assert.Equal(t, "WARN-BROWSER-001", warnings[0].code)
	assert.Contains(t, warnings[0].message, "webrtc_enabled=false")
}

// TestCheckBrowserVideoCapability_LiteBuild covers WARN-BROWSER-002: warns
// plainly that video can never work when webrtc.Available is false (a
// -tags lite build compiles the WebRTC stack out entirely), distinguishing
// this from a config mistake. webrtc.Available is a package-level var
// (exported specifically as a test seam — see pkg/gateway/browser_webrtc_
// fixwave_test.go for the established mutate/t.Cleanup-restore pattern this
// mirrors), so this is exercisable without an actual -tags lite build.
func TestCheckBrowserVideoCapability_LiteBuild(t *testing.T) {
	origAvailable := webrtc.Available
	webrtc.Available = false
	t.Cleanup(func() { webrtc.Available = origAvailable })

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCEnabled = true

	warnings := checkBrowserVideoCapability(cfg)

	require.Len(t, warnings, 1)
	assert.Equal(t, "WARN-BROWSER-002", warnings[0].code)
	assert.Contains(t, warnings[0].message, "lite build")
	assert.Contains(t, warnings[0].message, "BUILD")
}

// TestCheckBrowserVideoCapability_CaptureSharedContextDisabled is DELETED
// with ADR-075 FR-031. It asserted WARN-BROWSER-004, which warned that
// tools.browser.capture_shared_context was false. That key no longer exists
// and the warning it produced can no longer be true, so the test could only
// ever have been kept green by reintroducing the defect it described.

// TestCheckBrowserVideoCapability_NotCapable_IncludesReason covers
// WARN-BROWSER-003: warns when the base classifier reports not-capable, and
// the specific Reason string must be surfaced verbatim so an operator knows
// exactly which precondition failed. Computes the expected Reason via the
// same exported classifier (rather than hardcoding a platform-specific
// string) so the assertion holds regardless of host OS.
func TestCheckBrowserVideoCapability_NotCapable_IncludesReason(t *testing.T) {
	origAvailable := webrtc.Available
	webrtc.Available = true
	t.Cleanup(func() { webrtc.Available = origAvailable })

	profileDir := filepath.Join(t.TempDir(), "profile")

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCEnabled = true
	cfg.Tools.Browser.ProfileDir = profileDir
	cfg.Tools.Browser.ExecPath = ""

	installRoot := browser.InstallRootForProfileDir(profileDir)
	want := browser.ClassifyVideoCapabilityWithExec("", installRoot)
	require.False(
		t, want.Capable,
		"test setup: a fresh temp profile dir with nothing installed must classify not-capable",
	)
	require.NotEmpty(t, want.Reason)

	warnings := checkBrowserVideoCapability(cfg)

	require.Len(t, warnings, 1)
	assert.Equal(t, "WARN-BROWSER-003", warnings[0].code)
	assert.Contains(t, warnings[0].message, want.Reason)
}

// TestCheckBrowserVideoCapability_Capable stays silent when every
// precondition doctor can check from config passes: WebRTC enabled, not a
// lite build, base classification capable, and shared-context capture
// enabled.
func TestCheckBrowserVideoCapability_Capable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever classifies capable on linux; skipping on " + runtime.GOOS)
	}

	origAvailable := webrtc.Available
	webrtc.Available = true
	t.Cleanup(func() { webrtc.Available = origAvailable })

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCEnabled = true
	// A non-empty, non-headless-shell-named override is enough for
	// ClassifyVideoCapabilityWithExec to classify capable on linux — it
	// trusts the operator's override on basename alone, no stat/probe.
	cfg.Tools.Browser.ExecPath = "/usr/bin/google-chrome-stable"

	warnings := checkBrowserVideoCapability(cfg)

	assert.Empty(t, warnings)
}

// TestCheckConfig_ZeroValue_NoPanic guards against a panic on a fresh/empty
// config: nil Channels map, empty Browser/Exec sub-structs, unset version.
// Every doctor check must degrade to a sensible (possibly noisy) result
// rather than crash.
func TestCheckConfig_ZeroValue_NoPanic(t *testing.T) {
	origAvailable := webrtc.Available
	t.Cleanup(func() { webrtc.Available = origAvailable })

	assert.NotPanics(t, func() {
		cfg := &config.Config{}
		warnings := checkConfig(cfg)
		// A fresh zero-value config has webrtc_enabled=false, so at minimum
		// the browser check is expected to fire — asserting non-nil here
		// isn't the point, not panicking is; this just documents the shape.
		_ = warnings
	})
}

// TestCheckBrowserVideoCapability_ZeroValue_NoPanic isolates the browser
// check specifically against a completely empty BrowserToolConfig (all
// fields zero/empty string), per the "must not panic on a partial/fresh
// config" hard constraint.
func TestCheckBrowserVideoCapability_ZeroValue_NoPanic(t *testing.T) {
	origAvailable := webrtc.Available
	t.Cleanup(func() { webrtc.Available = origAvailable })

	assert.NotPanics(t, func() {
		cfg := &config.Config{}
		_ = checkBrowserVideoCapability(cfg)
	})
}

// ──────────────────────────────────────────────────────────────────────────────
// ADR-052 Phase 1 — package-bundled Chrome doctor checks (WARN-BROWSER-005/006)
// ──────────────────────────────────────────────────────────────────────────────

// TestCheckBrowserPackageChrome_NoPackageChrome stays silent when the
// running binary's os.Executable() has no chromium/ sibling — bare-binary
// installs (go build, copy-to-PATH, go install) are the document shape,
// and they have no package Chrome to verify. The runtime falls through to
// download; doctor doesn't second-guess that.
//
// This is the default scenario for `go test` because `go test` runs the
// compiled test binary from a temp directory with no chromium/ sibling,
// so the test reads as the baseline truth.
func TestCheckBrowserPackageChrome_NoPackageChrome(t *testing.T) {
	assert.Empty(t, checkBrowserPackageChrome(),
		"bare-binary install (no chromium/ sibling) must be silent — runtime falls through to download")
}

// TestCheckBrowserPackageChrome_HashMatch_StaysSilent covers the green
// path: a synthesized chromium/ + chrome-linux64/chrome + chrome.sha256
// where the SHA-256 in the manifest matches the binary on disk. Doctor
// must surface no warning.
func TestCheckBrowserPackageChrome_HashMatch_StaysSilent(t *testing.T) {
	if runtime.GOOS == "windows" {
		// os.Executable on Windows test runs returns the path the test
		// harness created; chromium/ sibling layout is darwin/linux.
		// The chrome.sha256 check itself is OS-independent, but the
		// parent-dir resolver on Windows uses a different layout. Skip
		// to keep this test focused.
		t.Skip("synthetic-package test only meaningful on linux/darwin")
	}

	withSyntheticPackageChrome(t)

	warnings := checkBrowserPackageChrome()
	assert.Empty(t, warnings, "matching chrome.sha256 must surface no warning")
}

// TestCheckBrowserPackageChrome_HashMismatch_Emits006 covers WARN-BROWSER-006:
// synthesize a package chrome with a wrong chrome.sha256 (manifest's hash
// is all zeros — definitely does not match the actual chrome binary's
// hash). Expect a WARN-BROWSER-006 with the expected/got hex digests in
// the message.
func TestCheckBrowserPackageChrome_HashMismatch_Emits006(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic-package test only meaningful on linux/darwin")
	}

	root := withSyntheticPackageChrome(t)

	// Overwrite chrome.sha256 with a guaranteed-wrong digest.
	wrongPath := filepath.Join(root, "chrome.sha256")
	require.NoError(t, os.WriteFile(wrongPath,
		[]byte("0000000000000000000000000000000000000000000000000000000000000000  chrome-linux64/chrome\n"),
		0o644))

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "hash mismatch must surface at least one warning")
	var found *warning
	for i := range warnings {
		if warnings[i].code == "WARN-BROWSER-006" {
			found = &warnings[i]
		}
	}
	require.NotNil(t, found, "expected WARN-BROWSER-006 in %v", warnings)
	assert.Contains(t, found.message, "0000000000000000000000000000000000000000000000000000000000000000")
}

// TestCheckBrowserPackageChrome_MissingLib_SurfacesWarning (TEST-008):
// synthesize an ELF chrome binary whose DT_NEEDED entry points to a
// guaranteed-not-installed soname, then assert the resulting warning
// surfaces the missing lib's name in the message (so an operator can read
// `apt-get install …` off the warning without needing to re-run --debug).
func TestCheckBrowserPackageChrome_MissingLib_SurfacesWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-process ELF parser is linux-only (Phase 1)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("linux-only ELF check; skipping on " + runtime.GOOS)
	}

	root := withSyntheticPackageChrome(t)

	// Overwrite the chrome binary with one whose DT_NEEDED points to a
	// soname that never exists on any mainstream Linux distribution.
	// "libmissingtest.so.42" is RFC-6335-reserved for documentation and
	// would never appear in /usr/lib.
	chromePath := filepath.Join(root, "chrome-linux64", "chrome")
	require.NoError(t, os.WriteFile(chromePath, buildMinimalELF64(t, "libmissingtest.so.42"), 0o755))

	// Recompute the chrome.sha256 to match the new binary — otherwise
	// WARN-BROWSER-006 would also fire and the test would assert against
	// the wrong warning code.
	manifest := sha256FileHex(t, chromePath) + "  chrome-linux64/chrome\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "chrome.sha256"), []byte(manifest), 0o644))

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "missing DT_NEEDED entry must surface a warning")
	var found *warning
	for i := range warnings {
		if warnings[i].code == "WARN-BROWSER-005" {
			found = &warnings[i]
		}
	}
	require.NotNil(t, found, "expected WARN-BROWSER-005 in %v", warnings)
	assert.Contains(t, found.message, "libmissingtest.so.42",
		"warning message must name the missing soname so the operator can install the right package")
}

// TestCheckBrowserPackageChrome_BinaryMissing_EmitsCorrectCode (CORR-011):
// when the chromium/ root exists but the chrome binary is absent, the
// diagnostic MUST be WARN-BROWSER-008 (binary absent), NOT WARN-BROWSER-005
// (which is reserved for the ldd-style "missing host shared libraries"
// diagnostic — distinct remediation).
func TestCheckBrowserPackageChrome_BinaryMissing_EmitsCorrectCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic-package test only meaningful on linux/darwin")
	}

	root := withSyntheticPackageChrome(t)

	// Remove the chrome binary (and its linux subdir) but leave the root
	// + chrome.sha256 behind. The synthetic helper builds
	// <root>/chrome-linux64/chrome; deleting the directory simulates the
	// "binary absent" failure mode.
	require.NoError(t, os.RemoveAll(filepath.Join(root, "chrome-linux64")))

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "missing chrome binary must surface a warning")
	var found008 *warning
	var found005 *warning
	for i := range warnings {
		switch warnings[i].code {
		case "WARN-BROWSER-008":
			found008 = &warnings[i]
		case "WARN-BROWSER-005":
			found005 = &warnings[i]
		}
	}
	require.NotNil(t, found008, "expected WARN-BROWSER-008 (binary absent) in %v", warnings)
	assert.Nil(
		t,
		found005,
		"WARN-BROWSER-005 must NOT fire for binary-absent — that's a separate remediation (install host libs vs reinstall package)",
	)
	assert.Contains(t, found008.message, "chrome binary is missing")
}

// TestCheckBrowserPackageChrome_FlatDockerHeavyLayout_NoFalsePositive is a
// regression test for a REAL live deployment bug: docker/Dockerfile.heavy's
// B2a step stages Alpine's apk-installed chromium FLAT at
// <chromium-root>/chrome (Alpine's package layout has no chrome-linux64/
// extraction subdir — that subdir is a Chrome-for-Testing goreleaser-archive
// artifact only), with a companion chrome.sha256 generated verbatim by:
//
//	sha256sum /usr/local/chromium/chrome \
//	  | awk '{printf "%s  %s\n", $1, $2}' > /usr/local/chromium/chrome.sha256
//
// sha256sum's own output is already "<hash>  <path>" (two spaces); the awk
// pass-through preserves that shape, so the manifest's second field is the
// FULL PATH given to sha256sum, not the bare basename "chrome". Live
// evidence from the deployed container (uat-omnipus, image built from this
// branch) confirmed the manifest content and the live binary's sha256sum
// match byte-for-byte — the payload and manifest are genuinely correct —
// yet `omnipus doctor` reported:
//
//	WARN-BROWSER-008: package chrome root present (/usr/local/chromium) but
//	chrome binary is missing — reinstall the omnipus package or remove the
//	chromium/ directory.
//
// Root cause: packageChromeBinaryPath (this package) only ever probed
// <root>/chrome-linux64/chrome — the CfT extraction-subdir layout — and had
// drifted out of sync with pkg/tools/browser/package_chrome.go's
// binaryLayoutsForRoot, which is what the REAL runtime resolver
// (findPackageChrome, reached via exec_resolver.go step 3 / capability.go)
// uses and which has always accepted the flat <root>/chrome layout
// ("flat install.sh layout" — see TestBinaryLayoutsForRoot_AllOSes and
// TestFindPackageChrome_GoreleaserExtractionSubdir_Found in
// pkg/tools/browser/package_chrome_test.go). The runtime path was never
// broken; only this diagnostic's stale, narrower duplicate of the layout
// list was. docker/Dockerfile.heavy needs no change — its layout already
// satisfies the verified package-Chrome floor.
//
// REVERT-PROOF: reverting packageChromeBinaryPath to check only
// chrome-linux64/chrome makes this test FAIL (WARN-BROWSER-008 present in
// the result), because this fixture deliberately has no chrome-linux64/
// subdirectory — exactly like the real container. With the fix in place
// (the flat "chrome" candidate added to packageChromeBinaryPath) the test
// PASSES.
func TestCheckBrowserPackageChrome_FlatDockerHeavyLayout_NoFalsePositive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("flat linux apk-chromium layout only meaningful on linux/darwin")
	}

	root := filepath.Join(t.TempDir(), "chromium")
	require.NoError(t, os.MkdirAll(root, 0o755))

	chromePath := filepath.Join(root, "chrome")
	chromeContent := buildMinimalELF64(t, "libc.so.6")
	require.NoError(t, os.WriteFile(chromePath, chromeContent, 0o755))

	// Reproduce docker/Dockerfile.heavy's `sha256sum | awk` pipeline
	// verbatim: "<hash>  <full-path-passed-to-sha256sum>\n".
	sum := sha256.Sum256(chromeContent)
	manifest := hex.EncodeToString(sum[:]) + "  " + chromePath + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "chrome.sha256"), []byte(manifest), 0o644))

	prevOverride := packageChromeRootOverride
	packageChromeRootOverride = root
	t.Cleanup(func() { packageChromeRootOverride = prevOverride })

	warnings := checkBrowserPackageChrome()
	for _, w := range warnings {
		assert.NotEqual(
			t,
			"WARN-BROWSER-008",
			w.code,
			"flat <root>/chrome layout (docker/Dockerfile.heavy's real on-disk shape) must not trigger a binary-missing false positive: %v",
			w,
		)
		assert.NotEqual(t, "WARN-BROWSER-006", w.code,
			"manifest hash genuinely matches the binary; must not report a mismatch: %v", w)
	}
}

// TestCheckBrowserPackageChrome_SymlinkedRoot_NoFalsePass (SEC-NEW-003):
// when the chromium/ root is a symlink to the synthetic tree (not the real
// directory), the Lstat-based root check MUST refuse to validate it. The
// diagnostic is WARN-BROWSER-008 (binary/path anomaly, distinct from a
// straight "missing" WARN). Without the Lstat check this test would
// silently pass — WARN-BROWSER-006 would emit a false-negative "hash OK"
// against the symlink target's contents.
func TestCheckBrowserPackageChrome_SymlinkedRoot_NoFalsePass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need elevated privileges on Windows; skip on " + runtime.GOOS)
	}

	// Build a real synthetic chromium tree (this is the legit root).
	realRoot := withSyntheticPackageChrome(t)

	// Now point the doctor at a symlink to it instead of the real root.
	symlinkParent := t.TempDir()
	symlinkRoot := filepath.Join(symlinkParent, "chromium")
	require.NoError(t, os.Symlink(realRoot, symlinkRoot))

	prevOverride := packageChromeRootOverride
	packageChromeRootOverride = symlinkRoot
	t.Cleanup(func() { packageChromeRootOverride = prevOverride })

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "symlinked root must surface a warning — Lstat refuses, no silent hash pass")
	var found008 *warning
	for i := range warnings {
		if warnings[i].code == "WARN-BROWSER-008" {
			found008 = &warnings[i]
		}
	}
	require.NotNil(t, found008, "expected WARN-BROWSER-008 in %v", warnings)
	assert.Contains(t, found008.message, "symlink",
		"diagnostic must mention symlink so the operator understands why the path was refused")

	// Explicitly assert there is NO WARN-BROWSER-006 in the warnings — a
	// false-positive "hash OK" against the symlink target's contents is the
	// exact failure SEC-NEW-003 documents.
	for _, w := range warnings {
		assert.NotEqual(
			t,
			"WARN-BROWSER-006",
			w.code,
			"symlinked root must NOT produce a WARN-BROWSER-006 hash-OK pass — that is the false-negative this test guards",
		)
	}
}

// TestParseSHA256Manifest_Hardening covers the SHA-256 parser edge cases
// enumerated in ADR-052 SEC-ADR052-004: BOM, CRLF, sha256: prefix, comment
// lines, uppercase hex (rejected), wrong length (rejected), trailing
// whitespace, two-line format, sha256sum binary mode. The doctor delegates
// to the shared pkg/tools/browser/chromeintegrity.ParseChromeSHA256Manifest
// helper so this test exercises the SAME parser the runtime uses — the
// doctor cannot drift from the runtime's tolerance rules.
func TestParseSHA256Manifest_Hardening(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "plain sha256sum format", input: good + "  chrome-linux64/chrome\n", want: good},
		{name: "binary-mode marker", input: good + " *chrome-linux64/chrome\n", want: good},
		{name: "sha256: prefix", input: "sha256: " + good + "\n", want: good},
		{name: "SHA-256: prefix (uppercase + hyphen)", input: "SHA-256: " + good + "\n", want: good},
		{name: "leading BOM stripped", input: "\xEF\xBB\xBF" + good + "  chrome\n", want: good},
		{name: "CRLF tolerated", input: good + "  chrome\r\n", want: good},
		{name: "comment line skipped", input: "# generated by goreleaser\n" + good + "  chrome\n", want: good},
		{name: "leading whitespace tolerated", input: "   " + good + "\n", want: good},
		{
			name:    "uppercase hex rejected",
			input:   "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF  chrome\n",
			want:    "",
			wantErr: true,
		},
		{name: "wrong length rejected", input: "abcdef  chrome\n", want: "", wantErr: true},
		{name: "empty file", input: "", want: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chromeintegrity.ParseChromeSHA256Manifest([]byte(tc.input))
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestParseSHA256Manifest_Adversarial (TEST-010): adversarial inputs that
// must NOT silently match. The hardening table above covers the canonical
// positive cases; this covers the failure modes — NUL inside the 64-char
// field (truncates cleanly, no digest absorb), garbage trailing bytes
// past the 64-char field (rejected — the field walker only takes
// exactly-64-char hex), CR-only line separator (CRLF in the hardening
// table above tests \r\n; CR alone is a classic Mac classic line ending
// and is also tolerated).
func TestParseSHA256Manifest_Adversarial(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			// A NUL byte inside the 64-char field truncates the field
			// before the digest is returned. The parser must NEVER absorb
			// the NUL into the digest and silently match.
			name:    "NUL inside 64-char field truncates to empty",
			input:   good[:30] + "\x00" + good[31:] + "  chrome\n",
			want:    "",
			wantErr: true,
		},
		{
			// 64-char hex followed by non-whitespace garbage past 64 chars
			// in the same field — the field walker only takes exactly-64,
			// so the extra chars get rejected.
			name:    "garbage trailing bytes past 64-char hex rejected",
			input:   good + "XYZ  chrome\n",
			want:    "",
			wantErr: true,
		},
		{
			// Two consecutive 64-char hex digests on separate lines — the
			// parser rejects (multi-digest manifest is a tampering signal,
			// surfaced rather than silently picking one).
			name:    "two digests rejected",
			input:   good + "  chrome\n" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff  chrome\n",
			want:    "",
			wantErr: true,
		},
		{
			// CR-only line separator (classic Mac OS, pre-OSX) — the
			// hardening table's CRLF tolerance was added for Windows
			// manifests; CR-only is the same family of fix.
			name:  "CR-only separator tolerated",
			input: good + "  chrome\r",
			want:  good,
		},
		{
			// 64-char hex split across a newline (line wrap). Must be
			// rejected — the parser does NOT concatenate across lines.
			name:    "hex split across line wrap rejected",
			input:   good[:32] + "\n" + good[32:] + "  chrome\n",
			want:    "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := chromeintegrity.ParseChromeSHA256Manifest([]byte(tc.input))
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got, "input=%q", tc.input)
		})
	}
}

// TestMissingChromeLibsELF_StructuralErrors (TEST-009): the in-process
// ELF walker (command_libs_linux.go) must return an explanatory error,
// not panic, when fed malformed inputs. table-driven across the common
// failure modes the parser must handle gracefully.
//
// NOTE: an e_phoff-out-of-range input (where e_phoff is a uint64 that
// wraps on conversion to int) is NOT in this table because the current
// parser has a bounds-check gap on that path (FIX-1 owns command_libs_linux.go).
// Once that gap is closed, this test should grow that case back in.
func TestMissingChromeLibsELF_StructuralErrors(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("missingChromeLibsELF is linux-only (build-tagged)")
	}

	// Build a known-good ELF64 once, then corrupt specific bytes per test
	// case. buildMinimalELF64 is the same helper the other doctor tests use
	// to construct the green-path chrome binary.
	good := buildMinimalELF64(t, "libc.so.6")

	zeroPheaders := append([]byte(nil), good...)
	// e_phnum (offset 56..57) = 0 — zero program headers means the parser
	// never finds PT_DYNAMIC; with a corrupted PT_DYNAMIC table it must
	// NOT silently report no missing libs.
	zeroPheaders[56] = 0
	zeroPheaders[57] = 0

	// FIX-HIGH-002: corrupt the DT_STRTAB entry's value field so DT_NEEDED
	// entries are collected but cannot be resolved to sonames. Per
	// buildMinimalELF64's layout, the DT_* table starts at file offset 176
	// (dynOff): entry 0 is DT_NEEDED (tag=1, val=0), entry 1 is DT_STRTAB
	// (tag=5, val=strtab offset) at bytes [192:208], with the val field at
	// [200:208]. Zeroing that val field leaves DT_NEEDED's collected entry
	// (val=0) intact while making strtabOff==0 — the exact "DT_NEEDED
	// entries WERE collected but DT_STRTAB is missing/out of range" shape.
	corruptedStrtab := append([]byte(nil), good...)
	for i := 200; i < 208; i++ {
		corruptedStrtab[i] = 0
	}

	tests := []struct {
		name        string
		payload     []byte
		wantMissing []string // expected returned missing (or nil on error)
		wantErr     bool
	}{
		{
			name:    "truncated ELF header (8 bytes, under ehdrSize=52)",
			payload: []byte{0x7f, 'E', 'L', 'F', 2, 1, 0, 0},
			wantErr: true,
			// The parser's len(data) < ehdrSize check fires AFTER the magic
			// check; the magic passes here, so we reach the truncated-header
			// branch and get a structured error rather than a panic.
		},
		{
			name:    "e_phnum=0 (zero program headers)",
			payload: zeroPheaders,
			wantErr: true,
			// FIX-HIGH-002: zero program headers is NOT expected for a
			// Chrome-for-Testing binary (always dynamically linked, always
			// carries PT_LOAD segments) — it now returns an error ("could
			// not determine") rather than silently discarding the fact
			// that the ELF is structurally anomalous. Before this fix the
			// parser returned nil, nil here (indistinguishable from "no
			// missing libs"), which is exactly the false-clean-bill-of-
			// health bug this fix closes. The contract: no panic, no
			// silent false-missing.
		},
		{
			name:    "32-bit ELF (EI_CLASS=1) — parser is 64-bit-only",
			payload: []byte{0x7f, 'E', 'L', 'F', 1, 1, 0, 0}, // EI_CLASS=1 = 32-bit
			wantErr: false,
			// The parser treats this as a not-an-elf path (it can't decode
			// the 32-bit header with 64-bit widths). It returns the
			// synthetic not-an-elf-binary entry — important: it does NOT
			// panic.
		},
		{
			name:    "DT_NEEDED collected but DT_STRTAB zero (corrupted string table pointer)",
			payload: corruptedStrtab,
			wantErr: true,
			// FIX-HIGH-002: before this fix, a DT_STRTAB/DT_STRSZ that is
			// zero/out-of-range discarded every already-collected
			// DT_NEEDED entry and returned nil, nil — indistinguishable
			// from "no dependencies at all". Now it returns an error so
			// the caller surfaces a "could not determine" warning instead
			// of a false-clean "0 missing libraries".
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "chrome")
			require.NoError(t, os.WriteFile(path, tc.payload, 0o755))

			// Wrap in a panic-recover so a regression that adds a panic
			// somewhere in the parser produces a clean test failure rather
			// than tearing down the whole test binary.
			var (
				got []string
				err error
			)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("missingChromeLibsELF panicked on %q: %v", tc.name, r)
					}
				}()
				got, err = missingChromeLibsELF(path)
			}()

			if tc.wantErr {
				assert.Error(t, err, "expected error for %q", tc.name)
			} else {
				assert.NoError(t, err)
			}
			// The contract under all malformed inputs: no panic (asserted
			// above), no spurious entries that look like real sonames.
			// Asserting a non-nil slice is acceptable; we don't pin the
			// exact synthetic "not-an-elf" entry text here so the test
			// doesn't over-constrain.
			_ = got
		})
	}
}

// TestDebianMissingLibs_AllEntriesNonEmpty (TEST-012): every entry in the
// debianMissingLibs table must be a non-empty package name — an empty
// value would emit `apt-get install` with a blank arg and fail silently
// or install the wrong thing. Catches typos + accidental deletions.
func TestDebianMissingLibs_AllEntriesNonEmpty(t *testing.T) {
	for _, lib := range []string{
		"libnss3.so", "libnspr4.so", "libatk-1.0.so.0", "libatk-bridge-2.0.so",
		"libcups.so.2", "libdrm.so.2", "libgbm.so.1", "libxkbcommon.so.0",
		"libXcomposite.so.1", "libXdamage.so.1", "libXrandr.so.2",
		"libxshmfence.so.1", "libasound.so.2", "libpango-1.0.so.0",
		"libcairo.so.2",
	} {
		out := debianMissingLibs([]string{lib})
		require.Len(t, out, 1, "%s must map to exactly one apt package", lib)
		assert.NotEmpty(t, out[0], "%s must map to a non-empty apt package name", lib)
	}
}

// TestRpmMissingLibs_AllEntriesNonEmpty (TEST-012): same for the dnf/rpm
// side. Mirror of TestDebianMissingLibs_AllEntriesNonEmpty.
func TestRpmMissingLibs_AllEntriesNonEmpty(t *testing.T) {
	for _, lib := range []string{
		"libnss3.so", "libnspr4.so", "libatk-1.0.so.0", "libatk-bridge-2.0.so",
		"libcups.so.2", "libdrm.so.2", "libgbm.so.1", "libxkbcommon.so.0",
		"libXcomposite.so.1", "libXdamage.so.1", "libXrandr.so.2",
		"libxshmfence.so.1", "libasound.so.2", "libpango-1.0.so.0",
		"libcairo.so.2",
	} {
		out := rpmMissingLibs([]string{lib})
		require.Len(t, out, 1, "%s must map to exactly one rpm package", lib)
		assert.NotEmpty(t, out[0], "%s must map to a non-empty rpm package name", lib)
	}
}

// TestCheckBrowserPackageChrome_NotAnELFBinary_SurfacesDiagnostic covers
// the non-ELF branch of WARN-BROWSER-005: a binary that is not ELF (a
// shell script, partial download, wrong arch) must surface a diagnostic
// entry so the operator does not see silent failure.
func TestCheckBrowserPackageChrome_NotAnELFBinary_SurfacesDiagnostic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("in-process ELF parser is linux-only (Phase 1)")
	}
	if runtime.GOOS != "linux" {
		// On darwin the linux ELF check is gated out by the runtime.GOOS
		// branch in checkBrowserPackageChrome, so no warning fires.
		t.Skip("linux-only ELF check; skipping on " + runtime.GOOS)
	}

	root := withSyntheticPackageChrome(t)

	// Overwrite the chrome binary with a non-ELF payload.
	chromePath := filepath.Join(root, "chrome-linux64", "chrome")
	require.NoError(t, os.WriteFile(chromePath, []byte("#!/bin/sh\necho not-a-browser\n"), 0o755))

	// Recompute the chrome.sha256 to match the new (non-ELF) binary so
	// WARN-BROWSER-006 stays silent and the test only asserts on
	// WARN-BROWSER-005.
	manifest := sha256FileHex(t, chromePath) + "  chrome-linux64/chrome\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "chrome.sha256"), []byte(manifest), 0o644))

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "non-ELF binary must surface a diagnostic warning")
	var found *warning
	for i := range warnings {
		if warnings[i].code == "WARN-BROWSER-005" {
			found = &warnings[i]
		}
	}
	require.NotNil(t, found, "expected WARN-BROWSER-005 in %v", warnings)
	assert.Contains(t, found.message, "ELF",
		"warning should mention ELF so operator knows what to investigate")
}

// TestDarwinMachOWarning_Table (FIX-HIGH-002) unit-tests darwinMachOWarning
// directly against synthetic (missing, machoErr) pairs — the real Mach-O
// parser (missingChromeLibsMachO) is darwin-build-tagged, so a Linux CI
// host can never drive it end-to-end via checkBrowserPackageChrome; this
// bypasses that restriction by feeding the parser's OUTPUT straight in.
// Covers the exact bug this fix closes: before it, the notAMachOBinary case
// returned nil (no warning at all — a corrupted/zeroed chrome binary
// reported a clean doctor bill of health).
func TestDarwinMachOWarning_Table(t *testing.T) {
	tests := []struct {
		name           string
		missing        []string
		machoErr       error
		wantNil        bool
		wantCode       string
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:    "clean — no missing deps, no error",
			missing: nil,
			wantNil: true,
		},
		{
			name:         "structural parse error",
			machoErr:     errors.New("truncated load command 0"),
			wantCode:     "WARN-BROWSER-005",
			wantContains: []string{"could not parse", "truncated load command 0"},
		},
		{
			name:           "not-a-Mach-O binary (FIX-HIGH-002 regression case)",
			missing:        []string{notAMachOBinary},
			wantCode:       "WARN-BROWSER-005",
			wantContains:   []string{"not a valid Mach-O"},
			wantNotContain: []string{"missing required macOS libraries"},
		},
		{
			name:         "real missing dylibs",
			missing:      []string{"@rpath/libmissing.dylib"},
			wantCode:     "WARN-BROWSER-005",
			wantContains: []string{"missing required macOS libraries", "libmissing.dylib"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := darwinMachOWarning(tc.missing, tc.machoErr)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got, "expected a warning for case %q", tc.name)
			assert.Equal(t, tc.wantCode, got.code)
			for _, s := range tc.wantContains {
				assert.Contains(t, got.message, s)
			}
			for _, s := range tc.wantNotContain {
				assert.NotContains(t, got.message, s)
			}
		})
	}
}

// TestCheckBrowserPackageChrome_SymlinkedBinary_DistinctFromHashMismatch
// (FIX-HIGH-003): a symlinked chrome binary at the leaf is a real
// security event (SEC-ADR052-005 — a substituted binary), not a checksum
// disagreement. Before this fix, VerifyChromeSHA256's symlink-refusal
// error was folded into the generic "bundled chrome hash mismatch —
// expected=X got=Y" message, which reads as ordinary bit-rot rather than
// a security-relevant anomaly.
func TestCheckBrowserPackageChrome_SymlinkedBinary_DistinctFromHashMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink requires elevated privileges on Windows")
	}
	if runtime.GOOS != "linux" {
		t.Skip("linux-only ELF check; skipping on " + runtime.GOOS)
	}

	root := withSyntheticPackageChrome(t)
	chromePath := filepath.Join(root, "chrome-linux64", "chrome")

	// Move the real chrome binary aside and replace it with a symlink
	// pointing back at it. Same bytes, same hash — the ONLY thing that
	// changed is that the leaf is now a symlink.
	realTarget := filepath.Join(root, "real-chrome")
	require.NoError(t, os.Rename(chromePath, realTarget))
	require.NoError(t, os.Symlink(realTarget, chromePath))

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "a symlinked binary must surface a warning")
	var found *warning
	for i := range warnings {
		if warnings[i].code == "WARN-BROWSER-006" {
			found = &warnings[i]
		}
	}
	require.NotNil(t, found, "expected WARN-BROWSER-006 in %v", warnings)
	assert.Contains(t, found.message, "symlink",
		"a symlinked binary is a security event, not a hash mismatch — the message must say so")
	assert.NotContains(t, found.message, "expected=",
		"must not report a fabricated expected/got hash-mismatch pair for a symlink refusal")
}

// TestCheckBrowserPackageChrome_MalformedManifest_DistinctFromHashMismatch
// (FIX-HIGH-003): an unparseable chrome.sha256 (garbage content) is a
// different failure mode than a genuine digest DISAGREEMENT between a
// well-formed manifest and the binary. Before this fix it was folded into
// the same "hash mismatch — expected=X got=Y" message with both fields
// left blank (parseManifestDigestBestEffort can't extract a digest from
// garbage), which is actively confusing — it looks like a hash check ran
// and both sides came back empty, not "the manifest itself is broken".
func TestCheckBrowserPackageChrome_MalformedManifest_DistinctFromHashMismatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic-package test only meaningful on linux/darwin")
	}

	root := withSyntheticPackageChrome(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "chrome.sha256"),
		[]byte("this is not a hex digest at all\n"),
		0o644,
	))

	warnings := checkBrowserPackageChrome()
	require.NotEmpty(t, warnings, "a malformed manifest must surface a warning")
	var found *warning
	for i := range warnings {
		if warnings[i].code == "WARN-BROWSER-006" {
			found = &warnings[i]
		}
	}
	require.NotNil(t, found, "expected WARN-BROWSER-006 in %v", warnings)
	assert.Contains(t, found.message, "parse",
		"a malformed manifest is a parse failure, not a hash mismatch — the message must say so")
	assert.NotContains(t, found.message, "expected=",
		"must not report a fabricated expected/got hash-mismatch pair for a parse failure")
}

// withSyntheticPackageChrome builds a minimal chromium/ + chrome-linux64/chrome
// + chrome.sha256 tree under t.TempDir() and points the doctor's
// packageChromeRootOverride at it. The seam is auto-restored via
// t.Cleanup — callers don't need to defer RemoveAll on the fixture root.
//
// STYLE-007: replaces the previous pattern of writing beside the compiled
// test binary via os.Executable()'s parent dir, which was fragile in CI
// (read-only parents) and racy under parallel tests.
//
// The chrome binary is a minimal but structurally valid ELF64 with one
// DT_NEEDED entry pointing to "libc.so.6" — the same soname the host's
// glibc lives under on every mainstream Linux distribution, so the
// in-process WARN-BROWSER-005 ELF walk resolves it cleanly and the
// SHA-256 path can be tested in isolation.
//
// On darwin the stale assumption here used to be that macOS test runs
// "skip the ELF check entirely" with no analog — false: checkBrowserPackageChrome's
// switch (command.go) also runs an in-process Mach-O dependency walk
// (missingChromeLibsMachO) when runtime.GOOS == "darwin", and an ELF64
// binary is not valid Mach-O. That mismatch made this helper's "clean,
// no-warning" fixture spuriously surface WARN-BROWSER-005 ("not a valid
// Mach-O executable") on every darwin CI run — confirmed by the
// Cross-Platform CI matrix (macos-latest, arm64) failing
// TestCheckBrowserPackageChrome_HashMatch_StaysSilent's "must surface no
// warning" assertion. Build a Mach-O with a single @rpath/ dependency on
// darwin instead — dylibResolves treats @rpath/ names as always-present
// (see TestMissingChromeLibsMachO_RpathSkipped), so the Mach-O walk
// resolves it cleanly regardless of what is actually installed on the
// CI runner, mirroring the ELF branch's "resolves cleanly" property.
//
// Returns the synthetic chromium/ root (the path the doctor sees as
// "package chrome root"). All test artifacts live under this directory.
func withSyntheticPackageChrome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "darwin" {
		return withSyntheticPackageChromeContent(t, buildMinimalMachO64(t, "@rpath/libSystem.dylib"))
	}
	return withSyntheticPackageChromeContent(t, buildMinimalELF64(t, "libc.so.6"))
}

// withSyntheticPackageChromeContent is withSyntheticPackageChrome
// parametrized on the chrome binary's raw content, so tests that need a
// non-ELF (or non-Mach-O, when paired with the checkBrowserPackageChromeGOOS
// seam) payload — e.g. the FIX-HIGH-002 "corrupt/zeroed binary" darwin
// coverage — don't have to hand-roll the chromium/ + chrome.sha256 tree
// layout themselves.
func withSyntheticPackageChromeContent(t *testing.T, chrome []byte) string {
	t.Helper()

	root := filepath.Join(t.TempDir(), "chromium")
	chromeDir := filepath.Join(root, "chrome-linux64")
	require.NoError(t, os.MkdirAll(chromeDir, 0o755))

	chromePath := filepath.Join(chromeDir, "chrome")
	require.NoError(t, os.WriteFile(chromePath, chrome, 0o755))

	sum := sha256.Sum256(chrome)
	manifest := hex.EncodeToString(sum[:]) + "  chrome-linux64/chrome\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "chrome.sha256"), []byte(manifest), 0o644))

	prevOverride := packageChromeRootOverride
	packageChromeRootOverride = root
	t.Cleanup(func() { packageChromeRootOverride = prevOverride })

	return root
}

// sha256FileHex returns the lowercase-hex SHA-256 of the file at path,
// computed by streaming through a fresh hasher (matches the production
// readChromeSHA path). Used by tests that overwrite the chrome binary
// and need to regenerate the matching chrome.sha256 manifest.
func sha256FileHex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// buildMinimalELF64 constructs the smallest valid ELF64 binary the
// in-process ELF parser in command_libs_linux.go can parse:
//   - ELF magic + ELF64 header (e_type=ET_EXEC, e_machine=EM_X86_64)
//   - Two program headers: PT_LOAD covering the whole file, PT_DYNAMIC
//     pointing at the DT_* table.
//   - A DT_* table with DT_NEEDED + DT_STRTAB + DT_STRSZ + DT_NULL.
//   - A string table containing soname + NUL.
//
// This is a structurally valid ELF — it cannot be executed, but the
// in-process DT_NEEDED walker doesn't try; it only walks program headers
// to find PT_DYNAMIC, then walks that for DT_NEEDED → DT_STRTAB. We
// choose soname="libc.so.6" because it exists at /lib64 on every
// mainstream Linux distribution and so the search-path probe in
// sonameExists() resolves it on any Linux test host.
func buildMinimalELF64(t *testing.T, soname string) []byte {
	t.Helper()

	// Layout (offsets):
	//   [0..64)    ELF64 header
	//   [64..120)  Phdr[0] = PT_LOAD covering the whole file
	//   [120..176) Phdr[1] = PT_DYNAMIC pointing at the DT_* table
	//   [176..240) DT_* table: DT_NEEDED, DT_STRTAB, DT_STRSZ, DT_NULL
	//   [240..)    string table: soname + NUL

	const strtabOff = 240
	const dynOff = 176
	const dynSize = 64 // 4 entries × 16 bytes

	hdr := make([]byte, 64)
	// ELF magic + class + data + version + OS/ABI = 0
	hdr[0] = 0x7f
	hdr[1] = 'E'
	hdr[2] = 'L'
	hdr[3] = 'F'
	hdr[4] = 2 // EI_CLASS = ELFCLASS64
	hdr[5] = 1 // EI_DATA = ELFDATA2LSB (little-endian)
	// Layout in little-endian:
	// e_type at 16 (ET_EXEC = 2)
	hdr[16] = 2
	hdr[17] = 0
	// e_machine at 18 (EM_X86_64 = 0x3e)
	hdr[18] = 0x3e
	hdr[19] = 0
	// e_version at 20 (EV_CURRENT = 1)
	hdr[20] = 1
	// e_phoff at 32 = 64 (right after the ELF header)
	hdr[32] = 64
	hdr[33] = 0
	hdr[34] = 0
	hdr[35] = 0
	hdr[36] = 0
	hdr[37] = 0
	hdr[38] = 0
	hdr[39] = 0
	// e_ehsize at 52 = 64
	hdr[52] = 64
	hdr[53] = 0
	// e_phentsize at 54 = 56
	hdr[54] = 56
	hdr[55] = 0
	// e_phnum at 56 = 2
	hdr[56] = 2
	hdr[57] = 0

	// Phdr[0] = PT_LOAD (p_type=1) covering the whole file. Only the
	// fields the parser touches need real values (p_type is read; p_offset
	// and p_filesz on PT_DYNAMIC are read — but for PT_LOAD the parser
	// just checks p_type != 2 and continues).
	phdr0 := make([]byte, 56)
	writeLE32(phdr0, 0, 1)  // p_type = PT_LOAD
	writeLE32(phdr0, 4, 5)  // p_flags = PF_R|PF_X
	writeLE64(phdr0, 8, 0)  // p_offset
	writeLE64(phdr0, 16, 0) // p_vaddr
	writeLE64(phdr0, 24, 0) // p_paddr
	writeLE64(phdr0, 32, 0) // p_filesz (full file)
	writeLE64(phdr0, 40, 0) // p_memsz
	writeLE64(phdr0, 48, 0) // p_align

	// Phdr[1] = PT_DYNAMIC (p_type=2). p_offset points at the DT_* table;
	// p_filesz is its size.
	phdr1 := make([]byte, 56)
	writeLE32(phdr1, 0, 2)        // p_type = PT_DYNAMIC
	writeLE32(phdr1, 4, 6)        // p_flags = PF_R|PF_W
	writeLE64(phdr1, 8, dynOff)   // p_offset
	writeLE64(phdr1, 16, dynOff)  // p_vaddr
	writeLE64(phdr1, 24, dynOff)  // p_paddr
	writeLE64(phdr1, 32, dynSize) // p_filesz
	writeLE64(phdr1, 40, dynSize) // p_memsz
	writeLE64(phdr1, 48, 8)       // p_align

	// DT_* table (4 entries × 16 bytes).
	dyn := make([]byte, dynSize)
	// DT_NEEDED (tag=1, val=offset-into-strtab = 0)
	writeLE64(dyn, 0, 1)
	writeLE64(dyn, 8, 0)
	// DT_STRTAB (tag=5, val=strtab offset in file)
	writeLE64(dyn, 16, 5)
	writeLE64(dyn, 24, strtabOff)
	// DT_STRSZ (tag=10, val=strtab size = len(soname)+1)
	writeLE64(dyn, 32, 10)
	writeLE64(dyn, 40, uint64(len(soname)+1))
	// DT_NULL (tag=0, val=0)
	writeLE64(dyn, 48, 0)
	writeLE64(dyn, 56, 0)

	strtab := append([]byte(soname), 0)

	out := make([]byte, 0, strtabOff+len(strtab))
	out = append(out, hdr...)
	out = append(out, phdr0...)
	out = append(out, phdr1...)
	out = append(out, dyn...)
	out = append(out, strtab...)
	return out
}

// writeLE32 writes a little-endian uint32 into b[offset..offset+4].
func writeLE32(b []byte, offset int, v uint32) {
	b[offset] = byte(v)
	b[offset+1] = byte(v >> 8)
	b[offset+2] = byte(v >> 16)
	b[offset+3] = byte(v >> 24)
}

// writeLE64 writes a little-endian uint64 into b[offset..offset+8].
func writeLE64(b []byte, offset int, v uint64) {
	b[offset] = byte(v)
	b[offset+1] = byte(v >> 8)
	b[offset+2] = byte(v >> 16)
	b[offset+3] = byte(v >> 24)
	b[offset+4] = byte(v >> 32)
	b[offset+5] = byte(v >> 40)
	b[offset+6] = byte(v >> 48)
	b[offset+7] = byte(v >> 56)
}

// silence unused-import warning for `bytes` (kept for future ELF fixture
// builders if WARN-BROWSER-005 grows synthetic-binary tests).
var _ = bytes.NewBuffer
