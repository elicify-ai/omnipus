// Omnipus — assembling one translated Base view into a
// records.ParseView-shaped VERSION-2 YAML file, and the three-way
// per-base/per-view outcome this whole importer exists to report honestly
// (see doc.go).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"sort"
	"strconv"
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
		vo, pv := translateOneView(vraw, outerTrans, pb.Limit, baseRelPath, slug, schemas)
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

func translateOneView(vraw map[string]any, outer TreeTranslation, outerLimit any, baseRelPath, slug string, schemas *SchemaIndex) (ViewOutcome, *ProducedView) {
	name, _ := vraw["name"].(string)
	vo := ViewOutcome{BaseRelPath: baseRelPath, DisplayName: name}

	viewTrans := TranslateFilterTree(vraw["filters"])

	// THE TYPE IS NOW OPTIONAL (FR-018b). A view that asserts no `type ==`
	// anywhere is written UNTYPED — it queries every note in scope, resolving
	// property names over the rows FR-021e keeps for every note. That is the
	// only expressible reading of the founder's folder-scoped bases, and it is
	// faithful precisely BECAUSE the folder clause that scopes them now
	// translates too (FR-134): an untyped view whose folder filter was dropped
	// would be the whole vault, which is the broadening FR-105 forbids — and
	// that case is caught by the dropped clause being a row-set loss, not by
	// refusing the view.
	resolvedType, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
	if conflict != "" {
		vo.Status = OutcomeRefused
		vo.RefusedReason = "cannot determine one record type: " + conflict
		return vo, nil
	}
	if resolvedType != "" && !schemas.HasType(resolvedType) {
		vo.Status = OutcomeRefused
		vo.RefusedReason = fmt.Sprintf("resolved record type %q has no inferred schema — no note in the vault carries `type: %s`", resolvedType, resolvedType)
		return vo, nil
	}
	vo.ResolvedType = resolvedType

	res := leafResolver{recordType: resolvedType, schemas: schemas}

	outerNode, losses := res.resolve(outer.Root, LossBaseOuterFilter)
	viewNode, viewLosses := res.resolve(viewTrans.Root, LossViewFilter)
	losses = append(losses, viewLosses...)

	filterNode := conjoin(outerNode, viewNode)

	layout, layoutLosses := translateLayout(vraw)
	vo.Layout = layout
	losses = append(losses, layoutLosses...)

	grouping, groupLosses := translateGrouping(vraw["groupBy"], res)
	losses = append(losses, groupLosses...)

	propsOut, propLosses := translateOrder(vraw["order"], res)
	losses = append(losses, propLosses...)

	sortOut, sortLosses := translateSort(vraw["sort"], res)
	losses = append(losses, sortLosses...)

	aggOut, aggLosses := translateSummaries(vraw["summaries"], res)
	losses = append(losses, aggLosses...)

	limit, limitLosses := translateLimit(vraw["limit"], outerLimit)
	losses = append(losses, limitLosses...)

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
		{Key: "name", Value: slug},
	}
	if resolvedType != "" {
		pairs = append(pairs, ordPair{Key: "type", Value: resolvedType})
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
	if filterNode != nil {
		pairs = append(pairs, ordPair{Key: "filter", Value: filterNodeYAML(*filterNode)})
	}
	if len(grouping) > 0 {
		pairs = append(pairs, ordPair{Key: "grouping", Value: seq(grouping...)})
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
	if limit > 0 {
		pairs = append(pairs, ordPair{Key: "limit", Value: limit})
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

// conjoin ANDs the base's outer filter with the view's own — which is exactly
// what Obsidian does, and the one place the two trees meet.
func conjoin(a, b *generated.VaultFilterNode) *generated.VaultFilterNode {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	}
	kids := []generated.VaultFilterNode{*a, *b}
	return &generated.VaultFilterNode{All: &kids}
}

// ---------------------------------------------------------------------------
// Resolving the intermediate tree against the view's record type
// ---------------------------------------------------------------------------

// leafResolver carries the two things a leaf needs to become a real filter
// node: which record type the view queries (EMPTY for an untyped view) and the
// inferred schemas to check it against.
type leafResolver struct {
	recordType string
	schemas    *SchemaIndex
}

// typed reports whether this view declares a record type, and therefore whether
// a property's declared type is knowable at all.
func (r leafResolver) typed() bool { return r.recordType != "" }

// resolve turns one intermediate subtree into a real VaultFilterNode, plus the
// named losses it produced.
//
// The FR-105 posture per combinator, stated once:
//
//	all  a child that could not be resolved is DROPPED and its loss named.
//	     Dropping a conjunct BROADENS, which is why that loss sits in a
//	     row-set-affecting position and disables the whole view.
//	any  a child that could not be resolved loses the WHOLE group. Keeping
//	     the rest would narrow the view to one side of an "either" the
//	     operator wrote deliberately — reported instead of guessed.
//	not  same: half of a negation is a different exclusion, not a partial one.
func (r leafResolver) resolve(n *rawNode, pos LossPosition) (*generated.VaultFilterNode, []string) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case rawKindLost:
		return nil, []string{lossf(pos, "%s", n.Verbatim)}

	case rawKindPrebuilt:
		return n.Prebuilt, nil

	case rawKindLeaf:
		node, reason, ok := buildV2LeafNode(r, n.Leaf)
		if !ok {
			return nil, []string{lossf(LossFilterLeaf, "%s — %s", describeLeaf(n.Leaf), reason)}
		}
		return node, nil

	case rawKindAll:
		var kids []generated.VaultFilterNode
		var losses []string
		for _, k := range n.Kids {
			child, childLosses := r.resolve(k, pos)
			losses = append(losses, childLosses...)
			if child != nil {
				kids = append(kids, *child)
			}
		}
		switch len(kids) {
		case 0:
			return nil, losses
		case 1:
			return &kids[0], losses
		default:
			return &generated.VaultFilterNode{All: &kids}, losses
		}

	case rawKindAny, rawKindNot:
		var kids []generated.VaultFilterNode
		for _, k := range n.Kids {
			child, childLosses := r.resolve(k, pos)
			if child == nil || len(childLosses) > 0 {
				// The group's own verbatim is reported, not the child's — a
				// reader has to see which `or:`/`not:` block went missing, and
				// a half-named group reads as if the rest survived.
				return nil, []string{lossf(pos, "%s", n.Verbatim)}
			}
			kids = append(kids, *child)
		}
		if len(kids) == 0 {
			return nil, []string{lossf(pos, "%s", n.Verbatim)}
		}
		if n.Kind == rawKindNot {
			inner := kids[0]
			if len(kids) > 1 {
				all := kids
				inner = generated.VaultFilterNode{All: &all}
			}
			return &generated.VaultFilterNode{Not: &inner}, nil
		}
		return &generated.VaultFilterNode{Any: &kids}, nil
	}
	return nil, []string{lossf(pos, "%s", n.Verbatim)}
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

// emittedLayoutKey returns the value to write into the view file's `layout:`
// key, or "" to omit it.
func emittedLayoutKey(layout string) string {
	if layout == "" || !knownLayouts[layout] {
		return ""
	}
	return layout
}

// ---------------------------------------------------------------------------
// FR-105 — WHERE "has a value" IS NOT "is truthy"
//
// Obsidian's bare-property filter (`archived`) is a JavaScript truthy test.
// Our nearest operator is `IS NOT NULL`, which asks a DIFFERENT question: does
// this record have a value at all. The two agree exactly when every value a
// property can hold is truthy, and they part company on the falsy-but-present
// ones — `false` on a checkbox, `0` on a number, `""` on a TEXT property.
// There the `IS NOT NULL` matches a record Obsidian's own filter rejects,
// which is the broadening FR-105 forbids by name.
//
// So the answer is decided PER DECLARED TYPE, as a partition over
// records.PropertyTypes rather than as a list of the dangerous ones. A list
// cannot detect its own incompleteness: add a ninth type and it defaults to
// "safe", the truthy test translates, and the view broadens — the exact
// failure, reintroduced by an omission. TestTruthyPartition_CoversEveryType
// fails by name instead.
//
// TEXT IS ON THE DANGEROUS SIDE, and it moved there with the version-2 writer.
// FR-007a makes `""` ABSENT for every NON-text type — so on those, "has a
// value" and "is truthy" agree about it. For `text` the same requirement says
// the opposite in as many words: *"For text, `""` remains a PRESENT empty
// string"*, which VaultFilterNode's own contract restates for the operator
// (R-3: "an empty string, an empty list and a zero are all VALUES, not
// absence"). So `IS NOT NULL` matches a text property holding `""` and
// Obsidian's bare truthy test does not. Version 1 classified text as safe on
// the strength of the first half of FR-007a alone; that was wrong, and it is
// corrected here rather than carried forward into a format that can finally
// express the distinction.
// ---------------------------------------------------------------------------

// truthyFalsyLiterals maps each declared property type to the present-but-
// FALSY values it can hold. An empty string means the type has none, so a
// bare truthy test translates faithfully.
var truthyFalsyLiterals = map[records.PropertyType]string{
	records.TypeCheckbox: "false",
	records.TypeInteger:  "0",
	records.TypeDecimal:  "0, 0.0",
	records.TypeText:     `the empty string ""`,

	records.TypeEnum:     "",
	records.TypeDate:     "",
	records.TypeRelation: "",
	records.TypePerson:   "",
}

// jsFalsyLiterals are the value spellings Obsidian's own JavaScript truthy
// test rejects. `false` and `0` are here; `"false"` as a QUOTED string is not,
// because a non-empty string is truthy in JavaScript — but this importer
// cannot see a value's quoting (observedValue carries text, and an enum's
// declared values are text by the time they reach a schema), so a declared
// value that READS like one of these is treated as one. That is the safe
// direction: it costs a view that is disabled when it need not have been.
var jsFalsyLiterals = map[string]bool{
	"false": true,
	"0":     true,
	"0.0":   true,
	"":      true,
}

// enumDeclaresAFalsyValue reports whether any of an enum's declared values is
// one Obsidian's truthy test would reject.
//
// THE TYPE ALONE IS NOT ENOUGH FOR AN ENUM, and this is the residual half of
// the finding that moved boolean inference to `checkbox`. `enum` is on the safe
// side of the partition because most enums are controlled vocabularies of
// words, every one of which is truthy. But an enum is whatever the vault put in
// it: `level: 0 / high / low` infers an enum declaring `0`, and `IS NOT NULL`
// then matches the record holding 0 that Obsidian's bare `level` filter
// rejects. So for this one type the partition consults the DECLARED VALUES,
// which is the only place the answer actually lives.
func enumDeclaresAFalsyValue(values []string) bool {
	for _, v := range values {
		if jsFalsyLiterals[records.FoldKey(strings.TrimSpace(v))] {
			return true
		}
	}
	return false
}

// truthyAdmitsAFalsyValue reports whether a declared property can hold a value
// that is present and falsy — i.e. whether "has a value" is BROADER than
// "is truthy" for it. An unknown type answers TRUE: an unclassified type is
// treated as the dangerous kind, so forgetting to classify one costs a view
// that is disabled when it need not have been, never a view that broadens.
func truthyAdmitsAFalsyValue(p InferredProperty) bool {
	lit, known := truthyFalsyLiterals[p.Type]
	if !known {
		return true
	}
	if lit != "" {
		return true
	}
	if p.Type == records.TypeEnum {
		return enumDeclaresAFalsyValue(p.EnumValues)
	}
	return false
}

// falsyLiteralsFor names the offending values for the refusal message.
func falsyLiteralsFor(p InferredProperty) string {
	if lit := truthyFalsyLiterals[p.Type]; lit != "" {
		return lit
	}
	if p.Type == records.TypeEnum {
		var falsy []string
		for _, v := range p.EnumValues {
			if jsFalsyLiterals[records.FoldKey(strings.TrimSpace(v))] {
				falsy = append(falsy, v)
			}
		}
		if len(falsy) > 0 {
			return "the declared value(s) " + strings.Join(falsy, ", ")
		}
	}
	return "a present but falsy value"
}

func describeLeaf(l v2Leaf) string {
	if l.Source != "" {
		return l.Source
	}
	return l.Property
}

// resolveViewType finds the single `type == "X"` this view unconditionally
// asserts, preferring the view's own filter over the base's outer filter —
// a view that narrows further (Content.base's per-view `type == "content"`)
// always wins over an outer filter that only narrows to a set (Content.base's
// outer `or: [type == "content", type == "brand-kit"]`, which the OR
// translator never harvests literals from at all — see translate.go).
//
// An empty resolved type with an empty conflict is NOT a failure any more: it
// is an UNTYPED view, which the format allows (FR-018b).
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

// buildV2LeafNode turns one intermediate leaf into a real filter node, or
// refuses it by name.
//
// It is the ONE place a property's declared type decides an operator, and every
// refusal below is a refusal because the available operator would return MORE
// rows than the Obsidian expression, never merely different ones.
func buildV2LeafNode(r leafResolver, l v2Leaf) (*generated.VaultFilterNode, string, bool) {
	var prop InferredProperty
	if r.typed() {
		p, ok := r.schemas.Lookup(r.recordType, l.Property)
		if !ok {
			return nil, fmt.Sprintf("property %q is not declared in the %q schema (never observed on a %s note)", l.Property, r.recordType, r.recordType), false
		}
		prop = p
	}

	switch l.Shape {
	case shapeFalsy:
		// `!prop`. Obsidian's falsy test catches absent, `false`, `0` and `""`;
		// `IS NULL` catches absent (and, under FR-007a, `""` on every non-text
		// type). Ours is a strict SUBSET, so it can only ever return FEWER
		// rows — the direction FR-105 permits — and it needs no declared type
		// to be safe, which is why an untyped view may carry it.
		return opNode(l.Property, generated.ISNULL), "", true

	case shapeTruthy:
		if !r.typed() {
			return nil, "the bare truthy test cannot be translated in an UNTYPED view: `has a value` is broader than `is truthy` for a checkbox, a number or a text property, and with no declared type there is nothing to rule those out", false
		}
		if truthyAdmitsAFalsyValue(prop) {
			return nil, fmt.Sprintf(
				"the bare truthy test has no faithful translation on a %s property — our nearest operator is `IS NOT NULL`, which also matches a record whose %s is present and FALSY (%s), so it would return MORE rows than the Obsidian original",
				prop.Type, l.Property, falsyLiteralsFor(prop)), false
		}
		return opNode(l.Property, generated.ISNOTNULL), "", true

	case shapeIsSet:
		// `prop != ""`. FR-007a rules this translation in as many words: for
		// every NON-text type the empty string IS the absent state, so
		// Obsidian's idiomatic "is set" becomes `IS NOT NULL`.
		//
		// It is safe under BOTH readings of what Obsidian does with a property
		// that is not there at all. If `undefined != ""` is TRUE (JavaScript's
		// own answer), Obsidian returns the set-plus-absent notes and ours
		// returns only the set ones — narrower. If Obsidian instead reads an
		// absent property as `""`, the two sets are identical. Neither reading
		// makes ours the larger set, which is the only question FR-105 asks.
		if !r.typed() {
			return nil, "an UNTYPED view cannot carry `!= \"\"`: on a text property the empty string is a PRESENT value (FR-007a), so `IS NOT NULL` would match a record the Obsidian filter excludes, and with no declared type there is nothing to rule that out", false
		}
		if prop.Type == records.TypeText {
			return nil, fmt.Sprintf(
				"`%s != \"\"` has no faithful translation on a TEXT property: FR-007a keeps `\"\"` a PRESENT value for text, so `IS NOT NULL` would also match a record whose %s is the empty string — a record the Obsidian filter excludes",
				l.Property, l.Property), false
		}
		return opNode(l.Property, generated.ISNOTNULL), "", true

	case shapeIsEmpty:
		// `prop == ""`. `IS NULL` also matches a record that never declared the
		// property at all, which Obsidian's comparison does not, so it returns
		// MORE rows. There is no other operator to reach for: no non-text type
		// has an empty literal to compare against (FR-007a).
		return nil, fmt.Sprintf(
			"`%s == \"\"` has no faithful translation: `IS NULL` also matches a record that never declared %s, which the Obsidian comparison does not, so it would return MORE rows than the original",
			l.Property, l.Property), false

	case shapeContains:
		if !r.typed() {
			return nil, "`contains` cannot be translated in an UNTYPED view: it is element membership on a many property and substring matching on a text one, and those are two different operators", false
		}
		if prop.Many {
			// R-9: `=` is element-wise on a many property, which is exactly
			// Obsidian's list `.contains`.
			value := l.Value
			if prop.Type == records.TypeEnum {
				canonical, ok := canonicalEnumValue(prop, value)
				if !ok {
					return nil, fmt.Sprintf("value %q is not one of %q's declared enum values (%s)", value, prop.Name, strings.Join(prop.EnumValues, ", ")), false
				}
				value = canonical
			}
			return valueNode(l.Property, generated.Equal, value), "", true
		}
		if prop.Type == records.TypeText {
			// Substring. `LIKE` is anchored to the WHOLE value, so the
			// substring is spelled with wildcards, and the literal is ESCAPED
			// — an unescaped `_` in the operand is a single-character wildcard
			// and would match notes the operator never asked for.
			return valueNode(l.Property, generated.LIKE, "%"+escapeLikeOperand(l.Value)+"%"), "", true
		}
		return nil, fmt.Sprintf("`contains` is not defined on %q (declared %s, not many-valued and not text)", l.Property, prop.Type), false

	case shapeCompare:
		if r.typed() {
			if prop.Many && isOrderingOp(l.Op) {
				// §8 R-13: the four ordering operators are UNDEFINED against a
				// many property. Emitting one produces a clause the engine
				// refuses per record at query time, which reads as an empty
				// view rather than as a translation this importer could not
				// make.
				return nil, fmt.Sprintf("operator %q is not defined on many-valued property %q (spec §8 R-13 leaves the ordering operators undefined for a list)", string(l.Op), l.Property), false
			}
			if prop.Type == records.TypeEnum {
				canonical, ok := canonicalEnumValue(prop, l.Value)
				if !ok {
					return nil, fmt.Sprintf("value %q is not one of %q's declared enum values (%s)", l.Value, prop.Name, strings.Join(prop.EnumValues, ", ")), false
				}
				return valueNode(l.Property, l.Op, canonical), "", true
			}
			if prop.Type == records.TypeRelation || prop.Type == records.TypePerson {
				// A relation is compared by TARGET (spec §8 R-8), which is what
				// a wikilink's inside is.
				link := l.Value
				if w, ok := records.ParseWikilink(l.Value); ok {
					link = w.Target
				}
				return valueNode(l.Property, l.Op, link), "", true
			}
		}
		return valueNode(l.Property, l.Op, l.Value), "", true
	}
	return nil, "this importer has no translation for that expression shape", false
}

func isOrderingOp(op generated.VaultFilterNodeOp) bool {
	switch op {
	case generated.LessThan, generated.LessThanEqual, generated.GreaterThan, generated.GreaterThanEqual:
		return true
	}
	return false
}

func canonicalEnumValue(prop InferredProperty, raw string) (string, bool) {
	for _, v := range prop.EnumValues {
		if records.FoldKey(v) == records.FoldKey(raw) {
			return v, true
		}
	}
	return "", false
}

func opNode(property string, op generated.VaultFilterNodeOp) *generated.VaultFilterNode {
	p, o := property, op
	return &generated.VaultFilterNode{Property: &p, Op: &o}
}

func valueNode(property string, op generated.VaultFilterNodeOp, value string) *generated.VaultFilterNode {
	n := opNode(property, op)
	v := value
	n.Value = &v
	return n
}

// escapeLikeOperand makes a literal safe as a LIKE operand. It mirrors
// pkg/records/filemeta.go's escapeLikeLiteral, which is unexported and is the
// canonical statement of the rule; the order matters (`\` first, or the two
// wildcard escapes would themselves be escaped).
func escapeLikeOperand(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "%", `\%`)
	s = strings.ReplaceAll(s, "_", `\_`)
	return s
}

