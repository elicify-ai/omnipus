// Omnipus — ADR-068 D15.6 / spec §4.1.6: knowledge_configure, the CONTROL
// PLANE. THE LOGIC HALF AND THE TOOL ADAPTER LIVE TOGETHER HERE, matching
// knowledge_edit.go's own reasoning (not knowledge_describe.go/knowledge_read.go's
// split): this file already needs AuthoringDeps for workspace scope, the
// lifecycle gate and FR-090 audit coverage, so there is no pkg/tools boundary
// left to preserve by splitting further.
//
// # Why this tool exists, and why it is not an operation of knowledge_edit
//
// Declaring a property, adding an enum value, or defining a record type or a
// saved view can retroactively make EXISTING notes valid or invalid, or
// change what a saved query returns. That is C-B (ADR-068 D15.1) — it changes
// what already-existing files MEAN, without writing them — and it is the
// opposite shape to knowledge_edit (which never reinterprets a file it did
// not write) and to knowledge_restructure (which writes many files and
// reinterprets none). Policy resolves on the tool NAME alone (FR-070c,
// Constraint #6), so a control plane folded into knowledge_edit makes the
// posture "this agent may edit notes freely, but may not redefine what a
// note is" inexpressible — see spec "Schema and view authoring" (ADR-068
// D23, D15.6).
//
// # What this file OWNS, and what it deliberately REUSES rather than
// reimplements
//
// This file adds exactly the four things every mutation in this package adds
// on top of the primitives it composes (see authoring_tools.go's own header):
// workspace scope, the lifecycle gate, audit coverage of refusals the lower
// layers never see, and argument shape. The validation and persistence
// PRIMITIVES are not reimplemented:
//
//	records.ParseSchema                the schema_version / closed-key-set /
//	                                    property-type / enum validation this
//	                                    tool's own refusal table is built on
//	records.ParseView                  the same discipline for a saved view
//	records.ValidateViewAgainstSchemas EXPORTED specifically for this tool
//	                                    (see its own doc comment)
//	records.LoadSchemas / LoadViews    reloaded after a write to compute the
//	                                    cascade against the schema as WRITTEN,
//	                                    never against an in-memory copy that
//	                                    could disagree with the file
//	records.Validate                   per-record validation, run before and
//	                                    after an edit to report the cascade
//	WithNoteWriteLock                  the SAME tier-1 lock every write in
//	                                    this package takes (D14) — reused here
//	                                    with a synthetic key naming the schema
//	                                    or view file, because a control-plane
//	                                    file is not a note but needs the
//	                                    identical guarantee: two callers
//	                                    racing on ONE file must not both be
//	                                    told they succeeded (FR-043a)
//
// Building the schema/view file's bytes by marshalling the agent's
// `definition` argument straight through records.ParseSchema / records.ParseView
// (rather than hand-rolling a second validator) is the load-bearing design
// choice in this file: it is the only way the write path and the read path
// (knowledge_describe, the properties index) can never disagree about what a
// valid schema or view is.
//
// # The one place this tool is allowed to write, and why nothing else gains it
//
// knowledge_edit's own header states the rule this tool is the exception to:
// "An op whose nature is ... the schema/view control plane — is refused BY
// NAME ... never attempted here under any argument spelling." This file is
// the ONE place in the package that computes a path under
// records.SchemaDir/records.ViewsDir and hands it to an os.OpenFile or
// fileutil.WriteFileAtomic call — every other mutating path in this package
// (author.go's CreateNote/EditNote) refuses ErrReservedLocation before it
// gets that far. Nothing in this file relaxes that guard for anyone else: it
// simply never asks author.go to write a note at all, because a schema or a
// view is not a note.
//
// # expect_version does not exist here (FR-018a, AC-C3)
//
// A single-file content hash cannot honestly guard a change whose blast
// radius is every note declaring the type. Safety here is policy, the audit
// entry (FR-077), and check_integrity (FR-075) — never a token this tool
// cannot honour. The tier-1 lock above guards the ONE FILE (FR-043a); it
// deliberately does not and cannot guard the cascade.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Operation names knowledge_configure's `op` argument accepts — the closed
// set spec §4.1.6's table declares.
const (
	opCreateRecordType = "create_record_type"
	opEditRecordType   = "edit_record_type"
	opDeleteRecordType = "delete_record_type"
	opWriteView        = "write_view"
	opDeleteView       = "delete_view"
	// opCreateView is the composer path (view-kinds-design-2026-09-03 §6.1):
	// eight named kinds plus a handful of property bindings, in place of a
	// raw `definition`. write_view (the definition-shaped escape hatch) and
	// delete_view are unchanged by its addition.
	opCreateView = "create_view"
)

// vaultConfigureOps lists the accepted ops, in the order spec §4.1.6
// documents them, with create_view placed next to write_view (both author a
// saved view; create_view is the composer, write_view the raw escape hatch).
var vaultConfigureOps = []string{
	opCreateRecordType, opEditRecordType, opDeleteRecordType, opWriteView, opCreateView, opDeleteView,
}

// vaultConfigureNoteOps are knowledge_edit's ops (write ONE named note,
// never reinterpret an existing one). Sending one of these here is refused
// by name — spec §4.1.6's "A one-file note edit sent here" row — never
// attempted under this tool's argument shape.
var vaultConfigureNoteOps = map[string]bool{
	"create": true, "set_property": true, "append_section": true,
	"link": true, "replace_body": true,
}

// vaultConfigureCascadeOps are knowledge_restructure's ops (write bytes into
// notes the caller did not name). Spec §4.1.6's "A cascading-in-bytes op
// sent here (C-A)" row.
var vaultConfigureCascadeOps = map[string]bool{
	"rename": true, "move": true, "trash": true, "restore": true,
}

// authorOpConfigure is this tool's audit operation. AC-C5 requires every
// call — applied or refused — to emit a "knowledge.configure" audit entry naming
// the operation, agent, workspace, target and outcome; the literal event
// name is spelled out in the acceptance criterion itself.
// knowledge.configure, not knowledge.note.configure: the sibling operations
// (knowledge.note.create/.edit/.rename/.trash/.restore) all act on a NOTE, and
// this one does not — it writes schema and view files under .omnipus-vault/.
// Renamed from "vault.configure" while the tool is still unregistered: this
// string lands in audit records, so changing it after the tool ships would
// split one operation across two names in the audit history.
const authorOpConfigure AuthorOperation = "knowledge.configure"

// configureCommonArgNames are the two arguments every op takes: which change
// to make, and where.
var configureCommonArgNames = []string{"op", "collection"}

// configureOpArgNames is each op's OWN accepted argument set, on top of
// configureCommonArgNames.
//
// WHY PER-OP AND NOT ONE UNION. This tool's unknown-argument sweep used to
// run once, in Execute, against a single flat list — which was correct while
// every argument in that list was read by at least one of five ops that
// mostly shared them. op=create_view then added ELEVEN flat bindings (kind,
// number, unit, date, image, choice, group_by, columns, sort, limit, filter)
// to the same union, and that quietly made all eleven acceptable on
// write_view, where nothing reads them. `write_view` with `kind: "board"`
// returned "view saved" and dropped the kind without a word — the exact
// silent-drop failure the sweep exists to prevent, arriving through the
// sweep's own accepted list.
//
// So the sweep now resolves the op first and checks against THAT op's set.
// create_view takes `type` (the record type it composes against) and its
// eleven bindings; write_view takes `definition` and nothing else, because
// everything write_view honours lives inside that object.
var configureOpArgNames = map[string][]string{
	opCreateRecordType: {"type", "definition"},
	opEditRecordType:   {"type", "definition"},
	opDeleteRecordType: {"type"},
	opWriteView:        {"view", "definition", "source"},
	opCreateView:       append([]string{"view", "type"}, createViewArgNames...),
	opDeleteView:       {"view"},
}

// configureArgNames is every argument Parameters() declares — the UNION of
// the sets above, DERIVED from them rather than restated, so an argument an
// op accepts is necessarily advertised and an advertised argument is
// necessarily accepted somewhere. expect_version is DELIBERATELY absent
// (FR-018a, AC-C3) — its absence from this list is what makes it absent from
// the tool schema, which is what the acceptance criterion actually tests.
var configureArgNames = buildConfigureArgNames()

