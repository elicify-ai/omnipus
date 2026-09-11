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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
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
	// opEmbed is US-11 / EMB-095: agent-authored embedded content. It is an
	// OPERATION on this tool, not a new tool name — see this const block's
	// own header ("every one of these is equally acceptable to an operator
	// who has granted knowledge_edit") and EMB-096 (the tool-policy diff for
	// this op is zero lines, in both the global ceiling and every per-agent
	// seed — TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp pins it).
	opEmbed = "embed"
	// opRelation is ADR-068 D15/FR-045: the three explicit relation verbs
	// (add / remove / replace) on a frontmatter relation or person property.
	// Like opEmbed it is an OPERATION on this tool, never a ninth tool name —
	// the tool-policy diff is ZERO LINES in both the global ceiling and every
	// per-agent seed, and TestKnowledgeToolPolicy_CatalogueUnchangedByEmbedOp
	// (set equality over the eight knowledge_* names) pins that for this op
	// too. See knowledge_edit_relation.go's header for the Hard Constraint #6
	// argument in full.
	opRelation = "relation"
)

// knowledgeEditOps lists the accepted ops, in the order they are documented —
// used to render "supported ops are ..." in a refusal.
var knowledgeEditOps = []string{opCreate, opSetProperty, opAppendSection, opLink, opReplaceBody, opEmbed, opRelation}

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
	"target", "alias", "section",
	// embed (US-11). 'view', 'target_heading' and 'target_block' are the
	// embed's OWN fragment modifiers — named distinctly from 'heading'
	// (append_section's destination heading) and from 'section' (link's and
	// embed's shared destination-section argument) because both of those
	// already exist and mean the OPPOSITE thing: 'heading' names a heading
	// on the note BEING WRITTEN; 'target_heading' names one on the note
	// BEING EMBEDDED (EMB-101). 'page' is accepted but always refused today
	// — reserved for the deferred PDF-page-fragment kind (US-12/EMB-105),
	// never silently ignored in the meantime (see execEmbed).
	"view", "target_heading", "target_block", "width", "page",
	// relation (FR-045). 'relation_op' is named distinctly from 'list_op'
	// (set_property's own add/remove sub-verb) because the two accept
	// different verb sets — list_op has no "replace" — and from 'relation'
	// (op link's relation PROPERTY NAME, which this op calls 'property',
	// matching RelationWriteRequest's own field). 'targets' is plural and a
	// LIST, distinct from op link's and op embed's singular 'target'.
	"relation_op", "targets",
}

