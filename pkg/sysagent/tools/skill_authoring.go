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
	"strings"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// Skill authoring tools — create_skill and edit_skill (Spec-6 U2,
// FR-9.2). These are the self-improvement / procedural-memory verbs: an agent
// (typically Ava) authors or refines a skill, and the resulting SKILL.md is
// written, validated, and versioned.
//
// CONSENT GATING: these tools mutate the skills tree, so they are sensitive. The
// consent gate is the agent loop's tool-approval hook (HookManager.ApproveTool →
// the ws_approval WebSocket prompt): an operator policy of "ask" for these tools
// routes every invocation through that approval prompt BEFORE Execute runs. The
// tool implementation deliberately does NOT bypass that gate — it never writes
// without having been dispatched, and dispatch is what the approval hook gates.
// (RequireNotBypass is a 503 HTTP dev-bypass guard at the wrong layer and is not
// used here — see Spec-6 §C-1.)
//
// SAFETY: every write is path-confined to the skills root (no traversal), the
// SKILL.md is validated against the loader contract (frontmatter + structure +
// size), prior versions are snapshotted under .versions/ for rollback, and each
// create/edit is audit-logged via structured logging.

// ---- create_skill ----

type SkillCreateTool struct{ deps *Deps }

func NewSkillCreateTool(d *Deps) *SkillCreateTool { return &SkillCreateTool{deps: d} }
func (t *SkillCreateTool) Name() string           { return "create_skill" }
func (t *SkillCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *SkillCreateTool) Description() string {
	return fmt.Sprintf(
		"Author a NEW skill (procedural memory). Writes a SKILL.md to the user skills "+
			"directory; prior versions are snapshotted for rollback. This call writes the file; it is not a "+
			"confirmation step. Policy may still Ask at execution; that is not a "+
			"substitute for the user's confirmation of the proposal, and you must not invent a "+
			"second confirmation ritual. content must not exceed %d bytes. A name that already exists "+
			"is refused — use edit_skill to modify it instead. "+
			"Parameters: name (required, alphanumeric+hyphens), content (required, full SKILL.md "+
			"including YAML frontmatter with name and description).",
		skills.MaxSkillMarkdownBytes,
	)
}

func (t *SkillCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "Skill name (alphanumeric with hyphens)."},
			"content": map[string]any{
				"type":        "string",
				"description": "Full SKILL.md content including YAML frontmatter.",
			},
			workspaceIDParam: map[string]any{
				"type": "string",
				"description": "Optional. The workspace whose project shelf gets the new skill (it needs exactly one " +
					"mounted project folder). Unlike the other skill tools, omitting it does NOT mean the current " +
					"workspace: it creates an installation-wide user skill available in every workspace. Only " +
					"Omnipus may name a workspace other than its own.",
			},
		},
		"required": []string{"name", "content"},
	}
}

func (t *SkillCreateTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	name, _ := args["name"].(string)
	content, _ := args["content"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if content == "" {
		return tools.ErrorResult(
			errorJSON("INVALID_INPUT", "content is required", "provide the full SKILL.md including frontmatter"),
		)
	}

	if t.deps == nil || t.deps.SkillWriter == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill writer not configured", "ensure the gateway is started with a valid workspace"))
	}

	target, refusal := resolveTargetWorkspace(t.deps, ctx, args)
	if refusal != nil {
		return refusal
	}
	// Ruling 2026-10-15 (WU-3 Q1): only an explicit workspace_id targets a
	// project shelf; omitted, create_skill writes an installation-wide user
	// skill — the documented exception to the contextual-workspace default.
	if target.Explicit {
		return t.createProjectSkill(ctx, target.ID, name, content)
	}

	path, revision, err := t.deps.SkillWriter.CreateSkillReviewed(name, content)
	if err != nil {
		return skillAuthoringError("create", name, err)
	}

	// Audit trail: a skill was authored. The consent decision itself is recorded
	// by the agent loop's approval hook; this records the resulting write.
	slog.Info("sysagent: create_skill wrote skill",
		"event", "skill_authored", "action", "create", "name", name, "path", path)

	return skillWrittenResult(path, content, map[string]any{
		"success":            true,
		"name":               name,
		"path":               path,
		"action":             "created",
		"revision":           revision,
		"persistence_status": "complete",
		"activation_status":  "active",
		"changed_fields":     []string{"content"},
	})
}

