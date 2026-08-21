// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// loop_mcp_reconcile_test.go — coverage for ReconcileMCP / MCPServerStatus /
// SetCentralMCPRegistries (pkg/agent/loop_mcp.go): the live MCP reconciliation
// path that replaced the old one-shot ensureMCPInitialized connect-once.
// ReconcileMCP diffs desired (config) vs. live (manager) servers, connects
// added/changed ones and disconnects removed/disabled ones, and — always —
// re-registers every live server's tools into both the per-agent tool
// registries and the central MCPRegistry so a hot-reload's fresh
// AgentRegistry (ReloadProviderAndConfig) never silently drops MCP tools.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/mcp"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// buildStubMCPServer compiles pkg/mcp/testdata/stub_mcp_server — the same
// stdio stub pkg/mcp/stub_mcp_server_test.go uses — and returns the path to
// the resulting binary. Adapted for pkg/agent's working directory: `go test`
// runs with cwd == pkg/agent, so the stub source lives one directory over at
// ../mcp/testdata/stub_mcp_server. The binary is cleaned up via t.TempDir.
func buildStubMCPServer(t *testing.T) string {
	t.Helper()

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("buildStubMCPServer: getwd: %v", err)
	}
	srcDir := filepath.Join(cwd, "..", "mcp", "testdata", "stub_mcp_server")
	if _, statErr := os.Stat(filepath.Join(srcDir, "main.go")); statErr != nil {
		t.Fatalf("buildStubMCPServer: stub source not found at %s: %v", srcDir, statErr)
	}

	outDir := t.TempDir()
	binPath := filepath.Join(outDir, "stub_mcp_server")

	cmd := exec.Command("go", "build", "-o", binPath, srcDir)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	out, buildErr := cmd.CombinedOutput()
	if buildErr != nil {
		t.Fatalf("buildStubMCPServer: go build failed:\n%s\nerr: %v", out, buildErr)
	}
	return binPath
}

// hasToolWithPrefix reports whether any name in names starts with prefix.
func hasToolWithPrefix(names []string, prefix string) bool {
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// countToolsWithPrefix counts how many names in names start with prefix.
func countToolsWithPrefix(names []string, prefix string) int {
	count := 0
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			count++
		}
	}
	return count
}

// toolNamesWithPrefix returns every name in names that starts with prefix.
func toolNamesWithPrefix(names []string, prefix string) []string {
	var out []string
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out
}