func buildConfigureArgNames() []string {
	out := append([]string(nil), configureCommonArgNames...)
	seen := map[string]bool{}
	for _, n := range out {
		seen[n] = true
	}
	// vaultConfigureOps, not a range over the map: a map range is unordered,
	// and this list is rendered into refusal text.
	for _, op := range vaultConfigureOps {
		for _, n := range configureOpArgNames[op] {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

// configureAcceptedArgs is the full accepted set for one op — the common
// arguments plus its own.
func configureAcceptedArgs(op string) []string {
	own := configureOpArgNames[op]
	out := make([]string, 0, len(configureCommonArgNames)+len(own))
	out = append(out, configureCommonArgNames...)
	out = append(out, own...)
	return out
}

// ---------------------------------------------------------------------------
// THE DESCRIPTION IS DERIVED, NEVER TRANSCRIBED
//
// A tool description is the only thing the model reads. A capability the
// description omits does not exist in practice however well it is
// implemented, and a name the description invents fails on every call that
// follows it. Both halves have already gone wrong here: this parameter's
// description asserted "the seven property types are … there is no eighth"
// while records.PropertyTypes had held EIGHT since Draft 11, which made
// `checkbox` — fully implemented, and accepted by this tool's own write path —
// unreachable through the only agent-facing schema-authoring tool there is.
//
// So the two closed sets this description states are BUILT FROM THE SAME
// VARIABLES THE VALIDATION READS, at init, rather than copied into prose that
// nobody re-checks when the set grows:
//
//	records.PropertyTypes   what records.ParseSchema accepts as a property type
//	records.OperatorNames() what a filter leaf's `op` may spell
//
// A hand-copied list is what created the defect; a derived one cannot drift.
// TestConfigureDescription_PropertyTypesAreDerivedNotTranscribed and
// TestConfigureDescription_OperatorsAreDerivedNotTranscribed hold that as
// behaviour rather than as this paragraph.
// ---------------------------------------------------------------------------

// configurePropertyTypeSentence names every declared property type, derived
// from records.PropertyTypes. The COUNT is rendered from len() for the same
// reason the names are: "seven" was the half of the old sentence that was
// wrong, and a literal count is a second thing to forget.
var configurePropertyTypeSentence = buildPropertyTypeSentence()

// configureOperatorSentence names every operator a filter leaf's `op` may
// spell, derived from records.OperatorNames(). Naming them rather than
// counting them is the point: "the ten SQL operators" tells a model a number
// it cannot act on, and a guessed spelling (`contains`, `is_null`) is refused.
var configureOperatorSentence = buildOperatorSentence()

func buildPropertyTypeSentence() string {
	names := make([]string, 0, len(records.PropertyTypes))
	for _, t := range records.PropertyTypes {
		names = append(names, string(t))
	}
	return fmt.Sprintf("The %d property types are %s.", len(names), strings.Join(names, ", "))
}

func buildOperatorSentence() string {
	return "`op` is one of " + strings.Join(records.OperatorNames(), ", ") + ";"
}

// cascadeExampleCap bounds how many per-record findings a cascade report
// names. The MATCHED/CLEAN/NEWLY-REPORTED counts are always exact; this only
// caps the example lines under them, on the same reasoning check_integrity's
// own per-category cap uses — an operator needs the number and a sample to
// act on, not a report so long it cannot be read.
const cascadeExampleCap = 10

// ConfigureTool is knowledge_configure.
type ConfigureTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// NewConfigureTool builds knowledge_configure over the same AuthoringDeps
// every mutation in this package shares.
func NewConfigureTool(deps AuthoringDeps) *ConfigureTool { return &ConfigureTool{deps: deps} }

// Name is the registered tool name (ADR-068 D15.6).
func (t *ConfigureTool) Name() string { return "knowledge_configure" }

// Description names the WIDEST operation (AC-C6, FR-079, FR-070c):
// delete_record_type, which reverts every record of a type at once — not
// create_record_type, which is the most commonly reached-for.
func (t *ConfigureTool) Description() string {
	return "Manage a knowledge base's OWN record types and saved views — the control plane " +
		"(ADR-068 D15.6). Every operation here changes what EXISTING notes MEAN, not their " +
		"bytes: deleting a record type (delete_record_type) reverts every record of that type " +
		"to an ordinary note in one call, and declaring or changing a type " +
		"(create_record_type / edit_record_type) can validate or revalidate every pre-existing " +
		"note that already carries that type in its frontmatter. create_view / write_view / " +
		"delete_view author a saved query — no note's bytes or validity changes, only what a " +
		"query returns. " + ConfigureCreateViewDescriptionFragment + " Its own render stack " +
		"includes the rule that a number's total is computed per unit and never combined " +
		"across units when the property declares one. Never touches a note's own content or path; use " +
		"knowledge_edit or knowledge_restructure for those. There is no built-in vocabulary: " +
		"this knowledge base's record types, properties and enum values are entirely what this tool " +
		"(or the operator) has declared."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *ConfigureTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *ConfigureTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in. One flattened object
// covers every op, matching knowledge_edit's own pattern.
func (t *ConfigureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"enum":        vaultConfigureOps,
				"description": "Which control-plane change to make.",
			},
			"collection": collectionParam(),
			"type": map[string]any{
				"type": "string",
				"description": "create_record_type / edit_record_type / delete_record_type: " +
					"the record type name (the value a note's own `type:` frontmatter key holds).",
			},
			"view": map[string]any{
				"type":        "string",
				"description": "create_view / write_view / delete_view: the view name.",
			},
			"source": map[string]any{
				"type": "string",
				"description": "create_view / write_view, optional: the .base data file this " +
					"view belongs to, as a path relative to the collection root (e.g. " +
					"'Projects.base'). Views reach the Library's base preview and the " +
					"'![[Projects.base#View]]' embed ONLY through this; a view without a " +
					"source answers knowledge_find but has no place in the UI. A starter " +
					".base file is written for you if none exists at that path.",
			},
			"kind": map[string]any{
				"type": "string",
				// Both DERIVED from view_kinds.go's rulebook, never
				// transcribed: the enum is its ViewKindOrder, and the prose is
				// built from its per-kind requirement phrases — see
				// createViewKindParamDescription for what went wrong when this
				// sentence was written by hand.
				"enum":        ViewKindOrder,
				"description": createViewKindParamDescription,
			},
			"filter": map[string]any{
				"type": "object",
				"description": "create_view only, optional. Same shape as write_view's " +
					"definition.filter: one tree of {all|any|not} over leaves of {property, op, value}.",
			},
			"number": map[string]any{
				"type": "string",
				"description": "create_view only. Required for kind=summary/trend/breakdown: the " +
					"integer or decimal property to total. A text property is refused even when its " +
					"values look numeric — declare it as a number via edit_record_type first. If the " +
					"property declares a companion unit (unit_property), the total is computed once " +
					"per unit value and never combined across units; that pairing is applied " +
					"automatically unless 'unit' is also given.",
			},
			"unit": map[string]any{
				"type": "string",
				"description": "create_view only, optional. Names the companion unit property of " +
					"'number'. Leave unset to let the schema's own declared pairing apply " +
					"automatically. Setting it to anything other than that declared pairing (including " +
					"an empty string) is refused — a number that totals per unit never draws one " +
					"combined figure across units.",
			},
			"date": map[string]any{
				"type":        "string",
				"description": "create_view only. Required for kind=calendar/trend: the date property.",
			},
			"image": map[string]any{
				"type": "string",
				"description": fmt.Sprintf(
					"create_view only. Accepted for kind=tiles, but never satisfies it: %s (D5) — "+
						"kind=tiles is refused unconditionally regardless of this argument.",
					imageIneligibleReason),
			},
			"choice": map[string]any{
				"type": "string",
				"description": fmt.Sprintf(
					"create_view only. Required for kind=board: the enum property (at most %d declared "+
						"values) that becomes the board's columns.", maxBoardEnumValues),
			},
			"group_by": map[string]any{
				"description": "create_view only. A property name, or (kind=breakdown only) a list of " +
					"exactly two DIFFERENT property names. Optional for summary/trend (at most one); " +
					"required and exactly two for breakdown; refused for every other kind.",
			},
			"columns": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "create_view only, optional. Properties to display, in this order. Omitted shows every declared property.",
			},
			"sort": map[string]any{
				"type":        "array",
				"description": "create_view only, optional. Same shape as write_view's definition.sort.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "create_view only, optional. Same as write_view's definition.limit.",
			},
			"definition": map[string]any{
				"type": "object",
				"description": "NOT for op=create_view — create_view takes flat arguments " +
					"(kind, type, number, unit, date, image, choice, group_by, columns, sort, " +
					"limit, filter) and refuses `definition` by name; send only the arguments " +
					"your kind needs, never empty placeholders (UAT D5: a first call carrying " +
					"every schema key wastes a round-trip). " +
					"create_record_type / edit_record_type: the full schema — " +
					"schema_version (required), type, optional label, optional identity " +
					"{prefix}, and properties (a map of property name to {type, many, " +
					"required, and per-type: label, values, to, inverse, unit}). " +
					configurePropertyTypeSentence + " There is no built-in vocabulary; every " +
					"type name, property name and enum value is this knowledge base's own. " +
					"write_view: the raw escape hatch for the LEGACY view shape. " +
					ConfigureWriteViewSteerLine + " It does NOT accept `kind` or `parts` — " +
					"those are op=create_view's vocabulary and are refused here (design D6), " +
					"because the eligibility rules that give them meaning run only in the " +
					"composer. An OPTIONAL type (the record type " +
					"queried; omit it for a view that spans every note in scope), one optional " +
					"`filter` tree of {all|any|not} over leaves of {property, op, value} where " +
					configureOperatorSentence + " and optional label, grouping ({property, " +
					"direction}), sort, properties, aggregates, limit, layout, formulas and " +
					"property_config. `formulas` and a DESCENDING grouping are stored " +
					"faithfully but knowledge_find cannot serve a view declaring either; the " +
					"write response says so rather than leaving you to find out at query " +
					"time. A view carries NO version key. `name` is taken from `view`, not " +
					"from this object.",
			},
		},
		"required": []string{"op"},
	}
}

