// Omnipus — the FIND layer's G2/G3 behaviour, ruled as D7 (2026-09-05):
// a number with a companion unit totals ONCE PER UNIT VALUE, never across
// units; rows with no confirmed unit are shown, excluded and counted.
//
// Every expectation below is derived from the design's own words
// (view-kinds-design-2026-09-03 §3 G2/G3/G4 and its §9 D7), never read off the
// engine: the numbers are added by hand in the comments beside them.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// Harness
// ---------------------------------------------------------------------------

// unitFixture is a live index over a corpus the test declares, against ONE
// schema directory the test declares. It loads through records.LoadSchemas and
// indexes through propindex.BuildNoteRows — the same paths production uses, so
// a schema this package could not actually load cannot pass a test here.
type unitFixture struct {
	t     *testing.T
	store propindex.Store
	set   *records.SchemaSet
	text  *stubText
}

func newUnitFixture(t *testing.T, schemas map[string]string) *unitFixture {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for name, body := range schemas {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schema was rejected: %v", report.Rejections)
	}

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
	return &unitFixture{t: t, store: store, set: set, text: &stubText{hits: map[string]TextHit{}}}
}

func (f *unitFixture) write(path, src string) {
	f.t.Helper()
	b := []byte(src)
	rec := records.ParseRecord(path, b)
	sc, _ := f.set.Get(rec.TypeName())
	rows := propindex.BuildNoteRows(rec, sc, b, propindex.SourceHash(b))
	if err := f.store.UpsertNote(context.Background(), rows); err != nil {
		f.t.Fatalf("UpsertNote(%s): %v", path, err)
	}
	f.text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
}

func (f *unitFixture) find(req generated.VaultFindRequest) generated.VaultFindResponse {
	f.t.Helper()
	deps := Deps{Schemas: f.set, Store: f.store, Text: f.text, Epoch: 8814}
	resp, err := Find(context.Background(), deps, req)
	if err != nil {
		// A refusal is an ANSWER here, not a transport failure: Find returns
		// the refusal response alongside it and that response is what the model
		// reads. The tests assert on the response.
		f.t.Logf("Find refused: %v", err)
	}
	return resp
}

func agg(op generated.VaultFindAggregateOp, prop string) *[]generated.VaultFindAggregate {
	a := []generated.VaultFindAggregate{{Op: op}}
	if prop != "" {
		p := prop
		a[0].Property = &p
	}
	return &a
}

// unitOfTotal reads a total's unit the way a consumer must: by the FIELD'S
// PRESENCE, never by a sentinel (an empty string is a legitimate unit value).
func unitOfTotal(t generated.VaultFindTotal) (string, bool) {
	if t.Unit == nil {
		return "", false
	}
	return *t.Unit, true
}

func isRefused(t generated.VaultFindTotal) bool { return t.Refused != nil && *t.Refused }

// ---------------------------------------------------------------------------
// Schemas
// ---------------------------------------------------------------------------

const invoiceUnitSchema = `
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

// shipmentNoUnitSchema is the BACK-COMPAT control: a number no record type
// pairs with a unit. Its total must be unchanged in shape and value.
const shipmentNoUnitSchema = `
schema_version: 1
type: shipment
label: Shipment
identity:
  prefix: SHP
properties:
  weight: { type: decimal }