// TestReconcileMCP_NoDesiredServers_NoOp verifies the cheap zero-server path:
// no manager, nothing configured → ReconcileMCP marks initialized without
// ever constructing an *mcp.Manager. minimalTestConfig's Tools.MCP.Enabled
// defaults to false (the global kill-switch) and cfg.Tools.MCP.Servers is
// empty, so `desired` is empty either way this test's outcome doesn't turn on
// the kill-switch value — it is exercising the true "nothing configured at
// all" case, distinct from TestReconcileMCP_StubServerLifecycle's disable
// case (Enabled=false with a server still IN the map, which must actively
// disconnect it, not just skip a connect).
func TestReconcileMCP_NoDesiredServers_NoOp(t *testing.T) {
	cfg := minimalTestConfig(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	if err := al.ReconcileMCP(context.Background()); err != nil {
		t.Fatalf("ReconcileMCP returned error with zero desired servers: %v", err)
	}

	al.mcp.mu.Lock()
	initialized := al.mcp.initialized
	mgr := al.mcp.manager
	al.mcp.mu.Unlock()

	if !initialized {
		t.Error("initialized = false, want true")
	}
	if mgr != nil {
		t.Error("manager should stay nil when no MCP servers are configured")
	}
}

// TestReconcileMCP_UnconnectableStdioServer verifies a per-server connect
// failure is recorded, not treated as systemic: ReconcileMCP still returns
// nil and marks initialized=true, but MCPServerStatus reports "error" with
// the failure message for that one server.
func TestReconcileMCP_UnconnectableStdioServer(t *testing.T) {
	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true // global kill-switch: must be on for any server to be in `desired`
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"bad-server": {
			Enabled: true,
			Command: "/nonexistent-binary-xyz",
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP returned a systemic error for a per-server connect failure: %v", err)
	}

	status, toolCount, errMsg := al.MCPServerStatus("bad-server")
	if status != "error" {
		t.Errorf("status = %q, want %q", status, "error")
	}
	if errMsg == "" {
		t.Error("errMsg is empty, want the recorded connect failure")
	}
	if toolCount != 0 {
		t.Errorf("toolCount = %d, want 0 (server never connected)", toolCount)
	}

	al.mcp.mu.Lock()
	initialized := al.mcp.initialized
	al.mcp.mu.Unlock()
	if !initialized {
		t.Error("initialized = false, want true — a per-server failure must not block initialization")
	}
}

// TestMCPServerStatus_Fallthrough exercises all three MCPServerStatus
// branches. The "connected" case is proven against a real manager + a real
// stub subprocess (rather than an assumption about mcp.Manager's internal
// shape), while "error" and "disconnected" exercise the connectErrs/central
// registry plumbing directly.
func TestMCPServerStatus_Fallthrough(t *testing.T) {
	binPath := buildStubMCPServer(t)

	cfg := minimalTestConfig(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	mgr := mcp.NewManager()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connectCfg := config.MCPServerConfig{Enabled: true, Command: binPath}
	if err := mgr.ConnectServer(ctx, "stub-server", connectCfg); err != nil {
		t.Fatalf("ConnectServer: %v", err)
	}
	al.mcp.setManager(mgr)

	t.Run("connected", func(t *testing.T) {
		status, _, errMsg := al.MCPServerStatus("stub-server")
		if status != "connected" {
			t.Errorf("status = %q, want connected", status)
		}
		if errMsg != "" {
			t.Errorf("errMsg = %q, want empty for a connected server", errMsg)
		}
	})

	t.Run("error", func(t *testing.T) {
		al.mcp.setConnectErr("broken-server", errors.New("boom"))
		status, _, errMsg := al.MCPServerStatus("broken-server")
		if status != "error" {
			t.Errorf("status = %q, want error", status)
		}
		if errMsg != "boom" {
			t.Errorf("errMsg = %q, want %q", errMsg, "boom")
		}
	})

	t.Run("disconnected", func(t *testing.T) {
		status, toolCount, errMsg := al.MCPServerStatus("never-configured")
		if status != "disconnected" {
			t.Errorf("status = %q, want disconnected", status)
		}
		if errMsg != "" {
			t.Errorf("errMsg = %q, want empty", errMsg)
		}
		if toolCount != 0 {
			t.Errorf("toolCount = %d, want 0 (no central registry wired)", toolCount)
		}
	})
}

// TestReconcileMCP_StubServerLifecycle drives the full connect → register →
// disable → disconnect/evict cycle against a real stub MCP subprocess,
// proving ReconcileMCP populates BOTH the per-agent tool registries and the
// central MCPRegistry on connect, and cleans up both on disable.
func TestReconcileMCP_StubServerLifecycle(t *testing.T) {
	binPath := buildStubMCPServer(t)

	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true // global kill-switch: must be on for any server to be in `desired`
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"stub-server": {Enabled: true, Command: binPath},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	centralMCP := tools.NewMCPRegistry()
	centralBuiltin := tools.NewBuiltinRegistry()
	al.SetCentralMCPRegistries(centralMCP, centralBuiltin)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (connect): %v", err)
	}

	// The stub server advertises exactly 2 tools (stub.echo, stub.noop) — an
	// exact count catches both under- and over-registration (e.g. a
	// duplicate registration bug that Count() would hide behind a bare
	// "> 0" check).
	const stubToolCount = 2

	status, toolCount, errMsg := al.MCPServerStatus("stub-server")
	if status != "connected" {
		t.Fatalf("status = %q (errMsg=%q), want connected", status, errMsg)
	}
	if toolCount != stubToolCount {
		t.Fatalf("toolCount = %d, want %d after connecting the stub server", toolCount, stubToolCount)
	}
	if got := centralMCP.Count(); got != stubToolCount {
		t.Fatalf("central MCP registry has %d tools after connect, want %d", got, stubToolCount)
	}

	// mia is minimalTestConfig's seeded default agent (coreagent.SeedConfig
	// stamps her as config.Agents.Defaults.DefaultAgentID on fresh install).
	// There is no "main" sentinel to fall back to anymore — every agent
	// checked here is a real, registered, seeded core agent.
	miaAgent, ok := al.GetRegistry().GetAgent("mia")
	if !ok {
		t.Fatal("mia agent not found in registry")
	}
	if got := countToolsWithPrefix(miaAgent.Tools.List(), "mcp_stub-server_"); got != stubToolCount {
		t.Errorf(
			"mia agent has %d mcp_stub-server_* tools, want %d; got %v",
			got,
			stubToolCount,
			miaAgent.Tools.List(),
		)
	}

	// A different core agent (jim) must get the same tools — the register
	// loop iterates ALL agents, not just one.
	jimAgent, ok := al.GetRegistry().GetAgent("jim")
	if !ok {
		t.Fatal("jim agent not found in registry")
	}
	if got := countToolsWithPrefix(jimAgent.Tools.List(), "mcp_stub-server_"); got != stubToolCount {
		t.Errorf("jim agent has %d mcp_stub-server_* tools, want %d; got %v", got, stubToolCount, jimAgent.Tools.List())
	}

	// Disable the server and reconcile again: it must disconnect, and both
	// the per-agent and central registrations must be evicted.
	cfg.Tools.MCP.Servers["stub-server"] = config.MCPServerConfig{Enabled: false, Command: binPath}
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (disable): %v", err)
	}

	status, toolCount, _ = al.MCPServerStatus("stub-server")
	if status != "disconnected" {
		t.Errorf("status after disable = %q, want disconnected", status)
	}
	if toolCount != 0 {
		t.Errorf("toolCount after disable = %d, want 0", toolCount)
	}
	if got := centralMCP.Count(); got != 0 {
		t.Errorf("central MCP registry still has %d tools after disable, want 0", got)
	}
	if hasToolWithPrefix(miaAgent.Tools.List(), "mcp_stub-server_") {
		t.Error("default agent still has a registered mcp_stub-server_* tool after disable")
	}
	if hasToolWithPrefix(jimAgent.Tools.List(), "mcp_stub-server_") {
		t.Error("jim agent still has a registered mcp_stub-server_* tool after disable")
	}

	// Re-enable the server entry itself and reconnect, then flip only the
	// GLOBAL kill-switch off: the still-enabled server entry must not matter
	// — the global flag alone must force a disconnect+evict, same as above.
	cfg.Tools.MCP.Servers["stub-server"] = config.MCPServerConfig{Enabled: true, Command: binPath}
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (reconnect before kill-switch pass): %v", err)
	}
	if status, _, errMsg := al.MCPServerStatus("stub-server"); status != "connected" {
		t.Fatalf("status before kill-switch pass = %q (errMsg=%q), want connected", status, errMsg)
	}

	cfg.Tools.MCP.Enabled = false
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (global kill-switch): %v", err)
	}
	if status, toolCount, _ := al.MCPServerStatus("stub-server"); status != "disconnected" || toolCount != 0 {
		t.Errorf(
			"status/toolCount after global kill-switch = %q/%d, want disconnected/0 (server entry itself is still enabled)",
			status,
			toolCount,
		)
	}
	if got := centralMCP.Count(); got != 0 {
		t.Errorf("central MCP registry still has %d tools after global kill-switch, want 0", got)
	}
	if hasToolWithPrefix(miaAgent.Tools.List(), "mcp_stub-server_") {
		t.Error("default agent still has a registered mcp_stub-server_* tool after global kill-switch")
	}
}

