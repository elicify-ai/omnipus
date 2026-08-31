// Omnipus — knowledge_describe must describe a SAVED VIEW as it actually is,
// in both view formats (ADR-068 D24.1, spec FR-018b, FR-101, FR-105, FR-109).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS ARE ORACLED AGAINST, AND WHY THE MESSAGES ARE SHOUTY
//
// The defect these tests exist for is not a missing feature. It is a CONFIDENT
// WRONG ANSWER: a version-2 view — the version FR-018b says every writer emits
// — carries its filtering in `filter` (a tree) and its grouping in `grouping`,
// and this renderer read only version 1's `filters`/`group_by`. A v2 view
// whose only keys were `filter:` and `layout:` therefore produced an empty
// parts list and was described to the agent as
//
//     every record of this type, every property
//
// An agent that reads that believes a narrow, filtered view returns
// everything, and reasons and acts on that belief. Nothing downstream
// contradicts it.
//
// So the expected values below are read off the CONTRACT (ViewDef and
// VaultFilterNode in contracts/openapi.yaml) and the view FILES the fixtures
// write, never off the renderer. Every view is written to a real vault
// directory and loaded through records.LoadViews, because a struct literal
// would skip the decode where FR-018b's version partition lives — and it was
// the decode half of the format that this renderer was blind to.
//
// The failure messages name the consequence, not the diff, because the reader
// of a red build here needs to know in one line that a filtered view was
// described as unfiltered.
// ---------------------------------------------------------------------------

const describeViewWidgetSchema = `
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

const describeViewFoundrySchema = `
schema_version: 1
type: foundry
properties:
  name:   { type: text }
  region: { type: enum, values: [north, south] }
`

// describeViewVault writes the two fixture schemas plus one view file into a
// real vault and loads both through the ordinary loaders.
func describeViewVault(t *testing.T, filename, viewBody string) *records.SavedView {
	t.Helper()
	root := t.TempDir()
	writeUnderMarker(t, root, "records", "widget.yaml", describeViewWidgetSchema)
	writeUnderMarker(t, root, "records", "foundry.yaml", describeViewFoundrySchema)
	schemas, sreport, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !sreport.OK() {
		t.Fatalf("the fixture schemas did not load: %v", sreport.Rejections)
	}
	writeUnderMarker(t, root, "views", filename, viewBody)
	set, report, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture view was rejected and so proves nothing about the renderer: %v", report.Rejections)
	}
	if set.Len() != 1 {
		t.Fatalf("expected exactly one loaded view, got %d (%v)", set.Len(), set.Names())
	}
	return set.Views()[0]
}

func writeUnderMarker(t *testing.T, root, sub, filename, body string) {
	t.Helper()
	dir := filepath.Join(root, records.VaultMarkerDirName, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

// ---------------------------------------------------------------------------
// THE HEADLINE CASE — a version-2 filter tree must reach the reader
// ---------------------------------------------------------------------------

// TestDescribeViews_V2FilterTreeIsDescribed is the test the whole file is for.
//
// The view is a real version-2 file with a non-trivial tree: a disjunction,
// with a nested conjunction inside it and a tree `not` alongside — the shape
// FR-018b says version 1 could not express at all. Every one of those clauses
// NARROWS what the view returns, so every one of them must appear in the
// description.
//
// MUTATION: make the version-2 branch of renderViewBody's switch not fire
// (drop `case records.ViewVersion2`). This test fails naming the clauses that
// vanished.
func TestDescribeViews_V2FilterTreeIsDescribed(t *testing.T) {
	v := describeViewVault(t, "active.yaml", `
schema_version: 2
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
    direction: desc
sort:
  - property: name
    direction: asc
properties: [name, state]
limit: 25
property_config:
  state:
    display_name: Stage
