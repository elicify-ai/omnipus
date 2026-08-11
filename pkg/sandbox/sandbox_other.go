//go:build !linux && !darwin

package sandbox

import "log/slog"

func selectBackendPlatform() (SandboxBackend, string) {
	slog.Info("Kernel sandbox not available on this platform. Using application-level enforcement.",
		"backend", "fallback")
	fb := NewFallbackBackend()
	return fb, fb.Name()
}

func probeLandlockABIPlatform() int {
	return 0
}

func restrictCurrentThreadIfNeeded() error { return nil }

// MarkStartLockedCalled is a no-op on non-Linux platforms: there is no
// Landlock domain to track, so the StartLocked contract marker has no effect.
// See the linux implementation in sandbox_linux.go for the full description.
func MarkStartLockedCalled() {}
