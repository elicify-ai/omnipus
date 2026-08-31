// Omnipus — the writer must refuse exactly what the reader refuses: a summary
// whose property type does not define it (FR-155), gated at import time
// instead of discovered at query time as a whole-request refusal.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
)

// ---------------------------------------------------------------------------
// THE ORACLE FOR THIS FILE IS NOT THIS PACKAGE
//
// Nothing below writes down "sum is for numbers". The expected outcome of every
// case is taken from knowledgefind.SummaryOpDefinedForType — the same predicate
// the query engine gates on, and (proved next door in
// TestSummaryOracleExport_PredictsFindExactly) one that agrees with a real
// Find on every op/type pair. So these tests fail if the importer and the
// engine ever disagree again, which is the defect, rather than failing if
// somebody edits a table in this package.
// ---------------------------------------------------------------------------

func gateResolver(recordType string, props ...InferredProperty) leafResolver {
	return leafResolver{recordType: recordType, schemas: NewSchemaIndex(map[string][]InferredProperty{
		recordType: props,
	})}
}

// TestSummaryGate_TypeDecidesEveryCase walks one op against one property of
// each type and requires translateSummaries to CARRY exactly the pairs the
// engine defines and DROP exactly the ones it does not.
//
// Both directions are asserted in one loop on purpose: a gate that drops
// everything would pass a drop-only test, and a gate that drops nothing (the
// state this fixes) would pass a carry-only one.
func TestSummaryGate_TypeDecidesEveryCase(t *testing.T) {
	types := map[string]records.PropertyType{
		"prose":  records.TypeText,
		"money":  records.TypeDecimal,
		"whole":  records.TypeInteger,
		"day":    records.TypeDate,
		"ticked": records.TypeCheckbox,
	}
	ops := []string{"sum", "avg", "median", "stddev", "min", "max", "range",
		"earliest", "latest", "checked", "unchecked", "empty", "filled", "unique"}

	carried, dropped := 0, 0
	for name, pt := range types {
		r := gateResolver("thing", InferredProperty{Name: name, Type: pt})
		for _, op := range ops {
			want := knowledgefind.SummaryOpDefinedForType(op, pt)
			nodes, losses := translateSummaries(map[string]any{name: op}, r)
			got := len(nodes) == 1 && len(losses) == 0
			if got != want {
				t.Errorf("%s(%s), a %s property: the engine %s it, the importer %s it.\n  losses: %v",
					op, name, pt,
					map[bool]string{true: "DEFINES", false: "REFUSES"}[want],
					map[bool]string{true: "wrote", false: "dropped"}[got],
					losses)
				continue
			}
			if want {
				carried++
				continue
			}
			dropped++
			if len(losses) != 1 {
				t.Fatalf("%s(%s) produced %d losses, want exactly 1: %v", op, name, len(losses), losses)
			}
			// The loss must name the alternatives, and name them in the
			// engine's own words rather than a paraphrase — an operator who
			// reads this line and then queries the property must be told the
			// same thing twice.
			defined := strings.Join(knowledgefind.SummaryOpsDefinedFor(pt), ", ")
			if !strings.Contains(losses[0], defined) {
				t.Errorf("%s(%s) was dropped without naming what %s DOES define (%q): %s",
					op, name, pt, defined, losses[0])
			}
		}
	}
	if carried == 0 || dropped == 0 {
		t.Fatalf("the table exercised only one direction (carried=%d dropped=%d); a gate has to be shown to pass something as well as to stop something", carried, dropped)
	}
	t.Logf("MEASURED: %d op/type pairs carried, %d dropped, every one decided by the engine's own predicate", carried, dropped)
}

// TestSummaryGate_UntypedViewIsNotGated pins the scope line. A folder-scoped
// view declares no record type, so there is no declared type to ask about; the
// engine resolves such a property from whatever the notes themselves declare.
// Gating on a type this importer does not have would drop a summary that works.
//
// WHAT THIS TEST DOES AND DOES NOT PROVE, measured rather than assumed. Deleting
// summaryDefinedForType's `!r.typed()` early return does NOT fail this test —
// the untyped case falls through to the not-found branch, which answers the
// same way, so the two are one branch reached by two doors. What DOES fail it is
// making the unknown-type case refuse. So this holds the BEHAVIOUR (an untyped
// view keeps its summary) and not the line; the early return stays because it
// states the reason, not because a mutation forced it.
func TestSummaryGate_UntypedViewIsNotGated(t *testing.T) {
	r := leafResolver{schemas: NewSchemaIndex(map[string][]InferredProperty{})}
	nodes, losses := translateSummaries(map[string]any{"amount": "Sum"}, r)
	if len(losses) != 0 || len(nodes) != 1 {
		t.Fatalf("an untyped view's summary was gated: nodes=%d losses=%v", len(nodes), losses)
	}
}

// TestSummaryGate_DoesNotFireOnAPropertyCheckPropertyAlreadyRefused holds that
// the two checks stay in their lanes: an undeclared NAME is still reported as
// an undeclared name, not as a type problem. The operator's next action is
// different in each case, so the wrong message costs them a step.
func TestSummaryGate_DoesNotFireOnAPropertyCheckPropertyAlreadyRefused(t *testing.T) {
	r := gateResolver("thing", InferredProperty{Name: "other", Type: records.TypeText})
	_, losses := translateSummaries(map[string]any{"absent": "Sum"}, r)
	if len(losses) != 1 {
		t.Fatalf("losses = %v, want exactly 1", losses)
	}
	if !strings.Contains(losses[0], "not a declared property") {
		t.Errorf("an undeclared property was reported as something else: %s", losses[0])
	}
	if strings.Contains(losses[0], "summaries defined for") {
		t.Errorf("the type gate answered for a property that does not exist: %s", losses[0])
	}
}

