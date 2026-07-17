// Omnipus — virtual-display sidecar: platform-agnostic types (live-browser-
// video-streaming spec, FR-021 / US-10). License: MIT. Copyright (c) 2026
// Omnipus contributors.
//
// DisplayConfig, its defaults, and the DisplaySidecar interface are shared by
// both the real Linux Xvfb-backed implementation (display_linux.go) and the
// non-Linux no-op stub (display_other.go). They live here, in an UNTAGGED
// file compiled on every platform, so the two platform files each define
// ONLY their own concrete implementation instead of maintaining two
// independent copies of the same config struct and interface (CS3 — the
// previous layout duplicated both in display_linux.go and display_other.go
// behind a "keep in sync" comment).

package browser

import "context"

// Default framebuffer geometry (FR-021) used when a DisplayConfig field is
// left zero-valued.
const (
	defaultDisplayWidth  = 1280
	defaultDisplayHeight = 720
	defaultDisplayDepth  = 24
)

// DisplayConfig configures the virtual-display sidecar's framebuffer. On
// non-Linux platforms the values are accepted but never used — Start always
// fails there (see display_other.go).
type DisplayConfig struct {
	Width  int
	Height int
	Depth  int
}

// withDefaults returns cfg with zero-valued fields replaced by the
// 1280x720x24 default (FR-021).
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
// headful-Chrome launch path (managedExecAllocatorOpts, W2) to give headful
// Chrome a framebuffer. display_linux.go provides the real Linux
// implementation (a supervised Xvfb process); display_other.go provides the
// non-Linux stub with an identical surface whose Start always fails.
type DisplaySidecar interface {
	// Start launches the sidecar and blocks until the display is confirmed
	// ready, ctx is canceled, or the readiness timeout expires. A non-nil
	// error means the sidecar is NOT available — callers MUST classify the
	// install not-video-capable (US-10/AC-3) and MUST NOT crash the gateway
	// or degrade agent browsing.
	Start(ctx context.Context) error
	// Display returns the wired DISPLAY value (e.g. ":37"), or "" if the
	// sidecar never started successfully or has since stopped.
	Display() string
	// Healthy reports whether the sidecar is currently up and supervised.
	Healthy() bool
	// Stop tears the sidecar down cleanly and frees the display number.
	// Safe to call even if Start was never called or failed.
	Stop()
}
