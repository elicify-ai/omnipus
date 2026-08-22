package sandbox_test

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestSpawnBackgroundChild_BasicSpawn verifies that SpawnBackgroundChild
// starts a simple command and returns a running *exec.Cmd that can be
// waited on successfully.
func TestSpawnBackgroundChild_BasicSpawn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test — Windows does not have 'true'")
	}
	t.Parallel()

	dir := t.TempDir()
	// "true" exits 0 immediately; good for verifying basic spawn + wait.
	cmd, err := sandbox.SpawnBackgroundChild(
		[]string{"true"},
		dir,
		nil, // env
		0,   // port
		sandbox.Limits{WorkspaceDir: dir},
	)
	if err != nil {
		t.Fatalf("SpawnBackgroundChild: unexpected error: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("cmd.Wait: unexpected error: %v", err)
	}
}

// TestSpawnBackgroundChild_EmptyParts verifies that an empty parts slice
// returns an error without starting a process.
func TestSpawnBackgroundChild_EmptyParts(t *testing.T) {
	t.Parallel()

	_, err := sandbox.SpawnBackgroundChild(
		nil,
		t.TempDir(),
		nil,
		0,
		sandbox.Limits{},
	)
	if err == nil {
		t.Fatal("expected error for empty parts, got nil")
	}
}

// TestSpawnBackgroundChild_EnvMerging verifies that proxy + npm-cache env
// vars are injected when the Limits carry non-empty EgressProxyAddr and
// WorkspaceDir. We capture them by running `env` and scanning the output.
func TestSpawnBackgroundChild_EnvMerging(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	proxyAddr := "127.0.0.1:9999"
	lim := sandbox.Limits{
		WorkspaceDir:    dir,
		EgressProxyAddr: proxyAddr,
	}

	// Capture env output by running `env`. We start the process, wait for it,
	// and inspect the combined output via cmd.Output. We use exec.Command
	// directly for the stdout capture; SpawnBackgroundChild doesn't wire
	// stdout. Instead we use a simpler approach: run env via sh and capture
	// via a file written in the workspace.
	envFile := dir + "/env_out.txt"
	cmd, err := sandbox.SpawnBackgroundChild(
		[]string{"sh", "-c", "env > " + envFile},
		dir,
		nil, // extra env
		0,
		lim,
	)
	if err != nil {
		t.Fatalf("SpawnBackgroundChild: unexpected error: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		// sh -c "env > file" exits 0; any non-zero exit is unexpected.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			t.Fatalf("sh -c env: exit %d", ee.ExitCode())
		}
		t.Fatalf("cmd.Wait: %v", err)
	}

	// Read the captured env.
	outBytes, readErr := readFile(t, envFile)
	if readErr != nil {
		t.Fatalf("read env output: %v", readErr)
	}
	out := string(outBytes)

	// Verify proxy variables are present.
	wantProxy := "http://" + proxyAddr
	for _, varName := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if !strings.Contains(out, varName+"="+wantProxy) {
			t.Errorf("expected %s=%s in child env, not found in output", varName, wantProxy)
		}
	}

	// Verify npm_config_cache is set to workspace/.npm-cache.
	wantCache := "npm_config_cache=" + dir + "/.npm-cache"
	if !strings.Contains(out, wantCache) {
		t.Errorf("expected %q in child env, not found", wantCache)
	}
}

