// Omnipus — assembling one translated Base view into a
// records.ParseView-shaped YAML file, and the three-way per-base/per-view
// outcome this whole importer exists to report honestly (see doc.go).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// Outcome is the three-way honesty contract SC-010/US-7 require of every
// imported Base view (and, rolled up, of every Base file).
type Outcome string

const (
	OutcomeConverted           Outcome = "CONVERTED"
	OutcomeConvertedWithLosses Outcome = "CONVERTED WITH NAMED LOSSES"
	OutcomeRefused             Outcome = "REFUSED"
)

// ViewOutcome is one produced (or refused) view.
type ViewOutcome struct {
	BaseRelPath  string
	DisplayName  string
	Status       Outcome
	ResolvedType string
	// Losses names every dropped clause/column/aggregate, in the order
	// found, prefixed with WHERE it came from — see loss.go's LossPosition
	// for the closed set of prefixes and what each one means for FR-105.
	// SC-010's "reported verbatim".
	Losses []string
	// Disabled is FR-105's broadening prohibition, on the record: the view
	// was STORED but MUST NOT be applied, because at least one of its
	// losses sits in a row-set-affecting position and applying it would
	// return MORE rows than the Obsidian original while looking correct.
	//
	// A disabled view is not a refusal. The file is written, the filters
	// that DID translate are in it, and the expressions that did not are in
	// `untranslated` verbatim — so an operator can see exactly what is
	// missing and decide. What it may not do is silently answer a query.
	Disabled bool
	// DisablingLosses is the subset of Losses that caused Disabled, so a
	// reader is never left diffing two lists to find out why.
	DisablingLosses []string
	// Layout is the rendering the Obsidian view asked for (FR-109), as
	// written in the `.base` file. Empty when the view declared none.
	Layout string
	// RefusedReason is set only when Status == OutcomeRefused.
	RefusedReason string
	// OutputRelPath is the vault-relative view file path, set only when a
	// file was produced (Converted or ConvertedWithLosses).
	OutputRelPath string
}

// ProducedView is one view file this importer is about to write.
type ProducedView struct {
	RelPath string // vault-relative, under .omnipus-vault/views/
	Bytes   []byte
}

// BaseOutcome is one `.base` file's rolled-up result.
type BaseOutcome struct {
	BaseRelPath   string
	Status        Outcome
	RefusedReason string // set only when Status == OutcomeRefused
	Views         []ViewOutcome
}

// TranslateBase translates every view in one parsed Base file.
func TranslateBase(pb *ParsedBase, baseRelPath string, schemas *SchemaIndex, slugs *SlugRegistry) (BaseOutcome, []ProducedView) {
	outcome := BaseOutcome{BaseRelPath: baseRelPath}
	if len(pb.Views) == 0 {
		outcome.Status = OutcomeRefused
		outcome.RefusedReason = "the base file declares no views at all"
		return outcome, nil
	}

	outerTrans := TranslateFilterTree(pb.Filters)

	var produced []ProducedView
	anyNonRefused := false
	anyLossy := false
	for _, vraw := range pb.Views {
		name, _ := vraw["name"].(string)
		slug := slugs.Slug(baseRelPath, name)
		vo, pv := translateOneView(vraw, outerTrans, baseRelPath, slug, schemas)
		outcome.Views = append(outcome.Views, vo)
		if vo.Status != OutcomeRefused {
			anyNonRefused = true
		}
		if vo.Status == OutcomeConvertedWithLosses {
			anyLossy = true
		}
		if pv != nil {
			produced = append(produced, *pv)
		}
	}

	switch {
	case !anyNonRefused:
		outcome.Status = OutcomeRefused
		outcome.RefusedReason = "every view in this base failed to translate — see each view's own reason below"
	case anyLossy || hasRefusedView(outcome.Views):
		outcome.Status = OutcomeConvertedWithLosses
	default:
		outcome.Status = OutcomeConverted
	}
	return outcome, produced
}

func hasRefusedView(views []ViewOutcome) bool {
	for _, v := range views {
		if v.Status == OutcomeRefused {
			return true
		}
	}
	return false
}

