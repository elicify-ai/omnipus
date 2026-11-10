//go:build linux || darwin

// Omnipus — non-blocking read flags for RootControlFS (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"

	"golang.org/x/sys/unix"
)

// nonBlockingReadFlags opens without blocking, so opening a FIFO named like
// a view file returns at once instead of waiting for a writer.
func nonBlockingReadFlags() int { return os.O_RDONLY | unix.O_NONBLOCK }