`)

	body := renderViewBody(v)

	// Every clause below is read off the view FILE above.
	for _, want := range []string{
		"state = shipped",          // the first disjunct
		"state = draft",            // inside the nested conjunction
		"batch >= 7",               // ditto — the clause that makes it a conjunction
		" OR ",                     // the disjunction itself: v1 could not express it
		" AND ",                    // the nesting inside it
		"NOT(batch IS NULL)",       // tree negation, which is NOT the `<>` leaf
		"group state asc",          // grouping, with the direction v1's list dropped
		"maker desc",               // the declared direction
		"layout cards",             // FR-109
		"display state as 'Stage'", // property_config
	} {
		if !strings.Contains(body, want) {
			t.Errorf("A FILTERED VIEW WAS DESCRIBED WITHOUT ITS FILTERING.\n"+
				"The view file declares %q and the description never mentions it.\n"+
				"An agent reading this description will believe the view returns more than it does.\n"+
				"rendered:\n%s", want, body)
		}
	}

	if strings.Contains(body, "every record of this type") {
		t.Fatalf("A FILTERED VIEW WAS DESCRIBED AS UNFILTERED.\n"+
			"This view declares a filter tree with three branches and it was described as returning "+
			"every record of its type.\nrendered:\n%s", body)
	}
}

// TestDescribeViews_V2FilterOnlyViewIsNeverUnfiltered is the exact shape that
// produced the wrong answer: a version-2 view whose ONLY keys are `filter` and
// `layout`. Every key the old renderer knew about is absent, so its parts list
// was empty and it fell straight through to the unfiltered line.
//
// MUTATION: drop `case records.ViewVersion2` from renderViewBody's switch. The
// coverage ledger still refuses to call the view unfiltered — that is
// mechanism 3 doing its job — but this test fails on the missing clause,
// because a description that only says "something is hidden" is not the same
// as one that says what.
func TestDescribeViews_V2FilterOnlyViewIsNeverUnfiltered(t *testing.T) {
	v := describeViewVault(t, "north.yaml", `
schema_version: 2
name: north-only
type: widget
layout: table
filter:
  all:
    - property: state
      op: "="
      value: shipped
    - property: batch
      op: "<"
      value: "100"
`)

	body := renderViewBody(v)

	if strings.Contains(body, "every record of this type") {
		t.Fatalf("A FILTERED VIEW WAS DESCRIBED AS UNFILTERED — the exact defect this file guards.\n"+
			"north-only returns only shipped widgets under batch 100, and the description says it returns "+
			"every record of its type.\nrendered:\n%s", body)
	}
	for _, want := range []string{"state = shipped", "batch < 100", " AND "} {
		if !strings.Contains(body, want) {
			t.Errorf("the description omits %q, which is one of the two clauses that narrow this view.\nrendered:\n%s", want, body)
		}
	}
}

// TestDescribeViews_V2GroupingKeepsItsDirection — FR-018b's own measurement is
// that version 1's bare `group_by` name list "dropped 24 real direction
// declarations silently". A description that shows the property and not the
// direction repeats that loss one layer up.
func TestDescribeViews_V2GroupingKeepsItsDirection(t *testing.T) {
	v := describeViewVault(t, "grouped.yaml", `
schema_version: 2
name: grouped
type: widget
grouping:
  - property: state
    direction: desc
`)
	body := renderViewBody(v)
	if !strings.Contains(body, "state desc") {
		t.Fatalf("the grouping direction declared in the file (desc) is not in the description.\n"+
			"FR-018b exists because the version-1 format dropped 24 of these silently.\nrendered:\n%s", body)
	}
}

// TestDescribeViews_V2OmittedGroupDirectionShowsTheEffectiveOne — ViewGroupBy's
// contract states "Omitted means `asc`" in prose rather than as a JSON Schema
// default. The loader must not fill it in; a description of what the view DOES
// must state it, because the alternative is a reader who cannot tell which way
// the groups come out.
func TestDescribeViews_V2OmittedGroupDirectionShowsTheEffectiveOne(t *testing.T) {
	v := describeViewVault(t, "grouped2.yaml", `