// ---------------------------------------------------------------------------
// Rendering a filter tree as the YAML records.ParseView reads back
// ---------------------------------------------------------------------------

// filterNodeYAML renders one VaultFilterNode as a key-ordered YAML mapping.
//
// The keys are the generated type's own `json:` tags, and they have to be
// exactly those: ParseView decodes the file through encoding/json with
// DisallowUnknownFields, so a near-miss spelling is a REJECTED view rather
// than a silently ignored key.
func filterNodeYAML(n generated.VaultFilterNode) *yaml.Node {
	var pairs []ordPair
	if n.Property != nil {
		pairs = append(pairs, ordPair{Key: "property", Value: *n.Property})
	}
	if n.Op != nil {
		pairs = append(pairs, ordPair{Key: "op", Value: string(*n.Op)})
	}
	if n.Value != nil {
		pairs = append(pairs, ordPair{Key: "value", Value: *n.Value})
	}
	if n.Values != nil {
		pairs = append(pairs, ordPair{Key: "values", Value: *n.Values})
	}
	if n.All != nil {
		pairs = append(pairs, ordPair{Key: "all", Value: filterChildrenYAML(*n.All)})
	}
	if n.Any != nil {
		pairs = append(pairs, ordPair{Key: "any", Value: filterChildrenYAML(*n.Any)})
	}
	if n.Not != nil {
		pairs = append(pairs, ordPair{Key: "not", Value: filterNodeYAML(*n.Not)})
	}
	return orderedMap(pairs...)
}

