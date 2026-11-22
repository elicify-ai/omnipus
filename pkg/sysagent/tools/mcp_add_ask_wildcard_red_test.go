// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

// RED tests — Skills & Tools redesign (#1963, epic #1519 page F), order 19
// (tool surface). TestMcpAddTool_WritesAskWildcard: the add_mcp_server agent
// tool writes the same global Ask key the screen's POST writes (FR-045:
// "adding a service (screen or add_mcp_server) MUST store Ask for all its
// current and future tools when no global key covers it"; C-7), and writes
// nothing when a broader global key covers the service (C-18; D8#11).
//
// pkg/sysagent/tools/mcp.go::MCPAddTool.Execute persists the server but
// writes no mcp_<server>_* key at this commit; per the qa-lead RED rule the
// test fails LOUD for that missing behaviour.

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"

	"github.com/stretchr/testify/require"
)

// TestMcpAddTool_WritesAskWildcard (order 19, tool surface of FR-045).
// Traces to: BDD S-59, S-04; spec C-7, C-18; Dataset D8 rows 10-11.
func TestMcpAddTool_WritesAskWildcard(t *testing.T) {
	t.Run("S-59 add_mcp_server writes mcp_<server>_* = ask", func(t *testing.T) {
		deps, cfg := newTestDepsWithCredStore(t)
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), systools.WithCurrentRevisionForTest(add, map[string]any{
			"name":    "notion",
			"command": "npx",
		}))
		require.False(t, res.IsError, "setup add failed: %s", res.ForLLM)
		got := cfg.Sandbox.ToolPolicies["mcp_notion_*"]
		require.Equal(t, "ask", got,
			"C-7/FR-045: add_mcp_server must write the global key mcp_notion_* = ask; got %q", got)
	})

	t.Run("S-59 D8#11 a broader global key covering the service writes nothing", func(t *testing.T) {
		deps, cfg := newTestDepsWithCredStore(t)
		cfg.Sandbox.ToolPolicies["mcp_*"] = "deny"
		add := systools.NewMCPAddTool(deps)
		res := add.Execute(context.Background(), systools.WithCurrentRevisionForTest(add, map[string]any{
			"name":    "notion",
			"command": "npx",
		}))
		require.False(t, res.IsError, "setup add failed: %s", res.ForLLM)
		got, written := cfg.Sandbox.ToolPolicies["mcp_notion_*"]
		require.False(t, written,
			"C-18/D8#11: a covered service must not gain its own key; got %q", got)
	})
}