schema_version: 2
name: grouped2
type: widget
grouping:
  - property: state
`)
	if v.Def.Grouping == nil || (*v.Def.Grouping)[0].Direction != nil {
		t.Fatalf("fixture precondition: the file omits the direction and the loader must leave it absent; got %+v", v.Def.Grouping)
	}
	if body := renderViewBody(v); !strings.Contains(body, "group state asc") {
		t.Fatalf("an omitted grouping direction means asc (ViewGroupBy's contract); the description must say which way the groups come out.\nrendered:\n%s", body)
	}
}

// TestDescribeViews_UnrenderableLayoutIsNamed — FR-109 declares six layouts
// and this product draws two. The field exists BECAUSE an Obsidian cards view
// once imported as a table, recorded no loss, and scored clean under the
// parity criterion. A description that prints `layout board` without saying it
// is not drawn re-tells that same half-truth.
func TestDescribeViews_UnrenderableLayoutIsNamed(t *testing.T) {
	v := describeViewVault(t, "board.yaml", `
schema_version: 2
name: board-view
type: widget
layout: board
`)
	body := renderViewBody(v)
	if !strings.Contains(body, "layout board") {
		t.Fatalf("the declared layout is missing from the description entirely.\nrendered:\n%s", body)
	}
	if !strings.Contains(body, "does not draw") {
		t.Errorf("`board` is one of the four layouts FR-109 declares and this product does not draw; "+
			"the description must say so rather than let a reader assume it renders.\nrendered:\n%s", body)
	}
}

// TestDescribeViews_V2FormulasAndDisplayConfigAreShownDeterministically —
// `formulas` and `property_config` are both MAPS, and Go map iteration order
// is randomised per run. A renderer that walked them unordered would produce a
// description that differs between two calls over the same file, which makes
// every downstream diff and every golden test worthless.
func TestDescribeViews_V2FormulasAndDisplayConfigAreShownDeterministically(t *testing.T) {
	v := describeViewVault(t, "formulas.yaml", `
schema_version: 2
name: with-formulas
type: widget
formulas:
  zeta: "batch + 1"
  alpha: "batch * 2"
property_config:
  state:
    display_name: Stage
  batch:
    display_name: Run
`)
	body := renderViewBody(v)
	for _, want := range []string{"alpha = batch * 2", "zeta = batch + 1", "batch as 'Run'", "state as 'Stage'"} {
		if !strings.Contains(body, want) {
			t.Errorf("the description omits %q; a computed property an agent can reference by name is not optional detail.\nrendered:\n%s", want, body)
		}
	}
	if strings.Index(body, "alpha") > strings.Index(body, "zeta") {
		t.Errorf("map keys must render sorted or the same file describes differently on two runs.\nrendered:\n%s", body)
	}
	for i := 0; i < 12; i++ {
		if again := renderViewBody(v); again != body {
			t.Fatalf("two renderings of one view differ:\n%s\nvs\n%s", body, again)
		}
	}
}

// TestDescribeViews_DisabledIsReportedInBothVersions — FR-105's kill switch.
// A disabled view is stored and REFUSED at serve time, so an agent that picks
// one off this list has chosen a view that can never answer. Both versions
// dropped the flag silently before.
func TestDescribeViews_DisabledIsReportedInBothVersions(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"version 1", "schema_version: 1\nname: v1-off\ntype: widget\ndisabled: true\ngroup_by: [state]\n"},
		{"version 2", "schema_version: 2\nname: v2-off\ntype: widget\ndisabled: true\nfilter: {property: state, op: \"=\", value: shipped}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := renderViewBody(describeViewVault(t, "off.yaml", tc.body))
			if !strings.Contains(body, "DISABLED") {
				t.Fatalf("a view FR-105 disabled is described as if it could be used; it returns nothing and is refused at serve time.\nrendered:\n%s", body)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// VERSION 1 MUST NOT MOVE
// ---------------------------------------------------------------------------

// TestDescribeViews_V1RenderingIsByteIdentical pins the version-1 rendering to
// the exact bytes it produced before the version-2 renderer existed.
//
// The golden below was CAPTURED from the pre-change binary in a detached
// worktree at the parent commit, not transcribed from the current code. A
// version-1 file on disk means exactly what it meant, verbatim — the same rule
// pkg/records applies on load — and its description must too.
//
// The fixture deliberately exercises every key the version-1 vocabulary has:
// filters (with `negate` and a `via` hop, the two leaves that have no faithful
// version-2 equivalent), group_by, sort, properties, aggregates, limit, and
// FR-101's untranslated report with its source attribution.
func TestDescribeViews_V1RenderingIsByteIdentical(t *testing.T) {
	v := describeViewVault(t, "v1.yaml", `
