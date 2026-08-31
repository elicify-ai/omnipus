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
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
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
	// FormulaRewrites names every carried formula whose SOURCE this importer
	// changed on the way in, and what changed.
	//
	// IT IS DELIBERATELY NOT A LOSS, and the distinction is the whole reason
	// the field exists rather than another `lossf` call. A loss is something
	// DROPPED, and every loss position that could hold this one is classified
	// row-set-affecting, so recording a rewrite as a loss would DISABLE the
	// view. FR-105 forbids returning MORE rows; both rewrites here return the
	// same rows or fewer (see translate.go's W1/W2 proofs), so disabling the
	// view would be the importer refusing its own faithful translation.
	//
	// It is also not nothing. A view file whose `formulas:` no longer reads
	// character-for-character like the `.base` file it came from is a
	// difference an operator must be able to find without diffing two files,
	// so the same text is written into the view file's own header comment —
	// see formulaRewriteHeader.
	FormulaRewrites []string
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

	// The base's `formulas:` block is translated PER RECORD TYPE, because a
	// formula is typed against the schema of the type its view queries and two
	// views in one base can resolve to two different types. It is cached
	// because the translation is the same work every time for the same type,
	// and because a base's formulas would otherwise be re-parsed and
	// re-validated once per view.
	formulaCache := map[string]FormulaTranslation{}
	formulasFor := func(recordType string) FormulaTranslation {
		if ft, done := formulaCache[recordType]; done {
			return ft
		}
		ft := TranslateFormulas(pb, schemaForType(schemas, recordType))
		formulaCache[recordType] = ft
		return ft
	}

	var produced []ProducedView
	anyNonRefused := false
	anyLossy := false
	for _, vraw := range pb.Views {
		name, _ := vraw["name"].(string)
		slug := slugs.Slug(baseRelPath, name)
		vo, pv := translateOneView(vraw, outerTrans, pb, baseRelPath, slug, schemas, formulasFor)
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

func translateOneView(
	vraw map[string]any,
	outer TreeTranslation,
	pb *ParsedBase,
	baseRelPath, slug string,
	schemas *SchemaIndex,
	formulasFor func(recordType string) FormulaTranslation,
) (ViewOutcome, *ProducedView) {
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

	res := leafResolver{recordType: resolvedType, schemas: schemas, formulas: formulasFor(resolvedType)}

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

	limit, limitLosses := translateLimit(vraw["limit"], pb.Limit)
	losses = append(losses, limitLosses...)

	propConfig, propConfigLosses := translatePropertyConfig(pb, propsOut)
	losses = append(losses, propConfigLosses...)

	// The formulas this view actually names, closed over their own references.
	// Collected from the RESOLVED trees, so a reference inside a lost `or:`
	// group does not pull its formula into the declaration.
	declaredFormulas := res.formulas.Closure(collectFormulaRefs(vraw, outer.Root, viewTrans.Root))
	vo.FormulaRewrites = formulaRewriteNotes(res.formulas, declaredFormulas)

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
	if len(declaredFormulas) > 0 {
		pairs = append(pairs, ordPair{Key: "formulas", Value: formulasYAML(res.formulas, declaredFormulas)})
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
	if propConfig != nil {
		pairs = append(pairs, ordPair{Key: "property_config", Value: propConfig})
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
	body, err := marshalDoc(top)
	bytes := append([]byte(formulaRewriteHeader(vo.FormulaRewrites)), body...)
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
	// formulas is the base's `formulas:` block, translated against THIS view's
	// record type. A `formula.<name>` in any property position resolves against
	// it; a name it does not carry becomes a named loss quoting why the formula
	// itself could not be translated, never a bare "no such formula".
	formulas FormulaTranslation
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
//	     It ALSO flips the polarity every leaf beneath it is judged at — see
//	     resolveTree and v2Leaf.Negated.
func (r leafResolver) resolve(n *rawNode, pos LossPosition) (*generated.VaultFilterNode, []string) {
	node, losses := r.resolveTree(n, pos, false)
	return node, renderLosses(losses)
}

// ---------------------------------------------------------------------------
// A LOSS IS AN EXPRESSION AND A REASON, AND THEY HAVE TO TRAVEL SEPARATELY
//
// They used to be glued together the moment a loss was made, as one rendered
// string. That is why a clause could be dropped with NO stated reason: `!=`
// desugars into a TREE NEGATION over an `=` leaf (nodeFromRawLeaf, and it must
// — `{not: {p,=,v}}` keeps the records where `p` is absent and `{p,<>,v}` drops
// them), so when the leaf underneath cannot be built, the failure is a CHILD's
// and the thing that must be NAMED is the PARENT's text. Reporting the parent
// meant throwing the child's whole diagnosis away. Six clauses across three of
// the founder's bases were reported as gone with nothing said about why.
//
// Keeping the two halves apart until the last moment lets the wrapper report
// its own expression AND the child's reason, which is the FR-107 answer:
//
//	[view filter] realm != "personal" — property "realm" is not declared in …
//
// WHAT THIS DELIBERATELY DOES NOT DO. It changes no node, no loss count and no
// loss position — only the words. That restraint is the point rather than
// modesty: `not:` is where a narrowing becomes a BROADENING. A multi-clause
// `not:` "loses nothing" only because Obsidian ANDs then negates, and the
// negation has no absence rule of its own downstream (knowledge_find evaluates
// `!inner.matched` flat; FR-008's absent-rescue lives on the negative
// OPERATORS in records.PreparedFilter.MatchValue and never reaches a
// COMBINATOR). So a clause that is safely narrower on its own is not safely
// narrower under a negation, and nothing may become newly translatable here
// without a proof at the TREE level. Improving a sentence needs no such proof.
//
// THAT PROOF NOW EXISTS, AND IT IS A MECHANISM RATHER THAN A PROMISE.
// resolveTree carries the POLARITY of the path from the root — flipped by
// every `not:`, preserved by `all` and `any` — down to each leaf as
// v2Leaf.Negated, and buildV2LeafNode refuses by name every translation that
// is only a SUBSET of the Obsidian clause when it finds itself under an odd
// number of negations. A translation that is EXACT (`=` on a declared value,
// `IS NOT NULL` for a truthy test on a type with no falsy value) is unaffected
// at any polarity, which is what keeps the ordinary `p != "done"` desugar —
// itself a `not:` over an `=` leaf — translating exactly as before.
// ---------------------------------------------------------------------------

// resolvedLoss is one named loss before it is rendered: the EXPRESSION the base
// file carried, and separately the diagnosis of why it went.
type resolvedLoss struct {
	Pos    LossPosition
	Expr   string
	Reason string
}

// render writes the loss the way the report reads it — `[position] expression`
// with the reason after a " — " separator when there is one. A loss with no
// reason renders exactly as it always did; splitLossLine then reports no
// reason, and report.go classifies it from the expression's shape.
func (l resolvedLoss) render() string {
	if l.Reason == "" {
		return lossf(l.Pos, "%s", l.Expr)
	}
	return lossf(l.Pos, "%s — %s", l.Expr, l.Reason)
}

func renderLosses(in []resolvedLoss) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, l := range in {
		out = append(out, l.render())
	}
	return out
}

// joinReasons gathers the diagnoses of the children a combinator lost, in the
// order they were written, without repeating one. A child that had no reason
// contributes nothing rather than an empty clause.
func joinReasons(in []resolvedLoss) string {
	seen := map[string]bool{}
	var out []string
	for _, l := range in {
		if l.Reason == "" || seen[l.Reason] {
			continue
		}
		seen[l.Reason] = true
		out = append(out, l.Reason)
	}
	return strings.Join(out, "; ")
}

// resolveTree is resolve's body, working in unrendered losses so that a
// combinator can report its OWN expression with its CHILD's reason.
func (r leafResolver) resolveTree(n *rawNode, pos LossPosition, neg bool) (*generated.VaultFilterNode, []resolvedLoss) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case rawKindLost:
		return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim, Reason: n.Reason}}

	case rawKindPrebuilt:
		return n.Prebuilt, nil

	case rawKindLeaf:
		// The POLARITY travels with the leaf, because the leaf builder is
		// where a translation is chosen and only the walk knows how many
		// `not:` wrappers stand above it. `all` and `any` preserve it: a
		// subset conjunct narrows a conjunction and a subset disjunct
		// narrows a disjunction. `not` inverts it. See v2Leaf.Negated.
		leaf := n.Leaf
		leaf.Negated = neg
		node, reason, ok := buildV2LeafNode(r, leaf)
		if !ok {
			return nil, []resolvedLoss{{Pos: LossFilterLeaf, Expr: describeLeaf(leaf), Reason: reason}}
		}
		return node, nil

	case rawKindAll:
		var kids []generated.VaultFilterNode
		var losses []resolvedLoss
		for _, k := range n.Kids {
			child, childLosses := r.resolveTree(k, pos, neg)
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
		childNeg := neg
		if n.Kind == rawKindNot {
			childNeg = !neg
		}
		for _, k := range n.Kids {
			child, childLosses := r.resolveTree(k, pos, childNeg)
			if child == nil || len(childLosses) > 0 {
				// The group's own verbatim is what is NAMED, not the child's —
				// a reader has to see which `or:`/`not:` block went missing,
				// and a half-named group reads as if the rest survived. The
				// child's REASON rides along, because "which" and "why" are
				// two different questions and only the first was ever answered.
				return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim, Reason: joinReasons(childLosses)}}
			}
			kids = append(kids, *child)
		}
		if len(kids) == 0 {
			return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim}}
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
	return nil, []resolvedLoss{{Pos: pos, Expr: n.Verbatim, Reason: n.Reason}}
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
	if strings.HasPrefix(l.Property, formulaNamespace) {
		return buildFormulaLeafNode(r, l)
	}

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
		//
		// A SUBSET IS ONLY SAFE IN POSITIVE POSITION. Under a `not:` the
		// subset becomes a superset and the same clause returns MORE rows.
		if l.Negated {
			return nil, fmt.Sprintf(
				"`!%s` has no faithful translation inside a `not:`: `IS NULL` is a strict SUBSET of Obsidian's falsy test, which also catches `false`, `0` and `\"\"` — safe at the top level, where a subset only narrows, but a negation inverts it and the clause would return MORE rows than the Obsidian original",
				l.Property), false
		}
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
		// `prop != ""`. FR-007a rules this translation on BOTH sides of its own
		// rule, and they need two different operators.
		//
		// NON-TEXT. The empty string IS the absent state, so Obsidian's
		// idiomatic "is set" is `IS NOT NULL`. It is safe under BOTH readings of
		// what Obsidian does with a property that is not there at all. If
		// `undefined != ""` is TRUE (JavaScript's own answer), Obsidian returns
		// the set-plus-absent notes and ours returns only the set ones —
		// narrower. If Obsidian instead reads an absent property as `""`, the
		// two sets are identical. Neither reading makes ours the larger set,
		// which is the only question FR-105 asks.
		//
		// TEXT, AND WHY THIS IS NO LONGER A REFUSAL. `""` stays a PRESENT value
		// on text, so `IS NOT NULL` genuinely does over-match — it admits the
		// record whose value IS the empty string, which the Obsidian filter
		// excludes. That reasoning was always right ABOUT `IS NOT NULL`, and it
		// was the only operator anyone had asked about. `<>` against the EMPTY
		// LITERAL is a different question with a better answer:
		//
		//	property state        `IS NOT NULL`   `<> ""`
		//	absent                false           false   (§8 R-2: an absent
		//	                                              operand is false for
		//	                                              every operator but
		//	                                              `IS NULL`, and this
		//	                                              leaf carries no
		//	                                              Negate, so FR-008's
		//	                                              re-inclusion — which
		//	                                              is a property of the
		//	                                              negative OPERATOR —
		//	                                              never applies)
		//	present, `""`         TRUE            false   ← the whole defect
		//	present, a value      true            true
		//
		// So `<> ""` selects exactly "present and not the empty string". That is
		// Obsidian's `!= ""` outright under the reading where an absent property
		// is not `!= ""`, and a strict SUBSET under the JavaScript reading —
		// the same two-reading proof the non-text branch already stands on, and
		// neither reading makes ours the larger set.
		//
		// THE EMPTY LITERAL IS EXPRESSIBLE, which is the other half of why this
		// works. `VaultFilterNode.value` carries no `minLength`; knowledgefind's
		// buildLeaf sets `LiteralGiven` from `value != nil` rather than from the
		// string being non-empty; and records.Filter.LiteralGiven exists in as
		// many words because "the empty string is a legitimate value for `=`".
		// A view's write-time validation checks enum literals only, so a text
		// `<> ""` passes it.
		//
		// MANY IS COVERED, not excluded by luck: `=`/`<>` are element-wise on a
		// many property (R-9), so `<> ""` there means "has a non-empty element",
		// which is again a subset of what JavaScript's list-to-string coercion
		// would answer.
		if !r.typed() {
			return nil, "an UNTYPED view cannot carry `!= \"\"`: on a text property `<> \"\"` is the faithful operator and on every other type it is `IS NOT NULL` (FR-007a), the two select different rows, and with no declared type there is nothing to choose between them", false
		}
		if prop.Type == records.TypeText {
			if l.Negated {
				return nil, fmt.Sprintf(
					"`%s != \"\"` has no faithful translation on a TEXT property inside a `not:`: outside a negation it is exactly `%s <> \"\"` — present and not the empty string — but that is at best a SUBSET of the Obsidian clause, and a `not:` inverts a subset into a superset, re-admitting every record that never declared %s and returning MORE rows than the Obsidian original",
					l.Property, l.Property, l.Property), false
			}
			return valueNode(l.Property, generated.LessThanGreaterThan, ""), "", true
		}
		if l.Negated {
			return nil, fmt.Sprintf(
				"`%s != \"\"` is `IS NOT NULL` on a %s property (FR-007a makes `\"\"` the absent state there), which is at best a SUBSET of the Obsidian clause — safe at the top level, where a subset only narrows, but a `not:` inverts it and the clause would return MORE rows than the Obsidian original",
				l.Property, prop.Type), false
		}
		return opNode(l.Property, generated.ISNOTNULL), "", true

	case shapeIsEmpty:
		// `prop == ""`, the mirror case, and it splits the same way.
		//
		// TEXT: `= ""` is exactly "present and empty" — absent is false (§8
		// R-2), `""` is true, a value is false. Obsidian's `== ""` is that same
		// set under the JavaScript reading and that set PLUS the absent records
		// under the other, so ours is never the larger one.
		//
		// EVERY OTHER TYPE: still refused, and now for a reason that has been
		// checked rather than assumed. `IS NULL` over-matches — it also matches
		// a record that never declared the property, which the Obsidian
		// comparison does not — and there is no literal-comparison path either:
		// FR-007a makes `""` the ABSENT state on a date, integer, decimal,
		// enum, relation, person or checkbox, so `""` is not a value any of
		// those types can hold and Filter.Validate refuses the literal through
		// the same ParseValue a note's own value goes through. Both operators
		// are exhausted, not just the obvious one.
		if !r.typed() {
			return nil, "an UNTYPED view cannot carry `== \"\"`: on a text property `= \"\"` is the faithful operator, and on every other type the empty string is not a value the type can hold at all (FR-007a), so with no declared type there is nothing to choose between them", false
		}
		if prop.Type == records.TypeText {
			if l.Negated {
				return nil, fmt.Sprintf(
					"`%s == \"\"` has no faithful translation on a TEXT property inside a `not:`: outside a negation it is exactly `%s = \"\"` — present and empty — but a `not:` over it also admits every record that never declared %s, and Obsidian's own `== \"\"` may already count those in, so the negated clause can return MORE rows than the Obsidian original",
					l.Property, l.Property, l.Property), false
			}
			return valueNode(l.Property, generated.Equal, ""), "", true
		}
		return nil, fmt.Sprintf(
			"`%s == \"\"` has no faithful translation on a %s property: `IS NULL` also matches a record that never declared %s, which the Obsidian comparison does not, so it would return MORE rows than the original — and no literal-comparison path exists either, because FR-007a makes `\"\"` the ABSENT state on a %s, so it is not a value the type can hold and the engine refuses the literal",
			l.Property, prop.Type, l.Property, prop.Type), false

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
	case strings.HasPrefix(name, formulaNamespace):
		// FR-140: a query reaches a formula ONLY as `formula.<name>`, resolved
		// against the saved view's own block. The reference is legal exactly
		// when this view will declare that formula — anything else makes the
		// loader refuse the WHOLE file (RejectViewUnknownFormula), so a name
		// that did not translate has to become a named loss here instead.
		ref := strings.TrimPrefix(name, formulaNamespace)
		if _, carried := r.formulas.Declared(ref); !carried {
			return r.formulas.RefusalFor(ref), false
		}
		return "", true
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

