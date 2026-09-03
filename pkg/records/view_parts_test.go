// Omnipus — tests for a saved view's part stack (view-kinds-design-2026-09-03
// §4, back-compat rule).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS HOLD
//
// The design's back-compat rule is one sentence — "a file with no `parts` is
// read as today: one part, `layout`" — and it is the sentence with the most
// ways to be quietly false. All 69 views in the founder's imported vault carry
// no `parts`. If synthesis loses their grouping, or maps `cards` onto a table,
// or invents a part for a layout that has none, every one of them still LOADS,
// still reports itself fine, and renders something nobody asked for. That is
// FR-109's measured failure — a cards view that imported as a table and scored
// CLEAN — arriving one layer down.
//
// So the legacy cases assert the synthesised part FAITHFULLY, field by field,
// and the `map` case asserts that nothing is synthesised at all.
// ---------------------------------------------------------------------------

// loadOneView writes a single view file into a fresh fixture vault, loads it
// against the fixture schemas, and returns it. A rejection is a fatal here:
// these cases are about views that must LOAD.
func loadOneView(t *testing.T, body string) *SavedView {
	t.Helper()
	root, schemas := viewFixtureSchemas(t, "")
	root = writeVaultView(t, root, "v.yaml", body)
	set, report, err := LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !report.OK() {
		t.Fatalf("expected a clean load, got %v", report.Rejections)
	}
	if set.Len() != 1 {
		t.Fatalf("expected exactly one view, got %v", set.Names())
	}
	return set.Views()[0]
}

// TestViewParts_LegacyViewSynthesisesOnePart is the back-compat rule itself:
// a view with no `parts` yields exactly one part, derived from `layout`.
//
// Falsified before it was trusted: pointing viewPartForLayout's `cards` arm
// at ViewPartPartTable reddens the cards and gallery rows, and returning a
// table for `map` reddens the map row — which is the flattening this whole
// surface is written against.
func TestViewParts_LegacyViewSynthesisesOnePart(t *testing.T) {
	cases := []struct {
		name     string
		layout   string // the `layout:` line, or "" for a view that omits it
		wantPart generated.ViewPartPart
		wantOK   bool
	}{
		{
			name:     "an omitted layout means table, which the contract states",
			layout:   "",
			wantPart: generated.ViewPartPartTable,
			wantOK:   true,
		},
		{
			name:     "table",
			layout:   "layout: table\n",
			wantPart: generated.ViewPartPartTable,
			wantOK:   true,
		},
		{
			name:     "cards is a tile grid, never a table",
			layout:   "layout: cards\n",
			wantPart: generated.ViewPartPartTiles,
			wantOK:   true,
		},
		{
			name:     "gallery is a tile grid too",
			layout:   "layout: gallery\n",
			wantPart: generated.ViewPartPartTiles,
			wantOK:   true,
		},
		{
			name:     "board draws status columns",
			layout:   "layout: board\n",
			wantPart: generated.ViewPartPartColumns,
			wantOK:   true,
		},
		{
			name:     "calendar",
			layout:   "layout: calendar\n",
			wantPart: generated.ViewPartPartCalendar,
			wantOK:   true,
		},
		{
			// The design leaves `map` out of the part vocabulary on purpose
			// (no coordinate data). A layout with no part must produce NO
			// part — a table substituted here would be an undetectable loss.
			name:   "map has no part, and none is invented for it",
			layout: "layout: map\n",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := loadOneView(t, "name: v\ntype: widget\n"+tc.layout)

			parts, ok := v.EffectiveParts()
			if ok != tc.wantOK {
				t.Fatalf("EffectiveParts ok = %v, want %v (parts: %+v)", ok, tc.wantOK, parts)
			}
			if !tc.wantOK {
				if len(parts) != 0 {
					t.Fatalf("a layout with no part must yield no parts, got %+v", parts)
				}
				return
			}
			if len(parts) != 1 {
				t.Fatalf("a view with no `parts` must synthesise exactly one, got %d: %+v", len(parts), parts)
			}
			if parts[0].Part != tc.wantPart {
				t.Fatalf("synthesised part = %q, want %q", string(parts[0].Part), string(tc.wantPart))
			}
		})
	}
}

// TestViewParts_SynthesisCarriesGroupingAndProperties — the half of the
// back-compat rule that is easy to drop. A legacy view's grouping and columns
// live at the TOP level; a synthesis that forgets them produces a part that
// renders an ungrouped, full-width table from a view that asked for neither.
func TestViewParts_SynthesisCarriesGroupingAndProperties(t *testing.T) {
	v := loadOneView(t, `
name: by-state
type: widget
grouping: [{property: state, direction: desc}]
properties: [name, state, batch]
`)

	parts, ok := v.EffectiveParts()
	if !ok || len(parts) != 1 {
		t.Fatalf("expected one synthesised part, got ok=%v parts=%+v", ok, parts)
	}
	got := parts[0]

	if got.Grouping == nil || len(*got.Grouping) != 1 {
		t.Fatalf("the view's grouping must reach the synthesised part; got %+v", got.Grouping)
	}
	g := (*got.Grouping)[0]
	if g.Property != "state" {
		t.Errorf("grouping property = %q, want %q", g.Property, "state")
	}
	if g.Direction == nil || *g.Direction != generated.ViewGroupByDirectionDesc {
		t.Errorf("the grouping DIRECTION must survive too — a bare name list is why every imported groupBy direction was lost; got %+v", g.Direction)
	}

	if got.Properties == nil {
		t.Fatalf("the view's columns must reach the synthesised part; got nil")
	}
	if want := []string{"name", "state", "batch"}; !equalStrings(*got.Properties, want) {
		t.Errorf("columns = %v, want %v (in the declared order)", *got.Properties, want)
	}
}

