// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/datamodel"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// slugRegexp matches characters that should be replaced in agent name → ID conversion.
var slugRegexp = regexp.MustCompile(`[^a-z0-9]+`)

// hexColorRe validates HTML hex colors like "#22C55E".
var hexColorRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// toSlug converts a display name to a URL-safe slug ID.
func toSlug(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = slugRegexp.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = fmt.Sprintf("agent-%d", rand.Intn(99999))
	}
	return s
}

// validateAgentColor returns an error when color is non-empty and not a valid
// 6-digit hex color. Empty strings pass validation (field is optional).
func validateAgentColor(s string) error {
	if s == "" {
		return nil
	}
	if !hexColorRe.MatchString(s) {
		return fmt.Errorf("invalid color %q: must match ^#[0-9A-Fa-f]{6}$", s)
	}
	return nil
}

// validateAgentIcon returns an error when icon is non-empty and contains
// characters outside the Phosphor icon naming convention (alphanumeric + hyphens,
// max 64 chars). Empty strings pass (field is optional).
func validateAgentIcon(s string) error {
	if s == "" {
		return nil
	}
	if len(s) > 64 {
		return fmt.Errorf("invalid icon %q: must be ≤64 characters", s)
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
			return fmt.Errorf("invalid icon %q: must be alphanumeric + hyphens only", s)
		}
	}
	return nil
}

// setAgentEnabled is the shared implementation for activate and deactivate.
// id must not be empty. Locked (core) agents cannot be toggled.
func setAgentEnabled(deps *Deps, id string, enabled bool) *tools.ToolResult {
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	var found bool
	err := deps.WithConfig(func(cfg *config.Config) error {
		for i, a := range cfg.Agents.List {
			if a.ID == id {
				if a.Locked {
					return fmt.Errorf("agent %q is a locked core agent and cannot be enabled/disabled", id)
				}
				found = true
				cfg.Agents.List[i].Enabled = &enabled
				return nil
			}
		}
		return nil // not found — handled after WithConfig returns
	})
	if err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), "Check disk space and permissions"))
	}
	if !found {
		return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND",
			fmt.Sprintf("No agent with ID %q", id),
			"Use system.agent.list to see available agents",
		))
	}
	slog.Info("sysagent: agent enabled state changed", "id", id, "enabled", enabled)
	return tools.NewToolResult(successJSON(map[string]any{
		"id":      id,
		"enabled": enabled,
	}))
}

// ---- system.agent.create ----

// AgentCreateTool implements system.agent.create per BRD §D.4.2.
type AgentCreateTool struct{ deps *Deps }

func NewAgentCreateTool(d *Deps) *AgentCreateTool { return &AgentCreateTool{deps: d} }

func (t *AgentCreateTool) Name() string           { return "system.agent.create" }
func (t *AgentCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentCreateTool) Description() string {
	return "Create a new custom agent with personality, model, tools, and configuration."
}

func (t *AgentCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			// Mandatory
			"name": map[string]any{"type": "string", "description": "Display name for the new agent"},
			"description": map[string]any{
				"type":        "string",
				"description": "One-line description of the agent's purpose",
			},
			"soul": map[string]any{
				"type":        "string",
				"description": "The agent's personality, role, and behavioral instructions (written to SOUL.md)",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Primary LLM model slug (e.g. 'z-ai/glm-5v-turbo')",
			},
			"color": map[string]any{"type": "string", "description": "Hex avatar color (e.g. '#22C55E')"},
			"icon": map[string]any{
				"type":        "string",
				"description": "Phosphor icon name (e.g. 'robot', 'pencil', 'book')",
			},
			// Optional
			"provider": map[string]any{
				"type":        "string",
				"description": "Provider name for the primary model (e.g. 'openrouter')",
			},
			"model_fallbacks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Fallback model slugs tried in order if primary fails",
			},
			"heartbeat": map[string]any{
				"type":        "string",
				"description": "Proactive scheduling instructions (written to HEARTBEAT.md)",
			},
			"can_delegate_to": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Agent IDs this agent can delegate tasks to. Use ['*'] for all.",
			},
			"max_tool_iterations": map[string]any{
				"type":        "integer",
				"description": "Max tool calls per turn (0 = system default)",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Per-turn timeout in seconds (0 = disabled)",
			},
			"restrict_to_workspace": map[string]any{
				"type":        "boolean",
				"description": "Sandbox file access to agent's workspace only",
			},
		},
		"required": []string{"name", "description", "soul", "model", "color", "icon"},
	}
}

