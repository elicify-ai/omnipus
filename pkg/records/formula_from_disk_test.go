// Omnipus — a formula declared in a SCHEMA FILE, loaded and evaluated.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// The formula engine — parser, static typing, caps, cycle walk, evaluator —
// was built, tested and COMPLETELY UNREACHABLE. Nine files of machinery and a
// `Property.Formula` field existed; `propertyDeclKeys` had no `formula` entry,
// so every schema file that declared one was REFUSED as an unknown key:
//
//	schema_bad_property: property "total": unknown key "formula" in a property
//	declaration; it carries only `inverse`, `label`, `many`, `required`, `to`,
//	`type`, `unit`, `values`
//
// Every existing formula test constructs its subject in Go. Not one loads a
// formula from a file, which is exactly why nothing failed while the feature
// could not be reached at all. THIS FILE IS THE ONE THAT GOES THROUGH DISK: it
// writes YAML into a vault, calls the ordinary loader, and evaluates the result
// over a record parsed from note bytes. If the declaration path breaks again,
// the failure lands here rather than in a report that quietly has no column.
// ---------------------------------------------------------------------------

// writeSchemaFile puts one schema file in a fresh vault and returns the vault
// root, so every test here goes through LoadSchemas rather than ParseSchema.
// The distinction matters: LoadSchemas is what a real vault uses, and a check
// that only ever ran against the inner function would not notice the outer one
// dropping the result.
func writeSchemaFile(t *testing.T, name, body string) string {
	t.Helper()
	vault := t.TempDir()
	dir := SchemaDir(vault)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the schema directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return vault
}

// loadOneSchema loads a vault that must contain exactly one accepted schema.
func loadOneSchema(t *testing.T, vault, recordType string) *Schema {
	t.Helper()
	set, report, err := LoadSchemas(vault)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the schema must load; rejected: %s", rejectionText(report))
	}
	sc, ok := set.Get(recordType)
	if !ok {
		t.Fatalf("record type %q did not load", recordType)
	}
	return sc
}

// loadRejection loads a vault whose schema must be REFUSED and returns the
// refusal. It fails loudly on an acceptance, because "no rejection" is the
// result this whole file exists to distinguish from a working feature.
func loadRejection(t *testing.T, vault string) SchemaRejection {
	t.Helper()
	_, report, err := LoadSchemas(vault)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if report.OK() {
		t.Fatalf("the schema must be REFUSED at load; it was accepted")
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one rejection, got %d: %s", len(report.Rejections), rejectionText(report))
	}
	return report.Rejections[0]
}

func rejectionText(r *SchemaLoadReport) string {
	out := make([]string, 0, len(r.Rejections))
	for _, rej := range r.Rejections {
		out = append(out, rej.String())
	}
	return strings.Join(out, " | ")
}

// diskCandidate is a FormulaCandidate over a REAL record: the property values
// come from ResolveProperty against the note's own frontmatter, not from a map
// a test filled in by hand. That is the difference between proving the engine
// computes and proving the whole path — file to schema to record to value —
// actually joins up.
type diskCandidate struct {
	rec    Record
	schema *Schema
}

func (c diskCandidate) FormulaProperty(name string) (PropertyValue, bool) {
	p, ok := c.schema.Property(name)
	if !ok {
		return PropertyValue{}, false
	}
	return ResolveProperty(c.rec, p), true
}

func (c diskCandidate) FormulaFileProperty(string) (PropertyValue, bool) {
	return PropertyValue{}, false
}

