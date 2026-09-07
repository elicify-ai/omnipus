// Omnipus — ADR-068 D15.3 / spec §4.1.4: knowledge_edit, WRITE to one named
// file. THE LOGIC HALF plus the tool adapter live together here — unlike
// knowledge_describe.go/knowledge_read.go's split, this file already imports
// pkg/tools transitively through authoring_tools.go's AuthoringDeps (the
// mutation preamble: scope, lifecycle, audit), so there is no boundary left
// to preserve by splitting further.
//
// # What this file owns, and what it deliberately reuses rather than
// reimplements
//
// knowledge_edit is a CONSOLIDATION of the ADR-067 authoring tools
// (knowledge_create, knowledge_set_property, knowledge_append_section,
// knowledge_link) behind ONE tool name, per FR-070c: policy resolves on the
// tool name alone, so five near-synonymous tools with independent policy
// toggles were never the five-tools-in-one this file is instead. The write
// mechanics are NOT reimplemented:
//
//	author.go            CreateNote / EditNote / SetProperty / AppendSectionAt
//	authoring_tools.go   AuthoringDeps.begin (scope + lifecycle + audit),
//	                     .refuse, AddWikilink, AppendSectionOnce
//	vault_edit_list.go   SetPropertyList / AddListValue / RemoveListValue /
//	                     SetPropertyScalarChecked (FR-040a/FR-040b, this
//	                     Stage 3 agent's own new primitive)
//	vault_edit_schema.go the schema-aware refusal layer (FR-011/FR-042) this
//	                     agent adds on top of the primitives above
//	replace_body.go      ReplaceBody / ReplaceBodyByAnchor / ByLineRange —
//	                     landed separately (Stage 3, body-edit agent);
//	                     composed here unchanged
//
// Every op below still goes through EditNote (or CreateNote), so every write
// shares ONE lock, ONE version-token compare-and-swap and ONE atomic-write
// path — property edits, section appends, links and body replacements can
// never disagree about what a stale write means, because there is exactly
// one mechanism that decides.
//
// # FR-072: compact text, never JSON
//
// Every result knowledge_edit returns is rendered by RenderEdit as compact text.
// This is a genuine behavioural difference from the ADR-067 tools this one
// supersedes (CreateTool etc. return jsonResult(map[string]any{...}) — that
// convention predates FR-072's widening to "all six" vault_* tools and MUST
// NOT be copied here.
//
// # The blast-radius rule (FR-070b), restated as code
//
// knowledge_edit writes ONLY the file named in `path` — never a second file. The
// operator directive behind this file's own review cites a real regression
// on this branch: a "move" that silently hard-deleted a note because its
// destination happened to satisfy the containment check while also sitting
// in the indexer's own skip set. knowledge_edit has no operation that composes a
// destination from two arguments, targets a directory, or accepts any path
// other than the one named `path` — create, set_property, append_section,
// link and replace_body all resolve exactly one collection-relative path and
// hand it to CreateNote/EditNote, which write exactly that path. An op whose
// nature is "touches files the caller did not name" — rename, move, trash,
// or anything in the schema/view control plane — is refused BY NAME, naming
// the tool that owns it (knowledge_restructure / knowledge_configure), never
// attempted here under any argument spelling.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Operation names knowledge_edit's `op` argument accepts. This is the CLOSED set
// FR-070c requires: every one of these is equally acceptable to an operator
// who has granted knowledge_edit, because none of them can write a file the
// caller did not name.
const (
	opCreate        = "create"
	opSetProperty   = "set_property"
	opAppendSection = "append_section"
	opLink          = "link"
	opReplaceBody   = "replace_body"
)

// knowledgeEditOps lists the accepted ops, in the order they are documented —
// used to render "supported ops are ..." in a refusal.
var knowledgeEditOps = []string{opCreate, opSetProperty, opAppendSection, opLink, opReplaceBody}

// knowledgeEditRedirect names the EXACT refusal for an op that belongs to a
// different tool by construction (C-A: writes bytes into a file the caller
// did not name; C-B: changes what already-existing notes mean). Sending one
// of these names here is refused, never attempted under a different
// argument shape — see FR-070b, AC-E2, and this file's header.
var knowledgeEditRedirect = map[string]string{
	"rename":             "rename cascades to notes you did not name; use knowledge_restructure",
	"move":               "move cascades to notes you did not name; use knowledge_restructure",
	"trash":              "trash cascades to notes you did not name; use knowledge_restructure",
	"restore":            "restore cascades to notes you did not name; use knowledge_restructure",
	"create_record_type": "create_record_type changes what existing notes mean; use knowledge_configure",
	"edit_record_type":   "edit_record_type changes what existing notes mean; use knowledge_configure",
	"delete_record_type": "delete_record_type changes what existing notes mean; use knowledge_configure",
	"write_view":         "write_view changes what existing notes mean; use knowledge_configure",
	"delete_view":        "delete_view changes what existing notes mean; use knowledge_configure",
}

