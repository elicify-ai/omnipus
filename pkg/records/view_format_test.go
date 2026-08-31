// Omnipus — tests for the ViewDef loader (ADR-068 D24.1,
// spec FR-018b..FR-018d, FR-109).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS ARE ORACLED AGAINST
//
// Every expected value below is read off spec FR-018b..FR-018d and FR-109, not
// off view.go. Where a number appears (64 leaves, depth 8) it is FR-023c's,
// quoted at the assertion.
//
// Every view here is written to a real vault directory and read back through
// LoadViews, never hand-built as a struct. The requirement is about files on
// disk, and a struct literal skips the whole decode — which is where the
// interesting failures live, because a key that decodes to nothing produces a
// view that parsed cleanly and lost half of itself.
// ---------------------------------------------------------------------------

// widgetSchemaFixture declares the record type the view fixtures below query.
// `state`, `maker` and `batch` mirror viewFixtureSchemas in view_test.go, so
// the two files' views describe the same shape of vault.
const widgetSchemaFixture = `
schema_version: 1
type: widget
label: Widget
identity:
  prefix: WI
properties:
  name:   { type: text, required: true }
  state:  { type: enum, values: [draft, shipped, withdrawn] }
  maker:  { type: relation, to: foundry }
  batch:  { type: integer }
  labels: { type: text, many: true }
`

const foundrySchemaFixture = `
schema_version: 1
type: foundry
properties:
  name:   { type: text }
  region: { type: enum, values: [north, south] }
`

// viewVault writes the two fixture schemas plus the named view files into one
// vault and loads both, returning the view set and the load report.
//
// It returns the REPORT rather than failing on a rejection, because half the
// cases below are about the rejection.
func viewVault(t *testing.T, views map[string]string) (*ViewSet, *ViewLoadReport, *SchemaSet) {
	t.Helper()
	root := writeVaultSchema(t, "", "widget.yaml", widgetSchemaFixture)
	root = writeVaultSchema(t, root, "foundry.yaml", foundrySchemaFixture)
	schemas, sreport, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !sreport.OK() {
		t.Fatalf("fixture schemas did not load: %v", sreport.Rejections)
	}
	for filename, body := range views {
		root = writeVaultView(t, root, filename, body)
	}
	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	return set, report, schemas
}

// mustLoadView loads one view file and fails with the rejection if it did not
// load — the common case, spelled once.
func mustLoadView(t *testing.T, filename, body string) *SavedView {
	t.Helper()
	set, report, _ := viewVault(t, map[string]string{filename: body})
	if !report.OK() {
		t.Fatalf("the view was rejected: %v", report.Rejections)
	}
	if set.Len() != 1 {
		t.Fatalf("expected exactly one loaded view, got %d (%v)", set.Len(), set.Names())
	}
	return set.Views()[0]
}

// ---------------------------------------------------------------------------
// FR-018b — the grammar, whole
// ---------------------------------------------------------------------------

