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
// Seam 1 — the grouping direction find's request cannot carry
// ---------------------------------------------------------------------------

// TestViewFindLoader_DescendingGroupIsRefusedNotFlattened.
//
// VaultFindRequest.group_by is a bare []string with NO direction field.
// Serving a descending grouping through it would reorder the groups
// ascending in silence — which is precisely the failure ViewGroupBy was added
// to end: 24 real `groupBy` directions in the founder's vault were flattened
// to the default and nothing anywhere said so.
//
// An ASCENDING key, and one with no direction at all, still cross — because
// ascending IS the documented default, so for those nothing is lost. That
// asymmetry is the test's real content: a blanket refusal would pass the
// first assertion while making the feature unusable.
func TestViewFindLoader_DescendingGroupIsRefusedNotFlattened(t *testing.T) {
	mk := func(dir *generated.ViewGroupByDirection) *SavedView {
		return &SavedView{Def: generated.ViewDef{
			Name: "g", Type: ptr("lot"),
			Grouping: ptr([]generated.ViewGroupBy{{Property: "state", Direction: dir}}),
		}}
	}

	desc := NewViewFindLoader(newSet(mk(ptr(generated.ViewGroupByDirectionDesc))))
	if req, ok := desc.View("g"); ok {
		t.Fatalf("a descending grouping was served as group_by=%v, through a request that cannot carry a direction; the groups would come back ascending with nothing to say so", req.GroupBy)
	}
	refusal, has := desc.ServeRefusal("g")
	if !has {
		t.Fatal("the view is unservable and no reason was reported")
	}
	if refusal.Code != ServeRefusalGroupDirection {
		t.Fatalf("refusal code = %q, want %q (%s)", refusal.Code, ServeRefusalGroupDirection, refusal.Reason)
	}
	if !strings.Contains(refusal.Reason, "state") {
		t.Errorf("the reason does not name the grouping key it refused: %s", refusal.Reason)
	}

	for _, tc := range []struct {
		name string
		dir  *generated.ViewGroupByDirection
	}{
		{"ascending", ptr(generated.ViewGroupByDirectionAsc)},
		{"unspecified", nil},
	} {
		t.Run(tc.name+" crosses losslessly", func(t *testing.T) {
			req, ok := NewViewFindLoader(newSet(mk(tc.dir))).View("g")
			if !ok {
				t.Fatal("ascending is the documented default, so nothing is lost and this view must still be servable")
			}
			if req.GroupBy == nil || len(*req.GroupBy) != 1 || (*req.GroupBy)[0] != "state" {
				t.Fatalf("GroupBy = %v, want [state]", req.GroupBy)
			}
		})
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
