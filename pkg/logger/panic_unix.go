//go:build !windows

package logger

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func initPanicFile(panicFile string) (io.WriteCloser, error) {
	file, err := os.OpenFile(panicFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND|os.O_SYNC, 0o600)
	if err != nil {
		panic(fmt.Sprintf("error in open panic: %v", err))
	}
	if bootStderr.Load() == nil {
		fd, dupErr := unix.FcntlInt(os.Stderr.Fd(), unix.F_DUPFD_CLOEXEC, 3)
		if dupErr != nil {
			// Cannot keep the original stderr (e.g. descriptor exhaustion).
			// Redirecting anyway would hide boot errors for good; leaving fd 2
			// alone would let the full text of later errors reach the terminal,
			// which the safe boot message exists to prevent. Refuse to start,
			// with a message that holds only the OS reason (#1823).
			_ = file.Close()
			return nil, fmt.Errorf("cannot preserve stderr before redirecting it to the panic log: %w", dupErr)
		}
		bootStderr.CompareAndSwap(nil, os.NewFile(uintptr(fd), "boot-stderr"))
	}
	if err = unix.Dup2(int(file.Fd()), int(os.Stderr.Fd())); err != nil {
		panic(fmt.Sprintf("error in syscall.Dup2: %v", err))
	}
	return file, nil
}