func filterChildrenYAML(children []generated.VaultFilterNode) *yaml.Node {
	nodes := make([]*yaml.Node, 0, len(children))
	for _, c := range children {
		nodes = append(nodes, filterNodeYAML(c))
	}
	return seq(nodes...)
}

// ---------------------------------------------------------------------------
// The non-filter halves of a view
// ---------------------------------------------------------------------------

// checkProperty answers whether a property name may appear in a view file this
// importer writes, in a COMPARISON position (a filter, a grouping key, a sort
// key, an aggregate target) or a DISPLAY one (`properties`).
//
// It mirrors records.ValidateViewAgainstSchemas' own checkV2Prop, and it has to:
// a name that check rejects makes the loader refuse the WHOLE view file, so a
// name this importer cannot vouch for must become a named loss here instead.
//
// An UNTYPED view checks nothing outside the reserved namespaces, which is not
// an oversight — FR-018b resolves an untyped view's ordinary property names
// over FR-021e's raw rows at query time, so there is no name the loader would
// refuse and therefore none this function may.
func (r leafResolver) checkProperty(name string, comparison bool) (reason string, ok bool) {
	switch {
	case strings.HasPrefix(name, "formula."):
		// FR-140's formulas are a version-2 capability this importer does not
		// yet carry — see doc.go's gap note. A `formula.x` reference with no
		// `formulas:` block beside it makes the loader refuse the whole file
		// (RejectViewUnknownFormula), so it must be a loss here.
		return fmt.Sprintf("computed property %q dropped — this importer does not yet carry a base's `formulas:` block, so there is nothing for the reference to resolve against", name), false
	case records.IsFileNamespace(name):
		if !records.IsFileProperty(name) {
			return fmt.Sprintf("%q is not one of the reserved file properties (%s)", name, strings.Join(records.FilePropertyNames, ", ")), false
		}
		if comparison && name == records.FileSelfProp {
			return fmt.Sprintf("%q is the note itself and is not a comparison target", name), false
		}
		return "", true
	case !r.typed():
		return "", true
	default:
		if _, found := r.schemas.Lookup(r.recordType, name); !found {
			return fmt.Sprintf("not a declared property of %q", r.recordType), false
		}
		return "", true
	}
}

