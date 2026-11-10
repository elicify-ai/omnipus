// Package skills — MCP tool registration bridge.
//
// MCPBridge queries a connected MCP manager for available tool definitions
// and converts them into DiscoveredTool entries that the skill auto-discovery
// pipeline can register (subject to policy enforcement — SEC-04, SEC-07).
package skills

import "github.com/modelcontextprotocol/go-sdk/mcp"

// DiscoveredTool represents a candidate tool reported by an MCP server.
// Discovery does not grant permission to execute it (SEC-04, SEC-07).
type DiscoveredTool struct {
	// SkillName is the name of the server that declared this tool.
	SkillName string
	// ToolName is the declared tool identifier.
	ToolName string
	// Source identifies the declaring server ("mcp:<serverName>").
	Source string
}

// MCPToolLister is the subset of the MCP manager API that MCPBridge needs.
// pkg/mcp.Manager satisfies this interface; a test double may be used in tests.
type MCPToolLister interface {
	GetAllTools() map[string][]*mcp.Tool
}

// MCPBridge translates MCP server tool definitions into DiscoveredTool entries.
type MCPBridge struct {
	lister MCPToolLister
}

// NewMCPBridge creates an MCPBridge backed by the provided MCPToolLister.
func NewMCPBridge(lister MCPToolLister) *MCPBridge {
	return &MCPBridge{lister: lister}
}

// DiscoverMCPTools returns DiscoveredTool entries for every tool exposed by
// all connected MCP servers.  The source field is set to "mcp:<serverName>".
//
// Returning a tool here does NOT grant permissions — callers must apply the
// policy engine before making tools available to agents (SEC-04, SEC-07).
func (b *MCPBridge) DiscoverMCPTools() []DiscoveredTool {
	if b.lister == nil {
		return nil
	}

	allTools := b.lister.GetAllTools()
	var discovered []DiscoveredTool
	for serverName, tools := range allTools {
		for _, t := range tools {
			if t == nil || t.Name == "" {
				continue
			}
			discovered = append(discovered, DiscoveredTool{
				SkillName: serverName,
				ToolName:  t.Name,
				Source:    "mcp:" + serverName,
			})
		}
	}
	return discovered
}
