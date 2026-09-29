//go:build linux || darwin

// Omnipus — no-follow view open on supported Unix platforms.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"io/fs"
	"os"
	"syscall"
)

func openNoFollowDefault(abs string) (fs.File, error) {
	return os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