`

// ---------------------------------------------------------------------------
// D7.1 + D7.4 — two units, one total each, exclusions counted
// ---------------------------------------------------------------------------

// TestFind_PerUnitTotals_TwoUnitsAndExcludedCounted is G2 and G3 together.
//
// The corpus, and the arithmetic done BY HAND:
//
//	a.md  100.50 SGD  ┐ SGD total = 100.50 + 49.50 = 150.00 over 2 rows
//	c.md   49.50 SGD  ┘
//	b.md  200.00 EUR  →  EUR total = 200.00 over 1 row
//	d.md   12.00 (no currency)  →  EXCLUDED from every total, still shown (G3)
//	e.md   no amount at all     →  carries no amount; in no total, and said so
//
// The one answer G2 forbids is 362.00 (or 350.00 without the unit-less row):
// a figure in no currency.
func TestFind_PerUnitTotals_TwoUnitsAndExcludedCounted(t *testing.T) {
	f := newUnitFixture(t, map[string]string{"invoice.yaml": invoiceUnitSchema})
	f.write("a.md", "---\ntype: invoice\nid: INV-1\nclient: Acme\namount: 100.50\ncurrency: SGD\n---\n# INV-1\n")
	f.write("b.md", "---\ntype: invoice\nid: INV-2\nclient: Acme\namount: 200.00\ncurrency: EUR\n---\n# INV-2\n")
	f.write("c.md", "---\ntype: invoice\nid: INV-3\nclient: Beta\namount: 49.50\ncurrency: SGD\n---\n# INV-3\n")
	f.write("d.md", "---\ntype: invoice\nid: INV-4\nclient: Beta\namount: 12.00\n---\n# INV-4\n")
	f.write("e.md", "---\ntype: invoice\nid: INV-5\nclient: Gamma\ncurrency: SGD\n---\n# INV-5\n")

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("invoice"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "amount"),
	})
	if resp.Refused {
		t.Fatalf("the query is answerable; it refused: %+v", resp.Problems)
	}

	// NO COMBINED FIGURE, in any field, ever.
	for _, tot := range resp.Totals {
		for _, forbidden := range []string{"362.00", "350.00", "349.50", "300.50"} {
			if tot.Value == forbidden {
				t.Fatalf("a cross-unit total reached the answer: %q — G2 admits no combined figure", tot.Value)
			}
		}
	}

	if len(resp.Totals) != 2 {
		t.Fatalf("two units in the data means two totals; got %d: %+v", len(resp.Totals), resp.Totals)
	}

	byUnit := map[string]generated.VaultFindTotal{}
	for _, tot := range resp.Totals {
		u, ok := unitOfTotal(tot)
		if !ok {
			t.Fatalf("a total over a unit-carrying number carries no unit: %+v", tot)
		}
		if tot.UnitProperty == nil || *tot.UnitProperty != "currency" {
			t.Fatalf("the unit must travel with the property it was read from; got %+v", tot.UnitProperty)
		}
		byUnit[u] = tot
	}

	sgd, ok := byUnit["SGD"]
	if !ok {
		t.Fatalf("no SGD total; got units %v", byUnit)
	}
	if sgd.Value != "150.00" {
		t.Fatalf("sum(amount) in SGD = %q, want %q (100.50 + 49.50, by hand)", sgd.Value, "150.00")
	}
	eur, ok := byUnit["EUR"]
	if !ok {
		t.Fatalf("no EUR total; got units %v", byUnit)
	}
	if eur.Value != "200.00" {
		t.Fatalf("sum(amount) in EUR = %q, want %q", eur.Value, "200.00")
	}

	// FR-125 per unit (D7.4): each total states ITS OWN scope, over its own
	// row subset, with the whole evaluated set as the denominator.
	if want := "over 2 of 5 evaluated rows (5 shown)"; !strings.HasPrefix(sgd.Scope, want) {
		t.Fatalf("SGD scope = %q, want it to open with %q", sgd.Scope, want)
	}
	if want := "over 1 of 5 evaluated rows (5 shown)"; !strings.HasPrefix(eur.Scope, want) {
		t.Fatalf("EUR scope = %q, want it to open with %q", eur.Scope, want)
	}
	for _, tot := range resp.Totals {
		if !strings.Contains(tot.Scope, "never combined across units") {
			t.Errorf("the scope does not say the figure is per unit: %q", tot.Scope)
		}
		// G3: the excluded row is COUNTED, in the same sentence as the number.
		if !strings.Contains(tot.Scope, "1 row excluded from every total (G3), still shown") {
			t.Errorf("the G3 exclusion is not counted in the scope: %q", tot.Scope)
		}
		if !strings.Contains(tot.Scope, "no confirmed currency value") {
			t.Errorf("the G3 exclusion does not name its cause: %q", tot.Scope)
		}
		// The row that carries no amount at all is a different fact, and it is
		// still reported.
		if !strings.Contains(tot.Scope, "carry no amount") {
			t.Errorf("the row carrying no amount is not accounted for: %q", tot.Scope)
		}
	}

	// The excluded row is STILL SHOWN (G3), not filtered out of the answer.
	if len(resp.Rows) != 5 {
		t.Fatalf("G3 shows the excluded row; got %d rows", len(resp.Rows))
	}

	// The surface an agent actually reads.
	rendered := Render(resp)
	for _, want := range []string{
		"TOTALS: sum(amount) = 200.00 EUR over 1 of 5 evaluated rows (5 shown)",
		"TOTALS: sum(amount) = 150.00 SGD over 2 of 5 evaluated rows (5 shown)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered answer does not carry %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "= 362.00") || strings.Contains(rendered, "= 350.00") {
		t.Fatalf("a combined figure reached the rendered answer:\n%s", rendered)
	}
	t.Logf("rendered totals:\n%s", rendered)
}

// TestFind_PerUnitTotals_AmbiguousUnitIsItsOwnCause is G3's second trigger. A
// row with TWO units is not a row with none: it HAS a unit, it has two, and
// its number is in one of them unknowably. The two are counted apart because
// they have opposite fixes.
func TestFind_PerUnitTotals_AmbiguousUnitIsItsOwnCause(t *testing.T) {
	const schema = `
