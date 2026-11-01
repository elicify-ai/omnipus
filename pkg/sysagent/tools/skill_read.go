// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

// get_skill — the one reader of one installed skill (skills spec r15 §5.2):
// required `fields`, version paging, the §5.2 who-may-call table and the
// §7.3 seen-record side effect. Content, file list and revision come from
// one captured observation (skills.ObserveSkill).
//
// Field selection follows T8's shared rule (§5.2 "uses T8's shared helper").
// That helper (pkg/tools/fieldselect.go) has not landed on this branch, so
// the same validation texts and size rule are implemented here; when T8
// lands, this file switches to the shared helper without a behaviour change.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// skillDetailFields is the 14 selectable SkillDetail properties (§5.2, C-7),
// in contract order. TestGetSkill_FieldsEnumMatchesSkillDetailContract
// compares it with contracts/components/schemas/SkillDetail.yaml.
var skillDetailFields = []string{
	"state", "name", "description", "shelf", "folder", "files", "metadata",
	"compatibility", "allowed_tools", "needs", "last_change", "has_local_changes",
	"versions", "instructions",
}

const (
	skillResultCharBudget  = 48000 // T8 shared result bound (characters)
	skillCutChars          = 8000  // T8 cut length for cuttable strings
	skillFilesDisplayCap   = 200   // §5.2 files.items cap
	skillVersionsDefault   = 50
	skillVersionsMaxLimit  = 100
	skillVersionsMinLimit  = 1
	getSkillDescriptionEnd = "If versions make the result too large, lower versions_limit."
)

// GetSkillTool is §5.2's one reader of one installed skill.
type GetSkillTool struct{ deps *Deps }

// NewGetSkillTool builds the get_skill tool over the system-tool deps.
func NewGetSkillTool(d *Deps) *GetSkillTool { return &GetSkillTool{deps: d} }

// Name implements tools.Tool.
func (t *GetSkillTool) Name() string { return "get_skill" }

// Scope implements tools.Tool.
func (t *GetSkillTool) Scope() tools.ToolScope { return tools.ScopeCore }

// Category implements tools.Tool (CategorySkills, as the other skill tools).
func (t *GetSkillTool) Category() tools.ToolCategory { return tools.CategorySkills }

// Description implements tools.Tool.
func (t *GetSkillTool) Description() string {
	return "Read one installed skill's details by id. fields is required: pass [\"all\"] or one or more of: " +
		strings.Join(skillDetailFields, ", ") + ". versions lists retained versions newest first " +
		"(versions_limit 1-100, default 50; pass next_before back as versions_before for older ones). " +
		"A successful read lets this session change the skill afterwards. " + getSkillDescriptionEnd
}

// Parameters implements tools.Tool (§5.2: id, fields, versions_before,
// versions_limit).
func (t *GetSkillTool) Parameters() map[string]any {
	enum := append([]string{"all"}, skillDetailFields...)
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "The skill id (slug)."},
			"fields": map[string]any{
				"type": "array", "minItems": 1, "uniqueItems": true,
				"items":       map[string]any{"type": "string", "enum": enum},
				"description": `["all"] or one or more property names.`,
			},
			"versions_before": map[string]any{"type": "string", "description": "A version id; the versions page then holds only older versions."},
			"versions_limit": map[string]any{
				"type": "integer", "minimum": skillVersionsMinLimit, "maximum": skillVersionsMaxLimit,
				"default": skillVersionsDefault, "description": "Page size of versions.",
			},
		},
		"required": []string{"id", "fields"},
	}
}

// Execute implements tools.Tool.
func (t *GetSkillTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	req, refusal := parseGetSkillArgs(args)
	if refusal != nil {
		return refusal
	}
	root, ok := skillsRootOf(t.deps)
	if !ok {
		return tools.ErrorResult(errorJSON("NOT_AVAILABLE", "skills directory not configured", "ensure the gateway is started with a valid home"))
	}
	actor := strings.TrimSpace(tools.ToolAgentID(ctx))
	session := tools.ToolTranscriptSessionID(ctx)
	if err := authorizeGetSkill(t.deps, actor, root, req.id); err != nil {
		var nf notFoundError
		if errors.As(err, &nf) {
			return tools.ErrorResult(errorJSON("NOT_FOUND", fmt.Sprintf("skill %q is not installed", req.id), "use list_skills to see installed skills"))
		}
		var denied permissionError
		if errors.As(err, &denied) {
			return tools.ErrorResult(errorJSON("PERMISSION_DENIED", fmt.Sprintf("skill %q is not available to this agent", req.id), ""))
		}
		return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not read skill %q: %v", req.id, err), "retry get_skill"))
	}
	obs, err := skills.ObserveSkill(root, req.id, skills.VersionQuery{Want: req.want["versions"], Before: req.before, Limit: req.limit})
	if err != nil {
		return t.observeFailure(session, req.id, err)
	}
	res, refusal := renderSkillDetail(obs, req)
	if refusal != nil {
		return refusal
	}
	if obs.Present {
		skills.RecordSeen(session, obs.Slug, obs.Revision)
	}
	return tools.NewToolResult(res)
}

