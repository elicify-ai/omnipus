//go:build !windows

package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"golang.org/x/sys/unix"
)

// The redirect must not outlive the restore. A marker written while fd 2
// points at the panic log lands in that file; a marker written after the
// restore must not.
func TestRestoreProcessStderr_UndoesPanicFileRedirect(t *testing.T) {
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

	panicLog := filepath.Join(t.TempDir(), "panic.log")
	cleanup, err := logger.InitPanic(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if _, err := os.Stderr.WriteString("HIDDEN_MARKER\n"); err != nil {
		t.Fatal(err)
	}
	if err := restoreProcessStderr(saved); err != nil {
		t.Fatal(err)
	}
	restored = true
	if _, err := os.Stderr.WriteString("VISIBLE_MARKER\n"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "HIDDEN_MARKER") {
		t.Fatalf("panic log %q does not contain the write made while stderr was redirected", text)
	}
	if strings.Contains(text, "VISIBLE_MARKER") {
		t.Fatalf("panic log still received a write after restore: %q", text)
	}
}

func TestSaveProcessStderr_DuplicateIsCloseOnExec(t *testing.T) {
	saved, err := saveProcessStderr()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restoreProcessStderr(saved); err != nil {
			t.Errorf("emergency stderr restore: %v", err)
		}
	})
	flags, err := unix.FcntlInt(uintptr(saved.fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Fatal("saved stderr descriptor is inheritable: close-on-exec is unset")
	}
}

func TestStartTestGateway_SaveFailureAbortsBeforeBoot(t *testing.T) {
	if os.Getenv("OMNIPUS_STDERR_CHILD") == "save-fail" {
		stderrSaveFailureChild(t)
		return
	}
	out, err := runStderrChild(t, "TestStartTestGateway_SaveFailureAbortsBeforeBoot", "save-fail")
	if err == nil {
		t.Fatalf("stderr save failure did not fail setup\n%s", out)
	}
	if strings.Contains(out, "RUNNER_STARTED") {
		t.Fatalf("gateway runner started after stderr could not be preserved\n%s", out)
	}
	if strings.Contains(out, "SETUP_RETURNED") {
		t.Fatalf("StartTestGateway returned after stderr could not be preserved\n%s", out)
	}
	if !strings.Contains(out, "cannot preserve stderr") {
		t.Fatalf("setup failure did not name the stderr save\n%s", out)
	}
}

func stderrSaveFailureChild(t *testing.T) {
	t.Helper()
	stderrSaver = func() (processStderrState, error) {
		return processStderrState{}, errors.New("dup failed")
	}
	RegisterGatewayRunner(func(context.Context, bool, string, string, bool) error {
		fmt.Fprintln(os.Stdout, "RUNNER_STARTED")
		return errors.New("runner must not be called")
	})
	StartTestGateway(t)
	fmt.Fprintln(os.Stdout, "SETUP_RETURNED")
}

// Removing the restore cleanup in StartTestGateway must fail this test.
// The child redirects stderr, refuses to boot, and only then writes a marker.
// The parent passes only when that marker comes back through the pipe the
// cleanup restored. The child's own test fails on purpose, because boot was
// refused; a subtest failure would also fail this parent, so the check is a
// separate process.
func TestStartTestGateway_CleanupRestoresStderr(t *testing.T) {
	if os.Getenv("OMNIPUS_STDERR_CHILD") == "restore" {
		stderrRestoreChild(t)
		return
	}
	out, err := runStderrChild(t, "TestStartTestGateway_CleanupRestoresStderr", "restore")
	if err == nil {
		t.Fatalf("expected the refusing runner to fail boot\n%s", out)
	}
	if !strings.Contains(out, "MARKER_OK") {
		t.Fatalf("stderr was not restored by StartTestGateway cleanup\n%s", out)
	}
}

func stderrRestoreChild(t *testing.T) {
	t.Helper()
	orig, err := unix.Dup(int(os.Stderr.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if dupErr := unix.Dup2(orig, int(os.Stderr.Fd())); dupErr != nil {
			fmt.Fprintf(os.Stdout, "ORIG_RESTORE_ERR %v\n", dupErr)
		}
		if closeErr := unix.Close(orig); closeErr != nil {
			fmt.Fprintf(os.Stdout, "ORIG_CLOSE_ERR %v\n", closeErr)
		}
	})

	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := readEnd.Close(); closeErr != nil {
			fmt.Fprintf(os.Stdout, "READ_CLOSE_ERR %v\n", closeErr)
		}
	})
	if err := unix.Dup2(int(writeEnd.Fd()), int(os.Stderr.Fd())); err != nil {
		t.Fatal(err)
	}
	if err := writeEnd.Close(); err != nil {
		t.Fatal(err)
	}

	// Registered before StartTestGateway, so it runs after that cleanup puts
	// the pipe back. Boot failure aborts the child; the parent reads this.
	t.Cleanup(func() { reportRestoredMarker(readEnd) })

	RegisterGatewayRunner(func(_ context.Context, _ bool, home, _ string, _ bool) error {
		cleanup, initErr := logger.InitPanic(filepath.Join(home, "panic.log"))
		if initErr != nil {
			return initErr
		}
		defer cleanup()
		return errors.New("boot refused after redirect")
	})
	StartTestGateway(t)
}

func reportRestoredMarker(readEnd *os.File) {
	if _, err := os.Stderr.WriteString("RESTORED_MARKER\n"); err != nil {
		fmt.Fprintf(os.Stdout, "MARKER_WRITE_ERR %v\n", err)
		return
	}
	if err := readEnd.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		fmt.Fprintf(os.Stdout, "MARKER_DEADLINE_ERR %v\n", err)
		return
	}
	buf := make([]byte, 4096)
	n, readErr := readEnd.Read(buf)
	if strings.Contains(string(buf[:n]), "RESTORED_MARKER") {
		fmt.Fprintln(os.Stdout, "MARKER_OK")
		return
	}
	fmt.Fprintf(os.Stdout, "MARKER_MISSING %q %v\n", buf[:n], readErr)
}

func runStderrChild(t *testing.T, name, mode string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0],
		"-test.run=^"+name+"$",
		"-test.count=1",
		"-test.v")
	cmd.Env = append(os.Environ(), "OMNIPUS_STDERR_CHILD="+mode)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
