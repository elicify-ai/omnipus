// Omnipus — spec FR-150..FR-155: the fifteen summary functions.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"encoding/json"
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
// THE FIXTURE, AND ITS ORACLE
// ---------------------------------------------------------------------------
//
// A second schema rather than an extra property on the shared `plant` one:
// `checkbox` is FR-004c's new type and the summaries need a column of it, and
// growing a fixture every other test in the package reads is how one test's
// needs quietly change another test's corpus.
//
// EVERY expected value below is computed BY HAND from the table, in the comment
// that carries it, and none of it is read off a run of the code under test. A
// test whose expectations came from the implementation asserts only that the
// implementation is deterministic.

const bloomSchemaYAML = `
schema_version: 1
type: bloom
label: Bloom
identity:
  prefix: BL
properties:
  species:   { type: text, required: true }
  height_cm: { type: decimal }
  cuttings:  { type: integer }
  planted:   { type: date }
  labels:    { type: text, many: true }
  potted:    { type: checkbox }
`

func bloomSet(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bloom.yaml"), []byte(bloomSchemaYAML), 0o600); err != nil {
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

// bloomFixture is newFixture's shape over the bloom schema. It reuses the
// fixture type's own write/deps so nothing here hand-assembles an indexed row.
func bloomFixture(t *testing.T) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), path, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return &fixture{t: t, store: store, set: bloomSet(t), text: &stubText{hits: map[string]TextHit{}}}
}

// THE CORPUS — five notes, and the whole oracle is derivable from this table:
//
//	id       height_cm  cuttings  planted     labels            potted
//	BL-0001  10.00      4         2026-03-01  [indoor, Humid]   true
//	BL-0002  20.00      1         2026-03-05  [INDOOR, dry]     false
//	BL-0003  30.00      7         2026-03-31  [humid]           true
//	BL-0004  60.00      4         2026-03-05  [shade]           false
//	BL-0005  —          —         —           —                 —
//
// BL-0005 carries NONE of the summarised properties, on purpose: `empty` needs
// something to count, and every other summary needs a row it must exclude.
func bloomCorpus(f *fixture) {
	f.t.Helper()
	write := func(n int, extra string) {
		f.write(fmt.Sprintf("garden/bloom-%04d.md", n), fmt.Sprintf(
			"---\ntype: bloom\nid: BL-%04d\nspecies: Rosa\n%s---\n", n, extra))
	}
	write(1, "height_cm: 10.00\ncuttings: 4\nplanted: 2026-03-01\nlabels: [indoor, Humid]\npotted: true\n")
	write(2, "height_cm: 20.00\ncuttings: 1\nplanted: 2026-03-05\nlabels: [INDOOR, dry]\npotted: false\n")
	write(3, "height_cm: 30.00\ncuttings: 7\nplanted: 2026-03-31\nlabels: [humid]\npotted: true\n")
	write(4, "height_cm: 60.00\ncuttings: 4\nplanted: 2026-03-05\nlabels: [shade]\npotted: false\n")
	write(5, "")
}

// summarise runs one aggregate over the corpus and returns the single total.
func summarise(t *testing.T, f *fixture, op, property string) generated.VaultFindTotal {
	t.Helper()
	agg := generated.VaultFindAggregate{Op: generated.VaultFindAggregateOp(op)}
	if property != "" {
		agg.Property = &property
	}
	aggs := []generated.VaultFindAggregate{agg}
	r := req(withType("bloom"))
	r.Aggregate = &aggs
	resp := mustFind(t, f.deps(), r)
	if len(resp.Totals) != 1 {
		t.Fatalf("%s(%s): totals = %d, want exactly 1", op, property, len(resp.Totals))
	}
	tot := resp.Totals[0]
	if tot.Refused != nil && *tot.Refused {
		t.Fatalf("%s(%s) was refused: %s", op, property, tot.Scope)
	}
	return tot
}

