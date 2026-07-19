// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// newTestDepsWithEmptySessions returns a Deps whose ListSessions is wired to
// return an empty slice (no errors).  Use this for tests that need the
// get_usage happy path but don't care about session data.
func newTestDepsWithEmptySessions() *systools.Deps {
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	return &systools.Deps{
		Home:             "/tmp/omnipus-test",
		ConfigPath:       "/tmp/omnipus-test/config.json",
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(*config.Config) error { return nil },
		ListSessions: func() ([]*session.UnifiedMeta, []error) {
			return nil, nil
		},
	}
}

// resultJSON unmarshals a tool result's ForLLM payload into a generic map.
func resultJSON(t *testing.T, forLLM string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(forLLM), &m); err != nil {
		t.Fatalf("result is not JSON: %v\npayload: %s", err, forLLM)
	}
	return m
}

// TestMCPTools_RoundTrip exercises the wired system.mcp.add / list / remove
// tools against a real (in-memory) config and asserts the data survives.
func TestMCPTools_RoundTrip(t *testing.T) {
	deps, cfg := newTestDeps()
	ctx := context.Background()

	add := systools.NewMCPAddTool(deps)
	list := systools.NewMCPListTool(deps)
	remove := systools.NewMCPRemoveTool(deps)

	// --- add a stdio server ---
	res := add.Execute(ctx, map[string]any{
		"name":    "filesystem",
		"command": "npx",
		"args":    []any{"-y", "@modelcontextprotocol/server-filesystem"},
		"env":     map[string]any{"ROOT": "/tmp"},
	})
	m := resultJSON(t, res.ForLLM)
	if m["success"] == false {
		t.Fatalf("add returned error: %s", res.ForLLM)
	}
	// Verify it actually landed in config (the real subsystem).
	srv, ok := cfg.Tools.MCP.Servers["filesystem"]
	if !ok {
		t.Fatalf("server not persisted to cfg.Tools.MCP.Servers; got %+v", cfg.Tools.MCP.Servers)
	}
	if srv.Type != "stdio" || srv.Command != "npx" || !srv.Enabled {
		t.Fatalf("persisted server has wrong fields: %+v", srv)
	}
	if len(srv.Args) != 2 || srv.Env["ROOT"] != "/tmp" {
		t.Fatalf("args/env not persisted: %+v", srv)
	}
	// Adding a server must also flip the global MCP kill-switch on, in the
	// same config write, or the newly added server is silently ignored by
	// ReconcileMCP (desired set is empty when Tools.MCP.Enabled=false).
	if !cfg.Tools.MCP.Enabled {
		t.Fatalf("want cfg.Tools.MCP.Enabled=true after add_mcp_server, got false")
	}

	// --- list shows it ---
	res = list.Execute(ctx, nil)
	m = resultJSON(t, res.ForLLM)
	servers, _ := m["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("list expected 1 server, got %d: %s", len(servers), res.ForLLM)
	}

	// --- duplicate add is rejected ---
	res = add.Execute(ctx, map[string]any{"name": "filesystem", "command": "npx"})
	m = resultJSON(t, res.ForLLM)
	if m["success"] != false {
		t.Fatalf("duplicate add should fail, got: %s", res.ForLLM)
	}

	// --- remove without confirm is rejected ---
	res = remove.Execute(ctx, map[string]any{"name": "filesystem"})
	if m2 := resultJSON(t, res.ForLLM); m2["success"] != false {
		t.Fatalf("remove without confirm should fail, got: %s", res.ForLLM)
	}

	// --- remove with confirm succeeds and persists ---
	res = remove.Execute(ctx, map[string]any{"name": "filesystem", "confirm": true})
	if m2 := resultJSON(t, res.ForLLM); m2["success"] == false {
		t.Fatalf("remove failed: %s", res.ForLLM)
	}
	if _, ok := cfg.Tools.MCP.Servers["filesystem"]; ok {
		t.Fatalf("server still present in cfg after remove")
	}

	// --- remove of unknown server returns NOT_FOUND ---
	res = remove.Execute(ctx, map[string]any{"name": "ghost", "confirm": true})
	m = resultJSON(t, res.ForLLM)
	if m["success"] != false {
		t.Fatalf("removing unknown server should fail, got: %s", res.ForLLM)
	}
}

