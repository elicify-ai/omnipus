//go:build !windows

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// canExecute asks the kernel's exec-time question for the effective user:
// faccessat(AT_EACCESS, X_OK). Mode bits alone lie (a 0601 file owned by the
// current user is not executable for its owner). It replaces the retired
// agent-side cli_path check (r17 §3.5 "reuse").
func canExecute(path string) error {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS)
}

// isAbsolutePath is the Unix rule: the path starts with "/".
func isAbsolutePath(p string) bool { return filepath.IsAbs(p) }

// windowsNameCheck applies only on Windows.
func windowsNameCheck(string) string { return "" }