// ---------------------------------------------------------------------------
// TEST 96 — FR-150..FR-153: all fifteen, hand-computed
// ---------------------------------------------------------------------------

// TestAggregates_AllFifteenWithDeclaredPrecision is spec test 96.
//
// Fifteen ops, one corpus, every expected value derived by hand in the comment
// beside it. The three that ROUND say so in their own label, and Stddev's says
// which standard deviation it is — FR-152 and FR-153 are not satisfied by a
// correct number, they are satisfied by a number a reader cannot misread.
func TestAggregates_AllFifteenWithDeclaredPrecision(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	cases := []struct {
		op       string
		property string
		want     string
		// why is the hand computation. It is not decoration: an expected value
		// with no derivation is indistinguishable from one copied off a run.
		why string
	}{
		// ── count: ROWS, not values ──────────────────────────────────────────
		{opCount, "", "5", "five notes are indexed; count takes no property and counts rows"},

		// ── the number domain, over height_cm = 10.00, 20.00, 30.00, 60.00 ───
		{opSum, "height_cm", "120.00",
			"10.00 + 20.00 + 30.00 + 60.00 = 120.00, exact, at the scale the notes wrote"},
		{opMin, "height_cm", "10.00", "the smallest of the four"},
		{opMax, "height_cm", "60.00", "the largest of the four"},
		{opRange, "height_cm", "50.00", "60.00 - 10.00 = 50.00"},
		{opAvg, "height_cm", "30.0000",
			"120 / 4 = 30 exactly; decimal values are written at scale 2, so FR-152's " +
				"declared scale is 2 + 2 = 4 and the mean renders 30.0000"},
		{opMedian, "height_cm", "25.0000",
			"sorted 10,20,30,60; four values, so the median is (20 + 30) / 2 = 25, " +
				"rendered at the same declared scale of 4"},
		{opStddev, "height_cm", "18.7083",
			"mean 30; deviations -20,-10,0,30; squares 400+100+0+900 = 1400; " +
				"POPULATION variance 1400/4 = 350; sqrt(350) = 18.70828693386970..., " +
				"which is 18.7083 at scale 4 rounded half-even"},

		// ── the number domain again, over an INTEGER: scale 0 + 2 = 2 ────────
		{opSum, "cuttings", "16", "4 + 1 + 7 + 4 = 16"},
		{opMin, "cuttings", "1", "the smallest of the four"},
		{opMax, "cuttings", "7", "the largest of the four"},
		{opRange, "cuttings", "6", "7 - 1 = 6"},
		{opAvg, "cuttings", "4.00",
			"16 / 4 = 4 exactly; an integer property declares scale 0, so the " +
				"declared scale is 0 + 2 = 2"},
		{opMedian, "cuttings", "4.00",
			"sorted 1,4,4,7; four values, so (4 + 4) / 2 = 4 at scale 2"},
		{opStddev, "cuttings", "2.12",
			"mean 4; squares of 0,-3,3,0 are 0+9+9+0 = 18; population variance " +
				"18/4 = 4.5; sqrt(4.5) = 2.1213203435596..., which is 2.12 at scale 2"},

		// ── the date domain, over 2026-03-01, -03-05, -03-31, -03-05 ─────────
		{opEarliest, "planted", "2026-03-01", "the earliest of the four dates"},
		{opLatest, "planted", "2026-03-31", "the latest of the four dates"},
		{opMin, "planted", "2026-03-01", "min is the same question as earliest, in the same domain"},
		{opMax, "planted", "2026-03-31", "max is the same question as latest"},
		{opRange, "planted", "30 days",
			"2026-03-31 minus 2026-03-01 is 30 days; a date range renders as a DURATION"},

		// ── the checkbox domain (FR-004c) ────────────────────────────────────
		{opChecked, "potted", "2", "BL-0001 and BL-0003 are true"},
		{opUnchecked, "potted", "2", "BL-0002 and BL-0004 are false"},

		// ── any type ─────────────────────────────────────────────────────────
		{opEmpty, "height_cm", "1", "only BL-0005 has no height_cm"},
		{opFilled, "height_cm", "4", "the other four have one"},
		{opEmpty, "potted", "1",
			"only BL-0005 has no potted — and checked + unchecked = 4, which is NOT " +
				"the row count, because absence is the checkbox third state"},
		{opFilled, "potted", "4", "the other four record a checkbox value"},
		{opUnique, "height_cm", "4", "10.00, 20.00, 30.00 and 60.00 are four distinct values"},
		{opUnique, "cuttings", "3", "4, 1, 7, 4 — the repeated 4 is one value, so three"},
		{opUnique, "planted", "3",
			"2026-03-01, 2026-03-05, 2026-03-31, 2026-03-05 — the repeated date is " +
				"one value, so three"},
		{opUnique, "labels", "4",
			"six values across four notes — indoor, Humid, INDOOR, dry, humid, shade — " +
				"and R-5/R-D fold case, so indoor/INDOOR are one and Humid/humid are " +
				"one: indoor, humid, dry, shade = four"},
		{opUnique, "potted", "2", "true and false"},
	}

	for _, c := range cases {
		name := c.op
		if c.property != "" {
			name = c.op + "_" + c.property
		}
		t.Run(name, func(t *testing.T) {
			tot := summarise(t, f, c.op, c.property)
			if tot.Value != c.want {
				t.Errorf("%s(%s) = %q, want %q — %s", c.op, c.property, tot.Value, c.want, c.why)
			}
			if strings.TrimSpace(tot.Scope) == "" {
				t.Errorf("%s(%s) returned a bare number with no scope clause", c.op, c.property)
			}
		})
	}
}

