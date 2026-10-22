//go:build windows

package testutil

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// processStderrState is what panic logging on Windows replaces:
// os.Stderr and the process standard-error handle (STD_ERROR_HANDLE).
// logger.initPanicFile sets both, and its cleanup closes the panic file
// without putting either back.
type processStderrState struct {
	file   *os.File
	handle windows.Handle
	set    bool
}

func saveProcessStderr() (processStderrState, error) {
	handle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return processStderrState{}, fmt.Errorf("read stderr handle: %w", err)
	}
	return processStderrState{file: os.Stderr, handle: handle, set: true}, nil
}

func restoreProcessStderr(saved processStderrState) error {
	if !saved.set {
		return fmt.Errorf("stderr was not saved")
	}
	os.Stderr = saved.file
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, saved.handle); err != nil {
		return fmt.Errorf("restore stderr handle: %w", err)
	}
	return nil
}
