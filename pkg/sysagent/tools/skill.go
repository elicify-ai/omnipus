// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
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
		"reported by list_skills — no path separators), revision (required from the reviewed skill read), confirm (bool, must be true). A conflict requires a fresh read; do not retry the stale deletion."
}

func (t *SkillRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":     map[string]any{"type": "string"},
			"confirm":  map[string]any{"type": "boolean"},
			"revision": map[string]any{"type": "string", "description": "Revision returned by the reviewed skill read."},
		},
		"required": []string{"name", "confirm", "revision"},
	}
}

func (t *SkillRemoveTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	confirm, _ := args["confirm"].(bool)
	revision, _ := args["revision"].(string)
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

	// ADR-072 D6.1 ("remove_skill follows the same rule — deleting a project
	// skill deletes the project's file"): resolve against the current turn's
	// workspace project shelf FIRST — independent of SkillInstaller, which
	// only ever reaches the central registry. Only a slug absent from the
	// project shelf falls through to the registry path below, unchanged.
	if shelf := resolveProjectShelf(t.deps, ctx); shelf != nil {
		if projWriter, ps, perr := skills.ResolveProjectSkillWriter(shelf, name); perr == nil {
			if revision == "" {
				return tools.ErrorResult(errorJSON("INVALID_INPUT", "revision is required", "read the skill again before removing it"))
			}
			if rmErr := projWriter.RemoveSkillReviewed(name, revision); rmErr != nil {
				if errors.Is(rmErr, skills.ErrNotFound) {
					return tools.ErrorResult(errorJSON("NOT_FOUND",
						fmt.Sprintf("skill %q is not installed", name), "use list_skills to see installed skills"))
				}
				slog.Warn("sysagent: remove_skill (project shelf) failed", "name", name, "error", rmErr)
				return tools.ErrorResult(errorJSON("REMOVE_FAILED",
					fmt.Sprintf("could not remove skill %q: %v", name, rmErr), ""))
			}
			tools.EmitSkillWriteAudit("project", t.Name(),
				tools.ToolAgentID(ctx), tools.ToolTranscriptSessionID(ctx), tools.ToolWorkspaceID(ctx), ps.Path)
			slog.Info("sysagent: remove_skill removed project skill",
				"name", name, "shelf", "project", "mount", ps.MountName, "path", ps.Path)
			return tools.NewToolResult(successJSON(map[string]any{
				"success":            true,
				"name":               name,
				"shelf":              "project",
				"mount":              ps.MountName,
				"revision":           revision,
				"persistence_status": "complete",
				"activation_status":  "active",
				"changed_fields":     []string{"installed"},
			}))
		} else if !errors.Is(perr, skills.ErrNotFound) {
			// ErrProjectWriteEscapesMount or another shelf-integrity problem —
			// not "no such project skill". Surface it rather than silently
			// falling through to the registry installer, which would look in
			// the wrong place entirely.
			slog.Warn("sysagent: remove_skill (project shelf) rejected", "name", name, "error", perr)
			return tools.ErrorResult(errorJSON("REMOVE_FAILED",
				fmt.Sprintf("could not remove project skill %q: %v", name, perr), ""))
		}
		// ErrNotFound: name is not on this workspace's project shelf — fall
		// through to the registry installer below, unchanged.
	}

	if t.deps == nil || t.deps.SkillInstaller == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill installer not configured", "ensure the gateway is started with a valid workspace"))
	}
	if revision == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "revision is required", "read the skill again before removing it"))
	}

	slog.Info("sysagent: remove_skill", "name", name)
	if err := t.deps.SkillInstaller.UninstallReviewed(name, revision); err != nil {
		if errors.Is(err, skills.ErrRevisionConflict) {
			return tools.ErrorResult(errorJSON("CONFLICT",
				fmt.Sprintf("skill %q changed after review", name), "read the skill again and submit its current revision"))
		}
		if isNotFound(err) {
			return tools.ErrorResult(errorJSON("NOT_FOUND",
				fmt.Sprintf("skill %q is not installed", name), "use list_skills to see installed skills"))
		}
		slog.Warn("sysagent: remove_skill failed", "name", name, "error", err)
		return tools.ErrorResult(errorJSON("REMOVE_FAILED",
			fmt.Sprintf("could not remove skill %q: %v", name, err), ""))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"success":            true,
		"name":               name,
		"revision":           revision,
		"persistence_status": "complete",
		"activation_status":  "active",
		"changed_fields":     []string{"installed"},
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
	return "List installed skills. scope=usable (the default) lists only skills assigned for this agent to run. " +
		"scope=management lists sanitized installed inventory for configuration review when the caller has management-read authority; " +
		"name may inspect one skill's sanitized content in management scope. Use Skill to run an assigned skill and find_skills to search the marketplace."
}

func (t *SkillListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"scope": map[string]any{"type": "string", "enum": []string{"usable", "management"}, "default": "usable"},
		"name":  map[string]any{"type": "string", "description": "Installed skill id to inspect; management scope only."},
	}}
}

func (t *SkillListTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t.deps == nil || t.deps.SkillsLoader == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill loader not configured", "ensure the gateway is started with a valid workspace"))
	}

	scope, name, err := parseSkillListArgs(args)
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "use scope usable or management and an installed skill id"))
	}
	if scope == "management" {
		if err := authorizeSkillManagementRead(t.deps, ctx); err != nil {
			return tools.ErrorResult(errorJSON("PERMISSION_DENIED", err.Error(), "use an authorized configuration agent"))
		}
		return t.executeManagement(ctx, name)
	}
	if name != "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is available only with management scope", "set scope to management"))
	}

	slog.Info("sysagent: list_skills", "scope", "usable")
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
		writer := skills.NewSkillWriter(filepath.Dir(filepath.Dir(info.Path)))
		revision, err := writer.SkillRevision(info.ID)
		if err != nil {
			return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not read revision for skill %q: %v", info.ID, err), "retry list_skills"))
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
			"revision":    revision,
		})
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"skills": skillMaps,
		"count":  len(skillMaps),
	}))
}