func translateOneView(vraw map[string]any, outer TreeTranslation, baseRelPath, slug string, schemas *SchemaIndex) (ViewOutcome, *ProducedView) {
	name, _ := vraw["name"].(string)
	vo := ViewOutcome{BaseRelPath: baseRelPath, DisplayName: name}

	viewTrans := TranslateFilterTree(vraw["filters"])

	resolvedType, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
	if resolvedType == "" {
		vo.Status = OutcomeRefused
		if conflict != "" {
			vo.RefusedReason = "cannot determine one record type: " + conflict
		} else {
			vo.RefusedReason = "no `type == \"...\"` equality found anywhere in this view's own filter or the base's outer filter, so there is no single record type to declare — ViewDef requires exactly one `type:`"
		}
		return vo, nil
	}
	if !schemas.HasType(resolvedType) {
		vo.Status = OutcomeRefused
		vo.RefusedReason = fmt.Sprintf("resolved record type %q has no inferred schema — no note in the vault carries `type: %s`", resolvedType, resolvedType)
		return vo, nil
	}
	vo.ResolvedType = resolvedType

	var losses []string
	for _, l := range outer.Lost {
		losses = append(losses, lossf(LossBaseOuterFilter, "%s", l))
	}
	for _, l := range viewTrans.Lost {
		losses = append(losses, lossf(LossViewFilter, "%s", l))
	}

	allLeaves := make([]RawLeaf, 0, len(outer.Leaves)+len(viewTrans.Leaves))
	allLeaves = append(allLeaves, outer.Leaves...)
	allLeaves = append(allLeaves, viewTrans.Leaves...)

	var filterNodes []*yaml.Node
	for _, rl := range allLeaves {
		node, reason, ok := buildFilterLeafNode(resolvedType, rl, schemas)
		if !ok {
			losses = append(losses, lossf(LossFilterLeaf, "%s — %s", describeLeaf(rl), reason))
			continue
		}
		filterNodes = append(filterNodes, node)
	}

	layout, layoutLosses := translateLayout(vraw)
	vo.Layout = layout
	losses = append(losses, layoutLosses...)

	groupBy, groupLosses := translateGroupBy(vraw["groupBy"], resolvedType, schemas)
	losses = append(losses, groupLosses...)

	propsOut, propLosses := translateOrder(vraw["order"], resolvedType, schemas)
	losses = append(losses, propLosses...)

	sortOut, sortLosses := translateSort(vraw["sort"], resolvedType, schemas)
	losses = append(losses, sortLosses...)

	aggOut, aggLosses := translateSummaries(vraw["summaries"], resolvedType, schemas)
	losses = append(losses, aggLosses...)

	// FR-105, THE BROADENING PROHIBITION. Every loss is classified by the
	// position it came from (loss.go). A loss anywhere a row set is decided
	// DISABLES the view; a loss in an annotation position does not. Nothing
	// here counts rows — see loss.go's header for why the oracle is
	// structural rather than arithmetic.
	for _, l := range losses {
		if lossPositionAffectsRowSet(l) {
			vo.DisablingLosses = append(vo.DisablingLosses, l)
		}
	}
	vo.Disabled = len(vo.DisablingLosses) > 0

	pairs := []ordPair{
		{Key: "schema_version", Value: records.SupportedViewVersion},
		{Key: "name", Value: slug},
		{Key: "type", Value: resolvedType},
	}
	if name != "" {
		pairs = append(pairs, ordPair{Key: "label", Value: name})
	}
	if vo.Disabled {
		pairs = append(pairs, ordPair{Key: "disabled", Value: true})
	}
	if layoutKey := emittedLayoutKey(layout); layoutKey != "" {
		pairs = append(pairs, ordPair{Key: "layout", Value: layoutKey})
	}
	if len(filterNodes) > 0 {
		pairs = append(pairs, ordPair{Key: "filters", Value: seq(filterNodes...)})
	}
	if len(groupBy) > 0 {
		pairs = append(pairs, ordPair{Key: "group_by", Value: groupBy})
	}
	if len(sortOut) > 0 {
		pairs = append(pairs, ordPair{Key: "sort", Value: seq(sortOut...)})
	}
	if len(propsOut) > 0 {
		pairs = append(pairs, ordPair{Key: "properties", Value: propsOut})
	}
	if len(aggOut) > 0 {
		pairs = append(pairs, ordPair{Key: "aggregates", Value: seq(aggOut...)})
	}
	pairs = append(pairs, ordPair{Key: "source", Value: baseRelPath})
	if len(losses) > 0 {
		pairs = append(pairs, ordPair{Key: "untranslated", Value: losses})
	}

	top := orderedMap(pairs...)
	bytes, err := marshalDoc(top)
	if err != nil {
		vo.Status = OutcomeRefused
		vo.RefusedReason = fmt.Sprintf("internal: could not render the translated view as YAML: %v", err)
		return vo, nil
	}

	relPath := records.ViewsDirName + "/" + slug + ".yaml"
	vo.OutputRelPath = ".omnipus-vault/" + relPath
	vo.Losses = losses
	if len(losses) > 0 {
		vo.Status = OutcomeConvertedWithLosses
	} else {
		vo.Status = OutcomeConverted
	}
	return vo, &ProducedView{RelPath: relPath, Bytes: bytes}
}