func (t *AgentCreateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	// Validate mandatory fields.
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", "Provide a name for the agent"))
	}
	description, _ := args["description"].(string)
	if strings.TrimSpace(description) == "" {
		return tools.ErrorResult(
			errorJSON("INVALID_INPUT", "description is required", "Provide a one-line description"),
		)
	}
	soul, _ := args["soul"].(string)
	if strings.TrimSpace(soul) == "" {
		return tools.ErrorResult(
			errorJSON(
				"INVALID_INPUT",
				"soul is required",
				"Provide the agent's personality and behavioral instructions",
			),
		)
	}
	model, _ := args["model"].(string)
	if strings.TrimSpace(model) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "model is required", "Provide the LLM model slug"))
	}
	color, _ := args["color"].(string)
	if err := validateAgentColor(color); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a 6-digit hex color, e.g. #22C55E"))
	}
	icon, _ := args["icon"].(string)
	if err := validateAgentIcon(icon); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_ICON", err.Error(), "Use alphanumeric + hyphens, e.g. robot"))
	}

	id := toSlug(name)
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	var finalID string
	err := t.deps.WithConfig(func(cfg *config.Config) error {
		for _, a := range cfg.Agents.List {
			if a.ID == id {
				return fmt.Errorf("AGENT_ALREADY_EXISTS: an agent with ID %q already exists", id)
			}
		}
		enabled := true
		newAgent := config.AgentConfig{
			ID:          id,
			Name:        name,
			Description: description,
			Color:       color,
			Icon:        icon,
			Enabled:     &enabled,
			Model:       &config.AgentModelConfig{Primary: model},
		}
		// Optional: model fallbacks.
		if fb, ok := args["model_fallbacks"].([]any); ok && len(fb) > 0 {
			for _, v := range fb {
				if s, ok := v.(string); ok && s != "" {
					newAgent.Model.Fallbacks = append(newAgent.Model.Fallbacks, s)
				}
			}
		}
		// Optional: delegation targets.
		if dt, ok := args["can_delegate_to"].([]any); ok && len(dt) > 0 {
			for _, v := range dt {
				if s, ok := v.(string); ok && s != "" {
					newAgent.CanDelegateTo = append(newAgent.CanDelegateTo, s)
				}
			}
		}
		// Seed the privilege rail (FR-008/FR-022): custom agents default to
		// system.*: deny so no system tool is reachable without an explicit
		// per-agent allow. The caller's tools config (if any) overrides only
		// the entries it explicitly names — the system.* seed is preserved
		// unless the caller explicitly sets system.* to something else.
		if newAgent.Tools == nil {
			newAgent.Tools = &config.AgentToolsCfg{}
		}
		if newAgent.Tools.Builtin.DefaultPolicy == "" {
			newAgent.Tools.Builtin.DefaultPolicy = config.ToolPolicyAllow
		}
		if newAgent.Tools.Builtin.Policies == nil {
			newAgent.Tools.Builtin.Policies = make(map[string]config.ToolPolicy)
		}
		// Only seed system.*: deny if the caller did not provide an explicit entry.
		if _, hasSystemWildcard := newAgent.Tools.Builtin.Policies["system.*"]; !hasSystemWildcard {
			newAgent.Tools.Builtin.Policies["system.*"] = config.ToolPolicyDeny
		}
		cfg.Agents.List = append(cfg.Agents.List, newAgent)
		finalID = id
		return nil
	})
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "AGENT_ALREADY_EXISTS:") {
			return tools.ErrorResult(errorJSON(
				"AGENT_ALREADY_EXISTS",
				fmt.Sprintf("An agent with ID %q already exists", id),
				"Use system.agent.update to modify the existing agent or choose a different name",
			))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", msg, "Check disk space and permissions"))
	}

	// Create agent workspace and write personality files.
	// Use deps.Home (OMNIPUS_HOME) as base — reliable in containers where HOME may be unset.
	omnipusHome := t.deps.Home
	if omnipusHome == "" {
		if h, err := os.UserHomeDir(); err == nil {
			omnipusHome = h + "/.omnipus"
		} else {
			return tools.ErrorResult(errorJSON("WORKSPACE_ERROR",
				"cannot determine home directory: "+err.Error(),
				"Set OMNIPUS_HOME environment variable"))
		}
	}
	wsPath := omnipusHome + "/agents/" + finalID
	if err := datamodel.InitAgentWorkspace(omnipusHome, finalID); err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR",
			"could not create agent workspace: "+err.Error(),
			"Check disk space and permissions"))
	}

	// Write SOUL.md — this is the agent's personality and is mandatory.
	if err := os.WriteFile(wsPath+"/SOUL.md", []byte(soul), 0o644); err != nil {
		return tools.ErrorResult(errorJSON("WRITE_ERROR",
			"could not write SOUL.md: "+err.Error(),
			"Check disk space and permissions"))
	}
	// Write HEARTBEAT.md if provided.
	if hb, ok := args["heartbeat"].(string); ok && strings.TrimSpace(hb) != "" {
		if err := os.WriteFile(wsPath+"/HEARTBEAT.md", []byte(hb), 0o644); err != nil {
			slog.Warn("sysagent: could not write HEARTBEAT.md", "id", finalID, "error", err)
		}
	}

	// Trigger hot-reload so the new agent is immediately available for chat.
	if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("sysagent: hot-reload after agent create failed — agent available after restart",
				"id", finalID, "error", err)
		}
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"id":     finalID,
		"name":   name,
		"model":  model,
		"type":   string(config.AgentTypeCustom),
		"status": "active",
	}))
}

