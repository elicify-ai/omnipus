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
// contract (contracts/components/schemas/ViewDef.yaml), which is the single
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
// ViewDef.yaml via oapi-codegen and is the only legal cross-boundary type for
// a view (Hard Constraint #8). There is deliberately no parallel
// hand-written struct here: the persisted YAML is a wire format the SPA reads,
// so a second shape would be exactly the drift the constraint exists to stop.
//
// WHY YAML IS DECODED THROUGH JSON. The generated type carries `json:` tags
// and no `yaml:` tags, because it was generated from a JSON-Schema contract.
// yaml.v3 would lower-case the Go field names and silently miss `group_by`,
// `schema_version` and every other multi-word key — producing a view that
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

	// SupportedViewVersion is the only view schema_version this release
	// understands. Mandatory for the same reason a record schema's is
	// (ADR-068 D2): these files are machine-generated, and a format that
	// changes unannounced is worse when nobody typed it.
	SupportedViewVersion = 1
)

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
	RejectViewUnreadable         ViewRejectionCode = "view_unreadable"
	RejectViewInvalidYAML        ViewRejectionCode = "view_invalid_yaml"
	RejectViewMissingVersion     ViewRejectionCode = "view_missing_version"
	RejectViewUnsupportedVersion ViewRejectionCode = "view_unsupported_version"
	RejectViewMissingName        ViewRejectionCode = "view_missing_name"
	RejectViewMissingType        ViewRejectionCode = "view_missing_type"
	RejectViewDuplicateName      ViewRejectionCode = "view_duplicate_name"
	RejectViewUnknownKey         ViewRejectionCode = "view_unknown_key"
	// RejectViewUnknownType is a view naming a record type the vault does not
	// declare. Reported rather than dropped: a view that queries a type
	// somebody deleted returns nothing, and "nothing" is indistinguishable
	// from "no matching records" — the silent-empty-result failure FR-024
	// exists to remove, arriving through the view instead of the query.
	RejectViewUnknownType ViewRejectionCode = "view_unknown_type"
	// RejectViewUnknownProperty is a view naming a property the type does not
	// declare — in a filter, a group_by, a sort, a select or an aggregate.
	RejectViewUnknownProperty ViewRejectionCode = "view_unknown_property"
	// RejectViewUnknownEnumValue is a filter literal that is not a member of
	// the enum it is compared against.
	RejectViewUnknownEnumValue ViewRejectionCode = "view_unknown_enum_value"
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
		return nil, reject(RejectViewMissingVersion, "",
			"schema_version is missing; it is mandatory from the first release (ADR-068 D2), so this view is rejected and never applied")
	}
	top, ok := raw.(map[string]any)
	if !ok {
		return nil, reject(RejectViewInvalidYAML, "",
			"a view file must be a mapping of field name to value, found %T", raw)
	}

	// Read name and version out of the generic value FIRST, so every message
	// below can name the view even when the strict decode is about to fail.
	declaredName, _ := top["name"].(string)
	declaredName = strings.TrimSpace(declaredName)

	rawVersion, hasVersion := top["schema_version"]
	if !hasVersion || rawVersion == nil {
		return nil, reject(RejectViewMissingVersion, declaredName,
			"schema_version is missing; it is mandatory from the first release (ADR-068 D2), so this view is rejected and never applied")
	}
	version, versionOK := rawVersion.(int)
	if !versionOK {
		return nil, reject(RejectViewUnsupportedVersion, declaredName,
			"schema_version must be a whole number, found %v", rawVersion)
	}
	if version != SupportedViewVersion {
		return nil, reject(RejectViewUnsupportedVersion, declaredName,
			"schema_version is %d; this release understands version %d only", version, SupportedViewVersion)
	}

	// The strict decode happens AFTER the version checks, deliberately: a key
	// this release does not know inside a schema_version it does not know is
	// an unsupported VERSION, and reporting "unknown key" would send the
	// operator to fix the wrong thing. Same ordering, and the same reason, as
	// ParseSchema.
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
	if strings.TrimSpace(def.Type) == "" {
		return nil, reject(RejectViewMissingType, def.Name,
			"view %q declares no `type`, so there is no record type for it to query", def.Name)
	}
	def.Name = strings.TrimSpace(def.Name)
	def.Type = strings.TrimSpace(def.Type)

	return &SavedView{Def: def, SourcePath: path}, nil
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
// writes (ViewDef.yaml: "A view naming a property or enum value that does not
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

	base, ok := schemas.Get(v.Def.Type)
	if !ok {
		return reject(RejectViewUnknownType,
			"view %q queries record type %q, which this vault does not declare; declared types: %s",
			v.Def.Name, v.Def.Type, joinOrNone(schemas.Types()))
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

	if v.Def.Filters != nil {
		for i, f := range *v.Def.Filters {
			sc := base
			for _, hop := range derefStrings(f.Via) {
				prop, found := sc.Property(hop)
				if !found {
					return reject(RejectViewUnknownProperty,
						"view %q follows relation %q in filter %d, which record type %q does not declare; declared: %s",
						v.Def.Name, hop, i+1, sc.Type, joinOrNone(sc.PropertyNames()))
				}
				if prop.Type != TypeRelation && prop.Type != TypePerson {
					return reject(RejectViewUnknownProperty,
						"view %q follows %q in filter %d as a relation, but %q declares it as %s; only relation and person properties can be followed",
						v.Def.Name, hop, i+1, sc.Type, prop.Type)
				}
				next, found := schemas.Get(prop.To)
				if !found {
					return reject(RejectViewUnknownType,
						"view %q follows relation %q in filter %d to record type %q, which this vault does not declare; declared types: %s",
						v.Def.Name, hop, i+1, prop.To, joinOrNone(schemas.Types()))
				}
				sc = next
			}
			if rej := checkProp(sc, f.Property, fmt.Sprintf("filter %d", i+1)); rej != nil {
				return rej
			}
			if rej := validateViewFilterValues(v, sc, f, i+1, reject); rej != nil {
				return rej
			}
		}
	}
	for _, g := range derefStrings(v.Def.GroupBy) {
		if rej := checkProp(base, g, "group_by"); rej != nil {
			return rej
		}
	}
	if v.Def.Sort != nil {
		for _, s := range *v.Def.Sort {
			if rej := checkProp(base, s.Property, "sort"); rej != nil {
				return rej
			}
		}
	}
	for _, p := range derefStrings(v.Def.Properties) {
		if rej := checkProp(base, p, "properties"); rej != nil {
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
			if rej := checkProp(base, *a.Property, "aggregates"); rej != nil {
				return rej
			}
		}
	}
	return nil
}

