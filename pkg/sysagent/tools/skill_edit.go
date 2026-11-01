// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// edit_skill — the skill change tool for actions that are not file writes
// (skills spec r15 §5.4): copy, delete_file, delete, restore. Every action
// runs through the one change pipeline (skills.ApplyChange). Writing a file
// into a skill is the file tools' job (§5.5).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

var editSkillActions = []string{"copy", "delete_file", "delete", "restore"}

// editSkillActionParams names the parameters each action accepts besides
// action and skill (§5.4: unknown or misplaced parameters → INVALID_INPUT).
var editSkillActionParams = map[string]string{
	"copy":        "to",
	"delete_file": "path",
	"delete":      "",
	"restore":     "version",
}

// SkillEditTool is §5.4's edit_skill.
type SkillEditTool struct{ deps *Deps }

// NewSkillEditTool builds the edit_skill tool over the system-tool deps.
func NewSkillEditTool(d *Deps) *SkillEditTool { return &SkillEditTool{deps: d} }

// Name implements tools.Tool.
func (t *SkillEditTool) Name() string { return "edit_skill" }

// Scope implements tools.Tool.
func (t *SkillEditTool) Scope() tools.ToolScope { return tools.ScopeCore }

// Description implements tools.Tool.
func (t *SkillEditTool) Description() string {
	return "Change a skill other than by writing a file: action=copy makes your own copy of a skill (built-ins " +
		"included) under the name given in to; action=delete_file removes one file (path, never SKILL.md); " +
		"action=delete deletes the skill (its history is kept, so it can be restored); action=restore brings " +
		"back a version listed by get_skill (version). To create a skill, write its SKILL.md with write_file; " +
		"to change a file, use write_file or edit_file. delete_file, delete and restoring an existing skill " +
		"need a current read of the skill in this session (get_skill, Skill, read_file or list_directory). " +
		"Built-in skills are read-only: copy them first. A new copy is granted to no agent."
}

// Parameters implements tools.Tool (§5.4).
func (t *SkillEditTool) Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"action":  map[string]any{"type": "string", "enum": editSkillActions},
			"skill":   map[string]any{"type": "string", "description": "The skill id (for copy: the source)."},
			"to":      map[string]any{"type": "string", "description": "copy only: the new skill's id."},
			"path":    map[string]any{"type": "string", "description": "delete_file only: the file, relative to the skill folder."},
			"version": map[string]any{"type": "string", "description": "restore only: a version id from get_skill versions."},
		},
		"required": []string{"action", "skill"},
	}
}

type editSkillRequest struct {
	action, skill, to, path, version string
}

func parseEditSkillArgs(args map[string]any) (editSkillRequest, error) {
	var req editSkillRequest
	action, _ := args["action"].(string)
	if _, ok := editSkillActionParams[action]; !ok {
		return req, fmt.Errorf("action must be one of %s", strings.Join(editSkillActions, ", "))
	}
	req.action = action
	allowed := editSkillActionParams[action]
	for key := range args {
		switch key {
		case "action", "skill":
		case "to", "path", "version":
			if key != allowed {
				return req, fmt.Errorf("parameter %q does not apply to action %s", key, action)
			}
		default:
			return req, fmt.Errorf("unknown parameter %q", key)
		}
	}
	str := func(key string) (string, error) {
		raw, ok := args[key]
		if !ok {
			return "", fmt.Errorf("%s is required for action %s", key, action)
		}
		s, isString := raw.(string)
		if !isString || strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("%s must be a non-empty string", key)
		}
		return strings.TrimSpace(s), nil
	}
	var err error
	if req.skill, err = str("skill"); err != nil {
		return req, err
	}
	if !skills.ValidSlug(req.skill) {
		return req, fmt.Errorf("%q is not a valid skill id", req.skill)
	}
	if allowed != "" {
		v, err := str(allowed)
		if err != nil {
			return req, err
		}
		switch allowed {
		case "to":
			req.to = v
		case "path":
			req.path = v
		case "version":
			req.version = v
		}
	}
	return req, nil
}