// TestMCPAdd_TransportValidation asserts per-transport field validation mirrors
// the gateway (stdio→command required; sse/http→url required + scheme check).
func TestMCPAdd_TransportValidation(t *testing.T) {
	deps, _ := newTestDeps()
	ctx := context.Background()
	add := systools.NewMCPAddTool(deps)

	cases := []struct {
		name    string
		args    map[string]any
		wantErr bool
	}{
		{"stdio missing command", map[string]any{"name": "s1", "transport": "stdio"}, true},
		{"http missing url", map[string]any{"name": "s2", "transport": "http"}, true},
		{
			"http public url rejected",
			map[string]any{"name": "s3", "transport": "http", "url": "http://evil.example.com"},
			true,
		},
		{"http loopback ok", map[string]any{"name": "s4", "transport": "http", "url": "http://localhost:8080"}, false},
		{"https ok", map[string]any{"name": "s5", "transport": "sse", "url": "https://mcp.example.com"}, false},
		{"bad transport", map[string]any{"name": "s6", "transport": "websocket", "url": "https://x"}, true},
		{"path-traversal name", map[string]any{"name": "../etc", "command": "x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := add.Execute(ctx, tc.args)
			m := resultJSON(t, res.ForLLM)
			gotErr := m["success"] == false
			if gotErr != tc.wantErr {
				t.Fatalf("want err=%v, got err=%v: %s", tc.wantErr, gotErr, res.ForLLM)
			}
		})
	}
}

// TestMCPAdd_GlobalKillSwitchGating asserts add_mcp_server's global
// tools.mcp.enabled auto-enable is gated correctly: it fires only for the
// fresh-install case (flag off, no other server already enabled), and
// leaves an operator's deliberate kill-switch alone when other servers are
// already enabled under it.
func TestMCPAdd_GlobalKillSwitchGating(t *testing.T) {
	t.Run("flips when no other server is enabled", func(t *testing.T) {
		deps, cfg := newTestDeps()
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "fresh-srv", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("add returned error: %s", res.ForLLM)
		}
		if !cfg.Tools.MCP.Enabled {
			t.Fatalf("want cfg.Tools.MCP.Enabled=true (fresh-install auto-enable), got false")
		}
		note, _ := m["note"].(string)
		if !strings.Contains(note, "Global MCP enable was off — turned on.") {
			t.Fatalf("note should disclose the flip, got: %q", note)
		}
	})

	t.Run("does not flip when another server is already enabled under an off switch", func(t *testing.T) {
		deps, cfg := newTestDeps()
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
			"existing-enabled": {Enabled: true, Type: "stdio", Command: "npx"},
		}
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "gated-srv", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("add returned error: %s", res.ForLLM)
		}
		if cfg.Tools.MCP.Enabled {
			t.Fatalf("want cfg.Tools.MCP.Enabled to remain false (operator kill-switch), got true")
		}
		if _, ok := cfg.Tools.MCP.Servers["gated-srv"]; !ok {
			t.Fatalf("the new server must still be saved even though the global flag was not flipped")
		}
		note, _ := m["note"].(string)
		if !strings.Contains(note, "MCP is globally disabled") {
			t.Fatalf("note should disclose that MCP is globally disabled, got: %q", note)
		}
		if !strings.Contains(note, "tools.mcp.enabled") {
			t.Fatalf("note should tell the operator which flag to enable, got: %q", note)
		}
	})
}

