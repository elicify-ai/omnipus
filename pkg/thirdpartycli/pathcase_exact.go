//go:build !darwin && !windows

package thirdpartycli

// foldPathCase keeps the path as written on Linux, whose filesystems are
// case-sensitive (see pathWithin).
func foldPathCase(p string) string { return p }
