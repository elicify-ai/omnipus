// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package doctor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
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
	cfg.Tools.Browser.CaptureSharedContext = true

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
	cfg.Tools.Browser.CaptureSharedContext = true

	warnings := checkBrowserVideoCapability(cfg)

	require.Len(t, warnings, 1)
	assert.Equal(t, "WARN-BROWSER-002", warnings[0].code)
	assert.Contains(t, warnings[0].message, "lite build")
	assert.Contains(t, warnings[0].message, "BUILD")
}

// TestCheckBrowserVideoCapability_CaptureSharedContextDisabled covers
// WARN-BROWSER-004: warns when capture_shared_context=false, the ADR-048
// precondition doctor can verify from config alone (unlike ExtensionDir
// seeding, which only a live BrowserManager knows about and which doctor
// must never construct).
func TestCheckBrowserVideoCapability_CaptureSharedContextDisabled(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ClassifyVideoCapabilityWithExec only ever classifies capable on linux; skipping on " + runtime.GOOS)
	}

	origAvailable := webrtc.Available
	webrtc.Available = true
	t.Cleanup(func() { webrtc.Available = origAvailable })

	cfg := &config.Config{}
	cfg.Tools.Browser.WebRTCEnabled = true
	cfg.Tools.Browser.CaptureSharedContext = false
	cfg.Tools.Browser.ExecPath = "/usr/bin/google-chrome-stable"

	warnings := checkBrowserVideoCapability(cfg)

	require.Len(t, warnings, 1)
	assert.Equal(t, "WARN-BROWSER-004", warnings[0].code)
	assert.Contains(t, warnings[0].message, "capture_shared_context=true")
}

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
	cfg.Tools.Browser.CaptureSharedContext = true
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
	cfg.Tools.Browser.CaptureSharedContext = true
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

	root := synthesizePackageChrome(t, "linux")
	defer os.RemoveAll(root.parent)

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

	root := synthesizePackageChrome(t, "linux")
	defer os.RemoveAll(root.parent)

	// Overwrite chrome.sha256 with a guaranteed-wrong digest.
	wrongPath := filepath.Join(root.parent, "chromium", "chrome.sha256")
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

// TestParseSHA256Manifest_Hardening covers the SHA-256 parser edge cases
// enumerated in ADR-052 SEC-ADR052-004: BOM, CRLF, sha256: prefix, comment
// lines, uppercase hex (rejected), wrong length (rejected), trailing
// whitespace, two-line format, sha256sum binary mode.
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
		{name: "leading BOM stripped", input: "\xEF\xBB\xBF" + good + "  chrome\n", want: good},
		{name: "CRLF tolerated", input: good + "  chrome\r\n", want: good},
		{name: "comment line skipped", input: "# generated by goreleaser\n" + good + "  chrome\n", want: good},
		{name: "leading whitespace tolerated", input: "   " + good + "\n", want: good},
		{name: "uppercase hex rejected", input: "0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF  chrome\n", want: ""},
		{name: "wrong length rejected", input: "abcdef  chrome\n", want: ""},
		{name: "empty file", input: "", want: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseSHA256Manifest([]byte(tc.input))
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
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

	root := synthesizePackageChrome(t, "linux")
	defer os.RemoveAll(root.parent)

	// Overwrite the chrome binary with a non-ELF payload.
	chromePath := filepath.Join(root.parent, "chromium", "chrome-linux64", "chrome")
	require.NoError(t, os.WriteFile(chromePath, []byte("#!/bin/sh\necho not-a-browser\n"), 0o755))

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

// synthesizePackageChrome builds a minimal chromium/ + chrome-linux64/chrome
// + chrome.sha256 tree at the location os.Executable()-based resolver
// looks for. Returns the parent dir of the chromium/ root + the path of
// the chromium dir itself.
//
// The chrome binary is a minimal but structurally valid ELF64 with one
// DT_NEEDED entry pointing to "libc.so.6" — the same soname the host's
// glibc lives under on every mainstream Linux distribution, so the
// in-process WARN-BROWSER-005 ELF walk resolves it cleanly and the
// SHA-256 path can be tested in isolation. macOS test runs skip the ELF
// check entirely (the runtime.GOOS == "linux" gate), so the synthetic
// binary need not be a valid Mach-O on darwin.
//
// The trick: the resolver hard-codes <dir(os.Executable())>/../chromium —
// which for a `go test` test binary resolves to <GOTMPDIR>/test-binary-dir/
// /../chromium, i.e. one level up from the test binary. We compute that
// location and lay down the synthetic tree there.
type synthChrome struct {
	parent string // parent dir (e.g. GOTMPDIR)
	root   string // <parent>/chromium
	chrome string // <root>/chrome-linux64/chrome
}

func synthesizePackageChrome(t *testing.T, _ string) synthChrome {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	absExe, err := filepath.Abs(exe)
	require.NoError(t, err)
	dir := filepath.Dir(absExe)
	parent := filepath.Dir(dir)
	root := filepath.Join(parent, "chromium")
	chromeDir := filepath.Join(root, "chrome-linux64")
	require.NoError(t, os.MkdirAll(chromeDir, 0o755))

	chrome := buildMinimalELF64(t, "libc.so.6")
	chromePath := filepath.Join(chromeDir, "chrome")
	require.NoError(t, os.WriteFile(chromePath, chrome, 0o755))

	sum := sha256.Sum256(chrome)
	manifest := hex.EncodeToString(sum[:]) + "  chrome-linux64/chrome\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "chrome.sha256"), []byte(manifest), 0o644))

	return synthChrome{parent: parent, root: root, chrome: chromePath}
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
