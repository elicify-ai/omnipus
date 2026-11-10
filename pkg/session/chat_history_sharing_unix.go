// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux || darwin

package session

// isArchiveSharingBusy is Windows-specific. Unix errors retain their normal
// failure classification; this predicate is not wired into retention yet.
func isArchiveSharingBusy(err error) bool {
	return false
}