// translateGrouping carries the Base's `groupBy` — INCLUDING its direction,
// which version 1 had no field for and therefore dropped on all 24 of the
// founder's grouped views.
func translateGrouping(raw any, r leafResolver) (nodes []*yaml.Node, losses []string) {
	gb, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}
	prop, _ := gb["property"].(string)
	if prop == "" {
		return nil, nil
	}
	if reason, ok := r.checkProperty(prop, true); !ok {
		return nil, []string{lossf(LossGroupBy, "grouping by %q dropped — %s", prop, reason)}
	}
	pairs := []ordPair{{Key: "property", Value: prop}}
	dir := strings.ToLower(strings.TrimSpace(stringOf(gb["direction"])))
	switch dir {
	case "":
		// No direction declared. ViewGroupBy leaves it optional and the reader
		// states the default, so omitting the key carries the same fact.
	case string(generated.ViewGroupByDirectionAsc):
		pairs = append(pairs, ordPair{Key: "direction", Value: dir})
	case string(generated.ViewGroupByDirectionDesc):
		// CARRIED, and the consequence NAMED. The view file is faithful — the
		// direction is what the operator asked for, and rewriting it to
		// ascending would be the silent flattening FR-109 exists to stop, one
		// field over. But knowledge_find's own request carries no group
		// direction, so pkg/records' view->find bridge refuses to SERVE a
		// descending grouping (ServeRefusalGroupDirection) rather than
		// reordering it silently. That refusal is invisible at import time, so
		// it is reported here: an imported view nobody can apply must not
		// score CLEAN.
		pairs = append(pairs, ordPair{Key: "direction", Value: dir})
		losses = append(losses, lossf(LossGroupBy,
			"grouping %q DESCENDING is carried into the view file faithfully, but a knowledge_find request has no group direction, so applying this view is refused until it does (ServeRefusalGroupDirection) — the groups are not silently reordered ascending",
			prop))
	default:
		losses = append(losses, lossf(LossGroupBy, "direction %q on %q dropped — the only declared group directions are asc and desc", dir, prop))
	}
	return []*yaml.Node{orderedMap(pairs...)}, losses
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// ---------------------------------------------------------------------------
// FR-105 REACHES `limit:` TOO — A ROW-COUNT BOUND DECIDES THE ROW SET
//
// Nothing in this package read a base view's `limit:` at all: ParsedBase kept
// only `filters` and `views`, and the whole package contained one occurrence of
// the word, in a comment. So a base view written `limit: 5` imported UNLIMITED,
// scored CONVERTED, and recorded zero losses — a view that returns fifty rows
// where the operator asked for five, with nothing anywhere to say so.
//
// A limit that is carried into the view file faithfully is NOT a loss; ViewDef
// has the field and this writes it. A limit that cannot be read — a value that
// is not a positive whole number — IS one, and it sits with the filters rather
// than with the annotations, because dropping a bound lets the view return MORE
// rows than the base asked for and that is the one direction FR-105 forbids.
// ---------------------------------------------------------------------------

