// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !windows

package childenv

// platformAllowedKey adds nothing outside Windows: Linux and macOS keep the
// exact-case allowlist.
func platformAllowedKey(string) bool { return false }

// SameKey reports whether two environment variable names name the same
// variable: exact-case outside Windows.
func SameKey(a, b string) bool { return a == b }