// TestView_TreeExpressesDisjunction is spec test 90.
//
// FR-018b's opening sentence is the requirement: "Seven filter groups in the
// founder's vault use disjunction; none was expressible as a view." So the
// case is a view whose filter is `{any: [...]}` with a nested `{all: [...]}`,
// carrying the rest of the grammar alongside it — a directional `grouping`, a
// `layout`, a `property_config` — read off disk and then handed to the
// knowledge_find bridge.
//
// THE TREE MUST ARRIVE AT FIND UNCHANGED. A view's `filter` already IS find's
// VaultFilterNode, so anything other than an identical tree on the other side
// is a translation, and the whole point of the format is that it needs none.
func TestView_TreeExpressesDisjunction(t *testing.T) {
	v := mustLoadView(t, "active.yaml", `
name: active-widgets
type: widget
label: Active widgets
layout: cards
filter:
  any:
    - property: state
      op: "="
      value: shipped
    - all:
        - property: state
          op: "="
          value: draft
        - property: batch
          op: ">="
          value: "7"
    - not:
        property: batch
        op: "IS NULL"
grouping:
  - property: state
  - property: maker
    direction: asc
sort:
  - property: name
    direction: asc
properties: [name, state]
limit: 25
property_config:
  state:
    display_name: Stage
`)

	// ── the tree ───────────────────────────────────────────────────────────
	if v.Def.Filter == nil {
		t.Fatal("the `filter` tree did not survive the load")
	}
	root := *v.Def.Filter
	if root.Any == nil {
		t.Fatalf("the root node is not a disjunction: %+v", root)
	}
	if len(*root.Any) != 3 {
		t.Fatalf("the disjunction has %d branches, want 3", len(*root.Any))
	}
	branches := *root.Any

	// branch 0 — a plain leaf.
	assertLeaf(t, branches[0], "state", generated.Equal, "shipped")

	// branch 1 — a nested conjunction.
	if branches[1].All == nil || len(*branches[1].All) != 2 {
		t.Fatalf("branch 1 is not a two-child `all`: %+v", branches[1])
	}
	assertLeaf(t, (*branches[1].All)[0], "state", generated.Equal, "draft")
	assertLeaf(t, (*branches[1].All)[1], "batch", generated.GreaterThanEqual, "7")

	// branch 2 — tree negation, which is NOT `<>` (spec §8 R-2).
	if branches[2].Not == nil {
		t.Fatalf("branch 2 is not a `not`: %+v", branches[2])
	}
	if branches[2].Not.Op == nil || *branches[2].Not.Op != generated.ISNULL {
		t.Fatalf("the negated leaf's operator is %v, want `IS NULL`", branches[2].Not.Op)
	}

	// ── grouping carries a direction ───────────────────────────────────────
	// FR-018b: "grouping entries are `{property, direction: asc|desc}`" — the
	// bare name list this replaced dropped 24 real direction declarations in
	// the founder's own vault, silently.
	if v.Def.Grouping == nil || len(*v.Def.Grouping) != 2 {
		t.Fatalf("grouping = %+v, want two keys", v.Def.Grouping)
	}
	g := *v.Def.Grouping
	if g[0].Property != "state" || g[0].Direction != nil {
		t.Errorf("grouping[0] = %+v; an omitted direction must stay ABSENT — the contract states asc is the default rather than declaring one, so a loader that filled it in would be inventing a declaration the file never made", g[0])
	}
	if g[1].Property != "maker" || g[1].Direction == nil || *g[1].Direction != generated.ViewGroupByDirectionAsc {
		t.Errorf("grouping[1] = %+v, want maker/asc", g[1])
	}

	// ── layout and display config ──────────────────────────────────────────
	if v.Def.Layout == nil || *v.Def.Layout != generated.ViewDefLayoutCards {
		t.Errorf("layout = %v, want cards (FR-109)", v.Def.Layout)
	}
	if v.Def.PropertyConfig == nil {
		t.Fatal("property_config did not survive the load")
	}
	cfg, ok := (*v.Def.PropertyConfig)["state"]
	if !ok || cfg.DisplayName == nil || *cfg.DisplayName != "Stage" {
		t.Errorf("property_config[state] = %+v, want display_name Stage", cfg)
	}

	// ── and it produces the right query ────────────────────────────────────
	req, served := NewViewFindLoader(newSet(v)).View("active-widgets")
	if !served {
		t.Fatal("a view using the full grammar was not servable; the grammar is find's own, so it must be")
	}
	if req.Type == nil || *req.Type != "widget" {
		t.Errorf("request type = %v, want widget", req.Type)
	}
	if !reflect.DeepEqual(req.Filter, v.Def.Filter) {
		t.Errorf("the request's filter differs from the view's own tree.\n view: %s\n  req: %s",
			renderNode(*v.Def.Filter), renderNode(*req.Filter))
	}
	if req.Filter == v.Def.Filter {
		t.Error("the request aliases the saved view's own tree; a request the engine normalises in place would then rewrite the view on disk's in-memory copy")
	}
	if req.GroupBy == nil || !reflect.DeepEqual(*req.GroupBy, []string{"state", "maker"}) {
		t.Errorf("request group_by = %v, want [state maker]", req.GroupBy)
	}
	if req.Select == nil || !reflect.DeepEqual(*req.Select, []string{"name", "state"}) {
		t.Errorf("request select = %v, want [name state]", req.Select)
	}
	if req.Limit == nil || *req.Limit != 25 {
		t.Errorf("request limit = %v, want 25", req.Limit)
	}
	if req.Sort == nil || len(*req.Sort) != 1 || (*req.Sort)[0].Property != "name" {
		t.Errorf("request sort = %+v, want one key on name", req.Sort)
	}
}