// ---------------------------------------------------------------------------
// A SUMMARY'S PROPERTY HAS A TYPE, AND UNTIL NOW NOBODY HERE ASKED WHAT IT WAS
//
// checkProperty answers ONE question: does this name resolve. That is the right
// question for a column and for a sort. It is not enough for a summary, because
// the query engine gates a summary on the property's TYPE as well:
// knowledgefind's aggregate branch refuses an op that the property's type does
// not define (FR-155, opsDefinedFor).
//
// And that refusal is not local. `refuse` aborts the WHOLE find request — not
// just the total — so ONE undefined summary makes every row of the view
// unreachable. A view carrying `sum` over a text property does not show a wrong
// number and it does not show a blank total: it shows nothing at all, with a
// refusal, and it does so for every caller for ever. Writing such a file is
// strictly worse than dropping the summary and naming the loss, because the
// loss is annotation-positioned (loss.go) and costs no rows, while the file
// costs all of them.
//
// THE CHECK ASKS knowledgefind's OWN TABLE, and that is the load-bearing part
// of this rather than the four lines below. Restating "sum is for numbers" here
// would put a second copy of the op/type mapping in the writer, and a second
// copy is the mechanism by which the writer and the reader drifted apart in the
// first place — which is the defect this gate closes, not just this instance of
// it. If FR-150 gains an op or moves one between domains, the importer follows
// automatically because it never knew the mapping to begin with.
//
// WHAT IS DELIBERATELY NOT GATED, and why that is a scope line rather than an
// oversight:
//
//   - An UNTYPED (folder-scoped) view. There is no record type to ask about, so
//     there is no type to check against; the engine resolves such a property
//     from whatever the notes themselves declare, which this importer cannot
//     know at write time.
//   - `formula.*` and `file.*`. Both resolve through knowledgefind's namespace
//     rather than through a schema, and neither's op/type mapping is reachable
//     from here without exporting more of that package's internals. The vault
//     that motivated this gate carries exactly one such summary
//     (`sum(formula.monthly_cost)`, a number-valued formula, which the engine
//     accepts), so extending the gate there is a real but separate piece of
//     work — recorded here rather than silently assumed away.
// ---------------------------------------------------------------------------

