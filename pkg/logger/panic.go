package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// bootStderr holds the process's original stderr, captured by the first
// InitPanic before the panic log is dup2'd over fd 2 (#1823).
var bootStderr atomic.Pointer[os.File]

// StderrRedirected reports whether InitPanic moved os.Stderr into the panic log,
// i.e. whether an error written to os.Stderr no longer reaches the terminal.
func StderrRedirected() bool { return bootStderr.Load() != nil }

// BootStderr returns the stderr the process started with. InitPanic redirects
// os.Stderr into the panic log, so a boot error printed to os.Stderr afterwards
// never reaches the operator's terminal; print it here instead. It falls back
// to os.Stderr when no redirect happened.
func BootStderr() io.Writer {
	if f := bootStderr.Load(); f != nil {
		return f
	}
	return os.Stderr
}

func InitPanic(filePath string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}
	writer, err := initPanicFile(filePath)
	if err != nil {
		return nil, err
	}
	return func() {
		defer writer.Close()
		if err := recover(); err != nil {
			now := time.Now().Format("2006-01-02 15:04:05")
			stack := debug.Stack()
			logMsg := "\n\n====================\n[" + now + "] PANIC OCCURRED: " + fmt.Sprintf(
				"%v",
				err,
			) + "\n" + string(
				stack,
			)

			writer.Write([]byte(logMsg))

			os.Exit(1)
		}
	}, nil
}
