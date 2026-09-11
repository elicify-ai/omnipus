// Omnipus — tests for the shared record-write guards (ADR-068 FR-045/FR-046,
// ADR-083 §4.6).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// guardFixture declares one property of every shape the guards discriminate
// on, so a single schema exercises all four rules.
//
// `tags` is `many` specifically so the arity guards have something to refuse
// that is NOT also an enum — otherwise a test asserting an arity refusal
// could be passing because of enum conformance instead.
const guardFixture = `
schema_version: 1
type: widget
identity: { prefix: WD }
properties:
  name:    { type: text }
  status:  { type: enum, values: [prospect, active, dormant] }
  owner:   { type: person, to: person }
  vendor:  { type: relation, to: company }
  tags:    { type: text, many: true }
  count:   { type: integer }
`

func guardSchema(t *testing.T) *Schema {
	t.Helper()
	set := loadSet(t, map[string]string{"widget.yaml": guardFixture})
	sc, ok := set.Get("widget")
	if !ok {
		t.Fatal("fixture must declare the widget type")
	}
	return sc
}

// guardText builds one wire RecordValue carrying text.
func guardText(s string) generated.RecordValue { return generated.RecordValue{Text: &s} }

// guardEnum builds one wire RecordValue carrying an enum member.
func guardEnum(s string) generated.RecordValue { return generated.RecordValue{Enum: &s} }

func guardProp(name string, vals ...generated.RecordValue) generated.RecordPropertyValue {
	return generated.RecordPropertyValue{Property: name, Values: vals}
}

// TestCheckRecordPropertyWrites_RefusesDerivedProperty is FR-046.
//
// IT IS THE REASON THE MOVE WAS WORTH MAKING, and this test could not have
// been written where the guard used to live. records.LoadSchemas refuses a
// schema file declaring `formula:`, so pkg/gateway — which only ever sees
// schemas from LoadSchemas — had no way to construct a Formula-bearing
// property and therefore no way to execute the branch that refuses one. Here,
// beside NewFormulaProperty, it is one call.
func TestCheckRecordPropertyWrites_RefusesDerivedProperty(t *testing.T) {
	sc := guardSchema(t)
	derived, err := NewFormulaProperty(Property{
		Name:       "headcount",
		RecordType: "widget",
		Type:       TypeInteger,
		Formula:    "count(employees)",
	})
	if err != nil {
		t.Fatalf("NewFormulaProperty: %v", err)
	}
	sc.Properties["headcount"] = derived
	sc.PropertyOrder = append(sc.PropertyOrder, "headcount")

	_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
		guardProp("headcount", guardText("12")),
	}, nil)
	if refusal == nil {
		t.Fatal("FR-046: a derived property must be refused, never written")
	}
	if refusal.Code != RefusalDerivedProperty {
		t.Fatalf("code must name the derived rule; got %q", refusal.Code)
	}
	if refusal.Property != "headcount" {
		t.Fatalf("refusal must name the property as data; got %q", refusal.Property)
	}
	if !strings.Contains(refusal.Message, "computed rather than stored") {
		t.Fatalf("message must say WHY it cannot be written; got %q", refusal.Message)
	}
}

// TestCheckRecordPropertyWrites_RefusesRelationAndPerson is FR-045, asserted
// for BOTH declared types because they are two separate schema declarations
// that happen to share one refusal — a guard that lost the person half would
// still pass a relation-only test.
func TestCheckRecordPropertyWrites_RefusesRelationAndPerson(t *testing.T) {
	sc := guardSchema(t)
	for _, name := range []string{"vendor", "owner"} {
		t.Run(name, func(t *testing.T) {
			_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
				guardProp(name, guardText("Acme Ltd")),
			}, nil)
			if refusal == nil {
				t.Fatalf("FR-045: %s must be refused through a record write", name)
			}
			if refusal.Code != RefusalRelationProperty {
				t.Fatalf("code must name the relation rule; got %q", refusal.Code)
			}
			if !strings.Contains(refusal.Message, "RelationWriteRequest") {
				t.Fatalf("message must point at the door that CAN write it; got %q", refusal.Message)
			}
		})
	}
}

// TestCheckRecordPropertyWrites_RefusesScalarOverSupply is the arity rule for
// a single-valued property.
func TestCheckRecordPropertyWrites_RefusesScalarOverSupply(t *testing.T) {
	sc := guardSchema(t)
	_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
		guardProp("name", guardText("first"), guardText("second")),
	}, nil)
	if refusal == nil {
		t.Fatal("a scalar property sent two values must be refused")
	}
	if refusal.Code != RefusalInvalidValue {
		t.Fatalf("code must be invalid_value; got %q", refusal.Code)
	}
	if !strings.Contains(refusal.Message, "holds one value; got 2") {
		t.Fatalf("message must state the arity it got; got %q", refusal.Message)
	}
}

