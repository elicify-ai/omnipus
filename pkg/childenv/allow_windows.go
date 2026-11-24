// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package childenv

import "strings"

// platformAllowedKey is the Windows half of AllowedKey: Windows variable
// names are case-insensitive, so every allowlist entry and prefix (and the
// Windows system variables, private_names.go::windowsSystemNames) matches
// regardless of case, and "Path" passes as PATH.
func platformAllowedKey(name string) bool {
	for key := range allowedEnvKeys {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	if isWindowsSystemName(name) {
		return true
	}
	for _, prefix := range allowedEnvPrefixes {
		if len(name) >= len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}

// SameKey reports whether two environment variable names name the same
// variable: case-insensitively on Windows.
func SameKey(a, b string) bool { return strings.EqualFold(a, b) }