// projectSkillsDir is the recognised skills folder of a mounted project that
// a new project skill goes into: the folder the project already uses
// (.omnipus/skills, else .claude/skills), or .omnipus/skills when it has none.
func projectSkillsDir(mountRoot string) string {
	for _, rel := range []string{filepath.Join(".omnipus", "skills"), filepath.Join(".claude", "skills")} {
		dir := filepath.Join(mountRoot, rel)
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	return filepath.Join(mountRoot, ".omnipus", "skills")
}

// createProjectSkill creates a project skill on the target workspace's shelf
// (FR-042, BDD-4.8): <name>/SKILL.md in the recognised skills folder
// (projectSkillsDir) of the workspace's one mounted project folder. A workspace with no mount, or with
// several (the destination would be a guess), is refused before any write.
func (t *SkillCreateTool) createProjectSkill(ctx context.Context, wsID, name, content string) *tools.ToolResult {
	mounts, ok := workspacepkg.LoadMounts(t.deps.Home, wsID)
	if !ok {
		return tools.ErrorResult(errorJSON("READ_FAILED",
			fmt.Sprintf("the project folders of workspace %q could not be read", wsID), "Retry, or check the workspace's folders"))
	}
	switch len(mounts) {
	case 0:
		return tools.ErrorResult(errorJSON("NO_PROJECT_FOLDER",
			fmt.Sprintf("workspace %q has no mounted project folder to hold a project skill", wsID),
			"Omit workspace_id to create a user skill, or mount a project folder in the workspace first"))
	case 1:
	default:
		names := make([]string, 0, len(mounts))
		for _, m := range mounts {
			names = append(names, m.Name)
		}
		return tools.ErrorResult(errorJSON("AMBIGUOUS",
			fmt.Sprintf("workspace %q has several mounted project folders (%s); the destination would be a guess",
				wsID, strings.Join(names, ", ")),
			"Create the skill in the project folder from the app, or omit workspace_id to create a user skill"))
	}
	writer := skills.NewSkillWriter(projectSkillsDir(mounts[0].HostPath))
	path, revision, err := writer.CreateSkillReviewed(name, content)
	if err != nil {
		return skillAuthoringError("create", name, err)
	}
	tools.EmitSkillWriteAudit("project", t.Name(),
		tools.ToolAgentID(ctx), tools.ToolTranscriptSessionID(ctx), wsID, path)
	slog.Info("sysagent: create_skill wrote project skill",
		"event", "skill_authored", "action", "create", "name", name, "path", path, "shelf", "project",
		"mount", mounts[0].Name, "workspace_id", wsID)
	return skillWrittenResult(path, content, map[string]any{
		"success":            true,
		"name":               name,
		"path":               path,
		"action":             "created",
		"shelf":              "project",
		"mount":              mounts[0].Name,
		"workspace_id":       wsID,
		"revision":           revision,
		"persistence_status": "complete",
		"activation_status":  "active",
		"changed_fields":     []string{"content"},
	})
}

// ---- edit_skill ----

type SkillEditTool struct{ deps *Deps }

func NewSkillEditTool(d *Deps) *SkillEditTool   { return &SkillEditTool{deps: d} }
func (t *SkillEditTool) Name() string           { return "edit_skill" }
func (t *SkillEditTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *SkillEditTool) Description() string {
	return fmt.Sprintf(
		"Edit / refine an EXISTING skill (self-improvement). Snapshots the prior version "+
			"for rollback, then writes the new SKILL.md. This call writes the file; it is not a "+
			"confirmation step. Policy may still Ask at execution; that is not a substitute for "+
			"the user's confirmation of the proposal, and you must not invent a second "+
			"confirmation ritual. "+
			"Editing a built-in creates a user override; the built-in is never mutated in place. "+
			"content must not exceed %d bytes. "+
			"Parameters: name, content (full new SKILL.md), and revision from the reviewed skill read are required. A conflict means the skill changed after review; stop, reread, and do not retry the stale write.",
		skills.MaxSkillMarkdownBytes,
	)
}

func (t *SkillEditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "Name of the existing skill to edit."},
			"content": map[string]any{
				"type":        "string",
				"description": "Full new SKILL.md content including YAML frontmatter.",
			},
			"revision":       map[string]any{"type": "string", "description": "Revision returned by the reviewed skill read."},
			workspaceIDParam: workspaceIDProperty("whose project skills are searched first (FR-042)"),
		},
		"required": []string{"name", "content", "revision"},
	}
}