// editArgNames is every argument knowledge_edit's Parameters() declares, across
// all five ops (each field's own description above says which op(s) read
// it). tools.go's own doc comment on unknownArgs states the principle this
// applies here too: "a silently ignored argument is a caller that believes
// it narrowed something." knowledge_describe and knowledge_read already enforce it
// (tools.go); this tool did not, which let a misspelled field (e.g.
// "bodyy" for "body") pass through Execute's dispatch as if it had never
// been sent — the op still ran, using none of the caller's actual value,
// and reported success.
var editArgNames = []string{
	"op", "collection", "path", "expect_version",
	"template", "title", "body", "frontmatter",
	"property", "value", "list_op",
	"heading", "level", "once",
	"anchor", "line_range",
	"target", "alias", "section", "relation",
}

// EditTool is knowledge_edit.
type EditTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// NewEditTool builds knowledge_edit over deps — the same AuthoringDeps every
// mutation in this package shares (Home, Audit, NameShape, Now).
func NewEditTool(deps AuthoringDeps) *EditTool { return &EditTool{deps: deps} }

// Name is the registered tool name (ADR-068 D15.3, FR-070).
func (t *EditTool) Name() string { return "knowledge_edit" }

// Description names the WIDEST operation this tool grants (FR-070c,
// FR-079), not the most common one: replace_body can rewrite an arbitrary
// span of a note's body, which is a broader capability than setting one
// property.
func (t *EditTool) Description() string {
	return "Write ONE named file in a knowledge base: create a note (optionally from a " +
		"template), set a frontmatter property (a single value or a whole list), add or " +
		"remove one list item, append a section, link to another note, or replace part of " +
		"a note's body by anchor text or line range. Never touches a second file, never " +
		"renames or deletes anything, and never changes what OTHER notes mean — use " +
		"knowledge_restructure or knowledge_configure for those. Every write after the first on a " +
		"note requires the version token knowledge_read returned."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *EditTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *EditTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in. One flattened object
// covers every op, matching the pattern the tool it consolidates
// (authoring_tools.go) already uses — each field's description says which
// op(s) it applies to.
func (t *EditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"enum":        knowledgeEditOps,
				"description": "Which write to perform.",
			},
			"collection": collectionParam(),
			"path":       pathParam("The note to write"),
			"expect_version": map[string]any{
				"type": "string",
				"description": "Required for every op except create. The version token of the " +
					"note as you last saw it — knowledge_read returns one as 'version'. The write " +
					"is refused if the note changed in the meantime, instead of overwriting " +
					"whatever changed. If you do not have a token, knowledge_read the note first.",
			},

			// create
			"template": map[string]any{
				"type": "string",
				"description": "create: which of the collection's templates to start from, " +
					"by name (see knowledge_describe). Leave unset to create from 'body' alone.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "create: the note's title. Fills the template's {{title}} placeholder.",
			},
			"body": map[string]any{
				"type": "string",
				"description": "create: literal content when no template is given, written " +
					"exactly as given. append_section / replace_body: the text to write.",
			},
			"frontmatter": map[string]any{
				"type": "object",
				"description": "create: frontmatter properties to set on the new note — each " +
					"value a single value or a list. Applied on top of the template, so a " +
					"template's own defaults are overridden by anything named here.",
			},

			// set_property
			"property": map[string]any{
				"type":        "string",
				"description": "set_property: the property name, e.g. 'status'.",
			},
			"value": map[string]any{
				"description": "set_property: the property's new value — a single value, or a " +
					"list for a many-valued property. With list_op set, the ONE value to add " +
					"or remove.",
			},
			"list_op": map[string]any{
				"type": "string",
				"enum": []string{"add", "remove"},
				"description": "set_property: add or remove ONE value from a many-valued " +
					"property without sending the whole list back. Leave unset to replace the " +
					"whole value.",
			},

			// append_section
			"heading": map[string]any{
				"type":        "string",
				"description": "append_section: the section's heading, e.g. 'Decisions'.",
			},
			"level": map[string]any{
				"type":        "integer",
				"description": "append_section: heading level, 1-6. Default 2.",
			},
			"once": map[string]any{
				"type": "boolean",
				"description": "append_section: when true, does nothing if the note already " +
					"carries this heading and body.",
			},

			// replace_body
			"anchor": map[string]any{
				"type": "string",
				"description": "replace_body: exact text — copied from a knowledge_read response, " +
					"never retyped from memory — to replace. Refused if it matches more than " +
					"once or not at all. Give anchor or line_range, not both.",
			},
			"line_range": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"start": map[string]any{"type": "integer"},
					"end":   map[string]any{"type": "integer"},
				},
				"description": "replace_body: a 1-based, inclusive whole-file line range to " +
					"replace, as an alternative to anchor.",
			},

			// link
			"target": map[string]any{
				"type":        "string",
				"description": "link: the note to link to, by name or path.",
			},
			"alias": map[string]any{
				"type":        "string",
				"description": "link: words to display instead of the target's name. Optional.",
			},
			"section": map[string]any{
				"type": "string",
				"description": "link: heading to put a body wikilink under. Ignored when " +
					"'relation' is given.",
			},
			"relation": map[string]any{
				"type": "string",
				"description": "link: a relation property name to record the link on, instead " +
					"of inserting a wikilink in the body.",
			},
		},
		"required": []string{"op", "path"},
	}
}

