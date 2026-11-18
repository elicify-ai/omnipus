//go:build !windows

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// #1823 review: when the original stderr cannot be preserved (descriptor
// exhaustion) InitPanic must refuse to start and leave fd 2 alone: redirecting
// would hide the boot error with no way to show it, and skipping the redirect
// silently would let full error text reach the terminal.
func TestInitPanic_DupFailureKeepsStderr_1823(t *testing.T) {
	dir := t.TempDir()
	terminal := filepath.Join(dir, "terminal.out")
	panicLog := filepath.Join(dir, "panic.log")

	term, err := os.OpenFile(terminal, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	saved, err := unix.Dup(int(os.Stderr.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.Dup2(int(term.Fd()), int(os.Stderr.Fd())); err != nil {
		t.Fatal(err)
	}
	prev := bootStderr.Swap(nil)

	var oldLim unix.Rlimit
	if err = unix.Getrlimit(unix.RLIMIT_NOFILE, &oldLim); err != nil {
		t.Fatal(err)
	}
	var hogs []*os.File
	t.Cleanup(func() {
		_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &oldLim)
		for _, f := range hogs {
			_ = f.Close()
		}
		_ = unix.Dup2(saved, int(os.Stderr.Fd()))
		_ = unix.Close(saved)
		bootStderr.Store(prev)
	})

	// Find the lowest free descriptor, cap the limit just above it, use every
	// remaining slot, then free exactly one for the panic log itself.
	probe, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	lowest := probe.Fd()
	_ = probe.Close()
	lim := unix.Rlimit{Cur: uint64(lowest) + 2, Max: oldLim.Max}
	if err = unix.Setrlimit(unix.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	for {
		f, openErr := os.Open(os.DevNull)
		if openErr != nil {
			break
		}
		hogs = append(hogs, f)
	}
	if len(hogs) == 0 {
		t.Fatal("SETUP: could not occupy any descriptor")
	}
	_ = hogs[len(hogs)-1].Close()
	hogs = hogs[:len(hogs)-1]

	cleanup, err := InitPanic(panicLog)
	if err == nil {
		cleanup()
		t.Fatal("InitPanic succeeded although stderr could not be preserved")
	}
	if !strings.Contains(err.Error(), "cannot preserve stderr") {
		t.Errorf("unexpected InitPanic error: %v", err)
	}
	if bootStderr.Load() != nil {
		t.Fatal("SETUP: the duplicate of stderr unexpectedly succeeded")
	}

	if _, err = os.Stderr.WriteString("AFTER_DUP_FAILURE\n"); err != nil {
		t.Fatal(err)
	}
	if err = unix.Setrlimit(unix.RLIMIT_NOFILE, &oldLim); err != nil {
		t.Fatal(err)
	}
	termData, _ := os.ReadFile(terminal)
	panicData, _ := os.ReadFile(panicLog)
	if !strings.Contains(string(termData), "AFTER_DUP_FAILURE") {
		t.Errorf("stderr was redirected although the original could not be kept; terminal=%q panic=%q", termData, panicData)
	}
}