// Execute dispatches by op.
func (t *ConfigureTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	target, refusal := t.deps.begin(ctx, authorOpConfigure, args)
	if refusal != nil {
		return refusal
	}

	// FR-018a / AC-C3: a caller that supplies expect_version is told exactly
	// why, in words it can act on, rather than folded into the generic
	// unknown-argument refusal below (which would be technically true but
	// would not explain that the parameter can never exist here).
	if _, has := args["expect_version"]; has {
		return t.deps.refuse(authorOpConfigure, target, nil,
			"knowledge_configure takes no expect_version: a single-file token cannot guard a "+
				"change to every note declaring this type. Re-read with knowledge_describe and re-send")
	}
	op := strings.TrimSpace(stringArg(args["op"]))
	if op == "" {
		return t.deps.refuse(authorOpConfigure, target, nil,
			"'op' is required; one of "+strings.Join(vaultConfigureOps, ", "))
	}
	if vaultConfigureNoteOps[op] {
		return t.deps.refuse(authorOpConfigure, target, nil, op+" writes one note; use knowledge_edit")
	}
	if vaultConfigureCascadeOps[op] {
		return t.deps.refuse(authorOpConfigure, target, nil, op+" writes notes you did not name; use knowledge_restructure")
	}
	// The unknown-argument sweep runs AFTER the op is resolved, because what
	// counts as unknown is a property of the op — see configureOpArgNames.
	// An unrecognised op is refused first: "op=frobnicate accepts …" would be
	// a nonsense sentence, and the op is the thing actually wrong.
	if _, known := configureOpArgNames[op]; !known {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"unsupported op %q; supported ops are %s", op, strings.Join(vaultConfigureOps, ", ")))
	}
	accepted := configureAcceptedArgs(op)
	if unknown := unknownArgs(args, accepted); len(unknown) > 0 {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"unknown argument(s) %s; op=%s accepts: %s",
			strings.Join(unknown, ", "), op, strings.Join(accepted, ", ")))
	}

	switch op {
	case opCreateRecordType:
		return t.execCreateRecordType(target, args)
	case opEditRecordType:
		return t.execEditRecordType(target, args)
	case opDeleteRecordType:
		return t.execDeleteRecordType(target, args)
	case opWriteView:
		return t.execWriteView(target, args)
	case opCreateView:
		return t.execCreateView(target, args)
	case opDeleteView:
		return t.execDeleteView(target, args)
	default:
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"unsupported op %q; supported ops are %s", op, strings.Join(vaultConfigureOps, ", ")))
	}
}

// ---------------------------------------------------------------------------
// create_record_type / edit_record_type
// ---------------------------------------------------------------------------

func (t *ConfigureTool) execCreateRecordType(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	typeName := strings.TrimSpace(stringArg(args["type"]))
	if typeName == "" {
		// UAT 2026-09-13 D-47: a `type` inside the definition is the same
		// declaration; it need not be repeated at the top level.
		if defMap, ok := args["definition"].(map[string]any); ok {
			typeName = strings.TrimSpace(stringArg(defMap["type"]))
		}
	}
	if typeName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'type' is required for create_record_type (at the top level, or as definition.type)")
	}
	// The name becomes a filename under records.SchemaDir — see
	// controlPlaneNameRefusal for why that is checked before anything else.
	if nrefusal := controlPlaneNameRefusal("type", typeName, records.SchemaDir(root)); nrefusal != "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: "+nrefusal)
	}

	defMap, derr := definitionMap(args["definition"])
	if derr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: "+derr.Error())
	}
	if merr := mergeDeclaredType(defMap, typeName); merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: "+merr.Error())
	}

	existing, _, lerr := records.LoadSchemas(root)
	if lerr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: loading existing record schemas: "+lerr.Error())
	}
	if sc, ok := existing.Get(typeName); ok {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"record type %q is already declared in %s; use op=edit_record_type to change it",
			typeName, sc.SourcePath))
	}

	yamlBytes, merr := marshalDefinition(defMap)
	if merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: "+merr.Error())
	}
	schemaPath := filepath.Join(records.SchemaDir(root), typeName+controlPlaneFileExt)
	if _, rej := records.ParseSchema(schemaPath, yamlBytes); rej != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: "+rej.Reason)
	}

	matches, serr := recordsOfType(root, typeName)
	if serr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "create_record_type: "+serr.Error())
	}

	if werr := createControlPlaneFile(target, schemaPath, yamlBytes); werr != nil {
		if errors.Is(werr, fs.ErrExist) {
			return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, schemaPath)}, fmt.Sprintf(
				"record type %q is already declared; use op=edit_record_type to change it", typeName))
		}
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, schemaPath)}, "create_record_type: "+werr.Error())
	}
	bumpIndexEpochOrWarn("create_record_type", t.deps.Home, root)

	newSet, _, rerr := records.LoadSchemas(root)
	if rerr != nil {
		// The file is written; the reload that computes the cascade report
		// failed. Applied, not refused — the write happened — but the
		// operator must be told the cascade could not be computed rather
		// than shown a silently wrong "0 affected".
		t.deps.record(AuthorAuditRecord{
			Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
			AgentID: target.agentID, WorkspaceID: target.workspaceID,
			Collection: target.col.Name, Root: root,
			Paths: []string{relControlPlanePath(root, schemaPath)}, At: t.deps.now(),
		})
		return tools.NewToolResult(fmt.Sprintf(
			"record type %q created at %s, but the cascade could not be computed: %v",
			typeName, relControlPlanePath(root, schemaPath), rerr))
	}

	cascade := computeCreateCascade(newSet, matches)
	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: []string{relControlPlanePath(root, schemaPath)}, At: t.deps.now(),
	})
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		Op: opCreateRecordType, Name: typeName,
		Path:    relControlPlanePath(root, schemaPath),
		Cascade: &cascade,
	}))
}