// ---- system.agent.update ----

// AgentUpdateTool implements system.agent.update per BRD §D.4.2.
type AgentUpdateTool struct{ deps *Deps }

func NewAgentUpdateTool(d *Deps) *AgentUpdateTool { return &AgentUpdateTool{deps: d} }

func (t *AgentUpdateTool) Name() string           { return "system.agent.update" }
func (t *AgentUpdateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentUpdateTool) Description() string {
	return "Update an existing agent's configuration. Only provided fields are changed; omitted fields are left as-is."
}

func (t *AgentUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":          map[string]any{"type": "string", "description": "Agent ID to update"},
			"name":        map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"soul": map[string]any{
				"type":        "string",
				"description": "New personality/instructions (overwrites SOUL.md)",
			},
			"model":                 map[string]any{"type": "string", "description": "New primary model slug"},
			"model_fallbacks":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"provider":              map[string]any{"type": "string"},
			"color":                 map[string]any{"type": "string"},
			"icon":                  map[string]any{"type": "string"},
			"heartbeat":             map[string]any{"type": "string", "description": "New HEARTBEAT.md content"},
			"can_delegate_to":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"max_tool_iterations":   map[string]any{"type": "integer"},
			"timeout_seconds":       map[string]any{"type": "integer"},
			"restrict_to_workspace": map[string]any{"type": "boolean"},
		},
		"required": []string{"id"},
	}
}

