package mcp

// manager_connect_lifecycle_test.go — regression coverage for ConnectServer's
// stdio child process lifetime and the shared env_file resolution helper.
//
// The stdio child's os/exec lifetime must be independent of the ctx passed to
// ConnectServer: that ctx bounds only the handshake. A per-connect timeout
// ctx (the shape reconciliation uses) is routinely canceled via a deferred
// cancel() moments after a successful connect — if the child were spawned
// with exec.CommandContext(ctx, ...), that cancellation would SIGKILL a
// subprocess that just finished a successful handshake.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestConnectServer_ChildSurvivesConnectCtxCancellation is the direct
// regression test for the stdio-child-lifetime fix: connect with a
// short-lived ctx, let it expire (mirroring reconciliation's per-connect
// timeout + deferred cancel right after a successful connect), then prove
// the child is still alive and answering by calling a real tool through it.
//
// BDD:
//
//	Given a stub stdio MCP server connected via ConnectServer with a
//	  short-lived ctx
//	When  that ctx is canceled shortly after ConnectServer returns
//	Then  the server remains connected and CallTool still succeeds
func TestConnectServer_ChildSurvivesConnectCtxCancellation(t *testing.T) {
	binPath := buildStubServer(t)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	cfg := config.MCPServerConfig{
		Enabled: true,
		Command: binPath,
		Args:    []string{},
	}

	// Mirrors ReconcileMCP's per-server connect pattern: a short-lived ctx
	// whose cancel fires (via defer) right after a successful connect.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := mgr.ConnectServer(ctx, "stub-survives", cfg); err != nil {
		cancel()
		t.Fatalf("ConnectServer: %v", err)
	}
	cancel()

	// Give the (already-fired) cancellation a moment to reach the child if
	// the regression were still present — exec.CommandContext delivers
	// SIGKILL near-instantly on ctx cancellation.
	time.Sleep(300 * time.Millisecond)

	if _, ok := mgr.GetServer("stub-survives"); !ok {
		t.Fatal("GetServer: server no longer connected after connect-ctx cancellation")
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	result, err := mgr.CallTool(callCtx, "stub-survives", "stub.echo", map[string]any{"message": "still-alive"})
	if err != nil {
		t.Fatalf("CallTool after connect-ctx cancellation: %v (child was killed when the connect ctx ended)", err)
	}
	if result == nil {
		t.Fatal("CallTool returned nil result")
	}
	if result.IsError {
		t.Fatalf("CallTool result reports an error: %+v", result.Content)
	}
	text, ok := firstTextContent(result)
	if !ok || text != "still-alive" {
		t.Fatalf("CallTool result content = %q, want echoed %q", text, "still-alive")
	}
}

// firstTextContent extracts the text of the first *mcp.TextContent block in
// a CallToolResult, for asserting the stub echoed back what was sent (proof
// the child is not just alive but actually processing requests correctly).
func firstTextContent(result *sdkmcp.CallToolResult) (string, bool) {
	for _, c := range result.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text, true
		}
	}
	return "", false
}

// buildHangServer compiles pkg/mcp/testdata/hang_stdio_server and returns
// the path to the resulting binary. Mirrors buildStubServer's on-demand
// build pattern (same test package, same testdata layout); cleaned up via
// t.TempDir().
func buildHangServer(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("buildHangServer: getwd: %v", err)
	}
	srcDir := filepath.Join(cwd, "testdata", "hang_stdio_server")
	if _, err := os.Stat(filepath.Join(srcDir, "main.go")); err != nil {
		t.Fatalf("buildHangServer: source not found at %s: %v", srcDir, err)
	}

	outDir := t.TempDir()
	binPath := filepath.Join(outDir, "hang_stdio_server")

	cmd := exec.Command("go", "build", "-o", binPath, srcDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Fatalf("buildHangServer: go build failed:\n%s\nerr: %v", out, buildErr)
	}

	return binPath
}