// summaryTypeGapToken is the phrase report.go's closed gap table classifies
// this loss by, DECLARED HERE — in the file that emits it — rather than written
// out a second time over there.
//
// READ THIS BEFORE REWORDING THE MESSAGE BELOW. report.go recognises a loss by
// matching SUBSTRINGS of the sentence this package writes, so improving a
// message for the founder can silently empty a bucket in the summary that reads
// it, ACROSS AN OWNERSHIP BOUNDARY, with nothing failing to say so. Three
// separate agents hit that coupling in one day on three unrelated changes; each
// stopped and asked, and none of them could have known from this side that
// there was anything to ask about.
//
// Naming the token here is the smallest thing that changes that. report.go's
// row refers to this constant instead of repeating the phrase, so the coupling
// is a symbol a compiler and a reader can both see, and
// TestSummaryGate_TheEmittedLossCarriesTheTokenReportGoClassifiesBy fails in
// THIS package if the emitted sentence stops containing it. Reword freely
// around it; keep the token in the sentence, or change the constant and let the
// classifier follow.
const summaryTypeGapToken = "and the summaries defined for"

// summaryDefinedForType reports whether op is defined over the DECLARED type of
// prop, and if it is not, the reason in the same words the engine would use.
func (r leafResolver) summaryDefinedForType(op, prop string) (reason string, ok bool) {
	if !r.typed() {
		return "", true
	}
	if strings.HasPrefix(prop, formulaNamespace) || records.IsFileNamespace(prop) {
		return "", true
	}
	p, found := r.schemas.Lookup(r.recordType, prop)
	if !found {
		// checkProperty has already refused this name and named its own
		// reason; reaching here would mean the two disagree.
		return "", true
	}
	if knowledgefind.SummaryOpDefinedForType(op, p.Type) {
		return "", true
	}
	return fmt.Sprintf(
		"%s is a %s property, "+summaryTypeGapToken+" %s are %s — a view carrying this one is REFUSED IN FULL by knowledge_find (the refusal aborts the whole request, not just the total), so it is dropped here and named instead. Declaring the property's real type with `knowledge_configure set schema %s property %s type=<…>` turns it back on",
		prop, p.Type, p.Type, strings.Join(knowledgefind.SummaryOpsDefinedFor(p.Type), ", "), r.recordType, prop), false
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
		if reason, ok := r.summaryDefinedForType(op, prop); !ok {
			losses = append(losses, lossf(LossAggregates, "%s(%s) dropped — %s", op, prop, reason))
			continue
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "op", Value: op}, ordPair{Key: "property", Value: prop}))
	}
	return nodes, losses
}