func (t *SkillEditTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	name, _ := args["name"].(string)
	content, _ := args["content"].(string)
	revision, _ := args["revision"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if content == "" {
		return tools.ErrorResult(
			errorJSON("INVALID_INPUT", "content is required", "provide the full new SKILL.md including frontmatter"),
		)
	}
	if revision == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "revision is required", "read the skill again before editing"))
	}

	if t.deps == nil || t.deps.SkillWriter == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill writer not configured", "ensure the gateway is started with a valid workspace"))
	}
	if t.deps.SkillsLoader == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill loader not configured", "ensure the gateway is started with a valid workspace"))
	}
	target, refusal := resolveTargetWorkspace(t.deps, ctx, args)
	if refusal != nil {
		return refusal
	}

	// ADR-072 D6.1: a project skill — one discovered under a mounted
	// repository's own .claude/skills or .omnipus/skills — is edited
	// directly in its own mount, never forked into the central registry.
	// Resolve against the CURRENT TURN's workspace project shelf FIRST; only
	// a slug absent from that shelf falls through to the ordinary
	// global-root path below, unchanged.
	shelf, shelfRefusal := resolveProjectShelf(t.deps, target.ID)
	if shelfRefusal != nil {
		return shelfRefusal
	}
	if shelf != nil {
		if projWriter, ps, perr := skills.ResolveProjectSkillWriter(shelf, name); perr == nil {
			path, _, nextRevision, editErr := projWriter.EditSkillReviewed(name, content, true, revision)
			if editErr != nil {
				return skillAuthoringError("edit", name, editErr)
			}
			tools.EmitSkillWriteAudit("project", t.Name(),
				tools.ToolAgentID(ctx), tools.ToolTranscriptSessionID(ctx), target.ID, path)
			slog.Info("sysagent: edit_skill wrote project skill",
				"event", "skill_authored", "action", "edit", "name", name,
				"path", path, "shelf", "project", "mount", ps.MountName)
			return skillWrittenResult(path, content, map[string]any{
				"success":            true,
				"name":               name,
				"path":               path,
				"action":             "edited",
				"shelf":              "project",
				"mount":              ps.MountName,
				"workspace_id":       target.ID,
				"revision":           nextRevision,
				"persistence_status": "complete",
				"activation_status":  "active",
				"changed_fields":     []string{"content"},
			})
		} else if !errors.Is(perr, skills.ErrNotFound) {
			// ErrProjectWriteEscapesMount or another shelf-integrity problem —
			// not "no such project skill". Surface it rather than silently
			// falling through to the registry path, which would write
			// somewhere the operator did not intend.
			return skillAuthoringError("edit", name, perr)
		}
		// ErrNotFound: name is not on this workspace's project shelf — fall
		// through to the registry/builtin path below, unchanged.
	}

	// Determine whether a source skill exists anywhere on the load path. If the
	// skill exists only as a built-in (or in another root), allow EditSkill to
	// create a user override in the writer's (global) root rather than failing.
	allowOverride := skillExistsOnLoadPath(t.deps.SkillsLoader, name)

	var path, nextRevision string
	var createdOverride bool
	var err error
	if allowOverride {
		if _, localErr := t.deps.SkillWriter.SkillRevision(name); errors.Is(localErr, skills.ErrNotFound) {
			sourcePath := ""
			for _, info := range t.deps.SkillsLoader.ListSkills() {
				if strings.EqualFold(info.ID, name) {
					sourcePath = info.Path
					break
				}
			}
			path, nextRevision, err = t.deps.SkillWriter.CreateOverrideReviewed(name, content, sourcePath, revision)
			createdOverride = err == nil
		} else {
			path, createdOverride, nextRevision, err = t.deps.SkillWriter.EditSkillReviewed(name, content, allowOverride, revision)
		}
	} else {
		path, createdOverride, nextRevision, err = t.deps.SkillWriter.EditSkillReviewed(name, content, false, revision)
	}
	if err != nil {
		return skillAuthoringError("edit", name, err)
	}

	slog.Info("sysagent: edit_skill wrote skill",
		"event", "skill_authored", "action", "edit", "name", name,
		"path", path, "created_override", createdOverride)

	action := "edited"
	if createdOverride {
		action = "override_created"
	}
	return skillWrittenResult(path, content, map[string]any{
		"success":            true,
		"name":               name,
		"path":               path,
		"action":             action,
		"created_override":   createdOverride,
		"revision":           nextRevision,
		"persistence_status": "complete",
		"activation_status":  "active",
		"changed_fields":     []string{"content"},
	})
}

