//go:build windows

package skills

import "os"

// hardLinkCount cannot be answered from os.FileInfo on Windows; it reports a
// single link, so the hard-link refusal is a Unix-only check.
func hardLinkCount(os.FileInfo) uint64 { return 1 }
