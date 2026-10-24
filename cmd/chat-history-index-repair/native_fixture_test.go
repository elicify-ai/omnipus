package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/stretchr/testify/require"
)

type repairNativeInstance struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	stderr *os.File
	root   string
	waited bool
}

func repairNativeBinary(t *testing.T) string {
	t.Helper()
	name := "omnipus-native-instance-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-tags", "goolm,stdjson", "-o", out, "./testdata/native-instance")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	body, err := cmd.CombinedOutput()
	require.NoError(t, err, "real native helper build: %s", body)
	resolved, err := filepath.EvalSymlinks(out)
	require.NoError(t, err)
	t.Logf("native fixture build exit=0 GOOS=%s", runtime.GOOS)
	return resolved
}

func repairStartNative(t *testing.T, binary, root string) *repairNativeInstance {
	t.Helper()
	stderr, err := os.CreateTemp(t.TempDir(), "native-stderr-*")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, stderr.Close()) })
	cmd := exec.Command(binary, "--data-root", root)
	cmd.Stderr = stderr // An actual file avoids reading a buffer while exec writes it.
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	output, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	child := &repairNativeInstance{cmd: cmd, input: input, stderr: stderr, root: root}
	t.Cleanup(func() {
		if !child.waited {
			// Cleanup is not a stopped oracle. Kill only our owned child after a
			// failing assertion, then always Wait so a failed capture cannot leak it.
			killErr := cmd.Process.Kill()
			closeErr := input.Close()
			waitErr := cmd.Wait() // Join before any cleanup assertion can abort.
			child.waited = true
			require.True(t, killErr == nil || errors.Is(killErr, os.ErrProcessDone), "owned-child cleanup kill: %v", killErr)
			require.True(t, closeErr == nil || errors.Is(closeErr, os.ErrClosed), "owned-child input close: %v", closeErr)
			if waitErr != nil {
				var exit *exec.ExitError
				require.ErrorAs(t, waitErr, &exit, "cleanup must actually reap the child")
			}
			t.Logf("cleanup reaped owned native child pid=%d", cmd.Process.Pid)
		}
	})
	ready, err := bufio.NewReader(output).ReadString('\n')
	require.NoError(t, err, "actual native PID registration/readiness, not a sleep")
	require.Equal(t, fmt.Sprintf("READY pid=%d\n", cmd.Process.Pid), ready)
	pidBytes, err := os.ReadFile(daemon.PIDPath(root))
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(cmd.Process.Pid), string(pidBytes), "real daemon decimal PID bytes, no newline")
	t.Logf("native fixture READY pid=%d matches actual child; real PID file read", cmd.Process.Pid)
	return child
}

func repairStopNative(t *testing.T, child *repairNativeInstance, action string, wantExit int) {
	t.Helper()
	require.False(t, child.waited, "one actual Wait per owned child")
	_, err := fmt.Fprintln(child.input, action)
	require.NoError(t, err)
	require.NoError(t, child.input.Close())
	err = child.cmd.Wait()
	child.waited = true // Even a wrong exit was really reaped; cleanup must not Wait twice.
	exit := 0
	if err != nil {
		var status *exec.ExitError
		require.ErrorAs(t, err, &status)
		exit = status.ExitCode()
	}
	diagnostic, readErr := os.ReadFile(child.stderr.Name())
	require.NoError(t, readErr)
	require.Equal(t, wantExit, exit, "actual native child Wait exit; stderr=%s", diagnostic)
	t.Logf("native fixture action=%s pid=%d Wait exit=%d (reaped)", action, child.cmd.Process.Pid, exit)
}

// Instrument controls are independent of the currently unimplemented repair CLI.
// L/T22/T25/T30 require both real stopped states before either can be trusted.
func TestHistoryRepairCommand_NativeInstanceFixtureHandshakeAndReap(t *testing.T) {
	binary := repairNativeBinary(t)
	for _, tc := range []struct {
		name, action string
		exit         int
		absent       bool
	}{{"graceful", "stop", 0, true}, {"crash", "crash", 86, false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := repairCanonicalFixture(t)
			child := repairStartNative(t, binary, root)
			registered := repairTree(t, root)
			repairStopNative(t, child, tc.action, tc.exit)
			if tc.absent {
				_, err := os.Stat(daemon.PIDPath(root))
				require.ErrorIs(t, err, os.ErrNotExist)
				delete(registered, "gateway.pid")
				// Only directory metadata can change when the real PID is removed.
				actual := repairTree(t, root)
				f := actual["."]
				registered["."] = f
				require.Equal(t, registered, actual, "clean helper changes no canonical file except its own PID removal")
			} else {
				require.Equal(t, registered, repairTree(t, root), "unclean exit really retains the captured PID bytes/mode/mtime")
			}
		})
	}
}
