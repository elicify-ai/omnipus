//go:build linux || darwin

// Omnipus — no-follow opening for Library view-index reads.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"os"
	"syscall"
)

func openViewForIndex(abs string) (*os.File, error) {
	return os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
