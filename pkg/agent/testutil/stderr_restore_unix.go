//go:build !windows

package testutil

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// processStderrState is the harness pipe duplicated out from under the panic
// redirect. fd is close-on-exec so a child process cannot inherit it and hold
// the test runner's output pipe open after this process restores and closes
// its own copy.
type processStderrState struct {
	fd  int
	set bool
}

// saveProcessStderr duplicates the fd Go currently uses for os.Stderr.
// logger.initPanicFile then dup2s the panic log onto that same fd and never
// restores it. The duplicate is created with close-on-exec in one call so
// there is no window where a child can inherit it.
func saveProcessStderr() (processStderrState, error) {
	saved, err := unix.FcntlInt(os.Stderr.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return processStderrState{}, fmt.Errorf("dup stderr: %w", err)
	}
	return processStderrState{fd: saved, set: true}, nil
}

// restoreProcessStderr puts the saved fd back onto os.Stderr and closes the
// spare. Closing the spare does not close the restored stderr.
func restoreProcessStderr(saved processStderrState) error {
	if !saved.set {
		return nil
	}
	if err := unix.Dup2(saved.fd, int(os.Stderr.Fd())); err != nil {
		closeErr := unix.Close(saved.fd)
		if closeErr != nil {
			return fmt.Errorf("dup2 stderr: %w (close saved: %w)", err, closeErr)
		}
		return fmt.Errorf("dup2 stderr: %w", err)
	}
	if err := unix.Close(saved.fd); err != nil {
		return fmt.Errorf("close saved stderr: %w", err)
	}
	return nil
}