// TestFormula_DeclaredOnDiskLoadsAndComputes is the exit proof: a formula
// written in a YAML file, loaded by the ordinary loader, evaluated over a
// record parsed from note bytes.
func TestFormula_DeclaredOnDiskLoadsAndComputes(t *testing.T) {
	vault := writeSchemaFile(t, "invoice.yaml", `schema_version: 1
type: invoice
properties:
  amount:
    type: decimal
    many: false
    required: true
  quantity:
    type: integer
    many: false
    required: true
  total:
    type: decimal
    many: false
    required: false
    formula: amount * quantity
`)
	sc := loadOneSchema(t, vault, "invoice")

	t.Run("the declaration reaches the Property", func(t *testing.T) {
		p, ok := sc.Property("total")
		if !ok {
			t.Fatalf("the derived property did not load")
		}
		if p.Formula != "amount * quantity" {
			t.Fatalf("Property.Formula must carry the SOURCE the file declared (FR-141); got %q", p.Formula)
		}
		if p.Type != TypeDecimal || p.Many {
			t.Fatalf("the declared result shape must survive the load; got type=%s many=%t", p.Type, p.Many)
		}
	})

	t.Run("the schema carries a validated set, so the formula is reachable without re-validating", func(t *testing.T) {
		if sc.Formulas == nil {
			t.Fatalf("Schema.Formulas is nil, so nothing can evaluate a formula this schema declares")
		}
		if got := sc.Formulas.Names(); len(got) != 1 || got[0] != "total" {
			t.Fatalf("Formulas.Names() = %v, want [total]", got)
		}
		d, ok := sc.Formulas.Get("total")
		if !ok {
			t.Fatalf("the validated set does not contain `total`")
		}
		// FR-143a: ONE static type and arity, settled before any record is read.
		if d.Type != FormulaNumber || d.Arity != ArityOne {
			t.Fatalf("static declaration = %s/%s, want number/one", d.Type, d.Arity)
		}
	})

	t.Run("it computes over a real record", func(t *testing.T) {
		// A note, as it would sit in the vault.
		rec := ParseRecord("Invoices/INV-0001.md", []byte(`---
type: invoice
amount: 12.50
quantity: 4
---

# INV-0001
`))
		if rec.ParseError != "" {
			t.Fatalf("the fixture note must parse: %s", rec.ParseError)
		}

		e := NewFormulaEvaluator(sc.Formulas, Comparator{}, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC))
		e.Begin(diskCandidate{rec: rec, schema: sc})
		res, ok := e.Evaluate("total")
		if !ok {
			t.Fatalf("the loaded formula did not evaluate")
		}
		if len(res.Problems) != 0 {
			t.Fatalf("unexpected problems: %+v", res.Problems)
		}
		if res.Absent {
			t.Fatalf("both operands are present, so the result must be too")
		}
		vals := res.Values()
		if len(vals) != 1 {
			t.Fatalf("a scalar formula must produce exactly one value; got %d", len(vals))
		}
		// The oracle is arithmetic, not the implementation: 12.50 × 4 = 50.
		// Compared as an exact decimal so a trailing-zero rendering
		// (`50.0000000000`) neither passes nor fails on presentation.
		want, err := ParseDecimal("50")
		if err != nil {
			t.Fatalf("building the expected value: %v", err)
		}
		if vals[0].Number.Cmp(want) != 0 {
			t.Fatalf("12.50 * 4 = %s, want 50", vals[0].Number)
		}
	})

	t.Run("an absent operand yields absence, not zero (FR-145)", func(t *testing.T) {
		// The guard that makes the previous subtest mean something: if the
		// candidate wiring were broken and every operand resolved to nothing,
		// a formula returning zero would look like a plausible total. It must
		// return ABSENCE instead.
		rec := ParseRecord("Invoices/INV-0002.md", []byte("---\ntype: invoice\nquantity: 4\n---\n"))
		e := NewFormulaEvaluator(sc.Formulas, Comparator{}, time.Now())
		e.Begin(diskCandidate{rec: rec, schema: sc})
		res, ok := e.Evaluate("total")
		if !ok {
			t.Fatalf("the loaded formula did not evaluate")
		}
		if !res.Absent {
			t.Fatalf("a missing `amount` must make the total ABSENT, not %v", res.Values())
		}
	})
}

