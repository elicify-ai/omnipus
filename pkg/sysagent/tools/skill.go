// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
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
	return "Remove a project or installation-wide skill. IRREVERSIBLE — the skill's whole directory, including " +
		"any version history it holds, is permanently deleted; there is no undo. It searches the target " +
		"workspace's project shelf first and removes a matching skill from its own project. Only if no " +
		"project skill matches does it use the installation's skills directory, which also contains " +
		"installation-wide authored skills and user overrides. A project-shelf read failure returns " +
		"READ_FAILED without removal or fallback; project-path integrity errors also stop removal instead " +
		"of falling back. A skill present only on the built-in shelf is not removed. If the removed skill " +
		"was shadowing a lower-priority skill of the same id (an override or a built-in), that " +
		"one becomes visible again. Parameters: name (required, the skill id as " +
		"reported by list_skills — no path separators), revision (required from the reviewed skill read), confirm (bool, must be true). A conflict requires a fresh read; do not retry the stale deletion."
}

func (t *SkillRemoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":           map[string]any{"type": "string"},
			"confirm":        map[string]any{"type": "boolean"},
			"revision":       map[string]any{"type": "string", "description": "Revision returned by the reviewed skill read."},
			workspaceIDParam: workspaceIDProperty("whose project skills are searched first (FR-042)"),
		},
		"required": []string{"name", "confirm", "revision"},
	}
}

func (t *SkillRemoveTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
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
	target, refusal := resolveTargetWorkspace(t.deps, ctx, args)
	if refusal != nil {
		return refusal
	}

	// ADR-072 D6.1 ("remove_skill follows the same rule — deleting a project
	// skill deletes the project's file"): resolve against the current turn's
	// workspace project shelf FIRST — independent of SkillInstaller, which
	// only ever reaches the central registry. Only a slug absent from the
	// project shelf falls through to the registry path below, unchanged.
	shelf, shelfRefusal := resolveProjectShelf(t.deps, target.ID)
	if shelfRefusal != nil {
		return shelfRefusal
	}
	if shelf != nil {
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
				tools.ToolAgentID(ctx), tools.ToolTranscriptSessionID(ctx), target.ID, ps.Path)
			slog.Info("sysagent: remove_skill removed project skill",
				"name", name, "shelf", "project", "mount", ps.MountName, "path", ps.Path)
			return skillRemovedResult(ps.Path, map[string]any{
				"success":            true,
				"name":               name,
				"shelf":              "project",
				"mount":              ps.MountName,
				"workspace_id":       target.ID,
				"revision":           revision,
				"persistence_status": "complete",
				"activation_status":  "active",
				"changed_fields":     []string{"installed"},
			})
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

	return changeSuccess(map[string]any{
		"success":            true,
		"name":               name,
		"revision":           revision,
		"persistence_status": "complete",
		"activation_status":  "active",
		"changed_fields":     []string{"installed"},
	}, working("the installer removed the skill directory and reported success"))
}

// skillRemovedResult renders a project-skill removal with its read-back: the
// skill's SKILL.md must be gone from disk.
func skillRemovedResult(skillFile string, fields map[string]any) *tools.ToolResult {
	const scope = "the skill's SKILL.md checked absent from disk"
	if _, err := os.Stat(skillFile); err == nil {
		return changeSuccess(fields, savedNotWorking("the skill file is still present after removal", scope))
	} else if !errors.Is(err, os.ErrNotExist) {
		return changeSuccess(fields, savedNotWorking("the removal could not be checked: "+err.Error(), scope))
	}
	return changeSuccess(fields, working(scope))
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
		"scope=management lists sanitized installed inventory for configuration review when the caller has management-read authority. " +
		"Read one skill's details with get_skill. Use Skill to run an assigned skill and find_skills to search the marketplace."
}

func (t *SkillListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"scope":          map[string]any{"type": "string", "enum": []string{"usable", "management"}, "default": "usable"},
		workspaceIDParam: workspaceIDProperty("whose project skills the management listing includes (management scope only)"),
	}}
}