// TestAggregates_RoundedValuesSayTheyAreRounded is FR-152's reader-facing half
// and FR-153's whole.
//
// The number being right is not the requirement. The requirement is that a
// reader cannot take a rounded number for an exact one, and cannot take a
// population standard deviation for a sample one — which is why both facts are
// asserted on the LABEL, the thing rendered beside the value.
func TestAggregates_RoundedValuesSayTheyAreRounded(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	for _, c := range []struct{ op, property, want string }{
		{opAvg, "height_cm", "rounded to 4 decimal place(s), round-half-even"},
		{opMedian, "height_cm", "rounded to 4 decimal place(s), round-half-even"},
		{opStddev, "height_cm", "rounded to 4 decimal place(s), round-half-even"},
		{opAvg, "cuttings", "rounded to 2 decimal place(s), round-half-even"},
	} {
		tot := summarise(t, f, c.op, c.property)
		if !strings.Contains(tot.Label, c.want) {
			t.Errorf("%s(%s) label = %q, which does not declare the precision (%q). "+
				"FR-152 reverses the recorded no-avg ruling by SATISFYING its objection — "+
				"a number whose precision nobody declared is the thing that was refused.",
				c.op, c.property, tot.Label, c.want)
		}
	}

	sd := summarise(t, f, opStddev, "height_cm")
	if !strings.Contains(sd.Label, "POPULATION standard deviation") {
		t.Errorf("stddev label = %q and does not say which standard deviation it is. "+
			"FR-153: Obsidian's documentation does not say which theirs is, so ours "+
			"DECLARES its definition rather than guessing at a match.", sd.Label)
	}
	if !strings.Contains(sd.Label, "divisor n") {
		t.Errorf("stddev label = %q and does not name the divisor, which is the only "+
			"thing that distinguishes the two definitions", sd.Label)
	}

	// The counting summaries are exact integers. A rounding note on one of them
	// would be a false caveat, which trains a reader to ignore the true ones.
	for _, op := range []string{opCount, opSum, opMin, opMax, opUnique, opEmpty, opFilled} {
		property := "height_cm"
		if op == opCount {
			property = ""
		}
		if tot := summarise(t, f, op, property); strings.Contains(tot.Label, "rounded") {
			t.Errorf("%s label = %q claims a rounding that did not happen", op, tot.Label)
		}
	}
}

