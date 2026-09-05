// Omnipus — ADR-068 D10 / spec FR-018: saved views, READ SIDE ONLY.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS, AND THE ONE THING IT DELIBERATELY IS NOT
//
// A saved view is a saved query, stored as data in
// <vault>/.omnipus-vault/views/<name>.yaml — the location is stated in the
// contract (the ViewDef schema, hosted INLINE in contracts/openapi.yaml
// under components.schemas since it references the recursive VaultFilterNode
// — see that file's own note), which is the single
// source of truth for the format under Hard Constraint #8.
//
// THREE CALLERS NEED TO READ ONE, and they arrived at three different times:
//
//	knowledge_describe   renders the views that already exist, so an agent's
//	                 opening move is "is there a view for this?" rather than
//	                 "let me invent a filter" (spec §4.1.1)
//	knowledge_find       applies one, then refines it with `filter` (spec §4.1.2)
//	knowledge_configure  writes one, and must validate what it is about to write
//	                 (spec §4.1.6, FR-018)
//
// Three readers would have become three parsers, three notions of what a
// malformed view is, and three answers to "does this view name a property the
// schema no longer declares". This file is the one loader; it has NO WRITER,
// on purpose. Writing is knowledge_configure's, and a writer living beside the
// reader is how a read path acquires the ability to repair what it reads.
//
// THE MODEL IS THE GENERATED TYPE. generated.ViewDef comes from
// contracts/openapi.yaml via oapi-codegen and is the only legal cross-boundary type for
// a view (Hard Constraint #8). There is deliberately no parallel
// hand-written struct here: the persisted YAML is a wire format the SPA reads,
// so a second shape would be exactly the drift the constraint exists to stop.
//
// WHY YAML IS DECODED THROUGH JSON. The generated type carries `json:` tags
// and no `yaml:` tags, because it was generated from a JSON-Schema contract.
// yaml.v3 would lower-case the Go field names and silently miss
// `property_config` and every other multi-word key — producing a view that
// parsed cleanly and had lost half of itself. So the YAML is decoded to a
// generic value, re-encoded as JSON, and unmarshalled into the generated type
// with DisallowUnknownFields, which is what makes the contract's
// `additionalProperties: false` an ENFORCED rule rather than a comment.
// ---------------------------------------------------------------------------

const (
	// ViewsDirName is the subdirectory of the marker directory that holds
	// saved views. It sits beside RecordsDirName; both are the control plane
	// (spec FR-015's restated rule), and both are written only by
	// knowledge_configure.
	ViewsDirName = "views"
)

// ---------------------------------------------------------------------------
// THERE IS ONE VIEW FORMAT AND IT CARRIES NO VERSION NUMBER
//
// A view is: ONE `filter` tree of all/any/not over the ten SQL operators —
// the same grammar knowledge_find evaluates — `grouping` keys that each carry
// a direction, an OPTIONAL `type`, plus `layout`, `formulas` and
// `property_config`.
//
// A SECOND, FLAT, AND-ONLY FORMAT USED TO LIVE HERE, behind a `schema_version`
// key and a key partition that policed which spelling belonged to which
// version. It stored `filters` (a flat AND-list in a separate seven-operator
// vocabulary) and `group_by` (a bare name list with no direction). The whole
// apparatus — the version constants, the supported-set, the partition, the
// two parallel translators and the two parallel renderers — existed for ONE
// purpose: keeping files written under the old format readable. No such file
// was ever written outside this project's own tooling, and none exists on
// disk, so the format is deleted rather than versioned around.
//
// THE RULE THAT PARTITION PROTECTED IS NOT DELETED, and a reader arriving from
// the old comments should be clear which of the two went. A VIEW IS NEVER
// BROADENED ON THE OPERATOR'S BEHALF (FR-105). The retired vocabulary's
// `contains` meant whole-element membership, and the obvious rewrite to
// `LIKE '%…%'` turns that into substring matching — `labels contains "in"`
// newly matching `indoor`, `printing` and `min`. Spec Draft 10 specified that
// translation and Draft 11 withdrew it as review finding F5. The prohibition
// still stands and knowledge_configure still enforces it; what is gone is the
// old format that made the mistranslation POSSIBLE. There is nothing left to
// mistranslate here, which is a stronger guarantee than the guard was.
// ---------------------------------------------------------------------------

// ViewsDir returns <vault>/.omnipus-vault/views.
func ViewsDir(vaultRoot string) string {
	return filepath.Join(vaultRoot, VaultMarkerDirName, ViewsDirName)
}

// ---------------------------------------------------------------------------
// Rejections — the same posture schema.go takes, for the same reason
// ---------------------------------------------------------------------------

// ViewRejectionCode names WHY a view file was refused. It is a code rather
// than a message so a caller can distinguish "you forgot a field" from "this
// view names a property the schema no longer declares" — the second is a
// vault that drifted, not a file somebody typed wrong.
type ViewRejectionCode string

