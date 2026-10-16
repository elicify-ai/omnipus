package skills

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockMCPLister is a test double for MCPToolLister that returns a static tool map.
type mockMCPLister struct {
	tools map[string][]*mcp.Tool
}

func (m *mockMCPLister) GetAllTools() map[string][]*mcp.Tool {
	return m.tools
}

// TestAutoDiscoveryFromMCP verifies that MCPBridge.DiscoverMCPTools returns
// DiscoveredTool entries for every tool exposed by connected MCP servers.
// Traces to: wave3-skill-ecosystem-spec.md line 858 (Test #28: TestAutoDiscoveryFromMCP)
// BDD: Given a connected MCP server "aws-mcp" exposing tools [list_buckets, list_ec2],
// When MCPBridge.DiscoverMCPTools() is called,
// Then two DiscoveredTool entries are returned with Source = "mcp:aws-mcp".

func TestAutoDiscoveryFromMCP(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 858 (Test #28)
	lister := &mockMCPLister{
		tools: map[string][]*mcp.Tool{
			"aws-mcp": {
				{Name: "list_buckets"},
				{Name: "list_ec2"},
			},
		},
	}

	bridge := NewMCPBridge(lister)
	discovered := bridge.DiscoverMCPTools()

	require.Len(t, discovered, 2, "must discover one entry per MCP tool")

	toolNames := make(map[string]bool)
	for _, dt := range discovered {
		toolNames[dt.ToolName] = true
		assert.Equal(t, "aws-mcp", dt.SkillName, "SkillName must be the MCP server name")
		assert.Equal(t, "mcp:aws-mcp", dt.Source, "Source must be 'mcp:<serverName>'")
	}

	assert.True(t, toolNames["list_buckets"])
	assert.True(t, toolNames["list_ec2"])
}

// TestAutoDiscoveryFromMCP_NilTools verifies graceful handling when MCP server
// exposes nil tool entries.
// Traces to: wave3-skill-ecosystem-spec.md line 858 (Test #28 — edge case: nil entries)

func TestAutoDiscoveryFromMCP_NilTools(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 858 (edge case: nil tool in list)
	lister := &mockMCPLister{
		tools: map[string][]*mcp.Tool{
			"server-a": {
				{Name: "valid-tool"},
				nil,        // nil entry must be skipped
				{Name: ""}, // empty name must be skipped
			},
		},
	}

	bridge := NewMCPBridge(lister)
	discovered := bridge.DiscoverMCPTools()

	require.Len(t, discovered, 1, "nil and empty-name MCP tools must be skipped")
	assert.Equal(t, "valid-tool", discovered[0].ToolName)
}

// TestAutoDiscoveryFromMCP_NilLister verifies graceful handling when no MCP manager
// is connected (nil lister).
// Traces to: wave3-skill-ecosystem-spec.md line 858 (edge case: nil lister)

func TestAutoDiscoveryFromMCP_NilLister(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 858 (edge case: nil lister)
	bridge := NewMCPBridge(nil)
	discovered := bridge.DiscoverMCPTools()
	assert.Nil(t, discovered, "nil lister must return nil (no panic)")
}

// TestAutoDiscoveryFromMCP_MultipleServers verifies that DiscoverMCPTools aggregates
// tools from all connected MCP servers.
// Traces to: wave3-skill-ecosystem-spec.md line 858 (Test #28 — multiple servers)

func TestAutoDiscoveryFromMCP_MultipleServers(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 858 (Test #28)
	lister := &mockMCPLister{
		tools: map[string][]*mcp.Tool{
			"server-a": {{Name: "tool-a1"}, {Name: "tool-a2"}},
			"server-b": {{Name: "tool-b1"}},
		},
	}

	bridge := NewMCPBridge(lister)
	discovered := bridge.DiscoverMCPTools()

	assert.Len(t, discovered, 3, "must aggregate tools from all MCP servers")

	sources := make(map[string]bool)
	for _, dt := range discovered {
		sources[dt.Source] = true
	}
	assert.True(t, sources["mcp:server-a"], "server-a tools must have Source=mcp:server-a")
	assert.True(t, sources["mcp:server-b"], "server-b tools must have Source=mcp:server-b")
}