// TestAggregates_OddCountMedianIsAnExactValueOfTheColumn.
//
// FR-152 rounds "Median (even count)" and the parenthetical is doing real work:
// an odd-count median IS one of the column's own values, so it is rendered
// exactly as the note wrote it and its label claims no rounding.
func TestAggregates_OddCountMedianIsAnExactValueOfTheColumn(t *testing.T) {
	f := bloomFixture(t)
	f.write("garden/a.md", "---\ntype: bloom\nid: BL-0001\nspecies: Rosa\nheight_cm: 10.00\n---\n")
	f.write("garden/b.md", "---\ntype: bloom\nid: BL-0002\nspecies: Rosa\nheight_cm: 20.00\n---\n")
	f.write("garden/c.md", "---\ntype: bloom\nid: BL-0003\nspecies: Rosa\nheight_cm: 60.00\n---\n")

	tot := summarise(t, f, opMedian, "height_cm")
	// Sorted 10.00, 20.00, 60.00 — three values, so the median is the middle
	// one, 20.00, at the scale the note itself wrote.
	if tot.Value != "20.00" {
		t.Errorf("median = %q, want 20.00 — the middle of three values, exactly as written", tot.Value)
	}
	if strings.Contains(tot.Label, "rounded") {
		t.Errorf("label = %q claims a rounding; nothing was computed, so nothing was rounded", tot.Label)
	}
}

// TestAggregates_ThereIsNoSummariesKeyOnTheWire is founder ruling 2, asserted
// against the serialised response rather than against the Go types — the wire
// is where a second key would actually appear.
func TestAggregates_ThereIsNoSummariesKeyOnTheWire(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	aggs := []generated.VaultFindAggregate{
		{Op: opMedian, Property: strPtr("height_cm")},
		{Op: opUnique, Property: strPtr("labels")},
		{Op: opCount},
	}
	r := req(withType("bloom"))
	r.Aggregate = &aggs
	resp := mustFind(t, f.deps(), r)

	blob, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(blob), `"summaries"`) {
		t.Errorf("the response carries a `summaries` key. The fifteen extend `aggregates` "+
			"under ONE name (founder ruling 2); a second key for one capability makes "+
			"every importer choose, and they choose differently:\n%s", blob)
	}
	if len(resp.Totals) != 3 {
		t.Fatalf("totals = %d, want 3 — the summaries ARE the totals", len(resp.Totals))
	}
}

// ---------------------------------------------------------------------------
// FR-155 — a summary a type does not define is refused NAMING the ones it does
// ---------------------------------------------------------------------------

func TestAggregates_UndefinedSummaryNamesTheOnesTheTypeDefines(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	cases := []struct {
		op, property string
		// mustName is a summary the property's own type DOES define, which the
		// refusal has to offer. A refusal that says only "not supported" leaves
		// the caller to guess, and in an agentic loop guessing means retrying.
		mustName string
	}{
		{opStddev, "species", opUnique},  // stddev over text
		{opChecked, "planted", opLatest}, // checked over a date
		{opSum, "planted", opEarliest},   // sum over a date
		{opAvg, "potted", opChecked},     // avg over a checkbox
		{opEarliest, "height_cm", opAvg}, // earliest over a number
	}

	for _, c := range cases {
		t.Run(c.op+"_"+c.property, func(t *testing.T) {
			aggs := []generated.VaultFindAggregate{
				{Op: generated.VaultFindAggregateOp(c.op), Property: strPtr(c.property)},
			}
			r := req(withType("bloom"))
			r.Aggregate = &aggs
			resp := mustRefuse(t, f.deps(), r)

			if len(resp.Problems) != 1 {
				t.Fatalf("problems = %d, want 1: %+v", len(resp.Problems), resp.Problems)
			}
			p := resp.Problems[0]
			if !strings.Contains(p.Reason, c.mustName) {
				t.Errorf("the refusal does not name %q, a summary %s DOES define: %q",
					c.mustName, c.property, p.Reason)
			}
			if p.Property == nil || *p.Property != c.property {
				t.Errorf("problem.Property = %v, want %q", p.Property, c.property)
			}
			// FR-154's other half: a refused query returns no summary at all,
			// rather than one over whatever it could reach.
			if len(resp.Totals) != 0 {
				t.Errorf("a refused query returned %d total(s): %+v", len(resp.Totals), resp.Totals)
			}
		})
	}
}