// Execute dispatches by op.
func (t *EditTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	op := strings.TrimSpace(stringArg(args["op"]))
	authorOp := AuthorOpEdit
	if op == opCreate {
		authorOp = AuthorOpCreate
	}
	target, refusal := t.deps.begin(ctx, authorOp, args)
	if refusal != nil {
		return refusal
	}
	// Checked before the op switch, and against the FULL cross-op name set
	// (not the subset the resolved op happens to read): a misspelled or
	// stale field must never be silently dropped regardless of which op
	// carried it, matching knowledge_describe/knowledge_read's own posture
	// (tools.go's unknownArgs).
	if unknown := unknownArgs(args, editArgNames); len(unknown) > 0 {
		return t.deps.refuse(authorOp, target, nil, fmt.Sprintf(
			"unknown argument(s) %s; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(editArgNames, ", ")))
	}
	switch op {
	case opCreate:
		return t.execCreate(ctx, target, args)
	case opSetProperty:
		return t.execSetProperty(ctx, target, args)
	case opAppendSection:
		return t.execAppendSection(ctx, target, args)
	case opLink:
		return t.execLink(ctx, target, args)
	case opReplaceBody:
		return t.execReplaceBody(ctx, target, args)
	default:
		return t.refuseOp(target, op)
	}
}

func (t *EditTool) refuseOp(target mutationTarget, op string) *tools.ToolResult {
	if op == "" {
		return t.deps.refuse(AuthorOpEdit, target, nil,
			"'op' is required; one of "+strings.Join(knowledgeEditOps, ", "))
	}
	if msg, ok := knowledgeEditRedirect[op]; ok {
		return t.deps.refuse(AuthorOpEdit, target, nil, msg)
	}
	return t.deps.refuse(AuthorOpEdit, target, nil,
		fmt.Sprintf("unsupported op %q; supported ops are %s", op, strings.Join(knowledgeEditOps, ", ")))
}

// loadSchemas loads this write's governing schema set AND the load report
// naming any schema file the loader rejected (G4). The report used to be
// discarded (`set, _, err :=`) — a malformed schema file silently degraded
// its own record type to unconstrained, with no signal anywhere that it had
// happened. Every call site now threads the report through to
// knowledgeEditResolveSchema, which — when the type being WRITTEN is one of
// the rejected ones — reports knowledgeEditRejectedSchema instead of the
// indistinguishable knowledgeEditUnknownType. See knowledgeEditGovernance.Note
// for the disposition (a loud warning in the result, not a refusal) and its
// own doc comment for the argument against §4 of the design.
func (t *EditTool) loadSchemas(target mutationTarget) (*records.SchemaSet, *records.SchemaLoadReport, error) {
	set, report, err := records.LoadSchemas(target.collection.Root())
	if err != nil {
		return nil, nil, fmt.Errorf("loading record schemas: %w", err)
	}
	return set, report, nil
}

// ---------------------------------------------------------------------------
// create
// ---------------------------------------------------------------------------

func (t *EditTool) execCreate(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpCreate, target, nil, err.Error())
	}
	template := strings.TrimSpace(stringArg(args["template"]))
	title := strings.TrimSpace(stringArg(args["title"]))
	now := t.deps.now()

	var content []byte
	if template != "" {
		raw, terr := ReadTemplate(OSLinkFS(), target.collection, template)
		if terr != nil {
			return t.deps.refuse(AuthorOpCreate, target, []string{rel}, terr.Error())
		}
		useTitle := title
		if useTitle == "" {
			useTitle = strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		}
		content = ExpandTemplate(raw, TemplateVars{Title: useTitle, Now: now})
	} else {
		content = []byte(stringArg(args["body"]))
	}

	set, report, serr := t.loadSchemas(target)
	if serr != nil {
		return t.deps.refuse(AuthorOpCreate, target, []string{rel}, serr.Error())
	}
	pairs, perr := frontmatterArgToPairs(args["frontmatter"])
	if perr != nil {
		return t.deps.refuse(AuthorOpCreate, target, []string{rel}, perr.Error())
	}
	for _, p := range pairs {
		// gov is nil here deliberately: the per-pair splice's OWN governance
		// outcome is not what the caller needs reported — the assembled-
		// frontmatter check just below runs after every pair has landed and
		// reports governance for the note as a WHOLE (G1/G3), which is the
		// answer that also covers properties this loop never touches (raw
		// body/template bytes).
		//
		// knowledgeEditAutoSplitCommaList runs first (Issue 6/F3): `type`
		// is sorted to the front by frontmatterArgToPairs, so by the time a
		// later pair such as `tags` is spliced, `content` already carries
		// whatever `type:` this same call set, and the schema for it is
		// resolvable from the bytes as they stand right now.
		values, isList := knowledgeEditAutoSplitCommaList(set, content, p.Key, p.Values, p.IsList)
		next, eerr := knowledgeEditSetPropertyEdit(set, report, p.Key, values, isList, nil)(content)
		if eerr != nil {
			return t.deps.refuse(AuthorOpCreate, target, []string{rel},
				fmt.Sprintf("frontmatter.%s: %v", p.Key, eerr))
		}
		content = next
	}

	// G1: op:create's frontmatter ARGUMENT was already validated above, one
	// property at a time, but raw `body` and expanded `template` bytes reach
	// this point completely unchecked — a body containing its own
	// `---\ntype: company\nrevenue: not-a-number\n---` block used to be
	// written verbatim. Validate the FULLY ASSEMBLED content's frontmatter,
	// every property present in it, through the exact same authority, before
	// it ever reaches CreateNote.
	gov, verr := knowledgeEditValidateAssembledFrontmatter(set, report, content)
	if verr != nil {
		return t.deps.refuse(AuthorOpCreate, target, []string{rel}, verr.Error())
	}

	res, err := CreateNote(OSLinkFS(), target.collection, CreateNoteRequest{
		RelPath:   rel,
		Body:      content,
		Now:       now,
		NameShape: t.deps.NameShape,
		Audit:     t.deps.Audit,
		Actor:     target.actor(),
		Lock:      target.lock,
	})
	if err != nil {
		return knowledgeEditFailure(AuthorOpCreate, err)
	}
	// A create always changes bytes on disk (it never no-ops — ErrNoteExists
	// refuses instead), so the freshness index is refreshed unconditionally.
	// See author.go's "Index freshness" section for what a refresh failure
	// means and why it is a warning here, not a refusal.
	indexWarning := refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opCreate, Path: res.RelPath, Version: res.Version,
		Changed: true, Bytes: res.Bytes, Template: template,
		SchemaNote: gov.Note(), IndexWarning: indexWarning,
	}))
}

