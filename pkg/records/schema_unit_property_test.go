// Omnipus — tests for `unit_property`, the per-record unit declaration
// (view-kinds-design-2026-09-03 §5).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS HOLD
//
// `unit_property` exists for ONE reason: to make "total once per unit value,
// never across units" (design §3 G2) enforceable. Every failure mode of the
// declaration therefore has the same consequence — a number that LOOKS
// unit-aware, is summed across currencies anyway, and reports nothing. So the
// rejection cases matter more than the acceptance case, and each asserts that
// the refusal NAMES the property and says which rule it broke, never the exact
// sentence (R-F).
//
// Falsified before it was trusted: deleting the validateUnitProperties call
// from ParseSchema reddens the missing-sibling and wrong-kind rows; deleting
// the self-reference guard in finalize reddens the self row.
// ---------------------------------------------------------------------------

// parseUnitPropertySchema runs one whole record type through the REAL loading
// path and returns either the schema or the rejection's reason.
func parseUnitPropertySchema(t *testing.T, properties string) (*Schema, string) {
	t.Helper()
	src := "schema_version: 1\ntype: invoice\nproperties:\n" + properties
	sc, rej := ParseSchema("invoice.yaml", []byte(src))
	if rej != nil {
		if rej.Reason == "" {
			t.Fatalf("a rejection with an empty reason tells the operator nothing: %+v", rej)
		}
		if rej.Code != RejectBadProperty {
			t.Fatalf("a bad property declaration is reported as %s; got %s (%s)", RejectBadProperty, rej.Code, rej.Reason)
		}
		return nil, rej.Reason
	}
	return sc, ""
}

// TestSchema_UnitProperty_ValidDeclarationsLoad — the shapes that must work,
// because they are the shapes the design's own example uses.
func TestSchema_UnitProperty_ValidDeclarationsLoad(t *testing.T) {
	cases := []struct {
		name       string
		properties string
		host       string
		wantUnitOf string
	}{
		{
			name: "a decimal paired with an enum, which is the design's example",
			properties: "" +
				"  amount:   { type: decimal, unit_property: currency }\n" +
				"  currency: { type: enum, values: [SGD, EUR, USD] }\n",
			host:       "amount",
			wantUnitOf: "currency",
		},
		{
			name: "an integer paired with an enum",
			properties: "" +
				"  weight: { type: integer, unit_property: measure }\n" +
				"  measure: { type: enum, values: [g, kg] }\n",
			host:       "weight",
			wantUnitOf: "measure",
		},
		{
			// The honest escape hatch for a vault that has not closed the set
			// yet. Refusing text would make every imported vault unpairable
			// until somebody enumerated every unit in it.
			name: "a text unit, for a set nobody has closed yet",
			properties: "" +
				"  amount:   { type: decimal, unit_property: currency }\n" +
				"  currency: { type: text }\n",
			host:       "amount",
			wantUnitOf: "currency",
		},
		{
			// The unit may be declared BEFORE its host. Nothing about the rule
			// depends on file order, and a loader that only looked backwards
			// would refuse a perfectly ordinary file.
			name: "the unit declared before the number that names it",
			properties: "" +
				"  currency: { type: enum, values: [SGD, EUR] }\n" +
				"  amount:   { type: decimal, unit_property: currency }\n",
			host:       "amount",
			wantUnitOf: "currency",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, reason := parseUnitPropertySchema(t, tc.properties)
			if reason != "" {
				t.Fatalf("this declaration must load; rejected: %s", reason)
			}
			p, found := sc.Property(tc.host)
			if !found {
				t.Fatalf("the schema lost property %q entirely", tc.host)
			}
			if p.UnitProperty != tc.wantUnitOf {
				t.Fatalf("%s.unit_property = %q, want %q — a key the author wrote must never be dropped in silence",
					tc.host, p.UnitProperty, tc.wantUnitOf)
			}
			// The declaration is metadata for the composer and the renderer.
			// It must not have disturbed the number's own declaration.
			if !isNumericType(p.Type) {
				t.Errorf("the host property's own type changed to %s", p.Type)
			}
			if p.Unit != "" {
				t.Errorf("a per-record unit must not invent a fixed `unit`; got %q", p.Unit)
			}
		})
	}
}