// TestAggregates_AnOpOutsideTheFifteenIsRefused. The enum is CLOSED; an op that
// falls through to a reducer with no case for it produces an empty value, and
// an empty value reads as an answer.
func TestAggregates_AnOpOutsideTheFifteenIsRefused(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	aggs := []generated.VaultFindAggregate{{Op: "mode", Property: strPtr("height_cm")}}
	r := req(withType("bloom"))
	r.Aggregate = &aggs
	resp := mustRefuse(t, f.deps(), r)

	if len(resp.Problems) != 1 {
		t.Fatalf("problems = %d, want 1: %+v", len(resp.Problems), resp.Problems)
	}
	if !strings.Contains(resp.Problems[0].Reason, "median") {
		t.Errorf("the refusal does not list the fifteen: %q", resp.Problems[0].Reason)
	}
}

// TestAggregates_TheFifteenAreExactlyFifteen pins the closed list against the
// generated wire enum. Both sides are enumerated independently — the domain
// tables here, the contract there — so a drift in either direction fails.
func TestAggregates_TheFifteenAreExactlyFifteen(t *testing.T) {
	ops := allSummaryOps()
	if len(ops) != 15 {
		t.Fatalf("allSummaryOps returned %d ops, want 15: %v", len(ops), ops)
	}
	for _, op := range ops {
		if !generated.VaultFindAggregateOp(op).Valid() {
			t.Errorf("%q is computed here but the request enum cannot name it", op)
		}
		if !generated.VaultFindTotalOp(op).Valid() {
			t.Errorf("%q is computed here but the RESPONSE enum cannot name it — the SPA "+
				"validates responses against the generated Zod schema and drops what "+
				"fails, so this is a correct answer that disappears silently", op)
		}
	}

	// FR-151's class split is CLOSED at two. A third buffered op is a third
	// thing that can exhaust memory, so it must be a decision with a bound
	// attached rather than a line someone added to a switch.
	var buffered []string
	for _, op := range ops {
		if isPopulationOp(op) {
			buffered = append(buffered, op)
		}
	}
	if len(buffered) != 2 || buffered[0] != opMedian || buffered[1] != opUnique {
		t.Errorf("the population class is %v, want exactly [median unique] (FR-151)", buffered)
	}
}

// ---------------------------------------------------------------------------
// TEST 101 — FR-151's B3, the column-buffer bound
// ---------------------------------------------------------------------------