// frontmatterPair is one create.frontmatter entry, decoded to the shape
// knowledgeEditSetPropertyEdit needs.
type frontmatterPair struct {
	Key    string
	Values []string
	IsList bool
}

// frontmatterArgToPairs decodes create's frontmatter map deterministically —
// keys sorted alphabetically, with "type" moved first so a record type
// declared in the SAME call is in effect for every other property this call
// also sets (knowledgeEditValidateValue reads `type:` off the content as it
// stands at the moment each edit is applied).
func frontmatterArgToPairs(raw any) ([]frontmatterPair, error) {
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("'frontmatter' must be an object of property name to value (got %T)", raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.SliceStable(keys, func(i, j int) bool {
		return keys[i] == records.RecordTypeKey && keys[j] != records.RecordTypeKey
	})
	pairs := make([]frontmatterPair, 0, len(keys))
	for _, k := range keys {
		values, isList, err := decodeValueArg(m[k])
		if err != nil {
			return nil, fmt.Errorf("frontmatter.%s: %w", k, err)
		}
		pairs = append(pairs, frontmatterPair{Key: k, Values: values, IsList: isList})
	}
	return pairs, nil
}

// ---------------------------------------------------------------------------
// set_property
// ---------------------------------------------------------------------------

func (t *EditTool) execSetProperty(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpEdit, target, nil, err.Error())
	}
	property := strings.TrimSpace(stringArg(args["property"]))
	if property == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'property' is required")
	}
	// expect is handed to EditNote unchanged, including when empty:
	// EditNote's own FR-106 compare-and-swap refuses an empty token itself
	// (author.go's checkVersion, "EMPTY IS REFUSED TOO") with a
	// *ConflictError this file already renders via knowledgeEditFailure. A
	// second, tool-level "'expect_version' is required" check here was
	// dead weight — mutation-testing it (disabling the check) left every
	// missing-token case refused exactly as before, because EditNote was
	// always the layer actually deciding it. Removed rather than kept as
	// unverified redundancy.
	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	listOp := strings.TrimSpace(stringArg(args["list_op"]))
	if listOp != "" && listOp != "add" && listOp != "remove" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("'list_op' must be \"add\" or \"remove\" when given, not %q", listOp))
	}
	raw, present := args["value"]
	if !present || raw == nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'value' is required")
	}
	set, report, serr := t.loadSchemas(target)
	if serr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, serr.Error())
	}

	// gov is filled in by the edit closure when it actually runs (inside
	// EditNote, synchronously, before EditNote returns) — G3: the caller
	// needs to know whether nothing was checked, and why, not just whether
	// the write succeeded.
	var gov knowledgeEditGovernance
	var edit NoteEdit
	if listOp != "" {
		value, ok := jsonScalarToString(raw)
		if !ok {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				fmt.Sprintf("'value' must be a single text value when 'list_op' is set (got %T)", raw))
		}
		edit = knowledgeEditListOpEdit(set, report, property, value, listOp == "add", &gov)
	} else {
		values, isList, verr := decodeValueArg(raw)
		if verr != nil {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel}, verr.Error())
		}
		// knowledgeEditAutoSplitCommaList (Issue 6/F3) needs the note's OWN
		// bytes to resolve its `type:` and schema, and — unlike execCreate,
		// which already holds `content` at this point — set_property's
		// target file is only read inside EditNote, synchronously, right
		// before this closure runs. So the split has to happen INSIDE the
		// closure, against the `src` EditNote hands it, not out here.
		edit = func(src []byte) ([]byte, error) {
			splitValues, splitIsList := knowledgeEditAutoSplitCommaList(set, src, property, values, isList)
			return knowledgeEditSetPropertyEdit(set, report, property, splitValues, splitIsList, &gov)(src)
		}
	}

	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath: rel, Edits: []NoteEdit{edit}, ExpectVersion: expect,
		Now: t.deps.now(), Audit: t.deps.Audit, Actor: target.actor(), Lock: target.lock,
	})
	if err != nil {
		return knowledgeEditFailure(AuthorOpEdit, err)
	}
	// A no-op edit (res.Changed == false) changed no byte on disk, so there is
	// nothing for either index to re-derive — refreshing would cost a file
	// read for a file the write path never touched.
	var indexWarning string
	if res.Changed {
		indexWarning = refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	}
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opSetProperty, Path: res.RelPath, Version: res.Version,
		Property: property, ListOp: listOp, Changed: res.Changed,
		SchemaNote: gov.Note(), IndexWarning: indexWarning,
	}))
}

