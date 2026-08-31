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
)

// ViewRejection is one refused view file.
type ViewRejection struct {
	// Paths names every file involved. A duplicate-name conflict holds both,
	// for the reason FR-003 gives for schemas: a rejection that named only
	// the second leaves an operator hunting for the first.
	Paths []string
	// Name is the declared view name where one was readable.
	Name   string
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
				Paths: allPaths,
				Name:  n,
				Code:  RejectViewDuplicateName,
				Reason: fmt.Sprintf(
					"view %q is declared in %d files (%s); all of them are rejected because there is no basis for preferring one — delete or rename all but one",
					n, len(group), strings.Join(allPaths, " and ")),
			})
			continue
		}
		v := group[0]
		if schemas != nil {
			if rej := ValidateViewAgainstSchemas(v, schemas); rej != nil {
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

// ParseView parses one view file's bytes. It returns either a view or a
// rejection, never both and never a bare error — every refusal carries a code
// and a path so a report can be assembled from it.
//
// It checks the FORMAT only. Whether the view's type and properties still
// exist is ValidateViewAgainstSchemas' question, because it needs the schemas
// and a caller may legitimately not have them (a format check during an
// import, for instance).
func ParseView(path string, data []byte) (*SavedView, *ViewRejection) {
	reject := func(code ViewRejectionCode, name, format string, args ...any) *ViewRejection {
		return &ViewRejection{
			Paths:  []string{path},
			Name:   name,
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

	if v.Def.Filter != nil {
		if rej := checkViewFilterTreeProperties(*v.Def.Filter, "filter", checkViewProp); rej != nil {
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

// checkViewFilterTreeProperties walks a version-2 filter tree and checks every
// LEAF's property name.
//
// It walks the tree rather than the leaves-in-isolation because the position
// reported to the operator has to name where in the tree the fault is; a bare
// "unknown property" over a 12-leaf disjunction sends them reading the whole
// file. The path is built as `filter.any[2].all[0]`, which is the shape they
// are looking at.
func checkViewFilterTreeProperties(
	n generated.VaultFilterNode,
	path string,
	check func(name, where string, comparison bool) *ViewRejection,
) *ViewRejection {
	if n.Property != nil {
		return check(*n.Property, path, true)
	}
	descend := func(kind string, children []generated.VaultFilterNode) *ViewRejection {
		for i, c := range children {
			if rej := checkViewFilterTreeProperties(c, fmt.Sprintf("%s.%s[%d]", path, kind, i), check); rej != nil {
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
		return checkViewFilterTreeProperties(*n.Not, path+".not", check)
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