// TestFormula_MalformedOnDiskIsRefusedAtLoad holds FR-140's load half: the
// refusal happens when the FILE is read, and it names the property.
//
// The table is the point. Each row is a different way a formula declaration can
// be wrong, and every one of them has to be caught by the loader rather than by
// whoever eventually evaluates it — a fault found at evaluation time surfaces
// as an empty column, which nobody reads as a defect.
func TestFormula_MalformedOnDiskIsRefusedAtLoad(t *testing.T) {
	const preamble = `schema_version: 1
type: invoice
properties:
  amount:
    type: decimal
    many: false
    required: true
  quantity:
    type: integer
    many: false
    required: true
`
	cases := []struct {
		name string
		decl string
		// want are substrings the refusal must contain. "total" is in every
		// row on purpose: a refusal that does not name the property leaves the
		// operator diffing a schema file by eye.
		want []string
	}{
		{
			name: "an expression that does not parse",
			decl: "    formula: amount *\n",
			want: []string{"total", "position"},
		},
		{
			name: "an unknown function",
			decl: "    formula: frobnicate(amount)\n",
			want: []string{"total", "frobnicate"},
		},
		{
			name: "an operand the record type does not declare",
			decl: "    formula: amount * nonesuch\n",
			want: []string{"total", "nonesuch"},
		},
		{
			name: "an empty expression",
			decl: "    formula: \"\"\n",
			want: []string{"total", "must carry its expression"},
		},
		{
			name: "a self-reference, FR-148's smallest cycle",
			decl: "    formula: total + 1\n",
			want: []string{"total", "itself"},
		},
		{
			name: "if() branches that disagree, FR-143a",
			decl: "    formula: if(amount > 1, 1, \"x\")\n",
			want: []string{"total"},
		},
		{
			name: "a declared type the expression does not produce",
			// The expression yields text; the property says decimal.
			decl: "    formula: \"\\\"fifty\\\"\"\n",
			want: []string{"total", "decimal", "text"},
		},
		{
			name: "a presentation value, which does not compare at all (R-16)",
			decl: "    formula: icon(\"star\")\n",
			// "does not compare" is asserted, not merely the word
			// "presentation". R-16 is its OWN refusal, and the weaker
			// assertion could not tell it apart from the ordinary
			// type-mismatch message, which also contains "presentation" —
			// verified by mutation: disabling the R-16 branch left the weaker
			// version of this row green.
			want: []string{"total", "presentation", "does not compare"},
		},
		{
			name: "a required derived property, which nothing could ever satisfy",
			decl: "    formula: amount * quantity\n",
			want: []string{"total", "required"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			required := "false"
			if strings.Contains(tc.name, "required derived") {
				required = "true"
			}
			body := preamble + "  total:\n    type: decimal\n    many: false\n    required: " + required + "\n" + tc.decl
			vault := writeSchemaFile(t, "invoice.yaml", body)
			rej := loadRejection(t, vault)
			if rej.Code != RejectBadProperty {
				t.Errorf("code = %q, want %q", rej.Code, RejectBadProperty)
			}
			if rej.Type != "invoice" {
				t.Errorf("the refusal must carry the record type so a report can group by it; got %q", rej.Type)
			}
			for _, want := range tc.want {
				if !strings.Contains(rej.Reason, want) {
					t.Errorf("the refusal must contain %q; got %q", want, rej.Reason)
				}
			}
		})
	}

	t.Run("a formula naming another derived property is refused naming both", func(t *testing.T) {
		vault := writeSchemaFile(t, "invoice.yaml", preamble+
			"  total:\n    type: decimal\n    many: false\n    required: false\n    formula: amount * quantity\n"+
			"  doubled:\n    type: decimal\n    many: false\n    required: false\n    formula: total * 2\n")
		rej := loadRejection(t, vault)
		for _, want := range []string{"doubled", "total", "derived"} {
			if !strings.Contains(rej.Reason, want) {
				t.Errorf("the refusal must contain %q; got %q", want, rej.Reason)
			}
		}
	})

	t.Run("a declared arity the expression does not produce", func(t *testing.T) {
		// ARITY IS THE HALF THAT GETS FORGOTTEN. FR-143a requires ONE static
		// type AND ONE static arity; the table above only ever exercises the
		// type, so a build that checked the type and dropped the arity check
		// would pass every row in it. (It did: this subtest exists because
		// disabling the arity comparison left the whole suite green.)
		//
		// `list()` produces MANY numbers; the property declares a scalar.
		vault := writeSchemaFile(t, "invoice.yaml", `schema_version: 1
type: invoice
properties:
  amount:
    type: decimal
    many: false
    required: true
  quantity:
    type: integer
    many: false
    required: true
  total:
    type: decimal
    many: false
    required: false
    formula: list(amount, quantity)
`)
		rej := loadRejection(t, vault)
		if rej.Code != RejectBadProperty {
			t.Errorf("code = %q, want %q", rej.Code, RejectBadProperty)
		}
		for _, want := range []string{"total", "many", "arity"} {
			if !strings.Contains(rej.Reason, want) {
				t.Errorf("the refusal must contain %q; got %q", want, rej.Reason)
			}
		}
	})

	t.Run("the same formula IS accepted once the declared arity matches", func(t *testing.T) {
		// The control for the row above: it must be the ARITY that was
		// refused, not `list()` itself. Without this, deleting `list` from the
		// grammar would make that row pass for the wrong reason.
		vault := writeSchemaFile(t, "invoice.yaml", `schema_version: 1
type: invoice
properties:
  amount:
    type: decimal
    many: false
    required: true
  quantity:
    type: integer
    many: false
    required: true
  total:
    type: decimal
    many: true
    required: false
    formula: list(amount, quantity)
`)
		sc := loadOneSchema(t, vault, "invoice")
		d, ok := sc.Formulas.Get("total")
		if !ok {
			t.Fatalf("the validated set does not contain `total`")
		}
		if d.Arity != ArityMany {
			t.Fatalf("arity = %s, want many", d.Arity)
		}
	})

	t.Run("FR-146's node cap is applied at load", func(t *testing.T) {
		// maxFormulaNodes is 64. `amount + amount + ...` builds one BinaryOp
		// per additional operand, so 40 operands is 40 refs + 39 operators =
		// 79 nodes: past the cap and nowhere near the 4 KiB source bound, so
		// it is the NODE cap that has to fire.
		expr := strings.TrimSuffix(strings.Repeat("amount + ", 40), " + ")
		vault := writeSchemaFile(t, "invoice.yaml", preamble+
			"  total:\n    type: decimal\n    many: false\n    required: false\n    formula: "+expr+"\n")
		rej := loadRejection(t, vault)
		for _, want := range []string{"total", "nodes"} {
			if !strings.Contains(rej.Reason, want) {
				t.Errorf("the refusal must contain %q; got %q", want, rej.Reason)
			}
		}
	})

	t.Run("a stored property is untouched by any of this", func(t *testing.T) {
		// The control. Every row above is a refusal, so a bug that refused
		// EVERY schema would make this whole test pass. This is the row that
		// would then fail.
		vault := writeSchemaFile(t, "invoice.yaml", preamble)
		sc := loadOneSchema(t, vault, "invoice")
		if sc.Formulas != nil {
			t.Fatalf("a schema with no derived properties must carry no formula set; got %d", sc.Formulas.Len())
		}
		if p, ok := sc.Property("amount"); !ok || p.Formula != "" {
			t.Fatalf("a stored property must carry no formula; got %+v", p)
		}
	})
}