schema_version: 1
type: invoice
label: Invoice
identity:
  prefix: INV
properties:
  amount:   { type: decimal, unit_property: currency }
  currency: { type: enum, values: [SGD, EUR], many: true }
`
	f := newUnitFixture(t, map[string]string{"invoice.yaml": schema})
	f.write("a.md", "---\ntype: invoice\nid: INV-1\namount: 100.50\ncurrency: [SGD]\n---\n# INV-1\n")
	f.write("b.md", "---\ntype: invoice\nid: INV-2\namount: 7.00\ncurrency: [SGD, EUR]\n---\n# INV-2\n")

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("invoice"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "amount"),
	})
	if resp.Refused {
		t.Fatalf("the query is answerable; it refused: %+v", resp.Problems)
	}
	if len(resp.Totals) != 1 {
		t.Fatalf("one confirmed unit means one total; got %d: %+v", len(resp.Totals), resp.Totals)
	}
	tot := resp.Totals[0]
	if tot.Value != "100.50" {
		t.Fatalf("sum(amount) in SGD = %q, want %q — the two-currency row is in no total", tot.Value, "100.50")
	}
	if !strings.Contains(tot.Scope, "more than one currency value") {
		t.Fatalf("the ambiguous row is not reported as ambiguous: %q", tot.Scope)
	}
	if strings.Contains(tot.Scope, "no confirmed currency value") {
		t.Fatalf("an ambiguous row was reported as a missing one — opposite fixes: %q", tot.Scope)
	}
}

// ---------------------------------------------------------------------------
// D7.3 — the untyped case refuses, exactly as the view endpoint does
// ---------------------------------------------------------------------------

func TestFind_UntypedUnitTotalIsRefused(t *testing.T) {
	f := newUnitFixture(t, map[string]string{
		"invoice.yaml":  invoiceUnitSchema,
		"shipment.yaml": shipmentNoUnitSchema,
	})
	f.write("a.md", "---\ntype: invoice\nid: INV-1\namount: 100.50\ncurrency: SGD\n---\n# INV-1\n")
	f.write("b.md", "---\ntype: invoice\nid: INV-2\namount: 200.00\ncurrency: EUR\n---\n# INV-2\n")

	resp := f.find(generated.VaultFindRequest{
		Aggregate: agg(generated.VaultFindAggregateOpSum, "amount"),
	})

	if len(resp.Totals) != 1 {
		t.Fatalf("a refused total is PRESENT and marked, never omitted; got %d: %+v", len(resp.Totals), resp.Totals)
	}
	tot := resp.Totals[0]
	if !isRefused(tot) {
		t.Fatalf("an untyped total over a unit-carrying number must be refused; got %+v", tot)
	}
	if tot.Value != "" {
		t.Fatalf("a refused total carries no value; got %q", tot.Value)
	}
	for _, want := range []string{"no `type`", "invoice", "G2", "add type=invoice"} {
		if !strings.Contains(tot.Scope, want) {
			t.Fatalf("the refusal does not carry %q: %q", want, tot.Scope)
		}
	}
	// THE ROWS THEMSELVES ARE STILL SHOWN.
	if len(resp.Rows) != 2 {
		t.Fatalf("the refusal withholds the total, not the rows; got %d rows", len(resp.Rows))
	}
}

// TestFind_UntypedUnitLessTotalStillAnswers is the other half of D7.3: with no
// declaration there are no units to cross, so an untyped total over a property
// NO schema pairs with a unit is answered, not refused.
func TestFind_UntypedUnitLessTotalStillAnswers(t *testing.T) {
	f := newUnitFixture(t, map[string]string{
		"invoice.yaml":  invoiceUnitSchema,
		"shipment.yaml": shipmentNoUnitSchema,
	})
	f.write("a.md", "---\ntype: shipment\nid: SHP-1\nweight: 2.50\n---\n# SHP-1\n")
	f.write("b.md", "---\ntype: shipment\nid: SHP-2\nweight: 1.25\n---\n# SHP-2\n")

	resp := f.find(generated.VaultFindRequest{
		Aggregate: agg(generated.VaultFindAggregateOpSum, "weight"),
	})
	if len(resp.Totals) != 1 {
		t.Fatalf("one total, unit-less; got %d: %+v", len(resp.Totals), resp.Totals)
	}
	tot := resp.Totals[0]
	if isRefused(tot) {
		t.Fatalf("no schema pairs `weight` with a unit, so nothing can be crossed: %+v", tot)
	}
	if tot.Value != "3.75" {
		t.Fatalf("sum(weight) = %q, want %q (2.50 + 1.25, by hand)", tot.Value, "3.75")
	}
	if tot.Unit != nil {
		t.Fatalf("a unit-less total must carry no unit field; got %q", *tot.Unit)
	}
}

// ---------------------------------------------------------------------------
// D7.2 — the partition bound refuses rather than degrading
// ---------------------------------------------------------------------------

func TestFind_UnitPartitionBoundRefuses(t *testing.T) {
	const schema = `
