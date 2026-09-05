// Omnipus — gate G2 at the FIND layer: a number with a declared companion
// unit must not be summed across units (view-kinds-design-2026-09-03 §3 G2).
//
// STATUS: DEFERRED. The test below is written, runs, and FAILS against the
// current engine — it is skipped, not deleted, so the defect stays visible and
// the fix has an oracle waiting for it. See the skip's own reason for why it is
// not fixed here.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// unitInvoiceSchemaYAML declares a decimal paired with a companion unit — the
// §5 shape the whole G2 rule is written about.
const unitInvoiceSchemaYAML = `
schema_version: 1
type: invoice
label: Invoice
identity:
  prefix: INV
properties:
  client:   { type: text }
  amount:   { type: decimal, unit_property: currency }
  currency: { type: enum, values: [SGD, EUR] }
`

func unitInvoiceSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "invoice.yaml"), []byte(unitInvoiceSchemaYAML), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schema was rejected: %v", report.Rejections)
	}
	return set
}

// TestFind_SumDoesNotCrossUnits_G2 is the FIND-layer half of G2.
//
// The design puts the gate in the composer and the renderer, and both now
// enforce it. `aggregate` does not: it reduces a column with no idea that
// PropertyDef.unit_property exists, so `sum(amount)` over 100.50 SGD and
// 200.00 EUR answers "300.50" — a figure in no currency, stated with the same
// confidence as a correct one, in the surface an agent reads directly.
//
// The expected shape is derived from G2, not from the engine: "a
// number-with-unit totals ONCE PER UNIT VALUE, never across units. No combined
// figure is ever emitted."
func TestFind_SumDoesNotCrossUnits_G2(t *testing.T) {
	t.Skip(`NEEDS-DEFERRAL — confirmed defect, but the fix is a design decision, not a mechanical change.

RUN THIS TEST WITH THE SKIP REMOVED AND IT FAILS: sum(amount) over 100.50 SGD
and 200.00 EUR returns the single total "300.50".

Why it is not fixed in this change:

 1. THE WIRE HAS NOWHERE TO PUT THE ANSWER. VaultFindTotal carries op, label,
    value and scope. Per-unit totals need a unit field on it and a rendering
    for the fifteen ops in render.go's writeTotals — a contract change to the
    tool's own answer format, which every agent prompt and every recorded
    session transcript reads.

 2. IT MOVES A DOCUMENTED MEMORY BOUND. FR-151's B3 column buffer is ONE budget
    for median and unique. Partitioning by unit value means either N budgets
    (the bound multiplied by unit cardinality, which is a regression on a
    stated guarantee) or one budget split across partitions (a new rule with
    its own failure mode). Neither is implied by G2; both need a ruling.

 3. THE UNTYPED CASE HAS NO ANSWER YET. A query with no 'type' resolves names
    across every in-scope schema, so there is no single declaration to read a
    companion unit from. The gateway's renderer settled this by REFUSING such a
    total; whether knowledge_find should refuse, or answer per unit using every
    declaration in scope, is the same open question one layer down.

 4. THE SCOPE CLAUSE IS PER TOTAL (FR-125). Splitting one total into N means N
    scope clauses over N different row subsets, and the "over X of Y evaluated
    rows" wording has to say which subset it counted — otherwise the split
    fixes G2 and breaks FR-125 in the same commit.

Interim protection, already landed: the gateway's view-result endpoint never
routes a view total through this path. It reduces per unit itself
(aggregateViewRows) and REFUSES where it cannot resolve the unit, so no
cross-unit figure reaches the SPA. This gap is reachable through
knowledge_find directly, and through a saved view's legacy 'aggregates' key —
which the endpoint now gates for exactly this reason rather than surfacing raw.`)

	path := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	set := unitInvoiceSet(t)
	text := &stubText{hits: map[string]TextHit{}}
	write := func(path, src string) {
		b := []byte(src)
		rec := records.ParseRecord(path, b)
		sc, _ := set.Get(rec.TypeName())
		rows := propindex.BuildNoteRows(rec, sc, b, propindex.SourceHash(b))
		if uerr := store.UpsertNote(context.Background(), rows); uerr != nil {
			t.Fatalf("UpsertNote(%s): %v", path, uerr)
		}
		text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
	}

	write("a.md", "---\ntype: invoice\nid: INV-1\nclient: Acme\namount: 100.50\ncurrency: SGD\n---\n# INV-1\n")
	write("b.md", "---\ntype: invoice\nid: INV-2\nclient: Acme\namount: 200.00\ncurrency: EUR\n---\n# INV-2\n")

	deps := Deps{Schemas: set, Store: store, Text: text, Epoch: 8814}
	rt := "invoice"
	prop := "amount"
	aggs := []generated.VaultFindAggregate{{Op: generated.VaultFindAggregateOpSum, Property: &prop}}

	resp, err := Find(context.Background(), deps, generated.VaultFindRequest{Type: &rt, Aggregate: &aggs})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if resp.Refused {
		t.Fatalf("the fixture query must be answerable: %+v", resp.Problems)
	}

	for _, total := range resp.Totals {
		if total.Value == "300.50" {
			t.Fatalf("sum(amount) answered %q — 100.50 SGD + 200.00 EUR is a figure in no currency; "+
				"G2 admits no combined total", total.Value)
		}
	}

	// The positive half: two units in the data means two totals, or a refusal
	// that says why there are none. A single unit-less number is the one
	// answer G2 forbids.
	if len(resp.Totals) == 1 && resp.Totals[0].Refused == nil {
		t.Fatalf("one unqualified total over two units: %+v", resp.Totals[0])
	}
}
