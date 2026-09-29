//go:build windows

// Omnipus — on Windows the caller verifies the opened file's identity against
// Lstat before reading, since O_NOFOLLOW is unavailable.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import "os"

func openViewForIndex(abs string) (*os.File, error) {
	return os.Open(abs)
}
