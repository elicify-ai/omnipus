package systools

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func (t *AgentUpdateTool) executeADR090(args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	revision, _ := args["revision"].(string)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}
	if revision == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "revision is required", "Call get_agent and retry with its revision"))
	}
	fields := suppliedAgentFields(args)
	if len(fields) == 0 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "at least one editable field is required", ""))
	}
	if color, present := args["color"].(string); present {
		if err := validateAgentColor(color); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a 6-digit hex color"))
		}
	}
	if icon, present := args["icon"].(string); present {
		if err := validateAgentIcon(icon); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_ICON", err.Error(), "Use a Phosphor icon name"))
		}
	}
	home, err := resolveOmnipusHome(t.deps.Home)
	if err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR", err.Error(), ""))
	}
	store := agentstore.New(home)
	var soul *string
	if raw, present := args["soul"]; present {
		v, ok := raw.(string)
		if !ok || strings.TrimSpace(v) == "" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", "soul must be a nonblank string", ""))
		}
		soul = &v
	}
	known := map[string]struct{}{}
	if t.deps.GetCfg != nil {
		if cfg := t.deps.GetCfg(); cfg != nil {
			for name := range cfg.Sandbox.ToolPolicies {
				known[name] = struct{}{}
			}
		}
	}
	result, mutateErr := store.MutateState(id, revision, func(a *config.AgentConfig) error {
		if err := agentmutation.ValidateFields(*a, fields); err != nil {
			return err
		}
		return applyAgentToolArgs(a, args, known)
	}, soul)
	if mutateErr != nil {
		var fe *agentmutation.FieldError
		switch {
		case errors.Is(mutateErr, entity.ErrNotFound):
			return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND", fmt.Sprintf("No agent with ID %q", id), "Use list_agents"))
		case errors.Is(mutateErr, agentstore.ErrInvalidRevision):
			return tools.ErrorResult(errorJSON("INVALID_INPUT", mutateErr.Error(), "Call get_agent"))
		case errors.Is(mutateErr, agentstore.ErrRevisionConflict):
			return tools.ErrorResult(errorJSON("CONFLICT", mutateErr.Error(), "Re-read with get_agent and revise the proposal"))
		case errors.As(mutateErr, &fe):
			return tools.ErrorResult(errorJSON(string(fe.Code), fe.Error(), ""))
		default:
			// A failure before staging has changed no files. Preserve explicit
			// partial/complete outcomes supplied by later storage stages.
			if result.PersistenceStatus == "" {
				result.PersistenceStatus = agentstore.PersistenceNone
			}
			if result.ErrorStage == "" {
				result.ErrorStage = "prepare"
			}
			slog.Warn("sysagent: save updated agent failed", "id", id, "stage", result.ErrorStage, "error", mutateErr)
			payload := map[string]any{
				"code":               "SAVE_FAILED",
				"message":            "Agent configuration could not be saved. Read its current state before retrying.",
				"persistence_status": result.PersistenceStatus,
				"activation_status":  result.ActivationStatus,
				"changed_fields":     append([]string{}, result.ChangedFields...),
				"error_stage":        result.ErrorStage,
			}
			if result.Revision != "" {
				payload["revision"] = result.Revision
			}
			return tools.ErrorResult(successJSON(payload))
		}
	}
	activation := "active"
	message := ""
	if t.deps.UpsertAgentFastFunc != nil {
		if err := t.deps.UpsertAgentFastFunc(id); err != nil {
			activation = "failed"
			message = err.Error()
			slog.Warn("sysagent: publish updated agent failed", "id", id, "error", err)
		}
	} else if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			activation = "failed"
			message = err.Error()
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "revision": result.Revision, "persistence_status": "complete", "activation_status": activation, "changed_fields": fields, "message": message}))
}

func suppliedAgentFields(args map[string]any) []string {
	fields := make([]string, 0, len(args))
	for name := range args {
		if name != "id" && name != "revision" {
			fields = append(fields, name)
		}
	}
	sort.Strings(fields)
	return fields
}

func applyAgentToolArgs(a *config.AgentConfig, args map[string]any, known map[string]struct{}) error {
	if v, ok := args["name"].(string); ok {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("name must be nonblank")
		}
		a.Name = v
	}
	if v, ok := args["description"].(string); ok {
		a.Description = v
	}
	if v, ok := args["color"].(string); ok {
		if err := validateAgentColor(v); err != nil {
			return err
		}
		a.Color = v
	}
	if v, ok := args["icon"].(string); ok {
		if err := validateAgentIcon(v); err != nil {
			return err
		}
		a.Icon = v
	}
	if v, ok := args["model"].(string); ok {
		if a.Model == nil {
			a.Model = &config.AgentModelConfig{}
		}
		a.Model.Primary = v
	}
	if v, ok := args["provider"].(string); ok {
		if a.Model == nil {
			a.Model = &config.AgentModelConfig{}
		}
		a.Model.Provider = strings.TrimSpace(v)
	}
	if raw, present := args["fallback_models"]; present {
		if err := applyFallbackModels(a, raw); err != nil {
			return err
		}
	}
	if raw, present := args["skills"]; present {
		vals, err := requiredStringArray(raw, "skills")
		if err != nil {
			return err
		}
		a.Skills = vals
	}
	if raw, present := args["mcp_servers"]; present {
		vals, err := decodeMCPBindings(raw)
		if err != nil {
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
	if v, ok := args["max_tool_iterations"].(float64); ok {
		if v < 0 {
			return fmt.Errorf("max_tool_iterations must be >= 0")
		}
		a.MaxToolIterations = int(v)
	}
	return nil
}

func requiredStringArray(raw any, field string) ([]string, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a non-null array", field)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("%s entries must be nonblank strings", field)
		}
		out = append(out, s)
	}
	return out, nil
}
func decodeMCPBindings(raw any) ([]config.AgentMCPServerBinding, error) {
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("mcp_servers must be a non-null array")
	}
	out := make([]config.AgentMCPServerBinding, 0, len(arr))
	seen := map[string]struct{}{}
	for _, v := range arr {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcp_servers entries must be objects")
		}
		id, _ := m["id"].(string)
		b := config.AgentMCPServerBinding{ID: id}
		if toolsRaw, present := m["tools"]; present {
			vals, err := requiredStringArray(toolsRaw, "mcp_servers.tools")
			if err != nil {
				return nil, err
			}
			b.Tools, b.ToolsSpecified = vals, true
		}
		if err := config.ValidateAgentMCPServerBinding(b); err != nil {
			return nil, err
		}
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("duplicate mcp server %q", id)
		}
		seen[id] = struct{}{}
		out = append(out, b)
	}
	return out, nil
}
func decodePolicyPatch(raw any) (agentmutation.ToolPolicyChanges, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return agentmutation.ToolPolicyChanges{}, fmt.Errorf("tool_policy_changes must be a non-null object")
	}
	p := agentmutation.ToolPolicyChanges{}
	if setRaw, present := m["set"]; present {
		setMap, ok := setRaw.(map[string]any)
		if !ok {
			return p, fmt.Errorf("tool_policy_changes.set must be an object")
		}
		p.Set = map[string]config.ToolPolicy{}
		for name, v := range setMap {
			s, ok := v.(string)
			if !ok {
				return p, fmt.Errorf("policy for %s must be a string", name)
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

var _ tools.Tool = (*AgentUpdateTool)(nil)