const (
	RejectViewUnreadable    ViewRejectionCode = "view_unreadable"
	RejectViewInvalidYAML   ViewRejectionCode = "view_invalid_yaml"
	RejectViewEmpty         ViewRejectionCode = "view_empty"
	RejectViewMissingName   ViewRejectionCode = "view_missing_name"
	RejectViewMissingType   ViewRejectionCode = "view_missing_type"
	RejectViewDuplicateName ViewRejectionCode = "view_duplicate_name"
	RejectViewUnknownKey    ViewRejectionCode = "view_unknown_key"
	// RejectViewUnknownType is a view naming a record type the vault does not
	// declare. Reported rather than dropped: a view that queries a type
	// somebody deleted returns nothing, and "nothing" is indistinguishable
	// from "no matching records" — the silent-empty-result failure FR-024
	// exists to remove, arriving through the view instead of the query.
	RejectViewUnknownType ViewRejectionCode = "view_unknown_type"
	// RejectViewUnknownProperty is a view naming a property the type does not
	// declare — in a filter, a group_by, a sort, a select or an aggregate.
	RejectViewUnknownProperty ViewRejectionCode = "view_unknown_property"
	// RejectViewInvalidLayout is a `layout` outside the declared enum. The
	// engine never reads layout — the SPA does — but an unrecognised value
	// would render as the default table, which is precisely the silent
	// flattening FR-109 exists to make impossible.
	RejectViewInvalidLayout ViewRejectionCode = "view_invalid_layout"
	// RejectViewFilterTooLarge is a filter tree over FR-023c's bound: at most
	// 64 leaves, at most 8 levels deep. The refusal names which bound.
	RejectViewFilterTooLarge ViewRejectionCode = "view_filter_too_large"
	// RejectViewInvalidFilterNode is a filter node that is neither a leaf
	// nor a combinator, or is both.
	RejectViewInvalidFilterNode ViewRejectionCode = "view_invalid_filter_node"
	// RejectViewInvalidFormula is a `formulas` entry that does not parse, does
	// not type, exceeds FR-146's caps, or takes part in a FR-148 reference
	// cycle.
	RejectViewInvalidFormula ViewRejectionCode = "view_invalid_formula"
	// RejectViewUnknownFormula is a `formula.<name>` reference in a property
	// position naming a formula the view does not declare.
	RejectViewUnknownFormula ViewRejectionCode = "view_unknown_formula"
	// RejectViewInvalidKind is a `kind` outside the eight declared view kinds
	// (view-kinds-design-2026-09-03 §2.3). The field is PROVENANCE — nothing
	// renders from it — so an unrecognised value cannot draw the wrong thing;
	// what it can do is send the composer's re-edit path looking for a kind
	// that does not exist, and answer "what kind is this view?" with a word no
	// part of the system knows.
	RejectViewInvalidKind ViewRejectionCode = "view_invalid_kind"
	// RejectViewInvalidPart is a malformed entry in a view's `parts` stack: an
	// unrecognised `part`, a blank binding, an unrecognised aggregate, or a
	// grouping direction outside asc/desc.
	//
	// AN UNKNOWN PART IS REFUSED RATHER THAN SKIPPED, which is the same ruling
	// RejectViewInvalidLayout carries and it is here for the same measured
	// reason. A part nobody recognises, quietly dropped, leaves a view that
	// loads clean, renders a stack one element short, and says nothing — the
	// silently-flattened cards view again, one layer down. A blank binding is
	// the same failure with a different shape: `number: ""` on a figures part
	// is a headline number bound to no property, which draws an empty figure
	// rather than an error.
	RejectViewInvalidPart ViewRejectionCode = "view_invalid_part"
	// RejectViewUnknownEnumValue is a filter literal that is not a member of
	// the enum the compared property declares — the ViewDef contract's "or
	// enum value" half. Reported rather than served, because an undeclared
	// literal cannot match any conforming record: `=` selects nothing and
	// `<>` selects everything, and both look exactly like a correct answer.
	RejectViewUnknownEnumValue ViewRejectionCode = "view_unknown_enum_value"
)

// ViewRejection is one refused view file.
type ViewRejection struct {
	// Paths names every file involved. A duplicate-name conflict holds both,
	// for the reason FR-003 gives for schemas: a rejection that named only
	// the second leaves an operator hunting for the first.
	Paths []string
	// Name is the declared view name where one was readable.
	Name string
	// Source is the view's own `source:` — the vault-relative path of the
	// `.base` file it was imported from — where the file was readable enough
	// to hold one. Empty for an authored view, and empty for a file so broken
	// that no key could be read out of it at all.
	//
	// IT IS HERE SO A REJECTION CAN BE ATTRIBUTED. A caller listing the views
	// that came from one base file must be able to say "and 2 more could not
	// be loaded" — and a rejected view is exactly the one whose parsed Def
	// nobody has. Without this the count would have to be guessed from the
	// filename spelling, which is the importer's convenience, not an index.
	Source string
	Code   ViewRejectionCode
	Reason string
}

// String renders a rejection as one reviewable line.
//
// Deliberately not Error(), for the reason SchemaRejection.String gives: this
// is a REPORT ENTRY, and giving it an Error() method would let it be returned
// as a bare error and lose its Code and Paths on the way.
func (r ViewRejection) String() string {
	return fmt.Sprintf("%s: %s (%s)", r.Code, r.Reason, strings.Join(r.Paths, ", "))
}

// ViewLoadReport is everything the loader could not accept.
type ViewLoadReport struct {
	Rejections []ViewRejection
	// ScannedFiles is every candidate file the loader looked at, sorted.
	ScannedFiles []string
}

// OK reports whether every candidate view file loaded.
func (r *ViewLoadReport) OK() bool { return r == nil || len(r.Rejections) == 0 }