// TestCheckRecordPropertyWrites_RefusesListCollapse is ADR-083 §4.6 — the
// guard the task brief calls "so a `many` property is not collapsed".
//
// The three rows are the whole rule, not one example of it: a shrink is
// refused, an equal-or-larger whole-list write is allowed, and an explicit
// clear is allowed. A guard that refused every write to a list property would
// pass the first row and fail the other two, which is exactly the wall §4.2c
// declined to build.
func TestCheckRecordPropertyWrites_RefusesListCollapse(t *testing.T) {
	sc := guardSchema(t)
	current := ParseRecord("Widgets/w.md", []byte("---\ntype: widget\nid: WD-0001\ntags:\n  - alpha\n  - beta\n  - gamma\n---\n"))

	t.Run("a write that would SHRINK the list is refused", func(t *testing.T) {
		_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
			guardProp("tags", guardText("alpha, beta, gamma")),
		}, &current)
		if refusal == nil {
			t.Fatal("§4.6: sending 1 value for a 3-value list must be refused")
		}
		if refusal.Code != RefusalListProperty {
			t.Fatalf("code must name the list rule; got %q", refusal.Code)
		}
		if !strings.Contains(refusal.Message, "holding 3 values and this write sends 1") {
			t.Fatalf("message must state both counts; got %q", refusal.Message)
		}
	})

	t.Run("a whole-list write of equal length is ALLOWED", func(t *testing.T) {
		writes, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
			guardProp("tags", guardText("a"), guardText("b"), guardText("c")),
		}, &current)
		if refusal != nil {
			t.Fatalf("a deliberate whole-list write must be allowed; got %q", refusal.Message)
		}
		if len(writes) != 1 || writes[0].Kind != PropertyWriteList {
			t.Fatalf("a many property must yield a list write; got %+v", writes)
		}
	})

	t.Run("an empty values array CLEARS and is allowed", func(t *testing.T) {
		writes, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
			guardProp("tags"),
		}, &current)
		if refusal != nil {
			t.Fatalf("D3.2: an empty values array is an explicit clear; got %q", refusal.Message)
		}
		if len(writes) != 1 || writes[0].Kind != PropertyWriteClear {
			t.Fatalf("an empty values array must yield a clear; got %+v", writes)
		}
	})
}

// TestCheckRecordPropertyWrites_RefusesValueOutsideEnum is the closed-enum
// rule, and it asserts the PERMITTED LIST is named.
//
// That half is the part an agent depends on: an agent has no dropdown, so a
// refusal that does not enumerate the legal values leaves it guessing, and
// guessing against a closed set is how a write loop never terminates.
func TestCheckRecordPropertyWrites_RefusesValueOutsideEnum(t *testing.T) {
	sc := guardSchema(t)
	_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
		guardProp("status", guardEnum("archived")),
	}, nil)
	if refusal == nil {
		t.Fatal("a value outside a closed enum must be refused")
	}
	if refusal.Code != RefusalInvalidValue {
		t.Fatalf("code must be invalid_value; got %q", refusal.Code)
	}
	for _, want := range []string{"prospect", "active", "dormant"} {
		if !strings.Contains(refusal.Message, want) {
			t.Fatalf("the refusal must list the permitted value %q; got %q", want, refusal.Message)
		}
	}
}

// TestCheckRecordPropertyWrites_RefusesIdentityKeys is ADR-068 D1/D7.
func TestCheckRecordPropertyWrites_RefusesIdentityKeys(t *testing.T) {
	sc := guardSchema(t)
	for _, key := range []string{RecordTypeKey, RecordIDKey, RecordIDKeyNamespaced} {
		t.Run(key, func(t *testing.T) {
			_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
				guardProp(key, guardText("anything")),
			}, nil)
			if refusal == nil {
				t.Fatalf("%q carries record identity and must be refused", key)
			}
			if refusal.Code != RefusalIdentityProperty {
				t.Fatalf("code must name the identity rule; got %q", refusal.Code)
			}
		})
	}
}