func (t *ConfigureTool) execEditRecordType(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	typeName := strings.TrimSpace(stringArg(args["type"]))
	if typeName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'type' is required for edit_record_type")
	}

	oldSet, _, lerr := records.LoadSchemas(root)
	if lerr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "edit_record_type: loading existing record schemas: "+lerr.Error())
	}
	oldSchema, ok := oldSet.Get(typeName)
	if !ok {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"no record type %q is declared; declared types: %s", typeName, joinOrNone(oldSet.Types())))
	}

	defMap, derr := definitionMap(args["definition"])
	if derr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "edit_record_type: "+derr.Error())
	}
	if merr := mergeDeclaredType(defMap, typeName); merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "edit_record_type: "+merr.Error())
	}

	yamlBytes, merr := marshalDefinition(defMap)
	if merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "edit_record_type: "+merr.Error())
	}
	schemaPath := oldSchema.SourcePath
	if _, rej := records.ParseSchema(schemaPath, yamlBytes); rej != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "edit_record_type: "+rej.Reason)
	}

	matches, serr := recordsOfType(root, typeName)
	if serr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "edit_record_type: "+serr.Error())
	}
	oldReport := records.Validate(oldSet, matches, records.ValidateOptions{})

	if werr := overwriteControlPlaneFile(target, schemaPath, yamlBytes); werr != nil {
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, schemaPath)}, "edit_record_type: "+werr.Error())
	}
	bumpIndexEpochOrWarn("edit_record_type", t.deps.Home, root)

	newSet, _, rerr := records.LoadSchemas(root)
	if rerr != nil {
		t.deps.record(AuthorAuditRecord{
			Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
			AgentID: target.agentID, WorkspaceID: target.workspaceID,
			Collection: target.col.Name, Root: root,
			Paths: []string{relControlPlanePath(root, schemaPath)}, At: t.deps.now(),
		})
		return tools.NewToolResult(fmt.Sprintf(
			"record type %q edited at %s, but the cascade could not be computed: %v",
			typeName, relControlPlanePath(root, schemaPath), rerr))
	}
	newReport := records.Validate(newSet, matches, records.ValidateOptions{})
	cascade := computeEditCascade(oldReport, newReport)
	// UAT 2026-09-13 D-25: "8 validate clean" was true under the validator's
	// rules and untrue in every way that mattered — after `priority` was
	// renamed to `prio`, 8 of 8 notes still carried `priority:` (now ignored
	// by every reader) and none carried `prio:`, and two saved views naming
	// `priority` silently stopped loading. Both are counted here.
	if newSchema, ok := newSet.Get(typeName); ok {
		cascade.StaleKeys = staleKeysAcrossRecords(newSchema, matches)
	}
	cascade.ViewsBroken = viewsNewlyRejected(root, oldSet, newSet)

	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: []string{relControlPlanePath(root, schemaPath)}, At: t.deps.now(),
	})
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		Op: opEditRecordType, Name: typeName,
		Path:    relControlPlanePath(root, schemaPath),
		Cascade: &cascade,
	}))
}

func (t *ConfigureTool) execDeleteRecordType(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	typeName := strings.TrimSpace(stringArg(args["type"]))
	if typeName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'type' is required for delete_record_type")
	}

	set, _, lerr := records.LoadSchemas(root)
	if lerr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "delete_record_type: loading existing record schemas: "+lerr.Error())
	}
	sc, ok := set.Get(typeName)
	if !ok {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"no record type %q is declared; declared types: %s", typeName, joinOrNone(set.Types())))
	}

	matches, serr := recordsOfType(root, typeName)
	if serr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "delete_record_type: "+serr.Error())
	}

	if werr := removeControlPlaneFile(target, sc.SourcePath); werr != nil {
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, sc.SourcePath)}, "delete_record_type: "+werr.Error())
	}
	bumpIndexEpochOrWarn("delete_record_type", t.deps.Home, root)

	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: []string{relControlPlanePath(root, sc.SourcePath)}, At: t.deps.now(),
	})
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		Op: opDeleteRecordType, Name: typeName,
		Path:     relControlPlanePath(root, sc.SourcePath),
		Reverted: len(matches),
	}))
}

// ---------------------------------------------------------------------------
// write_view / delete_view
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// D6 — the kind/part vocabulary has ONE author
//
// design §3: "All six [gates] live in the composer and renderer — testable
// once, skippable by no agent." The 2026-09-05 UAT proved that was a property
// of ONE OP: every G1 gate lives in knowledge_configure_create_view.go, and
// write_view — the same tool, one argument away, in front of the same agent in
// the same turn — called none of them. Asked for a `trend` on a record type
// with no number, the tester agent was refused twice by create_view and then
// called write_view TEN times until one succeeded; the file landed, and the
// server served `kind: trend` with an empty figures row, an empty chart and
// `problems: []`. A directed follow-up wrote `kind: tiles` bound to an enum —
// the exact binding D5 rejected for the record — and it was accepted and
// served the same way.
//
// The distinction D6 draws, and why it is not "close the escape hatch":
//
//   - A part stack the eight kinds do not produce is what the escape hatch is
//     FOR, and D6 does not touch it — except that such a stack must now be a
//     LEGACY-shaped view (layout / filter / grouping / properties /
//     aggregates), which is also the shape every hand-edited file and every
//     imported .base view already has.
//   - `kind: trend` on a type with no number, or `kind: tiles` on a vault
//     where D5 says tiles cannot exist, is NOT an uncovered composition. It is
//     the same closed set the design defines, asserted through a door with no
//     check on it. `kind` was documented here as "provenance only" and then
//     echoed back by the server as the view's kind — provenance for a
//     composition that never happened.
//
// So the gate is on the VOCABULARY, not on the impossibility. Refusing only
// impossible requests would leave two authors of one closed set, disagreeing
// the first time either was extended — the drift D5's "ONE shared eligibility
// helper" ruling exists to prevent.
const (
	writeViewKindKey  = "kind"
	writeViewPartsKey = "parts"
)

// writeViewComposerVocabularyRefusal returns the refusal for a write_view
// definition carrying the composer's own keys, or "" when the definition is
// the legacy shape write_view still owns.
func writeViewComposerVocabularyRefusal(defMap map[string]any) string {
	var found []string
	if _, ok := defMap[writeViewKindKey]; ok {
		found = append(found, "`"+writeViewKindKey+"`")
	}
	if _, ok := defMap[writeViewPartsKey]; ok {
		found = append(found, "`"+writeViewPartsKey+"`")
	}
	if len(found) == 0 {
		return ""
	}
	return "definition carries " + strings.Join(found, " and ") +
		", which op=create_view writes and this op does not. The eight view kinds and " +
		"the part stacks they assemble are the composer's vocabulary, and the eligibility " +
		"rules that give them meaning run only there — a trend needs a number tracked over " +
		"a date, tiles needs an image-capable property, a board needs a small enum. Written " +
		"here they would be asserted with nothing checking them, which is how a `trend` over " +
		"a type with no number gets saved and then served as an empty chart with no problem " +
		"reported. Call op=create_view with kind= and the bindings it names " +
		"(knowledge_describe on the record type lists which kinds this type supports, and " +
		"why the others are not offered). write_view remains the raw escape hatch for the " +
		"LEGACY view shape — layout, filter, grouping, properties, aggregates, sort, limit, " +
		"label, property_config, formulas — the shape hand-edited files and imported .base " +
		"views already have."
}