// ---------------------------------------------------------------------------
// FR-109 — a view's LAYOUT is part of what must not be lost silently
// ---------------------------------------------------------------------------

// renderedLayouts are the two layouts this product actually draws. Every
// other value of ViewDefLayout exists in the contract precisely BECAUSE it
// is not drawn: the importer has to be able to say what an Obsidian view
// actually asked for.
var renderedLayouts = map[string]bool{
	string(generated.ViewDefLayoutTable): true,
	string(generated.ViewDefLayoutCards): true,
}

// knownLayouts is every value ViewDefLayout declares — the set this
// importer may legally write into a view file's `layout:` key.
var knownLayouts = map[string]bool{
	string(generated.ViewDefLayoutTable):    true,
	string(generated.ViewDefLayoutCards):    true,
	string(generated.ViewDefLayoutBoard):    true,
	string(generated.ViewDefLayoutCalendar): true,
	string(generated.ViewDefLayoutGallery):  true,
	string(generated.ViewDefLayoutMap):      true,
}

// translateLayout reads the Obsidian view's own `type:` key — WHICH THIS
// IMPORTER PREVIOUSLY NEVER LOOKED AT.
//
// That omission is the finding this function closes, and it is worth stating
// plainly because it is the exact failure ADR-068 is written against: an
// Obsidian CARDS view imported as a table, recorded no loss at all, and
// would have scored CLEAN under W7's exit criterion. A green number over an
// undetected loss. The view was not broken — it was silently a different
// view, with nothing anywhere to say so.
//
// Note the key collision, which is why this is easy to get wrong: inside a
// `.base` file `type:` means TWO unrelated things. On a VIEW it is the
// rendering (`type: table`). Inside a FILTER expression (`type == "decision"`)
// it is the record-type discriminator. This function reads only the first;
// resolveViewType reads only the second.
func translateLayout(vraw map[string]any) (layout string, losses []string) {
	raw, _ := vraw["type"].(string)
	layout = strings.ToLower(strings.TrimSpace(raw))
	if layout == "" {
		// The base did not say. ViewDef's own rule is that an omitted
		// layout means `table`, so there is nothing to carry and nothing
		// to lose.
		return "", nil
	}

	switch {
	case !knownLayouts[layout]:
		// A layout ViewDef has no value for at all (Obsidian's `list`, or
		// anything it adds after this release). It cannot be written into
		// the file, so the ONLY place it can survive is a named loss.
		return layout, []string{lossf(LossLayout,
			"the Obsidian view asked for layout %q, which this release's view format has no value for; it imports as a table and the request is recorded here rather than lost",
			layout)}
	case !emitsLayoutKey():
		if layout == string(generated.ViewDefLayoutTable) {
			// The format's omitted-layout default IS table, so a table
			// view loses nothing by the key being absent.
			return layout, nil
		}
		return layout, []string{lossf(LossLayout,
			"the Obsidian view asked for layout %q; the view file format this release writes (schema_version %d) has no `layout` field, so the view imports as a table and the request is recorded here rather than lost",
			layout, records.SupportedViewVersion)}
	case !renderedLayouts[layout]:
		// Carried faithfully into the file, but the product draws only
		// table and cards — so the operator is told, by name, that they
		// will see a table.
		return layout, []string{lossf(LossLayout,
			"the Obsidian view asked for layout %q, which this product does not render; the request is carried in the view's `layout:` field and will be drawn as a table until it is",
			layout)}
	}
	return layout, nil
}

// emitsLayoutKey reports whether the view-file format this importer WRITES
// carries a `layout` field at all.
//
// `layout` is declared VERSION 2 ONLY on ViewDef, and this importer emits
// records.SupportedViewVersion, which is the only version the loader in
// pkg/records accepts. Writing a v2-only key into a v1 file would produce
// files that load today (the loader gates on version but not yet on
// per-version keys) and are REJECTED the moment somebody implements that
// gate — turning every imported view into a load failure long after the run
// that wrote them.
//
// So the version is asked, not assumed. When pkg/records starts emitting and
// accepting version 2, this returns true and translateLayout carries the key
// instead of reporting it as a loss, with no other change here.
//
// It is a function rather than a constant expression so the branch it guards
// is real code on both sides rather than something the compiler folds away.
func emitsLayoutKey() bool { return records.SupportedViewVersion >= 2 }