// TestConnectServer_HandshakeFailureReapsChild proves the other half of the
// stdio-child-lifetime fix: removing exec.CommandContext must not leak the
// spawned child when the handshake itself fails (short ctx, unresponsive
// "server"). The SDK's
// Client.Connect closes the ClientSession on every post-spawn failure
// branch, which drives sandboxedStdioConn.Close's reap; this test proves
// that chain actually reaps the process via external process-table
// evidence, rather than asserting it by reading the SDK source.
//
// hang_stdio_server (testdata/hang_stdio_server) drains stdin and writes
// nothing back, ever — no MCP handshake response is possible, so the
// client's initialize call blocks for the full ctx deadline. This avoids an
// earlier "cat -" version's race: cat echoes the raw initialize bytes back
// immediately, which the SDK can reject fast enough to reap the child before
// a polling loop's first tick observes it. It also avoids shell "hang
// forever" one-liners, whose tail-call exec optimization can silently drop
// an identifying argv marker. binPath is unique per test run (t.TempDir()),
// so it doubles as the pgrep -f needle. Skips if pgrep is unavailable.
//
// BDD:
//
//	Given a stdio "server" that never completes the MCP handshake
//	When  ConnectServer is called with a short-lived ctx
//	Then  ConnectServer returns an error
//	And   the process-table entry for the spawned child is gone shortly after
func TestConnectServer_HandshakeFailureReapsChild(t *testing.T) {
	requireExecutable(t, "pgrep")
	binPath := buildHangServer(t)

	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	cfg := config.MCPServerConfig{
		Enabled: true,
		Command: binPath,
		Args:    []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	connectErr := make(chan error, 1)
	go func() { connectErr <- mgr.ConnectServer(ctx, "hang-server", cfg) }()

	// Sanity check: the child must actually be observable while ConnectServer
	// is still blocked on the handshake — otherwise a later "not found" would
	// prove nothing (the process may simply never have existed on this host).
	if !waitForProcess(binPath, true, 1800*time.Millisecond) {
		t.Fatal("spawned child was never observed via pgrep — cannot verify the no-leak claim")
	}

	if err := <-connectErr; err == nil {
		t.Fatal("ConnectServer against a non-responding stdio server = nil error, want handshake failure")
	}

	if servers := mgr.GetServers(); len(servers) != 0 {
		t.Errorf("GetServers() after handshake failure = %d entries, want 0", len(servers))
	}

	// Give the reaper's Wait -> SIGTERM -> SIGKILL escalation room to finish.
	if !waitForProcess(binPath, false, 8*time.Second) {
		t.Fatalf("child process %q still alive after handshake failure — leaked subprocess", binPath)
	}
}

// requireExecutable skips the test when name is not on PATH, so the leak
// test degrades to "skipped" rather than "flaky failure" on a minimal image.
func requireExecutable(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not available on PATH, skipping: %v", name, err)
	}
}

