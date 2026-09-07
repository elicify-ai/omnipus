// Omnipus — the wire's value union must carry every declared property type.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// `PropertyDef.type` and `RecordValue.type` are two halves of one decision:
// what a property may be DECLARED as, and what one of its values may be
// CARRIED as. They drifted. `checkbox` (FR-004c, ADR-068 D24.5) was added to
// PropertyDef and to records.PropertyTypes in spec Draft 11 and never added to
// RecordValue, so the wire accepted a declaration for a type it could not
// carry a single value of.
//
// It stayed invisible because it is LATENT: nothing constructs a RecordValue
// today, so no round trip broke and no test failed. That is exactly the class
// of defect a drift test exists for — the first producer would have discovered
// it, at the point where the type it needs does not exist.
//
// The oracle is records.PropertyTypes, which is the closed set FR-004(c)
// defines, and the assertion is agreement in BOTH directions: a type the wire
// cannot carry, and a wire case no declaration can produce, are both drift.
// ---------------------------------------------------------------------------

// TestRecordValue_CarriesEveryDeclaredPropertyType is the drift check.
func TestRecordValue_CarriesEveryDeclaredPropertyType(t *testing.T) {
	t.Run("every declared property type is a valid RecordValue.type", func(t *testing.T) {
		for _, pt := range PropertyTypes {
			if !generated.RecordValueType(pt).Valid() {
				t.Errorf("property type %q can be DECLARED (PropertyDef) but no RecordValue can carry one of its values — the wire has no case for it",
					pt)
			}
		}
	})

	t.Run("every RecordValue.type is a declared property type", func(t *testing.T) {
		// The reverse direction: a wire case nothing can produce is a case a
		// consumer must still write a branch for, and that branch is dead.
		// There is no generated iterator over the enum, so the cases are read
		// off PropertyTypes and each is confirmed valid above; what is checked
		// here is that the enum is no LARGER, via the field set below.
		for _, field := range recordValueJSONFields(t) {
			if field == "type" {
				continue
			}
			if !isKnownPropertyType(PropertyType(field)) {
				t.Errorf("RecordValue carries a %q field, but %q is not one of the declared property types (%v)",
					field, field, PropertyTypes)
			}
		}
	})

	t.Run("every declared property type has its own field on RecordValue", func(t *testing.T) {
		// `type` names which field is populated, so a type with no field is a
		// tag that points at nothing.
		fields := map[string]bool{}
		for _, f := range recordValueJSONFields(t) {
			fields[f] = true
		}
		for _, pt := range PropertyTypes {
			if !fields[string(pt)] {
				t.Errorf("RecordValue has no %q field, so `type: %s` names a field that does not exist", pt, pt)
			}
		}
	})
}

// recordValueJSONFields reads the union's wire field names off the GENERATED
// struct's json tags, never off a hand-kept list — a hand-kept list is a third
// copy of the same decision and would drift from both the others.
func recordValueJSONFields(t *testing.T) []string {
	t.Helper()
	rt := reflect.TypeOf(generated.RecordValue{})
	out := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		out = append(out, strings.Split(tag, ",")[0])
	}
	return out
}
