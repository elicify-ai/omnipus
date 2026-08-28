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
// lexical order, so a test that passes cannot be passing by coincidence.
//
//	declared:  prospect, active, dormant, churned
//	lexical:   active, churned, dormant, prospect
//
// Every position differs. Under ADR-068 D4 as revised, declared order is what
// a REJECTION lists and nothing else; SORTING is lexical over the folded value
// (R-5). The two orders disagreeing on every element is what makes each of the
// assertions below discriminating.
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

// TestEnum_ClosedAndLexical covers FR-010 and FR-011 — spec §7 test 2, US-1
// scenario 1.3, and DS-1's `Active` row.
//
// The name changed with the ruling. It was TestEnum_OrderedAndClosed, and
// "Ordered" was the half ADR-068 D4 WITHDREW: *"the enum ordering is following
// SQLite standard; if we need different ordering we need to prefix the
// content."* "Closed" is unchanged and is the half D4's evidence supports.
func TestEnum_ClosedAndLexical(t *testing.T) {
	set := loadSet(t, map[string]string{"widget.yaml": enumFixture})
	widget, _ := set.Get("widget")
	status, _ := widget.Property("status")

	t.Run("PermittedValues keeps the FILE's declared order, because a rejection reads back the way the operator wrote it", func(t *testing.T) {
		want := []string{"prospect", "active", "dormant", "churned"}
		if got := status.PermittedValues(); !reflect.DeepEqual(got, want) {
			t.Fatalf("declared order must be preserved in the permitted list; want %v, got %v", want, got)
		}
		// Declared order is REPORTING data and nothing more. It must not have
		// become the sort order again: these two orders differ on every
		// element, so a sort that returned the declared order would be caught
		// by the lexical assertion below.
	})

	t.Run("R-5 sorting is LEXICAL over the folded value, NOT the declared position", func(t *testing.T) {
		// Start from the declared order, so an implementation that restored
		// the ordinal — or one that did not sort at all — leaves it declared
		// and fails.
		values := append([]string(nil), status.PermittedValues()...)
		SortValuesBySortKey(values)
		want := []string{"active", "churned", "dormant", "prospect"}
		if !reflect.DeepEqual(values, want) {
			t.Fatalf("R-5: an enum sorts lexically; want %v, got %v — the declared order is %v and must NOT be the answer",
				want, values, status.PermittedValues())
		}
	})

	t.Run("R-5(c) the sort KEY is the folded form, so case does not split one value into three places", func(t *testing.T) {
		// The executed evidence D4 records: "Won" < "lost" is TRUE on raw
		// bytes and FALSE folded. Byte order over raw values puts every
		// capitalised value before every lowercase one, which would render
		// `Won`, `won` and `WON` in three places while grouping collapsed them
		// into one.
		if !("Won" < "lost") {
			t.Fatal("fixture assumption broken: raw byte order must put \"Won\" before \"lost\"")
		}
		if FoldLess("Won", "lost") {
			t.Fatal("R-5(c): the sort key must be the FOLDED form — folded, \"won\" sorts AFTER \"lost\"; sorting raw bytes is the defect")
		}
	})

	t.Run("R-5(d) ties on the folded key break on raw bytes, so the order is TOTAL and deterministic", func(t *testing.T) {
		// Without the tie-break the order is only partial, and SC-014's
		// byte-identical-across-rebuild assertion stops holding the moment two
		// values fold alike.
		if FoldKey("Won") != FoldKey("won") {
			t.Fatal("fixture assumption broken: \"Won\" and \"won\" must fold alike")
		}
		if !FoldLess("Won", "won") {
			t.Fatal("R-5(d): a tie on the folded key must break on RAW bytes, and \"Won\" < \"won\" byte-wise")
		}
		if FoldLess("won", "Won") {
			t.Fatal("R-5(d): the tie-break must be antisymmetric — both directions cannot be true")
		}
		if FoldCompare("Won", "Won") != 0 {
			t.Fatal("R-5(d): a value must compare equal to itself")
		}
	})

	t.Run("D4's cost, asserted rather than described: a domain order is a VALUE PREFIX", func(t *testing.T) {
		// ADR-068 D4 adopts the `1-Pending…7-DoNotContact` prefix hack as the
		// mechanism, and is explicit that this is a real cost it accepts. The
		// assertion is here so the mechanism is verified rather than merely
		// promised: with the prefix, lexical order IS domain order.
		prefixed := []string{"3-proposal", "1-lead", "4-won", "2-qualified"}
		SortValuesBySortKey(prefixed)
		want := []string{"1-lead", "2-qualified", "3-proposal", "4-won"}
		if !reflect.DeepEqual(prefixed, want) {
			t.Fatalf("D4: prefixing is the ONLY way to get a domain order now; want %v, got %v", want, prefixed)
		}

		// And the unprefixed version of the same vocabulary does NOT come out
		// in domain order — which is exactly why the prefix is required. A
		// reader who doubts the cost is real can see it here.
		bare := []string{"proposal", "lead", "won", "qualified"}
		SortValuesBySortKey(bare)
		if reflect.DeepEqual(bare, []string{"lead", "qualified", "proposal", "won"}) {
			t.Fatal("the fixture no longer demonstrates the cost: this vocabulary must NOT sort into domain order without a prefix")
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

	t.Run("FR-011a matching RESOLVES case-insensitively, and renders the DECLARED spelling", func(t *testing.T) {
		// REVERSED by operator ruling R-D. This subtest previously asserted
		// that `Active` is REJECTED against enum(..., active, ...). It is not:
		// resolving `Active` TO `active` collapses two spellings into ONE
		// value, which is not what D4 forbids — D4 forbids auto-creating a
		// SECOND de-facto value, the way Notion's multi-select does on a typo.
		rec := ParseRecord("notes/b.md", []byte("---\ntype: widget\nstatus: Active\n---\n"))
		if r := ValidateRecord(set, rec, ValidateOptions{}); !r.Valid() {
			t.Fatalf("FR-011a: `Active` must RESOLVE to the declared `active`, not be rejected; findings: %v", r.Errors())
		}

		declared, ok := status.ResolveEnum("Active")
		if !ok {
			t.Fatal("FR-011a: ResolveEnum must resolve `Active` to the declared value")
		}
		if declared.Name != "active" {
			t.Fatalf("FR-011a: resolution must yield the DECLARED spelling so grouping and equality agree; got %q, want \"active\"", declared.Name)
		}

		// The file keeps its own spelling — FR-011c — so a report can quote
		// what the operator actually wrote.
		prop, _ := widget.Property("status")
		tv, verr := ParseValue(prop, Node{Kind: KindScalar, Text: "Active"})
		if verr != nil {
			t.Fatalf("ParseValue: %v", verr)
		}
		if tv.Raw != "Active" {
			t.Fatalf("FR-011c: Raw must keep the file's spelling; got %q", tv.Raw)
		}
		if tv.Enum.Name != "active" {
			t.Fatalf("the typed value must carry the DECLARED name; got %q", tv.Enum.Name)
		}
	})

	t.Run("FR-011a resolution is FULL Unicode, not ASCII and not a stdlib fold", func(t *testing.T) {
		// The discriminating case: German ß. `strings.EqualFold` answers false
		// here and so does `strings.ToLower`, so an implementation using
		// either fails this subtest while passing every ASCII one.
		root := writeVaultSchema(t, "", "de.yaml",
			"schema_version: 1\ntype: de\nproperties:\n  s: { type: enum, values: [strasse] }\n")
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if !report.OK() {
			t.Fatalf("fixture schema must load: %v", report.Rejections)
		}
		de, _ := set.Get("de")
		p, _ := de.Property("s")
		if _, ok := p.ResolveEnum("STRASSE"); !ok {
			t.Fatal("FR-011a: `STRASSE` must resolve to `strasse` — plain ASCII folding")
		}
		if _, ok := p.ResolveEnum("Straße"); !ok {
			t.Fatal("FR-011a: `Straße` must resolve to `strasse` under FULL Unicode folding; strings.ToLower and strings.EqualFold both answer NO here, and neither is permitted")
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

	t.Run("a repeated enum value is rejected", func(t *testing.T) {
		root := writeVaultSchema(t, "", "bad.yaml", "schema_version: 1\ntype: bad\nproperties:\n  s: { type: enum, values: [a, b, a] }\n")
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatalf("a value declared twice must be rejected")
		}
	})

	t.Run("two enum values differing ONLY by case are rejected — under FR-011a they are one value", func(t *testing.T) {
		// This rejection did not exist before the case ruling and is created by
		// it. With case-insensitive resolution, declaring both `Won` and `won`
		// gives ResolveEnum two right answers; the map would hand back
		// whichever was indexed last, silently. The author has to choose which
		// spelling their reports render.
		root := writeVaultSchema(t, "", "dupcase.yaml", "schema_version: 1\ntype: dupcase\nproperties:\n  s: { type: enum, values: [Won, won] }\n")
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatal("FR-011a: `Won` and `won` fold to one value, so declaring both must be rejected rather than resolved arbitrarily")
		}
		var msg string
		for _, r := range report.Rejections {
			msg += r.Reason
		}
		if !strings.Contains(msg, "Won") || !strings.Contains(msg, "won") {
			t.Fatalf("the rejection must name BOTH spellings so the author knows which to remove; got %q", msg)
		}
	})

	t.Run("full-Unicode duplicate detection: two enum values folding alike across scripts are rejected", func(t *testing.T) {
		// The ASCII case above passes under strings.ToLower too. This one does
		// not: `straße` and `STRASSE` fold to one value only under FULL
		// folding, so an implementation using a stdlib fold accepts a schema
		// with two values that are really one.
		root := writeVaultSchema(t, "", "dupfold.yaml", "schema_version: 1\ntype: dupfold\nproperties:\n  s: { type: enum, values: [straße, STRASSE] }\n")
		_, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatal("FR-011a: `straße` and `STRASSE` are ONE value under full Unicode folding; declaring both must be rejected")
		}
	})
}
