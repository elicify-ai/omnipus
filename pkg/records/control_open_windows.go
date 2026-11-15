//go:build windows

// Omnipus — read flags for RootControlFS on Windows (#1017).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "os"

// nonBlockingReadFlags is a plain read on Windows, which has no FIFO special
// files in the filesystem; the kind is still checked on the opened file.
func nonBlockingReadFlags() int { return os.O_RDONLY }