func (t *AgentUpdateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	// Path traversal protection: validate id contains no path separators.
	if err := validateID(id); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	// Validate color and icon before any mutation.
	color, colorPresent := args["color"].(string)
	if colorPresent {
		if err := validateAgentColor(color); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_COLOR", err.Error(), "Use a 6-digit hex color, e.g. #22C55E"))
		}
	}
	icon, iconPresent := args["icon"].(string)
	if iconPresent {
		if err := validateAgentIcon(icon); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_ICON", err.Error(), "Use alphanumeric + hyphens, e.g. robot"))
		}
	}

	var updated []string
	var found bool
	err := t.deps.WithConfig(func(cfg *config.Config) error {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID != id {
				continue
			}
			found = true
			a := &cfg.Agents.List[i]
			if a.Locked {
				return fmt.Errorf("agent %q is a locked core agent and cannot be modified", id)
			}
			if v, ok := args["name"].(string); ok && v != "" {
				a.Name = v
				updated = append(updated, "name")
			}
			if v, ok := args["description"].(string); ok && v != "" {
				a.Description = v
				updated = append(updated, "description")
			}
			if colorPresent && color != "" {
				a.Color = color
				updated = append(updated, "color")
			}
			if iconPresent && icon != "" {
				a.Icon = icon
				updated = append(updated, "icon")
			}
			// Model config.
			if v, ok := args["model"].(string); ok && v != "" {
				if a.Model == nil {
					a.Model = &config.AgentModelConfig{}
				}
				a.Model.Primary = v
				updated = append(updated, "model")
			}
			if fb, ok := args["model_fallbacks"].([]any); ok {
				if a.Model == nil {
					a.Model = &config.AgentModelConfig{}
				}
				a.Model.Fallbacks = nil
				for _, v := range fb {
					if s, ok := v.(string); ok && s != "" {
						a.Model.Fallbacks = append(a.Model.Fallbacks, s)
					}
				}
				updated = append(updated, "model_fallbacks")
			}
			// Delegation.
			if dt, ok := args["can_delegate_to"].([]any); ok {
				a.CanDelegateTo = nil
				for _, v := range dt {
					if s, ok := v.(string); ok && s != "" {
						a.CanDelegateTo = append(a.CanDelegateTo, s)
					}
				}
				updated = append(updated, "can_delegate_to")
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	if !found {
		return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND",
			fmt.Sprintf("No agent with ID %q", id),
			"Use system.agent.list to see available agents",
		))
	}

	// Write workspace files if provided. Use deps.Home as base.
	omnipusHome := t.deps.Home
	if omnipusHome == "" {
		if h, err := os.UserHomeDir(); err == nil {
			omnipusHome = h + "/.omnipus"
		}
	}
	wsPath := omnipusHome + "/agents/" + id
	if v, ok := args["soul"].(string); ok && strings.TrimSpace(v) != "" {
		if err := os.MkdirAll(wsPath, 0o700); err != nil {
			return tools.ErrorResult(errorJSON("WRITE_ERROR", "could not update SOUL.md: "+err.Error(), ""))
		} else if err := os.WriteFile(wsPath+"/SOUL.md", []byte(v), 0o644); err != nil {
			return tools.ErrorResult(errorJSON("WRITE_ERROR", "could not update SOUL.md: "+err.Error(), ""))
		} else {
			updated = append(updated, "soul")
		}
	}
	if v, ok := args["heartbeat"].(string); ok && strings.TrimSpace(v) != "" {
		if err := os.MkdirAll(wsPath, 0o700); err == nil {
			if err := os.WriteFile(wsPath+"/HEARTBEAT.md", []byte(v), 0o644); err != nil {
				slog.Warn("sysagent: could not write HEARTBEAT.md", "id", id, "error", err)
			} else {
				updated = append(updated, "heartbeat")
			}
		}
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"id":             id,
		"updated_fields": updated,
	}))
}

// ---- system.agent.delete ----

// AgentDeleteTool implements system.agent.delete per BRD §D.4.2.
type AgentDeleteTool struct{ deps *Deps }

func NewAgentDeleteTool(d *Deps) *AgentDeleteTool { return &AgentDeleteTool{deps: d} }

func (t *AgentDeleteTool) Name() string           { return "system.agent.delete" }
func (t *AgentDeleteTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentDeleteTool) Description() string {
	return "Delete an agent and all its data (sessions, memory, workspace).\nParameters: id (required), confirm (bool, must be true)."
}

func (t *AgentDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
		},
		"required": []string{"id", "confirm"},
	}
}