// ---------------------------------------------------------------------------
// append_section
// ---------------------------------------------------------------------------

func (t *EditTool) execAppendSection(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpEdit, target, nil, err.Error())
	}
	heading := strings.TrimSpace(stringArg(args["heading"]))
	if heading == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'heading' is required")
	}
	// expect is handed to EditNote unchanged, including when empty:
	// EditNote's own FR-106 compare-and-swap refuses an empty token itself
	// (author.go's checkVersion, "EMPTY IS REFUSED TOO") with a
	// *ConflictError this file already renders via knowledgeEditFailure. A
	// second, tool-level "'expect_version' is required" check here was
	// dead weight — mutation-testing it (disabling the check) left every
	// missing-token case refused exactly as before, because EditNote was
	// always the layer actually deciding it. Removed rather than kept as
	// unverified redundancy.
	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	level := 2
	if raw, ok := args["level"]; ok && raw != nil {
		level = intArg(raw, 2)
		if level < 1 || level > 6 {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'level' must be an integer from 1 to 6")
		}
	}
	once := boolArg(args["once"])
	body := stringArg(args["body"])

	edit := AppendSectionAt(level, heading, body)
	if once {
		edit = AppendSectionOnce(level, heading, body)
	}

	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath: rel, Edits: []NoteEdit{edit}, ExpectVersion: expect,
		Now: t.deps.now(), Audit: t.deps.Audit, Actor: target.actor(), Lock: target.lock,
	})
	if err != nil {
		return knowledgeEditFailure(AuthorOpEdit, err)
	}
	// See execSetProperty's identical comment: a no-op leaves the file
	// untouched, so nothing is re-indexed for it.
	var indexWarning string
	if res.Changed {
		indexWarning = refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	}
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opAppendSection, Path: res.RelPath, Version: res.Version,
		Heading: heading, Changed: res.Changed, IndexWarning: indexWarning,
	}))
}

// ---------------------------------------------------------------------------
// link
// ---------------------------------------------------------------------------

