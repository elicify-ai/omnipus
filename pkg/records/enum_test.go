// Omnipus — tests for FR-010 and FR-011 (ADR-068 D4).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"reflect"
	"strings"
	"testing"
)

// enumFixture uses a value set whose DECLARED order is deliberately not its
// alphabetical order, so a test that passes cannot be passing by coincidence.
//
//	declared:      prospect, active, dormant, churned
//	alphabetical:  active, churned, dormant, prospect
//
// Every position differs. FR-010 says sorting follows the declared position.
const enumFixture = `
schema_version: 1
type: widget
properties:
  status: { type: enum, values: [prospect, active, dormant, churned] }
  stage:
    type: enum
    values:
      - { name: idea,      group: open }
      - { name: building,  group: open }
      - { name: shipped,   group: done }
      - { name: abandoned, group: cancelled }
`

// TestEnum_OrderedAndClosed covers FR-010 and FR-011 — spec §7 test 2, US-1
// scenario 1.3, and DS-1's `Active` row.
func TestEnum_OrderedAndClosed(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": enumFixture})
	widget, _ := set.Get("widget")
	status, _ := widget.Property("status")

	t.Run("FR-010 values keep their declared order, not the alphabet", func(t *testing.T) {
		want := []string{"prospect", "active", "dormant", "churned"}
		if got := status.PermittedValues(); !reflect.DeepEqual(got, want) {
			t.Fatalf("FR-010: declared order must be preserved; want %v, got %v", want, got)
		}
		for i, v := range want {
			pos, ok := status.EnumPosition(v)
			if !ok {
				t.Fatalf("%q must be in the declared set", v)
			}
			if pos != i {
				t.Fatalf("FR-010: %q is declared at position %d, EnumPosition said %d", v, i, pos)
			}
		}
	})

	t.Run("FR-010 sorting follows declared position, not lexical order", func(t *testing.T) {
		// Start from the alphabetical order so a no-op sort would leave it
		// alphabetical and fail.
		values := []string{"active", "churned", "dormant", "prospect"}
		SortByEnumOrder(status, values)
		want := []string{"prospect", "active", "dormant", "churned"}
		if !reflect.DeepEqual(values, want) {
			t.Fatalf("FR-010: sorting must follow declared position; want %v, got %v", want, values)
		}
	})

	t.Run("FR-010 ordering uses position, through a real filter: `status < churned` matches prospect", func(t *testing.T) {
		// Alphabetically churned < prospect. By declared position the reverse.
		// ADR-068 D4: ordering is data, not spelling. Driven through
		// Filter.Match so it exercises the path a real query takes.
		prospect := ParseRecord("p.md", []byte("---\ntype: widget\nstatus: prospect\n---\n"))
		dormant := ParseRecord("d.md", []byte("---\ntype: widget\nstatus: dormant\n---\n"))

		f := Filter{Property: "status", Op: OpLess, Literal: "churned"}
		res, err := f.Match(widget, prospect)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !res.Matched {
			t.Fatalf("FR-010 / §8 R-5: `prospect` is declared before `churned`, so `status < churned` must match; a lexical comparator would say false")
		}
		res, err = f.Match(widget, dormant)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !res.Matched {
			t.Fatalf("`dormant` is declared before `churned` too")
		}

		// And the reverse direction, which a lexical comparator would get right
		// by accident above but wrong here.
		gt := Filter{Property: "status", Op: OpGreater, Literal: "prospect"}
		res, err = gt.Match(widget, dormant)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if !res.Matched {
			t.Fatalf("FR-010: `dormant` is declared after `prospect`, so `status > prospect` must match — lexically it is the other way round")
		}
	})

	t.Run("FR-011 a value outside the set is rejected, listing the permitted values", func(t *testing.T) {
		rec := ParseRecord("notes/a.md", []byte("---\ntype: widget\nstatus: won\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("FR-011 / US-1.3: an out-of-set enum value must be rejected")
		}
		f := rep.Errors()[0]
		if f.Code != FindingEnumNotPermitted {
			t.Fatalf("expected %q, got %q", FindingEnumNotPermitted, f.Code)
		}
		if f.Got != "won" {
			t.Fatalf("US-1.3 requires the OFFENDING VALUE to be reported; Got = %q", f.Got)
		}
		want := []string{"prospect", "active", "dormant", "churned"}
		if !reflect.DeepEqual(f.Permitted, want) {
			t.Fatalf("FR-011 requires the permitted values listed, in declared order; want %v, got %v", want, f.Permitted)
		}
		rendered := f.String()
		for _, v := range want {
			if !strings.Contains(rendered, v) {
				t.Fatalf("the rendered finding must list %q; got %q", v, rendered)
			}
		}
	})

	t.Run("FR-011 matching is exact, including case", func(t *testing.T) {
		// DS-1: `Active` against enum(prospect, active) is REJECTED. ADR-068
		// D4: auto-accepting the near-miss is how one column comes to hold
		// `Won`, `won` and `Closed Won`.
		rec := ParseRecord("notes/b.md", []byte("---\ntype: widget\nstatus: Active\n---\n"))
		rep := ValidateRecord(set, rec, ValidateOptions{})
		if rep.Valid() {
			t.Fatalf("DS-1 / D4: `Active` must be rejected against enum(..., active, ...) — matching is case-exact")
		}
		if got := rep.Errors()[0].Code; got != FindingEnumNotPermitted {
			t.Fatalf("expected %q, got %q", FindingEnumNotPermitted, got)
		}
	})

	t.Run("FR-011 the whole closed set is accepted", func(t *testing.T) {
		for _, v := range status.PermittedValues() {
			rec := ParseRecord("notes/c.md", []byte("---\ntype: widget\nstatus: "+v+"\n---\n"))
			if !ValidateRecord(set, rec, ValidateOptions{}).Valid() {
				t.Fatalf("declared value %q must be accepted", v)
			}
		}
	})

	t.Run("D4 an enum value may carry a lifecycle group", func(t *testing.T) {
		stage, _ := widget.Property("stage")
		wantGroups := map[string]string{"idea": "open", "building": "open", "shipped": "done", "abandoned": "cancelled"}
		for _, ev := range stage.Values {
			if wantGroups[ev.Name] != ev.Group {
				t.Fatalf("value %q: want group %q, got %q", ev.Name, wantGroups[ev.Name], ev.Group)
			}
		}
		if stage.Values[0].Name != "idea" || stage.Values[3].Name != "abandoned" {
			t.Fatalf("the long form must preserve declared order too: %v", stage.PermittedValues())
		}
	})

	t.Run("an enum declaring no values is rejected at load", func(t *testing.T) {
		root := writeVaultSchema(t, "", "bad.yaml", "schema_version: 1\ntype: bad\nproperties:\n  s: { type: enum }\n")
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatalf("an enum with no declared values can never be satisfied and must be rejected")
		}
	})

	t.Run("FR-010 a repeated enum value is rejected — a repeat has no position", func(t *testing.T) {
		root := writeVaultSchema(t, "", "bad.yaml", "schema_version: 1\ntype: bad\nproperties:\n  s: { type: enum, values: [a, b, a] }\n")
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatalf("FR-010 makes declared position the sort order, so a duplicate value must be rejected")
		}
	})
}
