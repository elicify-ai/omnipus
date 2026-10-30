//go:build darwin || windows

package clitrust

import "strings"

// foldPathCase lower-cases a path for comparison on macOS and Windows, whose
// default volumes are case-insensitive (see pathWithin).
func foldPathCase(p string) string { return strings.ToLower(p) }
