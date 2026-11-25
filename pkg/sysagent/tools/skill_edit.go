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
	"slices"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

var editSkillActions = []string{"copy", "delete_file", "delete", "restore", "set_category"}

// editSkillActionParams names the parameters each action accepts besides
// action and skill (§5.4: unknown or misplaced parameters → INVALID_INPUT).
var editSkillActionParams = map[string]string{
	"copy":        "to",
	"delete_file": "path",
	"delete":      "",
	"restore":     "version",
	// set_category (Skills & Tools C-21): choose the category of an
	// uncategorised skill, once.
	"set_category": "category",
}

// editSkillOptionalParams names the optional parameter an action accepts
// (FR-056: copy may choose the new skill's category).
var editSkillOptionalParams = map[string]string{
	"copy": "category",
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
		"Built-in skills are read-only: copy them first. A new copy is granted to no agent. " +
		"action=set_category chooses the category of a skill that has none yet (category); it can be chosen " +
		"only once and is never written into the skill files."
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
			"category": map[string]any{
				"type": "string", "enum": settableSkillCategories(),
				"description": "set_category, or optionally copy: the category to show the skill under.",
			},
		},
		"required": []string{"action", "skill"},
	}
}

type editSkillRequest struct {
	action, skill, to, path, version, category string
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
		case "to", "path", "version", "category":
			if key != allowed && key != editSkillOptionalParams[action] {
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
		case "category":
			req.category = v
		}
	}
	if optional := editSkillOptionalParams[action]; optional != "" {
		if _, given := args[optional]; given {
			v, err := str(optional)
			if err != nil {
				return req, err
			}
			req.category = v
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
	if req.action == "set_category" {
		return setSkillCategoryOnce(root, req)
	}
	if req.action == "copy" && req.category != "" && !skills.IsSkillCategory(req.category) {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", skills.UnknownCategoryError(req.category).Error()+". Nothing was copied.",
			"choose one of the listed categories"))
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
		// Founder Q-S3: the same best-effort grant hygiene as the REST delete.
		change.Kind, change.Grants = skills.KindDelete, t.deps.SkillGrants
	case "restore":
		change.Kind, change.Version = skills.KindRestore, req.version
	}
	// A built-in is read-only (§7.1 step 0) and has no revision a read could
	// record, so its refusal precedes the read precondition (UAT-1 F-3).
	if refusal := skills.RefuseBuiltinTarget(change); refusal != nil {
		return editSkillFailure(session, req.skill, refusal)
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
	// The pipeline already wrote the §5.4 copy text (once) into out.Message.
	message := out.Message
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
	warning := out.Warning
	if req.action == "copy" && req.category != "" && req.category != skills.CategoryUncategorised {
		// FR-056: the category is stored in Omnipus's own records, never in
		// the skill files. The copy is already committed, so a failed write
		// is reported with it rather than undoing the copy.
		if err := skills.SetCategoryOnce(root, req.to, req.category); err != nil {
			logger.ErrorCF("sysagent", "edit_skill: skill copied but its category was not saved",
				map[string]any{"skill": req.to, "category": req.category, "error": err.Error()})
			warning = strings.TrimSpace(warning + " The category was not saved: " + err.Error())
		} else {
			fields["category"] = req.category
		}
	}
	if warning != "" {
		fields["warning"] = warning
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

// settableSkillCategories are the categories set_category accepts: every
// category except uncategorised.
func settableSkillCategories() []string {
	var out []string
	for _, c := range skills.SkillCategories() {
		if c != skills.CategoryUncategorised {
			out = append(out, c)
		}
	}
	return out
}

// setSkillCategoryOnce is edit_skill set_category (C-21): the same
// once-only rule as PUT /skills/{name}/category. The category is stored in
// Omnipus's own records, never in the skill files.
func setSkillCategoryOnce(root string, req editSkillRequest) *tools.ToolResult {
	if _, _, err := skills.SkillVerified(root, req.skill); errors.Is(err, skills.ErrNotFound) {
		return tools.ErrorResult(errorJSON("NOT_FOUND", fmt.Sprintf("skill %q is not installed", req.skill),
			"call list_skills for the installed skills"))
	}
	if !slices.Contains(settableSkillCategories(), req.category) {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", skills.UnknownCategoryError(req.category).Error(),
			"choose one of the listed categories"))
	}
	switch err := skills.SetCategoryOnce(root, req.skill, req.category); {
	case errors.Is(err, skills.ErrCategorySet):
		return tools.ErrorResult(errorJSON("CATEGORY_SET", skills.CategorySetText, "leave the category as it is"))
	case err != nil:
		return tools.ErrorResult(errorJSON("WRITE_FAILED", "the category could not be saved: "+err.Error(),
			"try again; if it keeps failing, report it"))
	}
	return changeSuccess(map[string]any{
		"success":  true,
		"action":   req.action,
		"skill":    req.skill,
		"category": req.category,
		"message":  fmt.Sprintf("Category of %s set to %s.", req.skill, req.category),
	}, working("the category is stored in the skill records"))
}
