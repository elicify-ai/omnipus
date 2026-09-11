// Omnipus — pinning tests for wire.go's Go-value-to-wire-enum conversions.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// wire.go's own header explains the failure mode these tests exist to catch:
// a ninth PropertyType, or a fourth enum lifecycle group, added without
// extending the four wire enums that carry it. Go lets a cast between an open
// Go string type and a closed generated wire enum compile unconditionally —
// nothing notices until the SPA's Zod validator drops the whole response and
// a dashboard goes blank with no server error naming the cause. These tests
// pin the two conversion families (wirePropertyTypeString's four callers, and
// WireEnumValueGroup) against their NAMED sources of truth (PropertyTypes,
// EnumGroups) so a developer who adds an eighth... ninth property type or a
// fourth lifecycle group fails HERE, in Go, with a message naming the gap.
// ---------------------------------------------------------------------------

// TestWirePropertyType_EveryDeclaredTypeIsValidInAllFourWireEnums is the
// pinning test wire.go's wirePropertyTypeString doc comment names by name.
//
// All four generated enums (PropertyDefType, VaultFindCellType,
// RecordPropertyValueType, RecordValueType) have a generated .Valid() method
// (confirmed against pkg/api/generated/openapi_types.gen.go before writing
// this test), so validity is checked directly against the generated code
// rather than against a hand-kept member list that could itself drift.
func TestWirePropertyType_EveryDeclaredTypeIsValidInAllFourWireEnums(t *testing.T) {
	for _, pt := range PropertyTypes {
		pt := pt
		t.Run(string(pt), func(t *testing.T) {
			defType := WirePropertyDefType(pt)
			if !defType.Valid() {
				t.Errorf("PropertyType %q renders to PropertyDefType %q, which is NOT a valid member of the generated wire enum", pt, defType)
			}
			if string(defType) != string(pt) {
				t.Errorf("PropertyDefType round trip broke: %q became %q", pt, defType)
			}

			cellType := WireVaultFindCellType(pt)
			if !cellType.Valid() {
				t.Errorf("PropertyType %q renders to VaultFindCellType %q, which is NOT a valid member of the generated wire enum", pt, cellType)
			}
			if string(cellType) != string(pt) {
				t.Errorf("VaultFindCellType round trip broke: %q became %q", pt, cellType)
			}

			propValueType := WireRecordPropertyValueType(pt)
			if !propValueType.Valid() {
				t.Errorf("PropertyType %q renders to RecordPropertyValueType %q, which is NOT a valid member of the generated wire enum", pt, propValueType)
			}
			if string(propValueType) != string(pt) {
				t.Errorf("RecordPropertyValueType round trip broke: %q became %q", pt, propValueType)
			}

			valueType := WireRecordValueType(pt)
			if !valueType.Valid() {
				t.Errorf("PropertyType %q renders to RecordValueType %q, which is NOT a valid member of the generated wire enum", pt, valueType)
			}
			if string(valueType) != string(pt) {
				t.Errorf("RecordValueType round trip broke: %q became %q", pt, valueType)
			}
		})
	}
}

// TestWirePropertyTypeString_PanicsOnAnUndeclaredType pins the OTHER half of
// wire.go's design: a PropertyType outside the closed PropertyTypes set can
// only mean a developer added a ninth type and forgot to extend the wire
// contract, so it PANICS rather than silently degrading — see wire.go's
// header for why that is the right failure mode here (a build bug, not
// operator data).
func TestWirePropertyTypeString_PanicsOnAnUndeclaredType(t *testing.T) {
	const undeclared = PropertyType("url")

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("wirePropertyTypeString returned instead of panicking for an undeclared PropertyType")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected the panic value to be a string, got %T: %v", r, r)
		}
		if !strings.Contains(msg, string(undeclared)) {
			t.Fatalf("panic message must name the offending value %q; got %q", undeclared, msg)
		}
	}()

	got := wirePropertyTypeString(undeclared)
	t.Fatalf("wirePropertyTypeString(%q) returned %q instead of panicking", undeclared, got)
}