// RejectedNames lists the view names that failed to load, sorted.
func (r *ViewLoadReport) RejectedNames() []string {
	if r == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, rej := range r.Rejections {
		if rej.Name != "" {
			seen[rej.Name] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The model
// ---------------------------------------------------------------------------

// SavedView is one loaded view: the contract type, plus where it came from.
//
// Def is the generated wire type and is the whole of the view's content.
// SourcePath is not part of the format — it is provenance, named in every
// report about the view itself, so an operator is told which file to open.
type SavedView struct {
	Def        generated.ViewDef
	SourcePath string
}

// Name is the view's identifier.
func (v *SavedView) Name() string {
	if v == nil {
		return ""
	}
	return v.Def.Name
}

// DisplayLabel is the label if one was declared, otherwise the name — so no
// consumer has to invent its own fallback and no two consumers can invent
// different ones.
func (v *SavedView) DisplayLabel() string {
	if v == nil {
		return ""
	}
	if v.Def.Label != nil && strings.TrimSpace(*v.Def.Label) != "" {
		return *v.Def.Label
	}
	return v.Def.Name
}

// ---------------------------------------------------------------------------
// EffectiveParts — ONE SHAPE FOR TWO FILE FORMATS
//
// view-kinds-design-2026-09-03 §4 adds a `parts` stack to a view and keeps
// every view written before it loading unchanged. That is a back-compat rule,
// and back-compat rules are where a codebase grows two readers: one consumer
// writes `if v.Def.Parts == nil { … legacy … }`, the next writes a slightly
// different version of the same branch, and the two disagree the first time a
// legacy view carries a grouping.
//
// So the branch is written ONCE, here, and every downstream phase — the
// composer, the describe block, the renderer — consumes this accessor and
// never reads Def.Parts directly. The rule it implements is the design's, in
// full: an explicit stack is returned AS WRITTEN, and an absent one is one
// part derived from `layout` plus the view's own grouping and properties.
//
// A PART IS NEVER INVENTED FOR A LAYOUT THAT HAS NONE, and that is what the
// second return value is for. `map` is a declared LAYOUT (the importer must be
// able to record that an Obsidian view asked for one) and it is deliberately
// NOT a part — the design lists map among the things left out, for want of any
// coordinate data to draw. Synthesising a `table` for it would be FR-109's
// measured failure repeated exactly: a cards view that imported as a table,
// recorded no loss, and scored CLEAN. A caller that gets `false` must SAY the
// view has no drawable stack; it must not fall back to a table.
// ---------------------------------------------------------------------------

// EffectiveParts returns the part stack a renderer should walk, and reports
// whether there is one at all.
//
// ok is false only for a view whose `layout` names a rendering with no part
// equivalent (`map`) and which declares no explicit `parts` of its own. Every
// other view — including one that declares neither `layout` nor `parts` —
// yields at least one part.
func (v *SavedView) EffectiveParts() ([]generated.ViewPart, bool) {
	if v == nil {
		return nil, false
	}

	// An explicit stack is authoritative and is returned verbatim. The copy is
	// not ceremony: the slice header would otherwise alias Def.Parts, and a
	// caller appending to it would be writing into the loaded view.
	if v.Def.Parts != nil && len(*v.Def.Parts) > 0 {
		return append([]generated.ViewPart(nil), (*v.Def.Parts)...), true
	}

	// Omitted `layout` means `table`, which the contract states and which the
	// SPA has always assumed. Stating it here keeps the two agreeing.
	layout := generated.ViewDefLayoutTable
	if v.Def.Layout != nil {
		layout = *v.Def.Layout
	}
	part, ok := viewPartForLayout(layout)
	if !ok {
		return nil, false
	}

	synth := generated.ViewPart{Part: part}
	if v.Def.Grouping != nil && len(*v.Def.Grouping) > 0 {
		grouping := append([]generated.ViewGroupBy(nil), (*v.Def.Grouping)...)
		synth.Grouping = &grouping
	}
	if v.Def.Properties != nil && len(*v.Def.Properties) > 0 {
		props := append([]string(nil), (*v.Def.Properties)...)
		synth.Properties = &props
	}
	return []generated.ViewPart{synth}, true
}

// viewPartForLayout maps a legacy `layout` onto the part that draws it, and
// reports whether one exists.
//
// `cards` and `gallery` both map to `tiles`, and no information is lost by
// that: `layout` stays on the view as the record of what was actually asked
// for, so the narrower part vocabulary never becomes the only surviving
// answer. `map` has no part, deliberately — see the note above.
func viewPartForLayout(layout generated.ViewDefLayout) (generated.ViewPartPart, bool) {
	switch layout {
	case generated.ViewDefLayoutTable:
		return generated.ViewPartPartTable, true
	case generated.ViewDefLayoutCards, generated.ViewDefLayoutGallery:
		return generated.ViewPartPartTiles, true
	case generated.ViewDefLayoutBoard:
		return generated.ViewPartPartColumns, true
	case generated.ViewDefLayoutCalendar:
		return generated.ViewPartPartCalendar, true
	default:
		// `map`, and anything a later contract adds without adding a part.
		// Reached only through a layout ParseView already accepted as valid,
		// so this is the "no part exists" answer rather than a parse failure.
		return "", false
	}
}

// ViewSet is every saved view a vault declares.
type ViewSet struct {
	byName map[string]*SavedView
	order  []string
}

// NewViewSet returns an empty set. A vault with no views is an ordinary vault.
func NewViewSet() *ViewSet {
	return &ViewSet{byName: map[string]*SavedView{}}
}

// Get returns one view by its exact declared name.
//
// Lookup is EXACT, not folded. A view name is an identifier an agent supplies
// verbatim from a knowledge_describe response, and folding here would make
// "Open-Deals" and "open-deals" the same view while the two files sit side by
// side on disk being two different views.
func (s *ViewSet) Get(name string) (*SavedView, bool) {
	if s == nil {
		return nil, false
	}
	v, ok := s.byName[name]
	return v, ok
}

// Names lists the declared view names in load order, which is filename order
// and therefore stable across runs.
func (s *ViewSet) Names() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.order...)
}

// Views returns every loaded view, in the same order as Names.
func (s *ViewSet) Views() []*SavedView {
	if s == nil {
		return nil
	}
	out := make([]*SavedView, 0, len(s.order))
	for _, n := range s.order {
		out = append(out, s.byName[n])
	}
	return out
}

// Len is how many views loaded.
func (s *ViewSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.order)
}

func (s *ViewSet) add(v *SavedView) {
	if _, dup := s.byName[v.Def.Name]; !dup {
		s.order = append(s.order, v.Def.Name)
	}
	s.byName[v.Def.Name] = v
}

// ---------------------------------------------------------------------------
// Loading
// ---------------------------------------------------------------------------

// LoadViews reads every saved view in a vault.
//
// schemas may be nil, and the distinction is deliberate rather than a
// convenience: with a schema set, a view is additionally checked against it
// and a view naming a vanished type or property is REJECTED and reported;
// without one, only the view's own format is checked. A caller that has the
// schemas and passes nil gets a set of views that look fine and query nothing.
//
// A vault with no views directory is NOT an error. It is the ordinary state
// of every vault nobody has authored a view in, which is most of them.
func LoadViews(vaultRoot string, schemas *SchemaSet) (*ViewSet, *ViewLoadReport, error) {
	dir := ViewsDir(vaultRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return NewViewSet(), &ViewLoadReport{}, nil
		}
		return nil, nil, fmt.Errorf("reading views directory %s: %w", dir, err)
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		paths = append(paths, filepath.Join(dir, name))
	}
	sort.Strings(paths) // deterministic, so reports are reproducible

	return loadViewPaths(paths, schemas)
}

func loadViewPaths(paths []string, schemas *SchemaSet) (*ViewSet, *ViewLoadReport, error) {
	report := &ViewLoadReport{ScannedFiles: append([]string(nil), paths...)}
	parsed := make([]*SavedView, 0, len(paths))

	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			report.Rejections = append(report.Rejections, ViewRejection{
				Paths:  []string{p},
				Code:   RejectViewUnreadable,
				Reason: fmt.Sprintf("could not read the view file: %v", err),
			})
			continue
		}
		v, rej := ParseView(p, data)
		if rej != nil {
			report.Rejections = append(report.Rejections, *rej)
			continue
		}
		parsed = append(parsed, v)
	}

	// A duplicate NAME is the same conflict FR-003 describes for a duplicate
	// record type, and it gets the same answer: every member of the group is
	// rejected, all their paths named, because there is no basis for
	// preferring one and silently picking the alphabetically-first would make
	// which view runs depend on a filename.
	byName := map[string][]*SavedView{}
	nameOrder := []string{}
	for _, v := range parsed {
		if _, seen := byName[v.Def.Name]; !seen {
			nameOrder = append(nameOrder, v.Def.Name)
		}
		byName[v.Def.Name] = append(byName[v.Def.Name], v)
	}

	set := NewViewSet()
	for _, n := range nameOrder {
		group := byName[n]
		if len(group) > 1 {
			allPaths := make([]string, 0, len(group))
			for _, v := range group {
				allPaths = append(allPaths, v.SourcePath)
			}
			sort.Strings(allPaths)
			report.Rejections = append(report.Rejections, ViewRejection{
				Paths:  allPaths,
				Name:   n,
				Source: commonDeclaredSource(group),
				Code:   RejectViewDuplicateName,
				Reason: fmt.Sprintf(
					"view %q is declared in %d files (%s); all of them are rejected because there is no basis for preferring one — delete or rename all but one",
					n, len(group), strings.Join(allPaths, " and ")),
			})
			continue
		}
		v := group[0]
		if schemas != nil {
			if rej := ValidateViewAgainstSchemas(v, schemas); rej != nil {
				rej.Source = viewDeclaredSource(v)
				report.Rejections = append(report.Rejections, *rej)
				continue
			}
		}
		set.add(v)
	}

	sort.Slice(report.Rejections, func(i, j int) bool {
		if report.Rejections[i].Paths[0] != report.Rejections[j].Paths[0] {
			return report.Rejections[i].Paths[0] < report.Rejections[j].Paths[0]
		}
		return report.Rejections[i].Code < report.Rejections[j].Code
	})
	return set, report, nil
}