// emittedLayoutKey returns the value to write into the view file's `layout:`
// key, or "" to omit it.
func emittedLayoutKey(layout string) string {
	if layout == "" || !emitsLayoutKey() || !knownLayouts[layout] {
		return ""
	}
	return layout
}

// ---------------------------------------------------------------------------
// FR-105 — WHERE "has a value" IS NOT "is truthy"
//
// Obsidian's bare-property filter (`archived`) is a JavaScript truthy test.
// Our nearest operator is `is_absent` negated, which asks a DIFFERENT
// question: does this record have a value at all. The two agree exactly when
// every value a property can hold is truthy, and they part company on the
// falsy-but-present ones — `false` on a checkbox, `0` on a number. There the
// negated `is_absent` matches a record Obsidian's own filter rejects, which
// is the broadening FR-105 forbids by name.
//
// So the answer is decided PER DECLARED TYPE, as a partition over
// records.PropertyTypes rather than as a list of the dangerous ones. A list
// cannot detect its own incompleteness: add a ninth type and it defaults to
// "safe", the truthy test translates, and the view broadens — the exact
// failure, reintroduced by an omission. TestTruthyPartition_CoversEveryType
// fails by name instead.
//
// The empty string is not on this map anywhere, deliberately: FR-007a makes
// an empty string ABSENT, so `""` is falsy AND absent, and the two questions
// still agree on it.
// ---------------------------------------------------------------------------

// truthyFalsyLiterals maps each declared property type to the present-but-
// FALSY values it can hold. An empty string means the type has none, so a
// bare truthy test translates faithfully.
var truthyFalsyLiterals = map[records.PropertyType]string{
	records.TypeCheckbox: "false",
	records.TypeInteger:  "0",
	records.TypeDecimal:  "0, 0.0",

	records.TypeText:     "",
	records.TypeEnum:     "",
	records.TypeDate:     "",
	records.TypeRelation: "",
	records.TypePerson:   "",
}

// truthyAdmitsAFalsyValue reports whether a declared type can hold a value
// that is present and falsy — i.e. whether "has a value" is BROADER than
// "is truthy" for it. An unknown type answers TRUE: an unclassified type is
// treated as the dangerous kind, so forgetting to classify one costs a view
// that is disabled when it need not have been, never a view that broadens.
func truthyAdmitsAFalsyValue(t records.PropertyType) bool {
	lit, known := truthyFalsyLiterals[t]
	if !known {
		return true
	}
	return lit != ""
}

// falsyLiteralsFor names the offending values for the refusal message.
func falsyLiteralsFor(t records.PropertyType) string {
	if lit := truthyFalsyLiterals[t]; lit != "" {
		return lit
	}
	return "a present but falsy value"
}

func describeLeaf(rl RawLeaf) string {
	if len(rl.Values) > 0 {
		return fmt.Sprintf("%s %s %v", rl.Property, rl.Op, rl.Values)
	}
	return fmt.Sprintf("%s %s", rl.Property, rl.Op)
}

// resolveViewType finds the single `type == "X"` this view unconditionally
// asserts, preferring the view's own filter over the base's outer filter —
// a view that narrows further (Content.base's per-view `type == "content"`)
// always wins over an outer filter that only narrows to a set (Content.base's
// outer `or: [type == "content", type == "brand-kit"]`, which the OR
// translator never harvests literals from at all — see translate.go).
func resolveViewType(viewLits, outerLits []string) (resolved, conflict string) {
	vd := distinctSorted(viewLits)
	if len(vd) == 1 {
		return vd[0], ""
	}
	if len(vd) > 1 {
		return "", fmt.Sprintf("the view's own filter asserts more than one type (%s)", strings.Join(vd, ", "))
	}
	od := distinctSorted(outerLits)
	if len(od) == 1 {
		return od[0], ""
	}
	if len(od) > 1 {
		return "", fmt.Sprintf("the base's outer filter asserts more than one type (%s) and the view does not narrow it further", strings.Join(od, ", "))
	}
	return "", ""
}