// ---------------------------------------------------------------------------
// COMPUTED PROPERTIES — the base's `formulas:` block, carried
// ---------------------------------------------------------------------------

// buildFormulaLeafNode builds a filter leaf comparing against a COMPUTED
// property, or refuses it by name.
//
// The declared type comes from the translated formula rather than from a
// schema, which is the whole reason this is a separate function: `formula.*` is
// not scoped to a record type, so SchemaIndex.Lookup cannot answer for it. It
// is the same declaration the query path builds — namespace.go gives each
// formula ONE stable *Property wearing its inferred type — so a literal checked
// here is a literal that will compare there.
//
// THE LITERAL IS CHECKED, AND THAT IS NOT BELT-AND-BRACES. An unparseable
// literal is not refused at load: ValidateViewAgainstSchemas checks the NAME
// resolves, not the value's shape. It would surface only at query time, as a
// non-conforming comparison that is FALSE for every record — a view that
// returns nothing and looks exactly like a view whose filter matched nothing.
// Refusing it here turns a silent empty answer into a named loss.
func buildFormulaLeafNode(r leafResolver, l v2Leaf) (*generated.VaultFilterNode, string, bool) {
	name := strings.TrimPrefix(l.Property, formulaNamespace)
	decl, carried := r.formulas.Declared(name)
	if !carried {
		return nil, r.formulas.RefusalFor(name), false
	}
	if l.Shape != shapeCompare {
		return nil, fmt.Sprintf("only a direct comparison against %q is translated; this importer does not read the other Obsidian filter idioms on a computed property", l.Property), false
	}
	if decl.Arity == records.ArityMany && isOrderingOp(l.Op) {
		// §8 R-13 again, one namespace over: the ordering operators are
		// undefined against a list, and emitting one produces a clause the
		// engine refuses per record — an empty view rather than a named loss.
		return nil, fmt.Sprintf("operator %q is not defined on %q, whose result is a LIST (spec §8 R-13 leaves the ordering operators undefined for a list)", string(l.Op), l.Property), false
	}
	if reason, ok := formulaLiteralFits(decl, l.Value); !ok {
		return nil, reason, false
	}
	return valueNode(l.Property, l.Op, l.Value), "", true
}