// TestViewParts_SynthesisDoesNotAliasTheLoadedView — a returned slice that
// aliases Def.Grouping lets a caller's append write into the loaded view, and
// the next caller reads a view that nobody edited.
func TestViewParts_SynthesisDoesNotAliasTheLoadedView(t *testing.T) {
	v := loadOneView(t, `
name: by-state
type: widget
grouping: [{property: state}]
properties: [name]
`)

	parts, ok := v.EffectiveParts()
	if !ok || len(parts) != 1 {
		t.Fatalf("expected one synthesised part, got ok=%v parts=%+v", ok, parts)
	}
	(*parts[0].Properties)[0] = "mutated"
	(*parts[0].Grouping)[0].Property = "mutated"

	if (*v.Def.Properties)[0] != "name" {
		t.Errorf("mutating the returned columns changed the loaded view: %v", *v.Def.Properties)
	}
	if (*v.Def.Grouping)[0].Property != "state" {
		t.Errorf("mutating the returned grouping changed the loaded view: %v", *v.Def.Grouping)
	}
}

// TestViewParts_ExplicitStackRoundTrips — a parts-bearing file loads, and
// EffectiveParts hands back exactly what the file declared, in order.
//
// The design's own §4 example is the fixture, re-bound onto this package's
// fixture record type: figures over a number, then a grouped table with a
// subtotal.
func TestViewParts_ExplicitStackRoundTrips(t *testing.T) {
	v := loadOneView(t, `
name: batches--by-state
type: widget
kind: summary
layout: table
parts:
  - part: figures
    number: batch
    aggregate: sum
  - part: table
    grouping: [{property: state, direction: asc}]
    subtotals: {batch: sum}
    properties: [name, state, batch]
filter:
  property: state
  op: "="
  value: shipped
properties: [name, state, batch]
`)

	if v.Def.Kind == nil || *v.Def.Kind != generated.ViewDefKindSummary {
		t.Fatalf("`kind` must survive the decode as provenance; got %+v", v.Def.Kind)
	}

	parts, ok := v.EffectiveParts()
	if !ok {
		t.Fatalf("a view with an explicit stack always has parts")
	}
	if len(parts) != 2 {
		t.Fatalf("expected the two declared parts, got %d: %+v", len(parts), parts)
	}

	figures := parts[0]
	if figures.Part != generated.ViewPartPartFigures {
		t.Errorf("parts[0] = %q, want %q", string(figures.Part), string(generated.ViewPartPartFigures))
	}
	if figures.Number == nil || *figures.Number != "batch" {
		t.Errorf("parts[0].number = %+v, want \"batch\"", figures.Number)
	}
	if figures.Aggregate == nil || *figures.Aggregate != generated.ViewPartAggregateSum {
		t.Errorf("parts[0].aggregate = %+v, want sum", figures.Aggregate)
	}

	table := parts[1]
	if table.Part != generated.ViewPartPartTable {
		t.Errorf("parts[1] = %q, want %q", string(table.Part), string(generated.ViewPartPartTable))
	}
	if table.Grouping == nil || len(*table.Grouping) != 1 || (*table.Grouping)[0].Property != "state" {
		t.Errorf("parts[1].grouping = %+v, want one key on `state`", table.Grouping)
	}
	if table.Subtotals == nil {
		t.Fatalf("parts[1].subtotals was dropped; a subtotal row is the whole reason `summary` exists")
	}
	if op, found := (*table.Subtotals)["batch"]; !found || op != generated.ViewPartAggregateSum {
		t.Errorf("parts[1].subtotals[batch] = %q (found=%v), want sum", string(op), found)
	}
	if table.Properties == nil || !equalStrings(*table.Properties, []string{"name", "state", "batch"}) {
		t.Errorf("parts[1].properties = %+v, want the three declared columns in order", table.Properties)
	}

	// An explicit stack is authoritative: the legacy `layout` must not have
	// been consulted, and the view-level grouping/properties must not have
	// been copied into a part that declared none of its own.
	if figures.Grouping != nil || figures.Properties != nil {
		t.Errorf("an explicit part is returned AS WRITTEN; the figures part gained %+v / %+v", figures.Grouping, figures.Properties)
	}
}