// TestCheckRecordPropertyWrites_DerivedIsReportedBeforeArity pins the ORDER
// of two refusals against a property that trips both.
//
// Order is observable through the audit reason, so it is behaviour rather
// than style: an operator grepping for `derived_property` gets a different
// population depending on which rule reports first. FR-046 runs before arity
// because a derived property has no meaningful "arity to satisfy" refusal —
// telling a caller to send one value instead of two, about a property it may
// not write at all, sends it to fix the wrong thing.
//
// THE CASE THIS TEST DELIBERATELY DOES NOT COVER. The obvious precedence pair
// — FR-046 against FR-045, a property that is both derived AND a relation —
// is UNCONSTRUCTIBLE, so there is no order to pin between them:
// NewFormulaProperty refuses `to:` on a formula property (R-16, "a derived
// link is a presentation value, not a relation the knowledge base can check a
// target for"), while finalize REQUIRES `to:` on a relation (FR-034). The two
// rules are jointly unsatisfiable. Asserting that precedence would mean
// bypassing the constructor to hand-build a property the schema layer cannot
// produce, which pins the behaviour of a value that cannot reach either door.
func TestCheckRecordPropertyWrites_DerivedIsReportedBeforeArity(t *testing.T) {
	sc := guardSchema(t)
	derived, err := NewFormulaProperty(Property{
		Name:       "headcount",
		RecordType: "widget",
		Type:       TypeInteger,
		Formula:    "count(employees)",
	})
	if err != nil {
		t.Fatalf("NewFormulaProperty: %v", err)
	}
	sc.Properties["headcount"] = derived
	sc.PropertyOrder = append(sc.PropertyOrder, "headcount")

	// A SCALAR property sent TWO values also violates arity. The derived
	// refusal must still be the one reported.
	_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
		guardProp("headcount", guardText("1"), guardText("2")),
	}, nil)
	if refusal == nil {
		t.Fatal("a derived property must be refused")
	}
	if refusal.Code != RefusalDerivedProperty {
		t.Fatalf("FR-046 must be reported before arity; got %q (%s)", refusal.Code, refusal.Message)
	}
}

// TestCheckRecordPropertyWrites_RefusesUnknownAndDuplicate covers the two
// envelope-level refusals that do not depend on a declaration.
func TestCheckRecordPropertyWrites_RefusesUnknownAndDuplicate(t *testing.T) {
	sc := guardSchema(t)

	t.Run("an undeclared property names the declared set", func(t *testing.T) {
		_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
			guardProp("colour", guardText("red")),
		}, nil)
		if refusal == nil || refusal.Code != RefusalPropertyUnknown {
			t.Fatalf("an undeclared property must be refused as unknown; got %+v", refusal)
		}
		if !strings.Contains(refusal.Message, "status") {
			t.Fatalf("the refusal must name the declared properties; got %q", refusal.Message)
		}
	})

	t.Run("the same property twice in one write is refused", func(t *testing.T) {
		_, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
			guardProp("name", guardText("a")),
			guardProp("name", guardText("b")),
		}, nil)
		if refusal == nil || refusal.Code != RefusalInvalidValue {
			t.Fatalf("a duplicated property must be refused; got %+v", refusal)
		}
		if !strings.Contains(refusal.Message, "more than once") {
			t.Fatalf("message must say what is wrong; got %q", refusal.Message)
		}
	})
}

// TestCheckRecordPropertyWrites_AllowsAValidWrite is the positive control.
//
// Without it every assertion above could pass on a guard that refused
// everything unconditionally.
func TestCheckRecordPropertyWrites_AllowsAValidWrite(t *testing.T) {
	sc := guardSchema(t)
	writes, refusal := CheckRecordPropertyWrites(sc, []generated.RecordPropertyValue{
		guardProp("name", guardText("Widget One")),
		guardProp("status", guardEnum("active")),
	}, nil)
	if refusal != nil {
		t.Fatalf("a conforming write must be allowed; got %q", refusal.Message)
	}
	if len(writes) != 2 {
		t.Fatalf("want 2 writes, got %d", len(writes))
	}
	for _, w := range writes {
		if w.Kind != PropertyWriteScalar {
			t.Fatalf("%s is scalar and must yield a scalar write; got %v", w.Property, w.Kind)
		}
		if len(w.Values) != 1 {
			t.Fatalf("%s must carry exactly one resolved value; got %v", w.Property, w.Values)
		}
	}
}

// TestPropertyValueText_ReadsTheSchemasTypeNotTheRequests pins the rule that
// the schema is the authority over the value's own advisory `type` field.
func TestPropertyValueText_ReadsTheSchemasTypeNotTheRequests(t *testing.T) {
	txt := "not-a-number"
	// A value carrying ONLY text, offered for an integer property.
	if _, ok := PropertyValueText(generated.RecordValue{Text: &txt}, TypeInteger); ok {
		t.Fatal("a text-only value must not satisfy an integer property")
	}
	num := "42"
	got, ok := PropertyValueText(generated.RecordValue{Integer: &num}, TypeInteger)
	if !ok || got != "42" {
		t.Fatalf("an integer value must resolve for an integer property; got %q ok=%v", got, ok)
	}
	yes := true
	got, ok = PropertyValueText(generated.RecordValue{Checkbox: &yes}, TypeCheckbox)
	if !ok || got != "true" {
		t.Fatalf("a checkbox must render as canonical YAML true; got %q ok=%v", got, ok)
	}
}
