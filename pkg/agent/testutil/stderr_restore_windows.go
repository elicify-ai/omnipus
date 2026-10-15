//go:build windows

package testutil

// Windows panic logging replaces the os.Stderr variable; it does not dup the
// process fd. There is no fd to put back. println still uses the original fd.
func saveProcessStderr() (int, error) { return -1, nil }

func restoreProcessStderr(int) error { return nil }