// TestWireEnumValueGroup_MapsEveryDeclaredGroupAndOmitsAnythingElse pins
// WireEnumValueGroup against EnumGroups, the named source of truth, and
// checks the two OMIT cases wire.go's doc comment declares deliberately
// equivalent: the empty string (ungrouped) and any value outside EnumGroups
// (an operator typo) both come back ok=false, and the caller MUST then leave
// the wire field absent.
func TestWireEnumValueGroup_MapsEveryDeclaredGroupAndOmitsAnythingElse(t *testing.T) {
	t.Run("every declared group maps to a valid, round-tripping wire value", func(t *testing.T) {
		for _, g := range EnumGroups {
			g := g
			t.Run(g, func(t *testing.T) {
				got, ok := WireEnumValueGroup(g)
				if !ok {
					t.Fatalf("WireEnumValueGroup(%q) returned ok=false for a declared EnumGroups member", g)
				}
				if !got.Valid() {
					t.Fatalf("WireEnumValueGroup(%q) = %q, which is NOT a valid member of the generated EnumValueDefGroup wire enum", g, got)
				}
				if string(got) != g {
					t.Fatalf("round trip broke: %q became %q", g, got)
				}
			})
		}
	})

	t.Run("the empty string (ungrouped) is omitted, not a wire value", func(t *testing.T) {
		got, ok := WireEnumValueGroup("")
		if ok {
			t.Fatalf("WireEnumValueGroup(\"\") must return ok=false so the caller omits the field; got ok=true, value %q", got)
		}
		if got != "" {
			t.Fatalf("WireEnumValueGroup(\"\") must return the zero value on ok=false; got %q", got)
		}
	})

	t.Run("an undeclared group is omitted, not a wire value", func(t *testing.T) {
		got, ok := WireEnumValueGroup("blocked")
		if ok {
			t.Fatalf("WireEnumValueGroup(\"blocked\") must return ok=false — \"blocked\" is not in EnumGroups (%v); got ok=true, value %q", EnumGroups, got)
		}
		if got != "" {
			t.Fatalf("WireEnumValueGroup(\"blocked\") must return the zero value on ok=false; got %q", got)
		}
	})
}

// TestEnumValueDefs_DropsAnUnmappableGroupRatherThanEmittingIt builds a
// Property BY HAND — bypassing the parser's own group validation entirely —
// to prove EnumValueDefs() itself is the second, independent line of defence
// wire.go's header describes. Even if some other construction path produces
// an illegal group (a hand-built Property, a future code path that does not
// route through parseEnumValue), the wire stays valid: one bad word must
// never blank a whole view.
func TestEnumValueDefs_DropsAnUnmappableGroupRatherThanEmittingIt(t *testing.T) {
	p := &Property{
		Name: "status",
		Type: TypeEnum,
		Values: []EnumValue{
			{Name: "waiting", Group: "blocked"}, // NOT in EnumGroups — illegal
			{Name: "won", Group: EnumGroupDone}, // legal
		},
	}

	defs := p.EnumValueDefs()
	if len(defs) != 2 {
		t.Fatalf("expected both declared values to survive rendering (only the illegal GROUP is dropped, not the value), got %d entries: %+v", len(defs), defs)
	}

	var waiting, won *generated.EnumValueDef
	for i := range defs {
		switch defs[i].Value {
		case "waiting":
			waiting = &defs[i]
		case "won":
			won = &defs[i]
		}
	}
	if waiting == nil {
		t.Fatalf("expected an entry for %q, got %+v", "waiting", defs)
	}
	if won == nil {
		t.Fatalf("expected an entry for %q, got %+v", "won", defs)
	}

	if waiting.Group != nil {
		t.Errorf("the \"waiting\" entry declared an illegal group (\"blocked\"); its wire Group must be NIL (omitted), got %q", *waiting.Group)
	}
	if won.Group == nil {
		t.Fatalf("the \"won\" entry declared a legal group (%q); its wire Group must be populated, got nil", EnumGroupDone)
	}
	if *won.Group != generated.EnumValueDefGroupDone {
		t.Errorf("the \"won\" entry's wire Group must be %q, got %q", generated.EnumValueDefGroupDone, *won.Group)
	}
}

