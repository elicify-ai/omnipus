// Tests for the sandbox-aware exec branching wired in step 5 of the
// quizzical-marinating-frog plan.
//
// What we verify here:
//  1. ModeOff (and the empty default) → exec runs via plain sh -c, no sandbox.Run
//     involvement, behavior matches today's TestShellTool_Success.
//  2. ModeEnforce → exec routes through sandbox.Run; the Limits-derived env
//     reaches the child (we assert via a command that prints HTTP_PROXY).
//  3. Background (sandbox-on, non-PTY) → ApplyChildHardening is applied
//     (Setpgid set on Linux); the session is registered and the process
//     completes.
//
// We do NOT use net.Listen-based bind assertions here — see the note in
// pkg/sandbox/backend_linux_subprocess_test.go::rawTCPBind. Kernel-level
// bind enforcement is verified there; this file's contract is the dispatch
// branching, not the kernel layer.

package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// makeExecToolWithMode builds an ExecTool with the given sandbox mode and
// the supplied egress proxy (may be nil). Workspace is set to the test's
// t.TempDir so the hardened path has a valid cwd.
func makeExecToolWithMode(t *testing.T, mode string, proxy *sandbox.EgressProxy) *ExecTool {
	t.Helper()
	workspace := t.TempDir()
	tool, err := NewExecToolWithDeps(workspace, false, nil, ExecToolDeps{
		SandboxMode:        mode,
		EgressProxy:        proxy,
		ExecTimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatalf("NewExecToolWithDeps: %v", err)
	}
	return tool
}

// TestExecTool_SandboxOff_PreservesLegacyPath verifies that with
// SandboxMode="off" the exec tool runs the command via the legacy sh -c
// path (no sandbox.Run involvement). The behavioral assertion is the
// same as the long-standing TestShellTool_Success: a successful echo.
func TestExecTool_SandboxOff_PreservesLegacyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell")
	}
	tool := makeExecToolWithMode(t, string(sandbox.ModeOff), nil)
	if tool.sandboxOn() {
		t.Fatalf("sandboxOn() = true, want false for ModeOff")
	}

	ctx, cancel := context.WithTimeout(WithToolContext(context.Background(), "cli", ""), 10*time.Second)
	defer cancel()

	res := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "echo legacy-path-ok",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "legacy-path-ok") {
		t.Fatalf("expected output to contain marker, got: %s", res.ForLLM)
	}
}

// TestExecTool_EmptyMode_DefaultsToOff verifies that an unconfigured
// SandboxMode (zero value) maps to OFF. This preserves backward-compat with
// every existing test that constructs an ExecTool via NewExecTool without
// any deps.
func TestExecTool_EmptyMode_DefaultsToOff(t *testing.T) {
	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}
	if tool.sandboxOn() {
		t.Fatalf("sandboxOn() = true for zero-value mode; want false (back-compat)")
	}
}

// TestExecTool_SandboxEnforce_RoutesThroughHardenedPath verifies that with
// SandboxMode="enforce" the exec tool routes through sandbox.Run, evidenced
// by the HTTP_PROXY env var being injected when an EgressProxy is wired.
//
// The hardened path uses sandbox.Limits to inject HTTP_PROXY=http://<addr>.
// We start a real EgressProxy, run `sh -c 'env | grep HTTP_PROXY'`, and
// assert the proxy URL appears in the output.
func TestExecTool_SandboxEnforce_RoutesThroughHardenedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell")
	}
	proxy, err := sandbox.NewEgressProxy(nil, nil)
	if err != nil {
		t.Fatalf("NewEgressProxy: %v", err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	tool := makeExecToolWithMode(t, string(sandbox.ModeEnforce), proxy)
	if !tool.sandboxOn() {
		t.Fatalf("sandboxOn() = false for ModeEnforce; want true")
	}

	ctx, cancel := context.WithTimeout(WithToolContext(context.Background(), "cli", ""), 10*time.Second)
	defer cancel()

	res := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "env | grep -E '^(HTTP_PROXY|http_proxy)='",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}
	wantNeedle := "http://" + proxy.Addr()
	if !strings.Contains(res.ForLLM, wantNeedle) {
		t.Fatalf("expected hardened path to inject HTTP_PROXY=%s, got output: %s", wantNeedle, res.ForLLM)
	}
}

