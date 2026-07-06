package mcp

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestSandboxedCommandTransport_SpawnsAndReaps verifies the ADR-019 spawn path:
// sandboxedCommandTransport spawns a real subprocess via sandbox.StartLocked
// (inheriting the gateway Landlock domain on Linux), and Close reaps the
// process without leaving a zombie or hanging.
//
// On Linux, StartLocked re-applies the Landlock domain to a fresh OS thread
// before forking; on non-Linux / fallback sandbox it is a thin wrapper around
// cmd.Start(). This test guards the wiring (pipes + StartLocked + reap), not
// the kernel-level inheritance (covered by pkg/sandbox tests).
func TestSandboxedCommandTransport_SpawnsAndReaps(t *testing.T) {
	cmd := exec.Command("cat")
	tr := newSandboxedCommandTransport(cmd)
	tr.terminateDuration = 2 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := tr.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn == nil {
		t.Fatal("Connect returned nil connection")
	}
	if tr.cmd.Process == nil {
		t.Fatal("process not started — StartLocked did not spawn the child")
	}

	// Closing must reap the process within the terminate duration. `cat`
	// exits cleanly on stdin EOF (which Close triggers by closing the write
	// pipe), so reap should return promptly. If reap is broken, Close hangs
	// and the test times out.
	done := make(chan error, 1)
	go func() { done <- conn.Close() }()
	select {
	case <-done:
		// Success — cat exited cleanly.
	case <-time.After(5 * time.Second):
		_ = tr.cmd.Process.Kill()
		t.Fatal("Close hung — process reaping is broken")
	}

	if tr.cmd.ProcessState == nil || !tr.cmd.ProcessState.Exited() {
		t.Fatal("process did not exit after Close")
	}
}

// TestSandboxedCommandTransport_GracefulDegradation confirms the transport
// spawns and reaps on non-Linux platforms (or when sandbox is off), where
// StartLocked is a thin wrapper around cmd.Start().
func TestSandboxedCommandTransport_GracefulDegradation(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("graceful-degradation path is exercised on non-Linux; on Linux StartLocked re-applies Landlock")
	}
	cmd := exec.Command("cat")
	tr := newSandboxedCommandTransport(cmd)
	tr.terminateDuration = 1 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := tr.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect on non-Linux: %v", err)
	}
	if err := conn.Close(); err != nil && !strings.Contains(err.Error(), "unresponsive") {
		t.Logf("Close: %v", err)
	}
}

// TestNewSandboxedCommandTransport_Defaults confirms the constructor wires the
// cmd and applies the default 5s terminate duration.
func TestNewSandboxedCommandTransport_Defaults(t *testing.T) {
	cmd := exec.Command("cat")
	tr := newSandboxedCommandTransport(cmd)
	if tr.cmd != cmd {
		t.Fatal("cmd not stored on transport")
	}
	if tr.terminateDuration != 5*time.Second {
		t.Fatalf("expected default terminateDuration 5s, got %v", tr.terminateDuration)
	}
}

// TestSandboxedStdioConn_Reap_NilProcess confirms reap is a no-op when no
// process is attached (defensive — prevents a nil-pointer panic if Connect
// fails partway through spawn).
func TestSandboxedStdioConn_Reap_NilProcess(t *testing.T) {
	s := &sandboxedStdioConn{terminateDuration: 100 * time.Millisecond}
	if err := s.reap(); err != nil {
		t.Fatalf("reap with nil process should be no-op, got: %v", err)
	}
}

// TestSandboxedCommandTransport_CatEcho verifies the stdio pipes are correctly
// wired by spawning `cat` through StartLocked, writing to stdin, and reading
// the echo back from stdout. This catches pipe-misconfiguration regressions
// that the reap-only test would not. We call sandbox.StartLocked directly
// (instead of through the transport) because the transport's Connect consumes
// the pipes via the MCP SDK — this raw check isolates the pipe wiring from the
// SDK framing.
func TestSandboxedCommandTransport_CatEcho(t *testing.T) {
	cmd := exec.Command("cat")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	// StartLocked spawns the child on a Landlock-restricted OS thread on
	// Linux; thin wrapper around cmd.Start() elsewhere.
	if err = sandbox.StartLocked(cmd); err != nil {
		t.Fatalf("StartLocked: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	want := []byte("hello-adr-019\n")
	if _, werr := stdin.Write(want); werr != nil {
		t.Fatalf("stdin write: %v", werr)
	}
	_ = stdin.Close()

	got, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatalf("stdout read: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("echo mismatch: wrote %q, read %q", want, got)
	}
}

// TestSandboxedCommandTransport_ImplementsTransport is a compile-time guard:
// if the SDK's Transport interface changes, this fails to compile.
func TestSandboxedCommandTransport_ImplementsTransport(t *testing.T) {
	var _ mcp.Transport = (*sandboxedCommandTransport)(nil)
}
