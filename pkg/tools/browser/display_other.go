// Omnipus — Xvfb virtual-display sidecar: non-Linux stub (live-browser-video-
// streaming spec, FR-021 / US-10, spec §Platform Matrix). License: MIT.
// Copyright (c) 2026 Omnipus contributors.
//
// Xvfb (X11) is Linux-only, so video-capable live view is Linux-only. On
// every other GOOS this file provides a no-op DisplaySidecar whose Start
// always fails with a descriptive error — callers classify the install
// not-video-capable and continue with headless agent browsing unchanged.
// Never panics, never blocks, never touches agent browsing.
//
// DisplayConfig and the DisplaySidecar interface are platform-agnostic and
// live in the untagged display.go, shared by this file and display_linux.go.

//go:build !linux

package browser

import (
	"context"
	"fmt"
)

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