// TestViewParts_MalformedPartIsRefused — every way a part can be wrong and
// still decode, refused by name.
//
// Each case asserts the SHAPE of the refusal and that it names the offending
// token, never the exact sentence (R-F).
func TestViewParts_MalformedPartIsRefused(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantCode   ViewRejectionCode
		mustName   []string
		mustNotSay string
	}{
		{
			name:     "an unrecognised part is refused, not skipped",
			body:     "name: v\ntype: widget\nparts:\n  - part: figure\n",
			wantCode: RejectViewInvalidPart,
			// The bad token AND the permitted set: a refusal that does not say
			// what IS allowed sends the author to read our source.
			mustName: []string{"figure", "figures", "crosstab", "parts[0]"},
		},
		{
			name:     "an empty part name",
			body:     "name: v\ntype: widget\nparts:\n  - part: \"\"\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"parts[0]"},
		},
		{
			name:     "a blank binding is a key that means nothing",
			body:     "name: v\ntype: widget\nparts:\n  - part: figures\n    number: \"  \"\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"number", "parts[0]"},
		},
		{
			name:     "a blank binding on the second part names the second part",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n  - part: tiles\n    image: \"\"\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"image", "parts[1]"},
		},
		{
			name:     "an unrecognised aggregate",
			body:     "name: v\ntype: widget\nparts:\n  - part: figures\n    number: batch\n    aggregate: median\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"median", "sum", "count"},
		},
		{
			name:     "an unrecognised subtotal reduction",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    subtotals: {batch: stddev}\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"stddev", "batch"},
		},
		{
			name:     "a grouping direction outside asc/desc",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    grouping: [{property: state, direction: sideways}]\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"sideways", "asc", "desc"},
		},
		{
			name:     "a blank column name",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    properties: [name, \"\"]\n",
			wantCode: RejectViewInvalidPart,
			mustName: []string{"parts[0].properties[1]"},
		},
		{
			name:     "a kind outside the eight",
			body:     "name: v\ntype: widget\nkind: dashboard\n",
			wantCode: RejectViewInvalidKind,
			mustName: []string{"dashboard", "summary", "breakdown"},
		},
		{
			name:     "a key no part declares",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    colour: red\n",
			wantCode: RejectViewUnknownKey,
			mustName: []string{"colour"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, rej := ParseView("/vault/.omnipus-vault/views/v.yaml", []byte(tc.body))
			if rej == nil {
				t.Fatalf("expected a rejection, the view parsed")
			}
			if rej.Code != tc.wantCode {
				t.Fatalf("expected %s, got %s (%s)", tc.wantCode, rej.Code, rej.Reason)
			}
			for _, want := range tc.mustName {
				if !strings.Contains(rej.Reason, want) {
					t.Errorf("the refusal must name %q so the author can find it; got %q", want, rej.Reason)
				}
			}
			if len(rej.Paths) != 1 || rej.Paths[0] == "" {
				t.Errorf("every rejection must name the file: %v", rej.Paths)
			}
		})
	}
}

// TestViewParts_PartPropertyNamesAreCheckedAgainstTheSchema — a part is a
// property position like any other. It is the only one that postdates
// ValidateViewAgainstSchemas, so it is the only one that could have been left
// unchecked while every other position was checked.
func TestViewParts_PartPropertyNamesAreCheckedAgainstTheSchema(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		mustName []string
	}{
		{
			name:     "a binding naming a property the type no longer declares",
			body:     "name: v\ntype: widget\nparts:\n  - part: figures\n    number: turnover\n",
			mustName: []string{"turnover", "parts[0].number", "batch"},
		},
		{
			name:     "a part-level grouping key",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    grouping: [{property: phase}]\n",
			mustName: []string{"phase", "parts[0].grouping[0]"},
		},
		{
			name:     "a subtotal over a property that vanished",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    subtotals: {turnover: sum}\n",
			mustName: []string{"turnover", "parts[0].subtotals"},
		},
		{
			name:     "a part-level column",
			body:     "name: v\ntype: widget\nparts:\n  - part: table\n    properties: [name, colour]\n",
			mustName: []string{"colour", "parts[0].properties"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, schemas := viewFixtureSchemas(t, "")
			root = writeVaultView(t, root, "v.yaml", tc.body)
			set, report, err := LoadViews(root, schemas)
			if err != nil {
				t.Fatalf("LoadViews: %v", err)
			}
			if set.Len() != 0 {
				t.Fatalf("the view must not load; got %v", set.Names())
			}
			if len(report.Rejections) != 1 {
				t.Fatalf("expected one rejection, got %v", report.Rejections)
			}
			rej := report.Rejections[0]
			if rej.Code != RejectViewUnknownProperty {
				t.Fatalf("expected %s, got %s (%s)", RejectViewUnknownProperty, rej.Code, rej.Reason)
			}
			for _, want := range tc.mustName {
				if !strings.Contains(rej.Reason, want) {
					t.Errorf("the refusal must name %q; got %q", want, rej.Reason)
				}
			}
		})
	}
}

// equalStrings is a local helper: the fixtures compare short, ordered column
// lists and the ORDER is part of what is asserted.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
