package systools

import (
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

var agentUpdateArgNames = map[string]struct{}{
	"id": {}, "revision": {}, "name": {}, "description": {}, "soul": {},
	"model": {}, "provider": {}, "fallback_models": {}, "color": {}, "icon": {},
	"max_tool_iterations": {}, "skills": {}, "mcp_servers": {}, "tool_policy_changes": {},
	"memory_enabled": {}, "default": {}, "voice": {},
	"context_window_override": {}, "model_params": {},
}

var agentCreateArgNames = map[string]struct{}{
	"name": {}, "description": {}, "soul": {}, "model": {}, "color": {}, "icon": {},
	"agent_type": {}, "cli": {}, "cli_path": {}, "provider": {}, "fallback_models": {},
	"heartbeat": {}, "max_tool_iterations": {}, "skills": {}, "mcp_servers": {},
	"tool_policy_changes": {}, "memory_enabled": {}, "default": {}, "voice": {},
	"context_window_override": {}, "model_params": {},
}

func rejectUnknownAgentFields(args map[string]any, allowed map[string]struct{}) error {
	for name := range args {
		if _, ok := allowed[name]; !ok {
			return fieldErr(name, fmt.Sprintf("unknown field %q", name))
		}
	}
	return nil
}

func mutationFieldNames(args map[string]any) []string {
	fields := make([]string, 0, len(args))
	for name := range args {
		if name == "id" || name == "revision" || name == "agent_type" || name == "cli" || name == "cli_path" || name == "heartbeat" {
			continue
		}
		fields = append(fields, name)
	}
	return fields
}

func applyAgentToolArgs(a *config.AgentConfig, args map[string]any, known map[string]struct{}, inv AgentConfigInventory) error {
	if err := applyIdentityAndModelArgs(a, args); err != nil {
		return err
	}
	if err := applyCapabilityArgs(a, args, known, inv); err != nil {
		return err
	}
	return applySharedConfigArgs(a, args)
}

func applyIdentityAndModelArgs(a *config.AgentConfig, args map[string]any) error {
	if err := applyOptionalString(args, "name", func(v string) error {
		if strings.TrimSpace(v) == "" {
			return fieldErr("name", "name must be nonblank")
		}
		a.Name = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "description", func(v string) error {
		a.Description = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "color", func(v string) error {
		if err := validateAgentColor(v); err != nil {
			return err
		}
		a.Color = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "icon", func(v string) error {
		if err := validateAgentIcon(v); err != nil {
			return err
		}
		a.Icon = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "model", func(v string) error {
		if a.Model == nil {
			a.Model = &config.AgentModelConfig{}
		}
		a.Model.Primary = v
		return nil
	}); err != nil {
		return err
	}
	if err := applyOptionalString(args, "provider", func(v string) error {
		if a.Model == nil {
			a.Model = &config.AgentModelConfig{}
		}
		a.Model.Provider = strings.TrimSpace(v)
		return nil
	}); err != nil {
		return err
	}
	if raw, present := args["fallback_models"]; present {
		if err := applyFallbackModels(a, raw); err != nil {
			return err
		}
	}
	if raw, present := args["max_tool_iterations"]; present {
		n, err := jsonInt(raw, "max_tool_iterations")
		if err != nil {
			return err
		}
		if n < 0 {
			return fieldErr("max_tool_iterations", "max_tool_iterations must be >= 0")
		}
		a.MaxToolIterations = n
	}
	return nil
}

func applyCapabilityArgs(a *config.AgentConfig, args map[string]any, known map[string]struct{}, inv AgentConfigInventory) error {
	if _, present := args["skills"]; present && a.IsExternalCLIWorker() {
		return fieldErr("skills", "field is unknown or unsupported by this runtime")
	}
	if _, present := args["mcp_servers"]; present && a.IsExternalCLIWorker() {
		return fieldErr("mcp_servers", "field is unknown or unsupported by this runtime")
	}
	if _, present := args["tool_policy_changes"]; present && a.IsExternalCLIWorker() {
		return fieldErr("tool_policy_changes", "field is unknown or unsupported by this runtime")
	}
	if raw, present := args["skills"]; present {
		vals, err := requiredStringArray(raw, "skills")
		if err != nil {
			return err
		}
		if err := validateSkillAssignment(vals, inv); err != nil {
			return err
		}
		a.Skills = vals
	}
	if raw, present := args["mcp_servers"]; present {
		vals, err := decodeMCPBindings(raw)
		if err != nil {
			return err
		}
		if err := validateMCPAssignment(vals, inv); err != nil {
			return err
		}
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		a.Tools.MCP.Servers = vals
	}
	if raw, present := args["tool_policy_changes"]; present {
		patch, err := decodePolicyPatch(raw)
		if err != nil {
			return err
		}
		if a.Tools == nil {
			a.Tools = &config.AgentToolsCfg{}
		}
		next, err := agentmutation.ApplyToolPolicyChanges(a.Tools.Builtin.Policies, patch, known)
		if err != nil {
			return err
		}
		a.Tools.Builtin.Policies = next
	}
	return nil
}

func applySharedConfigArgs(a *config.AgentConfig, args map[string]any) error {
	if raw, present := args["memory_enabled"]; present {
		b, ok := raw.(bool)
		if !ok {
			return fieldErr("memory_enabled", "memory_enabled must be a boolean")
		}
		a.MemoryEnabled = &b
	}
	if raw, present := args["default"]; present {
		b, ok := raw.(bool)
		if !ok {
			return fieldErr("default", "default must be a boolean")
		}
		if b && a.IsWorker() {
			return fieldErr("default", "a worker agent cannot be set as the default agent (workers are not chat targets)")
		}
		a.Default = b
	}
	if err := applyOptionalString(args, "voice", func(v string) error {
		if a.IsWorker() && strings.TrimSpace(v) != "" {
			return fieldErr("voice", "a worker cannot have a per-agent voice (workers are not chat personas)")
		}
		a.Voice = v
		return nil
	}); err != nil {
		return err
	}
	if raw, present := args["context_window_override"]; present {
		if err := applyContextWindow(a, raw); err != nil {
			return err
		}
	}
	if raw, present := args["model_params"]; present {
		if err := applyModelParams(a, raw); err != nil {
			return err
		}
	}
	return nil
}

func applyOptionalString(args map[string]any, key string, apply func(string) error) error {
	raw, present := args[key]
	if !present {
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		return fieldErr(key, key+" must be a string")
	}
	return apply(s)
}

func applyContextWindow(a *config.AgentConfig, raw any) error {
	if raw == nil {
		a.ContextWindowOverride = nil
		return nil
	}
	n, err := jsonInt(raw, "context_window_override")
	if err != nil {
		return err
	}
	if n <= 0 {
		return fieldErr("context_window_override", "context_window_override must be a positive integer")
	}
	a.ContextWindowOverride = &n
	return nil
}

func applyModelParams(a *config.AgentConfig, raw any) error {
	m, ok := raw.(map[string]any)
	if !ok {
		return fieldErr("model_params", "model_params must be a non-null object")
	}
	for key := range m {
		if key != "temperature" && key != "max_tokens" {
			return fieldErr("model_params", fmt.Sprintf("unknown model_params field %q", key))
		}
	}
	merged := &config.AgentModelParams{}
	if a.ModelParams != nil {
		cp := *a.ModelParams
		merged = &cp
	}
	if value, present := m["temperature"]; present {
		f, ok := value.(float64)
		if !ok {
			return fieldErr("model_params", "temperature must be a number")
		}
		merged.Temperature = &f
	}
	if value, present := m["max_tokens"]; present {
		n, err := jsonInt(value, "model_params.max_tokens")
		if err != nil {
			return err
		}
		merged.MaxTokens = &n
	}
	a.ModelParams = merged
	return nil
}

func jsonInt(raw any, field string) (int, error) {
	f, ok := raw.(float64)
	if !ok {
		return 0, fieldErr(field, field+" must be an integer")
	}
	n := int(f)
	if float64(n) != f {
		return 0, fieldErr(field, field+" must be an integer")
	}
	return n, nil
}

func requiredStringArray(raw any, field string) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fieldErr(field, field+" must be a non-null array")
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fieldErr(field, field+" entries must be nonblank strings")
		}
		out = append(out, s)
	}
	return out, nil
}

func decodeMCPBindings(raw any) ([]config.AgentMCPServerBinding, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fieldErr("mcp_servers", "mcp_servers must be a non-null array")
	}
	out := make([]config.AgentMCPServerBinding, 0, len(arr))
	seen := map[string]struct{}{}
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fieldErr("mcp_servers", "mcp_servers entries must be objects")
		}
		for key := range m {
			if key != "id" && key != "tools" {
				return nil, fieldErr("mcp_servers", fmt.Sprintf("unknown mcp_servers field %q", key))
			}
		}
		id, okID := m["id"].(string)
		if !okID {
			return nil, fieldErr("mcp_servers", "mcp_servers.id must be a string")
		}
		b := config.AgentMCPServerBinding{ID: id}
		if toolsRaw, present := m["tools"]; present {
			vals, err := requiredStringArray(toolsRaw, "mcp_servers.tools")
			if err != nil {
				return nil, err
			}
			b.Tools, b.ToolsSpecified = vals, true
		}
		if err := config.ValidateAgentMCPServerBinding(b); err != nil {
			return nil, fieldErr("mcp_servers", err.Error())
		}
		if _, dup := seen[id]; dup {
			return nil, fieldErr("mcp_servers", fmt.Sprintf("duplicate mcp server %q", id))
		}
		seen[id] = struct{}{}
		out = append(out, b)
	}
	return out, nil
}