func (t *SkillListTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	if t.deps == nil || t.deps.SkillsLoader == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill loader not configured", "ensure the gateway is started with a valid workspace"))
	}

	scope, err := parseSkillListArgs(args)
	if err == nil {
		if _, present := args[workspaceIDParam]; present && scope != "management" {
			err = errors.New("workspace_id is available only with management scope")
		}
	}
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "use scope usable or management"))
	}
	session := tools.ToolTranscriptSessionID(ctx)
	if scope == "management" {
		if err := authorizeSkillManagementRead(t.deps, ctx); err != nil {
			return tools.ErrorResult(errorJSON("PERMISSION_DENIED", err.Error(), "use an authorized configuration agent"))
		}
		target, refusal := resolveTargetWorkspace(t.deps, ctx, args)
		if refusal != nil {
			return refusal
		}
		return t.executeManagement(target.ID, session)
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
		revision, description, err := observeListRow(info, session)
		if err != nil {
			return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not read skill %q: %v", info.ID, err), "retry list_skills"))
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
			"description": description,
			"revision":    revision,
		})
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"skills": skillMaps,
		"count":  len(skillMaps),
	}))
}

func parseSkillListArgs(args map[string]any) (scope string, err error) {
	scope = "usable"
	if _, ok := args["name"]; ok {
		// skills spec r15 §5.3: reading one skill moved to get_skill.
		return "", errors.New("name was removed from list_skills; use get_skill with id and fields")
	}
	for key := range args {
		if key != "scope" && key != workspaceIDParam {
			return "", fmt.Errorf("unknown parameter %q", key)
		}
	}
	if raw, ok := args["scope"]; ok {
		var valid bool
		scope, valid = raw.(string)
		if !valid || (scope != "usable" && scope != "management") {
			return "", errors.New("scope must be usable or management")
		}
	}
	return scope, nil
}

// observeListRow reads one listed skill through one captured observation
// (skills spec r15 §5.2 side effect, §7.3): the row's description and
// revision come from the same walk, and the row records this session's seen
// revision for the skill.
func observeListRow(info skills.SkillInfo, session string) (revision, description string, err error) {
	dir := filepath.Dir(info.Path)
	root, slug := filepath.Dir(dir), filepath.Base(dir)
	obs, err := skills.ObserveSkill(root, slug, skills.VersionQuery{})
	if err != nil {
		skills.ForgetSeen(session, slug)
		return "", "", err
	}
	description = info.Description
	if obs.HasInstruction {
		if d := obs.Meta("").Description; d != "" {
			description = d
		}
	}
	if obs.Revision == "" {
		skills.ForgetSeen(session, obs.Slug) // degraded: no revision certified (§7.3)
	} else {
		skills.RecordSeen(session, obs.Slug, obs.Revision)
	}
	return obs.Revision, description, nil
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
	for _, candidate := range []string{"create_agent", "update_agent", "edit_skill"} {
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

func (t *SkillListTool) executeManagement(wsID, session string) *tools.ToolResult {
	items := make([]managedSkill, 0)
	seen := make(map[string]struct{})
	shelf, _, shelfRefusal := resolveProjectShelfWithCollisions(t.deps, wsID, "")
	if shelfRefusal != nil {
		return shelfRefusal
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

	// Management rows never carry content (skills spec r15 §5.3); one skill's
	// details are read with get_skill.
	results := make([]map[string]any, 0, len(items))
	for _, item := range items {
		revision, description, readErr := observeListRow(item.info, session)
		if readErr != nil {
			return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not safely read skill %q: %v", item.info.ID, readErr), "retry list_skills"))
		}
		entry := map[string]any{
			"id": item.info.ID, "name": item.info.Name, "description": description,
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
		results = append(results, entry)
	}
	return tools.NewToolResult(successJSON(map[string]any{"scope": "management", "skills": results, "count": len(results)}))
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
