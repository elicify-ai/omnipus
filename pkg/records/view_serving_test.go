// Omnipus — the checks that live on the SERVING side of the view loader
// (ADR-068 D24.1; spec FR-018b, FR-018c, FR-105, FR-148).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE IS SEPARATE FROM view_format_test.go
//
// That file covers the LOADER: what a view file may say and how a malformed
// one is refused. This one covers what happens when a perfectly valid view
// meets knowledge_find's request shape and does not fit — the three seams
// where ViewDef can express something VaultFindRequest cannot.
//
// Every one of those seams is refused WITH A REASON rather than served with
// the difference dropped, and that is the whole content of this file. A
// dropped grouping direction, a dropped formula and an ignored `disabled`
// flag all produce a query that runs, looks right, and answers a question
// nobody asked.
//
// It carries its own fixture helpers on purpose. Sharing view_format_test.go's
// would couple two files that are edited by different hands.
// ---------------------------------------------------------------------------

// bridgeFixture declares the record type these tests query.
func bridgeFixture(t *testing.T) (string, *SchemaSet) {
	t.Helper()
	root := writeVaultSchema(t, "", "lot.yaml", `
schema_version: 1
type: lot
label: Lot
identity:
  prefix: LT
properties:
  name:   { type: text, required: true }
  state:  { type: enum, values: [draft, shipped] }
  batch:  { type: integer }
`)
	set, report, err := LoadSchemas(root)
	if err != nil || !report.OK() {
		t.Fatalf("fixture schemas: %v / %+v", err, report)
	}
	return root, set
}

// loadBridgeView writes one view file and returns the view or the rejection.
func loadBridgeView(t *testing.T, root string, schemas *SchemaSet, body string) (*SavedView, *ViewRejection) {
	t.Helper()
	root = writeVaultView(t, root, "b.yaml", body)
	views, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if len(report.Rejections) == 1 {
		return nil, &report.Rejections[0]
	}
	if len(report.Rejections) > 1 {
		t.Fatalf("one file produced %d rejections: %v", len(report.Rejections), report.Rejections)
	}
	if views.Len() != 1 {
		t.Fatalf("expected one loaded view, got %d", views.Len())
	}
	return views.Views()[0], nil
}

// ---------------------------------------------------------------------------
// Seam 1 — the grouping direction, CLOSED
// ---------------------------------------------------------------------------

// TestViewFindLoader_GroupDirectionCrossesRatherThanRefuses.
//
// THIS TEST ASSERTED THE OPPOSITE UNTIL THE SEAM CLOSED. It was called
// TestViewFindLoader_DescendingGroupIsRefusedNotFlattened, and it required
// ServeRefusalGroupDirection on any view grouping DESCENDING — because
// VaultFindRequest.group_by was a bare []string with no direction field, so
// serving such a view would have reordered its groups ascending in silence.
// That was the correct behaviour while it stood: 24 real `groupBy` directions
// in the founder's vault were flattened by the retired flat view format and
// nothing anywhere said so, and refusing is the honest answer to "I cannot
// carry what you recorded".
//
// `group_by` now carries a direction per key (VaultFindGroupBy), so there is
// nothing left to refuse. What this test guards is that the direction is
// CARRIED — not that the refusal is gone. A bridge that dropped the direction
// and served the view anyway would pass a test asserting only `ok == true`,
// while doing the exact thing the refusal existed to prevent.
func TestViewFindLoader_GroupDirectionCrossesRatherThanRefuses(t *testing.T) {
	mk := func(dir *generated.ViewGroupByDirection) *SavedView {
		return &SavedView{Def: generated.ViewDef{
			Name: "g", Type: ptr("lot"),
			Grouping: ptr([]generated.ViewGroupBy{{Property: "state", Direction: dir}}),
		}}
	}

	for _, tc := range []struct {
		name string
		dir  *generated.ViewGroupByDirection
		want *generated.VaultFindGroupByDirection
	}{
		{"descending", ptr(generated.ViewGroupByDirectionDesc), ptr(generated.VaultFindGroupByDirectionDesc)},
		{"ascending", ptr(generated.ViewGroupByDirectionAsc), ptr(generated.VaultFindGroupByDirectionAsc)},
		{"unspecified", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader := NewViewFindLoader(newSet(mk(tc.dir)))

			if r, has := loader.ServeRefusal("g"); has {
				t.Fatalf("the view is refused as %s: %s\nA direction the request can carry is not a reason to refuse the view.", r.Code, r.Reason)
			}
			req, ok := loader.View("g")
			if !ok {
				t.Fatal("View(\"g\") = false; the request carries a group direction now, so this view is servable")
			}
			if req.GroupBy == nil || len(*req.GroupBy) != 1 {
				t.Fatalf("GroupBy = %+v, want one key on state", req.GroupBy)
			}
			got := (*req.GroupBy)[0]
			if got.Property != "state" {
				t.Fatalf("GroupBy[0].Property = %q, want state", got.Property)
			}
			switch {
			case tc.want == nil && got.Direction != nil:
				t.Fatalf("the view declares NO direction and the request came back with %q; "+
					"asc is the documented default, stated rather than declared, so filling it in here "+
					"invents a declaration the view file never made", string(*got.Direction))
			case tc.want != nil && got.Direction == nil:
				t.Fatalf("the view groups %q and the request came back with no direction at all — "+
					"the groups would be ordered ascending with nothing anywhere to say so, "+
					"which is the exact silence ServeRefusalGroupDirection used to refuse rather than perform",
					string(*tc.dir))
			case tc.want != nil && *got.Direction != *tc.want:
				t.Fatalf("GroupBy[0].Direction = %q, want %q", string(*got.Direction), string(*tc.want))
			}
		})
	}
}