// skillWrittenResult renders a create_skill / edit_skill result with its FR-030
// verdict. The check is the existing activation check: the written SKILL.md is
// read back from the path the writer reports and must equal the submitted
// content (the writer already validated it against the loader contract).
func skillWrittenResult(path, content string, fields map[string]any) *tools.ToolResult {
	const scope = "SKILL.md validated against the loader contract and read back from disk"
	notTested := []string{"an agent loading and running the skill"}
	got, err := os.ReadFile(path)
	if err != nil {
		return changeSuccess(fields, savedNotWorking("the written skill could not be read back: "+err.Error(), scope, notTested...))
	}
	if string(got) != content {
		return changeSuccess(fields, savedNotWorking("the skill read back differs from what was written", scope, notTested...))
	}
	return changeSuccess(fields, working(scope, notTested...))
}

// resolveProjectShelf builds the ADR-072 D4.1 project shelf (skill.Skill
// discovery merged over every live mount) for the current tool call's
// effective workspace — the ctx-carried workspace id (tools.ToolWorkspaceID,
// the same value the agent loop threads through every turn), falling back to
// this installation's default workspace when the call carries none, mirroring
// pkg/agent/loop_env.go's wireDelegationInjectors/wireProjectShelfResolvers
// fallback exactly. Returns nil (no project shelf) when no workspace
// resolves or it simply has no mounts — the
// authoring tools' existing global-root SkillWriter/SkillInstaller path is
// then unaffected, exactly as before this fix (D6: "silent when there is
// nothing to find").
//
// R3 fix (2026-09): shared by SkillEditTool.Execute and SkillRemoveTool.Execute
// so create_skill/edit_skill/remove_skill can resolve a slug against the
// current workspace's mounted project skills before falling back to the
// central registry — the same resolution wireProjectShelfResolvers installs
// for the agent's own ContextBuilder, computed independently here because
// sysagent tools have no access to a live ContextBuilder from inside
// Execute.
//
// WU-3 (FR-040, FR-042): the workspace is now the call's resolved target
// (resolveTargetWorkspace — the explicit workspace_id or the contextual
// workspace), passed in rather than read from ctx here.
//
// An unreadable mount record is NOT "no project shelf" (silent-failure-hunter
// SF-01): it returns a shelfReadFailure refusal, because the workspace may
// hold a project copy of the slug and falling back to the global writer would
// change a different, possibly byte-identical, skill.
func resolveProjectShelf(deps *Deps, wsID string) (skills.ProjectShelf, *tools.ToolResult) {
	shelf, _, refusal := resolveProjectShelfWithCollisions(deps, wsID, "")
	return shelf, refusal
}