func decodePolicyPatch(raw any) (agentmutation.ToolPolicyChanges, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return agentmutation.ToolPolicyChanges{}, fieldErr("tool_policy_changes", "tool_policy_changes must be a non-null object")
	}
	for key := range m {
		if key != "set" && key != "remove" {
			return agentmutation.ToolPolicyChanges{}, fieldErr("tool_policy_changes", fmt.Sprintf("unknown tool_policy_changes field %q", key))
		}
	}
	p := agentmutation.ToolPolicyChanges{}
	if setRaw, present := m["set"]; present {
		setMap, ok := setRaw.(map[string]any)
		if !ok {
			return p, fieldErr("tool_policy_changes", "tool_policy_changes.set must be an object")
		}
		p.Set = map[string]config.ToolPolicy{}
		for name, v := range setMap {
			s, ok := v.(string)
			if !ok {
				return p, fieldErr("tool_policy_changes", fmt.Sprintf("policy for %s must be a string", name))
			}
			p.Set[name] = config.ToolPolicy(s)
		}
	}
	if remRaw, present := m["remove"]; present {
		vals, err := requiredStringArray(remRaw, "tool_policy_changes.remove")
		if err != nil {
			return p, err
		}
		p.Remove = vals
	}
	return p, nil
}

func requireDefaultWriter(deps *Deps) error {
	if deps == nil || deps.MutateConfig == nil {
		return fieldErr("default", "default singleton writer is not wired")
	}
	return nil
}