// TestServeRefusalGroupDirection_IsNeverEmitted pins the retired refusal code.
//
// The constant is kept for readers (see its doc comment on ViewServeRefusalCode
// — it is wire-visible, it has appeared in operator-facing refusal text and in
// the importer's own loss report, and a reader searching for why a view was
// once unservable should land on the explanation rather than on nothing). What
// must not come back is a code path that PRODUCES it.
//
// It walks a DESCENDING view — the only shape that ever produced this code —
// through the same Unservable() report an operator reads, so a reintroduced
// refusal fails here rather than being discovered as a missing view.
func TestServeRefusalGroupDirection_IsNeverEmitted(t *testing.T) {
	loader := NewViewFindLoader(newSet(&SavedView{Def: generated.ViewDef{
		Name: "descending", Type: ptr("lot"),
		Grouping: ptr([]generated.ViewGroupBy{
			{Property: "state", Direction: ptr(generated.ViewGroupByDirectionDesc)},
			{Property: "maker", Direction: ptr(generated.ViewGroupByDirectionDesc)},
		}),
	}}))
	for _, r := range loader.Unservable() {
		if r.Code == ServeRefusalGroupDirection {
			t.Fatalf("a view was refused as %s: %s\nThe request carries a group direction per key now; refusing the view makes every descending grouping in the vault unservable again.", r.Code, r.Reason)
		}
	}
}

// ---------------------------------------------------------------------------
// Seam 2 — formulas, which a find request has no key for
// ---------------------------------------------------------------------------

// TestViewFindLoader_FormulaViewIsServedAndItsSourcesHandedOver.
//
// THIS TEST ASSERTED THE OPPOSITE UNTIL THE SEAM CLOSED. It was called
// TestViewFindLoader_FormulaViewIsRefusedWithAReason, and it required
// ServeRefusalFormula on any view declaring `formulas` — on the premise that
// VaultFindRequest carries no formulas, so every `formula.<name>` would resolve
// against nothing.
//
// The premise was true and the conclusion was not. The formulas never needed to
// travel INSIDE the request: knowledgefind asks its LOADER for them
// (ViewFormulaLoader), validates them into the query's namespace, and resolves
// every reference against a real declaration. What the old refusal actually did
// was make the importer's whole formula capability unreachable — a `.base`
// file's `formulas:` block could be translated perfectly and written to disk,
// and the resulting view would be dropped from Names(), reported unknown by the
// `view` argument, and unservable.
//
// So the assertion is inverted, and it is inverted in BOTH halves deliberately:
// a loader that serves the view without handing over its sources is worse than
// the refusal it replaced — the view would run, return rows, and quietly answer
// a different question.
func TestViewFindLoader_FormulaViewIsServedAndItsSourcesHandedOver(t *testing.T) {
	root, schemas := bridgeFixture(t)
	v, rej := loadBridgeView(t, root, schemas, `
name: calc
type: lot
formulas:
  doubled: batch * 2
properties: [name, formula.doubled]
`)
	if rej != nil {
		t.Fatalf("a valid formula view must LOAD; it was rejected: %s", rej)
	}

	loader := NewViewFindLoader(newSet(v))
	if _, ok := loader.View("calc"); !ok {
		t.Fatal("a view declaring formulas was NOT served; the formulas reach knowledge_find through the loader, so there is nothing for the request to carry and nothing to refuse")
	}
	if refusal, has := loader.ServeRefusal("calc"); has {
		t.Fatalf("ServeRefusal = %+v, want servable", refusal)
	}
	if names := loader.Names(); len(names) != 1 || names[0] != "calc" {
		t.Fatalf("Names() = %v, want [calc] — a servable view must be listed, or the `view` argument reports it unknown", names)
	}

	// The other half: the SOURCES must come across, spelled exactly as stored
	// (FR-141 keeps a view's formulas as source text).
	sources, ok := loader.Formulas("calc")
	if !ok {
		t.Fatal("Formulas(calc) reported the view does not exist")
	}
	if got := sources["doubled"]; got != "batch * 2" {
		t.Errorf("Formulas(calc)[doubled] = %q, want %q", got, "batch * 2")
	}

	// A view that declares NO formulas is not the same fact as a view that does
	// not exist, and the loader must keep them apart or knowledgefind cannot.
	if _, ok := loader.Formulas("no-such-view"); ok {
		t.Error("Formulas() reported ok for a view that does not exist")
	}

	// The map is a COPY. Handing out the ViewSet's own map would let a caller
	// rewrite the saved view, and the next query would silently ask a different
	// question with the file on disk still saying the original.
	sources["doubled"] = "MUTATED"
	again, _ := loader.Formulas("calc")
	if again["doubled"] != "batch * 2" {
		t.Errorf("mutating the returned map changed the SAVED view: %q", again["doubled"])
	}
}