func resolveProjectShelfWithCollisions(deps *Deps, wsID, name string) (skills.ProjectShelf, []skills.SlugCollision, *tools.ToolResult) {
	if deps == nil || strings.TrimSpace(deps.Home) == "" {
		return nil, nil, nil
	}
	home := deps.Home

	wsID = strings.TrimSpace(wsID)
	if wsID == "" {
		def, err := workspacepkg.ResolveDefaultID(home)
		if err != nil || def == "" {
			return nil, nil, nil
		}
		wsID = def
	}

	mounts, ok, dropped := workspacepkg.LoadMountsWithDropStatus(home, wsID)
	if !ok || dropped {
		return nil, nil, tools.ErrorResult(errorJSON("READ_FAILED",
			fmt.Sprintf("the project folders of workspace %q could not be read completely, so its project skills are unknown", wsID),
			"Repair the workspace's folders in the app, then read the skill again before retrying"))
	}
	if len(mounts) == 0 {
		return nil, nil, nil
	}

	pm := make([]skills.ProjectMount, 0, len(mounts))
	for _, m := range mounts {
		pm = append(pm, skills.ProjectMount{Name: m.Name, Root: m.HostPath})
	}
	// Only a complete scan of the requested scope may prove a slug absent:
	// named reads inspect that slug in every project; writes and full listings
	// pass an empty name and still require a complete shelf scan.
	shelf, collisions, err := skills.LookupProjectSkillChecked(pm, name)
	if err != nil {
		return nil, nil, tools.ErrorResult(errorJSON("READ_FAILED",
			fmt.Sprintf("the project skills of workspace %q could not be inspected: %v", wsID, err),
			"Restore access to the project folder, then read the skill again before retrying"))
	}
	return shelf, collisions, nil
}

// skillExistsOnLoadPath reports whether a skill of the given name is resolvable
// by the loader anywhere on its search path (workspace/global/builtin). Used by
// system.skill.edit to decide whether a built-in edit should create a user
// override.
func skillExistsOnLoadPath(loader *skills.SkillsLoader, name string) bool {
	if loader == nil {
		return false
	}
	_, ok := loader.LoadSkill(name)
	return ok
}

// skillAuthoringError maps a SkillWriter error to a structured tool error with
// the appropriate code and operator suggestion, and logs denials for audit.
func skillAuthoringError(op, name string, err error) *tools.ToolResult {
	switch {
	case errors.Is(err, skills.ErrPathConfinement):
		slog.Warn("sysagent: skill authoring rejected — path confinement",
			"event", "skill_write_denied", "action", op, "name", name, "reason", "path_confinement")
		return tools.ErrorResult(errorJSON(
			"INVALID_INPUT",
			"skill name escapes the skills directory",
			"use a plain skill name (alphanumeric with hyphens, no '/' or '..')",
		))
	case errors.Is(err, skills.ErrOversize):
		slog.Warn("sysagent: skill authoring rejected — oversize",
			"event", "skill_write_denied", "action", op, "name", name, "reason", "oversize")
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("SKILL.md exceeds the maximum allowed size (%d bytes)", skills.MaxSkillMarkdownBytes),
			"trim the skill content"))
	case errors.Is(err, skills.ErrAlreadyExists):
		return tools.ErrorResult(errorJSON("ALREADY_EXISTS",
			fmt.Sprintf("a skill named %q already exists", name), "use edit_skill to modify it"))
	case errors.Is(err, skills.ErrNotFound):
		return tools.ErrorResult(errorJSON("NOT_FOUND",
			fmt.Sprintf("skill %q not found", name), "use create_skill to author a new skill"))
	case errors.Is(err, skills.ErrRevisionConflict):
		return tools.ErrorResult(errorJSON("CONFLICT",
			fmt.Sprintf("skill %q changed after review", name), "read the skill again and submit its current revision"))
	default:
		// Validation failures (invalid frontmatter, missing name/description)
		// and I/O errors land here. Surface the cause for the agent to correct.
		slog.Warn("sysagent: skill authoring failed",
			"event", "skill_write_denied", "action", op, "name", name, "error", err)
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("could not %s skill %q: %v", op, name, err),
			"check the SKILL.md frontmatter (name + description) and structure"))
	}
}