schema_version: 1
name: v1-everything
type: widget
label: Every v1 key
source: bases/widgets.base
filters:
  - {property: state, op: eq, values: [{type: enum, enum: shipped}]}
  - {property: batch, op: gt, values: [{type: integer, integer: "7"}], negate: true}
  - {property: region, op: eq, values: [{type: enum, enum: north}], via: [maker]}
group_by: [state]
sort:
  - {property: name, direction: asc}
properties: [name, state, batch]
aggregates:
  - {op: count}
  - {op: sum, property: batch}
limit: 25
untranslated: ["inFolder(\"99-Temp\")"]
`)

	const golden = "    filter state eq shipped AND NOT(batch gt 7) AND maker->region eq north; " +
		"group state; sort name asc; show name, state, batch; totals count, sum(batch); limit 25; " +
		"1 expression(s) from bases/widgets.base NOT translated and NOT applied\n"

	if got := renderViewBody(v); got != golden {
		t.Fatalf("the version-1 rendering changed. A v1 file on disk means what it always meant and its "+
			"description must be byte-identical.\n got: %q\nwant: %q", got, golden)
	}
}

// ---------------------------------------------------------------------------
// THE INVARIANT, MADE STRUCTURAL
// ---------------------------------------------------------------------------

// TestDescribeViews_EveryViewDefKeyIsAccountedFor is the mechanism that makes
// the invariant survive a future contract change.
//
// Every `json:` key on generated.ViewDef must be claimed by the head line, by
// the version-1 body renderer, or by the version-2 body renderer. Adding a key
// to ViewDef.yaml without teaching a renderer about it fails HERE, by name, at
// build time — instead of becoming a key the description silently drops.
//
// MUTATION: remove "layout" from viewV2BodyKeys. This test fails naming
// `layout`.
func TestDescribeViews_EveryViewDefKeyIsAccountedFor(t *testing.T) {
	claimed := map[string]bool{}
	for _, set := range [][]string{viewHeaderKeys, viewV1BodyKeys, viewV2BodyKeys} {
		for _, k := range set {
			claimed[k] = true
		}
	}

	wire := viewDefWireKeys()
	if len(wire) == 0 {
		t.Fatal("no json keys were read off generated.ViewDef; the reflection that guards this file is broken, " +
			"which would make every coverage check below vacuously green")
	}

	var unclaimed []string
	for _, k := range wire {
		if !claimed[k] {
			unclaimed = append(unclaimed, k)
		}
	}
	if len(unclaimed) > 0 {
		t.Errorf("generated.ViewDef declares %v, and no renderer in knowledge_describe.go claims them.\n"+
			"Until one does, a view carrying such a key is described without it. Decide which version owns "+
			"each key, add it to viewV1BodyKeys or viewV2BodyKeys, and RENDER it there.", unclaimed)
	}

	// The reverse: a claimed key that ViewDef no longer has is a stale claim,
	// and a stale claim silences the runtime gap report for a key that may
	// have been renamed rather than removed.
	inWire := map[string]bool{}
	for _, k := range wire {
		inWire[k] = true
	}
	var stale []string
	for k := range claimed {
		if !inWire[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("these keys are claimed by a renderer but no longer exist on generated.ViewDef: %v. "+
			"A stale claim silences the gap report for whatever replaced them.", stale)
	}
}

// TestDescribeViews_AnUnaccountedKeyIsReportedNotDropped exercises mechanism 2
// directly: a key the view carries that its version's renderer never claimed
// must reach the reader as an explicit gap.
//
// It simulates a future contract key by rendering a fully populated view
// against a DELIBERATELY SHORT accounted list — which is exactly the state the
// renderer is in the moment somebody adds a key to ViewDef.yaml.
func TestDescribeViews_AnUnaccountedKeyIsReportedNotDropped(t *testing.T) {
	v := describeViewVault(t, "gap.yaml", `