// validateViewFilterValues checks an enum literal against the declared set.
//
// Only enums are checked, and that is the honest boundary: an enum has a
// CLOSED declared set this file can compare against, so a wrong value is
// knowable here. Every other type's literal is checked by the comparator at
// query time against rules (dates, integer bounds, decimal scale) that live in
// filter.go and value.go, and re-deriving them here would be a second
// implementation of the one thing this package already has exactly one of.
func validateViewFilterValues(
	v *SavedView,
	sc *Schema,
	f generated.RecordFilter,
	position int,
	reject func(ViewRejectionCode, string, ...any) *ViewRejection,
) *ViewRejection {
	if f.Values == nil {
		return nil
	}
	prop, found := sc.Property(f.Property)
	if !found || prop.Type != TypeEnum {
		return nil
	}
	for _, val := range *f.Values {
		if val.Enum == nil {
			continue
		}
		if _, resolved := prop.ResolveEnum(*val.Enum); resolved {
			continue
		}
		return reject(RejectViewUnknownEnumValue,
			"view %q compares %s.%s against %q in filter %d, which is not one of its declared values; permitted: %s",
			v.Def.Name, sc.Type, f.Property, *val.Enum, position, joinOrNone(prop.PermittedValues()))
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

// ViewFilterLiterals renders one filter clause's operand values as display
// text, in the order they were declared.
//
// It lives here rather than in the renderer because RecordValue is a
// seven-field tagged union and a consumer that reached into it directly would
// have to know which field each type populates — knowledge every consumer
// would then hold a slightly different version of. There is one reader of that
// union, and it is this function.
//
// A relation or person value renders as its wikilink, which is what is written
// on disk (D5.1) and therefore what an operator recognises.
func ViewFilterLiterals(f generated.RecordFilter) []string {
	if f.Values == nil {
		return nil
	}
	out := make([]string, 0, len(*f.Values))
	for _, v := range *f.Values {
		if lit := viewFilterLiteral(v); lit != "" {
			out = append(out, lit)
		}
	}
	return out
}

func viewFilterLiteral(v generated.RecordValue) string {
	switch PropertyType(v.Type) {
	case TypeText:
		return derefOrEmpty(v.Text)
	case TypeEnum:
		return derefOrEmpty(v.Enum)
	case TypeDate:
		return derefOrEmpty(v.Date)
	case TypeInteger:
		return derefOrEmpty(v.Integer)
	case TypeDecimal:
		return derefOrEmpty(v.Decimal)
	case TypeRelation:
		return refLink(v.Relation)
	case TypePerson:
		return refLink(v.Person)
	default:
		// An unrecognised tag renders as the tag itself rather than as
		// nothing. A silently empty literal would make two different filters
		// render identically, which is the one thing a display of a saved
		// query must never do.
		return "<" + string(v.Type) + ">"
	}
}

func refLink(r *generated.RecordRef) string {
	if r == nil {
		return ""
	}
	return "[[" + r.Link + "]]"
}

func derefOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