// TestReconcileMCP_ReloadHeal_ReRegistersOnUnchangedConfig covers the
// specific reason ReconcileMCP re-registers tools UNCONDITIONALLY on every
// pass rather than only when something in the diff changed: a hot reload
// (ReloadProviderAndConfig) builds a brand-new AgentRegistry whose per-agent
// ToolRegistrys start empty, silently dropping every previously-registered
// MCP tool unless something re-adds them. This simulates that wipe directly
// (Unregister the tool from one agent's registry, leaving the manager
// connection and the central registry untouched) and proves a reconcile pass
// with a COMPLETELY UNCHANGED config heals it back — for that one agent —
// without needing any config diff to trigger it.
func TestReconcileMCP_ReloadHeal_ReRegistersOnUnchangedConfig(t *testing.T) {
	binPath := buildStubMCPServer(t)

	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"stub-server": {Enabled: true, Command: binPath},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	centralMCP := tools.NewMCPRegistry()
	centralBuiltin := tools.NewBuiltinRegistry()
	al.SetCentralMCPRegistries(centralMCP, centralBuiltin)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (connect): %v", err)
	}

	jimAgent, ok := al.GetRegistry().GetAgent("jim")
	if !ok {
		t.Fatal("jim agent not found in registry")
	}
	toolNames := toolNamesWithPrefix(jimAgent.Tools.List(), "mcp_stub-server_")
	if len(toolNames) == 0 {
		t.Fatal("jim agent has no mcp_stub-server_* tools after initial connect")
	}

	// Simulate the wipe a fresh AgentRegistry (ReloadProviderAndConfig) would
	// cause for this one agent: unregister the tools directly, without
	// touching the manager or the central registry.
	for _, name := range toolNames {
		jimAgent.Tools.Unregister(name)
	}
	if got := countToolsWithPrefix(jimAgent.Tools.List(), "mcp_stub-server_"); got != 0 {
		t.Fatalf("setup: jim agent still has %d mcp_stub-server_* tools after manual unregister", got)
	}
	if got := centralMCP.Count(); got != len(toolNames) {
		t.Fatalf(
			"setup: central MCP registry has %d tools, want %d (unaffected by the per-agent wipe)",
			got,
			len(toolNames),
		)
	}

	// Reconcile again with a config that has NOT changed at all.
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (heal pass): %v", err)
	}

	if got := countToolsWithPrefix(jimAgent.Tools.List(), "mcp_stub-server_"); got != len(toolNames) {
		t.Errorf(
			"jim agent has %d mcp_stub-server_* tools after heal pass, want %d (re-registered)",
			got,
			len(toolNames),
		)
	}
	if got := centralMCP.Count(); got != len(toolNames) {
		t.Errorf("central MCP registry has %d tools after heal pass, want %d", got, len(toolNames))
	}
}

