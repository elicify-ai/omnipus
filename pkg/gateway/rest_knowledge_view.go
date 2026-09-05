// Omnipus — GET /api/v1/library/{workspace_id}/knowledge/view: evaluate a
// saved view and return everything the SPA needs to draw it
// (view-kinds-design-2026-09-03 §7).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS, AND THE TWO RULES IT ENFORCES
//
// The endpoint resolves a saved view by name inside one in-scope knowledge
// base and EVALUATES it — through the same engine knowledge_find uses
// (knowledgefind.Find over vaultprops.OpenFindEnv, the moved body of
// FindTool.buildDeps), never through a second query engine. What this file
// adds on top of the engine's answer is the view-kinds renderer contract:
//
//   G2 — a number with a companion unit (Property.UnitProperty) totals ONCE
//   PER UNIT VALUE, never across units. Every total this file computes is a
//   list of gen.ViewUnitTotal, keyed by (property, unit value) — and where
//   the unit CANNOT be resolved at all (an untyped view, FR-018b, totalling
//   a property some schema pairs with a unit), the total is REFUSED rather
//   than computed unit-less, because "unit-less" over unit-carrying rows IS
//   the combined figure (unitPropertyForTotals, the gate every aggregation
//   passes first).
//
//   G3 — a row whose unit is missing or unconfirmed is SHOWN (it stays in
//   `rows`), EXCLUDED from every total, and COUNTED in the owning scope's
//   excluded_count with the reason spelled out.
//
// THE VALUES ARE READ FROM THE ENGINE'S OWN RENDERED CELLS, deliberately.
// VaultFindCell.Value is the exact decimal text (renderTyped renders
// Number.String() with thousands separators; multi-values join with ", "),
// so parsing here is the mechanical inverse of one known renderer — strip
// the separators, split on ", " — rather than a second read of the vault
// that could disagree with what the rows on screen say.
//
// A VIEW THAT CANNOT ANSWER IS A 200 WITH `refusal` SET — the wire form of
// records.ViewServeRefusal, the same shape knowledge_describe's "NOT
// SERVABLE by knowledge_find" line reads — never a blank panel and never a
// transport error. An out-of-scope collection_id answers exactly like an
// unknown view (FR-052/FR-053): the error channel must not confirm what
// exists in another workspace.
// ---------------------------------------------------------------------------

// maxViewResultRows bounds how many rows one view result will carry. Above it
// the result reports rows_truncated and computes NO totals — a total over a
// truncated set is a wrong number that looks right, which is the one output
// this surface exists to make impossible.
//
// It is a var, not a const, ONLY so a test can lower it: proving the cap is
// EXACT (2000 rows carried, not 2199) needs a corpus one row past it, and a
// 2001-note fixture would make the proof too slow to run and therefore not
// run. Nothing in production writes it.
var maxViewResultRows = 2000

// viewResultFind is the seam every evaluation in this file goes through.
//
// It is knowledgefind.Find and nothing else. It exists so a test can COUNT the
// evaluations one request makes — the quantity the offset-paging defect was
// about, and one that no field of the response reports. Behaviour that cannot
// be observed cannot be regression-tested, and this endpoint's cost was
// previously ten full re-evaluations of the whole query per request.
var viewResultFind = knowledgefind.Find

// viewExcluded is one scope's G3 exclusions, SPLIT BY CAUSE.
//
// A row with NO unit and a row with TWO are both rightly excluded — neither
// has confirmed which unit its number is in — but they are excluded for
// opposite reasons with opposite fixes: fill one in, or pick one of two.
// Reporting both as "no confirmed currency value" told an operator to supply a
// value that was already there twice, which is a footer that costs more than
// it explains.
type viewExcluded struct {
	missing   []string
	ambiguous []string
}

func (e *viewExcluded) add(other viewExcluded) {
	e.missing = append(e.missing, other.missing...)
	e.ambiguous = append(e.ambiguous, other.ambiguous...)
}

// viewResultExcludedReason writes the G3 footer line for one scope, naming
// each cause with its OWN count.
func viewResultExcludedReason(missing, ambiguous int, unitProps []string) string {
	prop := strings.Join(unitProps, "/")
	rowsOf := func(n int) string {
		if n == 1 {
			return "1 row"
		}
		return fmt.Sprintf("%d rows", n)
	}
	var causes []string
	if missing > 0 {
		causes = append(causes, fmt.Sprintf("%s has no confirmed %s value", rowsOf(missing), prop))
	}
	if ambiguous > 0 {
		causes = append(causes, fmt.Sprintf("%s records more than one %s value, so which one its number is in is not confirmed",
			rowsOf(ambiguous), prop))
	}
	return fmt.Sprintf("%s excluded from every total (G3), still shown: %s",
		rowsOf(missing+ambiguous), strings.Join(causes, "; "))
}