func (t *ConfigureTool) execWriteView(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	viewName := strings.TrimSpace(stringArg(args["view"]))
	if viewName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'view' is required for write_view")
	}
	// The name becomes a filename under records.ViewsDir — see
	// controlPlaneNameRefusal for why that is checked before anything else.
	if nrefusal := controlPlaneNameRefusal("view", viewName, records.ViewsDir(root)); nrefusal != "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+nrefusal)
	}

	defMap, derr := definitionMap(args["definition"])
	if derr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+derr.Error())
	}
	// D6 (design §9, ratified 2026-09-05): the kind/part vocabulary belongs to
	// op=create_view, and to no other door. See writeViewComposerVocabularyRefusal.
	if refusal := writeViewComposerVocabularyRefusal(defMap); refusal != "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+refusal)
	}
	if existingName, ok := defMap["name"]; ok {
		if s, _ := existingName.(string); strings.TrimSpace(s) != "" && strings.TrimSpace(s) != viewName {
			return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
				"write_view: 'view' is %q but definition.name is %q; they must agree, or definition.name should be left unset", viewName, s))
		}
	}
	defMap["name"] = viewName
	// D-13: `source` (the .base data file) may come as an argument or inside
	// the definition; both must agree when both are given.
	source, srefusal := viewSourceArg(target, args["source"])
	if srefusal != "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+srefusal)
	}
	if defSource, ok := defMap["source"]; ok {
		if s, _ := defSource.(string); strings.TrimSpace(s) != "" {
			defRel, drefusal := viewSourceArg(target, s)
			if drefusal != "" {
				return t.deps.refuse(authorOpConfigure, target, nil, "write_view: definition."+drefusal)
			}
			if source != "" && defRel != source {
				return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
					"write_view: 'source' is %q but definition.source is %q; they must agree", source, s))
			}
			source = defRel
		}
	}
	if source != "" {
		defMap["source"] = source
	}

	yamlBytes, merr := marshalDefinition(defMap)
	if merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+merr.Error())
	}
	viewPath := filepath.Join(records.ViewsDir(root), viewName+controlPlaneFileExt)
	parsed, rej := records.ParseView(viewPath, yamlBytes)
	if rej != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+rej.Reason)
	}

	schemas, _, lerr := records.LoadSchemas(root)
	if lerr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: loading record schemas: "+lerr.Error())
	}
	if rej := records.ValidateViewAgainstSchemas(parsed, schemas); rej != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+rej.Reason)
	}
	// D-21: write_view is an upsert of THIS exact name, but a case-colliding
	// name or a label another view already carries is refused.
	if existing, _, verr := records.LoadViews(root, schemas); verr == nil {
		label := ""
		if parsed.Def.Label != nil {
			label = *parsed.Def.Label
		}
		if crefusal := viewNameCollisionRefusal(existing, viewName, label, false); crefusal != "" {
			return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+crefusal)
		}
	}

	if werr := overwriteControlPlaneFile(target, viewPath, yamlBytes); werr != nil {
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, viewPath)}, "write_view: "+werr.Error())
	}
	sourceCreated, sourceWarn := ensureStarterBaseFile(target, source, parsed.DisplayLabel())

	paths := []string{relControlPlanePath(root, viewPath)}
	if sourceCreated {
		paths = append(paths, source)
	}
	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: paths, At: t.deps.now(),
	})
	// ViewDef.Type is a *string since FR-018b made `type` optional: an UNTYPED
	// view spans every note in scope and is entirely legal. ParseView refuses
	// only a `type:` that is PRESENT but blank (view.go's RejectViewMissingType
	// names it a typo rather than a deliberate absence) — an omitted key
	// reaches here as nil routinely, so this is the ORDINARY path, not a
	// defensive guard. The renderer says "untyped" rather than printing an
	// empty type name, because `querying record type ""` reads as a bug.
	viewType := ""
	if parsed.Def.Type != nil {
		viewType = *parsed.Def.Type
	}
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		Op: opWriteView, Name: viewName, Path: relControlPlanePath(root, viewPath),
		ViewType: viewType, Unservable: t.serveRefusalFor(root, schemas, viewName),
		Source: source, SourceCreated: sourceCreated, SourceWarning: sourceWarn,
	}))
}

// serveRefusalFor answers the one question a "view saved" response used to
// leave to the query path: will knowledge_find actually run this view?
//
// It will not, for three shapes ParseView and ValidateViewAgainstSchemas both
// ACCEPT — a stored `disabled` flag, a DESCENDING grouping key, and any
// declared `formulas` — because VaultFindRequest has no field that could
// carry them and serving the view anyway would silently answer a BROADER
// question than the view asks. That refusal is correct. What was wrong is
// that it arrived nowhere near the write: knowledge_configure reported
// success, knowledge_describe listed the view with its formulas rendered, and
// only knowledge_find said anything — and what it said was "no saved view
// named X", which is flatly false about a view that is on disk and listed.
//
// records.ViewFindLoader was built with this caller in mind and had none:
// view_find_bridge.go's own header states "knowledge_describe and
// knowledge_configure hold a *ViewFindLoader and can ask." This asks. Asking
// the loader rather than re-deriving the three conditions here is the whole
// point — a second copy of translateView's rules is how the write path and
// the query path start disagreeing again.
//
// Returns "" when the view IS servable. A reload failure is REPORTED, not
// swallowed: "saved, and I could not check" is a different statement from
// "saved, and knowledge_find will run it", and only one of them is true.
func (t *ConfigureTool) serveRefusalFor(root string, schemas *records.SchemaSet, viewName string) string {
	set, _, lerr := records.LoadViews(root, schemas)
	if lerr != nil {
		return fmt.Sprintf(
			"could not be checked — re-reading the views directory failed (%v); "+
				"run knowledge_describe to see whether knowledge_find can serve this view", lerr)
	}
	refusal, refused := records.NewViewFindLoader(set).ServeRefusal(viewName)
	if !refused {
		return ""
	}
	return fmt.Sprintf("%s (%s) — %s", refusal.Reason, refusal.Code, refusal.Remedy)
}

func (t *ConfigureTool) execDeleteView(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	viewName := strings.TrimSpace(stringArg(args["view"]))
	if viewName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'view' is required for delete_view")
	}

	set, _, lerr := records.LoadViews(root, nil)
	if lerr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "delete_view: loading existing views: "+lerr.Error())
	}
	// Resolve, not Get: a caller (agent or person relaying what the Library
	// shows) may name this view by its display LABEL rather than the slug it
	// is stored under — UAT 2026-09-13 Q-08's failure, applying identically
	// to delete_view since it resolves an EXISTING view by name exactly the
	// way knowledge_find's `view` argument does. records.ViewSet.Resolve is
	// the one shared implementation of slug-then-label lookup; see its own
	// doc comment for the full rule and why an ambiguous label is refused
	// rather than guessed.
	v, amb, ok := set.Resolve(viewName)
	if !ok {
		if amb != nil {
			pairs := make([]string, 0, len(amb.Candidates))
			for _, c := range amb.Candidates {
				pairs = append(pairs, fmt.Sprintf("%s (%s)", c.Label, c.Slug))
			}
			return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
				"%q names more than one saved view: %s; delete_view needs the slug — shown in parentheses above — because a label is not unique",
				viewName, strings.Join(pairs, ", ")))
		}
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"no view %q is declared; declared views: %s", viewName, joinOrNone(set.Names())))
	}

	// UAT 2026-09-13 D-27: name the downstream damage BEFORE the file goes —
	// every other destructive op in the family does. Embeds are found by
	// the label (what `![[X.base#Label]]` carries) and by the name.
	embeds := notesEmbeddingView(root, v)
	rawSource := ""
	if v.Def.Source != nil && strings.TrimSpace(*v.Def.Source) != "" {
		rawSource = strings.TrimSpace(*v.Def.Source)
	}

	if werr := removeControlPlaneFile(target, v.SourcePath); werr != nil {
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, v.SourcePath)}, "delete_view: "+werr.Error())
	}

	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: []string{relControlPlanePath(root, v.SourcePath)}, At: t.deps.now(),
	})
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		// v.Def.Name (the resolved slug), not the raw viewName argument: the
		// caller may have named this view by its LABEL, and Name should
		// always report the identifier Path actually points at.
		Op: opDeleteView, Name: v.Def.Name, Path: relControlPlanePath(root, v.SourcePath),
		Embeds: embeds, RawSource: rawSource, Label: v.DisplayLabel(),
	}))
}

// deleteViewEmbedListMax bounds how many embedding notes a delete_view
// response names; the count is always exact.
const deleteViewEmbedListMax = 10