// TestSchema_UnitProperty_ViolationsAreRejected — every way the declaration
// can be wrong, refused by name at load.
func TestSchema_UnitProperty_ViolationsAreRejected(t *testing.T) {
	cases := []struct {
		name       string
		properties string
		mustName   []string
	}{
		{
			name: "a sibling the type does not declare",
			properties: "" +
				"  amount: { type: decimal, unit_property: currency }\n" +
				"  client: { type: text }\n",
			// The offending property, the name it pointed at, and what IS
			// declared — a refusal that does not list the alternatives sends
			// the author to read our source.
			mustName: []string{"amount", "currency", "client"},
		},
		{
			name: "a number naming itself",
			properties: "" +
				"  amount: { type: decimal, unit_property: amount }\n",
			mustName: []string{"amount", "unit_property"},
		},
		{
			name: "a companion that is a second number, not a label",
			properties: "" +
				"  amount: { type: decimal, unit_property: rate }\n" +
				"  rate:   { type: decimal }\n",
			mustName: []string{"amount", "rate", "decimal"},
		},
		{
			name: "a companion that is a date",
			properties: "" +
				"  amount: { type: decimal, unit_property: issued }\n" +
				"  issued: { type: date }\n",
			mustName: []string{"amount", "issued", "date"},
		},
		{
			name: "a companion that is a relation",
			properties: "" +
				"  amount: { type: decimal, unit_property: client }\n" +
				"  client: { type: relation, to: company }\n",
			mustName: []string{"amount", "client", "relation"},
		},
		{
			name: "declared on a text host, which has no total to qualify",
			properties: "" +
				"  note:     { type: text, unit_property: currency }\n" +
				"  currency: { type: enum, values: [SGD] }\n",
			mustName: []string{"note", "unit_property", "text"},
		},
		{
			name: "declared on an enum host",
			properties: "" +
				"  state:    { type: enum, values: [draft], unit_property: currency }\n" +
				"  currency: { type: enum, values: [SGD] }\n",
			mustName: []string{"state", "unit_property", "enum"},
		},
		{
			// Two authorities for one fact: `unit` is fixed on every record,
			// `unit_property` varies per record. Accepted together, one of
			// them silently means nothing.
			name: "a fixed unit and a per-record unit at once",
			properties: "" +
				"  amount:   { type: decimal, unit: SGD, unit_property: currency }\n" +
				"  currency: { type: enum, values: [SGD, EUR] }\n",
			mustName: []string{"amount", "unit", "unit_property"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc, reason := parseUnitPropertySchema(t, tc.properties)
			if reason == "" {
				t.Fatalf("this declaration must be refused, it loaded: %+v", sc)
			}
			for _, want := range tc.mustName {
				if !strings.Contains(reason, want) {
					t.Errorf("the refusal must name %q so the author can find it; got %q", want, reason)
				}
			}
		})
	}
}

// TestSchema_UnitProperty_RejectionIsPerFileNotPerVault holds the posture the
// `formula` refusal already takes: one bad record type does not take the vault
// down with it. An operator with a 40-type vault must be told which file to
// open, not that nothing loads.
func TestSchema_UnitProperty_RejectionIsPerFileNotPerVault(t *testing.T) {
	root := writeVaultSchema(t, "", "invoice.yaml", `
schema_version: 1
type: invoice
properties:
  amount: { type: decimal, unit_property: currency }
  client: { type: text }
`)
	root = writeVaultSchema(t, root, "company.yaml", `
schema_version: 1
type: company
properties:
  name: { type: text }
`)

	set, report, err := LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected exactly one rejection, got %v", report.Rejections)
	}
	if got := report.RejectedTypes(); len(got) != 1 || got[0] != "invoice" {
		t.Errorf("the rejected type must be named: got %v", got)
	}
	if _, ok := set.Get("company"); !ok {
		t.Errorf("an unrelated record type must go on loading; the set holds %v", set.Types())
	}
}

// TestSchema_UnitProperty_NewPropertyEnforcesTheSameRules — NewProperty and
// the file loader must not drift on what a valid declaration is. The
// per-property half of the rule lives in finalize precisely so both paths get
// it; a hand-built property is held to exactly the same rules as a parsed one.
func TestSchema_UnitProperty_NewPropertyEnforcesTheSameRules(t *testing.T) {
	cases := []struct {
		name string
		decl Property
		// mustName is empty when the declaration must be ACCEPTED.
		mustName []string
	}{
		{
			name: "a numeric host with a sibling name is accepted here",
			decl: Property{Name: "amount", Type: TypeDecimal, UnitProperty: "currency"},
		},
		{
			name:     "a text host is refused",
			decl:     Property{Name: "note", Type: TypeText, UnitProperty: "currency"},
			mustName: []string{"unit_property", "text"},
		},
		{
			name:     "naming itself is refused",
			decl:     Property{Name: "amount", Type: TypeDecimal, UnitProperty: "amount"},
			mustName: []string{"unit_property"},
		},
		{
			name:     "a fixed unit alongside a per-record one is refused",
			decl:     Property{Name: "amount", Type: TypeDecimal, Unit: "SGD", UnitProperty: "currency"},
			mustName: []string{"unit", "unit_property"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewProperty(tc.decl)
			if len(tc.mustName) == 0 {
				if err != nil {
					t.Fatalf("this declaration must be accepted; got %v", err)
				}
				if p.UnitProperty != tc.decl.UnitProperty {
					t.Fatalf("unit_property = %q, want %q", p.UnitProperty, tc.decl.UnitProperty)
				}
				return
			}
			if err == nil {
				t.Fatalf("this declaration must be refused, NewProperty accepted it: %+v", p)
			}
			for _, want := range tc.mustName {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must name %q; got %q", want, err)
				}
			}
		})
	}
}