// ---------------------------------------------------------------------------
// Seam 3 — FR-105: a disabled view is never applied
// ---------------------------------------------------------------------------

// TestViewFindLoader_DisabledViewIsNeverApplied is the broadening prohibition
// made structural.
//
// An imported view whose untranslatable expression sat in a ROW-SET-AFFECTING
// position is stored DISABLED, precisely because applying it would return
// more rows than the original — the standing example being a base that also
// filtered out two scratch folders, whose loss leaves a view that still runs,
// still looks right, and now includes every scratch note in the vault.
//
// THE VIEW BELOW TRANSLATES PERFECTLY, and that is the whole point of the
// test: without an explicit check the flag is invisible, because nothing else
// about the view is wrong.
func TestViewFindLoader_DisabledViewIsNeverApplied(t *testing.T) {
	v := &SavedView{Def: generated.ViewDef{
		Name: "d", Type: ptr("lot"),
		Disabled:     ptr(true),
		Untranslated: ptr([]string{`!inFolder("99-Temp")`}),
		Filter: &generated.VaultFilterNode{
			Property: ptr("state"), Op: ptr(generated.Equal), Value: ptr("shipped"),
		},
	}}
	loader := NewViewFindLoader(newSet(v))

	if _, ok := loader.View("d"); ok {
		t.Fatal("a disabled view was served; its filter translates cleanly, which is exactly why `disabled` must be CHECKED rather than inferred from a translation failure")
	}
	refusal, has := loader.ServeRefusal("d")
	if !has || refusal.Code != ServeRefusalDisabled {
		t.Fatalf("ServeRefusal = %+v (has=%v), want %s", refusal, has, ServeRefusalDisabled)
	}
	if !strings.Contains(refusal.Reason, "99-Temp") {
		t.Errorf("FR-105 requires the refusal to name the expression that disabled the view: %s", refusal.Reason)
	}
	for _, n := range loader.Names() {
		if n == "d" {
			t.Fatal("Names() offers a disabled view")
		}
	}
}

// ---------------------------------------------------------------------------
// FR-018c — the reserved namespaces
// ---------------------------------------------------------------------------

