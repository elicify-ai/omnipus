// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---- remove_skill ----

type SkillRemoveTool struct{ deps *Deps }

func NewSkillRemoveTool(d *Deps) *SkillRemoveTool { return &SkillRemoveTool{deps: d} }
func (t *SkillRemoveTool) Name() string           { return "remove_skill" }
func (t *SkillRemoveTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *SkillRemoveTool) Description() string {
	return "Remove an installed skill. Parameters: name (required), confirm (bool, must be true)."
}

func (t *SkillRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":    map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
		},
		"required": []string{"name", "confirm"},
	}
}

func (t *SkillRemoveTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	confirm, _ := args["confirm"].(bool)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to remove a skill", ""))
	}

	if t.deps == nil || t.deps.SkillInstaller == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill installer not configured", "ensure the gateway is started with a valid workspace"))
	}

	slog.Info("sysagent: remove_skill", "name", name)
	if err := t.deps.SkillInstaller.Uninstall(name); err != nil {
		if isNotFound(err) {
			return tools.ErrorResult(errorJSON("NOT_FOUND",
				fmt.Sprintf("skill %q is not installed", name), "use list_skills to see installed skills"))
		}
		slog.Warn("sysagent: remove_skill failed", "name", name, "error", err)
		return tools.ErrorResult(errorJSON("REMOVE_FAILED",
			fmt.Sprintf("could not remove skill %q: %v", name, err), ""))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"success": true,
		"name":    name,
	}))
}

// isNotFound reports whether err is a "not found" error from the skill installer.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// ---- list_skills ----

type SkillListTool struct{ deps *Deps }

func NewSkillListTool(d *Deps) *SkillListTool   { return &SkillListTool{deps: d} }
func (t *SkillListTool) Name() string           { return "list_skills" }
func (t *SkillListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *SkillListTool) Description() string {
	return "List all installed skills. No parameters required."
}

func (t *SkillListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *SkillListTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	if t.deps == nil || t.deps.SkillsLoader == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill loader not configured", "ensure the gateway is started with a valid workspace"))
	}

	slog.Info("sysagent: list_skills")
	infos := t.deps.SkillsLoader.ListSkills()
	skillMaps := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		skillMaps = append(skillMaps, map[string]any{
			// id is the stable slug used to read/activate the skill; name is the
			// human-readable display name (may differ, e.g. "Daily Briefing").
			"id":          info.ID,
			"name":        info.Name,
			"path":        info.Path,
			"source":      info.Source,
			"description": info.Description,
		})
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"skills": skillMaps,
		"count":  len(skillMaps),
	}))
}
