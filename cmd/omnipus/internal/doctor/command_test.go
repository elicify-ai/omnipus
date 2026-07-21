// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package doctor

import (
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