// TestReconcileMCP_ConfigChange_Reconnects verifies a live server config
// change (Args) is detected by the reflect.DeepEqual drift-check in the
// removal pass and triggers a real teardown+reconnect, not a stale
// connection surviving the pass: the post-reconcile conn.Config must equal
// the NEW server config, and the underlying session must be a different
// instance than before the change.
func TestReconcileMCP_ConfigChange_Reconnects(t *testing.T) {
	binPath := buildStubMCPServer(t)

	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"stub-server": {Enabled: true, Command: binPath},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (initial connect): %v", err)
	}

	mgr := al.mcp.getManager()
	if mgr == nil {
		t.Fatal("manager is nil after initial connect")
	}
	origConn, ok := mgr.GetServer("stub-server")
	if !ok {
		t.Fatal("stub-server not connected after initial reconcile")
	}
	origSession := origConn.Session

	// Live config change: append a benign extra arg (the stub server ignores
	// argv entirely, so this cannot itself break the connect — only the
	// config-drift detection is under test here).
	newCfg := config.MCPServerConfig{Enabled: true, Command: binPath, Args: []string{"--benign-flag"}}
	cfg.Tools.MCP.Servers["stub-server"] = newCfg

	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (config-change pass): %v", err)
	}

	status, _, errMsg := al.MCPServerStatus("stub-server")
	if status != "connected" {
		t.Fatalf("status after config change = %q (errMsg=%q), want connected", status, errMsg)
	}

	newConn, ok := mgr.GetServer("stub-server")
	if !ok {
		t.Fatal("stub-server not connected after config-change reconcile")
	}
	if newConn.Session == origSession {
		t.Error("session unchanged after a config-change reconcile — expected teardown+reconnect, got a stale conn")
	}
	if !reflect.DeepEqual(newConn.Config, newCfg) {
		t.Errorf(
			"conn.Config = %+v, want %+v (the NEW server config, not the one it originally connected with)",
			newConn.Config,
			newCfg,
		)
	}
}