func (t *EditTool) execLink(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpEdit, target, nil, err.Error())
	}
	linkTarget := strings.TrimSpace(stringArg(args["target"]))
	if linkTarget == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'target' is required")
	}
	if _, cErr := cleanNoteArg(linkTarget); cErr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("the link target %q is not inside this collection", linkTarget))
	}
	// expect is handed to EditNote unchanged, including when empty:
	// EditNote's own FR-106 compare-and-swap refuses an empty token itself
	// (author.go's checkVersion, "EMPTY IS REFUSED TOO") with a
	// *ConflictError this file already renders via knowledgeEditFailure. A
	// second, tool-level "'expect_version' is required" check here was
	// dead weight — mutation-testing it (disabling the check) left every
	// missing-token case refused exactly as before, because EditNote was
	// always the layer actually deciding it. Removed rather than kept as
	// unverified redundancy.
	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	relation := strings.TrimSpace(stringArg(args["relation"]))

	// gov stays zero-value (Reason knowledgeEditGoverned, Note() == "") on
	// the non-relation branch below — a body wikilink never touches schema
	// governance at all, so there is nothing to report either way.
	var gov knowledgeEditGovernance
	var edit NoteEdit
	if relation != "" {
		set, report, serr := t.loadSchemas(target)
		if serr != nil {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel}, serr.Error())
		}
		edit = knowledgeEditLinkPropertyEdit(set, report, relation, "[["+linkTarget+"]]", &gov)
	} else {
		edit = AddWikilink(linkTarget,
			strings.TrimSpace(stringArg(args["alias"])),
			strings.TrimSpace(stringArg(args["section"])))
	}

	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath: rel, Edits: []NoteEdit{edit}, ExpectVersion: expect,
		Now: t.deps.now(), Audit: t.deps.Audit, Actor: target.actor(), Lock: target.lock,
	})
	if err != nil {
		return knowledgeEditFailure(AuthorOpEdit, err)
	}
	// See execSetProperty's identical comment: a no-op ("already linked")
	// leaves the file untouched, so nothing is re-indexed for it.
	var indexWarning string
	if res.Changed {
		indexWarning = refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	}
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opLink, Path: res.RelPath, Version: res.Version,
		Target: linkTarget, Relation: relation, Changed: res.Changed,
		SchemaNote: gov.Note(), IndexWarning: indexWarning,
	}))
}

// ---------------------------------------------------------------------------
// replace_body — composes replace_body.go's primitive, landed separately
// ---------------------------------------------------------------------------

func (t *EditTool) execReplaceBody(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpEdit, target, nil, err.Error())
	}
	// expect is handed to EditNote unchanged, including when empty:
	// EditNote's own FR-106 compare-and-swap refuses an empty token itself
	// (author.go's checkVersion, "EMPTY IS REFUSED TOO") with a
	// *ConflictError this file already renders via knowledgeEditFailure. A
	// second, tool-level "'expect_version' is required" check here was
	// dead weight — mutation-testing it (disabling the check) left every
	// missing-token case refused exactly as before, because EditNote was
	// always the layer actually deciding it. Removed rather than kept as
	// unverified redundancy.
	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	anchor := stringArg(args["anchor"])
	// 'body' must be PRESENT, not merely non-empty: an explicit "" is a
	// caller who deliberately wants the matched anchor/range deleted, and
	// that is a legitimate replace_body use. What is never legitimate is a
	// caller who meant to send replacement text and, through a typo'd key
	// or a forgotten field, sent none at all — stringArg(nil) and
	// stringArg("") are the identical "" this tool cannot tell apart from
	// bytes alone, so the presence check is the only place that distinction
	// can still be made. Before this check, replace_body with an anchor and
	// no 'body' argument matched the anchor text and spliced in "" —
	// silently deleting the matched span — and reported
	// "REPLACE_BODY (changed)", indistinguishable from an intentional
	// replacement.
	rawBody, bodyPresent := args["body"]
	if !bodyPresent || rawBody == nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			"'body' is required (send \"\" explicitly to delete the matched text)")
	}
	body := stringArg(rawBody)

	var lr *LineRange
	if raw, ok := args["line_range"]; ok && raw != nil {
		m, mok := raw.(map[string]any)
		if !mok {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				fmt.Sprintf("'line_range' must be an object with 'start' and 'end' (got %T)", raw))
		}
		lr = &LineRange{Start: intArg(m["start"], 0), End: intArg(m["end"], 0)}
	}

	edit := ReplaceBody(rel, anchor, lr, body)
	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath: rel, Edits: []NoteEdit{edit}, ExpectVersion: expect,
		Now: t.deps.now(), Audit: t.deps.Audit, Actor: target.actor(), Lock: target.lock,
	})
	if err != nil {
		return knowledgeEditFailure(AuthorOpEdit, err)
	}
	// See execSetProperty's identical comment: a no-op leaves the file
	// untouched, so nothing is re-indexed for it.
	var indexWarning string
	if res.Changed {
		indexWarning = refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	}
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opReplaceBody, Path: res.RelPath, Version: res.Version, Changed: res.Changed,
		IndexWarning: indexWarning,
	}))
}

// ---------------------------------------------------------------------------
// Compact-text rendering (FR-072) — no JSON document, ever
// ---------------------------------------------------------------------------