// TestMCPAdd_ReconcileAndStatus asserts add_mcp_server calls the live
// reconciliation hook and builds an honest response from MCPStatus, covering
// each status branch plus the nil-funcs fallback.
func TestMCPAdd_ReconcileAndStatus(t *testing.T) {
	t.Run("connected", func(t *testing.T) {
		deps, _ := newTestDeps()
		var reconcileCalls int
		var reconciledHadDeadline bool
		deps.ReconcileMCP = func(ctx context.Context) error {
			reconcileCalls++
			if ctx != nil {
				_, reconciledHadDeadline = ctx.Deadline()
			}
			return nil
		}
		deps.MCPStatus = func(name string) (string, int, string) {
			if name != "srv-connected" {
				t.Fatalf("MCPStatus called with unexpected name %q", name)
			}
			return "connected", 3, ""
		}
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "srv-connected", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("add returned error: %s", res.ForLLM)
		}
		if reconcileCalls != 1 {
			t.Fatalf("want ReconcileMCP called once, got %d", reconcileCalls)
		}
		if !reconciledHadDeadline {
			t.Fatalf("ReconcileMCP context missing or has no deadline (want 20s timeout)")
		}
		if m["status"] != "connected" {
			t.Fatalf("want status=connected, got %v", m["status"])
		}
		if tc, ok := m["tool_count"].(float64); !ok || tc != 3 {
			t.Fatalf("want tool_count=3, got %v", m["tool_count"])
		}
		note, _ := m["note"].(string)
		if !strings.Contains(note, "3 tool(s) registered") {
			t.Fatalf("note should mention tool count, got: %q", note)
		}
	})

	t.Run("error", func(t *testing.T) {
		deps, _ := newTestDeps()
		deps.ReconcileMCP = func(ctx context.Context) error { return nil }
		deps.MCPStatus = func(name string) (string, int, string) {
			return "error", 0, "connection refused"
		}
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "srv-error", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		// The config write DID succeed — this must still be success=true.
		if m["success"] == false {
			t.Fatalf("add should still succeed (config was saved) even when connect fails: %s", res.ForLLM)
		}
		if m["status"] != "error" {
			t.Fatalf("want status=error, got %v", m["status"])
		}
		note, _ := m["note"].(string)
		if !strings.Contains(note, "connection refused") {
			t.Fatalf("note should include the connect error, got: %q", note)
		}
		if !strings.Contains(note, "Server saved") {
			t.Fatalf("note should be honest that the config write succeeded, got: %q", note)
		}
	})

	t.Run("disconnected", func(t *testing.T) {
		deps, _ := newTestDeps()
		deps.ReconcileMCP = func(ctx context.Context) error { return nil }
		deps.MCPStatus = func(name string) (string, int, string) {
			return "disconnected", 0, ""
		}
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "srv-disc", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("add returned error: %s", res.ForLLM)
		}
		if m["status"] != "disconnected" {
			t.Fatalf("want status=disconnected, got %v", m["status"])
		}
		note, _ := m["note"].(string)
		if strings.Contains(note, "connects on the next agent-loop reload") {
			t.Fatalf("disconnected note must not claim it will connect, got: %q", note)
		}
	})

	t.Run("reconcile error is logged but does not fail the add", func(t *testing.T) {
		deps, _ := newTestDeps()
		deps.ReconcileMCP = func(ctx context.Context) error { return fmt.Errorf("boom") }
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "srv-reconcile-err", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("add should still succeed when reconcile itself errors (config write succeeded): %s", res.ForLLM)
		}
		// No MCPStatus wired alongside the erroring ReconcileMCP: falls back to
		// the honest reload-based note rather than fabricating a status.
		if _, hasStatus := m["status"]; hasStatus {
			t.Fatalf("status field should be absent when MCPStatus is nil, got: %s", res.ForLLM)
		}
		// The reconcile error must not be silently swallowed: the note must
		// surface it rather than claiming the generic "connects on the next
		// agent-loop reload" fallback, which would falsely imply reconcile
		// was never attempted.
		note, _ := m["note"].(string)
		if !strings.Contains(note, "boom") {
			t.Fatalf("note must surface the reconcile error, got: %q", note)
		}
		if strings.Contains(note, "connects on the next agent-loop reload") {
			t.Fatalf(
				"note must not use the never-attempted-reconcile fallback when reconcile actually errored, got: %q",
				note,
			)
		}
	})

	t.Run("nil funcs use fallback note and no status field", func(t *testing.T) {
		deps, _ := newTestDeps() // ReconcileMCP and MCPStatus left nil
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), map[string]any{"name": "srv-nil", "command": "npx"})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("add returned error: %s", res.ForLLM)
		}
		if _, hasStatus := m["status"]; hasStatus {
			t.Fatalf("status field should be absent when MCPStatus is nil, got: %s", res.ForLLM)
		}
		note, _ := m["note"].(string)
		if !strings.Contains(note, "connects on the next agent-loop reload") {
			t.Fatalf("nil-funcs fallback note changed unexpectedly: %q", note)
		}
	})
}

