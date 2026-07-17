// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestBrowserInstallRoot_DefaultProfileDir covers the fresh/default-install
// path (ADR-044 T5): cfg.Tools.Browser.ProfileDir empty falls back to
// <homePath>/browser/profiles/default (mirrors browser.DefaultConfig's own
// default, manager.go:78), and the derived install root must be
// <homePath>/browser/chromium — the same root exec_resolver.go's private
// installRoot derivation computes, so the boot-time sidecar wiring
// (bootLiveBrowserVideo) and setupAndStartServices' RegisterBrowserVideo
// agree on the exact install exec_resolver.go would actually launch.
func TestBrowserInstallRoot_DefaultProfileDir(t *testing.T) {
	homePath := "/home/user/.omnipus"
	cfg := &config.Config{}

	got := browserInstallRoot(homePath, cfg)

	want := filepath.Join(homePath, "browser", "chromium")
	if got != want {
		t.Errorf("browserInstallRoot(%q, empty ProfileDir) = %q, want %q", homePath, got, want)
	}
}

// TestBrowserInstallRoot_CustomProfileDir covers an operator-configured
// cfg.Tools.Browser.ProfileDir: the install root must still resolve to
// <profileDir's grandparent>/chromium regardless of where the profile lives.
func TestBrowserInstallRoot_CustomProfileDir(t *testing.T) {
	homePath := "/home/user/.omnipus"
	cfg := &config.Config{}
	cfg.Tools.Browser.ProfileDir = "/data/custom/browser/profiles/default"

	got := browserInstallRoot(homePath, cfg)

	want := filepath.Clean("/data/custom/browser/chromium")
	if got != want {
		t.Errorf("browserInstallRoot(%q, custom ProfileDir) = %q, want %q", homePath, got, want)
	}
}

// TestBrowserInstallRoot_AgreesWithRegisterBrowserVideoSite locks in that the
// extracted helper produces byte-identical output to the (pre-extraction)
// inline derivation setupAndStartServices used at its RegisterBrowserVideo
// call site — a regression here would silently desync the boot-time sidecar
// wiring from the capability check the SPA's live-view panel relies on.
func TestBrowserInstallRoot_AgreesWithRegisterBrowserVideoSite(t *testing.T) {
	homePath := "/home/user/.omnipus"
	cfg := &config.Config{}

	browserProfileDir := cfg.Tools.Browser.ProfileDir
	if browserProfileDir == "" {
		browserProfileDir = filepath.Join(homePath, "browser", "profiles", "default")
	}
	inlineEquivalent := filepath.Clean(
		filepath.Join(filepath.Dir(filepath.Clean(browserProfileDir)), "..", "chromium"),
	)

	got := browserInstallRoot(homePath, cfg)
	if got != inlineEquivalent {
		t.Errorf("browserInstallRoot(%q, cfg) = %q, want %q (must match the original inline derivation)",
			homePath, got, inlineEquivalent)
	}
}