// assertLeaf checks one leaf node's three fields at once, so a failure names
// which of them was wrong rather than dumping a struct.
func assertLeaf(t *testing.T, n generated.VaultFilterNode, property string, op generated.VaultFilterNodeOp, value string) {
	t.Helper()
	if n.Property == nil || *n.Property != property {
		t.Errorf("leaf property = %v, want %q", n.Property, property)
	}
	if n.Op == nil || *n.Op != op {
		t.Errorf("leaf operator = %v, want %q", n.Op, string(op))
	}
	if n.Value == nil || *n.Value != value {
		t.Errorf("leaf value = %v, want %q", n.Value, value)
	}
}

// renderNode renders a filter tree as one line, so a diff failure is readable.
func renderNode(n generated.VaultFilterNode) string {
	switch {
	case n.All != nil:
		return "all(" + renderChildren(*n.All) + ")"
	case n.Any != nil:
		return "any(" + renderChildren(*n.Any) + ")"
	case n.Not != nil:
		return "not(" + renderNode(*n.Not) + ")"
	}
	op, val := "", ""
	if n.Op != nil {
		op = string(*n.Op)
	}
	if n.Value != nil {
		val = *n.Value
	}
	return fmt.Sprintf("%s %s %q", derefOrEmpty(n.Property), op, val)
}

func renderChildren(children []generated.VaultFilterNode) string {
	parts := make([]string, 0, len(children))
	for _, c := range children {
		parts = append(parts, renderNode(c))
	}
	return strings.Join(parts, ", ")
}

// ---------------------------------------------------------------------------
// FR-109 — layout is carried, and an unrenderable one is not flattened
// ---------------------------------------------------------------------------