schema_version: 1
type: reading
label: Reading
identity:
  prefix: RD
properties:
  qty:  { type: decimal, unit_property: measure }
  measure: { type: text }
`
	f := newUnitFixture(t, map[string]string{"reading.yaml": schema})
	// One MORE distinct unit value than the bound admits, so the bound is
	// proved EXACT rather than merely reached: the refusal must fire at
	// unitPartitionMax + 1 and not before.
	for i := 0; i <= unitPartitionMax; i++ {
		f.write(fmt.Sprintf("r%03d.md", i),
			fmt.Sprintf("---\ntype: reading\nid: RD-%d\nqty: 1.00\nmeasure: u%03d\n---\n# RD-%d\n", i, i, i))
	}

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("reading"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "qty"),
		Limit:     func() *int { n := 200; return &n }(),
	})
	if len(resp.Totals) != 1 {
		t.Fatalf("the bound answers with ONE refused total, never a truncated list; got %d", len(resp.Totals))
	}
	tot := resp.Totals[0]
	if !isRefused(tot) {
		t.Fatalf("past the partition bound the total must be refused, not degraded: %+v", tot)
	}
	if tot.Value != "" {
		t.Fatalf("a refused total carries no value; got %q", tot.Value)
	}
	for _, want := range []string{"measure", fmt.Sprintf("more than %d distinct values", unitPartitionMax), "narrow the filter"} {
		if !strings.Contains(tot.Scope, want) {
			t.Fatalf("the bound's refusal does not carry %q: %q", want, tot.Scope)
		}
	}
}

// TestFind_UnitPartitionBoundIsNotReachedAtTheBound is the exactness half: at
// exactly the bound the answer is computed, so the refusal above is the bound
// firing and not an off-by-one refusing legitimate work.
func TestFind_UnitPartitionBoundIsNotReachedAtTheBound(t *testing.T) {
	const schema = `
schema_version: 1
type: reading
label: Reading
identity:
  prefix: RD
properties:
  qty:  { type: decimal, unit_property: measure }
  measure: { type: text }
`
	f := newUnitFixture(t, map[string]string{"reading.yaml": schema})
	for i := 0; i < unitPartitionMax; i++ {
		f.write(fmt.Sprintf("r%03d.md", i),
			fmt.Sprintf("---\ntype: reading\nid: RD-%d\nqty: 1.00\nmeasure: u%03d\n---\n# RD-%d\n", i, i, i))
	}
	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("reading"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "qty"),
		Limit:     func() *int { n := 200; return &n }(),
	})
	if len(resp.Totals) != unitPartitionMax {
		t.Fatalf("at exactly the bound every unit is totalled; got %d totals, want %d",
			len(resp.Totals), unitPartitionMax)
	}
	for _, tot := range resp.Totals {
		if isRefused(tot) {
			t.Fatalf("nothing is refused at the bound itself: %+v", tot)
		}
		if tot.Value != "1.00" {
			t.Fatalf("each unit holds one 1.00 reading; got %q", tot.Value)
		}
	}
}

// ---------------------------------------------------------------------------
// G4 — text is never totalled by the find tool either
// ---------------------------------------------------------------------------

// TestFind_TextIsNeverTotalled_G4 is the probe this change was required to run
// before assuming. IT WAS ALREADY ENFORCED: parse() refuses a summary the
// property's declared type does not define (FR-155 / opDefinedForType), so
// `sum` over a text property holding "1200" and "3400" never reaches the
// reducer and 4600 is unreachable. The test is kept as the regression that
// says so, because the gate lives in a different file from the rule it serves.
func TestFind_TextIsNeverTotalled_G4(t *testing.T) {
	const schema = `
