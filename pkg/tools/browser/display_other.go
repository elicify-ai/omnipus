// Omnipus — Xvfb virtual-display sidecar: non-Linux stub (live-browser-video-
// streaming spec, FR-021 / US-10, spec §Platform Matrix). License: MIT.
// Copyright (c) 2026 Omnipus contributors.
//
// Xvfb (X11) is Linux-only, so video-capable live view is Linux-only. On
// every other GOOS this file provides a no-op DisplaySidecar whose Start
// always fails with a descriptive error — callers classify the install
// not-video-capable and continue with headless agent browsing unchanged.
// Never panics, never blocks, never touches agent browsing.

//go:build !linux

package browser

import (
	"context"
	"fmt"
)

// Default framebuffer geometry, kept in sync with display_linux.go so
// callers see the same defaults regardless of platform.
const (
	defaultDisplayWidth  = 1280
	defaultDisplayHeight = 720
	defaultDisplayDepth  = 24
)

// DisplayConfig configures the virtual-display sidecar's framebuffer. See
// display_linux.go for the fields' meaning; on this platform the values are
// accepted but never used (Start always fails).
type DisplayConfig struct {
	Width  int
	Height int
	Depth  int
}

// withDefaults returns cfg with zero-valued fields replaced by the
// 1280x720x24 default, matching display_linux.go.
func (cfg DisplayConfig) withDefaults() DisplayConfig {
	if cfg.Width <= 0 {
		cfg.Width = defaultDisplayWidth
	}
	if cfg.Height <= 0 {
		cfg.Height = defaultDisplayHeight
	}
	if cfg.Depth <= 0 {
		cfg.Depth = defaultDisplayDepth
	}
	return cfg
}

// DisplaySidecar is the supervised virtual-display process consumed by the
// headful-Chrome launch path. See display_linux.go for the interface's full
// doc; the non-Linux stub below satisfies the same surface honestly.
type DisplaySidecar interface {
	Start(ctx context.Context) error
	Display() string
	Healthy() bool
	Stop()
}

// NewDisplaySidecar returns a stub DisplaySidecar on non-Linux platforms.
// Its Start always fails so callers classify the install not-video-capable
// (US-10/AC-3, spec §Platform Matrix) instead of guessing or crashing.
func NewDisplaySidecar(cfg DisplayConfig) DisplaySidecar {
	return &unavailableDisplaySidecar{cfg: cfg.withDefaults()}
}

// unavailableDisplaySidecar is the non-Linux no-op stub. Xvfb only exists on
// Linux (X11); every method here is a safe, honest not-available response —
// never a crash, never a fake success.
type unavailableDisplaySidecar struct {
	cfg DisplayConfig
}

func (s *unavailableDisplaySidecar) Start(_ context.Context) error {
	return fmt.Errorf("browser: xvfb sidecar: not available on this platform (Linux-only, FR-021)")
}

func (s *unavailableDisplaySidecar) Display() string { return "" }

func (s *unavailableDisplaySidecar) Healthy() bool { return false }

func (s *unavailableDisplaySidecar) Stop() {}
