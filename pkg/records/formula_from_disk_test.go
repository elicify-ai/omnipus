// Omnipus — a formula declared in a SCHEMA FILE is refused when the file loads.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHAT IT USED TO SAY
//
// It used to assert the OPPOSITE: that a schema file declaring
// `total: {type: decimal, formula: amount * quantity}` loaded, validated and
// produced an evaluable FormulaSet on Schema.Formulas. That was true, and it
// was half a feature. NOTHING evaluated the result — a query routes a bare
// property name to the record's STORED values, where a derived property has
// none, so the column rendered BLANK on every row while the answer reported
// itself COMPLETE. A blank column in a complete answer is indistinguishable
// from a note that has no value, which is why it is the worst available
// outcome: the operator concludes their data is wrong.
//
// The surface is specified nowhere — FR-140 says a query reaches a formula only
// as `formula.<name>`, served by a saved VIEW's `formulas:` map (FR-141,
// ADR-068 D24.3), and a schema property has no such address. So the loader is
// now the refusal point, and these tests hold the refusal: it happens when the
// FILE is read, it names the property and the remedy, and it costs exactly one
// schema file rather than the vault.
//
// The load-time VALIDATION that used to sit behind the acceptance (parse,
// FR-146 caps, FR-148 cycles, FR-143a typing) was deleted with the acceptance,
// deliberately, along with the tests that guarded it — it could no longer be
// reached from any caller, and diagnosing the syntax of an expression that is
// refused for being in the wrong FILE sends the author to fix the wrong thing.
// The engine itself is untouched and is still reached where a formula belongs:
// ValidateFormulaSet on the view load path (view.go::validateViewFormulas) and
// on the query path (knowledgefind/namespace.go).
// ---------------------------------------------------------------------------

// writeVault puts the named schema files in a fresh vault and returns its root,
// so every test here goes through LoadSchemas rather than ParseSchema. The
// distinction matters: LoadSchemas is what a real vault uses, and a check that
// only ever ran against the inner function would not notice the outer one
// dropping the result — or, for the per-file claim below, continuing past it.
func writeVault(t *testing.T, files map[string]string) string {
	t.Helper()
	vault := t.TempDir()
	dir := SchemaDir(vault)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the schema directory: %v", err)
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return vault
}