// translateLimit reads the view's own `limit:`, falling back to the base's
// top-level one — the same composition `filters:` uses, with the more specific
// declaration winning. It returns 0 when no limit was declared at all.
func translateLimit(viewLimit, outerLimit any) (limit int, losses []string) {
	raw, where := viewLimit, "the view's"
	if raw == nil {
		raw, where = outerLimit, "the base's"
	}
	if raw == nil {
		return 0, nil
	}
	n, ok := wholeNumber(raw)
	if !ok || n <= 0 {
		return 0, []string{lossf(LossLimit,
			"%s `limit: %v` could not be read as a positive whole number, so this view imports with NO row bound at all — it will return more rows than the Obsidian original",
			where, raw)}
	}
	return n, nil
}

// wholeNumber reads a YAML scalar as a whole number. yaml.v3 decodes an
// unquoted integer as `int`, but a quoted one arrives as a string and a large
// one can arrive as `int64` or `float64`, so all four are read rather than
// assuming the shape the fixture happened to have.
func wholeNumber(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// translateOrder carries the Base view's `order:` as the view's display
// `properties`.
//
// `file.name` is now CARRIED rather than skipped. Version 1 dropped every
// `file.*` column on the grounds that the note's identity is always shown
// anyway; version 2 has the reserved namespace (FR-018c/FR-130), so the column
// the operator actually asked for is written down instead of assumed.
func translateOrder(raw any, r leafResolver) (props []string, losses []string) {
	ord, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	for _, o := range ord {
		s := strings.TrimSpace(stringOf(o))
		if s == "" {
			continue
		}
		if reason, ok := r.checkProperty(s, false); !ok {
			losses = append(losses, lossf(LossProperties, "column %q dropped — %s", s, reason))
			continue
		}
		props = append(props, s)
	}
	return props, losses
}

func translateSort(raw any, r leafResolver) (nodes []*yaml.Node, losses []string) {
	srt, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	for _, s := range srt {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		prop := stringOf(sm["property"])
		if prop == "" {
			continue
		}
		if reason, ok := r.checkProperty(prop, true); !ok {
			losses = append(losses, lossf(LossSort, "sorting by %q dropped — %s", prop, reason))
			continue
		}
		dir := strings.ToLower(strings.TrimSpace(stringOf(sm["direction"])))
		if dir != string(generated.RecordSortDirectionDesc) {
			// RecordSort.Direction is REQUIRED on the wire and has exactly two
			// values, so an absent or unrecognised direction is written as the
			// ascending default rather than as an empty string the reader
			// would have to interpret.
			dir = string(generated.RecordSortDirectionAsc)
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "property", Value: prop}, ordPair{Key: "direction", Value: dir}))
	}
	return nodes, losses
}