// EditData is what RenderEdit needs to describe one knowledge_edit outcome.
type EditData struct {
	Op       string
	Path     string
	Version  string
	Changed  bool
	Bytes    int
	Template string
	Property string
	ListOp   string
	Heading  string
	Target   string
	Relation string
	// SchemaNote is knowledgeEditGovernance.Note() (G3) — empty when a
	// schema governed the write (nothing further to say) or the op never
	// resolves one (append_section, replace_body, a body-only link); a
	// non-empty line otherwise, naming WHY nothing was checked, so the
	// caller can tell "validated and fine" from "nothing was checked" as
	// required by the design's acceptance scenario D-08.
	SchemaNote string
	// IndexWarning is refreshIndexesForNote's return value (author.go's
	// "Index freshness" section) — empty when the text index and the
	// properties index were both refreshed successfully, a sentence
	// otherwise naming what could not be kept current. The write itself has
	// already succeeded by the time this is set; see that section's header
	// for why a refresh failure is reported here rather than turned into a
	// tool refusal.
	IndexWarning string
}

// RenderEdit renders a successful knowledge_edit response as compact text
// (FR-072) — never a JSON document.
func RenderEdit(d EditData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — version %s\n", d.Path, d.Version)
	switch d.Op {
	case opCreate:
		extra := ""
		if d.Template != "" {
			extra = fmt.Sprintf(" from template %q", d.Template)
		}
		fmt.Fprintf(&b, "CREATED%s (%d bytes)\n", extra, d.Bytes)
	case opSetProperty:
		verb := "SET"
		switch d.ListOp {
		case "add":
			verb = "ADDED TO"
		case "remove":
			verb = "REMOVED FROM"
		}
		fmt.Fprintf(&b, "%s %s (%s)\n", verb, d.Property, changedWord(d.Changed))
	case opAppendSection:
		state := "appended"
		if !d.Changed {
			state = "unchanged — section already present"
		}
		fmt.Fprintf(&b, "APPEND_SECTION %q (%s)\n", d.Heading, state)
	case opLink:
		if d.Relation != "" {
			fmt.Fprintf(&b, "LINK %s -> %s (%s)\n", d.Relation, d.Target, changedWord(d.Changed))
		} else {
			fmt.Fprintf(&b, "LINK -> %s (%s)\n", d.Target, changedWord(d.Changed))
		}
	case opReplaceBody:
		fmt.Fprintf(&b, "REPLACE_BODY (%s)\n", changedWord(d.Changed))
	}
	if d.SchemaNote != "" {
		fmt.Fprintf(&b, "%s\n", d.SchemaNote)
	}
	if d.IndexWarning != "" {
		fmt.Fprintf(&b, "INDEX: %s\n", d.IndexWarning)
	}
	return b.String()
}

func changedWord(changed bool) string {
	if changed {
		return "changed"
	}
	return "unchanged — already so"
}

// knowledgeEditFailure renders an error from EditNote/CreateNote (author.go) or
// replace_body.go as compact text (FR-072) — the same failure classes
// authoring_tools.go's lowerLayerFailure handles, but never as
// conflictResult's JSON body.
func knowledgeEditFailure(op AuthorOperation, err error) *tools.ToolResult {
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return tools.ErrorResult(renderVersionConflict(conflict))
	}
	var lockErr *LockTimeoutError
	if errors.As(err, &lockErr) {
		return tools.ErrorResult(fmt.Sprintf("%s: %v", op, lockErr))
	}
	return tools.ErrorResult(fmt.Sprintf("%s: %v", op, err))
}

// renderVersionConflict renders a stale-token refusal in the spec's
// normative wording (FR-043, §4.1.4): "Deals/Acme.md changed since you read
// it; you have v1:ab12…, current is v1:cd34… — knowledge_read it again and
// re-apply".
func renderVersionConflict(e *ConflictError) string {
	switch {
	case e.Expected == "":
		return fmt.Sprintf("%s: a write must carry the version token knowledge_read returned "+
			"(FR-106) — knowledge_read it, then re-send with that token", e.Path)
	case e.Actual == "":
		return fmt.Sprintf("%s changed since you read it: it has been deleted — no change made", e.Path)
	default:
		return fmt.Sprintf("%s changed since you read it; you have %s, current is %s — "+
			"knowledge_read it again and re-apply", e.Path, e.Expected, e.Actual)
	}
}

// ---------------------------------------------------------------------------
// Argument decoding shared by create.frontmatter and set_property.value
// ---------------------------------------------------------------------------

