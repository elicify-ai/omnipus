//go:build !windows

package testutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
			if restoreErr := restoreProcessStderr(saved); restoreErr != nil {
				t.Errorf("emergency stderr restore: %v", restoreErr)
			}
		}
	})

	panicLog := filepath.Join(t.TempDir(), "panic.log")
	cleanup, err := logger.InitPanic(panicLog)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if _, werr := os.Stderr.WriteString("HIDDEN_MARKER\n"); werr != nil {
		t.Fatal(werr)
	}
	if rerr := restoreProcessStderr(saved); rerr != nil {
		t.Fatal(rerr)
	}
	restored = true
	if _, werr := os.Stderr.WriteString("VISIBLE_MARKER\n"); werr != nil {
		t.Fatal(werr)
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
		if restoreErr := restoreProcessStderr(saved); restoreErr != nil {
			t.Errorf("emergency stderr restore: %v", restoreErr)
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
	if verdict := stderrRestoreChildVerdict(out, err); verdict != nil {
		t.Fatalf("%v\n%s", verdict, out)
	}
}

func stderrRestoreChild(t *testing.T) {
	t.Helper()
	armStderrRestoreProbe(t)
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

func armStderrRestoreProbe(t *testing.T) {
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
	// the pipe back. The parent reads the marker the cleanup prints.
	t.Cleanup(func() { reportRestoredMarker(readEnd) })
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

// stderrRestoreCleanupFaults are diagnostics a restore probe must not hide
// behind MARKER_OK. The marker only proves one write reached the pipe.
var stderrRestoreCleanupFaults = []string{
	"cannot restore stderr:",
	"ORIG_RESTORE_ERR",
	"ORIG_CLOSE_ERR",
	"READ_CLOSE_ERR",
	"MARKER_WRITE_ERR",
	"MARKER_DEADLINE_ERR",
	"MARKER_MISSING",
}

// stderrRestoreChildVerdict is the decision TestStartTestGateway_CleanupRestoresStderr
// uses. A nonzero exit plus MARKER_OK is not enough: a cleanup error printed
// beside the marker is still a failed restore.
func stderrRestoreChildVerdict(out string, childErr error) error {
	if childErr == nil {
		return errors.New("child exited 0; the refusing runner must fail boot")
	}
	if !strings.Contains(out, "boot refused after redirect") {
		return errors.New("missing boot-refusal diagnostic")
	}
	for _, bad := range stderrRestoreCleanupFaults {
		if strings.Contains(out, bad) {
			return fmt.Errorf("unexpected cleanup diagnostic %q", bad)
		}
	}
	if !strings.Contains(out, "MARKER_OK") {
		return errors.New("stderr was not restored: MARKER_OK missing")
	}
	return nil
}

func TestStderrRestoreChildVerdict_RejectsCleanupErrorBesideTheMarker(t *testing.T) {
	out := "failed to boot: boot refused after redirect\nMARKER_OK\ncannot restore stderr: dup2 stderr: broken pipe\n"
	err := stderrRestoreChildVerdict(out, errors.New("exit status 1"))
	if err == nil {
		t.Fatal("verdict accepted MARKER_OK beside a cleanup error")
	}
	if !strings.Contains(err.Error(), "cannot restore stderr:") {
		t.Fatalf("verdict %v does not name the cleanup error", err)
	}
}

func TestStderrRestoreChildVerdict_RejectsACleanExitAndADifferentFailure(t *testing.T) {
	refused := "failed to boot: boot refused after redirect\nMARKER_OK\n"
	if err := stderrRestoreChildVerdict(refused, nil); err == nil {
		t.Fatal("verdict accepted a zero exit")
	}
	if err := stderrRestoreChildVerdict("MARKER_OK\nsome other failure\n", errors.New("exit status 1")); err == nil {
		t.Fatal("verdict accepted a failure other than the boot refusal")
	}
	if err := stderrRestoreChildVerdict("boot refused after redirect\nMARKER_MISSING\n", errors.New("exit status 1")); err == nil {
		t.Fatal("verdict accepted a missing marker")
	}
}

func TestStderrRestoreChildVerdict_AcceptsRefusedBootWithOnlyTheMarker(t *testing.T) {
	out := "failed to boot: boot refused after redirect\nMARKER_OK\n"
	if err := stderrRestoreChildVerdict(out, errors.New("exit status 1")); err != nil {
		t.Fatal(err)
	}
}

// Removing the restore cleanup from the successful-return path of
// StartTestGateway must fail this test. The refusing-boot probe never
// registers Close, so it cannot see a restore that only the success path lost.
func TestStartTestGateway_SuccessfulBootRestoresStderr(t *testing.T) {
	if os.Getenv("OMNIPUS_STDERR_CHILD") == "restore-ok" {
		stderrSuccessChild(t)
		return
	}
	out, err := runStderrChild(t, "TestStartTestGateway_SuccessfulBootRestoresStderr", "restore-ok")
	if err != nil {
		t.Fatalf("successful boot did not finish cleanly\n%s", out)
	}
	if !strings.Contains(out, "MARKER_OK") {
		t.Fatalf("stderr was not restored after a successful boot\n%s", out)
	}
	for _, bad := range stderrRestoreCleanupFaults {
		if strings.Contains(out, bad) {
			t.Fatalf("cleanup diagnostic %s after a successful boot\n%s", bad, out)
		}
	}
}

func stderrSuccessChild(t *testing.T) {
	t.Helper()
	armStderrRestoreProbe(t)
	RegisterGatewayRunner(stderrSuccessRunner)
	StartTestGateway(t)
}

// stderrSuccessRunner serves /health and writes gateway.port, then shuts down
// when the harness cancels, reporting any serve or cleanup error. InitPanic
// steals stderr the way a real boot does; the marker reaches the pipe only if
// StartTestGateway's cleanup puts it back.
func stderrSuccessRunner(ctx context.Context, _ bool, home, configPath string, _ bool) (result error) {
	cleanup, err := logger.InitPanic(filepath.Join(home, "panic.log"))
	if err != nil {
		return err
	}
	defer cleanup()
	port, err := gatewayPortFromConfig(configPath)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: time.Second,
	}
	serveDone := make(chan struct{})
	var serveErr error
	go func() {
		defer close(serveDone)
		serveErr = srv.Serve(ln)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		} else if serveErr != nil {
			serveErr = fmt.Errorf("serve stderr-probe server: %w", serveErr)
		}
	}()
	defer func() {
		closeErr := srv.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close stderr-probe server: %w", closeErr)
		}
		<-serveDone
		result = errors.Join(result, closeErr, serveErr)
	}()
	if err := os.WriteFile(filepath.Join(home, gatewayPortFileName), []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write stderr-probe port: %w", err)
	}
	select {
	case <-serveDone:
		return errors.New("stderr-probe server stopped before cancellation")
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown stderr-probe server: %w", err)
	}
	return nil
}

func gatewayPortFromConfig(configPath string) (int, error) {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return 0, err
	}
	var cfg struct {
		Gateway struct {
			Port int `json:"port"`
		} `json:"gateway"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return 0, err
	}
	if cfg.Gateway.Port == 0 {
		return 0, errors.New("config has no gateway.port")
	}
	return cfg.Gateway.Port, nil
}