func parseSkillListArgs(args map[string]any) (scope, name string, err error) {
	scope = "usable"
	for key := range args {
		if key != "scope" && key != "name" {
			return "", "", fmt.Errorf("unknown parameter %q", key)
		}
	}
	if raw, ok := args["scope"]; ok {
		var valid bool
		scope, valid = raw.(string)
		if !valid || (scope != "usable" && scope != "management") {
			return "", "", errors.New("scope must be usable or management")
		}
	}
	if raw, ok := args["name"]; ok {
		var valid bool
		name, valid = raw.(string)
		name = strings.TrimSpace(name)
		if !valid || name == "" {
			return "", "", errors.New("name must be a non-empty string")
		}
		if err := validateID(name); err != nil {
			return "", "", errors.New("name must be an installed skill id without path separators")
		}
	}
	return scope, name, nil
}

func authorizeSkillManagementRead(deps *Deps, ctx context.Context) error {
	actor := strings.TrimSpace(tools.ToolAgentID(ctx))
	if actor == "" || deps == nil || deps.ResolveToolPolicy == nil {
		return errors.New("a current caller identity and live policy resolver are required")
	}
	policy, ok := deps.ResolveToolPolicy(actor, "list_skills")
	if !ok || (policy != string(config.ToolPolicyAllow) && policy != string(config.ToolPolicyAsk)) {
		return errors.New("list_skills management discovery is denied")
	}
	for _, candidate := range []string{"create_agent", "update_agent", "create_skill", "edit_skill"} {
		policy, current := deps.ResolveToolPolicy(actor, candidate)
		if !current {
			return errors.New("the caller is no longer registered")
		}
		if policy == string(config.ToolPolicyAllow) || policy == string(config.ToolPolicyAsk) {
			return nil
		}
	}
	return errors.New("management discovery requires at least one non-denied configuration tool")
}

type managedSkill struct {
	info         skills.SkillInfo
	origin       string
	sharedImpact string
	mount        string
}

func (t *SkillListTool) executeManagement(ctx context.Context, name string) *tools.ToolResult {
	items := make([]managedSkill, 0)
	seen := make(map[string]struct{})
	shelf, collisions := resolveProjectShelfWithCollisions(t.deps, ctx)
	if name != "" {
		for _, collision := range collisions {
			if strings.EqualFold(collision.Slug, name) {
				return tools.ErrorResult(errorJSON("AMBIGUOUS", fmt.Sprintf("skill %q exists in more than one mounted project", name), "use a workspace without the slug collision"))
			}
		}
	}
	for _, projectSkill := range shelf {
		key := strings.ToLower(projectSkill.ID)
		seen[key] = struct{}{}
		items = append(items, managedSkill{info: projectSkill.SkillInfo, origin: "project", sharedImpact: "workspace_members", mount: projectSkill.MountName})
	}
	for _, info := range t.deps.SkillsLoader.ListSkills() {
		key := strings.ToLower(info.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		origin, impact := info.Source, "all_agents"
		switch origin {
		case "global":
			origin = "user"
		case "workspace":
			origin, impact = "project", "workspace_members"
		}
		items = append(items, managedSkill{info: info, origin: origin, sharedImpact: impact})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].info.ID < items[j].info.ID })

	results := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if name != "" && !strings.EqualFold(item.info.ID, name) {
			continue
		}
		revision, content, readErr := readManagedSkillSnapshot(item.info, name != "")
		if readErr != nil {
			return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not safely read skill %q", item.info.ID), "retry list_skills"))
		}
		entry := map[string]any{
			"id": item.info.ID, "name": item.info.Name, "description": item.info.Description,
			"origin": item.origin, "shared_impact": item.sharedImpact, "revision": revision,
		}
		if item.mount != "" {
			entry["mount"] = item.mount
		}
		if item.info.Author != "" {
			entry["author"] = item.info.Author
		}
		if item.info.Version != "" {
			entry["version"] = item.info.Version
		}
		if name != "" {
			entry["content"] = content
		}
		results = append(results, entry)
	}
	if name != "" && len(results) == 0 {
		return tools.ErrorResult(errorJSON("NOT_FOUND", fmt.Sprintf("skill %q is not installed", name), "list management inventory without a name"))
	}
	return tools.NewToolResult(successJSON(map[string]any{"scope": "management", "skills": results, "count": len(results)}))
}

func readManagedSkillSnapshot(info skills.SkillInfo, includeContent bool) (revision, content string, err error) {
	root := filepath.Dir(filepath.Dir(info.Path))
	err = skills.WithMutationLock(root, func() error {
		rootReal, rootErr := filepath.EvalSymlinks(root)
		pathReal, pathErr := filepath.EvalSymlinks(info.Path)
		if rootErr != nil || pathErr != nil {
			return errors.New("skill path cannot be resolved")
		}
		rel, relErr := filepath.Rel(rootReal, pathReal)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return errors.New("skill path escapes its installed root")
		}
		writer := skills.NewSkillWriter(root)
		revision, err = writer.SkillRevision(info.ID)
		if err != nil || !includeContent {
			return err
		}
		var ok bool
		content, ok = skills.LoadSkillFile(pathReal)
		if !ok {
			return errors.New("skill content cannot be read")
		}
		return nil
	})
	return revision, content, err
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
