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

const (
	// viewResultPageSize is knowledgefind's own MaxLimit, asked for
	// explicitly so paging takes as few round trips as the engine permits.
	viewResultPageSize = 200
	// maxViewResultRows bounds how many rows one view result will carry.
	// Above it the result reports rows_truncated and computes NO totals —
	// a total over a truncated set is a wrong number that looks right,
	// which is the one output this surface exists to make impossible.
	maxViewResultRows = 2000
)

// viewResultExcludedReason writes the G3 footer line for one scope.
func viewResultExcludedReason(n int, unitProps []string) string {
	rows := "rows have"
	if n == 1 {
		rows = "row has"
	}
	return fmt.Sprintf("%d %s no confirmed %s value and are excluded from every total (G3); the rows themselves are still shown",
		n, rows, strings.Join(unitProps, "/"))
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
	ctx       context.Context
	env       vaultprops.FindEnv
	view      *records.SavedView
	sel       *[]string
	rowByPath map[string]*gen.VaultFindRow
	// groupCache caches full-evaluated-set groupings by signature, so a part
	// whose grouping matches the view's own reuses the base call's groups.
	groupCache map[string][]gen.VaultFindGroup
	truncated  bool
	// refusedUnitProps dedupes the untyped-view G2 refusal: one problem per
	// property per result, however many parts and groups total it.
	refusedUnitProps map[string]bool
	// refusedG4Props is the same dedupe for G4 — a property refused as
	// non-arithmetic says so once, not once per part, group and cell.
	refusedG4Props map[string]bool
	// refusedUnitStamps is the same dedupe for the unit-authority
	// disagreement between a part's stamp and the schema's declaration.
	refusedUnitStamps map[string]bool
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

// collectRows pages the WHOLE row set through the engine's own cursor,
// bounded by maxViewResultRows. It returns a refusal result when the engine
// refuses, and nil on success.
func (b *viewResultBuilder) collectRows(name string) *gen.ViewResult {
	limit := viewResultPageSize
	req := gen.VaultFindRequest{View: &name, Limit: &limit}
	if b.sel != nil {
		req.Select = b.sel
	}

	first, err := knowledgefind.Find(b.ctx, b.env.Deps, req)
	if err != nil || first.Refused {
		ref := viewResultRefused(*b.out, "evaluation_refused",
			fmt.Sprintf("the view %q could not be evaluated", name), "")
		if len(first.Problems) > 0 {
			p := first.Problems[0]
			remedy := ""
			if p.Fix != nil {
				remedy = *p.Fix
			}
			ref = viewResultRefused(*b.out, string(p.Code), p.Reason, remedy)
		}
		return &ref
	}

	b.out.Complete = first.Complete
	b.out.CompleteReason = first.CompleteReason
	// Problems are carried from the FIRST page only. Query-level problems
	// (exclusions, clamps) are recomputed identically on every page, so
	// appending each page's copy would state every one of them N times; the
	// cost is that a stale-row problem for a row on a later page is carried
	// only by that row's own `stale` flag, which survives in `rows`.
	b.out.Problems = first.Problems
	if b.out.Problems == nil {
		b.out.Problems = []gen.RecordProblem{}
	}

	appendRows := func(rows []gen.VaultFindRow) {
		for i := range rows {
			b.out.Rows = append(b.out.Rows, rows[i])
			if _, dup := b.rowByPath[rows[i].Path]; !dup {
				b.rowByPath[rows[i].Path] = &b.out.Rows[len(b.out.Rows)-1]
			}
		}
	}
	appendRows(first.Rows)

	if first.Groups != nil {
		b.groupCache[viewGroupingSignature(b.viewGrouping())] = *first.Groups
	}

	cursor := first.NextCursor
	for cursor != nil && len(b.out.Rows) < maxViewResultRows {
		pageReq := req
		pageReq.Cursor = cursor
		page, pageErr := knowledgefind.Find(b.ctx, b.env.Deps, pageReq)
		if pageErr != nil || page.Refused {
			// A page that refuses mid-walk (a stale cursor after a concurrent
			// reindex) degrades the VERDICT, never the rows already gathered.
			b.out.Complete = false
			reason := "the row set changed while it was being read; re-request the view"
			b.out.CompleteReason = &reason
			b.truncated = true
			t := true
			b.out.RowsTruncated = &t
			return nil
		}
		appendRows(page.Rows)
		b.out.Complete = b.out.Complete && page.Complete
		cursor = page.NextCursor
	}
	if cursor != nil {
		b.truncated = true
		t := true
		b.out.RowsTruncated = &t
		b.out.Complete = false
		reason := fmt.Sprintf("the view matches more than %d rows; only the first %d are carried and no total is computed over a truncated set",
			maxViewResultRows, len(b.out.Rows))
		b.out.CompleteReason = &reason
	}

	// The rows the row map indexes must be the FINAL slice elements: the
	// appends above may have reallocated the backing array, so the map is
	// rebuilt once over the settled slice rather than trusted.
	b.rowByPath = make(map[string]*gen.VaultFindRow, len(b.out.Rows))
	for i := range b.out.Rows {
		if _, dup := b.rowByPath[b.out.Rows[i].Path]; !dup {
			b.rowByPath[b.out.Rows[i].Path] = &b.out.Rows[i]
		}
	}
	return nil
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
	resp, err := knowledgefind.Find(b.ctx, b.env.Deps, req)
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

// viewUnitValue reads a row's unit cell. ok=false is G3's trigger: the unit
// is ABSENT, UNREADABLE, or MULTI-VALUED (a row carrying two units for one
// number has not confirmed which one the number is in).
func viewUnitValue(cell string) (string, bool) {
	if cell == "" || cell == "(unreadable)" {
		return "", false
	}
	if strings.Contains(cell, ", ") {
		return "", false
	}
	return cell, true
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
) (totals []gen.ViewUnitTotal, excludedPaths []string) {
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
			u, ok := viewUnitValue(viewCellValue(row, unitProp))
			if !ok {
				excludedPaths = append(excludedPaths, row.Path)
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
	return totals, excludedPaths
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

// renderViewAverage renders sum/count in exact rational arithmetic, rounded
// half-up at two digits past the sum's own scale — never through a float.
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
	outScale := scale + 2
	r := new(big.Rat).SetFrac(num, den)
	m := new(big.Int).Mul(r.Num(), viewPow10(int64(outScale)))
	q, rem := new(big.Int).QuoRem(m, r.Denom(), new(big.Int))
	if rem.Sign() != 0 {
		twice := new(big.Int).Abs(new(big.Int).Lsh(rem, 1))
		if twice.Cmp(r.Denom()) >= 0 {
			if r.Sign() >= 0 {
				q.Add(q, big.NewInt(1))
			} else {
				q.Sub(q, big.NewInt(1))
			}
		}
	}
	return records.NewDecimal(q, outScale).String()
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
func viewExcludedFields(paths []string, unitProps []string) (*int, *string, *[]string) {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		unique = append(unique, p)
	}
	n := len(unique)
	if n == 0 {
		return nil, nil, nil
	}
	sort.Strings(unique)
	reason := viewResultExcludedReason(n, unitProps)
	return &n, &reason, &unique
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
				rg := gen.ViewResultGroup{
					Key:       g.Key,
					Absent:    g.Absent,
					Count:     g.Count,
					Paths:     append([]string{}, g.Paths...),
					Subtotals: []gen.ViewUnitTotal{},
				}
				if len(subtotalProps) > 0 && !b.truncated {
					rows := b.rowsFor(g.Paths)
					excluded := make([]string, 0, len(rows))
					var unitProps []string
					for i, prop := range subtotalProps {
						unitProp, permitted := b.unitPropertyForTotals(src, prop)
						if !permitted {
							continue
						}
						totals, ex := b.aggregateViewRows(rows, prop, unitProp, subtotalOps[i])
						rg.Subtotals = append(rg.Subtotals, totals...)
						excluded = append(excluded, ex...)
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
		excluded := make([]string, 0, len(rows))
		var unitProps []string
		for i, prop := range subtotalProps {
			unitProp, permitted := b.unitPropertyForTotals(src, prop)
			if !permitted {
				continue
			}
			ts, ex := b.aggregateViewRows(rows, prop, unitProp, subtotalOps[i])
			totals = append(totals, ts...)
			excluded = append(excluded, ex...)
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
	var excluded []string
	for _, g := range groups {
		if g.Absent != nil && *g.Absent {
			// An undated row has no x position; it stays in `rows` and simply
			// is not plotted — a point at an invented date would be a value
			// nobody recorded.
			continue
		}
		totals, ex := b.aggregateViewRows(b.rowsFor(g.Paths), numberProp, unitProp, op)
		excluded = append(excluded, ex...)
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
	if src.Grouping == nil || len(*src.Grouping) < 2 {
		fix := "give the crosstab part two grouping keys through knowledge_configure"
		b.out.Problems = append(b.out.Problems, gen.RecordProblem{
			Code:    gen.AggregateRefused,
			Reason:  "a crosstab part needs two grouping keys, and this one declares fewer",
			Fix:     &fix,
			Records: []string{},
		})
		return
	}
	grouping := (*src.Grouping)[:2]
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
	var excluded []string
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
			excluded = append(excluded, ex...)
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