// formulaLiteralFits reports whether a filter literal can be read as the
// formula's declared result type.
//
// A TYPE THIS FUNCTION CANNOT CHECK IS REFUSED, not waved through. `date` and
// `link` results are legal formula types that the founder's vault never
// compares against, and accepting a literal for one without checking it would
// be exactly the silently-empty view the caller's doc comment describes. A
// refusal here costs a named loss on a shape that does not occur; accepting
// blind would cost a wrong answer on a shape that might.
func formulaLiteralFits(decl records.FormulaDecl, literal string) (string, bool) {
	switch decl.Type {
	case records.FormulaNumber:
		if _, err := records.ParseDecimal(strings.TrimSpace(literal)); err != nil {
			return fmt.Sprintf("%q is not a number, and formula %q produces one — the comparison would be non-conforming for every record, which reads as an empty view rather than as a translation this importer could not make", literal, decl.Name), false
		}
		return "", true
	case records.FormulaBoolean:
		switch strings.ToLower(strings.TrimSpace(literal)) {
		case "true", "false":
			return "", true
		}
		return fmt.Sprintf("%q is not `true` or `false`, and formula %q produces a truth value", literal, decl.Name), false
	case records.FormulaText:
		return "", true
	}
	return fmt.Sprintf("this importer does not compare against a %s-valued formula (%q); the comparison is recorded here rather than written as a clause that could not match", decl.Type, decl.Name), false
}

