// infer_base_test.go: tests for type properties from the vault's own `.base` file declarations (enum widening, formula evidence, summary evidence)

package vaultimport

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// --- moved from infer.go tests 2026-09-15 ---

// TestTypePropertiesFromBaseFormulas_OnlyPromotesAScalarText passes NO notes,
// which makes clause 2 admit everything — so whatever refuses a case here is
// the clause the case names, and not clause 2 refusing it for a reason the
// table is not about.
func TestTypePropertiesFromBaseFormulas_OnlyPromotesAScalarText(t *testing.T) {
	cases := []struct {
		name     string
		prop     InferredProperty
		wantType records.PropertyType
		wantFire bool
	}{
		{
			name:     "a value-less scalar text is re-read as a date",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText},
			wantType: records.TypeDate,
			wantFire: true,
		},
		{
			name:     "clause 3 — an enum is never overruled",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeEnum, Kind: ClassifyEnum, EnumValues: []string{"a", "b"}},
			wantType: records.TypeEnum,
		},
		{
			name:     "clause 3 — a checkbox is never overruled",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeCheckbox, Kind: ClassifyBoolean},
			wantType: records.TypeCheckbox,
		},
		{
			name:     "clause 3 — a relation is never overruled",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeRelation, Kind: ClassifyRelation, To: "company"},
			wantType: records.TypeRelation,
		},
		{
			name:     "clause 3 — an already-inferred date is left alone, and not reported a second time",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeDate, Kind: ClassifyDate},
			wantType: records.TypeDate,
		},
		{
			name:     "clause 4 — a list is refused; translate.go's W2 excludes a `many` date anyway",
			prop:     InferredProperty{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText, Many: true},
			wantType: records.TypeText,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, parsed := formulaEvidenceBase(t, "legal-entity", "last_refreshed")
			inferred := map[string][]InferredProperty{"legal-entity": {tc.prop}}

			out := TypePropertiesFromBaseFormulas(inferred, nil, []string{rel}, parsed)

			if got := inferred["legal-entity"][0].Type; got != tc.wantType {
				t.Errorf("property type is %s, want %s", got, tc.wantType)
			}
			if fired := len(out) == 1; fired != tc.wantFire {
				t.Errorf("the rule reported %d decision(s), want fired=%v", len(out), tc.wantFire)
			}
			if reported := inferred["legal-entity"][0].FormulaEvidenced != nil; reported != tc.wantFire {
				t.Errorf("the evidence payload on the property is present=%v, want %v — a decision this run cannot report is a silent one", reported, tc.wantFire)
			}
			if tc.wantFire {
				if k := inferred["legal-entity"][0].Kind; k != ClassifyDateFromFormula {
					t.Errorf("kind is %q, want %q — the report groups its entries by kind", k, ClassifyDateFromFormula)
				}
				if was := out[0].Was; was != records.TypeText {
					t.Errorf("the account says it replaced %s, want text", was)
				}
			}
		})
	}
}

// TestTypePropertiesFromBaseFormulas_AnUntypedViewAttributesNothing is the
// FR-018b clause. An untyped view queries every note in scope, so a property
// name in it is not scoped to one record type, and a declaration read from it
// would land on whichever schemas happened to share the name — here, BOTH.
func TestTypePropertiesFromBaseFormulas_AnUntypedViewAttributesNothing(t *testing.T) {
	src := []byte("filters:\n  and:\n    - file.inFolder(\"01-Areas\")\n" +
		"formulas:\n  age: if(last_refreshed, (today() - date(last_refreshed)).days, \"\")\n" +
		"views:\n  - type: table\n    name: Everything\n")
	pb, err := ParseBaseFile(src)
	if err != nil {
		t.Fatalf("the test's own base file does not parse: %v", err)
	}
	inferred := map[string][]InferredProperty{
		"legal-entity": {{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText}},
		"contact":      {{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText}},
	}

	out := TypePropertiesFromBaseFormulas(inferred, nil, []string{"Untyped.base"},
		map[string]*ParsedBase{"Untyped.base": pb})

	if len(out) != 0 {
		t.Fatalf("an UNTYPED view typed %d property/ies: %+v — the name is not scoped to one record type there, so this declaration was attributed by guesswork", len(out), out)
	}
	for _, rt := range []string{"legal-entity", "contact"} {
		if got := inferred[rt][0].Type; got != records.TypeText {
			t.Errorf("%s.last_refreshed became %s", rt, got)
		}
	}
}

// TestTypePropertiesFromBaseFormulas_AnUnparseableFormulaIsNotEvidence: a
// formula this product cannot parse is already a NAMED loss on every view that
// uses it. Reading a type out of text nothing could parse would be inventing
// evidence rather than reading it.
func TestTypePropertiesFromBaseFormulas_AnUnparseableFormulaIsNotEvidence(t *testing.T) {
	src := []byte("filters:\n  and:\n    - type == \"legal-entity\"\n" +
		"formulas:\n  age: date(last_refreshed ((((\n" +
		"views:\n  - type: table\n    name: All\n")
	pb, err := ParseBaseFile(src)
	if err != nil {
		t.Fatalf("the test's own base file does not parse: %v", err)
	}
	if _, perr := records.ParseFormula(pb.Formulas["age"]); perr == nil {
		t.Fatalf("the fixture formula %q PARSES, so this case no longer tests what it says it does", pb.Formulas["age"])
	}
	inferred := map[string][]InferredProperty{
		"legal-entity": {{Name: "last_refreshed", Type: records.TypeText, Kind: ClassifyText}},
	}
	if out := TypePropertiesFromBaseFormulas(inferred, nil, []string{"B.base"},
		map[string]*ParsedBase{"B.base": pb}); len(out) != 0 {
		t.Fatalf("a type was read out of an unparseable formula: %+v", out)
	}
}

func TestCollectFormulaEvidencedTypes_IsStablyOrdered(t *testing.T) {
	mk := func(rt, p string) InferredProperty {
		return InferredProperty{Name: p, Type: records.TypeDate, Kind: ClassifyDateFromFormula,
			FormulaEvidenced: &FormulaEvidencedType{RecordType: rt, Property: p, Type: records.TypeDate, Was: records.TypeText}}
	}
	inferred := map[string][]InferredProperty{
		"legal-entity": {mk("legal-entity", "last_refreshed")},
		"compliance":   {mk("compliance", "due_date"), mk("compliance", "approved_on")},
		"invoice":      {mk("invoice", "due_date")},
	}
	want := []string{"compliance.approved_on", "compliance.due_date", "invoice.due_date", "legal-entity.last_refreshed"}
	// Repeated, because Go randomises map iteration per range and a single
	// pass can agree with the wanted order by luck.
	for attempt := 0; attempt < 16; attempt++ {
		got := CollectFormulaEvidencedTypes(inferred)
		if len(got) != len(want) {
			t.Fatalf("collected %d accounts, want %d — every decision must reach the report", len(got), len(want))
		}
		for i, w := range want {
			if g := got[i].RecordType + "." + got[i].Property; g != w {
				t.Fatalf("position %d is %s, want %s — the order must not depend on Go's randomised map iteration, or two identical runs print different reports", i, g, w)
			}
		}
	}
}