// editOpArgs is the CLOSED, PER-OPERATION argument set each op actually
// reads — enforced in ADDITION to, not instead of, the global sweep above
// (editArgNames, via unknownArgs in Execute). The two catch different
// mistakes:
//
//   - editArgNames catches a MISSPELLED or invented field name shared by no
//     op at all (e.g. "bodyy" for "body") — a shape no operation would ever
//     read.
//   - editOpArgs catches a field name that is a genuine argument of a
//     DIFFERENT op, sent to one that does not read it — e.g. op="link"
//     given 'width' (embed's argument), or op="embed" given 'body'
//     (create/append_section/replace_body's argument). Both pass the
//     global sweep, because both names are legitimate members of
//     editArgNames; only the per-operation set catches them (EMB-100).
//
// Every entry always includes "op", "collection" and "path" (every op reads
// them) and "expect_version" (every op but create — see execCreate's own
// header comment on why create alone omits it).
var editOpArgs = map[string][]string{
	opCreate:        {"op", "collection", "path", "template", "title", "body", "frontmatter"},
	opSetProperty:   {"op", "collection", "path", "expect_version", "property", "value", "list_op"},
	opAppendSection: {"op", "collection", "path", "expect_version", "heading", "level", "once", "body"},
	opLink:          {"op", "collection", "path", "expect_version", "target", "alias", "section"},
	opReplaceBody:   {"op", "collection", "path", "expect_version", "anchor", "line_range", "body"},
	opEmbed: {
		"op", "collection", "path", "expect_version",
		"target", "section", "view", "target_heading", "target_block", "width", "page",
	},
	opRelation: {"op", "collection", "path", "expect_version", "property", "relation_op", "targets"},
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
		"remove one list item, append a section, link to another note, embed a picture, " +
		"PDF, note, or a saved data view under a heading (the correct notation is written " +
		"for you, after checking the target exists), add or remove relation links on a " +
		"property without sending the whole list back, or replace part of a note's body by " +
		"anchor text or line range. Never touches a second file, never renames or deletes " +
		"anything, and never changes what OTHER notes mean — use knowledge_restructure or " +
		"knowledge_configure for those. Every write after the first on a note requires the " +
		"version token knowledge_read returned."
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
				"type": "string",
				"description": "set_property: the property name, e.g. 'status'. relation: the " +
					"relation or person property to change, e.g. 'company'.",
			},
			"value": map[string]any{
				"description": "set_property: the property's new value — a single value, or a " +
					"list for a many-valued property. With list_op set, the ONE value to add " +
					"or remove. Cannot write a relation or person property — use op 'relation'.",
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

			// link, embed
			"target": map[string]any{
				"type": "string",
				"description": "link: the note to link to, by name or path. embed: the note, " +
					"picture, PDF or data file to embed, by path relative to the collection " +
					"root — checked to exist before anything is written.",
			},
			"alias": map[string]any{
				"type":        "string",
				"description": "link: words to display instead of the target's name. Optional.",
			},
			"section": map[string]any{
				"type": "string",
				"description": "link: heading to put the wikilink under. embed: heading to " +
					"put the embed under; created if absent.",
			},

			// embed (US-11). At most one of view/target_heading/target_block.
			"view": map[string]any{
				"type": "string",
				"description": "embed: which saved view of a data file (a .base target) to " +
					"show, by its display label exactly as knowledge_describe reports it. " +
					"Refused if the target is not a data file or the label does not match " +
					"an existing view.",
			},
			"target_heading": map[string]any{
				"type": "string",
				"description": "embed: show only this heading's section of the embedded note. " +
					"Names a heading ON THE EMBED TARGET, not on the note being written — see " +
					"'heading', which is append_section's unrelated destination heading. " +
					"Refused if the target is not a note or names no such heading.",
			},
			"target_block": map[string]any{
				"type": "string",
				"description": "embed: show only the block anchored with this ID in the " +
					"embedded note (without the leading '^'). Names a block ON THE EMBED " +
					"TARGET, not on the note being written. Refused if the target is not a " +
					"note or names no such block anchor.",
			},
			"width": map[string]any{
				"type": "string",
				"description": "embed: display width for a picture, as digits ('400') or " +
					"digits×digits ('400x300'). Applies only when the target is a picture; " +
					"refused otherwise.",
			},
			"page": map[string]any{
				"type": "string",
				"description": "embed: reserved for a future PDF page-fragment embed. NOT " +
					"supported yet — refused if given, on every target kind.",
			},

			// relation (FR-045)
			"relation_op": map[string]any{
				"type": "string",
				"enum": knowledgeEditRelationOps,
				"description": "relation: which change to make. 'add' adds the targets and " +
					"leaves every existing one in place. 'remove' removes them and leaves the " +
					"rest in place. 'replace' DISCARDS every existing target and stores only " +
					"the ones you send — use it only when you mean to, and send an empty " +
					"'targets' list to clear the property entirely.",
			},
			"targets": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
				"description": "relation: the notes to link to, by name or path — the " +
					"'[[...]]' notation is written for you. Adding a target that is already " +
					"there, or removing one that is not, changes nothing and is not an error, " +
					"so you never need to read the list first to add or remove one.",
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
	// THE `relation` REFUSAL — checked ahead of both generic sweeps below so
	// the caller gets the migration in the error text rather than a generic
	// "does not read" line it cannot act on. Same shape and same reason as
	// knowledge_restructure's expect_version refusal (AC-X3): an argument
	// this tool deliberately no longer declares, whose senders are all
	// following instructions that were true until recently.
	//
	// op "link" USED to accept `relation` and splice the wikilink into that
	// frontmatter property instead of the body. It was add-only — there was
	// no unlink — which made it half of a verb set rather than a way in, and
	// two ways to write one property is one too many. op "relation"
	// (knowledge_edit_relation.go) is the whole set, and preserves the rest
	// of the list on every verb, so it replaces this outright.
	//
	// Presence, not emptiness, is what is refused: `relation: ""` is a
	// caller whose property name resolved to nothing, and running it as a
	// body wikilink would silently do something other than what it asked.
	//
	// Tool-wide rather than op-scoped because NO op reads `relation` now —
	// an agent that sends it to set_property or to op "relation" itself
	// (meaning `property`) has made the same mistake and needs the same
	// sentence.
	if _, sentRelation := args["relation"]; sentRelation {
		return t.deps.refuse(authorOp, target, nil,
			"knowledge_edit's op \"link\" no longer takes 'relation': it only ever added an "+
				"edge, never removed one, and a relation property now has exactly one way in. "+
				"To write a relation property, send op \"relation\" instead: same collection, "+
				"path and expect_version as this call, plus property: the relation property "+
				"name you put in 'relation', relation_op: \"add\", \"remove\" or \"replace\", "+
				"and targets: a list of note names or paths (the '[[...]]' notation is written "+
				"for you). \"add\" and \"remove\" leave the rest of the list untouched; "+
				"\"replace\" discards it on purpose. To insert a wikilink in the note's BODY "+
				"instead, re-send this op \"link\" call with 'relation' dropped")
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
	// EMB-100: a SECOND sweep, narrower than the one above. `ok` gates this
	// on a RECOGNISED op only — an empty/garbage op has no entry in
	// editOpArgs and must still fall through to refuseOp's own "unsupported
	// op"/redirect messaging below, not this generic one.
	if allowed, ok := editOpArgs[op]; ok {
		if foreign := unknownArgs(args, allowed); len(foreign) > 0 {
			return t.deps.refuse(authorOp, target, nil, fmt.Sprintf(
				"%s does not read %s; %s accepts: %s",
				op, strings.Join(foreign, ", "), op, strings.Join(allowed, ", ")))
		}
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
	case opEmbed:
		return t.execEmbed(ctx, target, args)
	case opRelation:
		return t.execRelation(ctx, target, args)
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
		// knowledgeEditRelationAllowed: this is a CREATE. FR-045's hazard —
		// replacing a relation list another writer contributed to — cannot
		// exist on a note that does not exist yet, and refusing here would
		// stop an agent authoring a record with its relations in one call.
		next, eerr := knowledgeEditSetPropertyEdit(set, report, p.Key, values, isList, knowledgeEditRelationAllowed, nil)(content)
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
			// knowledgeEditRelationRefused: set_property is the door FR-045
			// closes — a caller sending the value it wants the property to
			// end up holding takes a relation list's other edges with it.
			return knowledgeEditSetPropertyEdit(set, report, property, splitValues, splitIsList, knowledgeEditRelationRefused, &gov)(src)
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

	// op "link" writes a BODY wikilink, and nothing else. It once had a
	// second mode — `relation`, which spliced the link into a frontmatter
	// relation property instead — and that mode is gone: op "relation" is
	// the single way in for a relation property now, and Execute refuses
	// `relation` here by name before this function is reached.
	//
	// No schema is loaded and no governance is resolved, because a body
	// wikilink is not a property write: there is no declared property for a
	// schema to have an opinion about, which is why EditData.SchemaNote is
	// left empty below rather than reporting "nothing was checked".
	edit := AddWikilink(linkTarget,
		strings.TrimSpace(stringArg(args["alias"])),
		strings.TrimSpace(stringArg(args["section"])))

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
		Target: linkTarget, Changed: res.Changed,
		IndexWarning: indexWarning,
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
// embed (US-11, EMB-095..EMB-102) — "put the correct notation for me, after
// checking it will not be a broken embed forever."
//
// This is the one op in the file that reads a SECOND path (the embed
// target) without writing to it — every other op's containment story is
// entirely about `path`, the file being written. embed's target is read-
// only: its existence and, for a heading/view fragment, its own headings or
// views are CHECKED, never modified. The blast-radius rule in this file's
// header ("knowledge_edit writes ONLY the file named in `path`") still
// holds — the target is read, not written.
// ---------------------------------------------------------------------------

// embedPictureExts is the extension-only picture classifier this write path
// uses to decide whether 'width' applies (EMB-030). It deliberately does
// not attempt the full eleven-kind classification the reader uses — this
// tool only ever needs to distinguish "picture" (accepts width) from
// everything else, and an extension is all a note-editing tool has to go
// on (there is no server-sniffed media type available here, matching the
// spec's Conservative Type Design note on the reader's own classifier).
var embedPictureExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".svg": true, ".bmp": true, ".avif": true,
}

func isPictureTarget(rel string) bool {
	return embedPictureExts[strings.ToLower(path.Ext(rel))]
}

func isDataFileTarget(rel string) bool {
	return strings.EqualFold(path.Ext(rel), ".base")
}

// embedWidthPattern is EMB's own data constraint: "A size given after `|`
// in an embed MUST match ^\d+(x\d+)?$ to be read as a size; anything else
// is display text." Written here so a malformed width is refused at write
// time rather than silently becoming caption text the caller never asked
// for (the "must not accept an argument it does not act on" prohibition).
var embedWidthPattern = regexp.MustCompile(`^\d+(x\d+)?$`)

// embedBlockAnchorPattern is the reader's own block-anchor grammar, not a
// server-invented rule: src/components/library/preview/noteTransclusion.ts's
// BLOCK_ANCHOR_RE (`/(?:^|[ \t])\^([A-Za-z0-9-]+)[ \t]*$/`) only ever
// recognizes a `^id` token whose id is `[A-Za-z0-9-]+` — anything else can
// never resolve to a real block when the reader later scans the target
// note. Enforcing the same grammar at write time also closes the concrete
// failure a security review demonstrated: target_block was written VERBATIM
// into "^" + value with no escaping, so a value containing "]]" plus a
// newline could break out of the single fragment position inside
// composeEmbedNotation's "![[target#^value]]" and land arbitrary markdown
// lines in the note under the guise of one embed notation. This is a
// correctness/contract fix (the op's own promise is "the correct notation
// is written for you", and EMB's block-anchor data constraint), not a
// privilege-escalation fix — an agent holding knowledge_edit can already
// write arbitrary markdown via 'body' on create/append_section/replace_body.
var embedBlockAnchorPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// embedTargetHeadings reads an embed target's headings via the SAME
// bounded-memory scanner links.go's own extraction uses (ScanNote), rather
// than loading the whole file into memory — this validates a file the
// write never otherwise touches, and FR-034a's "the note is never held in
// memory" property applies to it exactly as it does to the note actually
// being written.
func embedTargetHeadings(fsys LinkFS, resolvedPath string) ([]Heading, error) {
	f, err := fsys.Open(resolvedPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	scan, err := ScanNote(f)
	if err != nil {
		return nil, err
	}
	return scan.Headings, nil
}

// embedBlockAnchorLinePattern is the reader's own DEFINITION grammar for a
// block anchor — src/components/library/preview/noteTransclusion.ts's
// BLOCK_ANCHOR_RE (`/(?:^|[ \t])\^([A-Za-z0-9-]+)[ \t]*$/`), matched per LINE
// exactly as sliceBlock matches it there. This is deliberately a SEPARATE
// pattern from embedBlockAnchorPattern above: that one validates the bare id
// the caller supplied (no "^", no surrounding line), this one recognizes the
// "^id" token a note's own line ends with when it DEFINES an anchor. The two
// must agree on what an id looks like ([A-Za-z0-9-]+) or a real anchor could
// be refused, or a fake one accepted — which is exactly the failure this
// check exists to close (EMB-098's write/read agreement, applied to blocks
// the way it was already applied to headings and views).
var embedBlockAnchorLinePattern = regexp.MustCompile(`(?:^|[ \t])\^([A-Za-z0-9-]+)[ \t]*$`)

// embedBlockAnchorListMax bounds how many of a note's block anchors a
// refusal enumerates. A large note could carry hundreds of anchors; the
// refusal states the bound rather than silently showing a short list with no
// indication anything was left out.
const embedBlockAnchorListMax = 20

// embedTargetBlockAnchors reads an embed target's block anchors — the
// distinct "^id" tokens found at the end of a line, in document order, the
// first occurrence kept when an id repeats (matching sliceBlock's own
// first-match behavior). ReadNoteContent, the same read EditNote uses for
// the note actually being written, rather than a bare Open+ReadAll — a
// cloud-evicted target reads as a clean, empty EOF either way, so this at
// least goes through the one helper that documents that failure mode
// (FR-111) instead of a second, undocumented copy of it.
func embedTargetBlockAnchors(fsys LinkFS, resolvedPath string) ([]string, error) {
	content, err := ReadNoteContent(fsys, resolvedPath)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var anchors []string
	for _, lineBytes := range bytes.Split(content, []byte("\n")) {
		line := strings.TrimSuffix(string(lineBytes), "\r")
		m := embedBlockAnchorLinePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := m[1]
		if seen[id] {
			continue
		}
		seen[id] = true
		anchors = append(anchors, id)
	}
	return anchors, nil
}

// embedBlockRefusalText is op=embed's block-anchor analogue of
// readSectionRefusalText: "embed: no block "nope" in Plan.md; block anchors:
// ^q3, ^q4" — echoing what DOES exist so an agent that gets this refused can
// fix itself, the same contract the heading and view refusals already keep.
// requested is the raw argument as given (with or without its own leading
// "^"), never normalized, so the refusal names exactly what the caller typed.
func embedBlockRefusalText(embedPath, requested string, anchors []string) string {
	if len(anchors) == 0 {
		return fmt.Sprintf("embed: no block %q in %s; this note has no block anchors", requested, embedPath)
	}
	shown := anchors
	var suffix string
	if len(shown) > embedBlockAnchorListMax {
		shown = shown[:embedBlockAnchorListMax]
		suffix = fmt.Sprintf(", and %d more", len(anchors)-embedBlockAnchorListMax)
	}
	names := make([]string, len(shown))
	for i, a := range shown {
		names[i] = "^" + a
	}
	return fmt.Sprintf("embed: no block %q in %s; block anchors: %s%s",
		requested, embedPath, strings.Join(names, ", "), suffix)
}

// embedInsertEdit returns an edit that inserts one embed notation line
// under the named section, creating the section if absent (EMB-097) — the
// write half of US-11, sharing insertUnderSection with AddWikilink rather
// than a second insertion mechanism.
func embedInsertEdit(notation, section string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		return insertUnderSection(src, notation, section, 2)
	}
}

// composeEmbedNotation writes the exact syntax an embed of target,
// optionally fragmented by view/heading/block and optionally sized, is
// spelled as — "the link syntax is written for you", carried over from the
// retired knowledge_link's own principle. fragment is empty when the
// caller named no view/target_heading/target_block.
func composeEmbedNotation(target, fragment, width string) string {
	s := "![[" + target
	if fragment != "" {
		s += "#" + fragment
	}
	if width != "" {
		s += "|" + width
	}
	return s + "]]"
}

func (t *EditTool) execEmbed(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpEdit, target, nil, err.Error())
	}

	embedTarget := strings.TrimSpace(stringArg(args["target"]))
	if embedTarget == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'target' is required")
	}
	embedRel, cErr := cleanNoteArg(embedTarget)
	if cErr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: the target %q is not inside this collection", embedTarget))
	}

	section := strings.TrimSpace(stringArg(args["section"]))
	if section == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'section' is required")
	}

	view := strings.TrimSpace(stringArg(args["view"]))
	targetHeading := strings.TrimSpace(stringArg(args["target_heading"]))
	targetBlock := strings.TrimSpace(stringArg(args["target_block"]))
	width := strings.TrimSpace(stringArg(args["width"]))
	page := strings.TrimSpace(stringArg(args["page"]))

	// 'page' (a PDF page fragment, US-12/EMB-105) is a DEFERRED kind: no
	// renderer or notation form exists for it yet. Refusing beats writing
	// notation nothing can check and a reader would treat as a plain,
	// unfragmented embed — silently different from what was asked for.
	if page != "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			"embed: 'page' (a PDF page fragment) is not supported yet")
	}
	fragmentArgs := 0
	for _, v := range []string{view, targetHeading, targetBlock} {
		if v != "" {
			fragmentArgs++
		}
	}
	if fragmentArgs > 1 {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			"embed: give at most one of 'view', 'target_heading', 'target_block'")
	}

	// Contained AND EXISTS (EMB-098). A broken embed found later by a
	// reader — the failure US-11 exists to prevent — is exactly what
	// skipping this and only checking the STRING would produce.
	root, rErr := NewCollectionRoot(OSLinkFS(), target.collection.Root())
	if rErr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, fmt.Sprintf("embed: %v", rErr))
	}
	resolved, pErr := root.ResolveContainedNoSymlink(OSLinkFS(), embedRel)
	if pErr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: the target %q is not inside this collection", embedTarget))
	}
	info, statErr := OSLinkFS().Lstat(resolved)
	if statErr != nil || info.IsDir() {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: %q does not exist in this knowledge base", embedTarget))
	}

	isPicture := isPictureTarget(embedRel)
	isDataFile := isDataFileTarget(embedRel)
	isNote := IsMarkdownPath(embedRel)

	if width != "" && !isPicture {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: 'width' only applies to a picture; %q is not one", embedTarget))
	}
	if width != "" && !embedWidthPattern.MatchString(width) {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: 'width' must look like '400' or '400x300', got %q", width))
	}
	if view != "" && !isDataFile {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: 'view' only applies to a data file (.base); %q is not one", embedTarget))
	}
	if targetHeading != "" && !isNote {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: 'target_heading' only applies to a note; %q is not one", embedTarget))
	}
	if targetBlock != "" && !isNote {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: 'target_block' only applies to a note; %q is not one", embedTarget))
	}
	targetBlockID := strings.TrimPrefix(targetBlock, "^")
	if targetBlock != "" && !embedBlockAnchorPattern.MatchString(targetBlockID) {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("embed: 'target_block' must be a block anchor like '^abc123' (letters, digits, and hyphens only, with an optional leading '^'), got %q", targetBlock))
	}

	fragment := ""
	switch {
	case view != "":
		set, _, lerr := t.loadSchemas(target)
		if lerr != nil {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel}, fmt.Sprintf("embed: %v", lerr))
		}
		views, _, verr := records.LoadViews(target.collection.Root(), set)
		if verr != nil {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				fmt.Sprintf("embed: loading this collection's saved views: %v", verr))
		}
		var labels []string
		var matched *records.SavedView
		for _, v := range views.Views() {
			if v.Def.Source == nil || normalizeRel(strings.TrimSpace(*v.Def.Source)) != normalizeRel(embedRel) {
				continue
			}
			labels = append(labels, v.DisplayLabel())
			if matched == nil && v.DisplayLabel() == view {
				matched = v
			}
		}
		if matched == nil {
			listing := "no views are defined for it"
			if len(labels) > 0 {
				listing = "views: " + strings.Join(labels, ", ")
			}
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				fmt.Sprintf("embed: no view %q in %s; %s", view, embedTarget, listing))
		}
		fragment = matched.DisplayLabel()
	case targetHeading != "":
		headings, hErr := embedTargetHeadings(OSLinkFS(), resolved)
		if hErr != nil {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				fmt.Sprintf("embed: reading %q: %v", embedTarget, hErr))
		}
		want := matchSectionQuery(targetHeading)
		found := false
		for _, h := range headings {
			if h.Text == want {
				found = true
				break
			}
		}
		if !found {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				readSectionRefusalText(embedTarget, targetHeading, headings))
		}
		fragment = want
	case targetBlock != "":
		anchors, aErr := embedTargetBlockAnchors(OSLinkFS(), resolved)
		if aErr != nil {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				fmt.Sprintf("embed: reading %q: %v", embedTarget, aErr))
		}
		found := false
		for _, a := range anchors {
			if a == targetBlockID {
				found = true
				break
			}
		}
		if !found {
			return t.deps.refuse(AuthorOpEdit, target, []string{rel},
				embedBlockRefusalText(embedTarget, targetBlock, anchors))
		}
		fragment = "^" + targetBlockID
	}

	notation := composeEmbedNotation(embedRel, fragment, width)

	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	res, weErr := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath: rel, Edits: []NoteEdit{embedInsertEdit(notation, section)}, ExpectVersion: expect,
		Now: t.deps.now(), Audit: t.deps.Audit, Actor: target.actor(), Lock: target.lock,
	})
	if weErr != nil {
		return knowledgeEditFailure(AuthorOpEdit, weErr)
	}
	var indexWarning string
	if res.Changed {
		indexWarning = refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	}
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opEmbed, Path: res.RelPath, Version: res.Version,
		Target: embedRel, Notation: notation, Section: section, Changed: res.Changed,
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
	// RelationOp is op=relation's verb — "add", "remove" or "replace"
	// (FR-045). A DELIBERATELY separate field from ListOp (set_property's
	// own add/remove sub-verb) even though both render as a verb word: the
	// two accept different verb sets, and collapsing them would let a
	// "replace" render through a field whose own documented values never
	// include one.
	RelationOp string
	// Targets is op=relation's targets, as the caller named them — the bare
	// note names, not the "[[...]]" wikilinks actually written. The reply
	// echoes what was ASKED FOR so a caller can see its own input reflected
	// back; the stored notation is an implementation detail it did not send.
	Targets []string
	// Notation is the exact embed markdown text op=embed composed and
	// wrote (US-11) — e.g. "![[Tasks.base#Needs Daniel]]". Empty for every
	// other op.
	Notation string
	// Section is op=embed's destination heading — the section the
	// notation was inserted under. A DELIBERATELY separate field from
	// Heading (append_section's own heading argument), even though the two
	// render similarly: the two ops read differently-named arguments for
	// it (EMB-101's target_heading/target_block vs heading are the same
	// distinction one level down, on the embed TARGET rather than the
	// destination).
	Section string
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
		// One shape, because op "link" now has one mode. The second form
		// ("LINK <relation> -> <target>") rendered the frontmatter-property
		// mode, which op "relation" replaced outright — see Execute's
		// `relation` refusal.
		fmt.Fprintf(&b, "LINK -> %s (%s)\n", d.Target, changedWord(d.Changed))
	case opReplaceBody:
		fmt.Fprintf(&b, "REPLACE_BODY (%s)\n", changedWord(d.Changed))
	case opEmbed:
		fmt.Fprintf(&b, "EMBED %s under %q (%s)\n", d.Notation, d.Section, changedWord(d.Changed))
	case opRelation:
		// The verb is rendered in the caller's own vocabulary ("RELATION
		// ADD") rather than translated to a past-tense English word, so the
		// reply names the argument to send again rather than one to
		// translate back.
		targets := strings.Join(d.Targets, ", ")
		if targets == "" {
			targets = "(none — cleared)"
		}
		fmt.Fprintf(&b, "RELATION %s %s -> %s (%s)\n",
			strings.ToUpper(d.RelationOp), d.Property, targets, changedWord(d.Changed))
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