// TestSpawnBackgroundChild_PortInjection verifies that PORT=<port> is
// appended when port > 0 and that it takes precedence over any PORT in
// the caller-supplied env.
//
// The redirect path is passed via $ENV_OUT (set in callerEnv) and then
// dereferenced inside the shell as "$ENV_OUT". Previously the path was
// concatenated directly into the command string, which broke when
// t.TempDir() returned a path containing characters sh treats as
// metacharacters (parentheses on macOS Sonoma+, brackets on some
// gh-actions runners) — sh would exit 2 (syntax error) before ever
// running `env`. Quoting the var keeps the redirect literal regardless
// of TMPDIR layout.
func TestSpawnBackgroundChild_PortInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	envFile := dir + "/env_out.txt"
	// Provide a conflicting PORT in the caller env; our injected PORT should win.
	// ENV_OUT carries the output-file path so the shell never sees it as
	// part of its command string — no escaping required for paths with
	// spaces or shell metacharacters.
	callerEnv := []string{"PORT=9999", "ENV_OUT=" + envFile}

	cmd, err := sandbox.SpawnBackgroundChild(
		[]string{"sh", "-c", `env > "$ENV_OUT"`},
		dir,
		callerEnv,
		int32(18000), // injected port
		sandbox.Limits{WorkspaceDir: dir},
	)
	if err != nil {
		t.Fatalf("SpawnBackgroundChild: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		// Surface the dev-server log file the spawn writes its stdout/stderr
		// to, so a future failure has context beyond "exit status N".
		if log, readErr := readFile(t, dir+"/.dev-server.log"); readErr == nil {
			t.Fatalf("cmd.Wait: %v\n--- .dev-server.log ---\n%s", err, log)
		}
		t.Fatalf("cmd.Wait: %v", err)
	}

	outBytes, readErr := readFile(t, envFile)
	if readErr != nil {
		t.Fatalf("readFile(%s): %v", envFile, readErr)
	}
	out := string(outBytes)

	// The last PORT= entry wins under POSIX lookup; verify PORT=18000 is present.
	if !strings.Contains(out, "PORT=18000") {
		t.Errorf("expected PORT=18000 in child env; env output:\n%s", out)
	}
}

// TestSpawnBackgroundChild_GodModeSkipsHardening verifies that when limits
// is the zero value (god mode), SpawnBackgroundChild succeeds without
// attempting platform hardening (which would succeed anyway on most
// platforms with zero Limits, but we verify the code path via a minimal
// command that simply exits 0).
func TestSpawnBackgroundChild_GodModeSkipsHardening(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	cmd, err := sandbox.SpawnBackgroundChild(
		[]string{"true"},
		t.TempDir(),
		nil,
		0,
		sandbox.Limits{}, // zero = god mode
	)
	if err != nil {
		t.Fatalf("SpawnBackgroundChild (god mode): unexpected error: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("cmd.Wait (god mode): unexpected error: %v", err)
	}
}

// TestSpawnBackgroundChild_LogAccumulates verifies B1.4-e regression: successive
// spawns into the same workspace directory append to .dev-server.log rather than
// truncating it. O_TRUNC was removed; this test catches any regression that
// re-introduces it.
func TestSpawnBackgroundChild_LogAccumulates(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}
	t.Parallel()

	dir := t.TempDir()
	logPath := dir + "/.dev-server.log"

	// First spawn: write a sentinel line via the shell. The child's stdout is
	// wired by SpawnBackgroundChild to .dev-server.log.
	cmd1, err := sandbox.SpawnBackgroundChild(
		[]string{"sh", "-c", "echo spawn1"},
		dir, nil, 0,
		sandbox.Limits{WorkspaceDir: dir},
	)
	if err != nil {
		t.Fatalf("spawn1: %v", err)
	}
	if waitErr := cmd1.Wait(); waitErr != nil {
		t.Fatalf("spawn1 Wait: %v", waitErr)
	}

	// Second spawn: write a different sentinel line.
	cmd2, err := sandbox.SpawnBackgroundChild(
		[]string{"sh", "-c", "echo spawn2"},
		dir, nil, 0,
		sandbox.Limits{WorkspaceDir: dir},
	)
	if err != nil {
		t.Fatalf("spawn2: %v", err)
	}
	if err := cmd2.Wait(); err != nil {
		t.Fatalf("spawn2 Wait: %v", err)
	}

	// Both sentinel lines must be present in the log — second spawn must not
	// have truncated spawn1's output.
	logBytes, readErr := readFile(t, logPath)
	if readErr != nil {
		t.Fatalf("read log: %v", readErr)
	}
	logContent := string(logBytes)
	if !strings.Contains(logContent, "spawn1") {
		t.Errorf("log file missing spawn1 output after second spawn; got:\n%s", logContent)
	}
	if !strings.Contains(logContent, "spawn2") {
		t.Errorf("log file missing spawn2 output; got:\n%s", logContent)
	}
}

// readFile is a minimal helper used by tests in this file to read a small
// file written by a child process.
func readFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return exec.Command("cat", path).Output()
}
