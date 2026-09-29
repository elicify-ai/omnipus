//go:build windows

// Omnipus — Windows has no O_NOFOLLOW; the caller compares the opened file's
// volume/file ID with Lstat before reading any bytes.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"io/fs"
	"os"
)

func openNoFollowDefault(abs string) (fs.File, error) {
	return os.Open(abs)
}
