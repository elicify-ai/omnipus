// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- minimal test tool for MCP registry tests ---

type mcpTestTool struct {
	name string
}

func (t *mcpTestTool) Name() string               { return t.name }
func (t *mcpTestTool) Description() string        { return "mcp test tool: " + t.name }
func (t *mcpTestTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (t *mcpTestTool) Scope() ToolScope           { return ScopeGeneral }
func (t *mcpTestTool) RequiresAdminAsk() bool     { return false }
func (t *mcpTestTool) Category() ToolCategory     { return CategoryCode }
func (t *mcpTestTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return SilentResult("ok")
}

// --- TestMCPRegistry_DynamicAddRemove ---

// TestMCPRegistry_DynamicAddRemove verifies that tools can be dynamically added
// via RegisterServerTools and removed via EvictServer.
//
// BDD: Given an empty MCPRegistry,
//
//	When RegisterServerTools is called for "server-a" with two tools,
//	Then All() returns both tools;
//	When EvictServer("server-a") is called,
//	Then All() returns an empty slice.
//
// Traces to: pkg/tools/mcp_registry.go — RegisterServerTools + EvictServer.
func TestMCPRegistry_DynamicAddRemove(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	tools := []Tool{
		&mcpTestTool{name: "mcp.read"},
		&mcpTestTool{name: "mcp.write"},
	}
	collisions := reg.RegisterServerTools("server-a", tools, builtins)
	require.Empty(t, collisions, "no collisions expected for fresh registration")

	all := reg.All()
	require.Len(t, all, 2, "All() must return both registered tools")

	reg.EvictServer("server-a")
	assert.Empty(t, reg.All(), "All() must be empty after eviction")
}

// TestMCPRegistry_FirstServerWins verifies the first-server-wins collision rule:
// when two MCP servers register a tool with the same name, the first registration
// wins and a collision is recorded for the second (FR-034).
//
// BDD: Given server-a registers "mcp.search",
//
//	When server-b also tries to register "mcp.search",
//	Then server-b's registration is rejected (collision returned);
//	And Get("mcp.search") still returns server-a's tool.
//
// Traces to: pkg/tools/mcp_registry.go — RegisterServerTools first-server-wins (FR-034).
func TestMCPRegistry_FirstServerWins(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	toolA := &mcpTestTool{name: "mcp.search"}
	collisionsA := reg.RegisterServerTools("server-a", []Tool{toolA}, builtins)
	require.Empty(t, collisionsA, "first registration must not produce collisions")

	toolB := &mcpTestTool{name: "mcp.search"}
	collisionsB := reg.RegisterServerTools("server-b", []Tool{toolB}, builtins)
	require.Len(t, collisionsB, 1, "second registration of same name must produce a collision")
	assert.Equal(t, "mcp.search", collisionsB[0].ToolName, "collision must record the conflicting name")
	assert.Equal(t, "server-a", collisionsB[0].ConflictWith, "ConflictWith must name the first (winning) server")

	// server-a's tool must still be accessible.
	got, ok := reg.Get("mcp.search")
	require.True(t, ok, "Get must still return the first-registered tool")
	assert.Equal(t, "mcp.search", got.Name())
}

// TestMCPRegistry_EvictServer_OnlyEvictsTargetServer verifies that evicting one
// server does not remove tools from other servers.
//
// BDD: Given server-a and server-b each register a distinct tool,
//
//	When EvictServer("server-a") is called,
//	Then server-b's tool is still returned by All().
//
// Traces to: pkg/tools/mcp_registry.go — EvictServer (per-server isolation).
func TestMCPRegistry_EvictServer_OnlyEvictsTargetServer(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	reg.RegisterServerTools("server-a", []Tool{&mcpTestTool{name: "a.tool"}}, builtins)
	reg.RegisterServerTools("server-b", []Tool{&mcpTestTool{name: "b.tool"}}, builtins)

	reg.EvictServer("server-a")

	all := reg.All()
	require.Len(t, all, 1, "only server-b's tool should remain")
	assert.Equal(t, "b.tool", all[0].Name())
}

// TestMCPRegistry_SystemPrefixRejected verifies that MCP tools with "system."
// prefix are rejected by RegisterServerTools (FR-060).
//
// BDD: Given a builtin registry,
//
//	When RegisterServerTools registers a tool named "system.some_tool",
//	Then a collision is returned (rejected);
//	And All() does not contain the system-prefixed tool.
//
// Traces to: pkg/tools/mcp_registry.go — system prefix guard (FR-060).
func TestMCPRegistry_SystemPrefixRejected(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	collisions := reg.RegisterServerTools("mcp-server", []Tool{
		&mcpTestTool{name: "system.hijack"},
		&mcpTestTool{name: "safe.tool"},
	}, builtins)

	require.Len(t, collisions, 1, "one collision for the system-prefix tool")
	assert.Equal(t, "system.hijack", collisions[0].ToolName)

	all := reg.All()
	require.Len(t, all, 1, "only the safe tool should be registered")
	assert.Equal(t, "safe.tool", all[0].Name())
}

// --- M3: Rename detection tests (FR-083) ---

// TestMCPRegistry_ServerRename verifies that registering a known transport
// fingerprint under a new serverID triggers rename detection: the old server
// is evicted, the new server's tools are registered, and per-agent policies
// are notified (FR-083).
//
// BDD: Given server "old-server" registered at sse://example.com,
//
//	When "new-server" registers at the same sse://example.com endpoint,
//	Then old-server's tools are evicted;
//	And new-server's tools are present;
//	And the PolicyUpdater is called with (old, new) serverIDs.
//
// Traces to: pkg/tools/mcp_registry.go — RegisterServerToolsWithOpts rename detection (FR-083).
func TestMCPRegistry_ServerRename(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	// Register old server with a fingerprint.
	oldCollisions := reg.RegisterServerToolsWithOpts("old-server", []Tool{
		&mcpTestTool{name: "data.read"},
		&mcpTestTool{name: "data.write"},
	}, builtins, MCPServerOpts{
		TransportType: "sse",
		Endpoint:      "https://example.com/mcp",
	})
	require.Empty(t, oldCollisions, "initial registration must not produce collisions")
	require.Len(t, reg.All(), 2, "old-server tools registered")

	// Register new server at the same fingerprint → rename.
	var policyOldID, policyNewID string
	newCollisions := reg.RegisterServerToolsWithOpts("new-server", []Tool{
		&mcpTestTool{name: "data.read"},
		&mcpTestTool{name: "data.write"},
		&mcpTestTool{name: "data.delete"},
	}, builtins, MCPServerOpts{
		TransportType: "sse",
		Endpoint:      "https://example.com/mcp",
		PolicyUpdater: func(oldID, newID string) {
			policyOldID = oldID
			policyNewID = newID
		},
	})

	require.Empty(t, newCollisions, "rename registration must not produce collisions (old server evicted first)")
	assert.Equal(t, "old-server", policyOldID, "PolicyUpdater must receive old serverID")
	assert.Equal(t, "new-server", policyNewID, "PolicyUpdater must receive new serverID")

	all := reg.All()
	require.Len(t, all, 3, "new-server tools must be registered (old evicted)")
	// old-server must be gone
	_, stillHasOld := reg.byServer["old-server"]
	assert.False(t, stillHasOld, "old-server must be fully evicted")
}

// TestMCPRegistry_RenameDetection covers four sub-cases:
//  1. fresh add — no rename
//  2. reconnect-same-name — idempotent (no rename, tools updated)
//  3. reconnect-renamed — eviction+addition under one lock
//  4. conflict — rename collides with existing different server (different fingerprint)
//
// BDD: see sub-tests.
// Traces to: pkg/tools/mcp_registry.go — RegisterServerToolsWithOpts (FR-083).
func TestMCPRegistry_RenameDetection(t *testing.T) {
	t.Run("fresh_add_no_rename", func(t *testing.T) {
		builtins := NewBuiltinRegistry()
		reg := NewMCPRegistry()

		called := false
		collisions := reg.RegisterServerToolsWithOpts("server-a", []Tool{
			&mcpTestTool{name: "a.tool"},
		}, builtins, MCPServerOpts{
			TransportType: "stdio",
			Endpoint:      "/usr/bin/mcpserver",
			PolicyUpdater: func(_, _ string) { called = true },
		})
		require.Empty(t, collisions)
		assert.False(t, called, "PolicyUpdater must NOT be called on fresh add")
		require.Len(t, reg.All(), 1)
	})

	t.Run("reconnect_same_name_idempotent", func(t *testing.T) {
		builtins := NewBuiltinRegistry()
		reg := NewMCPRegistry()

		opts := MCPServerOpts{TransportType: "sse", Endpoint: "https://same.example/mcp"}

		// First registration.
		reg.RegisterServerToolsWithOpts("server-b", []Tool{&mcpTestTool{name: "b.tool"}}, builtins, opts)
		// Second registration — same fingerprint, same serverID.
		called := false
		collisions := reg.RegisterServerToolsWithOpts("server-b", []Tool{
			&mcpTestTool{name: "b.tool"},
			&mcpTestTool{name: "b.extra"},
		}, builtins, MCPServerOpts{
			TransportType: opts.TransportType,
			Endpoint:      opts.Endpoint,
			PolicyUpdater: func(_, _ string) { called = true },
		})
		require.Empty(t, collisions)
		assert.False(t, called, "PolicyUpdater must NOT be called on same-name reconnect")
		require.Len(t, reg.All(), 2, "tool list must be updated to 2 tools")
	})

	t.Run("reconnect_renamed_eviction_and_addition", func(t *testing.T) {
		builtins := NewBuiltinRegistry()
		reg := NewMCPRegistry()

		// Register "alpha" at a fingerprint.
		reg.RegisterServerToolsWithOpts("alpha", []Tool{&mcpTestTool{name: "c.tool"}}, builtins, MCPServerOpts{
			TransportType: "http",
			Endpoint:      "https://renamed.example/mcp",
		})

		// Register "beta" at the same fingerprint → rename alpha→beta.
		renameCalled := false
		collisions := reg.RegisterServerToolsWithOpts(
			"beta",
			[]Tool{&mcpTestTool{name: "c.tool"}},
			builtins,
			MCPServerOpts{
				TransportType: "http",
				Endpoint:      "https://renamed.example/mcp",
				PolicyUpdater: func(oldID, newID string) {
					renameCalled = true
					assert.Equal(t, "alpha", oldID)
					assert.Equal(t, "beta", newID)
				},
			},
		)
		require.Empty(t, collisions, "after eviction of old, tool name is free")
		assert.True(t, renameCalled, "PolicyUpdater must be called on rename")

		// alpha must be gone, beta must be present.
		_, alphaPresent := reg.byServer["alpha"]
		_, betaPresent := reg.byServer["beta"]
		assert.False(t, alphaPresent, "alpha must be evicted")
		assert.True(t, betaPresent, "beta must be registered")
	})

	t.Run("conflict_different_fingerprint_first_wins", func(t *testing.T) {
		builtins := NewBuiltinRegistry()
		reg := NewMCPRegistry()

		// Register "server-1" at fingerprint A with "shared.tool".
		reg.RegisterServerToolsWithOpts("server-1", []Tool{&mcpTestTool{name: "shared.tool"}}, builtins, MCPServerOpts{
			TransportType: "sse",
			Endpoint:      "https://endpoint-a.example/mcp",
		})
		// Register "server-2" at a DIFFERENT fingerprint, same tool name → first-wins collision.
		collisions := reg.RegisterServerToolsWithOpts(
			"server-2",
			[]Tool{&mcpTestTool{name: "shared.tool"}},
			builtins,
			MCPServerOpts{
				TransportType: "sse",
				Endpoint:      "https://endpoint-b.example/mcp",
			},
		)
		require.Len(t, collisions, 1, "different-fingerprint collision must be recorded")
		assert.Equal(t, "server-1", collisions[0].ConflictWith, "first server must win")

		// server-1's tool still accessible.
		got, ok := reg.Get("shared.tool")
		require.True(t, ok)
		_ = got
	})
}

// --- M4: requires_admin_ask opt-in tests (FR-064) ---

// TestMCPRegistry_RequiresAdminAsk verifies that an MCP server registered with
// requires_admin_ask: ["dangerous_tool"] produces a tool whose RequiresAdminAsk()
// returns true, while sibling tools return false (FR-064).
//
// BDD: Given a server config with requires_admin_ask: ["dangerous_tool"],
//
//	When its tools are registered via RegisterServerToolsWithOpts,
//	Then dangerous_tool.RequiresAdminAsk() == true;
//	And safe_tool.RequiresAdminAsk() == false.
//
// Traces to: pkg/tools/mcp_registry.go — mcpAdminAskTool wrapper (FR-064).
func TestMCPRegistry_RequiresAdminAsk(t *testing.T) {
	builtins := NewBuiltinRegistry()
	reg := NewMCPRegistry()

	collisions := reg.RegisterServerToolsWithOpts("my-server", []Tool{
		&mcpTestTool{name: "dangerous_tool"},
		&mcpTestTool{name: "safe_tool"},
		&mcpTestTool{name: "another_safe"},
	}, builtins, MCPServerOpts{
		RequiresAdminAsk: []string{"dangerous_tool"},
	})
	require.Empty(t, collisions)

	dangerousTool, ok := reg.Get("dangerous_tool")
	require.True(t, ok)
	safeTool, ok2 := reg.Get("safe_tool")
	require.True(t, ok2)
	anotherSafe, ok3 := reg.Get("another_safe")
	require.True(t, ok3)

	assert.True(t, dangerousTool.RequiresAdminAsk(),
		"dangerous_tool must have RequiresAdminAsk()==true (FR-064 opt-in)")
	assert.False(t, safeTool.RequiresAdminAsk(),
		"safe_tool must have RequiresAdminAsk()==false (not in requires_admin_ask list)")
	assert.False(t, anotherSafe.RequiresAdminAsk(),
		"another_safe must have RequiresAdminAsk()==false")
}
