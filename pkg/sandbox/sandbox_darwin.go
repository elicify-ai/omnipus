//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import "log/slog"

// selectBackendPlatform picks the strongest available backend on macOS
// (ADR-052 Phase-3 AC-6). Seatbelt is preferred; if sandbox-exec is missing or
// an operator has set the kill-switch, we degrade to application-level
// enforcement rather than failing boot — hard constraint #4.
func selectBackendPlatform() (SandboxBackend, string) {
	sb := NewSeatbeltBackend()
	if sb.Available() {
		name := sb.Name()
		slog.Info("Seatbelt sandbox available", "backend", name, "enforcement", "per-child via sandbox-exec")
		return sb, name
	}

	slog.Warn("Seatbelt not available. Using application-level enforcement.", "backend", "fallback")
	fb := NewFallbackBackend()
	return fb, fb.Name()
}

// probeLandlockABIPlatform reports 0 on macOS: Landlock is a Linux LSM and has
// no darwin equivalent. Seatbelt capability is reported through the backend
// name, not through an ABI version.
func probeLandlockABIPlatform() int {
	return 0
}

// restrictCurrentThreadIfNeeded is a no-op on macOS and cannot be otherwise.
//
// On Linux this is where a spawn thread enters the Landlock domain so the
// child inherits it. Seatbelt has no equivalent: sandbox-exec can only launch
// a fresh child already inside a profile, and the in-process API
// (sandbox_init(3)) is C-only, which hard constraint #2 forbids. Confinement
// therefore happens in applyPlatformHardening, which rewrites the child's argv
// to run under sandbox-exec. Returning nil here is correct, not a gap — see
// backend_darwin_seatbelt.go's header for the posture consequences.
func restrictCurrentThreadIfNeeded() error { return nil }

// MarkStartLockedCalled is a no-op on macOS: there is no Landlock domain whose
// inheritance needs tracking. See the linux implementation in sandbox_linux.go.
func MarkStartLockedCalled() {}
