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
)

// vaultConfigureOps lists the accepted ops, in the order spec §4.1.6
// documents them.
var vaultConfigureOps = []string{
	opCreateRecordType, opEditRecordType, opDeleteRecordType, opWriteView, opDeleteView,
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

// configureArgNames is every argument Parameters() declares. expect_version
// is DELIBERATELY absent (FR-018a, AC-C3) — its absence from this list is
// what makes it absent from the tool schema, which is what the acceptance
// criterion actually tests.
var configureArgNames = []string{"op", "collection", "type", "view", "definition"}

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
		"note that already carries that type in its frontmatter. write_view / delete_view " +
		"author a saved query — no note's bytes or validity changes, only what a query returns. " +
		"Never touches a note's own content or path; use knowledge_edit or knowledge_restructure " +
		"for those. There is no built-in vocabulary: this vault's record types, properties and " +
		"enum values are entirely what this tool (or the operator) has declared."
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
				"description": "write_view / delete_view: the view name.",
			},
			"definition": map[string]any{
				"type": "object",
				"description": "create_record_type / edit_record_type: the full schema — " +
					"schema_version (required), type, optional label, optional identity " +
					"{prefix}, and properties (a map of property name to {type, many, " +
					"required, and per-type: label, values, to, inverse, unit}). The seven " +
					"property types are text, enum, relation, date, integer, decimal, person — " +
					"there is no eighth and no built-in vocabulary; every type name, property " +
					"name and enum value is this vault's own. write_view: schema_version " +
					"(required), type (the record type queried), and optional label, filters, " +
					"group_by, sort, properties, aggregates, limit. `name` is taken from `view`, " +
					"not from this object.",
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
	if unknown := unknownArgs(args, configureArgNames); len(unknown) > 0 {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"unknown argument(s) %s; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(configureArgNames, ", ")))
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

	switch op {
	case opCreateRecordType:
		return t.execCreateRecordType(target, args)
	case opEditRecordType:
		return t.execEditRecordType(target, args)
	case opDeleteRecordType:
		return t.execDeleteRecordType(target, args)
	case opWriteView:
		return t.execWriteView(target, args)
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
		return t.deps.refuse(authorOpConfigure, target, nil, "'type' is required for create_record_type")
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
	schemaPath := filepath.Join(records.SchemaDir(root), typeName+".yaml")
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

func (t *ConfigureTool) execWriteView(target mutationTarget, args map[string]any) *tools.ToolResult {
	root := target.collection.Root()
	viewName := strings.TrimSpace(stringArg(args["view"]))
	if viewName == "" {
		return t.deps.refuse(authorOpConfigure, target, nil, "'view' is required for write_view")
	}

	defMap, derr := definitionMap(args["definition"])
	if derr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+derr.Error())
	}
	if existingName, ok := defMap["name"]; ok {
		if s, _ := existingName.(string); strings.TrimSpace(s) != "" && strings.TrimSpace(s) != viewName {
			return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
				"write_view: 'view' is %q but definition.name is %q; they must agree, or definition.name should be left unset", viewName, s))
		}
	}
	defMap["name"] = viewName

	yamlBytes, merr := marshalDefinition(defMap)
	if merr != nil {
		return t.deps.refuse(authorOpConfigure, target, nil, "write_view: "+merr.Error())
	}
	viewPath := filepath.Join(records.ViewsDir(root), viewName+".yaml")
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

	if werr := overwriteControlPlaneFile(target, viewPath, yamlBytes); werr != nil {
		return t.deps.refuse(authorOpConfigure, target, []string{relControlPlanePath(root, viewPath)}, "write_view: "+werr.Error())
	}

	t.deps.record(AuthorAuditRecord{
		Operation: authorOpConfigure, Outcome: AuthorOutcomeApplied,
		AgentID: target.agentID, WorkspaceID: target.workspaceID,
		Collection: target.col.Name, Root: root,
		Paths: []string{relControlPlanePath(root, viewPath)}, At: t.deps.now(),
	})
	return tools.NewToolResult(RenderConfigure(ConfigureData{
		Op: opWriteView, Name: viewName, Path: relControlPlanePath(root, viewPath),
		ViewType: parsed.Def.Type,
	}))
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
	v, ok := set.Get(viewName)
	if !ok {
		return t.deps.refuse(authorOpConfigure, target, nil, fmt.Sprintf(
			"no view %q is declared; declared views: %s", viewName, joinOrNone(set.Names())))
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
		Op: opDeleteView, Name: viewName, Path: relControlPlanePath(root, v.SourcePath),
	}))
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
			"this vault has %d notes, above the %d-note bound a cascade computation can sweep",
			len(notes), IntegritySweepLimit)
	}
	var out []records.Record
	for _, n := range notes {
		abs := filepath.Join(root, filepath.FromSlash(n.RelPath))
		data, rerr := os.ReadFile(abs) //nolint:gosec // abs is root-joined from a Scan() result, which enumerates only paths already contained in root
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
		if mkErr := os.MkdirAll(filepath.Dir(abs), controlPlaneDirPerm); mkErr != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(abs), mkErr)
		}
		f, oerr := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, controlPlaneFilePerm) //nolint:gosec // abs is built from records.SchemaDir/ViewsDir joined with a name this file controls
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
		if rerr := os.Remove(abs); rerr != nil {
			return rerr
		}
		return nil
	})
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

	// ViewType is write_view's record type, named so the response confirms
	// what the view queries without a second call.
	ViewType string
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
		fmt.Fprintf(&b, "view %q saved at %s, querying record type %q\n", d.Name, d.Path, d.ViewType)
		fmt.Fprintf(&b, "CASCADE (meaning): what this view returns changes; no note's own validity changes\n")
	case opDeleteView:
		fmt.Fprintf(&b, "view %q deleted (%s removed)\n", d.Name, d.Path)
		fmt.Fprintf(&b, "CASCADE (meaning): no note's own validity changes; any query naming this view by name is now refused\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
}