func (t *AgentDeleteTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	confirm, _ := args["confirm"].(bool)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to delete an agent",
			"Set confirm=true to proceed with deletion",
		))
	}
	var found bool
	err := t.deps.WithConfig(func(cfg *config.Config) error {
		newList := cfg.Agents.List[:0]
		for _, a := range cfg.Agents.List {
			if a.ID == id {
				if a.Locked {
					return fmt.Errorf("agent %q is a locked core agent and cannot be deleted", id)
				}
				found = true
				continue
			}
			newList = append(newList, a)
		}
		cfg.Agents.List = newList
		return nil
	})
	if err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	if !found {
		return tools.ErrorResult(errorJSON("AGENT_NOT_FOUND",
			fmt.Sprintf("No agent with ID %q", id),
			"Use system.agent.list to see available agents",
		))
	}
	// Remove workspace directory (best-effort; failure is non-fatal but logged).
	wsPath := datamodel.AgentWorkspacePath(t.deps.Home, id)
	if err := os.RemoveAll(wsPath); err != nil {
		slog.Warn("sysagent: workspace cleanup incomplete",
			"agent_id", id, "path", wsPath, "error", err)
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"id":      id,
		"deleted": true,
	}))
}

// ---- system.agent.list ----

// AgentListTool implements system.agent.list per BRD §D.4.2.
type AgentListTool struct{ deps *Deps }

func NewAgentListTool(d *Deps) *AgentListTool { return &AgentListTool{deps: d} }

func (t *AgentListTool) Name() string           { return "system.agent.list" }
func (t *AgentListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentListTool) Description() string {
	return "List all agents with their status, model, and task count.\nParameters: status (optional: active/inactive/all, default all)."
}

func (t *AgentListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"active", "inactive", "all"}},
		},
	}
}

func (t *AgentListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	filter, _ := args["status"].(string)
	if filter == "" {
		filter = "all"
	}
	type agentSummary struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Model  string `json:"model,omitempty"`
	}
	cfg := t.deps.GetCfg()
	var result []agentSummary
	for _, a := range cfg.Agents.List {
		status := "active"
		if !a.IsActive() {
			status = "inactive"
		}
		if filter != "all" && filter != status {
			continue
		}
		model := ""
		if a.Model != nil {
			model = a.Model.Primary
		}
		result = append(result, agentSummary{
			ID:   a.ID,
			Name: a.Name,
			Type: string(a.ResolveType(func(id string) bool {
				// Check if agent has Type explicitly set to "core" in config.
				// This avoids importing coreagent (would create import cycle).
				return a.Type == config.AgentTypeCore
			})),
			Status: status,
			Model:  model,
		})
	}
	return tools.NewToolResult(successJSON(map[string]any{"agents": result}))
}

// ---- system.agent.activate ----

// AgentActivateTool implements system.agent.activate per BRD §D.4.2.
// It sets Enabled=true on the agent entry and persists the change via SaveConfig.
type AgentActivateTool struct{ deps *Deps }

func NewAgentActivateTool(d *Deps) *AgentActivateTool { return &AgentActivateTool{deps: d} }

func (t *AgentActivateTool) Name() string           { return "system.agent.activate" }
func (t *AgentActivateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentActivateTool) Description() string {
	return "Activate a core or custom agent, persisting the enabled state.\nParameters: id (required)."
}

func (t *AgentActivateTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *AgentActivateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	return setAgentEnabled(t.deps, id, true)
}

// ---- system.agent.deactivate ----

// AgentDeactivateTool implements system.agent.deactivate per BRD §D.4.2.
// It sets Enabled=false on the agent entry and persists the change via SaveConfig.
type AgentDeactivateTool struct{ deps *Deps }

func NewAgentDeactivateTool(d *Deps) *AgentDeactivateTool { return &AgentDeactivateTool{deps: d} }

func (t *AgentDeactivateTool) Name() string           { return "system.agent.deactivate" }
func (t *AgentDeactivateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentDeactivateTool) Description() string {
	return "Deactivate an agent (makes it unavailable for new sessions), persisting the disabled state.\nParameters: id (required)."
}

func (t *AgentDeactivateTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *AgentDeactivateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	return setAgentEnabled(t.deps, id, false)
}