// TestParseEnumValue_RefusesAnUndeclaredGroup drives the validation added to
// parseEnumValue through the PUBLIC path (a schema file loaded through
// LoadSchemas) rather than calling the unexported parser directly — a far
// stronger proof that an operator's actual mistake is actually caught and
// actually reported.
func TestParseEnumValue_RefusesAnUndeclaredGroup(t *testing.T) {
	t.Run("a schema declaring an undeclared group is rejected, naming the group and the legal set", func(t *testing.T) {
		root := writeVaultSchema(t, "", "ticket.yaml", `
schema_version: 1
type: ticket
properties:
  status:
    type: enum
    values:
      - { name: waiting, group: blocked }
`)
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if report.OK() {
			t.Fatal("a schema declaring group \"blocked\" (not in EnumGroups) must be REJECTED, got a clean load")
		}
		if len(report.Rejections) != 1 {
			t.Fatalf("expected exactly one rejection, got %d: %v", len(report.Rejections), report.Rejections)
		}
		rej := report.Rejections[0]
		if rej.Code != RejectBadProperty {
			t.Errorf("expected code %q, got %q", RejectBadProperty, rej.Code)
		}
		if !strings.Contains(rej.Reason, "blocked") {
			t.Errorf("rejection reason must name the offending group %q; got %q", "blocked", rej.Reason)
		}
		for _, g := range EnumGroups {
			if !strings.Contains(rej.Reason, g) {
				t.Errorf("rejection reason must name the legal set %v so the operator knows what IS allowed; %q is missing from %q", EnumGroups, g, rej.Reason)
			}
		}
		if _, ok := set.Get("ticket"); ok {
			t.Fatal("a rejected schema must not be loaded into the set")
		}
	})

	// POSITIVE CONTROL, in the same test: a legal group loads cleanly and the
	// type appears in the set. Without this, the test above would pass on a
	// loader that rejects every schema unconditionally.
	t.Run("positive control: a declared legal group loads cleanly", func(t *testing.T) {
		root := writeVaultSchema(t, "", "ticket.yaml", `
schema_version: 1
type: ticket
properties:
  status:
    type: enum
    values:
      - { name: done, group: done }
`)
		set, report, err := LoadSchemas(root)
		if err != nil {
			t.Fatalf("LoadSchemas: %v", err)
		}
		if !report.OK() {
			t.Fatalf("a schema declaring the legal group %q must load cleanly, got rejections: %v", EnumGroupDone, report.Rejections)
		}
		schema, ok := set.Get("ticket")
		if !ok {
			t.Fatal("expected record type \"ticket\" to be loaded")
		}
		status, ok := schema.Property("status")
		if !ok {
			t.Fatal("expected property \"status\" to be declared")
		}
		if len(status.Values) != 1 || status.Values[0].Group != EnumGroupDone {
			t.Fatalf("expected one value with group %q, got %+v", EnumGroupDone, status.Values)
		}
	})
}

// TestSchemaRejectionCodes_IsCompleteAndUnique pins SchemaRejectionCodes
// against a hand-written expectation derived by READING schema.go's const
// block (the nine RejectXxx constants), rather than by deriving it from the
// slice itself — a new constant added to the const block without updating
// SchemaRejectionCodes must fail HERE.
func TestSchemaRejectionCodes_IsCompleteAndUnique(t *testing.T) {
	// Hand-written from schema.go's const block, in the order declared there.
	want := []SchemaRejectionCode{
		RejectUnreadable,
		RejectInvalidYAML,
		RejectMissingVersion,
		RejectUnsupportedVersion,
		RejectMissingType,
		RejectDuplicateType,
		RejectNoProperties,
		RejectBadProperty,
		RejectUnknownKey,
	}

	if len(SchemaRejectionCodes) != len(want) {
		t.Fatalf("SchemaRejectionCodes has %d members, expected %d (%v); a RejectXxx constant was added to schema.go without updating SchemaRejectionCodes, or vice versa — got %v",
			len(SchemaRejectionCodes), len(want), want, SchemaRejectionCodes)
	}

	seen := make(map[SchemaRejectionCode]int, len(SchemaRejectionCodes))
	for i, code := range SchemaRejectionCodes {
		if prev, dup := seen[code]; dup {
			t.Errorf("SchemaRejectionCodes contains %q twice, at positions %d and %d", code, prev, i)
		}
		seen[code] = i
	}

	wantSet := make(map[SchemaRejectionCode]bool, len(want))
	for _, code := range want {
		wantSet[code] = true
	}
	for _, code := range SchemaRejectionCodes {
		if !wantSet[code] {
			t.Errorf("SchemaRejectionCodes contains %q, which is not one of the RejectXxx constants read from schema.go's const block (%v)", code, want)
		}
	}
	for _, code := range want {
		if _, ok := seen[code]; !ok {
			t.Errorf("SchemaRejectionCodes is missing %q, a RejectXxx constant declared in schema.go's const block", code)
		}
	}
}
