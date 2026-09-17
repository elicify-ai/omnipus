package systools

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type AgentGetTool struct{ deps *Deps }

func NewAgentGetTool(d *Deps) *AgentGetTool    { return &AgentGetTool{deps: d} }
func (t *AgentGetTool) Name() string           { return "get_agent" }
func (t *AgentGetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentGetTool) Description() string {
	return "Read one agent's sanitized configuration, instructions, editable fields, stored assignments, revision, and activation status. Use get_agent_tools for the full tool catalog."
}
func (t *AgentGetTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}
}

func (t *AgentGetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	state, err := agentstore.New(home).ReadState(id)
	if err != nil {
		if errors.Is(err, entity.ErrNotFound) {
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
		}
		return tools.ErrorResult(errorJSON("READ_FAILED", err.Error(), ""))
	}
	overrides := storedOverrideNames(state.Agent)
	resp := map[string]any{
		"id": state.Agent.ID, "name": state.Agent.Name, "description": state.Agent.Description,
		"type": wireAgentType(*state.Agent), "soul": state.Soul, "skills": nonNilStrings(state.Agent.Skills),
		"mcp_servers": storedMCPBindings(state.Agent), "stored_tool_overrides": storedOverrides(state.Agent),
		"override_names": overrides, "model": state.Agent.Model, "revision": state.Revision,
		"activation_status": "active", "editable_fields": editableDescriptors(*state.Agent),
	}
	return tools.NewToolResult(successJSON(resp))
}

type AgentGetToolsTool struct{ deps *Deps }

func NewAgentGetToolsTool(d *Deps) *AgentGetToolsTool { return &AgentGetToolsTool{deps: d} }
func (t *AgentGetToolsTool) Name() string             { return "get_agent_tools" }
func (t *AgentGetToolsTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *AgentGetToolsTool) Description() string {
	return "Read the static and installed connector tool inventory with a target agent's stored assignments and effective policies. This does not grant or execute target tools."
}
func (t *AgentGetToolsTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}, "required": []string{"id"}}
}
func (t *AgentGetToolsTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	state, err := agentstore.New(home).ReadState(id)
	if err != nil {
		return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", err.Error(), "Use list_agents"))
	}
	global := map[string]string{}
	if t.deps.GetCfg != nil {
		if cfg := t.deps.GetCfg(); cfg != nil {
			for name, policy := range cfg.Sandbox.ToolPolicies {
				global[name] = policy
			}
		}
	}
	effective := make(map[string]string, len(global))
	for name, policy := range global {
		effective[name] = policy
	}
	if state.Agent.Tools != nil {
		for name, policy := range state.Agent.Tools.Builtin.Policies {
			effective[name] = strictest(policy, config.ToolPolicy(global[name]))
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": id, "revision": state.Revision, "override_names": storedOverrideNames(state.Agent),
		"stored_tool_overrides": storedOverrides(state.Agent), "mcp_servers": storedMCPBindings(state.Agent),
		"effective_policies": effective, "static_catalog": sortedStringKeys(global),
	}))
}

func storedOverrides(a *config.AgentConfig) map[string]config.ToolPolicy {
	if a.Tools == nil || a.Tools.Builtin.Policies == nil {
		return map[string]config.ToolPolicy{}
	}
	return a.Tools.Builtin.Policies
}
func storedOverrideNames(a *config.AgentConfig) []string {
	names := make([]string, 0, len(storedOverrides(a)))
	for name := range storedOverrides(a) {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func storedMCPBindings(a *config.AgentConfig) []config.AgentMCPServerBinding {
	if a.Tools == nil || a.Tools.MCP.Servers == nil {
		return []config.AgentMCPServerBinding{}
	}
	return a.Tools.MCP.Servers
}
func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func strictest(local, global config.ToolPolicy) string {
	if local == config.ToolPolicyDeny || global == config.ToolPolicyDeny {
		return "deny"
	}
	if local == config.ToolPolicyAsk || global == config.ToolPolicyAsk {
		return "ask"
	}
	if local == config.ToolPolicyAllow {
		return "allow"
	}
	return string(global)
}
func wireAgentType(a config.AgentConfig) string {
	if a.IsExternalCLIWorker() {
		return "subagent_3p"
	}
	if a.IsWorker() {
		return "Subagent"
	}
	if a.IsSystem() {
		return "system"
	}
	if a.Type == config.AgentTypeCore {
		return "core"
	}
	return "Main"
}

func editableDescriptors(a config.AgentConfig) []map[string]any {
	fields := []string{"name", "description", "color", "icon", "soul", "skills", "mcp_servers", "tool_policy_changes", "model", "provider", "fallback_models", "context_window_override", "model_params", "max_tool_iterations", "memory_enabled", "default", "voice", "shell_policy", "type"}
	out := make([]map[string]any, 0, len(fields))
	for _, field := range fields {
		err := agentmutation.ValidateFields(a, []string{field})
		d := map[string]any{"name": field, "editable": err == nil}
		if err != nil {
			d["reason"] = err.Error()
		}
		out = append(out, d)
	}
	return out
}