// ---------------------------------------------------------------------------
// FIFTEEN AGGREGATE OPS, NOT FOUR — AND THE OLD REFUSAL QUOTED A RETRACTED RULE
//
// This map used to hold `sum`, `min`, `max`, `count` and its refusal told the
// operator "there is deliberately no avg". RecordAggregate.yaml opens with the
// sentence "THE 'THERE IS DELIBERATELY NO AVG' PARAGRAPH THAT STOOD HERE IS
// SUPERSEDED" and declares fifteen ops, `avg` and `median` among them. So an
// Obsidian `Average` summary was dropped, and the operator was told the reason
// was a product rule that no longer exists.
//
// The source of truth is now the GENERATED enum, and allRecordAggregateOps
// below is asserted against it: an op added to the contract and not reachable
// from here fails TestAggregates_EveryContractOpIsReachable by name rather than
// drifting quietly, which is the same guard the query side already has.
// ---------------------------------------------------------------------------

// allRecordAggregateOps is every op the contract declares. It is the drift
// anchor — the test walks it, not the map below.
var allRecordAggregateOps = []generated.RecordAggregateOp{
	generated.RecordAggregateOpCount,
	generated.RecordAggregateOpSum,
	generated.RecordAggregateOpMin,
	generated.RecordAggregateOpMax,
	generated.RecordAggregateOpAvg,
	generated.RecordAggregateOpMedian,
	generated.RecordAggregateOpStddev,
	generated.RecordAggregateOpRange,
	generated.RecordAggregateOpEarliest,
	generated.RecordAggregateOpLatest,
	generated.RecordAggregateOpChecked,
	generated.RecordAggregateOpUnchecked,
	generated.RecordAggregateOpEmpty,
	generated.RecordAggregateOpFilled,
	generated.RecordAggregateOpUnique,
}