// ---------------------------------------------------------------------------
// THE ACCEPTANCE STATEMENT, THROUGH THE REAL LOADERS
// ---------------------------------------------------------------------------

// TestSummaryGate_NoWrittenViewCarriesASummaryTheEngineWouldRefuse imports a
// vault whose base asks for `Sum` over a property this importer can only read
// as text, then reads the result back through records.LoadSchemas /
// records.LoadViews / records.NewViewFindLoader — the product's own path — and
// requires every aggregate that survived to be one the engine defines.
//
// THE SECOND HALF IS THE POINT. The same check is then run against the SAME
// request with the refused aggregate PUT BACK BY HAND, and it must fail. Without
// that, a check that never looks at anything passes this test unchanged.
func TestSummaryGate_NoWrittenViewCarriesASummaryTheEngineWouldRefuse(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Two real notes carrying a `cost` nobody can read as a number — the
	// founder's demonstrated house style for a money field, which is what
	// makes `text` the honest reading rather than a failure of inference.
	costs := []string{
		"PLACEHOLDER — amount unknown",
		"to be confirmed with the vendor",
		"see the signed order form",
		"annual, billed in arrears",
		"waived for the pilot",
		"pending a credit note",
		"quoted verbally, no paperwork",
		"unknown until the audit closes",
	}
	for i, c := range costs {
		write(fmt.Sprintf("notes/bill-%d.md", i+1),
			fmt.Sprintf("---\ntype: bill\nvendor: Vendor %d\ncost: %s\n---\n", i+1, c))
	}
	write("bases/Bills.base", `
filters:
  and:
    - type == "bill"
views:
  - type: table
    name: Spend
    order:
      - vendor
      - cost
    summaries:
      cost: Sum
`)

	if _, err := Run(root, true); err != nil {
		t.Fatalf("Run: %v", err)
	}

	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	billSchema, ok := schemas.Get("bill")
	if !ok {
		t.Fatal("the run declared no `bill` schema, so nothing below is about anything")
	}
	costProp, ok := billSchema.Property("cost")
	if !ok {
		t.Fatal("`cost` was not declared at all; this fixture only tests what it means to test while it is")
	}
	if costProp.Type != records.TypeText {
		t.Fatalf("this fixture only tests what it means to test while `cost` reads as TEXT; it read as %s, for which sum may well be defined", costProp.Type)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !viewRep.OK() {
		t.Fatalf("the importer wrote a view the real loader rejects: %v", viewRep.Rejections)
	}
	if len(views.Names()) == 0 {
		t.Fatal("no view was written, so nothing below is checked")
	}

	loader := records.NewViewFindLoader(views)
	checked := 0
	for _, slug := range views.Names() {
		req, servable := loader.View(slug)
		if !servable {
			continue
		}
		checked++
		for _, bad := range undefinedAggregates(t, req, schemas) {
			t.Errorf("view %q carries %s — knowledge_find refuses the WHOLE request over it, so this view can never return a row", slug, bad)
		}
	}
	if checked == 0 {
		t.Fatal("no servable view was reached, so the assertion above ran over nothing")
	}

	// The column survived. That is the other half of the trade and it must be
	// stated, or "no bad summary" is satisfiable by writing nothing at all.
	req, servable := loader.View(views.Names()[0])
	if !servable {
		t.Fatal("the one view is not servable")
	}
	if req.Select == nil || !contains(*req.Select, "cost") {
		t.Errorf("the `cost` COLUMN was dropped along with the summary; the summary costs the total, never the column. select=%v", req.Select)
	}

	// INSTRUMENT CHECK. Put the refused summary back by hand and require the
	// same check to catch it.
	cost := "cost"
	aggs := []generated.VaultFindAggregate{{Op: generated.VaultFindAggregateOpSum, Property: &cost}}
	req.Aggregate = &aggs
	if bad := undefinedAggregates(t, req, schemas); len(bad) == 0 {
		t.Fatal("the check reported nothing after sum(cost) was ADDED BACK to the request by hand — it cannot see the defect it is asserting the absence of")
	}
}

// undefinedAggregates names every aggregate in a request whose op the property
// type does not define. It asks the ENGINE'S exported predicate, so it cannot
// agree with the importer by construction.
func undefinedAggregates(t *testing.T, req generated.VaultFindRequest, schemas *records.SchemaSet) []string {
	t.Helper()
	if req.Aggregate == nil || req.Type == nil {
		return nil
	}
	schema, ok := schemas.Get(*req.Type)
	if !ok {
		t.Fatalf("the bridge produced a request for record type %q, which the vault does not declare", *req.Type)
	}
	var bad []string
	for _, a := range *req.Aggregate {
		if a.Property == nil || *a.Property == "" {
			continue
		}
		prop, found := schema.Property(*a.Property)
		if !found {
			continue
		}
		if !knowledgefind.SummaryOpDefinedForType(string(a.Op), prop.Type) {
			bad = append(bad, string(a.Op)+"("+*a.Property+") over a "+string(prop.Type)+" property")
		}
	}
	return bad
}
