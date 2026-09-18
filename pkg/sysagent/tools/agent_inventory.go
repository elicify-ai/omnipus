package systools

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// AgentConfigInventory is a snapshot of installed skills and live connector
// inventory used to validate create_agent/update_agent assignments. Callers
// must capture it before taking the agentstore lock so registry/config mutexes
// never nest under that lock.
type AgentConfigInventory struct {
	// SkillsListed is true when this snapshot came from a real inventory
	// listing (possibly empty). False means inventory was unavailable — not
	// "nothing is installed".
	SkillsListed  bool
	Skills        map[string]struct{}
	ConfiguredMCP map[string]struct{}
	LiveMCPTools  map[string]map[string]struct{}
}

func fieldErr(field, reason string) error {
	return &agentmutation.FieldError{Code: agentmutation.InvalidInput, Fields: []string{field}, Reason: reason}
}

func (d *Deps) currentInventory() AgentConfigInventory {
	if d != nil && d.AgentConfigInventory != nil {
		return d.AgentConfigInventory()
	}
	inv := AgentConfigInventory{
		SkillsListed:  false,
		Skills:        map[string]struct{}{},
		ConfiguredMCP: map[string]struct{}{},
		LiveMCPTools:  map[string]map[string]struct{}{},
	}
	if d != nil && d.GetCfg != nil {
		if cfg := d.GetCfg(); cfg != nil {
			for id := range cfg.Tools.MCP.Servers {
				inv.ConfiguredMCP[id] = struct{}{}
			}
		}
	}
	return inv
}

// CollectLiveMCPNames returns the public registry name and the remote MCP
// tool name, matching pkg/tools mcpAssignmentAllows (name == remote || name == tool.Name()).
func CollectLiveMCPNames(tool tools.Tool) []string {
	if tool == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	add(tool.Name())
	if src, ok := tool.(tools.MCPSource); ok {
		_, remote := src.MCPSource()
		add(remote)
	}
	return out
}

func validateSkillAssignment(ids []string, inv AgentConfigInventory) error {
	if len(ids) == 0 {
		return nil
	}
	if !inv.SkillsListed {
		return fieldErr("skills", "skill inventory is unavailable")
	}
	for _, id := range ids {
		if _, ok := inv.Skills[id]; !ok {
			return fieldErr("skills", fmt.Sprintf("unknown skill id: %q", id))
		}
	}
	return nil
}

func validateMCPAssignment(bindings []config.AgentMCPServerBinding, inv AgentConfigInventory) error {
	for _, b := range bindings {
		if _, ok := inv.ConfiguredMCP[b.ID]; !ok {
			return fieldErr("mcp_servers", fmt.Sprintf("MCP server %q is not configured", b.ID))
		}
		if !b.ToolsSpecified {
			continue
		}
		live := inv.LiveMCPTools[b.ID]
		for _, name := range b.Tools {
			if name == "*" {
				continue
			}
			if _, ok := live[name]; !ok {
				return fieldErr("mcp_servers", fmt.Sprintf("MCP tool %q is not available on server %q", name, b.ID))
			}
		}
	}
	return nil
}

func knownToolPolicies(d *Deps) map[string]struct{} {
	known := map[string]struct{}{}
	if d == nil || d.GetCfg == nil {
		return known
	}
	if cfg := d.GetCfg(); cfg != nil {
		for name := range cfg.Sandbox.ToolPolicies {
			known[name] = struct{}{}
		}
	}
	return known
}
