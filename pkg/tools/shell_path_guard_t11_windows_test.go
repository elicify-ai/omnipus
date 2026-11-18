//go:build windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "testing"

// t11RootIsWritable implements spec §3.1 condition 5 on Windows: the root
// always counts as writable, so the stand-alone quoted-literal rule is off.
func t11RootIsWritable(t *testing.T) bool {
	t.Helper()
	return true
}
