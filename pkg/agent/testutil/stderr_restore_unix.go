//go:build !windows

package testutil

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// saveProcessStderr duplicates the fd Go currently uses for os.Stderr.
// logger.initPanicFile then dup2s the panic log onto that same fd and never
// restores it. The saved fd still refers to the harness pipe.
func saveProcessStderr() (int, error) {
	saved, err := unix.Dup(int(os.Stderr.Fd()))
	if err != nil {
		return -1, fmt.Errorf("dup stderr: %w", err)
	}
	return saved, nil
}

// restoreProcessStderr puts the saved fd back onto os.Stderr and closes the
// spare. Closing the spare does not close the restored stderr.
func restoreProcessStderr(saved int) error {
	if saved < 0 {
		return nil
	}
	if err := unix.Dup2(saved, int(os.Stderr.Fd())); err != nil {
		_ = unix.Close(saved)
		return fmt.Errorf("dup2 stderr: %w", err)
	}
	return unix.Close(saved)
}