// TestMCPRemove_Reconcile asserts remove_mcp_server calls the live
// reconciliation hook and adjusts its note accordingly.
func TestMCPRemove_Reconcile(t *testing.T) {
	t.Run("reconcile wired", func(t *testing.T) {
		deps, cfg := newTestDeps()
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
			"srv": {Enabled: true, Type: "stdio", Command: "npx"},
		}
		var reconcileCalls int
		var reconciledHadDeadline bool
		deps.ReconcileMCP = func(ctx context.Context) error {
			reconcileCalls++
			if ctx != nil {
				_, reconciledHadDeadline = ctx.Deadline()
			}
			return nil
		}
		remove := systools.NewMCPRemoveTool(deps)
		res := remove.Execute(context.Background(), map[string]any{"name": "srv", "confirm": true})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("remove returned error: %s", res.ForLLM)
		}
		if reconcileCalls != 1 {
			t.Fatalf("want ReconcileMCP called once, got %d", reconcileCalls)
		}
		if !reconciledHadDeadline {
			t.Fatalf("ReconcileMCP context missing or has no deadline (want 20s timeout)")
		}
		note, _ := m["note"].(string)
		if note != "Server disconnected and removed." {
			t.Fatalf("want the reconciled note, got: %q", note)
		}
		if _, ok := cfg.Tools.MCP.Servers["srv"]; ok {
			t.Fatalf("server still present in cfg after remove")
		}
	})

	t.Run("reconcile nil falls back to old note", func(t *testing.T) {
		deps, cfg := newTestDeps() // ReconcileMCP left nil
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
			"srv": {Enabled: true, Type: "stdio", Command: "npx"},
		}
		remove := systools.NewMCPRemoveTool(deps)
		res := remove.Execute(context.Background(), map[string]any{"name": "srv", "confirm": true})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf("remove returned error: %s", res.ForLLM)
		}
		note, _ := m["note"].(string)
		if note != "Server removed from config. Its tools are unregistered on the next agent-loop reload." {
			t.Fatalf("want the fallback note, got: %q", note)
		}
	})

	t.Run("reconcile error is logged but does not fail the remove", func(t *testing.T) {
		deps, cfg := newTestDeps()
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
			"srv": {Enabled: true, Type: "stdio", Command: "npx"},
		}
		deps.ReconcileMCP = func(ctx context.Context) error { return fmt.Errorf("boom") }
		remove := systools.NewMCPRemoveTool(deps)
		res := remove.Execute(context.Background(), map[string]any{"name": "srv", "confirm": true})
		m := resultJSON(t, res.ForLLM)
		if m["success"] == false {
			t.Fatalf(
				"remove should still succeed when reconcile itself errors (config removal succeeded): %s",
				res.ForLLM,
			)
		}
		// A reconcile error must not be papered over with the optimistic
		// "disconnected and removed" note — that asserts a live-side outcome
		// (the disconnect) that was never actually observed.
		note, _ := m["note"].(string)
		wantNote := "Removed from config; live disconnect may not have completed: boom"
		if note != wantNote {
			t.Fatalf("want honest reconcile-error note %q, got %q", wantNote, note)
		}
		if _, ok := cfg.Tools.MCP.Servers["srv"]; ok {
			t.Fatalf("server still present in cfg after remove despite reconcile error")
		}
	})
}

