// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

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
		{"http public url rejected", map[string]any{"name": "s3", "transport": "http", "url": "http://evil.example.com"}, true},
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

// TestHonestStubTools_ReturnNotImplemented asserts the genuinely-unbuilt tools
// no longer fake success: they return an explicit NOT_IMPLEMENTED error and
// their description warns the model.
func TestHonestStubTools_ReturnNotImplemented(t *testing.T) {
	deps, _ := newTestDeps()
	ctx := context.Background()

	type honest struct {
		name string
		exec func() string
		desc string
	}
	chEnable := systools.NewChannelEnableTool(deps)
	chDisable := systools.NewChannelDisableTool(deps)
	chTest := systools.NewChannelTestTool(deps)
	backup := systools.NewBackupCreateTool(deps)
	cost := systools.NewCostQueryTool(deps)

	tools := []honest{
		{"channel.enable", func() string { return chEnable.Execute(ctx, map[string]any{"id": "telegram"}).ForLLM }, chEnable.Description()},
		{"channel.disable", func() string { return chDisable.Execute(ctx, map[string]any{"id": "telegram"}).ForLLM }, chDisable.Description()},
		{"channel.test", func() string { return chTest.Execute(ctx, map[string]any{"id": "telegram"}).ForLLM }, chTest.Description()},
		{"backup.create", func() string { return backup.Execute(ctx, map[string]any{}).ForLLM }, backup.Description()},
		{"cost.query", func() string { return cost.Execute(ctx, map[string]any{"period": "today"}).ForLLM }, cost.Description()},
	}

	for _, tl := range tools {
		t.Run(tl.name, func(t *testing.T) {
			// Description must warn the model it is not implemented.
			if !strings.Contains(tl.desc, "NOT IMPLEMENTED") {
				t.Fatalf("%s description must say NOT IMPLEMENTED, got: %q", tl.name, tl.desc)
			}
			// Execute must return an error envelope with code NOT_IMPLEMENTED —
			// never a fake success.
			m := resultJSON(t, tl.exec())
			if m["success"] != false {
				t.Fatalf("%s must return success=false, got: %v", tl.name, m)
			}
			errObj, _ := m["error"].(map[string]any)
			if errObj["code"] != "NOT_IMPLEMENTED" {
				t.Fatalf("%s must return code NOT_IMPLEMENTED, got: %v", tl.name, errObj)
			}
		})
	}
}