// TestAggregates_ColumnBufferBoundRefusesNamed is spec test 101, byte half.
//
// Ninety notes each carrying one 100,000-byte label. `unique` buffers a key per
// DISTINCT value, so the column it is asked to hold is 9,000,180 bytes against
// a bound of 8,388,608 — and the point of the assertion is not that it refuses
// but WHERE: the buffer must stop at the bound, mid-scan, holding a fraction of
// the column, rather than reading the column and complaining afterwards.
//
//	8,388,608 / 100,002 bytes per key = 83.88, so 83 keys fit
//	(83 x 100,002 = 8,300,166) and the 84th (8,400,168) does not.
func TestAggregates_ColumnBufferBoundRefusesNamed(t *testing.T) {
	const notes = 90
	const valueLen = 100_000

	f := bloomFixture(t)
	for i := 1; i <= notes; i++ {
		// Distinct, so nothing dedupes: a repeated value would be admitted once
		// and the bound would never be reached.
		label := strings.Repeat("a", valueLen-6) + fmt.Sprintf("%06d", i)
		f.write(fmt.Sprintf("garden/big-%04d.md", i), fmt.Sprintf(
			"---\ntype: bloom\nid: BL-%04d\nspecies: Rosa\nlabels: [%s]\n---\n", i, label))
	}

	aggs := []generated.VaultFindAggregate{{Op: opUnique, Property: strPtr("labels")}}
	limit := 1
	minimal := generated.VaultFindRequestDetail("minimal")
	r := req(withType("bloom"))
	r.Aggregate = &aggs
	r.Limit = &limit
	r.Detail = &minimal

	resp := mustFind(t, f.deps(), r)
	if len(resp.Totals) != 1 {
		t.Fatalf("totals = %d, want 1", len(resp.Totals))
	}
	tot := resp.Totals[0]

	// FR-154 — a bound that refuses returns NO SUMMARY. A count of distinct
	// values taken over the 83 that fit would be a confident wrong number, and
	// nothing in the response would say so.
	if tot.Refused == nil || !*tot.Refused {
		t.Fatalf("unique(labels) over a %d-byte column returned %q instead of refusing. "+
			"A summary over a truncated population is the exact failure this "+
			"document exists to remove.", notes*(valueLen+2), tot.Value)
	}
	if tot.Value != "" {
		t.Errorf("a refused summary carries the value %q; a number would be read as an answer", tot.Value)
	}

	// MID-SCAN, and this is the assertion that distinguishes a real abort from
	// a post-hoc complaint: it stopped at 83 of 90, so the other 7 keys were
	// never held.
	if !strings.Contains(tot.Scope, "83 value(s)") {
		t.Errorf("the refusal does not report the count REACHED (83, where 83 x 100,002 "+
			"= 8,300,166 bytes fits under the 8,388,608-byte bound and an 84th does "+
			"not). A bound that stopped later than it claims is a bound that did not "+
			"stop: %q", tot.Scope)
	}
	if strings.Contains(tot.Scope, fmt.Sprintf("%d value(s)", notes)) {
		t.Errorf("the refusal reports all %d values as held, which means the column was "+
			"read in full and the bound was checked afterwards — the allocation B3 "+
			"exists to prevent: %q", notes, tot.Scope)
	}
	// The remedy, and the bound itself, in the same sentence as the count.
	for _, want := range []string{"8,388,608", "100,000", "narrow the filter", "scalar property"} {
		if !strings.Contains(tot.Scope, want) {
			t.Errorf("the refusal does not name %q: %q", want, tot.Scope)
		}
	}

	// The refusal is per-SUMMARY, not per-query: the answer itself is intact.
	if resp.Counts.Evaluated != notes {
		t.Errorf("evaluated = %d, want %d — B3 bounds one column, not the query",
			resp.Counts.Evaluated, notes)
	}
}

// TestColumnBuffer_ValueHalfStopsAtTheBound is B3's OTHER half, exercised
// directly because reaching 100,000 values through the index would need more
// rows than B2 admits — 10,000 survivors times 11 elements is the cheapest
// route and it is still a corpus, not a test.
//
// The unit under test is the whole bound: both halves, and the fact that
// whichever is reached FIRST wins.
func TestColumnBuffer_ValueHalfStopsAtTheBound(t *testing.T) {
	var b columnBuffer
	for i := 0; i < columnBufferMaxValues; i++ {
		if !b.admit(1) {
			t.Fatalf("the buffer refused value %d of %d, below its own bound", i+1, columnBufferMaxValues)
		}
	}
	if b.values != columnBufferMaxValues {
		t.Fatalf("buffer holds %d values, want %d", b.values, columnBufferMaxValues)
	}
	if b.admit(1) {
		t.Errorf("the buffer admitted value %d, one past a bound of %d",
			columnBufferMaxValues+1, columnBufferMaxValues)
	}

	// The byte half, in isolation: one value is enough to cross it, so the two
	// halves are genuinely independent rather than one guarding the other.
	var c columnBuffer
	if !c.admit(columnBufferMaxBytes) {
		t.Errorf("the buffer refused a value that exactly fills the byte bound")
	}
	if c.admit(1) {
		t.Errorf("the buffer admitted a byte past a bound of %d", columnBufferMaxBytes)
	}
	if c.values != 1 {
		t.Errorf("buffer holds %d values after one admission and one refusal, want 1", c.values)
	}
}