// knowledgeEditAutoSplitCommaList is Issue 6/F3's fix for a specific,
// unambiguous round-trip failure: a many-valued property (`many: true`)
// whose note stores its value as a plain comma-joined scalar — e.g.
// `tags: a, b` — rather than a real YAML sequence. knowledge_describe /
// knowledge_read hand that scalar back to an agent exactly as written; the
// agent, having no reason to suspect the plain-looking string it just read
// is not "a value", sends it straight back into create/set_property and
// hits vault_edit_schema.go's arity refusal ("declared as a list; got a
// single value — send a list") on every attempt, because a bare scalar was
// never a legal shape for a many-valued property to begin with (see
// records.Property.Many's own doc comment: "a list property is never
// silently a scalar").
//
// That "never silently" rule is about a VALIDATED value disagreeing with
// its declaration — this function runs strictly BEFORE validation, on the
// caller's raw argument shape, and turns exactly one case of "obviously
// meant a list" into the list it names. It is the one caller of this
// package's decodeValueArg that ever changes the shape decodeValueArg
// reported, so the two only ever seem to disagree at this one, deliberate
// site.
//
// WHY AUTO-SPLIT (over sharpening the refusal's wording, the brief's other
// option): a bare scalar sent for a many-valued property was ALWAYS
// refused before this fix, in EVERY case — there is no existing legal
// scalar-for-many usage this could ever break, only a shape this function
// makes newly acceptable. Splitting is applied unconditionally whenever the
// property is many-valued and the caller sent a scalar (not only when the
// scalar contains a comma), because a zero-comma scalar is just the N=1
// case of the same comma-joined shape the corpus already uses.
//
// THE DOCUMENTED BOUNDARY (per the brief's "document the boundary"): this
// is inherently ambiguous for a many-valued property whose OWN elements may
// legitimately contain a literal comma — "New York, NY" as one tag reads
// identically to two tags "New York" and "NY". This function cannot tell
// those apart from a bare string and does not try; it always chooses "N
// values". A caller that means one value containing a literal comma MUST
// send an explicit JSON list (`["New York, NY"]`) — decodeValueArg reports
// that shape as isList==true already, and this function is a no-op
// whenever isList is already true, so an explicit list always passes
// through untouched.
//
// Each split element is whitespace-trimmed (the corpus's own comma-joined
// convention, e.g. "a, b", puts a space after the comma) and an empty
// element (from a leading/trailing/doubled comma) is dropped. If every
// element is empty after trimming, the value is left unchanged — an empty
// string sent for a many-valued property is not "obviously a list" and is
// better left to the ordinary arity refusal than silently turned into an
// empty list.
//
// Enum validation is NOT this function's job and is untouched by it:
// splitting only changes the SHAPE (scalar -> list) that gets handed to
// knowledgeEditSetPropertyEdit; that function still runs records.ParseValue
// against every resulting element exactly as it does for a list the caller
// sent directly, so an invalid enum member is refused exactly as before —
// this function has no way to see, and no need to see, what the property's
// type is beyond its declared arity.
func knowledgeEditAutoSplitCommaList(set *records.SchemaSet, content []byte, property string, values []string, isList bool) ([]string, bool) {
	if set == nil || isList || len(values) != 1 {
		return values, isList
	}
	fm, ferr := records.ParseFrontmatter(content)
	if ferr != nil {
		return values, isList
	}
	typeName := (records.Record{Frontmatter: fm}).TypeName()
	if typeName == "" {
		return values, isList
	}
	schema, ok := set.Get(typeName)
	if !ok {
		return values, isList
	}
	prop, ok := schema.Property(property)
	if !ok || !prop.Many {
		return values, isList
	}
	parts := strings.Split(values[0], ",")
	split := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			split = append(split, trimmed)
		}
	}
	if len(split) == 0 {
		return values, isList
	}
	return split, true
}

// decodeValueArg reads a "value" (or one frontmatter entry) that may be a
// scalar (text, a number, a boolean) or a list of those, and reports which
// shape the caller actually sent — the shape the arity check in
// vault_edit_schema.go measures against.
func decodeValueArg(raw any) (values []string, isList bool, err error) {
	if list, ok := raw.([]any); ok {
		out := make([]string, 0, len(list))
		for i, el := range list {
			s, sok := jsonScalarToString(el)
			if !sok {
				return nil, false, fmt.Errorf("[%d] must be text, a number or a boolean (got %T)", i, el)
			}
			out = append(out, s)
		}
		return out, true, nil
	}
	s, ok := jsonScalarToString(raw)
	if !ok {
		return nil, false, fmt.Errorf("must be text, a number, a boolean, or a list of those (got %T)", raw)
	}
	return []string{s}, false, nil
}

// jsonScalarToString converts one JSON-decoded scalar to its text form —
// what the frontmatter splice writes. Not every JSON type is a scalar this
// tool accepts: an object is refused (ok=false) rather than silently
// stringified.
func jsonScalarToString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		return strconv.FormatBool(x), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case json.Number:
		return x.String(), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	default:
		return "", false
	}
}
