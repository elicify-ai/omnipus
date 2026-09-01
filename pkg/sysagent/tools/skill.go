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
	return "Remove an installed skill. IRREVERSIBLE — the skill's whole directory, including " +
		"any version history it holds, is permanently deleted; there is no undo. This only " +
		"reaches a skill in the operator's installed-skills directory (populated by installing " +
		"from the marketplace) — it does NOT reach a skill authored with create_skill or a user " +
		"override created with edit_skill, which live in a separate directory: naming one here " +
		"returns NOT_FOUND even though list_skills reports it as available. If the removed skill " +
		"was shadowing a lower-priority skill of the same id (an override or a built-in), that " +
		"one becomes visible again. Parameters: name (required, the skill id as " +
		"reported by list_skills — no path separators), confirm (bool, must be true)."
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
	// The LLM supplies this name and it ends up in a filesystem path, so it gets
	// the same entity-id guard every other path-bearing sysagent tool uses — and
	// the same one the REST delete handler applies (validateEntityID). Without
	// it, name=".." reached os.RemoveAll(filepath.Join(workspace,"skills",".."))
	// and deleted the operator's workspace. This is the outer of two layers:
	// skills.SkillInstaller.Uninstall independently confines the resolved path,
	// which is what catches the cases validateID does not (e.g. ".").
	if err := validateID(name); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("invalid skill name %q: a skill id must not contain path separators or %q", name, ".."),
			"use list_skills to see the id of each installed skill"))
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
	return "List all installed skills (procedures, playbooks, and capabilities loaded from SKILL.md files) " +
		"available to you right now, with each skill's id, name, and description. Use find_skills instead " +
		"to search the marketplace for skills that are NOT yet installed. No parameters required."
}

func (t *SkillListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *SkillListTool) Execute(ctx context.Context, _ map[string]any) *tools.ToolResult {
	if t.deps == nil || t.deps.SkillsLoader == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill loader not configured", "ensure the gateway is started with a valid workspace"))
	}

	slog.Info("sysagent: list_skills")
	infos := t.deps.SkillsLoader.ListSkills()
	// ADR-072 D4/N1 (FR-025): the same grant predicate the menu uses gates
	// this listing too — a slug the acting agent may not use must not appear
	// here either. grantPredicateFor returns nil (no filtering) only when
	// there is no resolvable agent context at all (agent id absent from ctx,
	// or config/agent unavailable) — a wiring gap, not a real "ungranted"
	// posture; see its own doc comment for why that fallback exists.
	allow := grantPredicateFor(t.deps, ctx)
	skillMaps := make([]map[string]any, 0, len(infos))
	for _, info := range infos {
		if allow != nil && !allow(info.ID) {
			continue
		}
		skillMaps = append(skillMaps, map[string]any{
			// id is the stable slug used to read/activate the skill; name is the
			// human-readable display name (may differ, e.g. "Daily Briefing").
			//
			// "path" is intentionally OMITTED (ADR-072 D4/N1, FR-006/FR-025):
			// a filesystem location is a load-bearing disclosure this listing
			// must not carry now that skill content is reached exclusively
			// through the Skill tool rather than by an agent reading the path
			// itself.
			"id":          info.ID,
			"name":        info.Name,
			"source":      info.Source,
			"description": info.Description,
		})
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"skills": skillMaps,
		"count":  len(skillMaps),
	}))
}

// grantPredicateFor resolves the acting agent's per-agent skill grant list
// (ADR-072 D4/D5: the registry/builtin shelves' grant instrument — a
// project skill's grant is the mount itself, D4.1, and is out of this
// sysagent tool's reach since it has no workspace/mount context to consult)
// directly from the live config, with no per-tool wiring needed.
//
// Returns nil — meaning "do not filter" — only when there is no resolvable
// agent context: deps/GetCfg unavailable, or no agent id was carried on ctx
// via tools.WithAgentID at all. That is a caller-wiring gap, not a security
// posture, and list_skills has always been reachable without a caller-
// supplied agent id in this package's existing tests and call sites that
// predate this ADR. Once an agent id IS resolvable, D5's real semantics
// apply: an agent with an absent grant list, an empty one, or one not found
// in the live roster is granted NOTHING (FR-025/FR-032/FR-033), matched
// case-insensitively.
func grantPredicateFor(deps *Deps, ctx context.Context) func(slug string) bool {
	if deps == nil || deps.GetCfg == nil {
		return nil
	}
	agentID := strings.TrimSpace(tools.ToolAgentID(ctx))
	if agentID == "" {
		return nil
	}
	cfg := deps.GetCfg()
	if cfg == nil {
		return nil
	}
	for _, a := range cfg.Agents.List {
		if a.ID != agentID {
			continue
		}
		granted := make(map[string]struct{}, len(a.Skills))
		for _, s := range a.Skills {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" {
				granted[s] = struct{}{}
			}
		}
		return func(slug string) bool {
			_, ok := granted[strings.ToLower(strings.TrimSpace(slug))]
			return ok
		}
	}
	// Agent id present but not found in the live roster — deny everything
	// rather than fail open on an identity that does not resolve.
	return func(string) bool { return false }
}