// TestReconcileMCP_ErrorRecovery drives a full error → recovery → error
// again → disable cycle against a single server name: an unconnectable
// command first reports "error", then recovers to "connected" once the
// config is fixed to point at the real stub binary (with tools actually
// registered), then breaks again to reconfirm "error" is re-derived (not
// stuck from the first failure), and finally proves DISABLING a
// currently-failing server clears its recorded connect error instead of
// leaving a permanent "error" status behind for a server no longer in the
// desired set.
func TestReconcileMCP_ErrorRecovery(t *testing.T) {
	binPath := buildStubMCPServer(t)
	const badCommand = "/nonexistent-binary-xyz"

	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"flaky-server": {Enabled: true, Command: badCommand},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	centralMCP := tools.NewMCPRegistry()
	centralBuiltin := tools.NewBuiltinRegistry()
	al.SetCentralMCPRegistries(centralMCP, centralBuiltin)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (initial connect attempt): %v", err)
	}

	status, toolCount, errMsg := al.MCPServerStatus("flaky-server")
	if status != "error" {
		t.Fatalf("status = %q (errMsg=%q), want error", status, errMsg)
	}
	if errMsg == "" {
		t.Error("errMsg is empty, want the recorded connect failure")
	}
	if toolCount != 0 {
		t.Errorf("toolCount = %d, want 0", toolCount)
	}

	// Fix the config to point at the real stub binary and reconcile again.
	cfg.Tools.MCP.Servers["flaky-server"] = config.MCPServerConfig{Enabled: true, Command: binPath}
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (recovery pass): %v", err)
	}

	status, toolCount, errMsg = al.MCPServerStatus("flaky-server")
	if status != "connected" {
		t.Fatalf("status after recovery = %q (errMsg=%q), want connected", status, errMsg)
	}
	if errMsg != "" {
		t.Errorf("errMsg after recovery = %q, want empty", errMsg)
	}
	if toolCount == 0 {
		t.Error("toolCount after recovery = 0, want > 0 (tools registered)")
	}

	// Break it again (different config than the working one, so the
	// config-drift check disconnects the working connection first) and
	// confirm status is re-derived as "error", not stuck from the first
	// failure.
	cfg.Tools.MCP.Servers["flaky-server"] = config.MCPServerConfig{Enabled: true, Command: badCommand}
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (re-break pass): %v", err)
	}
	status, _, errMsg = al.MCPServerStatus("flaky-server")
	if status != "error" {
		t.Fatalf("status after re-break = %q (errMsg=%q), want error", status, errMsg)
	}
	if errMsg == "" {
		t.Fatal("errMsg after re-break is empty, want the recorded connect failure")
	}

	// Disabling a currently-failing server must prune its connectErr, not
	// leave it permanently reporting "error" now that it has dropped out of
	// the desired set entirely.
	cfg.Tools.MCP.Servers["flaky-server"] = config.MCPServerConfig{Enabled: false, Command: badCommand}
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP (disable pass): %v", err)
	}
	status, _, errMsg = al.MCPServerStatus("flaky-server")
	if status != "disconnected" {
		t.Errorf(
			"status after disabling a failing server = %q (errMsg=%q), want disconnected (connect-error pruning)",
			status,
			errMsg,
		)
	}
	if errMsg != "" {
		t.Errorf("errMsg after disabling a failing server = %q, want empty (connect-error pruning)", errMsg)
	}
}

// TestReconcileMCP_LiveToolCallAfterReconcile is the agent-level liveness
// tripwire for the stdio-child-lifetime bug: ReconcileMCP connects each
// server on its own goroutine with a bounded mcpConnectTimeout child ctx
// (see the connect loop in reconcileLocked) whose deferred cancel fires
// moments after a successful connect returns. If ConnectServer ever ties the
// spawned stdio child's process lifetime to that same ctx, the child dies at
// cancellation and a real tool call issued afterward — through the SAME live
// manager ReconcileMCP populated, on a completely independent ctx — fails
// even though MCPServerStatus still reports "connected". A status/tool-count
// check alone (as the other tests in this file do) cannot catch that: the
// manager's map entry stays present regardless of whether the underlying
// process is alive. Only an actual CallTool round-trip proves liveness.
func TestReconcileMCP_LiveToolCallAfterReconcile(t *testing.T) {
	binPath := buildStubMCPServer(t)

	cfg := minimalTestConfig(t)
	cfg.Tools.MCP.Enabled = true
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"stub-server": {Enabled: true, Command: binPath},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := al.ReconcileMCP(ctx); err != nil {
		t.Fatalf("ReconcileMCP: %v", err)
	}

	status, _, errMsg := al.MCPServerStatus("stub-server")
	if status != "connected" {
		t.Fatalf("status = %q (errMsg=%q), want connected", status, errMsg)
	}

	mgr := al.mcp.getManager()
	if mgr == nil {
		t.Fatal("manager is nil after a successful reconcile")
	}

	// A fresh ctx, unrelated to ReconcileMCP's own (already-returned) ctx or
	// its per-connect child ctx (both long expired by now) — success here can
	// only mean the stdio child process is still alive and answering.
	callCtx, callCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer callCancel()
	result, err := mgr.CallTool(callCtx, "stub-server", "stub.echo", map[string]any{"message": "reconcile-liveness"})
	if err != nil {
		t.Fatalf(
			"CallTool after ReconcileMCP: %v (stdio child likely killed by a connect-ctx-scoped process lifetime)",
			err,
		)
	}
	if result == nil {
		t.Fatal("CallTool returned nil result")
	}
	if result.IsError {
		t.Fatalf("CallTool result reports an error: %+v", result.Content)
	}
	text, ok := firstAgentTestTextContent(result)
	if !ok || text != "reconcile-liveness" {
		t.Fatalf("CallTool result content = %q, want echoed %q", text, "reconcile-liveness")
	}
}

// firstAgentTestTextContent extracts the text of the first
// *sdkmcp.TextContent block in a CallToolResult, for asserting the stub
// echoed back what was sent (proof the child is not just present in the
// manager's map but actually alive and processing requests).
func firstAgentTestTextContent(result *sdkmcp.CallToolResult) (string, bool) {
	for _, c := range result.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			return tc.Text, true
		}
	}
	return "", false
}