func (a *restAPI) handleKnowledgeViewResult(w http.ResponseWriter, r *http.Request, workspaceID string) {
	q := r.URL.Query()
	collectionID := strings.TrimSpace(q.Get("collection_id"))
	if collectionID == "" {
		jsonErr(w, http.StatusBadRequest, "collection_id is required")
		return
	}
	viewName := strings.TrimSpace(q.Get("view"))
	if viewName == "" {
		jsonErr(w, http.StatusBadRequest, "view is required")
		return
	}

	// US-9/FR-053: an out-of-scope collection is answered exactly like an
	// unknown view — resolved BEFORE the rate limiter so the two cannot be
	// told apart by timing a 429 either (the same ordering the search
	// endpoint documents).
	col, inScope := a.resolveScopedCollection(workspaceID, collectionID)
	if !inScope {
		jsonOK(w, viewResultRefusedUnknown(viewName))
		return
	}

	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	env, closeEnv, err := vaultprops.OpenFindEnv(r.Context(), a.homePath, col)
	defer closeEnv()
	if err != nil {
		logger.ErrorCF("rest", "knowledge: open view environment",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	jsonOK(w, buildViewResult(r.Context(), env, viewName))
}

// ---------------------------------------------------------------------------
// Refusals
// ---------------------------------------------------------------------------

// newViewResult is the empty-but-honest base every answer builds on: the
// required arrays are real empty arrays, never nil, so no code path can
// marshal a null where the contract promises [].
func newViewResult(name string) gen.ViewResult {
	return gen.ViewResult{
		View:     name,
		Label:    name,
		Parts:    []gen.ViewResultPart{},
		Rows:     []gen.VaultFindRow{},
		Problems: []gen.RecordProblem{},
	}
}

func viewResultRefused(out gen.ViewResult, code, reason, remedy string) gen.ViewResult {
	out.Refusal = &gen.ViewResultRefusal{Code: code, Reason: reason, Remedy: remedy}
	out.Complete = false
	rs := reason
	out.CompleteReason = &rs
	return out
}

// viewResultRefusedUnknown is the one refusal two different facts share on
// purpose: "no such view in this collection" and "no such collection in this
// workspace's scope" must be indistinguishable (FR-052/FR-053).
func viewResultRefusedUnknown(name string) gen.ViewResult {
	return viewResultRefused(newViewResult(name),
		string(records.ServeRefusalUnknownView),
		fmt.Sprintf("no saved view named %q is addressable in this workspace", name),
		"call knowledge_describe include=views to see the saved views in scope")
}

// ---------------------------------------------------------------------------
// The builder
// ---------------------------------------------------------------------------

type viewResultBuilder struct {
	ctx  context.Context
	env  vaultprops.FindEnv
	view *records.SavedView
	// parts is the resolved part stack, held so the row collection can ask the
	// engine for the grouping the stack actually needs.
	parts     []gen.ViewPart
	sel       *[]string
	rowByPath map[string]*gen.VaultFindRow
	// groupCache caches full-evaluated-set groupings by signature, so a part
	// whose grouping matches the view's own reuses the base call's groups.
	groupCache map[string][]gen.VaultFindGroup
	truncated  bool
	// snapshotEpoch is the properties-index generation the ROW evaluation ran
	// against. Every later evaluation this request makes is checked against
	// it, so a grouping computed over a re-indexed vault can never be paired
	// with rows from before the reindex — the Count=N beside an N-1 subtotal
	// case. Zero means the engine reported no epoch, and the check is skipped
	// rather than passed on a coincidence of zeroes.
	snapshotEpoch int64
	// refusedUnitProps dedupes the untyped-view G2 refusal: one problem per
	// property per result, however many parts and groups total it.
	refusedUnitProps map[string]bool
	// refusedG4Props is the same dedupe for G4 — a property refused as
	// non-arithmetic says so once, not once per part, group and cell.
	refusedG4Props map[string]bool
	// refusedUnitStamps is the same dedupe for the unit-authority
	// disagreement between a part's stamp and the schema's declaration.
	refusedUnitStamps map[string]bool
	// refusedLegacyAggs is the same dedupe for a legacy `aggregates:` entry
	// withheld because the engine computes it across units.
	refusedLegacyAggs map[string]bool
	// formulas is the view's own computed properties, validated lazily and at
	// most once (a formula's static type is inferred from its expression, so
	// it cannot be read off the file).
	formulas         *records.FormulaSet
	formulasResolved bool
	out              *gen.ViewResult
}

func buildViewResult(ctx context.Context, env vaultprops.FindEnv, name string) gen.ViewResult {
	out := newViewResult(name)

	v, ok := env.Views.Get(name)
	if !ok {
		// A view that EXISTS on disk but was refused at load is reported by
		// its rejection, not as "unknown" — a statement this endpoint could
		// disprove is a statement it must not make.
		if env.ViewReport != nil {
			for _, rej := range env.ViewReport.Rejections {
				if rej.Name == name {
					return viewResultRefused(out, string(rej.Code), rej.Reason,
						"fix the view file and re-save; knowledge_configure re-validates on write")
				}
			}
		}
		return viewResultRefusedUnknown(name)
	}

	out.Label = v.DisplayLabel()
	if v.Def.Kind != nil {
		k := string(*v.Def.Kind)
		out.Kind = &k
	}
	if v.Def.Type != nil {
		t := *v.Def.Type
		out.Type = &t
	}

	// The SAME refusal knowledge_find would give, from the SAME loader —
	// today that is only records.ServeRefusalDisabled (FR-105), but this
	// reads the refusal generically so a new code arrives here without
	// anyone remembering to come back (the renderViewServeRefusal pattern).
	loader := records.NewViewFindLoader(env.Views)
	if refusal, refused := loader.ServeRefusal(name); refused {
		return viewResultRefused(out, string(refusal.Code), refusal.Reason, refusal.Remedy)
	}

	parts, drawable := v.EffectiveParts()
	if !drawable {
		layout := ""
		if v.Def.Layout != nil {
			layout = string(*v.Def.Layout)
		}
		return viewResultRefused(out, "no_drawable_parts",
			fmt.Sprintf("view %q asks for layout %q, which has no drawable part equivalent, and declares no parts of its own", name, layout),
			"give the view an explicit `parts` stack through knowledge_configure, or use a layout with a drawable part")
	}

	b := &viewResultBuilder{
		ctx:        ctx,
		env:        env,
		view:       v,
		rowByPath:  map[string]*gen.VaultFindRow{},
		groupCache: map[string][]gen.VaultFindGroup{},
		parts:      parts,
		out:        &out,
	}
	b.sel = b.buildSelect(parts)

	if refused := b.collectRows(name); refused != nil {
		return *refused
	}

	for _, src := range parts {
		out.Parts = append(out.Parts, b.buildPart(src))
	}
	return out
}

// buildSelect widens the view's own column selection so every property a part
// aggregates or binds is present as a cell on every row. When the view names
// NO `properties`, the engine renders every declared property already and
// nothing needs widening — nil keeps that default.
func (b *viewResultBuilder) buildSelect(parts []gen.ViewPart) *[]string {
	if b.view.Def.Properties == nil {
		return nil
	}
	seen := map[string]bool{}
	sel := []string{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		sel = append(sel, name)
	}
	for _, p := range *b.view.Def.Properties {
		add(p)
	}
	addPtr := func(p *string) {
		if p != nil {
			add(*p)
		}
	}
	for _, part := range parts {
		if part.Properties != nil {
			for _, p := range *part.Properties {
				add(p)
			}
		}
		addPtr(part.Number)
		addPtr(part.Unit)
		addPtr(part.Date)
		addPtr(part.Choice)
		addPtr(part.Image)
		if part.Number != nil {
			add(b.unitPropertyOf(*part.Number))
		}
		if part.Subtotals != nil {
			for prop := range *part.Subtotals {
				add(prop)
				add(b.unitPropertyOf(prop))
			}
		}
	}
	return &sel
}

// collectRows evaluates the view ONCE and keeps every row it is allowed to
// carry. It returns a refusal result when the engine refuses, and nil on
// success.
//
// IT IS ONE EVALUATION, AND THAT IS THE POINT. This used to walk the engine's
// OFFSET cursor a page at a time, and an offset cursor is not a resumable
// stream: every page is a fresh Find() that re-runs the WHOLE evaluation —
// candidate selection, filter, sort, aggregate over the full set — and then
// discards everything before the offset. Ten pages therefore cost ten complete
// evaluations of one query per HTTP request, and the engine's 4 kB response
// budget (sized for a language model's context window, not for an HTTP body)
// trimmed each page as well, so a view over a few hundred records could not
// reach its own row cap however many pages it took, and reported itself
// INCOMPLETE after carrying every row.
//
// Deps.RenderRows is what makes one call sufficient: the caller states the
// bound it can actually take and the engine answers within it, capping the
// page there instead of at MaxLimit and skipping a byte budget written for a
// different consumer. The request asks for ONE MORE row than will be kept,
// which is how "there are more" is distinguished from "that was all" without
// a second round trip — and the extra row is dropped, so the cap is EXACT
// rather than a floor the last page overshoots from.
func (b *viewResultBuilder) collectRows(name string) *gen.ViewResult {
	// One past the cap: the extra row is the truncation detector and is never
	// carried.
	limit := maxViewResultRows + 1
	req := gen.VaultFindRequest{View: &name, Limit: &limit}
	if b.sel != nil {
		req.Select = b.sel
	}
	// THE GROUPING THE STACK NEEDS, ASKED FOR IN THE SAME CALL. A view whose
	// own `grouping:` is empty but whose table part declares one would
	// otherwise need a second full evaluation to obtain groups the first call
	// could have produced — and a second evaluation is a second point in time
	// as well as a second cost.
	base := b.baseGrouping()
	if gb := viewGroupByRequest(base); len(gb) > 0 {
		req.GroupBy = &gb
	}
	deps := b.env.Deps
	deps.RenderRows = limit

	resp, err := viewResultFind(b.ctx, deps, req)
	if err != nil || resp.Refused {
		ref := viewResultRefused(*b.out, "evaluation_refused",
			fmt.Sprintf("the view %q could not be evaluated", name), "")
		if len(resp.Problems) > 0 {
			p := resp.Problems[0]
			remedy := ""
			if p.Fix != nil {
				remedy = *p.Fix
			}
			ref = viewResultRefused(*b.out, string(p.Code), p.Reason, remedy)
		}
		return &ref
	}

	b.out.Complete = resp.Complete
	b.out.CompleteReason = resp.CompleteReason
	b.out.Problems = resp.Problems
	if b.out.Problems == nil {
		b.out.Problems = []gen.RecordProblem{}
	}
	b.snapshotEpoch = viewResponseEpoch(resp)

	rows := resp.Rows
	if len(rows) > maxViewResultRows {
		rows = rows[:maxViewResultRows]
	}
	b.out.Rows = append(b.out.Rows, rows...)

	// TRUNCATION IS EITHER OF TWO FACTS, and both are the same answer: the
	// engine offered a cursor (more rows exist than were asked for), or it
	// returned the detector row.
	if resp.NextCursor != nil || len(resp.Rows) > maxViewResultRows {
		b.truncated = true
		t := true
		b.out.RowsTruncated = &t
		b.out.Complete = false
		reason := fmt.Sprintf("the view matches more than %d rows; only the first %d are carried and no total is computed over a truncated set",
			maxViewResultRows, len(b.out.Rows))
		b.out.CompleteReason = &reason
	}

	b.collectLegacyAggregates(resp.Totals)

	// The base call's groups come from the SAME evaluation as the rows, so a
	// group's count and its members are one consistent snapshot by
	// construction — the property groupsFor has to work for to obtain.
	if resp.Groups != nil {
		b.groupCache[viewGroupingSignature(base)] = *resp.Groups
	}

	// The row map indexes the FINAL slice elements: the append above may have
	// reallocated the backing array, so the map is built over the settled
	// slice rather than during the copy.
	b.rowByPath = make(map[string]*gen.VaultFindRow, len(b.out.Rows))
	for i := range b.out.Rows {
		if _, dup := b.rowByPath[b.out.Rows[i].Path]; !dup {
			b.rowByPath[b.out.Rows[i].Path] = &b.out.Rows[i]
		}
	}
	return nil
}

// collectLegacyAggregates surfaces the view's own `aggregates:` results,
// GATED.
//
// `aggregates` predates the part stack and 69 saved views still use it. The
// bridge forwards it into the engine request and the engine computes it — and
// this builder used to throw the answer away, so one saved view showed its
// totals in knowledge_find and none at all in the base preview. A panel that
// silently omits a number the chat will state teaches its reader that the view
// has no totals, which is the confidently-wrong shape this whole surface
// exists against.
//
// THE GATE IS G2, AND IT IS NEEDED BECAUSE THE SOURCE IS UNIT-BLIND. The
// engine's `aggregate` has no idea PropertyDef.unit_property exists (the
// deferred defect recorded in knowledgefind's unit_aggregate_g2_test.go), so
// sum(amount) over SGD and EUR is a figure in no currency. Surfacing that raw
// would import the very output every other total in this file refuses. So a
// summary that COMBINES VALUES is dropped and refused by name whenever its
// property declares a companion unit; summaries that count rows or distinct
// values cross no units and pass through.
//
// G4 needs no gate here: the engine already refuses a summary a type does not
// define (opDefinedForType) and marks the total refused rather than omitting
// it, so a refused total is surfaced AS refused — which is the honest form of
// the same answer.
func (b *viewResultBuilder) collectLegacyAggregates(totals []gen.VaultFindTotal) {
	if b.view.Def.Aggregates == nil || len(*b.view.Def.Aggregates) == 0 || len(totals) == 0 {
		return
	}
	// The engine computes one total per requested aggregate, in request order,
	// and the bridge builds that request from this same list in this same
	// order — so position is the pairing. A length mismatch means that
	// invariant no longer holds, and the honest response is to surface nothing
	// rather than to attribute a number to the wrong property.
	declared := *b.view.Def.Aggregates
	if len(declared) != len(totals) {
		fix := "re-request the view; if it persists, the view's `aggregates` and the engine's answer have diverged and the view needs re-saving"
		b.out.Problems = append(b.out.Problems, gen.RecordProblem{
			Code:    gen.AggregateRefused,
			Reason:  "this view's `aggregates` could not be matched to the engine's answer, so none of them are shown rather than one being attributed to the wrong property",
			Fix:     &fix,
			Records: []string{},
		})
		b.out.Complete = false
		return
	}

	out := make([]gen.VaultFindTotal, 0, len(totals))
	for i, a := range declared {
		if a.Property != nil && strings.TrimSpace(*a.Property) != "" &&
			!viewAggregateCrossesNoUnits(string(a.Op)) {
			prop := strings.TrimSpace(*a.Property)
			if unitProp := b.anyDeclaredUnitFor(prop); unitProp != "" {
				b.refuseLegacyUnitAggregate(prop, string(a.Op), unitProp)
				continue
			}
		}
		out = append(out, totals[i])
	}
	if len(out) > 0 {
		b.out.Aggregates = &out
	}
}

// viewAggregateCrossesNoUnits is the closed list of summaries whose answer is
// a COUNT rather than a quantity. Counting rows, absences, presences or
// distinct values says nothing about what the numbers are denominated in, so
// no unit can be crossed. Everything else combines or selects values and is
// gated.
func viewAggregateCrossesNoUnits(op string) bool {
	switch op {
	case "count", "empty", "filled", "unique":
		return true
	default:
		return false
	}
}

// anyDeclaredUnitFor names the companion unit property declared for one
// number, from the view's own record type when it has one and from every
// in-scope type when it does not — the same reach the untyped G2 gate uses,
// because the question is the same: could this total cross a unit?
func (b *viewResultBuilder) anyDeclaredUnitFor(prop string) string {
	if b.view.Def.Type != nil {
		return b.unitPropertyOf(prop)
	}
	for _, t := range b.env.Schemas.Types() {
		sc, ok := b.env.Schemas.Get(t)
		if !ok || sc == nil {
			continue
		}
		if p, found := sc.Property(prop); found && p != nil && p.UnitProperty != "" {
			return p.UnitProperty
		}
	}
	return ""
}

// refuseLegacyUnitAggregate records the G2 refusal for one legacy aggregate,
// once per property per result.
func (b *viewResultBuilder) refuseLegacyUnitAggregate(prop, op, unitProp string) {
	if b.refusedLegacyAggs == nil {
		b.refusedLegacyAggs = map[string]bool{}
	}
	if b.refusedLegacyAggs[prop] {
		return
	}
	b.refusedLegacyAggs[prop] = true
	fix := fmt.Sprintf("replace the view's `aggregates` entry with a part that totals %q — a `figures` part reduces once per %s value (G2), which the legacy key cannot",
		prop, unitProp)
	b.out.Problems = append(b.out.Problems, gen.RecordProblem{
		Code: gen.AggregateRefused,
		Reason: fmt.Sprintf("this view's legacy `aggregates` asks for %s of %q, and %q carries the companion unit %q — that summary is computed across every unit at once, which is the combined figure G2 forbids, so it is not shown; the rows themselves are still shown",
			op, prop, prop, unitProp),
		Fix:     &fix,
		Records: []string{},
	})
	b.out.Complete = false
}

// viewResponseEpoch reads the properties-index generation an evaluation ran
// against. Zero means the engine reported none, which is treated as "cannot be
// compared" rather than as a generation that matches every other zero.
func viewResponseEpoch(resp gen.VaultFindResponse) int64 {
	if resp.Index == nil || resp.Index.Epoch == nil {
		return 0
	}
	return *resp.Index.Epoch
}

// ---------------------------------------------------------------------------
// Groupings
// ---------------------------------------------------------------------------

func (b *viewResultBuilder) viewGrouping() []gen.ViewGroupBy {
	if b.view.Def.Grouping == nil {
		return nil
	}
	return *b.view.Def.Grouping
}

// baseGrouping is the grouping the single row-collecting evaluation asks for:
// the view's own when it declares one, otherwise the first part's that does.
//
// It exists so the ordinary shape — a stack whose parts all group the way the
// first one does, or do not group at all — costs ONE evaluation. A part
// grouping some other way still needs its own call, and pays for it knowingly.
func (b *viewResultBuilder) baseGrouping() []gen.ViewGroupBy {
	if g := b.viewGrouping(); len(g) > 0 {
		return g
	}
	for _, part := range b.parts {
		if part.Grouping != nil && len(*part.Grouping) > 0 {
			// A crosstab's grouping is two-level and the engine caps grouping
			// at two levels, so this is safe to pass through as written.
			return *part.Grouping
		}
	}
	return nil
}

// viewGroupByRequest translates a view grouping into the engine's own group_by
// argument. One translation, used by both the base call and groupsFor, so the
// two cannot come to disagree about what a direction means.
func viewGroupByRequest(grouping []gen.ViewGroupBy) []gen.VaultFindGroupBy {
	gb := make([]gen.VaultFindGroupBy, 0, len(grouping))
	for _, g := range grouping {
		key := gen.VaultFindGroupBy{Property: g.Property}
		if g.Direction != nil {
			dir := gen.VaultFindGroupByDirection(string(*g.Direction))
			key.Direction = &dir
		}
		gb = append(gb, key)
	}
	return gb
}

// effectiveGrouping is the ViewPart contract's inheritance rule: a part's own
// `grouping` stands when declared; otherwise the view's.
func (b *viewResultBuilder) effectiveGrouping(src gen.ViewPart) []gen.ViewGroupBy {
	if src.Grouping != nil && len(*src.Grouping) > 0 {
		return *src.Grouping
	}
	return b.viewGrouping()
}

func viewGroupingSignature(grouping []gen.ViewGroupBy) string {
	parts := make([]string, 0, len(grouping))
	for _, g := range grouping {
		dir := "asc"
		if g.Direction != nil {
			dir = string(*g.Direction)
		}
		parts = append(parts, g.Property+"\x00"+dir)
	}
	return strings.Join(parts, "\x01")
}

// groupsFor returns the FULL-evaluated-set groups for one grouping, reusing
// the base call's groups when the signature matches and otherwise asking the
// engine once more with the grouping overridden (caller arguments win over
// the view's own, which is exactly the applyView contract). ok=false means
// the extra evaluation failed; the failure is already recorded in problems.
func (b *viewResultBuilder) groupsFor(grouping []gen.ViewGroupBy) ([]gen.VaultFindGroup, bool) {
	if len(grouping) == 0 {
		return nil, true
	}
	sig := viewGroupingSignature(grouping)
	if cached, hit := b.groupCache[sig]; hit {
		return cached, true
	}

	gb := make([]gen.VaultFindGroupBy, 0, len(grouping))
	for _, g := range grouping {
		key := gen.VaultFindGroupBy{Property: g.Property}
		if g.Direction != nil {
			dir := gen.VaultFindGroupByDirection(string(*g.Direction))
			key.Direction = &dir
		}
		gb = append(gb, key)
	}
	// Groups are computed over the FULL evaluated set regardless of the page
	// size (FR-125a's ordering), so the page here is deliberately minimal.
	limit := 1
	name := b.view.Name()
	req := gen.VaultFindRequest{View: &name, Limit: &limit, GroupBy: &gb}
	if b.sel != nil {
		req.Select = b.sel
	}
	resp, err := viewResultFind(b.ctx, b.env.Deps, req)
	if err == nil && !resp.Refused && !b.sameSnapshotAsRows(resp) {
		// A SECOND EVALUATION IS A SECOND POINT IN TIME, and this is the one
		// place this request cannot avoid taking one — a part grouping some
		// way the view itself does not has no groups in the base call's
		// answer. If the properties index was rebuilt in between, the groups
		// describe a different row set from the one in `rows`, and pairing
		// them would put a count of N beside a subtotal over N-1 with nothing
		// said. The groups are dropped, not reconciled: reconciling two
		// snapshots is inventing a third.
		fix := "re-request the view; the vault was re-indexed while this answer was being assembled"
		b.out.Problems = append(b.out.Problems, gen.RecordProblem{
			Code:    gen.AggregateRefused,
			Reason:  "this part's grouping was evaluated against a different index generation from the rows, so its groups and their subtotals are not shown — they would describe a row set this answer does not carry",
			Fix:     &fix,
			Records: []string{},
		})
		b.out.Complete = false
		return nil, false
	}
	if err != nil || resp.Refused || resp.Groups == nil {
		reason := "grouped evaluation failed"
		if len(resp.Problems) > 0 {
			reason = resp.Problems[0].Reason
		}
		fix := "re-request the view"
		b.out.Problems = append(b.out.Problems, gen.RecordProblem{
			Code:    gen.AggregateRefused,
			Reason:  fmt.Sprintf("this part's grouping could not be evaluated: %s", reason),
			Fix:     &fix,
			Records: []string{},
		})
		b.out.Complete = false
		return nil, false
	}
	b.groupCache[sig] = *resp.Groups
	return *resp.Groups, true
}

// carriedPaths bounds a group's member list to the rows this answer actually
// carries, and COUNTS what it dropped.
//
// A group's `paths` are documented as references INTO the result's own `rows`.
// Copying the engine's full membership broke that on both sides: it named rows
// the answer does not carry (a dangling reference the SPA resolves to nothing),
// and it made the payload grow with the CORPUS rather than with the row cap —
// 100k matching records produced ~100k path strings per grouped part, whatever
// the 2000-row cap said.
//
// The shortfall is stated, never silent. A group's `count` is still its size
// over the full evaluated set, so count and len(paths) legitimately differ once
// the cap binds; `paths_omitted` is the difference, and a reader who wants to
// know why a group of 600 lists 40 members is told.
func (b *viewResultBuilder) carriedPaths(paths []string) ([]string, int) {
	out := make([]string, 0, len(paths))
	omitted := 0
	for _, p := range paths {
		if _, ok := b.rowByPath[p]; ok {
			out = append(out, p)
			continue
		}
		omitted++
	}
	return out, omitted
}

// sameSnapshotAsRows reports whether a later evaluation ran against the same
// properties-index generation as the row collection. An epoch of zero on
// either side means the comparison cannot be made, and an unmakeable
// comparison is not treated as a passing one.
func (b *viewResultBuilder) sameSnapshotAsRows(resp gen.VaultFindResponse) bool {
	got := viewResponseEpoch(resp)
	if b.snapshotEpoch == 0 || got == 0 {
		return true
	}
	return got == b.snapshotEpoch
}

// ---------------------------------------------------------------------------
// Cell reading — the mechanical inverse of knowledgefind's renderTyped
// ---------------------------------------------------------------------------

func viewCellValue(row *gen.VaultFindRow, prop string) string {
	if row == nil {
		return ""
	}
	for _, c := range row.Cells {
		if c.Property == prop {
			return c.Value
		}
	}
	return ""
}

// viewNumberValues parses a rendered number cell back into exact decimals.
// Multi-values join with ", " (comma-space) and thousands separators are bare
// commas with no space, so the split is unambiguous. A segment that does not
// parse (a non-conforming value rendered verbatim, or "(unreadable)")
// contributes nothing — the engine's own problem list already names it.
func viewNumberValues(cell string) []records.Decimal {
	if strings.TrimSpace(cell) == "" {
		return nil
	}
	var out []records.Decimal
	for _, seg := range strings.Split(cell, ", ") {
		seg = strings.ReplaceAll(strings.TrimSpace(seg), ",", "")
		if seg == "" {
			continue
		}
		d, err := records.ParseDecimal(seg)
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	return out
}

// viewUnitOutcome is what reading one row's unit cell settled.
type viewUnitOutcome int

const (
	// viewUnitConfirmed: exactly one readable unit value.
	viewUnitConfirmed viewUnitOutcome = iota
	// viewUnitMissing: no value at all, or one that could not be read.
	viewUnitMissing
	// viewUnitAmbiguous: MORE THAN ONE value. The row has a unit — it has two
	// — and the number is in one of them, unknowably.
	viewUnitAmbiguous
)

// viewUnitValue reads a row's unit cell and says WHICH of G3's two triggers
// fired, because the two have different fixes and a footer that merges them
// helps with neither.
func viewUnitValue(cell string) (string, viewUnitOutcome) {
	if cell == "" || cell == "(unreadable)" {
		return "", viewUnitMissing
	}
	if strings.Contains(cell, ", ") {
		return "", viewUnitAmbiguous
	}
	return cell, viewUnitConfirmed
}

// unitPropertyOf resolves the DECLARED companion unit of a number property —
// declared on the record type, never inferred (design §5). For an untyped
// view it answers "" because no single schema speaks for the rows; whether
// totalling unit-less is then even PERMITTED is unitPropertyForTotals'
// question, and every aggregation goes through that gate, never through this
// lookup alone.
func (b *viewResultBuilder) unitPropertyOf(numberProp string) string {
	if b.view.Def.Type == nil {
		return ""
	}
	sc, ok := b.env.Schemas.Get(*b.view.Def.Type)
	if !ok {
		return ""
	}
	p, found := sc.Property(numberProp)
	if !found || p == nil {
		return ""
	}
	return p.UnitProperty
}

// ---------------------------------------------------------------------------
// G4 — "Text is never totalled, even when it parses as a number"
// ---------------------------------------------------------------------------
//
// The design puts all six gates in the COMPOSER AND THE RENDERER. The composer
// half exists; this is the renderer half, and it was missing.
//
// aggregateViewRows reads the engine's RENDERED CELL TEXT (the deliberate
// choice this file's header explains) and parses whatever looks decimal. That
// is safe only once something upstream has established that the column IS
// arithmetic. Nothing did: a `figures` part bound to a TEXT property holding
// "1200" and "3400" served a headline 4,600 — a number nobody recorded, over a
// column the schema calls prose. The composer refuses to write such a view;
// `write_view` (the raw escape hatch), a hand-edited file and an imported
// .base view all reach here without passing the composer.
//
// The permitted set is `integer` and `decimal` — §8 R-1's one comparison
// domain — and it is the whole of the rule. Every other declared type is
// refused for the five reductions a view part can name: `avg`/`sum` over a
// date or a checkbox is undefined, and `min`/`max`/`count` are refused too
// rather than half-implemented, because this file's accumulator parses
// DECIMALS and would silently reduce over only those rows whose text happened
// to parse. A count of rows is already on every group as `count`.

// viewTotalsAsNumber is G4's permitted set, named once so the gate and its
// refusal message cannot disagree about what it is.
func viewTotalsAsNumber(t records.PropertyType) bool {
	return t == records.TypeInteger || t == records.TypeDecimal
}

// declaredTypeOf resolves a property's DECLARED type through the same three
// namespaces one query resolves names against (knowledgefind's `namespace`):
// `formula.<name>` from the view's own formulas, `file.<name>` from the twelve
// reserved file properties, and otherwise the schema — the view's own when it
// declares a `type`, or every schema in scope when it does not.
//
// The second return names the AUTHORITY the type was read from, so a refusal
// can be checked against a file rather than merely believed.
//
// UNRESOLVED IS TEXT, not "unknown, proceed". An untyped query resolves every
// name it cannot place in the text domain (namespace.resolveUntyped: "by rule,
// every name is legal there and resolves in the text domain"), so a binding no
// schema declares is prose by resolution — and G4 refuses prose. A typed view
// cannot reach that branch at all: the loader's checkViewProp already rejects
// a part binding the record type does not declare.
func (b *viewResultBuilder) declaredTypeOf(prop string) (records.PropertyType, string) {
	if strings.HasPrefix(prop, knowledgefind.FormulaNamespace) {
		if decl, ok := b.formulaDecl(strings.TrimPrefix(prop, knowledgefind.FormulaNamespace)); ok {
			if pt, mapped := records.FormulaPropertyType(decl.Type); mapped {
				return pt, "the view's own formula"
			}
		}
		return records.TypeText, "the view's own formula"
	}
	if records.IsFileNamespace(prop) {
		if p, ok := records.FileProperty(prop); ok && p != nil {
			return p.Type, "the reserved file properties"
		}
		return records.TypeText, "the reserved file properties"
	}
	if b.view.Def.Type != nil {
		if sc, ok := b.env.Schemas.Get(*b.view.Def.Type); ok {
			if p, found := sc.Property(prop); found && p != nil {
				return p.Type, "record type " + *b.view.Def.Type
			}
		}
		return records.TypeText, "record type " + *b.view.Def.Type
	}
	// Untyped: every in-scope declaration. The engine has already refused the
	// query outright if two of them disagree on the comparison domain
	// (namespace.refuseSplitDomain), so the first is the domain — but the scan
	// still prefers a NON-number declaration when it meets one, because a
	// refusal is the safe direction to be wrong in.
	var (
		firstType   records.PropertyType
		firstOwner  string
		haveAny     bool
		nonNumber   records.PropertyType
		nonNumOwner string
	)
	for _, t := range b.env.Schemas.Types() {
		sc, ok := b.env.Schemas.Get(t)
		if !ok || sc == nil {
			continue
		}
		p, found := sc.Property(prop)
		if !found || p == nil {
			continue
		}
		if !haveAny {
			firstType, firstOwner, haveAny = p.Type, "record type "+t, true
		}
		if !viewTotalsAsNumber(p.Type) && nonNumOwner == "" {
			nonNumber, nonNumOwner = p.Type, "record type "+t
		}
	}
	if nonNumOwner != "" {
		return nonNumber, nonNumOwner
	}
	if haveAny {
		return firstType, firstOwner
	}
	return records.TypeText, "no record type in scope"
}

// formulaDecl resolves one of the view's own formulas, validating the set ONCE
// per result. The set is validated rather than read raw because a formula's
// static type is INFERRED from its expression (FR-143a) and exists nowhere on
// disk — the file stores source text only.
func (b *viewResultBuilder) formulaDecl(name string) (records.FormulaDecl, bool) {
	if !b.formulasResolved {
		b.formulasResolved = true
		if b.view.Def.Formulas != nil && len(*b.view.Def.Formulas) > 0 {
			var schema *records.Schema
			if b.view.Def.Type != nil {
				if sc, ok := b.env.Schemas.Get(*b.view.Def.Type); ok {
					schema = sc
				}
			}
			set, _ := records.ValidateFormulaSet(*b.view.Def.Formulas, schema)
			b.formulas = set
		}
	}
	if b.formulas == nil {
		return records.FormulaDecl{}, false
	}
	return b.formulas.Get(name)
}

// permittedToTotal is G4 as a gate: false means the caller computes nothing
// and the refusal has already been recorded, ONCE per property per result.
func (b *viewResultBuilder) permittedToTotal(numberProp string) bool {
	declared, authority := b.declaredTypeOf(numberProp)
	if viewTotalsAsNumber(declared) {
		return true
	}
	if b.refusedG4Props == nil {
		b.refusedG4Props = map[string]bool{}
	}
	if b.refusedG4Props[numberProp] {
		return false
	}
	b.refusedG4Props[numberProp] = true
	fix := fmt.Sprintf("convert %q to a number property (change its `type:` to integer or decimal and re-validate the records), or bind this part to a property that is already one",
		numberProp)
	b.out.Problems = append(b.out.Problems, gen.RecordProblem{
		Code: gen.AggregateRefused,
		Reason: fmt.Sprintf("no total of %q: %s declares it %s, and only integer and decimal are totalled — values that merely LOOK like numbers are not added up (G4); the rows themselves are still shown",
			numberProp, authority, declared),
		Fix:     &fix,
		Records: []string{},
	})
	b.out.Complete = false
	return false
}

// unitPropertyForTotals is the gate every total passes before any accumulator
// is keyed: it resolves the companion unit AND answers whether totalling this
// property is permitted at all (ok=false → the caller computes nothing; the
// refusal has already been recorded as a problem).
//
// The case it exists for: a view with no `type` (legal, FR-018b) spans every
// note in scope, so no single schema resolves its units. This builder used to
// total such numbers unit-less — which, over a property SOME schema declares
// with a companion unit (Property.UnitProperty), silently keyed every row
// into the one unit-less accumulator: SGD + EUR + the unit-less G3 row, one
// combined figure, no caveat. That is the exact output G2 ("no combined
// figure is ever emitted") and G3 ("excluded from every total") forbid, and
// the design admits no untyped exception. Reachable through the raw
// write_view escape hatch, a hand-edited view file, and imported .base views
// that resolve untyped (pkg/vaultimport ResolvedType == "").
//
// So: an untyped view refuses to total any property that ANY schema in scope
// pairs with a unit — the rows stay shown, the refusal names the fix. A
// property no schema pairs with a unit keeps its unit-less total: with no
// declaration there are no units to cross (declared, never inferred).
//
// IT ALSO RECONCILES THE TWO READERS OF A UNIT, which is the second defect it
// was widened for. The part on disk carries the unit the COMPOSER stamped
// (ViewPart.unit) and the schema carries the unit the RECORD TYPE declares,
// and nothing rewrites a view when a record type changes. Deleting
// `unit_property` from the type while keeping the `currency` property left the
// part still saying `unit: currency` and this endpoint resolving none — one
// combined SGD+EUR+unit-less figure, no refusal, and a view file that still
// read as unit-aware to anyone inspecting it.
//
// The rule: THE SCHEMA IS THE AUTHORITY (design §5 — declared, never
// inferred), and a DISAGREEMENT NEVER PASSES SILENTLY. Where the part's stamp
// and the schema's declaration differ — including the schema resolving none
// while the part stamps one — the total is refused, naming BOTH sides, because
// picking either would total under a rule one of the two files does not
// describe and the operator would not know which file to edit. A part with no
// stamp of its own simply takes the schema's answer, which then reaches the
// wire (ViewResultPart.unit_property) so nothing downstream re-derives it.
func (b *viewResultBuilder) unitPropertyForTotals(src gen.ViewPart, numberProp string) (unitProp string, ok bool) {
	// G4 RUNS FIRST, because it decides whether the column is arithmetic at
	// all — asking which unit a paragraph of prose is denominated in is a
	// question that only makes sense after that.
	if !b.permittedToTotal(numberProp) {
		return "", false
	}
	if b.view.Def.Type != nil {
		resolved := b.unitPropertyOf(numberProp)
		if !b.unitStampAgrees(src, numberProp, resolved) {
			return "", false
		}
		return resolved, true
	}
	// The untyped G2 refusal is tried BEFORE the stamp check, because it is
	// the more specific diagnosis of the same situation: "this view declares
	// no type, so no schema can resolve the unit" tells the operator what to
	// do, where "the part stamps currency and nothing resolves it" would send
	// them to edit a view file that is not the problem.
	if declaring := b.typesDeclaringUnitFor(numberProp); len(declaring) > 0 {
		b.refuseUntypedUnitTotal(numberProp, declaring)
		return "", false
	}
	if !b.unitStampAgrees(src, numberProp, "") {
		return "", false
	}
	return "", true
}

// unitStampAgrees is the reconciliation. false means the two authorities
// disagree, the refusal has been recorded (once per property per result), and
// the caller computes nothing.
//
// A part's `unit:` key belongs to its `number:` binding. A part that names a
// number and stamps a unit is talking about THAT number, so a subtotal over
// some OTHER property is not governed by the stamp; a part that stamps a unit
// and names no number is talking about whatever it totals.
func (b *viewResultBuilder) unitStampAgrees(src gen.ViewPart, numberProp, resolved string) bool {
	if src.Unit == nil {
		return true
	}
	if src.Number != nil && *src.Number != numberProp {
		return true
	}
	stamped := strings.TrimSpace(*src.Unit)
	if stamped == "" || stamped == resolved {
		return true
	}

	if b.refusedUnitStamps == nil {
		b.refusedUnitStamps = map[string]bool{}
	}
	if b.refusedUnitStamps[numberProp] {
		return false
	}
	b.refusedUnitStamps[numberProp] = true

	owner := "no record type in scope"
	if b.view.Def.Type != nil {
		owner = "record type " + *b.view.Def.Type
	}
	schemaSide := fmt.Sprintf("declares it with the companion unit %q", resolved)
	fix := fmt.Sprintf("re-point the part's `unit:` at %q, or declare `unit_property: %s` on the number", resolved, stamped)
	if resolved == "" {
		schemaSide = "declares it with no companion unit"
		fix = fmt.Sprintf("declare `unit_property: %s` on %q in the record type, or drop the part's `unit:` key so the number totals unit-less",
			stamped, numberProp)
	}
	b.out.Problems = append(b.out.Problems, gen.RecordProblem{
		Code: gen.AggregateRefused,
		Reason: fmt.Sprintf("no total of %q: this part is stamped `unit: %s`, but %s %s — the two disagree, and a unit is DECLARED, never inferred (design §5), so neither reading may be picked silently; the rows themselves are still shown",
			numberProp, stamped, owner, schemaSide),
		Fix:     &fix,
		Records: []string{},
	})
	b.out.Complete = false
	return false
}

// typesDeclaringUnitFor lists, sorted, every record type in scope whose
// schema declares numberProp with a companion unit property.
func (b *viewResultBuilder) typesDeclaringUnitFor(numberProp string) []string {
	var out []string
	for _, t := range b.env.Schemas.Types() {
		sc, ok := b.env.Schemas.Get(t)
		if !ok {
			continue
		}
		if p, found := sc.Property(numberProp); found && p != nil && p.UnitProperty != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// refuseUntypedUnitTotal records the G2 refusal for one property, ONCE per
// result — figures, footer totals and every group's subtotal all funnel here,
// and one problem line says it all.
func (b *viewResultBuilder) refuseUntypedUnitTotal(numberProp string, declaring []string) {
	if b.refusedUnitProps == nil {
		b.refusedUnitProps = map[string]bool{}
	}
	if b.refusedUnitProps[numberProp] {
		return
	}
	b.refusedUnitProps[numberProp] = true
	fix := fmt.Sprintf("declare `type:` on the view so %q resolves its companion unit and totals once per unit value, or total a property no record type pairs with a unit", numberProp)
	b.out.Problems = append(b.out.Problems, gen.RecordProblem{
		Code: gen.AggregateRefused,
		Reason: fmt.Sprintf("no total of %q: this view declares no `type`, and record type %s declares %q with a companion unit — a total that cannot resolve units would add across them (G2); the rows themselves are still shown",
			numberProp, strings.Join(declaring, "/"), numberProp),
		Fix:     &fix,
		Records: []string{},
	})
}

// ---------------------------------------------------------------------------
// Aggregation — G2 and G3 live here
// ---------------------------------------------------------------------------

type viewUnitAccum struct {
	count     int
	sum       records.Decimal
	hasSum    bool
	sumFailed bool
	min       records.Decimal
	max       records.Decimal
	hasMinMax bool
}

func (a *viewUnitAccum) add(d records.Decimal) {
	a.count++
	if !a.hasSum {
		a.sum, a.hasSum = d, true
	} else if !a.sumFailed {
		s, err := a.sum.Add(d)
		if err != nil {
			a.sumFailed = true
		} else {
			a.sum = s
		}
	}
	if !a.hasMinMax {
		a.min, a.max, a.hasMinMax = d, d, true
		return
	}
	if d.Cmp(a.min) < 0 {
		a.min = d
	}
	if d.Cmp(a.max) > 0 {
		a.max = d
	}
}

// aggregateViewRows reduces one number property over one row scope, ONCE PER
// UNIT VALUE (G2). Rows whose unit is missing or unconfirmed are returned in
// excludedPaths (G3) — counted by the caller, shown by the rows list, and in
// no total.
func (b *viewResultBuilder) aggregateViewRows(
	rows []*gen.VaultFindRow, numberProp, unitProp string, op gen.ViewPartAggregate,
) (totals []gen.ViewUnitTotal, excluded viewExcluded) {
	accums := map[string]*viewUnitAccum{}
	var order []string
	for _, row := range rows {
		if row == nil {
			continue
		}
		nums := viewNumberValues(viewCellValue(row, numberProp))
		if len(nums) == 0 {
			continue
		}
		unitKey := ""
		if unitProp != "" {
			u, outcome := viewUnitValue(viewCellValue(row, unitProp))
			switch outcome {
			case viewUnitMissing:
				excluded.missing = append(excluded.missing, row.Path)
				continue
			case viewUnitAmbiguous:
				excluded.ambiguous = append(excluded.ambiguous, row.Path)
				continue
			}
			unitKey = u
		}
		acc := accums[unitKey]
		if acc == nil {
			acc = &viewUnitAccum{}
			accums[unitKey] = acc
			order = append(order, unitKey)
		}
		for _, n := range nums {
			acc.add(n)
		}
	}
	sort.Strings(order)
	totals = make([]gen.ViewUnitTotal, 0, len(order))
	for _, unitKey := range order {
		acc := accums[unitKey]
		value, ok := renderViewAggregate(acc, op)
		if !ok {
			fix := "narrow the view's filter"
			b.out.Problems = append(b.out.Problems, gen.RecordProblem{
				Code:    gen.AggregateRefused,
				Reason:  fmt.Sprintf("the %s of %q could not be computed for unit %q", string(op), numberProp, unitKey),
				Fix:     &fix,
				Records: []string{},
			})
			continue
		}
		t := gen.ViewUnitTotal{Property: numberProp, Op: op, Value: value, Count: acc.count}
		if unitProp != "" {
			u := unitKey
			t.Unit = &u
			// The unit value and the property it was READ FROM travel
			// together: a consumer that acquired "SGD" without knowing it came
			// from `currency` would have to guess which column to pair the
			// figure with, and the schema it would need to stop guessing is
			// not something the SPA has.
			up := unitProp
			t.UnitProperty = &up
		}
		totals = append(totals, t)
	}
	return totals, excluded
}

func renderViewAggregate(acc *viewUnitAccum, op gen.ViewPartAggregate) (string, bool) {
	switch op {
	case gen.ViewPartAggregateCount:
		return strconv.Itoa(acc.count), true
	case gen.ViewPartAggregateSum:
		if acc.sumFailed {
			return "", false
		}
		return acc.sum.String(), true
	case gen.ViewPartAggregateMin:
		return acc.min.String(), true
	case gen.ViewPartAggregateMax:
		return acc.max.String(), true
	case gen.ViewPartAggregateAvg:
		if acc.sumFailed || acc.count == 0 {
			return "", false
		}
		return renderViewAverage(acc.sum, acc.count), true
	default:
		return "", false
	}
}

// renderViewAverage renders sum/count in exact rational arithmetic — never
// through a float — at the column's own scale plus two (FR-152), rounded HALF
// TO EVEN by knowledgefind's own exported rule.
//
// The rule is BORROWED rather than reimplemented, and that is the whole point
// of the change that put it here. This function used to round half UP while
// knowledge_find rounded half to even and said "round-half-even" in its own
// label: one column, one set of records, two answers, and the only reader who
// could catch it is one who thought to compare a chat answer against a panel.
// One rule needs one implementation, or the two drift again the next time
// either is touched.
func renderViewAverage(sum records.Decimal, count int) string {
	num := new(big.Int).Set(sum.Unscaled())
	den := big.NewInt(int64(count))
	scale := sum.Scale()
	if scale >= 0 {
		den.Mul(den, viewPow10(int64(scale)))
	} else {
		num.Mul(num, viewPow10(int64(-scale)))
		scale = 0
	}
	return knowledgefind.RoundHalfEven(new(big.Rat).SetFrac(num, den), scale+2)
}

func viewPow10(n int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(n), nil)
}

// ---------------------------------------------------------------------------
// Parts
// ---------------------------------------------------------------------------

func (b *viewResultBuilder) rowsFor(paths []string) []*gen.VaultFindRow {
	out := make([]*gen.VaultFindRow, 0, len(paths))
	for _, p := range paths {
		if row, ok := b.rowByPath[p]; ok {
			out = append(out, row)
		}
	}
	return out
}

func (b *viewResultBuilder) allRows() []*gen.VaultFindRow {
	out := make([]*gen.VaultFindRow, 0, len(b.out.Rows))
	for i := range b.out.Rows {
		out = append(out, &b.out.Rows[i])
	}
	return out
}

// resolveColumns is the part contract's column ladder: the part's own
// `properties`, else the view's, else the record type's declaration order.
func (b *viewResultBuilder) resolveColumns(src gen.ViewPart) *[]string {
	if src.Properties != nil && len(*src.Properties) > 0 {
		cols := append([]string(nil), (*src.Properties)...)
		return &cols
	}
	if b.view.Def.Properties != nil && len(*b.view.Def.Properties) > 0 {
		cols := append([]string(nil), (*b.view.Def.Properties)...)
		return &cols
	}
	if b.view.Def.Type != nil {
		if sc, ok := b.env.Schemas.Get(*b.view.Def.Type); ok {
			cols := sc.PropertyNames()
			if len(cols) > 0 {
				return &cols
			}
		}
	}
	return nil
}

// viewExcludedFields stamps a scope's G3 fields from the union of excluded
// rows: the count, the reason, and THE ROWS THEMSELVES.
//
// The paths are returned because the count alone was not enough. The unit a
// row is excluded for is resolved from the RECORD TYPE, which the SPA cannot
// read, so a part carrying no `unit:` stamp of its own left the renderer able
// to say "1 row excluded" and unable to say which one. The three travel
// together, from one deduplicated set, so the list can never disagree with the
// count beside it.
func viewExcludedFields(ex viewExcluded, unitProps []string) (*int, *string, *[]string) {
	seen := map[string]struct{}{}
	dedupe := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
		return out
	}
	// Missing is deduplicated FIRST, so a row that is somehow reported under
	// both causes is counted once, under the cause with the simpler fix.
	missing := dedupe(ex.missing)
	ambiguous := dedupe(ex.ambiguous)

	n := len(missing) + len(ambiguous)
	if n == 0 {
		return nil, nil, nil
	}
	all := append(append(make([]string, 0, n), missing...), ambiguous...)
	sort.Strings(all)
	reason := viewResultExcludedReason(len(missing), len(ambiguous), unitProps)
	return &n, &reason, &all
}

func (b *viewResultBuilder) buildPart(src gen.ViewPart) gen.ViewResultPart {
	p := gen.ViewResultPart{
		Part:   gen.ViewResultPartPart(string(src.Part)),
		Source: src,
	}

	switch src.Part {
	case gen.ViewPartPartTable, gen.ViewPartPartList, gen.ViewPartPartTiles,
		gen.ViewPartPartColumns, gen.ViewPartPartCalendar:
		p.Columns = b.resolveColumns(src)
		b.buildTablePartData(&p, src)
	case gen.ViewPartPartFigures:
		b.buildFiguresPartData(&p, src)
	case gen.ViewPartPartChart:
		b.buildChartPartData(&p, src)
	case gen.ViewPartPartCrosstab:
		b.buildCrosstabPartData(&p, src)
	}
	return p
}

func viewPartAggregateOf(src gen.ViewPart) gen.ViewPartAggregate {
	if src.Aggregate != nil {
		return *src.Aggregate
	}
	return gen.ViewPartAggregateSum
}

// buildTablePartData attaches groups (with per-group per-unit subtotals) and
// the footer totals to a table-ish part.
func (b *viewResultBuilder) buildTablePartData(p *gen.ViewResultPart, src gen.ViewPart) {
	grouping := b.effectiveGrouping(src)
	subtotalProps, subtotalOps := viewSubtotalPlan(src)

	if len(grouping) > 0 {
		if groups, ok := b.groupsFor(grouping); ok {
			resultGroups := make([]gen.ViewResultGroup, 0, len(groups))
			for _, g := range groups {
				paths, omitted := b.carriedPaths(g.Paths)
				rg := gen.ViewResultGroup{
					Key:       g.Key,
					Absent:    g.Absent,
					Count:     g.Count,
					Paths:     paths,
					Subtotals: []gen.ViewUnitTotal{},
				}
				if omitted > 0 {
					n := omitted
					rg.PathsOmitted = &n
				}
				if len(subtotalProps) > 0 && !b.truncated {
					rows := b.rowsFor(g.Paths)
					var excluded viewExcluded
					var unitProps []string
					for i, prop := range subtotalProps {
						unitProp, permitted := b.unitPropertyForTotals(src, prop)
						if !permitted {
							continue
						}
						totals, ex := b.aggregateViewRows(rows, prop, unitProp, subtotalOps[i])
						rg.Subtotals = append(rg.Subtotals, totals...)
						excluded.add(ex)
						if unitProp != "" {
							unitProps = append(unitProps, unitProp)
						}
					}
					rg.ExcludedCount, rg.ExcludedReason, rg.ExcludedPaths = viewExcludedFields(excluded, unitProps)
				}
				resultGroups = append(resultGroups, rg)
			}
			p.Groups = &resultGroups
		}
	}

	if len(subtotalProps) > 0 && !b.truncated {
		rows := b.allRows()
		totals := make([]gen.ViewUnitTotal, 0, len(subtotalProps))
		var excluded viewExcluded
		var unitProps []string
		for i, prop := range subtotalProps {
			unitProp, permitted := b.unitPropertyForTotals(src, prop)
			if !permitted {
				continue
			}
			ts, ex := b.aggregateViewRows(rows, prop, unitProp, subtotalOps[i])
			totals = append(totals, ts...)
			excluded.add(ex)
			if unitProp != "" {
				unitProps = append(unitProps, unitProp)
			}
		}
		p.Totals = &totals
		p.ExcludedCount, p.ExcludedReason, p.ExcludedPaths = viewExcludedFields(excluded, unitProps)
	}
}

// viewSubtotalPlan reads a part's subtotal map in SORTED property order, so
// two renders of one view emit totals in one order.
func viewSubtotalPlan(src gen.ViewPart) ([]string, []gen.ViewPartAggregate) {
	if src.Subtotals == nil || len(*src.Subtotals) == 0 {
		return nil, nil
	}
	props := make([]string, 0, len(*src.Subtotals))
	for prop := range *src.Subtotals {
		props = append(props, prop)
	}
	sort.Strings(props)
	ops := make([]gen.ViewPartAggregate, 0, len(props))
	for _, prop := range props {
		ops = append(ops, (*src.Subtotals)[prop])
	}
	return props, ops
}

func (b *viewResultBuilder) buildFiguresPartData(p *gen.ViewResultPart, src gen.ViewPart) {
	if src.Number == nil || b.truncated {
		return
	}
	numberProp := *src.Number
	unitProp, permitted := b.unitPropertyForTotals(src, numberProp)
	if !permitted {
		// The part still answers an EMPTY totals list — the SPA's explicit
		// "no figures" state — while the refusal problem says why.
		totals := []gen.ViewUnitTotal{}
		p.Totals = &totals
		return
	}
	// THE RESOLVED UNIT, ON THE WIRE. The SPA cannot read the record type, so
	// without this it would have to fall back on `source.unit` — the stamp the
	// composer wrote, which a later schema edit can leave stale, and which is
	// exactly the second authority this gate exists to eliminate.
	if unitProp != "" {
		up := unitProp
		p.UnitProperty = &up
	}
	totals, excluded := b.aggregateViewRows(b.allRows(), numberProp, unitProp, viewPartAggregateOf(src))
	p.Totals = &totals
	if unitProp != "" {
		p.ExcludedCount, p.ExcludedReason, p.ExcludedPaths = viewExcludedFields(excluded, []string{unitProp})
	}
}

func (b *viewResultBuilder) buildChartPartData(p *gen.ViewResultPart, src gen.ViewPart) {
	if src.Number == nil || src.Date == nil || b.truncated {
		return
	}
	numberProp, dateProp := *src.Number, *src.Date
	unitProp, permitted := b.unitPropertyForTotals(src, numberProp)
	if !permitted {
		return
	}
	// THE RESOLVED UNIT, ON THE WIRE. The SPA cannot read the record type, so
	// without this it would have to fall back on `source.unit` — the stamp the
	// composer wrote, which a later schema edit can leave stale, and which is
	// exactly the second authority this gate exists to eliminate.
	if unitProp != "" {
		up := unitProp
		p.UnitProperty = &up
	}
	op := viewPartAggregateOf(src)

	groups, ok := b.groupsFor([]gen.ViewGroupBy{{Property: dateProp}})
	if !ok {
		return
	}
	pointsByUnit := map[string][]gen.ViewResultPoint{}
	var unitOrder []string
	var excluded viewExcluded
	for _, g := range groups {
		if g.Absent != nil && *g.Absent {
			// An undated row has no x position; it stays in `rows` and simply
			// is not plotted — a point at an invented date would be a value
			// nobody recorded.
			continue
		}
		totals, ex := b.aggregateViewRows(b.rowsFor(g.Paths), numberProp, unitProp, op)
		excluded.add(ex)
		for _, t := range totals {
			unitKey := ""
			if t.Unit != nil {
				unitKey = *t.Unit
			}
			if _, exists := pointsByUnit[unitKey]; !exists {
				unitOrder = append(unitOrder, unitKey)
			}
			pointsByUnit[unitKey] = append(pointsByUnit[unitKey],
				gen.ViewResultPoint{Key: g.Key, Value: t.Value, Count: t.Count})
		}
	}
	sort.Strings(unitOrder)
	series := make([]gen.ViewResultSeries, 0, len(unitOrder))
	for _, unitKey := range unitOrder {
		s := gen.ViewResultSeries{Points: pointsByUnit[unitKey]}
		if unitProp != "" {
			u := unitKey
			s.Unit = &u
		}
		series = append(series, s)
	}
	p.Series = &series
	if unitProp != "" {
		p.ExcludedCount, p.ExcludedReason, p.ExcludedPaths = viewExcludedFields(excluded, []string{unitProp})
	}
}

func (b *viewResultBuilder) buildCrosstabPartData(p *gen.ViewResultPart, src gen.ViewPart) {
	if src.Number == nil || b.truncated {
		return
	}
	// THE VIEW-LEVEL FALLBACK IS THE RENDERER'S TO APPLY. EffectiveParts
	// deliberately does not copy a view's own `grouping:` down into a part
	// that declares none — its comment says so — and this builder read
	// src.Grouping directly, so a perfectly legal view that declared both keys
	// at the top level and a bare `- part: crosstab` beneath refused with
	// "needs two grouping keys" while the keys sat one level up in the same
	// file. effectiveGrouping is the inheritance rule every other part already
	// goes through.
	grouping := b.effectiveGrouping(src)
	if len(grouping) < 2 {
		fix := "give the crosstab part two grouping keys through knowledge_configure, or declare them on the view so every part inherits them"
		b.out.Problems = append(b.out.Problems, gen.RecordProblem{
			Code:    gen.AggregateRefused,
			Reason:  "a crosstab part needs two grouping keys, and neither it nor its view declares two",
			Fix:     &fix,
			Records: []string{},
		})
		return
	}
	grouping = grouping[:2]
	numberProp := *src.Number
	unitProp, permitted := b.unitPropertyForTotals(src, numberProp)
	if !permitted {
		return
	}
	// THE RESOLVED UNIT, ON THE WIRE. The SPA cannot read the record type, so
	// without this it would have to fall back on `source.unit` — the stamp the
	// composer wrote, which a later schema edit can leave stale, and which is
	// exactly the second authority this gate exists to eliminate.
	if unitProp != "" {
		up := unitProp
		p.UnitProperty = &up
	}
	op := viewPartAggregateOf(src)

	groups, ok := b.groupsFor(grouping)
	if !ok {
		return
	}
	ct := gen.ViewResultCrosstab{
		RowProperty:    grouping[0].Property,
		ColumnProperty: grouping[1].Property,
		RowKeys:        []string{},
		ColumnKeys:     []string{},
		Cells:          []gen.ViewResultCrosstabCell{},
	}
	colSeen := map[string]bool{}
	var excluded viewExcluded
	for _, g := range groups {
		ct.RowKeys = append(ct.RowKeys, g.Key)
		if g.Subgroups == nil {
			continue
		}
		for _, sg := range *g.Subgroups {
			if !colSeen[sg.Key] {
				colSeen[sg.Key] = true
				ct.ColumnKeys = append(ct.ColumnKeys, sg.Key)
			}
			totals, ex := b.aggregateViewRows(b.rowsFor(sg.Paths), numberProp, unitProp, op)
			excluded.add(ex)
			for _, t := range totals {
				cell := gen.ViewResultCrosstabCell{
					Row:    g.Key,
					Column: sg.Key,
					Value:  t.Value,
					Count:  t.Count,
					Unit:   t.Unit,
				}
				ct.Cells = append(ct.Cells, cell)
			}
		}
	}
	if unitProp != "" {
		ct.ExcludedCount, ct.ExcludedReason, ct.ExcludedPaths = viewExcludedFields(excluded, []string{unitProp})
	}
	p.Crosstab = &ct
}