// collectFormulaRefs gathers every `formula.<name>` this view names, in any
// position, so the view declares exactly the formulas it uses (FR-146's budget
// — see FormulaTranslation.Closure).
//
// It reads the RESOLVED filter trees rather than the raw YAML, so a reference
// inside an `or:` group that was itself lost does not drag its formula into the
// declaration. Everything else is read from the raw view, because a column or a
// summary is a plain string either way.
func collectFormulaRefs(vraw map[string]any, trees ...*rawNode) []string {
	seen := map[string]bool{}
	add := func(name string) {
		if strings.HasPrefix(name, formulaNamespace) {
			seen[strings.TrimPrefix(name, formulaNamespace)] = true
		}
	}
	var walk func(*rawNode)
	walk = func(n *rawNode) {
		if n == nil {
			return
		}
		if n.Kind == rawKindLeaf {
			add(n.Leaf.Property)
		}
		for _, k := range n.Kids {
			walk(k)
		}
	}
	for _, t := range trees {
		walk(t)
	}
	if gb, ok := vraw["groupBy"].(map[string]any); ok {
		add(stringOf(gb["property"]))
	}
	if ord, ok := vraw["order"].([]any); ok {
		for _, o := range ord {
			add(strings.TrimSpace(stringOf(o)))
		}
	}
	if srt, ok := vraw["sort"].([]any); ok {
		for _, e := range srt {
			if sm, isMap := e.(map[string]any); isMap {
				add(stringOf(sm["property"]))
			}
		}
	}
	if summ, ok := vraw["summaries"].(map[string]any); ok {
		for k := range summ {
			add(k)
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// formulasYAML renders the view's `formulas:` map — name to SOURCE TEXT
// (FR-141), which is what makes it diffable against the `.base` file it came
// from.
func formulasYAML(ft FormulaTranslation, names []string) *yaml.Node {
	pairs := make([]ordPair, 0, len(names))
	for _, n := range names {
		pairs = append(pairs, ordPair{Key: n, Value: ft.Sources[n]})
	}
	return orderedMap(pairs...)
}

// formulaRewriteNotes renders, for the view's own outcome and header comment,
// what changed about each carried formula's source.
func formulaRewriteNotes(ft FormulaTranslation, names []string) []string {
	var out []string
	for _, n := range names {
		note, changed := ft.Rewritten[n]
		if !changed {
			continue
		}
		out = append(out, fmt.Sprintf("formula %q was rewritten: %s", n, note))
	}
	return out
}

// formulaRewriteHeader renders the rewrite notes as the produced file's header
// comment.
//
// THE FILE IS WHERE THE CORRECTION IS MADE, which is the same argument
// schema_write.go makes for provisioning: a console report scrolls away, and an
// operator comparing this view against the `.base` file it came from is holding
// the file, not the report.
func formulaRewriteHeader(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# GENERATED BY `omnipus records import`.\n")
	b.WriteString("#\n")
	b.WriteString("# THE FORMULA SOURCES BELOW ARE NOT CHARACTER-FOR-CHARACTER THE `.base` FILE'S.\n")
	b.WriteString("# Obsidian's formulas are JavaScript expressions; these are typed expressions\n")
	b.WriteString("# over declared properties, where absence propagates and `if` takes a boolean.\n")
	b.WriteString("# Each rewrite below returns the same value on every record as the original,\n")
	b.WriteString("# or no value where the original showed nothing — never a value where the\n")
	b.WriteString("# original had none.\n")
	b.WriteString("#\n")
	for _, n := range notes {
		for _, wrapped := range wrapComment(n, 76) {
			b.WriteString("# ")
			b.WriteString(wrapped)
			b.WriteString("\n")
		}
	}
	b.WriteString("#\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// FR-018b — the base's top-level `properties:` DISPLAY block
//
// This block was dropped SILENTLY, which is worse than dropping it loudly and
// is the reason it is handled here at all. It is not translated, not refused
// and not named anywhere, so a base whose only untranslatable content was its
// column headings scored CLEAN — a green number over an undetected loss, which
// is the exact failure this importer exists to make impossible.
//
// It is PURE PRESENTATION. ViewPropertyConfig's contract says so in as many
// words, and says why it matters: a display name is never usable in a filter, a
// sort, a grouping or a formula, so carrying one — or dropping one — can never
// change which records a view returns. That is why every loss below sits at an
// annotation position and none of them disables a view.
// ---------------------------------------------------------------------------

// translatePropertyConfig carries the base's `properties:` block into the
// view's `property_config`, dropping — by name — the entries it cannot.
//
// IT IS SCOPED TO THE COLUMNS THIS VIEW ACTUALLY SHOWS, and the scoping is the
// difference between a report and a pile. Obsidian declares the block ONCE per
// base and every view in the file shares it, so Compliance.base's two headings
// are visible to three views of which one displays neither. Reporting a
// "dropped heading" on a view that was never going to render that column is
// not a loss — nothing was lost — and doing it per view multiplied 14 bases'
// small blocks into 72 loss lines that told a reader nothing.
//
// So the rule is exactly: a heading is carried when its column SURVIVED into
// this view's display list, and reported when the column survived and the
// heading could not follow it. A heading whose COLUMN was dropped needs no
// second line — the column's own loss already names it, and saying it twice
// implies two separate things went missing.
func translatePropertyConfig(pb *ParsedBase, displayed []string) (node *yaml.Node, losses []string) {
	if pb == nil || len(pb.PropertyConfigNames) == 0 || len(displayed) == 0 {
		return nil, nil
	}
	shown := make(map[string]bool, len(displayed))
	for _, d := range displayed {
		shown[d] = true
	}
	var pairs []ordPair
	for _, name := range pb.PropertyConfigNames {
		if !shown[name] {
			continue
		}
		cfg, ok := pb.PropertyConfig[name]
		if !ok {
			continue
		}
		for _, unknown := range cfg.UnknownKeys {
			losses = append(losses, lossf(LossProperties,
				"display setting %q on column %q dropped — this release's per-column presentation (`property_config`) carries a display name and nothing else",
				unknown, name))
		}
		if cfg.DisplayName == "" {
			// An entry that declared no readable `displayName` at all — an
			// Obsidian key this importer does not carry, or a value that was
			// not a mapping. Named, because a `properties:` entry that
			// produced nothing must not vanish between the file and the report.
			if len(cfg.UnknownKeys) == 0 {
				losses = append(losses, lossf(LossProperties,
					"the base configures column %q under `properties:` but declares no display name this importer can read", name))
			}
			continue
		}
		pairs = append(pairs, ordPair{Key: name, Value: orderedMap(ordPair{Key: "display_name", Value: cfg.DisplayName})})
	}
	if len(pairs) == 0 {
		return nil, losses
	}
	return orderedMap(pairs...), losses
}

// schemaForType renders one inferred record type as the schema file this
// import will WRITE, and parses it back.
//
// IT GOES THROUGH THE REAL RENDERER AND THE REAL PARSER ON PURPOSE. A formula
// is typed against a property's declared type, so a second, hand-built model of
// the schema here could type a formula against `decimal` that the loader later
// types against `text` — a formula accepted at import and refused on load, or
// worse, one accepted by both and evaluated differently. Rendering the same
// bytes schema_write.go emits and parsing them with the same parser
// records.LoadSchemas uses makes that disagreement unrepresentable.
//
// A nil result is the honest answer for an untyped view (and for a type with no
// inferred schema): SchemaFormulaEnv documents nil as "refuse every property
// operand", so a formula naming a property is refused rather than guessed.
func schemaForType(si *SchemaIndex, recordType string) *records.Schema {
	if si == nil || recordType == "" {
		return nil
	}
	byName, ok := si.byType[recordType]
	if !ok {
		return nil
	}
	props := make([]InferredProperty, 0, len(byName))
	for _, p := range byName {
		props = append(props, p)
	}
	sort.Slice(props, func(i, j int) bool { return props[i].Name < props[j].Name })
	data, err := RenderSchemaYAML(recordType, props)
	if err != nil {
		return nil
	}
	schema, rejection := records.ParseSchema(recordType+".yaml", data)
	if rejection != nil {
		return nil
	}
	return schema
}

// formulaNamespace is FR-140's reserved prefix for a computed property.
//
// It is spelled here rather than imported because pkg/records keeps its own
// copy unexported (view.go's viewFormulaNamespace) and pkg/records/knowledgefind
// keeps a third (FormulaNamespace). TestFormulaNamespace_MatchesTheLoaders
// asserts this constant against the loader's behaviour rather than against
// either spelling, so a divergence fails by name instead of producing a view
// whose references the loader refuses.
const formulaNamespace = "formula."