schema_version: 1
type: ticket
label: Ticket
identity:
  prefix: TIC
properties:
  estimate: { type: text }
`
	f := newUnitFixture(t, map[string]string{"ticket.yaml": schema})
	f.write("a.md", "---\ntype: ticket\nid: TIC-1\nestimate: 1200\n---\n# TIC-1\n")
	f.write("b.md", "---\ntype: ticket\nid: TIC-2\nestimate: 3400\n---\n# TIC-2\n")

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("ticket"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "estimate"),
	})
	if !resp.Refused {
		t.Fatalf("a text column is never totalled, even when it parses as a number (G4): %+v", resp.Totals)
	}
	for _, tot := range resp.Totals {
		if tot.Value == "4600" || tot.Value == "4,600" {
			t.Fatalf("text was totalled: %q", tot.Value)
		}
	}
	rendered := Render(resp)
	if !strings.Contains(rendered, "estimate") || !strings.Contains(rendered, "text property") {
		t.Fatalf("the refusal does not name the property and its declared type:\n%s", rendered)
	}
	if strings.Contains(rendered, "4600") || strings.Contains(rendered, "4,600") {
		t.Fatalf("a total over prose reached the answer:\n%s", rendered)
	}
}

// ---------------------------------------------------------------------------
// BACK-COMPAT — a unit-LESS number is unchanged in shape and value
// ---------------------------------------------------------------------------

// TestFind_UnitLessTotalIsUnchanged pins the pre-D7 answer EXACTLY: one total,
// no unit fields, the same value, and the same rendered line — byte for byte.
//
// The oracle is the behaviour before this change, not the behaviour after it:
// existing agents read this line, and a per-unit split that quietly reworded
// every ordinary total would be a breaking change wearing a bug fix's clothes.
func TestFind_UnitLessTotalIsUnchanged(t *testing.T) {
	f := newUnitFixture(t, map[string]string{"shipment.yaml": shipmentNoUnitSchema})
	f.write("a.md", "---\ntype: shipment\nid: SHP-1\nweight: 2.50\n---\n# SHP-1\n")
	f.write("b.md", "---\ntype: shipment\nid: SHP-2\nweight: 1.25\n---\n# SHP-2\n")

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("shipment"),
		Aggregate: agg(generated.VaultFindAggregateOpSum, "weight"),
	})
	if resp.Refused {
		t.Fatalf("the query is answerable; it refused: %+v", resp.Problems)
	}
	if len(resp.Totals) != 1 {
		t.Fatalf("a unit-less number yields exactly ONE total; got %d: %+v", len(resp.Totals), resp.Totals)
	}
	tot := resp.Totals[0]
	if tot.Label != "sum(weight)" {
		t.Errorf("label = %q, want %q", tot.Label, "sum(weight)")
	}
	if tot.Value != "3.75" {
		t.Errorf("value = %q, want %q (2.50 + 1.25, by hand)", tot.Value, "3.75")
	}
	if want := "over 2 of 2 evaluated rows (2 shown)"; tot.Scope != want {
		t.Errorf("scope = %q, want %q verbatim — the pre-D7 wording", tot.Scope, want)
	}
	if tot.Unit != nil || tot.UnitProperty != nil {
		t.Errorf("a unit-less total carries neither field; got unit=%v unit_property=%v", tot.Unit, tot.UnitProperty)
	}
	if tot.Refused != nil {
		t.Errorf("refused = %v, want absent", *tot.Refused)
	}

	rendered := Render(resp)
	if want := "TOTALS: sum(weight) = 3.75 over 2 of 2 evaluated rows (2 shown)"; !strings.Contains(rendered, want) {
		t.Fatalf("the rendered line changed; want %q in:\n%s", want, rendered)
	}
}

// TestFind_CountAndUniqueCrossNoUnits holds D7.1's closed list: a summary whose
// answer is a COUNT is dimensionless, so it is never partitioned even over a
// unit-carrying property. Splitting `unique(amount)` per currency would answer
// a question nobody asked.
func TestFind_CountAndUniqueCrossNoUnits(t *testing.T) {
	f := newUnitFixture(t, map[string]string{"invoice.yaml": invoiceUnitSchema})
	f.write("a.md", "---\ntype: invoice\nid: INV-1\namount: 100.50\ncurrency: SGD\n---\n# INV-1\n")
	f.write("b.md", "---\ntype: invoice\nid: INV-2\namount: 100.50\ncurrency: EUR\n---\n# INV-2\n")

	for _, tc := range []struct {
		op    generated.VaultFindAggregateOp
		prop  string
		value string
	}{
		{generated.VaultFindAggregateOpUnique, "amount", "1"},
		{generated.VaultFindAggregateOpFilled, "amount", "2"},
		{generated.VaultFindAggregateOpEmpty, "amount", "0"},
		{generated.VaultFindAggregateOpCount, "", "2"},
	} {
		t.Run(string(tc.op), func(t *testing.T) {
			resp := f.find(generated.VaultFindRequest{
				Type:      strPtr("invoice"),
				Aggregate: agg(tc.op, tc.prop),
			})
			if len(resp.Totals) != 1 {
				t.Fatalf("a dimensionless summary is never split per unit; got %d: %+v", len(resp.Totals), resp.Totals)
			}
			if resp.Totals[0].Unit != nil {
				t.Fatalf("%s carries no unit; got %q", tc.op, *resp.Totals[0].Unit)
			}
			if resp.Totals[0].Value != tc.value {
				t.Fatalf("%s = %q, want %q", tc.op, resp.Totals[0].Value, tc.value)
			}
		})
	}
}

// TestFind_MedianSharesOneColumnBudgetAcrossUnits is D7.2 as an executable
// statement rather than a comment.
//
// B3 (FR-151) bounds the memory ONE column reduction holds. G2 splits one
// reduction into one scan per unit value, and the two designs available were:
// give each partition its own budget (the bound silently multiplied by unit
// cardinality — a regression on a stated guarantee) or share one across them.
// D7.2 rules SHARED, and the two are indistinguishable below the bound, so the
// bound is lowered here and the corpus is built to cross it IN TOTAL while no
// single partition does: 2 units x 2 buffered values, against a budget of 3.
//
// A per-partition budget answers two medians. The shared budget refuses, and
// the refusal is the assertion.
func TestFind_MedianSharesOneColumnBudgetAcrossUnits(t *testing.T) {
	restore := columnBufferMaxValues
	columnBufferMaxValues = 3
	t.Cleanup(func() { columnBufferMaxValues = restore })

	f := newUnitFixture(t, map[string]string{"invoice.yaml": invoiceUnitSchema})
	f.write("a.md", "---\ntype: invoice\nid: INV-1\namount: 10.00\ncurrency: SGD\n---\n# INV-1\n")
	f.write("b.md", "---\ntype: invoice\nid: INV-2\namount: 20.00\ncurrency: SGD\n---\n# INV-2\n")
	f.write("c.md", "---\ntype: invoice\nid: INV-3\namount: 30.00\ncurrency: EUR\n---\n# INV-3\n")
	f.write("d.md", "---\ntype: invoice\nid: INV-4\namount: 40.00\ncurrency: EUR\n---\n# INV-4\n")

	resp := f.find(generated.VaultFindRequest{
		Type:      strPtr("invoice"),
		Aggregate: agg(generated.VaultFindAggregateOpMedian, "amount"),
	})
	if len(resp.Totals) == 0 {
		t.Fatalf("a refused total is PRESENT and marked, never omitted: %+v", resp)
	}
	refusedAny := false
	for _, tot := range resp.Totals {
		if isRefused(tot) {
			refusedAny = true
			if !strings.Contains(tot.Scope, "column-buffer bound") {
				t.Fatalf("the refusal does not name B3: %q", tot.Scope)
			}
		}
	}
	if !refusedAny {
		t.Fatalf("four buffered values against a budget of three must refuse — the budget is being handed "+
			"out per partition instead of once per aggregate (D7.2): %+v", resp.Totals)
	}

	// And the control: the SAME corpus under the production budget answers,
	// so the refusal above is the bound firing rather than the fixture being
	// unanswerable.
	columnBufferMaxValues = restore
	resp = f.find(generated.VaultFindRequest{
		Type:      strPtr("invoice"),
		Aggregate: agg(generated.VaultFindAggregateOpMedian, "amount"),
	})
	if len(resp.Totals) != 2 {
		t.Fatalf("under the real budget the same corpus answers two medians; got %d: %+v", len(resp.Totals), resp.Totals)
	}
	for _, tot := range resp.Totals {
		if isRefused(tot) {
			t.Fatalf("nothing is refused under the production budget: %+v", tot)
		}
	}
}