func defaultSingletonPartialResult(id, revision string, fields []string) *tools.ToolResult {
	return tools.ErrorResult(successJSON(map[string]any{
		"id":                 id,
		"code":               "SAVE_FAILED",
		"message":            "agent was saved but the default-agent singleton could not be updated. Read current state before retrying.",
		"persistence_status": agentstore.PersistencePartial,
		"activation_status":  agentstore.ActivationNotAttempted,
		"revision":           revision,
		"changed_fields":     fields,
		"error_stage":        "defaults_singleton",
	}))
}

func writeDefaultSingleton(deps *Deps, id string, want bool) error {
	if err := requireDefaultWriter(deps); err != nil {
		return err
	}
	return deps.WithConfig(func(cfg *config.Config) error {
		cur := cfg.Agents.Defaults.DefaultAgentID
		if want {
			if cur != id {
				cfg.Agents.Defaults.DefaultAgentID = id
			}
			return nil
		}
		if cur == id {
			cfg.Agents.Defaults.DefaultAgentID = ""
		}
		return nil
	})
}

func publishAgentActivation(deps *Deps, id string) (agentstore.ActivationStatus, string) {
	if deps != nil && deps.UpsertAgentFastFunc != nil {
		if err := deps.UpsertAgentFastFunc(id); err != nil {
			return agentstore.ActivationFailed, err.Error()
		}
		return agentstore.ActivationActive, ""
	}
	if deps != nil && deps.ReloadFunc != nil {
		if err := deps.ReloadFunc(); err != nil {
			return agentstore.ActivationFailed, err.Error()
		}
		return agentstore.ActivationActive, ""
	}
	return agentstore.ActivationNotAttempted, ""
}