// writeSchemaFile is writeVault for the one-file case.
func writeSchemaFile(t *testing.T, name, body string) string {
	t.Helper()
	return writeVault(t, map[string]string{name: body})
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

// loadRejection loads a vault whose single schema must be REFUSED and returns
// the refusal. It fails loudly on an acceptance, because "no rejection" is the
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

// invoicePreamble is a valid two-property record type. Every fixture below adds
// a third property to it, so the ONLY difference between an accepted vault and
// a refused one is the `formula` key.
const invoicePreamble = `schema_version: 1
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

// TestSchemaFormula_IsRefusedAtLoadNamingTheRemedy is the exit proof for the
// authoring moment: the message an operator reads the instant they save the
// file.
func TestSchemaFormula_IsRefusedAtLoadNamingTheRemedy(t *testing.T) {
	vault := writeSchemaFile(t, "invoice.yaml", invoicePreamble+
		"  total:\n    type: decimal\n    many: false\n    required: false\n    formula: amount * quantity\n")

	set, report, err := LoadSchemas(vault)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if report.OK() {
		t.Fatalf("a schema declaring a formula property was ACCEPTED at load. " +
			"It would then render that column BLANK on every row of an answer calling itself COMPLETE")
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected one rejection, got %d: %s", len(report.Rejections), rejectionText(report))
	}
	rej := report.Rejections[0]

	if rej.Code != RejectBadProperty {
		t.Errorf("code = %q, want %q — the fault is in one property declaration, and a report groups by that",
			rej.Code, RejectBadProperty)
	}
	if rej.Type != "invoice" {
		t.Errorf("the refusal must carry the record type so a report can group by it; got %q", rej.Type)
	}
	if len(rej.Paths) != 1 || filepath.Base(rej.Paths[0]) != "invoice.yaml" {
		t.Errorf("the refusal must name the file the author has to open; Paths = %v", rej.Paths)
	}
	if _, ok := set.Get("invoice"); ok {
		t.Errorf("the refused record type is still in the SchemaSet, so queries would go on using a " +
			"declaration the loader rejected")
	}

	// THE MESSAGE. Each of these is something the author cannot act without.
	// They are asserted on the rendered rejection, which is what a report
	// prints, not on the reason string alone.
	rendered := rej.String()
	for _, want := range []string{
		`"total"`,                 // which property
		"`formula`",               // which key on it
		"invoice.yaml",            // which file
		"BLANK",                   // what would otherwise happen
		"COMPLETE",                // ...and why that is the dangerous part
		"FR-140",                  // the requirement that settles the address
		"ADR-068 D24.3",           // and the decision
		"`formulas:` map",         // where it belongs instead
		"`formula.`",              // how a query then reaches it
		"stored property",         // the other legitimate fix
		"every other record type", // the blast radius, stated to the author
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the refusal an author reads does not mention %q.\nRendered:\n%s", want, rendered)
		}
	}

	// REQUIREMENT: `formula` stays a KNOWN key. Dropping it from
	// propertyDeclKeys would also refuse this schema — for the wrong reason,
	// telling an author whose SPELLING is right and whose PLACEMENT is wrong
	// that we have never heard of the key.
	if strings.Contains(rendered, "unknown key") {
		t.Errorf("`formula` fell through to the generic unknown-key refusal. It is a key the contract "+
			"publishes; calling it unknown tells the author to check their spelling when the fix is to "+
			"move the expression.\nRendered:\n%s", rendered)
	}
}

// TestSchemaFormula_RefusalCostsOneFileNotTheVault is the blast-radius
// measurement: one bad schema file, one rejection, every other record type in
// the same vault still loaded.
func TestSchemaFormula_RefusalCostsOneFileNotTheVault(t *testing.T) {
	vault := writeVault(t, map[string]string{
		"invoice.yaml": invoicePreamble +
			"  total:\n    type: decimal\n    many: false\n    required: false\n    formula: amount * quantity\n",
		"customer.yaml": `schema_version: 1
type: customer
properties:
  name:
    type: text
    many: false
    required: true
  seats:
    type: integer
    many: false
    required: false
`,
	})

	set, report, err := LoadSchemas(vault)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if len(report.Rejections) != 1 {
		t.Fatalf("expected exactly one rejection — the offending file only; got %d: %s",
			len(report.Rejections), rejectionText(report))
	}
	if base := filepath.Base(report.Rejections[0].Paths[0]); base != "invoice.yaml" {
		t.Errorf("the wrong file was rejected: %s", base)
	}
	if got := set.Types(); len(got) != 1 || got[0] != "customer" {
		t.Fatalf("the untouched record type must still load; SchemaSet holds %v", got)
	}
	// Loaded, and loaded INTACT — a set that held the type but had dropped its
	// properties would satisfy the check above and answer nothing.
	sc, _ := set.Get("customer")
	if p, ok := sc.Property("seats"); !ok || p.Type != TypeInteger {
		t.Errorf("the unaffected record type lost its declarations: %+v", sc.PropertyOrder)
	}
	if report.OK() {
		t.Errorf("report.OK() must be false while any file is rejected")
	}
}

// TestSchemaFormula_EveryWayOfWritingTheKeyIsRefused.
//
// The refusal is on the KEY's presence, checked before the declaration is
// decoded, so it cannot be walked around by writing the expression somewhere
// yaml.v3 would still deliver it. Each row here is a spelling that a check on
// the decoded VALUE would have missed.
func TestSchemaFormula_EveryWayOfWritingTheKeyIsRefused(t *testing.T) {
	cases := []struct {
		name string
		decl string
	}{
		{
			name: "written inline",
			decl: "  total:\n    type: decimal\n    formula: amount * quantity\n",
		},
		{
			name: "written with no expression at all",
			// `formula: ""` is an author declaring a derived property and
			// giving it nothing. It must get the PLACEMENT message, not
			// "carry its expression" — the latter says an expression would
			// have worked here, and none would.
			decl: "  total:\n    type: decimal\n    formula: \"\"\n",
		},
		{
			name: "contributed by a `<<` merge",
			decl: "  total:\n    <<: {formula: amount * quantity}\n    type: decimal\n",
		},
		{
			name: "contributed by a `<<` merge from a SEQUENCE of sources",
			// A different branch of declaredKeys from the row above: `<<`
			// accepts a list of mappings, and a walk that only handled the
			// single-mapping form would let this one through.
			decl: "  total:\n    <<: [{formula: amount * quantity}]\n    type: decimal\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rej := loadRejection(t, writeSchemaFile(t, "invoice.yaml", invoicePreamble+tc.decl))
			if rej.Code != RejectBadProperty {
				t.Errorf("code = %q, want %q", rej.Code, RejectBadProperty)
			}
			for _, want := range []string{`"total"`, "`formula`", "`formulas:` map"} {
				if !strings.Contains(rej.Reason, want) {
					t.Errorf("the refusal must contain %q; got %q", want, rej.Reason)
				}
			}
			if strings.Contains(rej.Reason, "carry its expression") {
				t.Errorf("the refusal says an expression was missing, which tells the author that "+
					"supplying one would work here. It would not.\nGot: %q", rej.Reason)
			}
		})
	}
}

// TestSchemaFormula_AStoredPropertyIsUntouched is the control every refusal
// test above needs: a bug that refused EVERY schema file would make all of them
// pass. This is the one that would then fail.
func TestSchemaFormula_AStoredPropertyIsUntouched(t *testing.T) {
	sc := loadOneSchema(t, writeSchemaFile(t, "invoice.yaml", invoicePreamble), "invoice")

	if got := len(sc.PropertyOrder); got != 2 {
		t.Fatalf("the ordinary schema lost properties: %v", sc.PropertyOrder)
	}
	// No schema file can produce a DERIVED Property any more, so nothing
	// downstream has to ask whether a schema property might be computed.
	for _, name := range sc.PropertyOrder {
		if p := sc.Properties[name]; p.Formula != "" {
			t.Errorf("the loader produced a derived property %q (formula %q); a schema file cannot "+
				"declare one at all", name, p.Formula)
		}
	}
	if p, ok := sc.Property("amount"); !ok || p.Type != TypeDecimal || !p.Required {
		t.Errorf("a stored declaration did not survive the load: %+v", p)
	}
}