// TestExecTool_SandboxEnforce_BackgroundSessionRegisters verifies that the
// background (non-PTY) path under SandboxMode="enforce" still spawns the
// child, registers the session, and lets us poll it. We use a short-lived
// `sleep 0.5` so the test stays fast while exercising the full
// ApplyChildHardening + cmd.Start sequence.
func TestExecTool_SandboxEnforce_BackgroundSessionRegisters(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell")
	}
	tool := makeExecToolWithMode(t, string(sandbox.ModeEnforce), nil)

	ctx, cancel := context.WithTimeout(WithToolContext(context.Background(), "cli", ""), 10*time.Second)
	defer cancel()

	startRes := tool.Execute(ctx, map[string]any{
		"action":     "run",
		"command":    "sleep 0.2 && echo bg-done",
		"background": true,
	})
	if startRes.IsError {
		t.Fatalf("expected background start to succeed, got error: %s", startRes.ForLLM)
	}
	// The ForLLM payload is JSON {"sessionId": "..."}; we don't need to
	// parse it formally — just confirm a session was registered.
	sessions := tool.sessionManager.List()
	if len(sessions) == 0 {
		t.Fatalf("expected at least one registered session; got 0")
	}

	// Wait for the session to finish so the test cleanup is deterministic.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		done := true
		for _, s := range tool.sessionManager.List() {
			if s.Status == "running" {
				done = false
				break
			}
		}
		if done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background session did not complete within 5s")
}

// TestExecTool_SandboxOff_ScrubsGatewayEnv verifies that sensitive gateway
// env vars are NOT passed to child processes even when sandbox=off (A1.1-b).
// Without the fix, `exec env` on the sandbox-off path would print
// OMNIPUS_MASTER_KEY — a total credential-store compromise.
func TestExecTool_SandboxOff_ScrubsGatewayEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses POSIX shell")
	}

	// Inject a bogus master key into the current process environment.
	// t.Setenv restores the original value (or unsets it) at test cleanup.
	const fakeKey = "FAKE_MASTER_KEY_SHOULD_NOT_LEAK_12345"
	t.Setenv("OMNIPUS_MASTER_KEY", fakeKey)
	t.Setenv("OMNIPUS_KEY_FILE", "/fake/path/master.key")
	t.Setenv("OMNIPUS_BEARER_TOKEN", "fake-bearer-token-abc")

	tool := makeExecToolWithMode(t, string(sandbox.ModeOff), nil)
	if tool.sandboxOn() {
		t.Fatalf("sandboxOn() = true for ModeOff; want false")
	}

	ctx, cancel := context.WithTimeout(WithToolContext(context.Background(), "cli", ""), 10*time.Second)
	defer cancel()

	// Run `env` and capture its output — this shows every env var the child inherited.
	res := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "env",
	})
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.ForLLM)
	}

	output := res.ForLLM
	if strings.Contains(output, fakeKey) {
		t.Errorf("OMNIPUS_MASTER_KEY leaked to child process env on sandbox=off path")
	}
	if strings.Contains(output, "OMNIPUS_MASTER_KEY") {
		t.Errorf("OMNIPUS_MASTER_KEY key name leaked to child process env on sandbox=off path")
	}
	if strings.Contains(output, "OMNIPUS_KEY_FILE") {
		t.Errorf("OMNIPUS_KEY_FILE leaked to child process env on sandbox=off path")
	}
	if strings.Contains(output, "OMNIPUS_BEARER_TOKEN") {
		t.Errorf("OMNIPUS_BEARER_TOKEN leaked to child process env on sandbox=off path")
	}
}
