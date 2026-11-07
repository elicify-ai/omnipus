package systools

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// toolPermissions is get_agent's `tool_permissions` (r17 §7.12.1, R2-MAJ-005):
// the live, callable tool inventory of agent a. It iterates the central tool
// catalog (built-in plus MCP, resolved at call time), keeps a tool when the
// agent's MCP assignment allows it (denied tools are kept and shown as deny),
// and reports the effective policy (global ceiling and per-agent entries,
// strictest wins, MCP wildcards and God Mode included). Reading a permission
// never grants or executes the tool.
func (t *AgentGetTool) toolPermissions(a *config.AgentConfig) ([]map[string]any, *tools.ToolResult) {
	if t.deps.GetCfg == nil || t.deps.GetCfg() == nil {
		return nil, tools.ErrorResult(errorJSON("CONFIG_UNAVAILABLE", "Global tool policies are unavailable", agentReadRecovery))
	}
	cfg := t.deps.GetCfg()
	if t.deps.ToolCatalog == nil {
		return nil, tools.ErrorResult(errorJSON("CATALOG_UNAVAILABLE", "Tool metadata catalog is unavailable", agentReadRecovery))
	}
	catalog := t.deps.ToolCatalog()
	if len(catalog) == 0 {
		return nil, tools.ErrorResult(errorJSON("CATALOG_UNAVAILABLE", "Tool metadata catalog is unavailable", agentReadRecovery))
	}
	global := make(map[string]string, len(cfg.Sandbox.ToolPolicies))
	for name, policy := range cfg.Sandbox.ToolPolicies {
		global[name] = policy
	}
	stored := storedOverrides(a)
	resolver := runtimePolicyResolver(global, stored, t.deps.godModeActive())
	resolver.MCPServers = tools.CloneMCPBindings(storedMCPBindings(a))
	entries, err := toolPermissionEntries(resolver, stored, catalog)
	if err != nil {
		return nil, tools.ErrorResult(errorJSON("CATALOG_UNAVAILABLE", err.Error(), agentReadRecovery))
	}
	return entries, nil
}

// toolPermissionEntries builds one AgentToolPermission per assignment-allowed
// catalog tool, sorted by name: {tool, effective, override, description?}.
// override is the stored value of the tool's exact key, else null;
// description is at most 40 characters and omitted on deny.
func toolPermissionEntries(resolver *tools.ToolPolicyCfg, stored map[string]config.ToolPolicy, catalog []tools.Tool) ([]map[string]any, error) {
	ordered := append([]tools.Tool(nil), catalog...)
	for _, tool := range ordered {
		if tool == nil || tool.Name() == "" {
			return nil, fmt.Errorf("tool catalog contains missing tool metadata")
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name() < ordered[j].Name() })
	out := make([]map[string]any, 0, len(ordered))
	for _, tool := range ordered {
		// Share the runtime assignment predicate, but do not filter Deny:
		// the reader needs to see denied tools in this inventory.
		if !tools.MCPAssignmentAllows(tool, resolver) {
			continue
		}
		policy := effectivePolicy(resolver, tool.Name())
		entry := map[string]any{"tool": tool.Name(), "effective": policy, "override": nil}
		if v, ok := stored[tool.Name()]; ok {
			entry["override"] = string(v)
		}
		if policy != string(config.ToolPolicyDeny) {
			description := tool.Description()
			if source, ok := tool.(tools.MCPSource); ok {
				server, _ := source.MCPSource()
				description = strings.TrimPrefix(description, "[MCP:"+server+"] ")
			}
			entry["description"] = shortAgentToolDescription(description)
		}
		out = append(out, entry)
	}
	return out, nil
}

// shortAgentToolDescription follows the compact-output sentence and character
// rules. A word that cannot fit is omitted rather than split mid-word.
func shortAgentToolDescription(description string) string {
	text := strings.Join(strings.Fields(description), " ")
	end := len(text)
	for _, boundary := range []string{". ", "! ", "? "} {
		if i := strings.Index(text, boundary); i >= 0 && i+1 < end {
			end = i + 1
		}
	}
	text = text[:end]
	characters := []rune(text)
	if len(characters) <= 40 {
		return text
	}
	cut := 39
	for cut > 0 && !unicode.IsSpace(characters[cut]) {
		cut--
	}
	return strings.TrimSpace(string(characters[:cut])) + "…"
}