// Execute implements tools.Tool.
func (t *SkillEditTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	req, err := parseEditSkillArgs(args)
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), "see the edit_skill parameter list"))
	}
	root, ok := skillsRootOf(t.deps)
	if !ok {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE", "skills directory not configured", "ensure the gateway is started with a valid home"))
	}
	session := tools.ToolTranscriptSessionID(ctx)
	change := skills.ChangeRequest{
		Slug: req.skill,
		Actor: skills.ChangeActor{
			Kind: "agent", AgentID: tools.ToolAgentID(ctx), SessionID: session,
			WorkspaceID: tools.ToolWorkspaceID(ctx), Tool: t.Name(),
		},
	}
	switch req.action {
	case "copy":
		change.Kind, change.To, change.Expect = skills.KindCreate, req.to, skills.Expectation{Absent: true}
	case "delete_file":
		change.Kind, change.Path = skills.KindDeleteFile, req.path
	case "delete":
		change.Kind = skills.KindDelete
	case "restore":
		change.Kind, change.Version = skills.KindRestore, req.version
	}
	if change.Expect == (skills.Expectation{}) {
		expect, refusal := seenExpectation(root, session, req)
		if refusal != nil {
			return refusal
		}
		change.Expect = expect
	}
	out, err := skills.ApplyChange(root, change)
	target := req.skill
	if req.action == "copy" {
		target = req.to
	}
	if err != nil {
		return editSkillFailure(session, target, err)
	}
	if req.action == "delete" {
		skills.ForgetSeen(session, target)
	} else {
		skills.RecordSeen(session, target, out.Revision) // the session's own committed change (§7.3)
	}
	message := out.Message
	if req.action == "copy" {
		message = fmt.Sprintf("Created '%s' as your own copy of '%s'. No agent is granted it yet; grant it to the agents that should use it. (%s)", req.to, req.skill, out.Message)
	}
	fields := map[string]any{
		"success":            true,
		"action":             req.action,
		"skill":              target,
		"revision":           out.Revision,
		"message":            message,
		"persistence_status": string(out.PersistenceStatus),
		"activation_status":  string(out.ActivationStatus),
		"changed_fields":     out.ChangedFields,
	}
	if out.Warning != "" {
		fields["warning"] = out.Warning
	}
	return changeSuccess(fields, working("the change pipeline committed the change and recorded its version"))
}

// seenExpectation supplies §7.3's expectation for delete_file, delete and
// restore: the session's seen revision for an existing skill (none →
// SKILL_NOT_READ), expected absence for restoring a deleted skill.
func seenExpectation(root, session string, req editSkillRequest) (skills.Expectation, *tools.ToolResult) {
	if req.action == "restore" {
		_, present, err := skills.ResolveInstalledSlug(root, req.skill)
		if err != nil {
			return skills.Expectation{}, tools.ErrorResult(errorJSON("STORAGE_ERROR", fmt.Sprintf("could not read skill '%s': %v", req.skill, err), ""))
		}
		if !present {
			return skills.Expectation{Absent: true}, nil
		}
	}
	rev, ok := skills.SeenRevision(session, req.skill)
	if !ok {
		return skills.Expectation{}, tools.ErrorResult(errorJSON(skills.ErrSkillNotRead.Error(),
			fmt.Sprintf("read skill '%s' first (Skill, get_skill, read_file or list_directory), then retry", req.skill), ""))
	}
	return skills.Expectation{Revision: rev}, nil
}

// editSkillFailure renders a refused or partly saved change. A partial save
// removes this session's seen record (§7.3), so the next change needs a
// fresh read.
func editSkillFailure(session, target string, err error) *tools.ToolResult {
	var ce *skills.ChangeError
	if !errors.As(err, &ce) {
		return tools.ErrorResult(errorJSON("STORAGE_ERROR", fmt.Sprintf("the change to '%s' failed: %v", target, err), ""))
	}
	if errors.Is(err, skills.ErrSkillPartiallySaved) {
		skills.ForgetSeen(session, target)
	}
	return tools.ErrorResult(errorJSON(ce.Code.Error(), ce.Text, ""))
}
