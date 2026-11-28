// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build darwin

package launcher

// applyLimits sets the limits macOS accepts. Address-space, data and
// resident-size limits return EINVAL on macOS (S-1), so memory is left to the
// parent-side watchdog (best effort, AD-6).
func applyLimits(l Limits) error { return applyCommonLimits(l) }

// armParentDeath: macOS has no parent-death signal. The target exits on stdin
// EOF and startup recovery handles the rest (AD-2, AD-8).
func armParentDeath() error { return nil }
