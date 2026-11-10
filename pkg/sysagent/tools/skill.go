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
	// List and get_skill must agree (skills spec r15 §5): a project row comes
	// only from the checked project shelf above, and every other row is a
	// skill get_skill reads. A loader row is labelled by where it lives: its
	// folder directly under the registry root is a user skill whatever the
	// loader tier reported; the loader's pre-D4.1 "workspace" tier outside
	// the registry is no r15 shelf (get_skill cannot read it), so it is not
	// listed — and never as "project".
	registryRoot, _ := skillsRootOf(t.deps)
	for _, info := range t.deps.SkillsLoader.ListSkills() {
		key := strings.ToLower(info.ID)
		if _, exists := seen[key]; exists {
			continue
		}
		origin, impact := info.Source, "all_agents"
		switch {
		case origin == "builtin":
		case registryRoot != "" && filepath.Clean(filepath.Dir(filepath.Dir(info.Path))) == filepath.Clean(registryRoot):
			origin = "user"
		case origin == "global":
			origin = "user"
		default:
			continue
		}
		seen[key] = struct{}{}
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