schema_version: 2
name: gap
type: widget
filter: {property: state, op: "=", value: shipped}
limit: 10
`)

	gaps := unaccountedViewKeys(v.Def, []string{"limit"})
	if len(gaps) == 0 {
		t.Fatal("a declared key that no renderer claims was reported as no gap at all. " +
			"That is precisely how a version-2 filter tree became invisible: unclaimed, unrendered, unmentioned.")
	}
	found := false
	for _, g := range gaps {
		if g == "filter" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the unclaimed `filter` key is not in the gap report %v", gaps)
	}
}

// TestDescribeViews_UnknownSchemaVersionIsNeverCalledUnfiltered — a view whose
// format no renderer understands is the worst case for a confident answer,
// because there is nothing to render and the temptation is to say so as
// "everything".
//
// The loader refuses an unsupported version, so this builds the SavedView by
// hand deliberately: the point is that the renderer holds the line on its own
// rather than relying on a caller having validated first.
func TestDescribeViews_UnknownSchemaVersionIsNeverCalledUnfiltered(t *testing.T) {
	shipped := "shipped"
	state := "state"
	op := generated.VaultFilterNodeOp("=")
	v := &records.SavedView{Def: generated.ViewDef{
		SchemaVersion: 99,
		Name:          "from-the-future",
		Filter:        &generated.VaultFilterNode{Property: &state, Op: &op, Value: &shipped},
	}}

	body := renderViewBody(v)
	if strings.Contains(body, "every record of this type") {
		t.Fatalf("a view in a format this code cannot read was described as returning every record.\nrendered:\n%s", body)
	}
	if !strings.Contains(body, "filter") {
		t.Errorf("the description does not name the key it could not show; a reader cannot tell what is missing.\nrendered:\n%s", body)
	}
}

// TestDescribeViews_UnfilteredIsClaimedOnlyForAnEmptyView is mechanism 3 stated
// as a test: the words "every record of this type, every property" are
// reachable ONLY from a view that declares nothing beyond its own identity.
//
// MUTATION: change renderViewBody's empty-parts branch to return the
// unfiltered line unconditionally (the state the code was in). The
// version-2 subtest fails.
func TestDescribeViews_UnfilteredIsClaimedOnlyForAnEmptyView(t *testing.T) {
	t.Run("a genuinely unconstrained view says so", func(t *testing.T) {
		v := describeViewVault(t, "all.yaml", "schema_version: 2\nname: everything\ntype: widget\n")
		body := renderViewBody(v)
		if body != "    every record of this type, every property\n" {
			t.Fatalf("a view declaring nothing but its identity IS unconstrained and must say so plainly; got %q", body)
		}
	})

	t.Run("an empty filters list narrows nothing and is not a false alarm", func(t *testing.T) {
		// `filters: []` on a version-1 view is an AND-list of no clauses. It
		// constrains nothing, so reporting it as an unshown constraint would
		// be a false alarm — and a renderer that cries wolf gets ignored.
		v := describeViewVault(t, "empty.yaml", "schema_version: 1\nname: empty-filters\ntype: widget\nfilters: []\n")
		if body := renderViewBody(v); body != "    every record of this type, every property\n" {
			t.Fatalf("an empty filter list narrows nothing; got %q", body)
		}
	})

	t.Run("every narrowing key blocks the unfiltered claim", func(t *testing.T) {
		// Each of these is rendered by a real branch today. The assertion is
		// about the LINE, not the branch: whatever the renderer does or fails
		// to do, this sentence must not appear over a view that constrains.
		for name, body := range map[string]string{
			"a v2 filter tree":           "schema_version: 2\nname: f\ntype: widget\nfilter: {property: state, op: \"=\", value: shipped}\n",
			"a v1 filter list":           "schema_version: 1\nname: f\ntype: widget\nfilters: [{property: state, op: eq, values: [{type: enum, enum: shipped}]}]\n",
			"a limit":                    "schema_version: 2\nname: f\ntype: widget\nlimit: 5\n",
			"a disabled flag":            "schema_version: 2\nname: f\ntype: widget\ndisabled: true\n",
			"an untranslated expression": "schema_version: 2\nname: f\ntype: widget\nuntranslated: [\"inFolder(\\\"99-Temp\\\")\"]\n",
		} {
			t.Run(name, func(t *testing.T) {
				got := renderViewBody(describeViewVault(t, "n.yaml", body))
				if strings.Contains(got, "every record of this type") {
					t.Fatalf("%s constrains what this view returns, and the description says it returns every record.\nrendered:\n%s", name, got)
				}
			})
		}
	})
}

// TestDescribeViews_MalformedFilterNodeIsNeverSilent — a filter node that is
// neither a leaf nor a combinator, and an empty combinator, must both produce
// text. Rendering nothing would make a constrained view read as an
// unconstrained one, which is the same wrong answer arriving through a
// different door.
func TestDescribeViews_MalformedFilterNodeIsNeverSilent(t *testing.T) {
	empty := generated.VaultFilterNode{}
	if got := renderViewFilterNode(empty, 0); got == "" {
		t.Error("a filter node this renderer cannot read rendered as the empty string; " +
			"an empty rendering of a filter is indistinguishable from no filter")
	}
	emptyAll := generated.VaultFilterNode{All: &[]generated.VaultFilterNode{}}
	if got := renderViewFilterNode(emptyAll, 0); got == "" {
		t.Error("an empty `all` group rendered as the empty string")
	}
}

// TestDescribeViews_PopulatedKeysReadTheGeneratedTypeNotATranscription guards
// the reflection the two mechanisms stand on. If it stopped seeing fields,
// every coverage check in this file would pass vacuously — the false-green
// shape this repo's own false-green-patterns doc warns about.
func TestDescribeViews_PopulatedKeysReadTheGeneratedTypeNotATranscription(t *testing.T) {
	if n := reflect.TypeOf(generated.ViewDef{}).NumField(); n < 5 {
		t.Fatalf("generated.ViewDef has %d fields; the reflection guard is looking at the wrong type", n)
	}
	limit := 5
	def := generated.ViewDef{SchemaVersion: 2, Name: "n", Limit: &limit}
	got := populatedViewKeys(def)
	want := []string{"limit", "name", "schema_version"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("populatedViewKeys(%+v) = %v, want %v", def, got, want)
	}

	// `disabled: false` is identical to omitted on ViewDef's own contract, so
	// it must not read as a declared key — otherwise every ordinary view
	// carrying the flag would trip a false constraint warning.
	no := false
	if keys := populatedViewKeys(generated.ViewDef{SchemaVersion: 2, Name: "n", Disabled: &no}); len(keys) != 2 {
		t.Errorf("`disabled: false` is identical to omitted (ViewDef.Disabled's contract) and must not count as declared; got %v", keys)
	}
	yes := true
	if keys := populatedViewKeys(generated.ViewDef{SchemaVersion: 2, Name: "n", Disabled: &yes}); len(keys) != 3 {
		t.Errorf("`disabled: true` must count as declared; got %v", keys)
	}
}