// viewDeclaredSource is a loaded view's own `source:`, or "" when it declares
// none. One spelling of the nil-check, so a caller attributing views to a base
// file never writes its own.
func viewDeclaredSource(v *SavedView) string {
	if v == nil || v.Def.Source == nil {
		return ""
	}
	return strings.TrimSpace(*v.Def.Source)
}

// DeclaredSource is the vault-relative path of the `.base` file this view was
// imported from, or "" for a view somebody authored directly.
func (v *SavedView) DeclaredSource() string { return viewDeclaredSource(v) }

// commonDeclaredSource is the source every member of a rejected group agrees
// on, or "" when they do not all agree.
//
// A duplicate-name conflict rejects SEVERAL files at once, and attributing the
// group to one member's base would let a caller counting "views from this base
// that failed to load" count a file that came from somewhere else. Where the
// members disagree the group is attributed to no base, which under-counts
// rather than mis-attributes — the direction that cannot mislead.
func commonDeclaredSource(group []*SavedView) string {
	if len(group) == 0 {
		return ""
	}
	first := viewDeclaredSource(group[0])
	for _, v := range group[1:] {
		if viewDeclaredSource(v) != first {
			return ""
		}
	}
	return first
}

// ParseView parses one view file's bytes. It returns either a view or a
// rejection, never both and never a bare error — every refusal carries a code
// and a path so a report can be assembled from it.
//
// It checks the FORMAT only. Whether the view's type and properties still
// exist is ValidateViewAgainstSchemas' question, because it needs the schemas
// and a caller may legitimately not have them (a format check during an
// import, for instance).
func ParseView(path string, data []byte) (*SavedView, *ViewRejection) {
	// Read out of the generic value below, and captured by the closure rather
	// than passed to it, so every rejection raised AFTER the file was read as
	// a mapping carries the base it came from — including the ones raised
	// before the strict decode produces a Def anybody could ask.
	var declaredSource string
	reject := func(code ViewRejectionCode, name, format string, args ...any) *ViewRejection {
		return &ViewRejection{
			Paths:  []string{path},
			Name:   name,
			Source: declaredSource,
			Code:   code,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, reject(RejectViewInvalidYAML, "", "the view file is not valid YAML: %v", err)
	}

	// An empty file decodes to a zero-Kind node with no error. Treat it as the
	// missing-version case rather than as broken YAML, so the operator is told
	// the one thing that is actually wrong with it.
	var raw any
	if node.Kind != 0 {
		if err := node.Decode(&raw); err != nil {
			return nil, reject(RejectViewInvalidYAML, "", "the view file is not valid YAML: %v", err)
		}
	}
	if raw == nil {
		return nil, reject(RejectViewEmpty, "",
			"the view file is empty; a view must at least declare a `name`, so this file is rejected and never applied")
	}
	top, ok := raw.(map[string]any)
	if !ok {
		return nil, reject(RejectViewInvalidYAML, "",
			"a view file must be a mapping of field name to value, found %T", raw)
	}

	// Read the name out of the generic value FIRST, so every message below can
	// name the view even when the strict decode is about to fail.
	declaredName, _ := top["name"].(string)
	declaredName = strings.TrimSpace(declaredName)
	rawSource, _ := top["source"].(string)
	declaredSource = strings.TrimSpace(rawSource)

	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, reject(RejectViewInvalidYAML, declaredName,
			"the view file holds a value that cannot be represented: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(jsonBytes)))
	dec.DisallowUnknownFields()
	var def generated.ViewDef
	if err := dec.Decode(&def); err != nil {
		// DisallowUnknownFields is what turns the contract's
		// `additionalProperties: false` into an enforced rule. yaml.v3 and
		// encoding/json both DROP an unknown key in silence otherwise, which
		// is how `group-by:` for `group_by:` becomes a view that loads, looks
		// right and groups by nothing.
		if strings.Contains(err.Error(), "unknown field") {
			return nil, reject(RejectViewUnknownKey, declaredName, "%v", cleanJSONFieldError(err))
		}
		return nil, reject(RejectViewInvalidYAML, declaredName, "%v", cleanJSONFieldError(err))
	}

	if strings.TrimSpace(def.Name) == "" {
		return nil, reject(RejectViewMissingName, "",
			"the view declares no `name`, so nothing can ask for it by name")
	}
	def.Name = strings.TrimSpace(def.Name)

	// `type` is OPTIONAL (spec FR-018b). An untyped view queries every note in
	// scope, resolving property names over the rows FR-021e keeps for every
	// note — which is what four of the founder's eighteen bases do, scoping
	// purely by folder and spanning record types.
	//
	// A `type:` that is PRESENT but blank is REFUSED. An empty string is not
	// "untyped" — it is a typo for a type name, and treating it as the
	// deliberate absence would turn a misspelling into a vault-wide query.
	if def.Type != nil {
		trimmedType := strings.TrimSpace(*def.Type)
		if trimmedType == "" {
			return nil, reject(RejectViewMissingType, def.Name,
				"view %q declares an empty `type`, which is a typo rather than a deliberate absence; omit the key entirely for an untyped view that spans every note in scope",
				def.Name)
		}
		def.Type = &trimmedType
	}

	v := &SavedView{Def: def, SourcePath: path}
	if rej := parseCheckShape(v, reject); rej != nil {
		return nil, rej
	}
	return v, nil
}

// parseCheckShape is the FORMAT half of a view: the checks that need no
// schema. Everything that needs one (property names, formula types) is
// ValidateViewAgainstSchemas'.
func parseCheckShape(
	v *SavedView,
	reject func(ViewRejectionCode, string, string, ...any) *ViewRejection,
) *ViewRejection {
	def := v.Def

	// `layout` is an enum the JSON decoder does NOT police — a bare string
	// type accepts any string. Left unchecked, `layout: card` (singular) would
	// load, mean nothing to the SPA and render as the default table: exactly
	// the silently-flattened cards view FR-109 was written after.
	if def.Layout != nil && !def.Layout.Valid() {
		return reject(RejectViewInvalidLayout, def.Name,
			"view %q asks for layout %q, which is not one of the declared layouts; permitted: %s",
			def.Name, string(*def.Layout), strings.Join(viewLayoutNames(), ", "))
	}

	// `kind` and `parts` are the view-kinds addition (design §4), and both are
	// bare strings to the JSON decoder for exactly the reason `layout` is:
	// the contract's enum is not enforced by the decode, so an unrecognised
	// value arrives as a perfectly well-formed view.
	if def.Kind != nil && !def.Kind.Valid() {
		return reject(RejectViewInvalidKind, def.Name,
			"view %q declares kind %q, which is not one of the declared view kinds; permitted: %s",
			def.Name, string(*def.Kind), strings.Join(viewKindNames(), ", "))
	}
	if def.Parts != nil {
		for i, part := range *def.Parts {
			if rej := checkViewPartShape(def.Name, i, part, reject); rej != nil {
				return rej
			}
		}
	}

	// FR-023c's bound applies to a view's tree identically to a request's.
	// Measured HERE rather than left to the query path because a view is
	// written once and evaluated forever: a tree that will be refused on every
	// query should be refused when it is loaded, naming which bound it broke.
	if def.Filter != nil {
		leaves, depth, rej := measureViewFilterTree(*def.Filter, 1)
		if rej != "" {
			return reject(RejectViewInvalidFilterNode, def.Name,
				"view %q has a filter node that %s", def.Name, rej)
		}
		if leaves > maxViewFilterLeaves {
			return reject(RejectViewFilterTooLarge, def.Name,
				"view %q has a filter tree of %d leaves; FR-023c caps a filter at %d",
				def.Name, leaves, maxViewFilterLeaves)
		}
		if depth > maxViewFilterDepth {
			return reject(RejectViewFilterTooLarge, def.Name,
				"view %q has a filter tree %d levels deep; FR-023c caps a filter at %d",
				def.Name, depth, maxViewFilterDepth)
		}
	}
	return nil
}

// checkViewPartShape is the FORMAT half of one entry in a view's part stack
// (view-kinds-design-2026-09-03 §4). Whether the properties it names still
// exist is ValidateViewAgainstSchemas', exactly as it is for every other
// property position on a view.
//
// EVERY CHECK HERE EXISTS BECAUSE THE DECODE DOES NOT MAKE IT. `part` and
// `aggregate` are Go string types, so any string decodes; a `minLength: 1` in
// the contract is a JSON Schema assertion nothing on this path evaluates. Left
// unchecked, `part: figure` (singular) and `number: ""` both load, and both
// produce a stack element that draws nothing while the view reports itself
// fine.
func checkViewPartShape(
	viewName string,
	index int,
	part generated.ViewPart,
	reject func(ViewRejectionCode, string, string, ...any) *ViewRejection,
) *ViewRejection {
	where := fmt.Sprintf("parts[%d]", index)

	if !part.Part.Valid() {
		return reject(RejectViewInvalidPart, viewName,
			"view %q declares %s as part %q, which is not one of the declared parts; permitted: %s",
			viewName, where, string(part.Part), strings.Join(viewPartNames(), ", "))
	}

	// The bindings, in the order the design lists them, so a reader comparing
	// this against §4 reads down both at once. A binding is OPTIONAL — which
	// of them a given part requires is the composer's gate G1, not the
	// loader's — but a binding that is PRESENT and blank is refused: the
	// author wrote a key, and a key that means nothing must never be accepted
	// in silence.
	for _, b := range []struct {
		key   string
		value *string
	}{
		{"number", part.Number},
		{"unit", part.Unit},
		{"date", part.Date},
		{"image", part.Image},
		{"choice", part.Choice},
	} {
		if b.value != nil && strings.TrimSpace(*b.value) == "" {
			return reject(RejectViewInvalidPart, viewName,
				"view %q declares %s with a blank `%s`; a binding names a property, so omit the key entirely rather than leaving it empty",
				viewName, where, b.key)
		}
	}

	if part.Aggregate != nil && !part.Aggregate.Valid() {
		return reject(RejectViewInvalidPart, viewName,
			"view %q declares %s with aggregate %q, which is not one of the declared reductions; permitted: %s",
			viewName, where, string(*part.Aggregate), strings.Join(viewPartAggregateNames(), ", "))
	}

	// The subtotal map is walked in SORTED key order, not map order: a map
	// walk would report a different one of two bad entries on every run, and
	// an operator fixing the file would be chasing a moving refusal.
	if part.Subtotals != nil {
		keys := make([]string, 0, len(*part.Subtotals))
		for k := range *part.Subtotals {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if strings.TrimSpace(k) == "" {
				return reject(RejectViewInvalidPart, viewName,
					"view %q declares %s with a blank `subtotals` key; a subtotal is keyed by a property name",
					viewName, where)
			}
			if op := (*part.Subtotals)[k]; !op.Valid() {
				return reject(RejectViewInvalidPart, viewName,
					"view %q declares %s with subtotal %q = %q, which is not one of the declared reductions; permitted: %s",
					viewName, where, k, string(op), strings.Join(viewPartAggregateNames(), ", "))
			}
		}
	}

	if part.Grouping != nil {
		for gi, g := range *part.Grouping {
			if strings.TrimSpace(g.Property) == "" {
				return reject(RejectViewInvalidPart, viewName,
					"view %q declares %s.grouping[%d] with a blank `property`; a grouping key names a property",
					viewName, where, gi)
			}
			if g.Direction != nil && !g.Direction.Valid() {
				return reject(RejectViewInvalidPart, viewName,
					"view %q declares %s.grouping[%d] in direction %q, which is not a declared direction; permitted: asc, desc",
					viewName, where, gi, string(*g.Direction))
			}
		}
	}

	if part.Properties != nil {
		for pi, p := range *part.Properties {
			if strings.TrimSpace(p) == "" {
				return reject(RejectViewInvalidPart, viewName,
					"view %q declares %s.properties[%d] as a blank name; a column names a property",
					viewName, where, pi)
			}
		}
	}
	return nil
}

// FR-023c's two numbers, named so a reader can see which refusal cites which.
const (
	maxViewFilterLeaves = 64
	maxViewFilterDepth  = 8
)

// measureViewFilterTree returns the leaf count and the maximum depth of a v2
// filter tree, or a non-empty complaint for a node that is neither one form
// nor the other.
//
// It counts what FR-023c's bound is stated over: LEAVES, not nodes. The
// combinators are free — a tree's cost is the comparisons it performs, and a
// nested `all` performs none.
//
// It recurses, and the recursion is safe because depth is carried and the
// caller refuses above maxViewFilterDepth — but a hand-written file could
// nest thousands of levels before any bound is consulted, so the walk stops
// dead at a hard ceiling well above the real one rather than growing the
// stack on the way to reporting a number nobody needs precisely.
func measureViewFilterTree(n generated.VaultFilterNode, depth int) (leaves, maxDepth int, complaint string) {
	if depth > viewFilterWalkCeiling {
		return 0, depth, fmt.Sprintf("nests deeper than %d levels, which is past any bound this release will evaluate", viewFilterWalkCeiling)
	}
	forms := 0
	if n.All != nil {
		forms++
	}
	if n.Any != nil {
		forms++
	}
	if n.Not != nil {
		forms++
	}
	isLeaf := n.Property != nil || n.Op != nil || n.Value != nil || n.Values != nil
	if isLeaf {
		forms++
	}
	switch {
	case forms == 0:
		return 0, depth, "is empty: it names no property and no all/any/not"
	case forms > 1:
		return 0, depth, "sets more than one of {property…}, all, any and not; a node is a leaf or one combinator, never a mixture"
	}
	if isLeaf {
		return 1, depth, ""
	}
	children := []generated.VaultFilterNode{}
	switch {
	case n.All != nil:
		children = *n.All
	case n.Any != nil:
		children = *n.Any
	case n.Not != nil:
		children = []generated.VaultFilterNode{*n.Not}
	}
	if len(children) == 0 {
		return 0, depth, "is an all/any combinator with no children, which asserts nothing"
	}
	maxDepth = depth
	for _, c := range children {
		l, d, comp := measureViewFilterTree(c, depth+1)
		if comp != "" {
			return 0, d, comp
		}
		leaves += l
		if d > maxDepth {
			maxDepth = d
		}
	}
	return leaves, maxDepth, ""
}

// viewFilterWalkCeiling stops the measuring walk before the stack does. It is
// far above FR-023c's real bound of 8 on purpose: the refusal an operator
// should see is "8 levels", and this ceiling only exists so a pathological
// file cannot turn a load into a crash on the way to saying so.
const viewFilterWalkCeiling = 512

func viewLayoutNames() []string {
	return []string{
		string(generated.ViewDefLayoutTable),
		string(generated.ViewDefLayoutCards),
		string(generated.ViewDefLayoutBoard),
		string(generated.ViewDefLayoutCalendar),
		string(generated.ViewDefLayoutGallery),
		string(generated.ViewDefLayoutMap),
	}
}

// viewKindNames lists the eight view kinds in the order the design's table
// lists them (§2.3), so a refusal reads in the same order as the document the
// author was working from.
func viewKindNames() []string {
	return []string{
		string(generated.ViewDefKindTable),
		string(generated.ViewDefKindList),
		string(generated.ViewDefKindTiles),
		string(generated.ViewDefKindBoard),
		string(generated.ViewDefKindCalendar),
		string(generated.ViewDefKindSummary),
		string(generated.ViewDefKindTrend),
		string(generated.ViewDefKindBreakdown),
	}
}

// viewPartNames lists the eight parts in the design's own order (§2.2).
func viewPartNames() []string {
	return []string{
		string(generated.ViewPartPartTable),
		string(generated.ViewPartPartList),
		string(generated.ViewPartPartTiles),
		string(generated.ViewPartPartColumns),
		string(generated.ViewPartPartCalendar),
		string(generated.ViewPartPartFigures),
		string(generated.ViewPartPartChart),
		string(generated.ViewPartPartCrosstab),
	}
}

func viewPartAggregateNames() []string {
	return []string{
		string(generated.ViewPartAggregateSum),
		string(generated.ViewPartAggregateAvg),
		string(generated.ViewPartAggregateMin),
		string(generated.ViewPartAggregateMax),
		string(generated.ViewPartAggregateCount),
	}
}

// cleanJSONFieldError rewrites encoding/json's wording into the vocabulary an
// operator is looking at. They are reading a YAML file; "json: unknown field"
// sends them looking for JSON.
func cleanJSONFieldError(err error) error {
	msg := err.Error()
	msg = strings.ReplaceAll(msg, "json: unknown field ", "the view file declares a field this release does not know: ")
	msg = strings.ReplaceAll(msg, "json: ", "")
	return errors.New(msg)
}

// ---------------------------------------------------------------------------
// Validation against the schemas
// ---------------------------------------------------------------------------

// ValidateViewAgainstSchemas checks that every name a view mentions is still
// declared, and returns a rejection naming the valid alternatives when one is
// not — FR-024's pattern, applied to a view instead of a query.
//
// It is exported because knowledge_configure needs exactly this check BEFORE it
// writes (the ViewDef schema: "A view naming a property or enum value that does not
// exist is REJECTED at write time, not stored and discovered broken later"),
// and a second implementation of the same rule would eventually disagree with
// this one about some edge.
//
// It returns the FIRST fault it finds rather than all of them. That is a
// deliberate narrowing: a view whose type vanished has every property fault as
// a consequence, and reporting forty derived faults buries the one real cause.
func ValidateViewAgainstSchemas(v *SavedView, schemas *SchemaSet) *ViewRejection {
	if v == nil || schemas == nil {
		return nil
	}
	reject := func(code ViewRejectionCode, format string, args ...any) *ViewRejection {
		return &ViewRejection{
			Paths:  []string{v.SourcePath},
			Name:   v.Def.Name,
			Code:   code,
			Reason: fmt.Sprintf(format, args...),
		}
	}

	// THE TYPE IS OPTIONAL (FR-018b). ParseView has already refused a `type:`
	// that is present but blank, so a nil here is a deliberately UNTYPED
	// view: it queries every note in scope and resolves property names over
	// the rows FR-021e keeps for every note, which means there is no single
	// schema to check its ordinary property names against. Those names are
	// therefore NOT checked — deliberately, and it is not a gap: a name no
	// in-scope type declares resolves in the text domain at query time, so
	// there is no name this loader could refuse without refusing a query
	// FR-018b requires to work.
	//
	// A declared type holding ZERO records is a valid, empty view (FR-018d) —
	// which is a fact about the INDEX, not the schema set, so nothing here
	// distinguishes it. RejectViewUnknownType below still fires for a type NO
	// schema declares: that is drift, not provisioning.
	var base *Schema
	if v.Def.Type != nil {
		declaredType := *v.Def.Type
		found, ok := schemas.Get(declaredType)
		if !ok {
			return reject(RejectViewUnknownType,
				"view %q queries record type %q, which this vault does not declare; declared types: %s",
				v.Def.Name, declaredType, joinOrNone(schemas.Types()))
		}
		base = found
	}

	// The formulas are validated FIRST, because every later property position
	// may reference one as `formula.<name>` and a reference can only be
	// checked against a set that is known good. FR-140: the parser lives in
	// the write path, and the loader re-validates so a hand-edited file is
	// re-checked.
	formulas, rej := validateViewFormulas(v, base, reject)
	if rej != nil {
		return rej
	}

	// Every place a property name can appear, checked against the type it
	// belongs to. `via` moves the check to the related type, which is the
	// whole point of the field.
	checkProp := func(sc *Schema, name, where string) *ViewRejection {
		if _, found := sc.Property(name); found {
			return nil
		}
		return reject(RejectViewUnknownProperty,
			"view %q names property %q in %s, which record type %q does not declare; declared: %s",
			v.Def.Name, name, where, sc.Type, joinOrNone(sc.PropertyNames()))
	}

	// checkViewProp is the property-position checker for every position a view
	// can name one: it resolves the reserved namespaces (FR-018c, `formula.`
	// and `file.`) before falling back to the record type's own declarations,
	// and it resolves BY NAME at query time on an untyped view (base == nil),
	// where there is no schema to check against and nothing to refuse.
	//
	// `comparison` distinguishes a COMPARISON position (filter, sort,
	// grouping, aggregate) from a DISPLAY one (`properties`). `file.file` is
	// the whole note, renderable but not comparable (FR-130), so it is legal
	// in one and refused in the other.
	checkViewProp := func(name, where string, comparison bool) *ViewRejection {
		switch {
		case isViewFormulaRef(name):
			ref := strings.TrimPrefix(name, viewFormulaNamespace)
			if formulas != nil {
				if _, ok := formulas.Get(ref); ok {
					return nil
				}
			}
			return reject(RejectViewUnknownFormula,
				"view %q names %q in %s, but declares no formula %q; declared formulas: %s",
				v.Def.Name, name, where, ref, joinOrNone(viewFormulaNames(v)))
		case IsFileNamespace(name):
			if !IsFileProperty(name) {
				return reject(RejectViewUnknownProperty,
					"view %q names %q in %s, which is not one of the reserved file properties; permitted: %s",
					v.Def.Name, name, where, strings.Join(FilePropertyNames, ", "))
			}
			if comparison && name == FileSelfProp {
				return reject(RejectViewUnknownProperty,
					"view %q names %q in %s, but %q is the note itself and is not a comparison target; permitted here: %s",
					v.Def.Name, name, where, FileSelfProp, strings.Join(FileFilterablePropertyNames, ", "))
			}
			return nil
		case base == nil:
			// Untyped view: resolved by name at query time (FR-018b). There is
			// nothing to check and nothing to refuse.
			return nil
		default:
			return checkProp(base, name, where)
		}
	}

	// checkViewEnumLiteral is the ViewDef contract's SECOND half — "a view
	// naming a property OR ENUM VALUE that does not exist is REJECTED at write
	// time". Only the first half survived the flat format's deletion; the
	// literal check went with it, because it had only ever been wired to the
	// retired flat list.
	//
	// AN UNDECLARED ENUM LITERAL CANNOT MATCH ANY CONFORMING RECORD, and that
	// is why it is a rejection rather than a warning: `state = "Closed Won"`
	// against a declared [draft, shipped, withdrawn] selects nothing, `<>`
	// selects everything, and neither is distinguishable from a correct
	// answer by anyone reading the response.
	//
	// Property.ResolveEnum is the membership oracle, and it must be: it is the
	// SAME one value.go::parseEnumValueNode asks at query time. A second
	// implementation would agree on the easy cases and disagree on FR-011a's
	// full-Unicode fold, at which point a view refused here would be served
	// there, or the reverse.
	//
	// WHAT IS DELIBERATELY LEFT ALONE:
	//
	//   - `LIKE` carries a PATTERN, not a value. filter.go passes it through as
	//     text for exactly this reason — `ship%` is not a declared value and
	//     never will be, and refusing it would refuse a legitimate query.
	//   - `IS NULL`/`IS NOT NULL` carry no literal at all, so there is nothing
	//     to check.
	//   - Every NON-enum literal. A date, an integer or a decimal is checked by
	//     value.go's parser at query time against bound and format rules that
	//     live there; re-deriving them here would be a second implementation of
	//     the one thing this package already has exactly one of. An enum is
	//     different in kind: its permitted set is CLOSED and declared, so the
	//     answer is knowable from the schema alone.
	//   - An untyped view (base == nil). FR-018b resolves its property names by
	//     name at query time, so there is no declared set to compare against.
	checkViewEnumLiteral := func(n generated.VaultFilterNode, where string) *ViewRejection {
		if base == nil || n.Property == nil {
			return nil
		}
		name := *n.Property
		if isViewFormulaRef(name) || IsFileNamespace(name) {
			return nil
		}
		prop, found := base.Property(name)
		// A missing property has already been refused by checkViewProp on the
		// same node; a property declaring no values has no set to check
		// against, which mirrors the comparator's own asymmetry (R-1).
		if !found || prop.Type != TypeEnum || len(prop.Values) == 0 {
			return nil
		}
		if n.Op != nil && Operator(*n.Op) == OpLike {
			return nil
		}
		literals := make([]string, 0, 1)
		if n.Value != nil {
			literals = append(literals, *n.Value)
		}
		if n.Values != nil {
			literals = append(literals, *n.Values...)
		}
		for _, lit := range literals {
			if _, declared := prop.ResolveEnum(lit); declared {
				continue
			}
			return reject(RejectViewUnknownEnumValue,
				"view %q compares %s.%s against %q in %s, which is not one of its declared values (matching ignores case); permitted: %s",
				v.Def.Name, base.Type, name, lit, where, joinOrNone(prop.PermittedValues()))
		}
		return nil
	}

	if v.Def.Filter != nil {
		if rej := checkViewFilterTree(*v.Def.Filter, "filter", checkViewProp, checkViewEnumLiteral); rej != nil {
			return rej
		}
	}
	if v.Def.Grouping != nil {
		for _, g := range *v.Def.Grouping {
			if rej := checkViewProp(g.Property, "grouping", true); rej != nil {
				return rej
			}
			if g.Direction != nil && !g.Direction.Valid() {
				return reject(RejectViewUnknownProperty,
					"view %q groups by %q in direction %q, which is not a declared direction; permitted: asc, desc",
					v.Def.Name, g.Property, string(*g.Direction))
			}
		}
	}
	if v.Def.Sort != nil {
		for _, srt := range *v.Def.Sort {
			if rej := checkViewProp(srt.Property, "sort", true); rej != nil {
				return rej
			}
		}
	}
	for _, p := range derefStrings(v.Def.Properties) {
		if rej := checkViewProp(p, "properties", false); rej != nil {
			return rej
		}
	}
	if v.Def.Aggregates != nil {
		for _, a := range *v.Def.Aggregates {
			if a.Property == nil || strings.TrimSpace(*a.Property) == "" {
				// count takes no property, and the contract says so. A missing
				// property on sum/min/max is the WRITER's fault to refuse; the
				// reader has nothing to check it against and must not invent a
				// property name to complain about.
				continue
			}
			if rej := checkViewProp(*a.Property, "aggregates", true); rej != nil {
				return rej
			}
		}
	}

	// A part's bindings and columns are property positions like any other, and
	// they are checked like any other. A part is the ONE place a property name
	// could otherwise reach a renderer unchecked — every position above
	// predates the part stack — and a `number: amount` binding on a type that
	// no longer declares `amount` draws a headline figure over nothing.
	if v.Def.Parts != nil {
		for i, part := range *v.Def.Parts {
			where := fmt.Sprintf("parts[%d]", i)
			for _, b := range []struct {
				key        string
				value      *string
				comparison bool
			}{
				{"number", part.Number, true},
				{"unit", part.Unit, true},
				{"date", part.Date, true},
				{"image", part.Image, false},
				{"choice", part.Choice, true},
			} {
				if b.value == nil {
					continue
				}
				if rej := checkViewProp(*b.value, where+"."+b.key, b.comparison); rej != nil {
					return rej
				}
			}
			if part.Grouping != nil {
				for gi, g := range *part.Grouping {
					if rej := checkViewProp(g.Property, fmt.Sprintf("%s.grouping[%d]", where, gi), true); rej != nil {
						return rej
					}
				}
			}
			if part.Subtotals != nil {
				names := make([]string, 0, len(*part.Subtotals))
				for name := range *part.Subtotals {
					names = append(names, name)
				}
				sort.Strings(names) // deterministic: a map walk would report a random one
				for _, name := range names {
					if rej := checkViewProp(name, where+".subtotals", true); rej != nil {
						return rej
					}
				}
			}
			if part.Properties != nil {
				for _, p := range *part.Properties {
					if rej := checkViewProp(p, where+".properties", false); rej != nil {
						return rej
					}
				}
			}
		}
	}

	// `property_config` is PURE PRESENTATION and the engine never reads it
	// (FR-018b, keeping Obsidian's own rule verbatim). Its keys are still
	// property names, and a config entry for a property that does not exist is
	// a column heading nothing will ever render — checked in the DISPLAY
	// position, so `file.file` is legal here for the same reason it is legal
	// in `properties`.
	if v.Def.PropertyConfig != nil {
		names := make([]string, 0, len(*v.Def.PropertyConfig))
		for name := range *v.Def.PropertyConfig {
			names = append(names, name)
		}
		sort.Strings(names) // deterministic: a map walk would report a random one
		for _, name := range names {
			if rej := checkViewProp(name, "property_config", false); rej != nil {
				return rej
			}
		}
	}
	return nil
}

// viewFormulaNamespace is the reserved prefix a query writes to reach a
// formula (FR-018c/FR-140): `formula.<name>` in any property position.
const viewFormulaNamespace = "formula."

func isViewFormulaRef(name string) bool { return strings.HasPrefix(name, viewFormulaNamespace) }

func viewFormulaNames(v *SavedView) []string {
	if v == nil || v.Def.Formulas == nil {
		return nil
	}
	out := make([]string, 0, len(*v.Def.Formulas))
	for n := range *v.Def.Formulas {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// validateViewFormulas re-checks a view's formulas on LOAD.
//
// FR-140 puts the parser in the write path; this is the re-check that makes a
// hand-edited file safe, and it is the same entry point knowledge_configure
// calls before writing — one implementation, so the two can never disagree
// about whether an expression is legal.
//
// A nil schema (an untyped view) is passed through unchanged: SchemaFormulaEnv
// documents nil as the typeless case and refuses a formula naming a record
// property with a message that says the view declares no type. Formulas over
// `file.*` and literals still type cleanly, which is exactly the set that
// does not depend on a schema.
//
// EVERY refusal is reported, not the first. ValidateViewAgainstSchemas returns
// one rejection, so they are joined into it — an author fixing four formulas
// one load at a time is an author who stops using formulas.
func validateViewFormulas(
	v *SavedView,
	schema *Schema,
	reject func(ViewRejectionCode, string, ...any) *ViewRejection,
) (*FormulaSet, *ViewRejection) {
	if v.Def.Formulas == nil || len(*v.Def.Formulas) == 0 {
		return nil, nil
	}
	set, errs := ValidateFormulaSet(*v.Def.Formulas, schema)
	if len(errs) == 0 {
		return set, nil
	}
	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	sort.Strings(msgs)
	return nil, reject(RejectViewInvalidFormula,
		"view %q declares %d formula(s) this release refuses: %s",
		v.Def.Name, len(errs), strings.Join(msgs, "; "))
}

// checkViewFilterTree walks a filter tree and checks every LEAF: first its
// property NAME, then — once the name is known good — its enum LITERALS.
//
// The order is not incidental. A literal can only be judged against a property
// that exists, so the name check has to answer first; running them the other
// way round would report "not one of its declared values" for a property that
// has no declared values because it has no declaration at all.
//
// It walks the tree rather than the leaves-in-isolation because the position
// reported to the operator has to name where in the tree the fault is; a bare
// "unknown property" over a 12-leaf disjunction sends them reading the whole
// file. The path is built as `filter.any[2].all[0]`, which is the shape they
// are looking at.
func checkViewFilterTree(
	n generated.VaultFilterNode,
	path string,
	checkName func(name, where string, comparison bool) *ViewRejection,
	checkLiteral func(n generated.VaultFilterNode, where string) *ViewRejection,
) *ViewRejection {
	if n.Property != nil {
		if rej := checkName(*n.Property, path, true); rej != nil {
			return rej
		}
		return checkLiteral(n, path)
	}
	descend := func(kind string, children []generated.VaultFilterNode) *ViewRejection {
		for i, c := range children {
			if rej := checkViewFilterTree(c, fmt.Sprintf("%s.%s[%d]", path, kind, i), checkName, checkLiteral); rej != nil {
				return rej
			}
		}
		return nil
	}
	switch {
	case n.All != nil:
		return descend("all", *n.All)
	case n.Any != nil:
		return descend("any", *n.Any)
	case n.Not != nil:
		return checkViewFilterTree(*n.Not, path+".not", checkName, checkLiteral)
	}
	return nil
}

// derefStrings reads an optional generated string list without every caller
// writing the same nil check.
func derefStrings(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

// joinOrNone renders a list of valid names for a refusal. An EMPTY list says
// so in words rather than trailing off after a colon: "declared: " with
// nothing after it reads as a truncated message, and a reader cannot tell
// whether the list was empty or the message was cut.
func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