// ---------------------------------------------------------------------------
// TEST 97 — FR-154: a bound refusal carries no summary
// ---------------------------------------------------------------------------

// b1Store reports a candidate population past B1 while delegating everything
// else to a real store, so the refusal below is the PRODUCTION B1 path firing —
// not a hand-built response asserted against itself.
type b1Store struct {
	propindex.Store
	count int
}

func (s b1Store) CountCandidates(context.Context, propindex.Selector) (int, error) {
	return s.count, nil
}

// TestAggregates_BoundRefusalReturnsNoValue is spec test 97.
//
// A median over a truncated set is unconstructable, and it is asserted through
// the refusal path rather than by reading the code: the query asks for four
// summaries, B1 fires, and the response carries none of them.
func TestAggregates_BoundRefusalReturnsNoValue(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	d := f.deps()
	d.Store = b1Store{Store: f.store, count: propindex.BoundNarrowedCandidates + 1}

	aggs := []generated.VaultFindAggregate{
		{Op: opMedian, Property: strPtr("height_cm")},
		{Op: opAvg, Property: strPtr("height_cm")},
		{Op: opUnique, Property: strPtr("labels")},
		{Op: opCount},
	}
	r := req(withType("bloom"))
	r.Aggregate = &aggs

	resp := mustRefuse(t, d, r)

	if len(resp.Totals) != 0 {
		t.Fatalf("a query refused at B1 returned %d total(s): %+v. FR-154: no summary is "+
			"ever computed over a truncated set, and `count` is the most tempting of "+
			"them because a row count over what fit LOOKS right.",
			len(resp.Totals), resp.Totals)
	}
	if resp.Complete {
		t.Errorf("a refused query reports COMPLETE")
	}
	if len(resp.Problems) != 1 || !strings.Contains(resp.Problems[0].Reason, group3(propindex.BoundNarrowedCandidates)) {
		t.Errorf("the refusal does not quote B1's own limit: %+v", resp.Problems)
	}
}

// ---------------------------------------------------------------------------
// SCOPE — FR-125 over the new ops
// ---------------------------------------------------------------------------

// TestAggregates_ValueCountIsNotARowCount.
//
// FR-151's memory correction turns on this distinction, and a reader hits it
// first in the scope clause: `unique(labels)` reduces six VALUES held by four
// ROWS, and a scope that reported only "over 4 of 5 rows" would let a reader
// take six for four.
func TestAggregates_ValueCountIsNotARowCount(t *testing.T) {
	f := bloomFixture(t)
	bloomCorpus(f)

	tot := summarise(t, f, opUnique, "labels")
	if !strings.Contains(tot.Scope, "over 4 of 5 evaluated rows") {
		t.Errorf("scope = %q, want it to name the 4 rows carrying labels out of 5 evaluated", tot.Scope)
	}
	if !strings.Contains(tot.Scope, "6 value(s) read") {
		t.Errorf("scope = %q, want it to name the 6 values those 4 rows hold. A value "+
			"count read as a record count is the arithmetic error B3 exists to "+
			"correct, met by a reader instead of by a bound.", tot.Scope)
	}
	if !strings.Contains(tot.Scope, "1 row(s) carry no labels") {
		t.Errorf("scope = %q, want it to name the row it excluded", tot.Scope)
	}
}