// waitForProcess polls `pgrep -f needle` until it reports the wanted
// presence state or timeout elapses, returning whether that state was
// observed.
func waitForProcess(needle string, wantPresent bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if pgrepFinds(needle) == wantPresent {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// pgrepFinds reports whether any live process's command line contains
// needle, via `pgrep -f`.
func pgrepFinds(needle string) bool {
	out, err := exec.Command("pgrep", "-f", needle).Output()
	if err != nil {
		return false // pgrep exits 1 on no match; treat any error as "not found"
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// TestResolveServerEnvFile covers the four cases ResolveServerEnvFile's
// callers (pkg/agent reconciliation's desired-build, the gateway's MCP test
// endpoint) rely on: absolute/empty EnvFile pass through untouched, a
// relative EnvFile joins against a non-empty workspacePath, and a relative
// EnvFile with an empty workspacePath is reported as an error rather than
// silently left unresolved.
//
// BDD:
//
//	Given an MCPServerConfig and a workspacePath
//	When  ResolveServerEnvFile is called
//	Then  EnvFile is resolved (or left alone) exactly as documented, other
//	  fields are untouched, and the original cfg argument is never mutated
func TestResolveServerEnvFile(t *testing.T) {
	base := config.MCPServerConfig{
		Enabled: true,
		Command: "some-command",
		Args:    []string{"--flag"},
		Env:     map[string]string{"K": "V"},
	}

	t.Run("empty EnvFile passes through unchanged", func(t *testing.T) {
		cfg := base
		cfg.EnvFile = ""
		got, err := ResolveServerEnvFile(cfg, "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.EnvFile != "" {
			t.Errorf("EnvFile = %q, want empty", got.EnvFile)
		}
		if got.Command != base.Command || got.Args[0] != base.Args[0] {
			t.Errorf("other fields mutated: got %+v", got)
		}
	})

	t.Run("absolute EnvFile passes through unchanged", func(t *testing.T) {
		cfg := base
		cfg.EnvFile = "/abs/.env"
		got, err := ResolveServerEnvFile(cfg, "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.EnvFile != "/abs/.env" {
			t.Errorf("EnvFile = %q, want unchanged /abs/.env", got.EnvFile)
		}
	})

	t.Run("relative EnvFile joins against workspacePath", func(t *testing.T) {
		cfg := base
		cfg.EnvFile = ".env"
		got, err := ResolveServerEnvFile(cfg, "/workspace")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "/workspace/.env"
		if got.EnvFile != want {
			t.Errorf("EnvFile = %q, want %q", got.EnvFile, want)
		}
		// Original cfg (caller's copy) must not be mutated.
		if cfg.EnvFile != ".env" {
			t.Errorf("caller's cfg.EnvFile mutated to %q", cfg.EnvFile)
		}
	})

	t.Run("relative EnvFile with empty workspacePath errors", func(t *testing.T) {
		cfg := base
		cfg.EnvFile = "sub/.env"
		_, err := ResolveServerEnvFile(cfg, "")
		if err == nil {
			t.Fatal("expected error for relative env_file with empty workspace path, got nil")
		}
		if !strings.Contains(err.Error(), "workspace path is empty") {
			t.Errorf("error = %q, want it to mention the empty workspace path", err.Error())
		}
	})
}

// TestResolveServerEnvRefs covers ResolveServerEnvRefs, the credential-store
// counterpart to ResolveServerEnvFile: add_mcp_server (pkg/sysagent/tools/
// mcp.go) stores env secrets in the credential store and writes only refs
// into config.MCPServerConfig.EnvRefs, and this is where those refs get
// turned back into real values at connect time.
//
// BDD:
//
//	Given an MCPServerConfig with EnvRefs and a credential resolver
//	When  ResolveServerEnvRefs is called
//	Then  each ref is resolved into Env, an EnvRefs value overrides a
//	  same-named literal Env value, a server with no EnvRefs is an untouched
//	  no-op even with a nil resolver (back-compat for literal-Env servers
//	  added before this mechanism, or via the gateway REST API), and a
//	  resolver failure (locked store, missing ref, nil resolver with
//	  non-empty EnvRefs) is reported as an error rather than silently
//	  connecting with a missing secret.
func TestResolveServerEnvRefs(t *testing.T) {
	t.Run("no EnvRefs is a no-op even with a nil resolver (back-compat)", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			Enabled: true,
			Command: "some-command",
			Env:     map[string]string{"LITERAL": "unchanged"},
		}
		got, err := ResolveServerEnvRefs(cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Env["LITERAL"] != "unchanged" {
			t.Errorf("literal Env must survive untouched, got: %+v", got.Env)
		}
	})

	t.Run("non-empty EnvRefs with a nil resolver errors", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			EnvRefs: map[string]string{"TOKEN": "mcp_srv_TOKEN"},
		}
		_, err := ResolveServerEnvRefs(cfg, nil)
		if err == nil {
			t.Fatal("expected error when EnvRefs is non-empty and resolver is nil, got nil")
		}
	})

	t.Run("resolves refs into Env, overriding a same-named literal", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			Env: map[string]string{
				"KEEP":     "literal-value",
				"OVERRIDE": "stale-literal",
			},
			EnvRefs: map[string]string{
				"OVERRIDE": "mcp_srv_OVERRIDE",
				"SECRET":   "mcp_srv_SECRET",
			},
		}
		store := map[string]string{
			"mcp_srv_OVERRIDE": "fresh-secret",
			"mcp_srv_SECRET":   "another-secret",
		}
		resolve := func(refKey string) (string, error) {
			v, ok := store[refKey]
			if !ok {
				return "", fmt.Errorf("no such credential %q", refKey)
			}
			return v, nil
		}
		got, err := ResolveServerEnvRefs(cfg, resolve)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Env["KEEP"] != "literal-value" {
			t.Errorf("KEEP = %q, want literal-value untouched", got.Env["KEEP"])
		}
		if got.Env["OVERRIDE"] != "fresh-secret" {
			t.Errorf("OVERRIDE = %q, want the ref to win over the stale literal", got.Env["OVERRIDE"])
		}
		if got.Env["SECRET"] != "another-secret" {
			t.Errorf("SECRET = %q, want the resolved ref value", got.Env["SECRET"])
		}
		// The original cfg (caller's copy) must not be mutated — the merge
		// happens on a fresh map.
		if cfg.Env["OVERRIDE"] != "stale-literal" {
			t.Errorf("caller's cfg.Env mutated: %+v", cfg.Env)
		}
	})

	t.Run("a resolver failure is surfaced, not silently dropped", func(t *testing.T) {
		cfg := config.MCPServerConfig{
			EnvRefs: map[string]string{"MISSING": "mcp_srv_MISSING"},
		}
		resolve := func(string) (string, error) { return "", fmt.Errorf("credential store is locked") }
		_, err := ResolveServerEnvRefs(cfg, resolve)
		if err == nil {
			t.Fatal("expected the resolver's error to propagate, got nil")
		}
		if !strings.Contains(err.Error(), "credential store is locked") {
			t.Errorf("error = %q, want it to wrap the resolver's error", err.Error())
		}
	})
}