// obsidianAggregateAliases maps the spellings Obsidian's own summary vocabulary
// uses onto ours, folded to lower case. Only names that are genuinely the same
// function under a different word are here — a near-miss is refused by name
// rather than guessed at, because a summary silently computed as the wrong
// function is worse than a summary the operator is told was dropped.
var obsidianAggregateAliases = map[string]generated.RecordAggregateOp{
	"average":  generated.RecordAggregateOpAvg,
	"mean":     generated.RecordAggregateOpAvg,
	"distinct": generated.RecordAggregateOpUnique,
	"notempty": generated.RecordAggregateOpFilled,
	"stdev":    generated.RecordAggregateOpStddev,
}

// aggregateOpFor resolves one `.base` summary function name to a contract op.
// Matching ignores case: Obsidian writes `Sum` and `Average`, we write `sum`
// and `avg`, and the capitalisation is spelling, not meaning.
func aggregateOpFor(raw string) (generated.RecordAggregateOp, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return "", false
	}
	for _, op := range allRecordAggregateOps {
		if string(op) == key {
			return op, true
		}
	}
	op, ok := obsidianAggregateAliases[key]
	return op, ok
}

// aggregateOpNames renders the supported set for a refusal message.
func aggregateOpNames() string {
	names := make([]string, 0, len(allRecordAggregateOps))
	for _, op := range allRecordAggregateOps {
		names = append(names, string(op))
	}
	return strings.Join(names, ", ")
}

func translateSummaries(raw any, r leafResolver) (nodes []*yaml.Node, losses []string) {
	summ, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}
	for _, prop := range sortedKeys(summ) {
		opRaw := stringOf(summ[prop])
		opVal, known := aggregateOpFor(opRaw)
		if !known {
			losses = append(losses, lossf(LossAggregates, "summary %q on %q dropped — this release has no aggregate by that name; the fifteen it does have are %s", opRaw, prop, aggregateOpNames()))
			continue
		}
		op := string(opVal)
		if reason, ok := r.checkProperty(prop, true); !ok {
			losses = append(losses, lossf(LossAggregates, "%s(%s) dropped — %s", op, prop, reason))
			continue
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "op", Value: op}, ordPair{Key: "property", Value: prop}))
	}
	return nodes, losses
}