// notesEmbeddingView lists the notes whose body carries an embed of v —
// `![[<anything>#<label>]]` or `#<name>]]`, with or without a `|size` — so
// delete_view can say which dashboards will show a broken embed. A walk
// failure answers nil: the cascade line is then simply absent, never wrong.
func notesEmbeddingView(root string, v *records.SavedView) []string {
	fsys := OSLinkFS()
	croot, err := NewCollectionRoot(fsys, root)
	if err != nil {
		return nil
	}
	wr, err := WalkContained(fsys, croot)
	if err != nil {
		return nil
	}
	needles := [][]byte{
		[]byte("#" + v.DisplayLabel() + "]]"), []byte("#" + v.DisplayLabel() + "|"),
		[]byte("#" + v.Def.Name + "]]"), []byte("#" + v.Def.Name + "|"),
	}
	var out []string
	for _, rel := range wr.Files {
		if !IsMarkdownPath(rel) {
			continue
		}
		content, rerr := ReadNoteContent(fsys, filepath.Join(croot.Path(), filepath.FromSlash(rel)))
		if rerr != nil {
			continue
		}
		for _, n := range needles {
			if idx := bytes.Index(content, n); idx >= 0 && bytes.Contains(content[:idx], []byte("![[")) {
				out = append(out, rel)
				break
			}
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Cascade computation
// ---------------------------------------------------------------------------

// ConfigureCascade is the "CASCADE (meaning)" block spec §4.1.6 requires on
// EVERY response, in counts, before anything else — the requirement that
// makes C-B visible at all when the file diff itself is one small YAML file.
type ConfigureCascade struct {
	Matched       int
	Clean         int
	NewlyReported int
	LostValidity  int
	Examples      []string
	// StaleKeys counts, per key, the matched notes still carrying a
	// frontmatter key the (new) schema does not declare — the aftermath of a
	// rename or removal (D-25). Declaration order is not meaningful here;
	// rendered sorted by key.
	StaleKeys map[string]int
	// ViewsBroken names saved views that loaded before this edit and are
	// rejected after it, with the loader's own reason (D-25).
	ViewsBroken []string
}

// staleKeysAcrossRecords counts, per key, how many of the matched records
// carry a frontmatter key the schema does not declare — excluding the
// discriminator/identity keys, which no schema declares.
func staleKeysAcrossRecords(sc *records.Schema, matches []records.Record) map[string]int {
	out := map[string]int{}
	for _, rec := range matches {
		for _, key := range rec.Frontmatter.Keys {
			switch key {
			case records.RecordTypeKey, records.RecordIDKey, records.RecordIDKeyNamespaced:
				continue
			}
			if _, ok := sc.Property(key); !ok {
				out[key]++
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// viewsNewlyRejected lists the saved views the loader accepts against oldSet
// and rejects against newSet — "view X names property priority, which
// record type project does not declare" — each with the loader's reason.
func viewsNewlyRejected(root string, oldSet, newSet *records.SchemaSet) []string {
	_, before, berr := records.LoadViews(root, oldSet)
	_, after, aerr := records.LoadViews(root, newSet)
	if berr != nil || aerr != nil || after == nil {
		return nil
	}
	wasRejected := map[string]bool{}
	if before != nil {
		for _, r := range before.Rejections {
			wasRejected[r.Name+"|"+string(r.Code)] = true
		}
	}
	var out []string
	for _, r := range after.Rejections {
		if wasRejected[r.Name+"|"+string(r.Code)] {
			continue
		}
		name := r.Name
		if name == "" && len(r.Paths) > 0 {
			name = filepath.Base(r.Paths[0])
		}
		out = append(out, fmt.Sprintf("%s (%s)", name, r.Reason))
	}
	return out
}

// computeCreateCascade reports AC-C1: the count of pre-existing notes
// converted, and which of them newly fail validation. LostValidity is always
// 0 by construction — these notes were ordinary notes a moment ago (D1: a
// type matching no schema is not a record at all), so there is no prior
// record-validity state for any of them to have LOST. That is a substantive
// difference from edit_record_type, not an oversight: reporting it as 0 here
// says, honestly, "nothing was already a valid record of this type before
// this call, because this type did not exist before this call."
func computeCreateCascade(newSet *records.SchemaSet, matches []records.Record) ConfigureCascade {
	report := records.Validate(newSet, matches, records.ValidateOptions{})
	c := ConfigureCascade{Matched: len(matches)}
	for _, rr := range report.Records {
		if rr.Valid() {
			c.Clean++
			continue
		}
		c.NewlyReported++
		for _, f := range rr.Errors() {
			if len(c.Examples) < cascadeExampleCap {
				c.Examples = append(c.Examples, f.String())
			}
		}
	}
	return c
}

// computeEditCascade reports FR-015/FR-017's revalidation. NewlyReported and
// LostValidity are the SAME set of records here — a record either was valid
// under the old declaration and is not under the new one, or it was already
// invalid and stays this cascade's concern rather than its cause. A record
// invalid both before and after is neither newly reported nor newly lost: it
// counts toward Matched, never toward Clean, and its finding is not named
// here because check_integrity already names it and this report would
// otherwise imply the edit caused a fault it did not cause.
func computeEditCascade(oldReport, newReport *records.ValidationReport) ConfigureCascade {
	wasValid := make(map[string]bool, len(oldReport.Records))
	for _, rr := range oldReport.Records {
		wasValid[rr.Path] = rr.Valid()
	}
	c := ConfigureCascade{Matched: len(newReport.Records)}
	for _, rr := range newReport.Records {
		if rr.Valid() {
			c.Clean++
			continue
		}
		if wasValid[rr.Path] {
			c.NewlyReported++
			c.LostValidity++
			for _, f := range rr.Errors() {
				if len(c.Examples) < cascadeExampleCap {
					c.Examples = append(c.Examples, f.String())
				}
			}
		}
	}
	return c
}

// ---------------------------------------------------------------------------
// Definition decoding — the argument the model sends becomes the file
// ---------------------------------------------------------------------------

// definitionMap validates that `definition` is present and is a JSON object,
// and returns a shallow copy so callers can set/overwrite the `type`/`name`
// key without mutating the caller's own args map.
func definitionMap(raw any) (map[string]any, error) {
	if raw == nil {
		return nil, errors.New("'definition' is required")
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("'definition' must be an object, found %T", raw)
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out, nil
}

// mergeDeclaredType enforces that the op-level `type` argument and
// definition's own `type` key agree, when the definition states one at all.
// Refusing a disagreement rather than silently preferring one is the same
// posture D2.1 takes for every other declared-vs-implied conflict in this
// vault: an author who wrote something meaningful must never have it thrown
// away in silence.
func mergeDeclaredType(defMap map[string]any, typeName string) error {
	if declared, ok := defMap["type"]; ok {
		if s, _ := declared.(string); strings.TrimSpace(s) != "" && strings.TrimSpace(s) != typeName {
			return fmt.Errorf("'type' is %q but definition.type is %q; they must agree", typeName, s)
		}
	}
	defMap["type"] = typeName
	return nil
}

// marshalDefinition renders the agent's JSON-shaped definition as the YAML
// bytes records.ParseSchema / records.ParseView already know how to
// validate. Numbers are normalised first (see normalizeJSONNumbers) so a
// tool-call argument's `schema_version: 1` — which arrives as a Go
// float64 — round-trips as a whole number rather than a YAML float literal.
func marshalDefinition(defMap map[string]any) ([]byte, error) {
	normalized := normalizeJSONNumbers(defMap)
	out, err := yaml.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("definition could not be encoded: %w", err)
	}
	return out, nil
}

// normalizeJSONNumbers recursively converts any whole-valued float64 (what a
// JSON-decoded tool argument holds for every bare integer) into an int64, so
// the YAML this file writes round-trips through records.ParseSchema's
// `schema_version *int` and every other integer-shaped declared key without
// depending on how a YAML float happens to format.
func normalizeJSONNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeJSONNumbers(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeJSONNumbers(val)
		}
		return out
	case float64:
		if t == math.Trunc(t) && !math.IsInf(t, 0) {
			return int64(t)
		}
		return t
	default:
		return v
	}
}

// ---------------------------------------------------------------------------
// Reading the vault for a cascade count
// ---------------------------------------------------------------------------

// recordsOfType scans the collection and parses every note declaring
// `type: typeName`, for a cascade report to validate. Bounded by the same
// IntegritySweepLimit check_integrity enforces (FR-075a) — a cascade
// computation opens every note of the matching type, same order of work as
// a sweep, and must refuse above the bound rather than silently truncate.
func recordsOfType(root, typeName string) ([]records.Record, error) {
	scan, err := Scan(root)
	if err != nil {
		return nil, fmt.Errorf("scanning collection: %w", err)
	}
	notes := scan.Notes()
	if len(notes) > IntegritySweepLimit {
		return nil, fmt.Errorf(
			"this knowledge base has %d notes, above the %d-note bound a cascade computation can sweep",
			len(notes), IntegritySweepLimit)
	}
	var out []records.Record
	for _, n := range notes {
		abs := filepath.Join(root, filepath.FromSlash(n.RelPath))
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			// An unreadable note is check_integrity's finding to report, not
			// this cascade's failure to compute — the note plainly does not
			// match typeName either way, so it is simply excluded here.
			continue
		}
		rec := records.ParseRecord(n.RelPath, data)
		if rec.TypeName() == typeName {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Writing the control-plane file — the one thing this whole file exists to
// do safely
// ---------------------------------------------------------------------------

// controlPlaneFilePerm / controlPlaneDirPerm mirror marker.go's own posture
// for Omnipus state living inside the operator's folder: owner-only, because
// no other process needs to read a schema or view file and it may grow to
// hold more of the vault's own configuration.
const (
	controlPlaneDirPerm  fs.FileMode = 0o700
	controlPlaneFilePerm fs.FileMode = 0o600
)

// controlPlaneLockKey names the synthetic "note path" WithNoteWriteLock
// strikes its tier-1 lock on. It is namespaced under the same
// CollectionRoot every note lock uses, so a schema file and a note that
// happened to share a spelling (impossible in practice — see
// relControlPlanePath — but not relied upon here) could never collide.
func controlPlaneLockKey(root, abs string) string {
	rel := relControlPlanePath(root, abs)
	return rel
}

// createControlPlaneFile writes a NEW schema or view file. O_EXCL — not
// fileutil.WriteFileAtomic's temp-file-plus-rename — because rename REPLACES
// an existing destination, which is exactly the "already exists" race
// create_record_type must refuse rather than silently win (mirrors
// author.go's own reasoning for CreateNote, restated in this file's header).
func createControlPlaneFile(target mutationTarget, abs string, data []byte) error {
	return WithNoteWriteLock(target.lock, controlPlaneLockKey(target.collection.Root(), abs), func() error {
		if _, vErr := resolveControlWritePath(OSLinkFS(), target.collection.Root(), abs); vErr != nil {
			return fmt.Errorf("knowledge: control-plane write refused: %w", vErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(abs), controlPlaneDirPerm); mkErr != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(abs), mkErr)
		}
		f, oerr := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, controlPlaneFilePerm)
		if oerr != nil {
			return oerr
		}
		if _, werr := f.Write(data); werr != nil {
			_ = f.Close()
			_ = os.Remove(abs)
			return fmt.Errorf("write %s: %w", abs, werr)
		}
		if serr := f.Sync(); serr != nil {
			_ = f.Close()
			_ = os.Remove(abs)
			return fmt.Errorf("sync %s: %w", abs, serr)
		}
		return f.Close()
	})
}

// overwriteControlPlaneFile replaces an EXISTING schema or view file's
// content (edit_record_type, write_view) via atomic temp-file-plus-rename,
// inside the same tier-1 lock createControlPlaneFile uses — FR-043a's
// "internal CAS that guards THE FILE": a concurrent overwrite of this exact
// file cannot interleave with this one, in-process or cross-process.
func overwriteControlPlaneFile(target mutationTarget, abs string, data []byte) error {
	return WithNoteWriteLock(target.lock, controlPlaneLockKey(target.collection.Root(), abs), func() error {
		if _, vErr := resolveControlWritePath(OSLinkFS(), target.collection.Root(), abs); vErr != nil {
			return fmt.Errorf("knowledge: control-plane write refused: %w", vErr)
		}
		if mkErr := os.MkdirAll(filepath.Dir(abs), controlPlaneDirPerm); mkErr != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(abs), mkErr)
		}
		return fileutil.WriteFileAtomic(abs, data, controlPlaneFilePerm)
	})
}

// removeControlPlaneFile deletes a schema or view file (delete_record_type,
// delete_view), inside the same lock.
func removeControlPlaneFile(target mutationTarget, abs string) error {
	return WithNoteWriteLock(target.lock, controlPlaneLockKey(target.collection.Root(), abs), func() error {
		if _, vErr := resolveControlWritePath(OSLinkFS(), target.collection.Root(), abs); vErr != nil {
			return fmt.Errorf("knowledge: control-plane delete refused: %w", vErr)
		}
		if rerr := os.Remove(abs); rerr != nil {
			return rerr
		}
		return nil
	})
}

// controlPlaneFileExt is the suffix every control-plane name is given when it
// becomes a filename. Named once so controlPlaneNameRefusal validates the
// EXACT string the callers below join, rather than a second spelling of it.
const controlPlaneFileExt = ".yaml"

// controlPlaneNameRefusal is the ONE validator for every argument that becomes
// a control-plane FILENAME — `view` under records.ViewsDir, `type` under
// records.SchemaDir. It returns "" when the name is safe, or the refusal to
// give the caller.
//
// THE THREAT, PLAINLY. These names arrive from an agent, and the only thing
// standing between the argument and an os.OpenFile is a filepath.Join. A
// `view` of "../records/company" joins to <vault>/.omnipus-vault/records/
// company.yaml — the record-type SCHEMA for `company` — and every other gate
// in this tool passes it, because none of them ever looked at the name's
// shape: the view document is valid, it names a declared type, and the write
// is an ordinary atomic overwrite. Longer "../.." chains leave the vault
// entirely and land anywhere the process can write. The same join guards
// nothing under records.SchemaDir either; that door writes with O_EXCL, so it
// cannot overwrite, but it can still CREATE a file outside the vault.
//
// So the rule is deliberately narrow and stated positively: a control-plane
// name is a plain filename, and it produces a file DIRECTLY inside the
// directory that kind of file lives in. Nothing about "sanitising" the name
// into something safe — a name that is not already safe is refused by name,
// because an agent that asked for "../records/company" did not mean
// "records-company" and must be told so rather than silently redirected.
//
// The checks are belt and braces on purpose. The three explicit refusals
// (separator, absolute, bare dot segment) name what is wrong in words the
// caller can act on; the containment assertion afterwards is the backstop
// that catches anything the three did not anticipate on a platform whose
// separator rules differ, and it compares against the SAME joined path the
// callers go on to use.
func controlPlaneNameRefusal(argName, name, dir string) string {
	label := records.VaultMarkerDirName + "/" + filepath.Base(dir)
	refuse := func(why string) string {
		return fmt.Sprintf(
			"'%s' must be a name, not a path: %q %s. It becomes one file directly inside %s/, "+
				"so it may not contain a path separator, may not be an absolute path, and may "+
				"not be \".\" or \"..\"",
			argName, name, why, label)
	}

	switch {
	case name == "":
		return refuse("is blank")
	case strings.ContainsRune(name, 0):
		return refuse("contains a NUL byte")
	case strings.ContainsAny(name, `/\`):
		// Both separators, on every platform: a backslash is not a separator
		// on Linux or macOS, but a name carrying one is still an attempt to
		// address a path and is refused rather than turned into a file whose
		// name contains a backslash.
		return refuse("contains a path separator")
	case filepath.IsAbs(name):
		return refuse("is an absolute path")
	case name == "." || name == "..":
		return refuse("names a directory, not a file")
	}
	// UAT 2026-09-13 D-56: a name is a file name and, for a record type, a
	// value every note's `type:` key holds — so quotes, control characters
	// and the characters no filesystem accepts are refused by name, and a
	// type name may carry no whitespace at all.
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			return fmt.Sprintf("'%s' %q contains a control character; use letters, digits, spaces, '-' or '_'", argName, name)
		case strings.ContainsRune("\"'<>:|?*", r):
			return fmt.Sprintf("'%s' %q contains %q, which is not allowed in a file name; use letters, digits, spaces, '-' or '_'", argName, name, r)
		}
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Sprintf("'%s' %q begins with '.', which would make a hidden file; start with a letter or digit", argName, name)
	}
	if argName == "type" && strings.ContainsAny(name, " \t") {
		return fmt.Sprintf("'type' %q contains whitespace; a record type name is written into every note's `type:` key — use letters, digits, '-' or '_' (e.g. %q)",
			name, strings.Join(strings.Fields(name), "-"))
	}

	// The backstop: the file this name produces must sit directly in dir.
	// Checked against the join the callers actually perform, so the two can
	// never drift.
	target := filepath.Join(dir, name+controlPlaneFileExt)
	if filepath.Dir(target) != filepath.Clean(dir) {
		return refuse("resolves outside the directory it must be written in")
	}
	return ""
}

// relControlPlanePath renders a schema/view's absolute path as the
// vault-relative form every message and audit record names it by
// (".omnipus-vault/records/company.yaml"), falling back to the absolute path
// if it is somehow outside root — never silently dropped from the message.
func relControlPlanePath(root, abs string) string {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(rel)
}

// ---------------------------------------------------------------------------
// Rendering — compact text at the tool boundary, never JSON (FR-072)
// ---------------------------------------------------------------------------

// ConfigureData is everything RenderConfigure needs to render one response.
type ConfigureData struct {
	Op   string
	Name string
	Path string

	// Cascade is set for create_record_type / edit_record_type. Absent for
	// delete_record_type (Reverted is the shape that fits it — see AC-C4)
	// and for write_view / delete_view (a view changes no note's validity).
	Cascade *ConfigureCascade

	// Reverted is delete_record_type's count of records that revert to
	// ordinary notes (AC-C4).
	Reverted int

	// ViewType is write_view's/create_view's record type, named so the
	// response confirms what the view queries without a second call. Empty
	// means the view is UNTYPED (FR-018b) and spans every note in scope — a
	// legal view, not a missing value.
	ViewType string

	// Kind is create_view's own kind argument, echoed back so the response
	// confirms which of the eight it built without a second call. Empty for
	// every op other than create_view.
	Kind string

	// PartsSummary is create_view's assembled part stack, one entry per
	// part, in draw order — design §6.1: "the answer with … the assembled
	// stack so the agent can read back what it built." Empty for every op
	// other than create_view.
	PartsSummary []string

	// Source is the .base data file the saved view is tied to (D-13),
	// SourceCreated whether this call wrote a starter file there, and
	// SourceWarning why it could not. Empty for a view with no source.
	Source        string
	SourceCreated bool
	SourceWarning string

	// Embeds are the notes that embed the view delete_view just removed
	// (D-27); RawSource is that view's .base file, which delete_view does
	// NOT edit; Label is the label those embeds address it by.
	Embeds    []string
	RawSource string
	Label     string

	// Unservable is write_view's/create_view's answer to "will knowledge_find run this?",
	// empty when it will. See ConfigureTool.serveRefusalFor: a view carrying
	// `formulas`, a descending grouping or a stored `disabled` flag is written
	// faithfully and REFUSED at query time, and the write is the only place
	// that fact can reach the author while they can still act on it.
	Unservable string
}

// RenderConfigure renders one knowledge_configure result as compact text
// (FR-072). The cascade block, where one applies, is stated in counts BEFORE
// anything else — spec §4.1.6: "Every response MUST state the cascade in
// meaning, in counts, before the next-actions block." — because the file
// diff alone (one small YAML file) is exactly what makes C-B invisible.
func RenderConfigure(d ConfigureData) string {
	var b strings.Builder
	switch d.Op {
	case opCreateRecordType:
		fmt.Fprintf(&b, "record type %q created at %s\n", d.Name, d.Path)
		writeCascadeBlock(&b, d.Name, d.Cascade)
	case opEditRecordType:
		fmt.Fprintf(&b, "record type %q edited at %s\n", d.Name, d.Path)
		writeCascadeBlock(&b, d.Name, d.Cascade)
	case opDeleteRecordType:
		fmt.Fprintf(&b, "record type %q deleted (%s removed)\n", d.Name, d.Path)
		fmt.Fprintf(&b, "CASCADE (meaning): %d record(s) revert to ordinary notes\n", d.Reverted)
	case opWriteView:
		queries := fmt.Sprintf("querying record type %q", d.ViewType)
		if d.ViewType == "" {
			queries = "untyped: it spans every note in scope"
		}
		fmt.Fprintf(&b, "view %q saved at %s, %s\n", d.Name, d.Path, queries)
		fmt.Fprintf(&b, "CASCADE (meaning): what this view returns changes; no note's own validity changes\n")
		writeViewSourceLine(&b, d)
		if d.Unservable != "" {
			// Stated AFTER the cascade block (spec §4.1.6 puts the cascade
			// first) and before anything else, in the words the loader itself
			// uses, so the same sentence appears here and in a
			// knowledge_describe listing of the same view.
			fmt.Fprintf(&b, "NOT SERVABLE by knowledge_find: %s\n", d.Unservable)
		}
	case opCreateView:
		queries := fmt.Sprintf("querying record type %q", d.ViewType)
		if d.ViewType == "" {
			queries = "untyped: it spans every note in scope"
		}
		fmt.Fprintf(&b, "view %q saved at %s, kind=%s, %s\n", d.Name, d.Path, d.Kind, queries)
		fmt.Fprintf(&b, "CASCADE (meaning): what this view returns changes; no note's own validity changes\n")
		fmt.Fprintf(&b, "PARTS: %s\n", strings.Join(d.PartsSummary, " -> "))
		writeViewSourceLine(&b, d)
		if d.Unservable != "" {
			fmt.Fprintf(&b, "NOT SERVABLE by knowledge_find: %s\n", d.Unservable)
		}
	case opDeleteView:
		fmt.Fprintf(&b, "view %q deleted (%s removed)\n", d.Name, d.Path)
		fmt.Fprintf(&b, "CASCADE (meaning): no note's own validity changes; any query naming this view by name is now refused\n")
		if len(d.Embeds) > 0 {
			shown := d.Embeds
			more := ""
			if len(shown) > deleteViewEmbedListMax {
				shown = shown[:deleteViewEmbedListMax]
				more = fmt.Sprintf(", and %d more", len(d.Embeds)-deleteViewEmbedListMax)
			}
			fmt.Fprintf(&b, "  %d note(s) embed this view (as #%s) and will now show it as a broken embed: %s%s\n",
				len(d.Embeds), d.Label, strings.Join(shown, ", "), more)
		}
		if d.RawSource != "" {
			fmt.Fprintf(&b, "  the data file %s still lists a view named %q — this op removes only the saved view, never a .base file; edit %s in the Library to remove it there too\n",
				d.RawSource, d.Label, d.RawSource)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// writeViewSourceLine is the D-13 SOURCE line for write_view/create_view: an
// agent must be able to see whether the view it just saved has a place in
// the Library at all.
func writeViewSourceLine(b *strings.Builder, d ConfigureData) {
	switch {
	case d.Source == "":
		fmt.Fprintf(b, "SOURCE: none — this view answers knowledge_find but appears in no data file; give source: <file>.base to show it in the Library's base preview and in ![[<file>.base#%s]] embeds\n", d.Name)
	case d.SourceCreated:
		fmt.Fprintf(b, "SOURCE: %s (a starter data file was created there; open it in the Library to see this view)\n", d.Source)
	case d.SourceWarning != "":
		fmt.Fprintf(b, "SOURCE: %s — WARNING: %s\n", d.Source, d.SourceWarning)
	default:
		fmt.Fprintf(b, "SOURCE: %s\n", d.Source)
	}
}

func writeCascadeBlock(b *strings.Builder, typeName string, c *ConfigureCascade) {
	if c == nil {
		return
	}
	fmt.Fprintf(b, "CASCADE (meaning): %d note(s) now match record type %q\n", c.Matched, typeName)
	fmt.Fprintf(b, "  %d validate clean\n", c.Clean)
	if c.NewlyReported > 0 {
		fmt.Fprintf(b, "  %d newly reported:\n", c.NewlyReported)
		for _, ex := range c.Examples {
			fmt.Fprintf(b, "    %s\n", ex)
		}
		if c.NewlyReported > len(c.Examples) {
			fmt.Fprintf(b, "    ... and %d more\n", c.NewlyReported-len(c.Examples))
		}
	} else {
		fmt.Fprintf(b, "  0 newly reported\n")
	}
	fmt.Fprintf(b, "  %d record(s) lost validity\n", c.LostValidity)
	if len(c.StaleKeys) > 0 {
		keys := sortedMapKeys(c.StaleKeys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s (%d note(s))", k, c.StaleKeys[k]))
		}
		fmt.Fprintf(b, "  STALE KEYS: notes still carry key(s) this type no longer declares — %s — which no reader or query evaluates; on each note, set the declared key with knowledge_edit set_property and remove the old one with value: null, or re-declare the property\n",
			strings.Join(parts, ", "))
	}
	if len(c.ViewsBroken) > 0 {
		fmt.Fprintf(b, "  VIEWS BROKEN: %d saved view(s) no longer load after this edit:\n", len(c.ViewsBroken))
		for _, v := range c.ViewsBroken {
			fmt.Fprintf(b, "    %s\n", v)
		}
	}
}
