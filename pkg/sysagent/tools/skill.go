// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/skills"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ---- system.skill.install ----

type SkillInstallTool struct{ deps *Deps }

func NewSkillInstallTool(d *Deps) *SkillInstallTool { return &SkillInstallTool{deps: d} }
func (t *SkillInstallTool) Name() string            { return "system.skill.install" }
func (t *SkillInstallTool) Scope() tools.ToolScope  { return tools.ScopeCore }
func (t *SkillInstallTool) Description() string {
	return "Install a skill from ClawHub.\nParameters: name (required), agent_ids (optional), credentials (optional)."
}

func (t *SkillInstallTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        map[string]any{"type": "string"},
			"agent_ids":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"credentials": map[string]any{"type": "object"},
		},
		"required": []string{"name"},
	}
}

func (t *SkillInstallTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	var agentIDs []string
	if v, ok := args["agent_ids"].([]any); ok {
		for _, a := range v {
			if s, ok := a.(string); ok {
				agentIDs = append(agentIDs, s)
			}
		}
	}

	if t.deps == nil || t.deps.SkillInstaller == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill installer not configured", "ensure the gateway is started with a valid workspace"))
	}

	slog.Info("sysagent: system.skill.install", "name", name, "agent_ids", agentIDs)
	if err := t.deps.SkillInstaller.InstallFromGitHub(ctx, name); err != nil {
		slog.Warn("sysagent: system.skill.install failed", "name", name, "error", err)
		return tools.ErrorResult(errorJSON("INSTALL_FAILED",
			fmt.Sprintf("could not install skill %q: %v", name, err), "check the skill name or GitHub URL"))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"success":         true,
		"name":            name,
		"agents_assigned": agentIDs,
	}))
}

// ---- system.skill.remove ----

type SkillRemoveTool struct{ deps *Deps }

func NewSkillRemoveTool(d *Deps) *SkillRemoveTool { return &SkillRemoveTool{deps: d} }
func (t *SkillRemoveTool) Name() string           { return "system.skill.remove" }
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

	slog.Info("sysagent: system.skill.remove", "name", name)
	if err := t.deps.SkillInstaller.Uninstall(name); err != nil {
		if isNotFound(err) {
			return tools.ErrorResult(errorJSON("NOT_FOUND",
				fmt.Sprintf("skill %q is not installed", name), "use system.skill.list to see installed skills"))
		}
		slog.Warn("sysagent: system.skill.remove failed", "name", name, "error", err)
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

// ---- system.skill.search ----

type SkillSearchTool struct{ deps *Deps }

func NewSkillSearchTool(d *Deps) *SkillSearchTool { return &SkillSearchTool{deps: d} }
func (t *SkillSearchTool) Name() string           { return "system.skill.search" }
func (t *SkillSearchTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *SkillSearchTool) Description() string {
	return "Search ClawHub for skills.\nParameters: query (required), sort (trending/new/popular), limit."
}

func (t *SkillSearchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string"},
			"sort":  map[string]any{"type": "string"},
			"limit": map[string]any{"type": "integer"},
		},
		"required": []string{"query"},
	}
}

func (t *SkillSearchTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	query, _ := args["query"].(string)
	if query == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "query is required", ""))
	}
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	if t.deps == nil || t.deps.RegistryManager == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill registry not configured", "ensure skills.registries.clawhub.enabled=true in config"))
	}

	slog.Info("sysagent: system.skill.search", "query", query, "limit", limit)
	results, err := t.deps.RegistryManager.SearchAll(ctx, query, limit)
	if err != nil {
		var partialErr *skills.PartialSearchError
		if errors.As(err, &partialErr) {
			// Partial results — some registries failed. Return what we have with a note.
			return tools.NewToolResult(successJSON(map[string]any{
				"results": searchResultsToMaps(results),
				"query":   query,
				"partial": true,
				"note":    fmt.Sprintf("partial results: one or more registries failed (%v)", partialErr.Cause),
			}))
		}
		slog.Warn("sysagent: system.skill.search failed", "query", query, "error", err)
		return tools.ErrorResult(errorJSON("SEARCH_FAILED",
			fmt.Sprintf("search failed: %v", err), "check registry connectivity or try again later"))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"results": searchResultsToMaps(results),
		"query":   query,
	}))
}

// searchResultsToMaps converts []skills.SearchResult to []map[string]any for JSON serialization.
func searchResultsToMaps(results []skills.SearchResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		out = append(out, map[string]any{
			"slug":          r.Slug,
			"display_name":  r.DisplayName,
			"summary":       r.Summary,
			"version":       r.Version,
			"registry_name": r.RegistryName,
			"score":         r.Score,
		})
	}
	return out
}

// ---- system.skill.list ----

type SkillListTool struct{ deps *Deps }

func NewSkillListTool(d *Deps) *SkillListTool   { return &SkillListTool{deps: d} }
func (t *SkillListTool) Name() string           { return "system.skill.list" }
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

	slog.Info("sysagent: system.skill.list")
	infos := t.deps.SkillsLoader.ListSkills()
	skillMaps := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		skillMaps = append(skillMaps, map[string]any{
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
