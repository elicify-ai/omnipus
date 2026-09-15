// view_translate.go: Translate one imported view definition into the native view model

package vaultimport

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"gopkg.in/yaml.v3"
)

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

	// THE RECORD TYPE IS ONLY KNOWN HERE, AND ONE KIND OF NODE WAS WAITING FOR
	// IT. A base's outer filter is translated ONCE, above the view loop, so a
	// disjunction whose branches name DIFFERENT record types could not be
	// settled there — "either type" is not a fact any single view's `type:` can
	// hold. It is carried as a rawKindTypedAny instead and settled now, against
	// the type this view actually resolved to, which is the missing premise:
	//
	//	(inFolder AND (type=="content" OR type=="brand-kit")) AND type=="content"
	//
	// is `inFolder AND type=="content"` — the disjunction is absorbed by the
	// view's own conjunct. Both trees are reduced under the SAME type because
	// both are conjoined into one filter below, and each returns a rewritten
	// copy: `outer` is shared by every view in this base and must not be
	// reduced in place. A group that does not settle degrades to the same loss,
	// with the same verbatim, that it produced before. See
	// ReduceTypedDisjunctions for why the rewrite is an equivalence.
	outer = ReduceTypedDisjunctions(outer, resolvedType)
	viewTrans = ReduceTypedDisjunctions(viewTrans, resolvedType)

	res := leafResolver{recordType: resolvedType, schemas: schemas, formulas: formulasFor(resolvedType), schema: schemaForType(schemas, resolvedType)}

	// FR-140 — THE AUTHORED-FORMULA PASS, and it must run BEFORE resolution.
	// It is the only thing in this file that adds to the view's `formulas:`
	// block, and every later step reads that block: collectFormulaRefs walks
	// the RESOLVED trees, buildFormulaLeafNode resolves a reference against
	// res.formulas, and formulasYAML writes the sources. So the trees are
	// rewritten and the resolver extended here, once, and nothing downstream
	// needs to know an expression was ever there.
	authored := synthesiseFilterFormulas(&res, pb, baseRelPath, vraw, outer.Root, viewTrans.Root)
	outer.Root = authored.rewrite(outer.Root)
	viewTrans.Root = authored.rewrite(viewTrans.Root)
	vo.AuthoredFormulas = authored.Notes

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
	header := authoredFormulaHeader(vo.AuthoredFormulas) + formulaRewriteHeader(vo.FormulaRewrites)
	bytes := append([]byte(header), body...)
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
	case !records.ViewLayoutIsRendered(generated.ViewDefLayout(layout)):
		// Carried faithfully into the file, but the product draws no part
		// for this layout (only `map`, today) — so the operator is told, by
		// name, that they will see a table. records.ViewLayoutIsRendered is
		// the same authority the renderer's own part-for-layout mapping
		// (records.viewPartForLayout) uses, so this can never again name a
		// layout — like board or calendar — that the SPA actually draws.
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
		// The direction is carried into the view file, and a knowledge_find
		// request can now ASK for it — VaultFindRequest.group_by[].direction
		// landed with the group-direction work, and the bridge serves such a
		// view instead of refusing it. So nothing is lost here any more.
		//
		// This used to append a LossGroupBy saying the view was "refused until
		// [the request] does". That sentence outlived the refusal it described
		// by exactly one commit, which is the shape this package keeps getting
		// wrong: a loss line is a claim about ANOTHER component, and it goes
		// stale silently when that component changes. The `default:` arm below
		// is still a real loss — an unknown spelling is genuinely dropped.
		pairs = append(pairs, ordPair{Key: "direction", Value: dir})
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
