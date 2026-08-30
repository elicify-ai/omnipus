// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
			"directory; prior versions are snapshotted for rollback, but whether the write "+
			"additionally prompts for operator approval depends on your operator's tool-approval "+
			"policy for this tool. content must not exceed %d bytes. A name that already exists "+
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
		},
		"required": []string{"name", "content"},
	}
}

func (t *SkillCreateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
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

	path, err := t.deps.SkillWriter.CreateSkill(name, content)
	if err != nil {
		return skillAuthoringError("create", name, err)
	}

	// Audit trail: a skill was authored. The consent decision itself is recorded
	// by the agent loop's approval hook; this records the resulting write.
	slog.Info("sysagent: create_skill wrote skill",
		"event", "skill_authored", "action", "create", "name", name, "path", path)

	return tools.NewToolResult(successJSON(map[string]any{
		"success": true,
		"name":    name,
		"path":    path,
		"action":  "created",
	}))
}

// ---- edit_skill ----

type SkillEditTool struct{ deps *Deps }

func NewSkillEditTool(d *Deps) *SkillEditTool   { return &SkillEditTool{deps: d} }
func (t *SkillEditTool) Name() string           { return "edit_skill" }
func (t *SkillEditTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *SkillEditTool) Description() string {
	return fmt.Sprintf(
		"Edit / refine an EXISTING skill (self-improvement). Snapshots the prior version "+
			"for rollback, then writes the new SKILL.md; whether the write additionally prompts "+
			"for operator approval depends on your operator's tool-approval policy for this tool. "+
			"Editing a built-in creates a user override; the built-in is never mutated in place. "+
			"content must not exceed %d bytes. "+
			"Parameters: name (required), content (required, full new SKILL.md).",
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
		},
		"required": []string{"name", "content"},
	}
}

func (t *SkillEditTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	name, _ := args["name"].(string)
	content, _ := args["content"].(string)
	if name == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "name is required", ""))
	}
	if content == "" {
		return tools.ErrorResult(
			errorJSON("INVALID_INPUT", "content is required", "provide the full new SKILL.md including frontmatter"),
		)
	}

	if t.deps == nil || t.deps.SkillWriter == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill writer not configured", "ensure the gateway is started with a valid workspace"))
	}
	if t.deps.SkillsLoader == nil {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"skill loader not configured", "ensure the gateway is started with a valid workspace"))
	}

	// Determine whether a source skill exists anywhere on the load path. If the
	// skill exists only as a built-in (or in another root), allow EditSkill to
	// create a user override in the writer's (global) root rather than failing.
	allowOverride := skillExistsOnLoadPath(t.deps.SkillsLoader, name)

	path, createdOverride, err := t.deps.SkillWriter.EditSkill(name, content, allowOverride)
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
	return tools.NewToolResult(successJSON(map[string]any{
		"success":          true,
		"name":             name,
		"path":             path,
		"action":           action,
		"created_override": createdOverride,
	}))
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