func (t *GetSkillTool) observeFailure(session, id string, err error) *tools.ToolResult {
	var ce *skills.ChangeError
	switch {
	case errors.Is(err, skills.ErrNotFound):
		return tools.ErrorResult(errorJSON("NOT_FOUND", fmt.Sprintf("skill %q is not installed", id), "use list_skills to see installed skills"))
	case errors.Is(err, skills.ErrUnknownVersion) && errors.As(err, &ce):
		return tools.ErrorResult(errorJSON("INVALID_INPUT", ce.Text, "omit versions_before to start from the newest version"))
	case errors.As(err, &ce):
		// §9.5: a read that could not observe the state vouches for no
		// revision, and removes any earlier one for this session.
		if errors.Is(err, skills.ErrSkillInstructionLimit) || errors.Is(err, skills.ErrSkillInstructionUnresolved) {
			skills.ForgetSeen(session, id)
		}
		return tools.ErrorResult(errorJSON(ce.Code.Error(), ce.Text, ""))
	default:
		skills.ForgetSeen(session, id)
		return tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not read skill %q: %v", id, err), "retry get_skill"))
	}
}

type getSkillRequest struct {
	id     string
	fields []string // requested names in contract order
	want   map[string]bool
	before string
	limit  int
}

func parseGetSkillArgs(args map[string]any) (getSkillRequest, *tools.ToolResult) {
	invalid := func(msg string) (getSkillRequest, *tools.ToolResult) {
		return getSkillRequest{}, tools.ErrorResult(errorJSON("INVALID_INPUT", msg, ""))
	}
	for key := range args {
		switch key {
		case "id", "fields", "versions_before", "versions_limit":
		default:
			return invalid(fmt.Sprintf("unknown parameter %q", key))
		}
	}
	id, _ := args["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" || validateID(id) != nil || !skills.ValidSlug(id) {
		return invalid("id must be a skill slug (letters, digits and single hyphens)")
	}
	fields, msg := validateSkillFields(args["fields"])
	if msg != "" {
		return invalid(msg)
	}
	req := getSkillRequest{id: id, fields: fields, want: map[string]bool{}, limit: skillVersionsDefault}
	for _, f := range fields {
		req.want[f] = true
	}
	if raw, ok := args["versions_before"]; ok {
		s, isString := raw.(string)
		if !isString || strings.TrimSpace(s) == "" {
			return invalid("versions_before must be a version id")
		}
		req.before = s
	}
	if raw, ok := args["versions_limit"]; ok {
		n, valid := integerArg(raw)
		if !valid || n < skillVersionsMinLimit || n > skillVersionsMaxLimit {
			return invalid(fmt.Sprintf("versions_limit must be an integer from %d to %d", skillVersionsMinLimit, skillVersionsMaxLimit))
		}
		req.limit = n
	}
	return req, nil
}

// validateSkillFields applies T8's shared `fields` rule and returns the
// requested names in contract order, or the refusal text.
func validateSkillFields(raw any) ([]string, string) {
	valid := strings.Join(skillDetailFields, ", ")
	required := `fields is required for skill: pass ["all"] or one or more of: ` + valid
	list, ok := raw.([]any)
	if !ok {
		if ss, isStrings := raw.([]string); isStrings {
			for _, s := range ss {
				list = append(list, s)
			}
			ok = true
		}
	}
	if !ok || len(list) == 0 {
		return nil, required
	}
	names := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		name, isString := item.(string)
		if !isString {
			return nil, required
		}
		if seen[name] {
			return nil, fmt.Sprintf("duplicate field %q for skill", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if seen["all"] {
		if len(names) > 1 {
			return nil, `"all" cannot be combined with other field names`
		}
		return append([]string{}, skillDetailFields...), ""
	}
	for _, name := range names {
		if !containsString(skillDetailFields, name) {
			return nil, fmt.Sprintf("unknown field %q for skill. Valid: all, %s", name, valid)
		}
	}
	ordered := make([]string, 0, len(names))
	for _, f := range skillDetailFields {
		if seen[f] {
			ordered = append(ordered, f)
		}
	}
	return ordered, ""
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// skillsRootOf is the registry skills directory the tools act on.
func skillsRootOf(d *Deps) (string, bool) {
	if d == nil {
		return "", false
	}
	if d.SkillWriter != nil && d.SkillWriter.Root() != "" {
		return d.SkillWriter.Root(), true
	}
	if d.Home != "" {
		return filepath.Join(d.Home, "skills"), true
	}
	return "", false
}

type notFoundError struct{}

func (notFoundError) Error() string { return "not installed" }

type permissionError struct{}

func (permissionError) Error() string { return "not available" }

// authorizeGetSkill applies §5.2's who-may-call table. Authorization is
// decided before existence (§5.2 error precedence): NOT_FOUND goes only to a
// caller with authority, when neither a live folder nor history exists.
func authorizeGetSkill(d *Deps, actor, root, id string) error {
	if actor == "" || d == nil || d.ResolveToolPolicy == nil {
		return permissionError{}
	}
	onDisk, present, err := skills.ResolveInstalledSlug(root, id)
	if err != nil {
		return err
	}
	historyID, _, err := skills.NewestVersion(root, onDisk)
	if err != nil {
		return err
	}
	if toolNotDenied(d, actor, "edit_skill") {
		if !present && historyID == "" {
			return notFoundError{}
		}
		return nil // editor: installed skills and deleted skills with history
	}
	if present && skillManagementReader(d, actor) {
		return nil
	}
	if present {
		bound, err := agentMayLoad(d, actor, root, onDisk)
		if err != nil {
			return err
		}
		if bound {
			return nil
		}
	}
	return permissionError{}
}

// toolNotDenied reports whether actor's effective policy for tool is allow
// or ask (an unregistered tool counts as denied).
func toolNotDenied(d *Deps, actor, tool string) bool {
	policy, ok := d.ResolveToolPolicy(actor, tool)
	return ok && (policy == string(config.ToolPolicyAllow) || policy == string(config.ToolPolicyAsk))
}

// skillManagementReader is §5.2's management-read authority: list_skills not
// denied and one of create_agent, update_agent, edit_skill not denied.
func skillManagementReader(d *Deps, actor string) bool {
	if !toolNotDenied(d, actor, "list_skills") {
		return false
	}
	for _, candidate := range []string{"create_agent", "update_agent", "edit_skill"} {
		if toolNotDenied(d, actor, candidate) {
			return true
		}
	}
	return false
}

// agentMayLoad reports whether the skill is in actor's loadable set: granted
// by name and the grant bound to the folder's current generation (SD-30).
func agentMayLoad(d *Deps, actor, root, onDisk string) (bool, error) {
	if d.GetCfg == nil {
		return false, nil
	}
	cfg := d.GetCfg()
	if cfg == nil {
		return false, nil
	}
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if a.ID != actor {
			continue
		}
		granted := false
		for _, s := range a.Skills {
			if strings.EqualFold(strings.TrimSpace(s), onDisk) {
				granted = true
				break
			}
		}
		if !granted {
			return false, nil
		}
		return skills.GrantBound(filepath.Join(root, onDisk), onDisk, a.SkillGrants)
	}
	return false, nil
}

// renderSkillDetail projects an observation onto the requested fields and
// applies T8's size rule.
func renderSkillDetail(obs *skills.SkillObservation, req getSkillRequest) (string, *tools.ToolResult) {
	out := map[string]any{"id": obs.Slug}
	var absent []string
	var instructions string
	hasInstructions := false
	if obs.Deleted {
		out["revision"] = nil
		for _, f := range req.fields {
			switch f {
			case "state":
				out["state"] = "deleted"
			case "versions":
				out["versions"] = versionsValue(obs)
			default:
				absent = append(absent, f)
			}
		}
	} else {
		out["revision"] = obs.Revision
		meta := obs.Meta(os.Getenv("PATH"))
		for _, f := range req.fields {
			needsInstruction := f == "name" || f == "description" || f == "metadata" ||
				f == "compatibility" || f == "allowed_tools" || f == "needs" || f == "instructions"
			if needsInstruction && !obs.HasInstruction {
				absent = append(absent, f) // a folder without SKILL.md (§7.3 present, repairable)
				continue
			}
			switch f {
			case "state":
				out[f] = "installed"
			case "name":
				out[f] = meta.Name
			case "description":
				out[f] = meta.Description
			case "shelf":
				out[f] = shelfOf(obs)
			case "folder":
				out[f] = realFolder(obs.Dir)
			case "files":
				out[f] = filesValue(obs)
			case "metadata":
				out[f] = meta.Metadata
			case "compatibility":
				if meta.Compatibility != nil {
					out[f] = *meta.Compatibility
				} else {
					out[f] = nil
				}
			case "allowed_tools":
				out[f] = meta.AllowedTools
			case "needs":
				out[f] = needsValue(meta.Needs)
			case "last_change":
				out[f] = lastChangeValue(obs.LastChange)
			case "has_local_changes":
				out[f] = obs.HasLocalChanges
			case "versions":
				out[f] = versionsValue(obs)
			case "instructions":
				instructions, hasInstructions = meta.Body, true
				out[f] = instructions
			}
		}
	}
	if len(absent) > 0 {
		out["absent_fields"] = absent
	}
	body, err := encodeCompact(out)
	if err != nil {
		return "", tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not encode skill %q: %v", obs.Slug, err), ""))
	}
	if charCount(body) <= skillResultCharBudget {
		return body, nil
	}
	if hasInstructions {
		if runes := []rune(instructions); len(runes) > skillCutChars {
			out["instructions"] = string(runes[:skillCutChars]) + fmt.Sprintf("…[cut: %s of %s characters]", groupDigits(skillCutChars), groupDigits(len(runes)))
			out["truncated_fields"] = []string{"instructions"}
			body, err = encodeCompact(out)
			if err != nil {
				return "", tools.ErrorResult(errorJSON("READ_FAILED", fmt.Sprintf("could not encode skill %q: %v", obs.Slug, err), ""))
			}
			if charCount(body) <= skillResultCharBudget {
				return body, nil
			}
		}
	}
	return "", tools.ErrorResult(errorJSON("RESULT_TOO_LARGE",
		fmt.Sprintf("result too large for skill %q. Request fewer fields.", obs.Slug), getSkillDescriptionEnd))
}

func shelfOf(obs *skills.SkillObservation) string {
	if obs.Builtin {
		return "builtin"
	}
	return "registry"
}

func realFolder(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return dir
}

func filesValue(obs *skills.SkillObservation) map[string]any {
	items := make([]map[string]any, 0, min(len(obs.Files), skillFilesDisplayCap))
	for i, f := range obs.Files {
		if i >= skillFilesDisplayCap {
			break
		}
		items = append(items, map[string]any{"path": f.Path, "size": f.Size, "exec": f.Exec})
	}
	return map[string]any{"items": items, "total": len(obs.Files)}
}

func needsValue(needs []skills.NeedStatus) []map[string]any {
	out := make([]map[string]any, 0, len(needs))
	for _, n := range needs {
		row := map[string]any{"program": n.Program, "status": "missing"}
		if n.Present {
			row["status"], row["path"] = "present", n.Path
		}
		out = append(out, row)
	}
	return out
}

func lastChangeValue(lc skills.LastChange) map[string]any {
	switch {
	case lc.ShippedWith != "":
		return map[string]any{"shipped_with": lc.ShippedWith}
	case lc.Unknown:
		return map[string]any{"unknown": true}
	}
	paths := lc.Paths
	if paths == nil {
		paths = []string{}
	}
	return map[string]any{"at": lc.At, "actor": lc.Actor, "tool": lc.Tool, "paths": paths}
}

func versionsValue(obs *skills.SkillObservation) map[string]any {
	items := make([]map[string]any, 0, len(obs.Versions))
	for _, v := range obs.Versions {
		row := map[string]any{"id": v.ID, "kind": v.Kind, "actor": v.Actor, "at": v.At, "paths": v.Paths, "paths_total": v.PathsTotal}
		if v.Tool != "" {
			row["tool"] = v.Tool
		}
		items = append(items, row)
	}
	var next any
	if obs.NextBefore != "" {
		next = obs.NextBefore
	}
	return map[string]any{"items": items, "total": obs.VersionsTotal, "next_before": next}
}

// encodeCompact renders v as compact JSON without HTML escaping, so the
// character budget counts what the model actually receives.
func encodeCompact(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buf.String(), "\n"), nil
}

func charCount(s string) int { return len([]rune(s)) }

func groupDigits(n int) string {
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
