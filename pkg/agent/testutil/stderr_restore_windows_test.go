//go:build windows

package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"golang.org/x/sys/windows"
)

// Panic logging on Windows replaces both os.Stderr and the process
// standard-error handle. Restore must put both back. This runs only on
// Windows; the cross-compile vet is what this machine can check.
func TestRestoreProcessStderr_PutsBackHandleAndGoFile(t *testing.T) {
	saved, err := saveProcessStderr()
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			if err := restoreProcessStderr(saved); err != nil {
				t.Errorf("emergency stderr restore: %v", err)
			}
		}
	})

	before := os.Stderr
	panicLog := filepath.Join(t.TempDir(), "panic.log")
	cleanup, err := logger.InitPanic(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if os.Stderr == before {
		t.Fatal("InitPanic did not replace os.Stderr")
	}

	if err := restoreProcessStderr(saved); err != nil {
		t.Fatal(err)
	}
	restored = true
	if os.Stderr != before {
		t.Fatal("os.Stderr was not restored")
	}
	handle, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		t.Fatal(err)
	}
	if handle != saved.handle {
		t.Fatalf("stderr handle = %v, want %v", handle, saved.handle)
	}
}
