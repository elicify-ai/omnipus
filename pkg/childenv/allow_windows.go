// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build windows

package childenv

import "strings"

// windowsAllowedKeys are the Windows system variables a child needs to find
// system files and temp folders (ruling WU19-win 2). None carries a secret;
// they play the role HOME and TMPDIR play on Unix.
var windowsAllowedKeys = []string{
	"SystemRoot", "SystemDrive", "windir", "ComSpec", "PATHEXT", "TEMP", "TMP", "USERPROFILE",
}

// platformAllowedKey is the Windows half of AllowedKey: Windows variable
// names are case-insensitive, so every allowlist entry and prefix (and the
// Windows system variables above) matches regardless of case, and "Path"
// passes as PATH.
func platformAllowedKey(name string) bool {
	for key := range allowedEnvKeys {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	for _, key := range windowsAllowedKeys {
		if strings.EqualFold(key, name) {
			return true
		}
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