// TestView_LayoutIsCarriedAndPoliced covers the measured failure FR-109 was
// written after: "An Obsidian CARDS view imported as a table, recorded no loss
// at all, and scored CLEAN under the parity exit criterion."
//
// Two halves, and the second is the one that matters. Carrying `cards` is
// necessary; REFUSING an unrecognised layout is what stops the flattening,
// because a bare string field accepts anything and an unrecognised value
// renders as the default table with nobody told.
func TestView_LayoutIsCarriedAndPoliced(t *testing.T) {
	// Every declared layout survives the round trip, including the four the
	// SPA does not render — they exist precisely so the importer can RECORD
	// what an Obsidian view asked for.
	for _, layout := range viewLayoutNames() {
		t.Run("carried: "+layout, func(t *testing.T) {
			v := mustLoadView(t, "l.yaml", fmt.Sprintf(
				"name: l\ntype: widget\nlayout: %s\n", layout))
			if v.Def.Layout == nil {
				t.Fatalf("layout %q was dropped on load", layout)
			}
			if string(*v.Def.Layout) != layout {
				t.Fatalf("layout = %q, want %q", string(*v.Def.Layout), layout)
			}
		})
	}

	// An omitted layout stays ABSENT rather than being filled in with `table`.
	// The contract states table is the default; a loader that wrote it in
	// would make "the author asked for a table" and "the author said nothing"
	// indistinguishable, and the importer's loss report is built on telling
	// them apart.
	v := mustLoadView(t, "plain.yaml", "name: plain\ntype: widget\n")
	if v.Def.Layout != nil {
		t.Errorf("an omitted layout was filled in as %q; absent must stay absent", string(*v.Def.Layout))
	}

	// An unrecognised layout is REFUSED, naming the permitted set. `card`
	// (singular) is the realistic typo and the one that would otherwise render
	// as a silent table.
	for _, bad := range []string{"card", "Cards", "grid", ""} {
		t.Run("refused: "+bad, func(t *testing.T) {
			_, report, _ := viewVault(t, map[string]string{
				"bad.yaml": fmt.Sprintf("name: bad\ntype: widget\nlayout: %q\n", bad),
			})
			if report.OK() {
				t.Fatalf("layout %q loaded; an unrecognised layout renders as the default table with nobody told (FR-109)", bad)
			}
			rej := report.Rejections[0]
			if rej.Code != RejectViewInvalidLayout {
				t.Fatalf("code = %s, want %s (%s)", rej.Code, RejectViewInvalidLayout, rej.Reason)
			}
			if !strings.Contains(rej.Reason, "cards") || !strings.Contains(rej.Reason, "table") {
				t.Errorf("the refusal does not list the permitted layouts: %s", rej.Reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR-018b / FR-018d — the optional type
// ---------------------------------------------------------------------------

// TestView_UntypedViewLoads covers FR-018b's "`type` is OPTIONAL" and the two
// edges either side of it.
//
// A `type:` that is present but blank is a typo, never the deliberate absence
// — treating it as untyped would turn a misspelling into a vault-wide query.
// And a type NO schema declares stays refused: the optional type makes absence
// legal, not an unknown name.
func TestView_UntypedViewLoads(t *testing.T) {
	// An untyped view loads, and its ordinary property names are NOT
	// checked against any single schema: FR-018b resolves them by name over
	// FR-021e's rows at query time, so there is no name the loader could
	// refuse without refusing a query FR-018b requires to work.
	v := mustLoadView(t, "folder.yaml", `
name: folder-scoped
filter:
  property: undeclared_anywhere
  op: IS NOT NULL
properties: [name, undeclared_anywhere]
`)
	if v.Def.Type != nil {
		t.Fatalf("type = %v, want absent", v.Def.Type)
	}

	// A blank type is a typo, not the deliberate absence.
	if _, rej := ParseView("/v/x.yaml", []byte("name: v\ntype: \"   \"\n")); rej == nil || rej.Code != RejectViewMissingType {
		t.Fatalf("a blank `type` must be refused as a typo, got %+v", rej)
	}

	// A type NO schema declares is still drift (FR-018d: "`RejectViewUnknownType`
	// still fires for a type NO schema declares — that is drift, not
	// provisioning").
	_, report, _ := viewVault(t, map[string]string{
		"gone.yaml": "name: gone\ntype: sprocket\n",
	})
	if report.OK() {
		t.Fatal("a view naming an undeclared type loaded; the optional type does not make an unknown one acceptable")
	}
	if report.Rejections[0].Code != RejectViewUnknownType {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewUnknownType, report.Rejections[0].Reason)
	}
}

// ---------------------------------------------------------------------------
// FR-023c — the tree bound, applied to a view
// ---------------------------------------------------------------------------

// TestView_FilterTreeBoundsAreRefusedAtLoad checks the two numbers FR-023c
// states, on the side of each that must fail.
//
// The bound is measured at LOAD rather than left to the query path because a
// view is written once and evaluated forever: a tree that will be refused on
// every query should be refused when it is stored, naming which bound it
// broke.
func TestView_FilterTreeBoundsAreRefusedAtLoad(t *testing.T) {
	// FR-023c's two numbers, WRITTEN OUT rather than read from the code's own
	// constants. A test that sized its fixtures from maxViewFilterLeaves would
	// follow the constant anywhere it was moved to, and pass at a cap of 128
	// as happily as at 64 — which is precisely the guard failing open.
	const specLeafCap = 64
	const specDepthCap = 8
	if maxViewFilterLeaves != specLeafCap || maxViewFilterDepth != specDepthCap {
		t.Fatalf("the code caps a view filter at %d leaves / depth %d; FR-023c states %d and %d",
			maxViewFilterLeaves, maxViewFilterDepth, specLeafCap, specDepthCap)
	}

	leafYAML := func(indent string) string {
		return indent + "- property: batch\n" + indent + "  op: IS NOT NULL\n"
	}
	flat := func(n int) string {
		var b strings.Builder
		b.WriteString("name: wide\ntype: widget\nfilter:\n  all:\n")
		for i := 0; i < n; i++ {
			b.WriteString(leafYAML("    "))
		}
		return b.String()
	}

	// 64 leaves is the cap and must LOAD; 65 must not.
	if _, report, _ := viewVault(t, map[string]string{"w.yaml": flat(specLeafCap)}); !report.OK() {
		t.Fatalf("a tree at FR-023c's cap of %d leaves was refused: %v", specLeafCap, report.Rejections)
	}
	_, report, _ := viewVault(t, map[string]string{"w.yaml": flat(specLeafCap + 1)})
	if report.OK() {
		t.Fatalf("a tree of %d leaves loaded; FR-023c caps a filter at %d", specLeafCap+1, specLeafCap)
	}
	if report.Rejections[0].Code != RejectViewFilterTooLarge {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewFilterTooLarge, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "64") {
		t.Errorf("the refusal does not name the bound it broke: %s", report.Rejections[0].Reason)
	}

	// Depth. `not` nests one node per level, so the depth is exact.
	nested := func(levels int) string {
		var b strings.Builder
		b.WriteString("name: deep\ntype: widget\nfilter:\n")
		indent := "  "
		for i := 0; i < levels-1; i++ {
			b.WriteString(indent + "not:\n")
			indent += "  "
		}
		b.WriteString(indent + "property: batch\n" + indent + "op: IS NOT NULL\n")
		return b.String()
	}
	if _, atCap, _ := viewVault(t, map[string]string{"d.yaml": nested(specDepthCap)}); !atCap.OK() {
		t.Fatalf("a tree at FR-023c's depth cap of %d was refused: %v", specDepthCap, atCap.Rejections)
	}
	_, report, _ = viewVault(t, map[string]string{"d.yaml": nested(specDepthCap + 1)})
	if report.OK() {
		t.Fatalf("a tree %d levels deep loaded; FR-023c caps depth at %d", specDepthCap+1, specDepthCap)
	}
	if report.Rejections[0].Code != RejectViewFilterTooLarge {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewFilterTooLarge, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "8") {
		t.Errorf("the refusal does not name the depth bound it broke: %s", report.Rejections[0].Reason)
	}

	// A node that is neither a leaf nor a combinator, and one that is both.
	for _, tc := range []struct{ name, filter string }{
		{"empty node", "filter:\n  {}\n"},
		{"leaf and combinator at once", "filter:\n  property: batch\n  op: IS NOT NULL\n  all:\n    - property: batch\n      op: IS NULL\n"},
		{"childless combinator", "filter:\n  all: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "name: odd\ntype: widget\n" + tc.filter
			_, report, _ := viewVault(t, map[string]string{"o.yaml": body})
			if report.OK() {
				t.Fatal("a malformed filter node loaded")
			}
			if report.Rejections[0].Code != RejectViewInvalidFilterNode {
				t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
					RejectViewInvalidFilterNode, report.Rejections[0].Reason)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FR-140s / FR-018b — formulas and property_config
// ---------------------------------------------------------------------------

// TestView_FormulasAreRevalidatedOnLoad covers the second half of FR-140's
// rule: "The parser lives in the write path … the view loader re-validates on
// load, so a hand-edited file is re-checked."
//
// A view file is a text file an operator can open. If only the writer checked
// formulas, an edit made in a text editor would be discovered broken at query
// time — the failure this loader exists to move forward.
func TestView_FormulasAreRevalidatedOnLoad(t *testing.T) {
	// A valid formula loads, and is referenceable as `formula.<name>` in a
	// property position (FR-018c's reserved namespace).
	v := mustLoadView(t, "calc.yaml", `
name: calc
type: widget
formulas:
  doubled: batch * 2
properties: [name, formula.doubled]
`)
	if v.Def.Formulas == nil || (*v.Def.Formulas)["doubled"] != "batch * 2" {
		t.Fatalf("the formula source text did not survive verbatim: %+v", v.Def.Formulas)
	}

	// A reference to a formula the view does not declare is refused, naming
	// what IS declared — a dangling `formula.` reference would otherwise
	// resolve against nothing and return an empty column in silence.
	_, report, _ := viewVault(t, map[string]string{"dangling.yaml": `
name: dangling
type: widget
formulas:
  doubled: batch * 2
properties: [formula.tripled]
`})
	if report.OK() {
		t.Fatal("a reference to an undeclared formula loaded")
	}
	if report.Rejections[0].Code != RejectViewUnknownFormula {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewUnknownFormula, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "doubled") {
		t.Errorf("the refusal does not list the formulas that ARE declared: %s", report.Rejections[0].Reason)
	}

	// An expression that does not parse is refused at LOAD, not stored and
	// discovered later.
	_, report, _ = viewVault(t, map[string]string{"broken.yaml": `
name: broken
type: widget
formulas:
  bad: "batch * "
`})
	if report.OK() {
		t.Fatal("an unparseable formula loaded; FR-140 requires the loader to re-check a hand-edited file")
	}
	if report.Rejections[0].Code != RejectViewInvalidFormula {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewInvalidFormula, report.Rejections[0].Reason)
	}

	// property_config keys are PROPERTY names and are checked as such — a
	// config entry for a property that does not exist is a column heading
	// nothing will ever render.
	_, report, _ = viewVault(t, map[string]string{"cfg.yaml": `
name: cfg
type: widget
property_config:
  nonexistent:
    display_name: Ghost
`})
	if report.OK() {
		t.Fatal("property_config named an undeclared property and loaded")
	}
	if report.Rejections[0].Code != RejectViewUnknownProperty {
		t.Fatalf("code = %s, want %s (%s)", report.Rejections[0].Code,
			RejectViewUnknownProperty, report.Rejections[0].Reason)
	}
	if !strings.Contains(report.Rejections[0].Reason, "property_config") {
		t.Errorf("the refusal does not say WHERE the bad name is: %s", report.Rejections[0].Reason)
	}
}
