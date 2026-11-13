//go:build !windows

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// #1823: InitPanic dup2s the panic log over fd 2, so the "Error: ..." line the
// CLI prints for a failed boot (e.g. a listener bind failure) went into
// gateway_panic.log and the operator's terminal showed nothing. BootStderr must
// keep pointing at the stderr the process started with.
func TestBootStderr_SurvivesPanicRedirect_1823(t *testing.T) {
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
	t.Cleanup(func() {
		_ = unix.Dup2(saved, int(os.Stderr.Fd()))
		_ = unix.Close(saved)
		bootStderr.Store(prev)
	})

	cleanup, err := InitPanic(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if !StderrRedirected() {
		t.Error("StderrRedirected must be true after InitPanic redirected stderr")
	}
	if _, err = BootStderr().Write([]byte("BOOT_ERROR_MARKER\n")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stderr.WriteString("PANIC_LOG_MARKER\n"); err != nil {
		t.Fatal(err)
	}

	termData, _ := os.ReadFile(terminal)
	panicData, _ := os.ReadFile(panicLog)
	if !strings.Contains(string(termData), "BOOT_ERROR_MARKER") {
		t.Errorf("BootStderr did not reach the original stderr; terminal=%q", termData)
	}
	if strings.Contains(string(panicData), "BOOT_ERROR_MARKER") {
		t.Errorf("BootStderr wrote into the panic log: %q", panicData)
	}
	if !strings.Contains(string(panicData), "PANIC_LOG_MARKER") {
		t.Errorf("os.Stderr no longer goes to the panic log: %q", panicData)
	}
	if strings.Contains(string(termData), "PANIC_LOG_MARKER") {
		t.Errorf("os.Stderr write leaked to the original stderr: %q", termData)
	}
}