// TestView_ReservedNamespacesInAViewsPropertyPositions.
//
// `file.*` is valid in every property position of a view, and the interesting
// half is the boundary: a name INSIDE the namespace that is not one of the
// thirteen is a typo rather than an extension point, and `file.file` — the
// note itself — renders without comparing (FR-130).
func TestView_ReservedNamespacesInAViewsPropertyPositions(t *testing.T) {
	root, schemas := bridgeFixture(t)

	t.Run("file.* is accepted in a filter and a sort", func(t *testing.T) {
		if _, rej := loadBridgeView(t, root, schemas, `
name: recent
type: lot
filter:
  property: file.mtime
  op: ">"
  value: "2026-01-01"
sort: [{property: file.mtime, direction: desc}]
`); rej != nil {
			t.Fatalf("the reserved file namespace was rejected: %s", rej)
		}
	})

	t.Run("a misspelling inside the namespace is refused, naming the real ones", func(t *testing.T) {
		_, rej := loadBridgeView(t, root, schemas,
			"name: recent\ntype: lot\nsort: [{property: file.mtimes, direction: desc}]\n")
		if rej == nil {
			t.Fatal("`file.mtimes` loaded; a name in the reserved namespace that is not one of the thirteen is a typo, not an extension point")
		}
		if rej.Code != RejectViewUnknownProperty {
			t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewUnknownProperty, rej.Reason)
		}
		if !strings.Contains(rej.Reason, FileMtimeProp) {
			t.Errorf("the refusal does not list the file properties that would have worked: %s", rej.Reason)
		}
	})

	t.Run("file.file renders but does not compare", func(t *testing.T) {
		_, rej := loadBridgeView(t, root, schemas,
			"name: recent\ntype: lot\nsort: [{property: file.file, direction: desc}]\n")
		if rej == nil {
			t.Fatalf("`%s` is the note itself and is not a comparison target (FR-130); sorting on it must be refused", FileSelfProp)
		}
		if rej.Code != RejectViewUnknownProperty {
			t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewUnknownProperty, rej.Reason)
		}
		if _, rej2 := loadBridgeView(t, root, schemas,
			"name: recent\ntype: lot\nproperties: [file.file, name]\n"); rej2 != nil {
			t.Fatalf("`%s` must stay RENDERABLE in `properties`; the refusal is scoped to comparison positions: %s", FileSelfProp, rej2)
		}
	})
}

// ---------------------------------------------------------------------------
// The tree is WALKED, not counted
// ---------------------------------------------------------------------------

// TestView_FilterTreeLeafPropertiesAreValidatedAtEveryDepth.
//
// Counting a tree's leaves and validating them are two different walks, and a
// bound check that only counted would let an undeclared property three levels
// down through. The refusal must also LOCATE the fault: "unknown property"
// over a twelve-leaf disjunction sends the operator reading the whole file.
func TestView_FilterTreeLeafPropertiesAreValidatedAtEveryDepth(t *testing.T) {
	root, schemas := bridgeFixture(t)
	_, rej := loadBridgeView(t, root, schemas, `
name: nested
type: lot
filter:
  any:
    - property: state
      op: "="
      value: draft
    - all:
        - property: batch
          op: ">"
          value: "1"
        - not:
            property: nonesuch
            op: "="
            value: x
`)
	if rej == nil {
		t.Fatal("an undeclared property buried three levels down was accepted; the tree is being counted but not walked")
	}
	if rej.Code != RejectViewUnknownProperty {
		t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewUnknownProperty, rej.Reason)
	}
	if !strings.Contains(rej.Reason, "filter.any[1].all[1].not") {
		t.Errorf("the refusal does not locate the fault in the tree, so an operator cannot find it: %s", rej.Reason)
	}
	if !strings.Contains(rej.Reason, "state") {
		t.Errorf("the refusal does not list the properties that would have worked (FR-024): %s", rej.Reason)
	}
}

// ---------------------------------------------------------------------------
// FR-148 — a formula reference cycle
// ---------------------------------------------------------------------------

// TestView_FormulaCycleIsRefusedAtLoad.
//
// `a: formula.b + 1` and `b: formula.a + 1` both PARSE cleanly. Nothing about
// either expression is malformed; the fault is in the reference GRAPH, and an
// evaluator that followed it would recurse forever. FR-148 requires the cycle
// to be refused at write AND at load, so a hand-edited file cannot introduce
// one.
func TestView_FormulaCycleIsRefusedAtLoad(t *testing.T) {
	root, schemas := bridgeFixture(t)
	_, rej := loadBridgeView(t, root, schemas, `
name: looping
type: lot
formulas:
  a: formula.b + 1
  b: formula.a + 1
`)
	if rej == nil {
		t.Fatal("a formula reference cycle loaded; both expressions parse, so nothing but an explicit graph check can catch it, and an evaluator following it would not return")
	}
	if rej.Code != RejectViewInvalidFormula {
		t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewInvalidFormula, rej.Reason)
	}
	// The refusal must name the formulas involved — a cycle reported without
	// its path leaves the author guessing which of sixteen formulas to look at.
	for _, want := range []string{"a", "b"} {
		if !strings.Contains(rej.Reason, want) {
			t.Errorf("the refusal does not name formula %q in the cycle: %s", want, rej.Reason)
		}
	}
}