func distinctSorted(ss []string) []string {
	seen := map[string]struct{}{}
	for _, s := range ss {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func buildFilterLeafNode(recordType string, rl RawLeaf, schemas *SchemaIndex) (*yaml.Node, string, bool) {
	prop, ok := schemas.Lookup(recordType, rl.Property)
	if !ok {
		return nil, fmt.Sprintf("property %q is not declared in the %q schema (never observed on a %s note)", rl.Property, recordType, recordType), false
	}
	if rl.Truthy && truthyAdmitsAFalsyValue(prop.Type) {
		// FR-105, the exact broadening this flag exists to stop. Obsidian's
		// bare `%s` matches only records whose value is TRUTHY; the negated
		// `is_absent` it would otherwise become matches every record that
		// HAS a value, `false` and `0` included. More rows, silently.
		return nil, fmt.Sprintf(
			"the bare truthy test `%s` has no faithful translation on a %s property — our nearest operator is \"has a value\", which also matches a record whose %s is present and FALSY (%s), so it would return MORE rows than the Obsidian original",
			rl.Property, prop.Type, rl.Property, falsyLiteralsFor(prop.Type)), false
	}
	if prop.Many && rl.Op != "contains" && rl.Op != "is_absent" {
		// §8 R-13: `contains` and `is_absent` are the only two operators
		// defined against a many-valued property. Emitting anything else
		// produces a clause the engine refuses per record at query time,
		// which reads as an empty view rather than as a translation this
		// importer could not make.
		return nil, fmt.Sprintf("operator %q is not defined on many-valued property %q (only contains/is_absent are defined for a list)", rl.Op, rl.Property), false
	}
	if rl.Op == "contains" {
		if prop.Type != records.TypeText && !prop.Many {
			return nil, fmt.Sprintf("`contains` is not defined on %q (declared %s, not many-valued and not text)", rl.Property, prop.Type), false
		}
	} else if rl.Op != "is_absent" && prop.Type == records.TypeText {
		return nil, fmt.Sprintf("operator %q is not defined on text property %q (only contains/is_absent are defined for text)", rl.Op, rl.Property), false
	}

	pairs := []ordPair{{Key: "property", Value: rl.Property}, {Key: "op", Value: rl.Op}}
	if rl.Negate {
		pairs = append(pairs, ordPair{Key: "negate", Value: true})
	}
	if len(rl.Values) > 0 {
		valNodes := make([]*yaml.Node, 0, len(rl.Values))
		for _, raw := range rl.Values {
			vn, reason, ok := buildValueNode(prop, raw)
			if !ok {
				return nil, reason, false
			}
			valNodes = append(valNodes, vn)
		}
		pairs = append(pairs, ordPair{Key: "values", Value: seq(valNodes...)})
	}
	return orderedMap(pairs...), "", true
}

func buildValueNode(prop InferredProperty, raw string) (*yaml.Node, string, bool) {
	switch prop.Type {
	case records.TypeText:
		return orderedMap(ordPair{Key: "type", Value: "text"}, ordPair{Key: "text", Value: raw}), "", true
	case records.TypeEnum:
		for _, v := range prop.EnumValues {
			if records.FoldKey(v) == records.FoldKey(raw) {
				return orderedMap(ordPair{Key: "type", Value: "enum"}, ordPair{Key: "enum", Value: v}), "", true
			}
		}
		return nil, fmt.Sprintf("value %q is not one of %q's declared enum values (%s)", raw, prop.Name, strings.Join(prop.EnumValues, ", ")), false
	case records.TypeDate:
		return orderedMap(ordPair{Key: "type", Value: "date"}, ordPair{Key: "date", Value: raw}), "", true
	case records.TypeInteger:
		return orderedMap(ordPair{Key: "type", Value: "integer"}, ordPair{Key: "integer", Value: raw}), "", true
	case records.TypeDecimal:
		return orderedMap(ordPair{Key: "type", Value: "decimal"}, ordPair{Key: "decimal", Value: raw}), "", true
	case records.TypeRelation, records.TypePerson:
		link := raw
		if w, ok := records.ParseWikilink(raw); ok {
			link = w.Target
		}
		field := "relation"
		if prop.Type == records.TypePerson {
			field = "person"
		}
		ref := orderedMap(ordPair{Key: "link", Value: link}, ordPair{Key: "resolved", Value: false})
		return orderedMap(ordPair{Key: "type", Value: string(prop.Type)}, ordPair{Key: field, Value: ref}), "", true
	case records.TypeCheckbox:
		// `checkbox` is FR-004c's eighth property type and RecordValue —
		// the version-1 filter-literal wire type this importer writes — still
		// carries only the original seven. There is no field to put `true`
		// in, so the clause is refused BY NAME and its view disables, rather
		// than being dropped (which would broaden it) or coerced into a text
		// literal (which would compare "true" against a boolean and match
		// nothing, an empty view dressed as a working one).
		return nil, fmt.Sprintf("property %q is declared `checkbox`, which the version-%d view file format has no filter-literal representation for (RecordValue carries the original seven types)", prop.Name, records.SupportedViewVersion), false
	}
	return nil, fmt.Sprintf("property %q is declared %q, a type this importer has no filter-literal representation for", prop.Name, prop.Type), false
}

func translateGroupBy(raw any, recordType string, schemas *SchemaIndex) (groupBy []string, losses []string) {
	gb, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}
	prop, _ := gb["property"].(string)
	dir, _ := gb["direction"].(string)
	if prop == "" {
		return nil, nil
	}
	if strings.HasPrefix(prop, "formula.") {
		return nil, []string{lossf(LossGroupBy, "grouping by computed property %q dropped — this vault's `.base` formulas have no representation (no computed/derived property type exists)", prop)}
	}
	if _, ok := schemas.Lookup(recordType, prop); !ok {
		return nil, []string{lossf(LossGroupBy, "grouping by %q dropped — not a declared property of %q", prop, recordType)}
	}
	groupBy = []string{prop}
	if dir != "" {
		losses = append(losses, lossf(LossGroupBy, "direction %q on %q dropped — group_by has no sort-direction field on the wire (ViewDef.group_by is a bare property list)", dir, prop))
	}
	return groupBy, losses
}

func translateOrder(raw any, recordType string, schemas *SchemaIndex) (props []string, losses []string) {
	ord, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	for _, o := range ord {
		s, _ := o.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if s == "file.name" || strings.HasPrefix(s, "file.") {
			// The note's own identity/path is always shown regardless of
			// `properties:` — nothing queryable is lost by not naming it.
			continue
		}
		if strings.HasPrefix(s, "formula.") {
			losses = append(losses, lossf(LossProperties, "column %q dropped — computed/formula properties have no representation", s))
			continue
		}
		if _, ok := schemas.Lookup(recordType, s); !ok {
			losses = append(losses, lossf(LossProperties, "column %q dropped — not a declared property of %q", s, recordType))
			continue
		}
		props = append(props, s)
	}
	return props, losses
}

func translateSort(raw any, recordType string, schemas *SchemaIndex) (nodes []*yaml.Node, losses []string) {
	srt, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	for _, s := range srt {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		prop, _ := sm["property"].(string)
		dir, _ := sm["direction"].(string)
		if prop == "" {
			continue
		}
		if strings.HasPrefix(prop, "formula.") {
			losses = append(losses, lossf(LossSort, "sorting by computed property %q dropped — formulas have no representation", prop))
			continue
		}
		if _, ok := schemas.Lookup(recordType, prop); !ok {
			losses = append(losses, lossf(LossSort, "sorting by %q dropped — not a declared property of %q", prop, recordType))
			continue
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "property", Value: prop}, ordPair{Key: "direction", Value: strings.ToLower(strings.TrimSpace(dir))}))
	}
	return nodes, losses
}

var supportedAggregateOps = map[string]string{"sum": "sum", "min": "min", "max": "max", "count": "count"}

func translateSummaries(raw any, recordType string, schemas *SchemaIndex) (nodes []*yaml.Node, losses []string) {
	summ, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}
	for _, prop := range sortedKeys(summ) {
		opRaw, _ := summ[prop].(string)
		op, known := supportedAggregateOps[strings.ToLower(strings.TrimSpace(opRaw))]
		if !known {
			losses = append(losses, lossf(LossAggregates, "summary %q on %q dropped — unsupported aggregate (only sum/min/max/count exist; there is deliberately no avg)", opRaw, prop))
			continue
		}
		if strings.HasPrefix(prop, "formula.") {
			losses = append(losses, lossf(LossAggregates, "%s(%s) dropped — formulas have no representation", op, prop))
			continue
		}
		if _, ok := schemas.Lookup(recordType, prop); !ok {
			losses = append(losses, lossf(LossAggregates, "%s(%s) dropped — not a declared property of %q", op, prop, recordType))
			continue
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "op", Value: op}, ordPair{Key: "property", Value: prop}))
	}
	return nodes, losses
}