// TestGetUsageTool_BasicBehaviour asserts that get_usage (the replacement for
// the retired query_cost stub) executes successfully and returns a well-formed
// usage report — no dollar amounts, no NOT_IMPLEMENTED error.
func TestGetUsageTool_BasicBehaviour(t *testing.T) {
	// Use a deps with a wired (empty) ListSessions so the happy-path subtests
	// reach the aggregator rather than the nil-guard error.
	deps := newTestDepsWithEmptySessions()
	ctx := context.Background()

	tool := systools.NewUsageQueryTool(deps)

	t.Run("name_is_get_usage", func(t *testing.T) {
		if tool.Name() != "get_usage" {
			t.Errorf("want Name()=get_usage, got %q", tool.Name())
		}
	})

	t.Run("description_mentions_token_not_dollars", func(t *testing.T) {
		desc := tool.Description()
		if strings.Contains(desc, "NOT IMPLEMENTED") {
			t.Fatalf("get_usage description must not say NOT IMPLEMENTED: %q", desc)
		}
		// The description must not mention dollar signs or USD.
		if strings.Contains(desc, "$") || strings.Contains(strings.ToLower(desc), "usd") {
			t.Fatalf("get_usage description must not mention dollar/USD: %q", desc)
		}
		if !strings.Contains(strings.ToLower(desc), "token") {
			t.Fatalf("get_usage description must mention tokens: %q", desc)
		}
	})

	t.Run("default_params_succeed", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{})
		m := resultJSON(t, result.ForLLM)
		if m["success"] == false {
			t.Fatalf("get_usage with default params must succeed, got: %v", m)
		}
	})

	t.Run("output_has_no_dollar_cost_fields", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"period": "month", "by": "agent"})
		raw := result.ForLLM
		lower := strings.ToLower(raw)
		for _, forbidden := range []string{"\"cost\"", "\"usd\"", "\"dollars\""} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("get_usage output must not contain %q; got: %s", forbidden, raw)
			}
		}
	})

	t.Run("invalid_period_returns_error", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"period": "yesterday"})
		m := resultJSON(t, result.ForLLM)
		if m["success"] != false {
			t.Fatalf("invalid period must return success=false, got: %v", m)
		}
		errObj, _ := m["error"].(map[string]any)
		if errObj["code"] != "INVALID_PARAM" {
			t.Fatalf("invalid period must return INVALID_PARAM, got: %v", errObj)
		}
	})

	t.Run("invalid_by_returns_error", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"by": "category"})
		m := resultJSON(t, result.ForLLM)
		if m["success"] != false {
			t.Fatalf("invalid by must return success=false, got: %v", m)
		}
	})

	t.Run("valid_periods_accepted", func(t *testing.T) {
		for _, period := range []string{"day", "week", "month", "all"} {
			result := tool.Execute(ctx, map[string]any{"period": period})
			m := resultJSON(t, result.ForLLM)
			if m["success"] == false {
				t.Errorf("period=%q must succeed, got: %v", period, m)
			}
		}
	})

	t.Run("valid_dimensions_accepted", func(t *testing.T) {
		for _, by := range []string{"agent", "model", "session"} {
			result := tool.Execute(ctx, map[string]any{"by": by})
			m := resultJSON(t, result.ForLLM)
			if m["success"] == false {
				t.Errorf("by=%q must succeed, got: %v", by, m)
			}
		}
	})

	t.Run("output_shape_has_required_fields", func(t *testing.T) {
		result := tool.Execute(ctx, map[string]any{"period": "month", "by": "agent"})
		m := resultJSON(t, result.ForLLM)
		for _, key := range []string{"period", "by", "period_end", "total", "breakdown"} {
			if _, ok := m[key]; !ok {
				t.Errorf("output must have field %q; full output: %s", key, result.ForLLM)
			}
		}
	})

	t.Run("nil_list_sessions_handled", func(t *testing.T) {
		// A nil ListSessions wiring is a configuration bug that must fail loudly
		// with USAGE_UNAVAILABLE rather than returning a fake-zero empty report.
		nilDeps, _ := newTestDeps() // newTestDeps does NOT wire ListSessions
		nilTool := systools.NewUsageQueryTool(nilDeps)
		result := nilTool.Execute(ctx, map[string]any{"period": "month"})
		if result == nil {
			t.Fatal("get_usage must return a non-nil ToolResult even with nil ListSessions")
		}
		m := resultJSON(t, result.ForLLM)
		if m["success"] != false {
			t.Fatalf("nil ListSessions must return success=false (USAGE_UNAVAILABLE), got: %v", m)
		}
		errObj, _ := m["error"].(map[string]any)
		if code, _ := errObj["code"].(string); code != "USAGE_UNAVAILABLE" {
			t.Errorf("nil ListSessions must return USAGE_UNAVAILABLE, got code=%q", code)
		}
	})
}
