package tools

import (
	"slices"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// MCPSource identifies the actual connector and remote tool without parsing a
// sanitized public tool name. Unknown MCP implementations fail closed.
type MCPSource interface {
	MCPSource() (serverID, remoteToolName string)
}

// MCPManagementBuiltin marks a static builtin that MANAGES MCP servers
// (add_mcp_server, remove_mcp_server, list_mcp_servers). It carries
// CategoryMCP for grouping, but it is not a tool published by a connected
// server, so a per-agent connector assignment never applies to it.
type MCPManagementBuiltin interface {
	IsMCPManagementBuiltin()
}

// CloneMCPBindings preserves omitted versus explicitly empty tool selections.
// Policy builders call it before publishing their immutable snapshot.
func CloneMCPBindings(bindings []config.AgentMCPServerBinding) []config.AgentMCPServerBinding {
	out := slices.Clone(bindings)
	for i := range out {
		out[i].Tools = slices.Clone(out[i].Tools)
	}
	return out
}

func mcpAssignmentAllows(tool Tool, cfg *ToolPolicyCfg) bool {
	if _, builtin := tool.(MCPManagementBuiltin); builtin {
		// The MCP-management builtins share CategoryMCP for the tool picker
		// but are not connector tools: only their tool policy governs them
		// (#1063 F1 — the fail-closed branch below denied them to every
		// agent while the policy read-back said allow).
		return true
	}
	source, isMCP := tool.(MCPSource)
	if !isMCP {
		return tool.Category() != CategoryMCP
	}
	if cfg == nil {
		return false
	}
	server, remote := source.MCPSource()
	if server == "" || remote == "" {
		return false
	}
	for _, binding := range cfg.MCPServers {
		if binding.ID != server {
			continue
		}
		if !binding.ToolsSpecified && binding.Tools == nil {
			return true
		}
		if len(binding.Tools) == 1 && binding.Tools[0] == "*" {
			return true
		}
		for _, name := range binding.Tools {
			if name == remote || name == tool.Name() {
				return true
			}
		}
	}
	return false
}
