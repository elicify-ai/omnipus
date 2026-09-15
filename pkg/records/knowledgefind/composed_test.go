// Omnipus — the COMPOSED path: a formula over a file.* property, inside an
// untyped multi-type view, aggregated by a buffering summary, under B1/B2/B3.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// Each of the four capabilities below already had coverage on its own, and a
// reviewer found every one of them coherent alone and the COMPOSITION
// unspecified. Alone-correct is the weakest useful property a feature can have:
// `file.mtime` resolving correctly proves nothing about whether a formula that
// reads it is evaluated before the summary that reduces it, or whether a view
// with no record type can name either.
//
// So this file runs ONE query that needs all four at once —
//
//	a formula          formula.payload = toFixed(file.size - 40, 0)
//	over a file.*      file.size, plus file.folder and file.tags in the filter
//	untyped, multi-type  no `type`: plants, tools and an untyped note together
//	a buffering summary  median() and unique(), FR-151's population class
//
// — and asserts the answer against numbers derived from the corpus by hand,
// below, before the query was ever run.
//
// THE ORACLE IS THE TABLE IN composedCorpus AND NOTHING ELSE. Every expected
// value in this file is arithmetic over the sizes and tags declared there:
// 1040-40 = 1000, the median of {1000, 3000, 5000} is 3000, the distinct tags
// of the three survivors are three. None of it was read off a response.
// ---------------------------------------------------------------------------

// composedCorpus is the fixture AND the oracle's input.
//
// `size` is the file's stat size in bytes, written into the note row exactly as
// FR-131 stores it. It is 40 more than a round number in every row, so the
// formula `file.size - 40` produces values a reader can check at a glance and a
// wrong operand (file.size vs. a property, or an off-by-one folder) produces a
// visibly wrong one.
var composedCorpus = []struct {
	path     string
	typeName string // "" is a note with no declared type (FR-005)
	tags     []string
	size     int64
}{
	{path: "garden/monstera.md", typeName: "plant", tags: []string{"indoor", "keep"}, size: 1040},
	{path: "garden/fern.md", typeName: "plant", tags: []string{"indoor"}, size: 2040},
	{path: "garden/spade.md", typeName: "tool", tags: []string{"shed", "keep"}, size: 3040},
	{path: "garden/rake.md", typeName: "tool", tags: []string{"shed"}, size: 4040},
	{path: "garden/notes.md", typeName: "", tags: []string{"keep"}, size: 5040},
	{path: "shed/hose.md", typeName: "tool", tags: []string{"keep"}, size: 9040},
}

// The composed query, written once so every assertion below is about the SAME
// execution rather than about six queries that happen to resemble each other.
const (
	composedFolder  = "garden"
	composedTag     = "keep"
	composedFormula = "toFixed(file.size - 40, 0)"
	composedView    = "garden-keepers"
)

// THE ORACLE, derived by hand from composedCorpus above.
//
//	filter: file.folder = "garden" AND file.tags = "keep"
//	  monstera  garden + keep   -> IN   payload 1040-40 = 1000
//	  fern      garden, no keep -> out
//	  spade     garden + keep   -> IN   payload 3040-40 = 3000
//	  rake      garden, no keep -> out
//	  notes     garden + keep   -> IN   payload 5040-40 = 5000   (untyped note)
//	  hose      keep, but folder "shed" -> out
//
// sort formula.payload desc: notes(5000), spade(3000), monstera(1000)
// group_by file.tags:  indoor{monstera}  keep{monstera,spade,notes}  shed{spade}
// sum(formula.payload)    = 1000 + 3000 + 5000 = 9000
// median(formula.payload) = middle of {1000,3000,5000}             = 3000
// unique(file.tags)       = |{indoor, keep, shed}|                 = 3
// count()                 = 3 rows
var (
	wantOrderedPaths = []string{"garden/notes.md", "garden/spade.md", "garden/monstera.md"}
	wantPayloads     = map[string]int64{
		"garden/notes.md":    5000,
		"garden/spade.md":    3000,
		"garden/monstera.md": 1000,
	}
	wantGroups = []struct {
		key   string
		paths []string
	}{
		{key: "indoor", paths: []string{"garden/monstera.md"}},
		{key: "keep", paths: []string{"garden/monstera.md", "garden/spade.md", "garden/notes.md"}},
		{key: "shed", paths: []string{"garden/spade.md"}},
	}
	wantSum    = big.NewRat(9000, 1)
	wantMedian = big.NewRat(3000, 1)
	wantUnique = big.NewRat(3, 1)
	wantCount  = big.NewRat(3, 1)
)

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// composedSchemas declares TWO record types, because "multi-type" is the point: a
// view with no `type` spans them, and a one-type corpus could not tell a view
// that spans types from one that quietly narrowed to the only type there is.
func composedSchemas(t *testing.T) *records.SchemaSet {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	write("plant.yaml", `
schema_version: 1
type: plant
label: Plant
identity:
  prefix: PL
properties:
  species: { type: text }
`)
	write("tool.yaml", `
schema_version: 1
type: tool
label: Tool
identity:
  prefix: TL
properties:
  material: { type: text }
`)
	set, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schemas were rejected: %v", report.Rejections)
	}
	return set
}

// composedFixture is a live properties index over composedCorpus, with a recorder
// attached so the emitted SQL can be read back.
type composedFixture struct {
	store propindex.Store
	set   *records.SchemaSet
	rec   *propindex.Recorder
	text  *stubText
}

func newComposedFixture(t *testing.T) *composedFixture {
	t.Helper()
	rec := propindex.NewRecorder()
	path := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), path, propindex.Options{Recorder: rec})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	f := &composedFixture{store: store, set: composedSchemas(t), rec: rec, text: &stubText{hits: map[string]TextHit{}}}

	// A fixed instant per note, so nothing in the answer depends on when the
	// test ran. The stat columns are written the way FR-131 stores them and the
	// way FR-136 refreshes them — through NoteRows, not by reaching into SQL.
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	for i, n := range composedCorpus {
		src := composedNote(n.typeName, n.tags)
		b := []byte(src)
		record := records.ParseRecord(n.path, b)
		schema, _ := f.set.Get(record.TypeName())
		rows := propindex.BuildNoteRows(record, schema, b, propindex.SourceHash(b))
		rows.Size = n.size
		rows.MtimeNanos = base.Add(time.Duration(i) * time.Hour).UnixNano()
		rows.CtimeNanos = base.UnixNano()
		rows.HasCtime = true
		if err := f.store.UpsertNote(context.Background(), rows); err != nil {
			t.Fatalf("UpsertNote(%s): %v", n.path, err)
		}
		f.text.hits[n.path] = TextHit{Path: n.path, SourceHash: rows.SourceHash, Score: 1}
	}
	// Only the QUERY's statements are the subject of the SQL guard; the write
	// path legitimately mentions every column there is.
	rec.Reset()
	return f
}

func composedNote(typeName string, tags []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if typeName != "" {
		b.WriteString("type: " + typeName + "\n")
	}
	b.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	b.WriteString("---\n\n# A note\n")
	return b.String()
}

// composedViews is a ViewLoader that ALSO carries the view's `formulas:` map —
// the FR-141 half the base interface does not model.
type composedViews struct {
	req      generated.VaultFindRequest
	formulas map[string]string
}

func (g composedViews) View(name string) (generated.VaultFindRequest, bool) {
	if name != composedView {
		return generated.VaultFindRequest{}, false
	}
	return g.req, true
}

func (g composedViews) Names() []string { return []string{composedView} }

// The interface is SATISFIABLE, asserted at compile time — without this, the
// negative result in TestSeam_ProductionViewLoaderStillCannotCarryViewFormulas
// could mean "nothing can implement ViewFormulaLoader" rather than "the
// production loader does not yet".
var (
	_ ViewLoader        = composedViews{}
	_ ViewFormulaLoader = composedViews{}
)

func (g composedViews) Formulas(name string) (map[string]string, bool) {
	if name != composedView {
		return nil, false
	}
	return g.formulas, true
}

func viewName(s string) *string { return &s }

// composedRequest is the ONE query. It names no `type` — that is the untyped
// multi-type half — and every other argument reaches across the other three.
func composedRequest() (generated.VaultFindRequest, composedViews) {
	folder := records.FileFolderProp
	tags := records.FileTagsProp
	eq := generated.VaultFilterNodeOp("=")
	folderValue := composedFolder
	tagValue := composedTag
	payload := FormulaNamespace + "payload"
	desc := generated.VaultFindSortDirectionDesc
	limit := 10

	req := generated.VaultFindRequest{
		View:  viewName(composedView),
		Limit: &limit,
		Filter: &generated.VaultFilterNode{
			All: &[]generated.VaultFilterNode{
				{Property: &folder, Op: &eq, Value: &folderValue},
				{Property: &tags, Op: &eq, Value: &tagValue},
			},
		},
		Sort:    &[]generated.VaultFindSort{{Property: payload, Direction: &desc}},
		GroupBy: &[]generated.VaultFindGroupBy{{Property: tags}},
		Aggregate: &[]generated.VaultFindAggregate{
			{Op: generated.VaultFindAggregateOp(opCount)},
			{Op: generated.VaultFindAggregateOp(opSum), Property: &payload},
			{Op: generated.VaultFindAggregateOp(opMedian), Property: &payload},
			{Op: generated.VaultFindAggregateOp(opUnique), Property: &tags},
		},
	}
	views := composedViews{
		// The saved view contributes the formulas and nothing else, so a
		// failure here cannot be a saved filter doing the work.
		req:      generated.VaultFindRequest{},
		formulas: map[string]string{"payload": composedFormula},
	}
	return req, views
}

// ---------------------------------------------------------------------------
// THE COMPOSED TEST
// ---------------------------------------------------------------------------

func TestComposed_FormulaOverFileMetadataInAnUntypedViewUnderABufferingSummary(t *testing.T) {
	f := newComposedFixture(t)
	req, views := composedRequest()

	d := Deps{
		Schemas: f.set,
		Store:   f.store,
		Text:    f.text,
		Views:   views,
		Epoch:   4242,
		Now:     time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}

	resp, err := Find(context.Background(), d, req)
	if err != nil {
		t.Fatalf("the composed query was refused: %v", err)
	}
	if resp.Refused {
		t.Fatalf("the composed query was refused: %+v", resp.Problems)
	}

	// ── 1. THE ROWS: the file.* filter decided membership, in Go ────────────
	got := make([]string, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		got = append(got, r.Path)
	}
	if !reflect.DeepEqual(got, wantOrderedPaths) {
		t.Errorf("rows = %v, want %v\n(the filter is file.folder=%q AND file.tags=%q; the order is "+
			"formula.payload desc)", got, wantOrderedPaths, composedFolder, composedTag)
	}
	if resp.Counts.Evaluated != len(wantOrderedPaths) {
		t.Errorf("evaluated = %d, want %d", resp.Counts.Evaluated, len(wantOrderedPaths))
	}

	// ── 2. THE FORMULA VALUE, per row ──────────────────────────────────────
	//
	// The rendered cell is checked NUMERICALLY rather than as a string: the
	// arithmetic is what the oracle computed, and the thousands separator is a
	// choice of the compact-text projection, not part of the answer.
	for _, row := range resp.Rows {
		cell, ok := cellValue(row, FormulaNamespace+"payload")
		if !ok {
			t.Errorf("%s: no formula.payload cell; the formula was never rendered", row.Path)
			continue
		}
		want := big.NewRat(wantPayloads[row.Path], 1)
		if gotRat := mustRat(t, cell); gotRat.Cmp(want) != 0 {
			t.Errorf("%s: formula.payload = %s, want %s (file.size - 40)", row.Path, gotRat, want)
		}
	}

	// ── 3. THE GROUPS: a file.* MANY property, one record in several ────────
	if resp.Groups == nil {
		t.Fatalf("group_by %s produced no groups at all", records.FileTagsProp)
	}
	for i, want := range wantGroups {
		if i >= len(*resp.Groups) {
			t.Errorf("group %d (%q) is missing; got %d groups", i, want.key, len(*resp.Groups))
			continue
		}
		g := (*resp.Groups)[i]
		if g.Key != want.key {
			t.Errorf("group %d key = %q, want %q", i, g.Key, want.key)
		}
		if g.Count != len(want.paths) {
			t.Errorf("group %q count = %d, want %d (paths %v)", want.key, g.Count, len(want.paths), want.paths)
		}
		if !sameSet(g.Paths, want.paths) {
			t.Errorf("group %q paths = %v, want %v", want.key, g.Paths, want.paths)
		}
	}

	// ── 4. THE SUMMARIES, streaming AND buffering ──────────────────────────
	//
	// median and unique are FR-151's POPULATION class: they buffer a column
	// under B3. They are the ones that would break first if a formula were
	// evaluated lazily, per page, or after the page was cut — so they are the
	// composition's real load test, not decoration on it.
	assertTotal(t, resp, opCount, "", wantCount)
	assertTotal(t, resp, opSum, FormulaNamespace+"payload", wantSum)
	assertTotal(t, resp, opMedian, FormulaNamespace+"payload", wantMedian)
	assertTotal(t, resp, opUnique, records.FileTagsProp, wantUnique)

	// ── 5. RULING R-A, over the statements this very query emitted ─────────
	assertNoComparisonReachedSQL(t, f.rec)

	// ── 6. B1 IS STILL TAKEN FIRST, before the child-table prepass ─────────
	//
	// The order is the bound's whole value. B1 exists to refuse a query BEFORE
	// it pays for anything, so a query that buffers 50,000 notes' tags and then
	// discovers it was over the evaluation bound has already paid the cost the
	// bound was written to avoid. Asserting the ORDER of the emitted statements
	// is how that is checked without writing 50,001 notes.
	assertB1PrecedesTheChildPrepass(t, f.rec)
}

// assertNoComparisonReachedSQL is the emitted-SQL guard applied to the COMPOSED
// query rather than to a synthetic one.
//
// It reads the statements the store actually handed the driver while the query
// above ran, and fails if any of them mentions a value column of the parent or
// of either child table in a way a comparison would need — the columns FR-135's
// COLUMN half names: `mtime`, `ctime`, `size`, `tag`, `target`, `embed`.
//
// It also fails if any statement joins two child tables, which is D16.6's
// fan-out and FR-131's named prohibition.
func assertNoComparisonReachedSQL(t *testing.T, rec *propindex.Recorder) {
	t.Helper()
	stmts := rec.InPhase(propindex.PhaseRead)
	if len(stmts) == 0 {
		// A guard that can pass over nothing is not a guard. The composed query
		// counts candidates, streams them, streams note_tags and (for the
		// formula's file.size, which rides on the parent row) nothing more —
		// so it must have emitted statements, and zero means the recorder was
		// not wired and this whole check was vacuous.
		t.Fatal("the recorder captured no read statements; the SQL guard was vacuous")
	}

	// The literal-comparison shapes a pushed-down predicate would take. A bare
	// column NAME appears legitimately in a SELECT list, so the guard looks for
	// the column adjacent to an operator or a bind, which a projection never is.
	valueColumns := []string{"mtime", "ctime", "size", "tag", "target", "embed"}
	operators := []string{" = ", " > ", " < ", ">=", "<=", "!=", " LIKE ", " IN ", " GLOB "}

	children := []string{"note_props", "note_tags", "note_links"}

	for _, sql := range stmts {
		flat := strings.Join(strings.Fields(sql), " ")

		for _, col := range valueColumns {
			for _, op := range operators {
				needle := col + op
				if strings.Contains(flat, needle) {
					t.Errorf("a read statement compares %q in SQL — ruling R-A says SQLite narrows and "+
						"the Go comparator decides:\n  %s", needle, flat)
				}
			}
		}

		seen := 0
		for _, child := range children {
			if strings.Contains(flat, child) {
				seen++
			}
		}
		if seen > 1 {
			t.Errorf("a read statement names %d child tables at once — FR-131 forbids the join, because "+
				"two children of one parent joined together is a cross-product:\n  %s", seen, flat)
		}
	}
}

// assertB1PrecedesTheChildPrepass reads the statement ORDER, which is the only
// observable the bound's cost argument actually rests on.
func assertB1PrecedesTheChildPrepass(t *testing.T, rec *propindex.Recorder) {
	t.Helper()
	stmts := rec.InPhase(propindex.PhaseRead)
	firstCount, firstChild := -1, -1
	for i, sql := range stmts {
		flat := strings.Join(strings.Fields(sql), " ")
		if firstCount < 0 && strings.Contains(flat, "COUNT(*)") && strings.Contains(flat, "FROM notes") {
			firstCount = i
		}
		if firstChild < 0 && (strings.Contains(flat, "note_tags") || strings.Contains(flat, "note_links")) {
			firstChild = i
		}
	}
	if firstCount < 0 {
		t.Fatal("no B1 count statement was emitted at all; the evaluation bound never ran")
	}
	if firstChild < 0 {
		t.Fatal("no child-table statement was emitted; the composed query names file.tags, so the " +
			"prepass must have run and this ordering check is vacuous without it")
	}
	if firstCount > firstChild {
		t.Errorf("the child-table prepass (statement %d) ran BEFORE B1's count (statement %d): a query "+
			"over the evaluation bound would buffer every candidate's tags and only then be refused, "+
			"which is exactly the cost B1 exists to avoid", firstChild, firstCount)
	}
}

// TestComposed_SelectorStillCarriesExactlyThreeFields is FR-135's growth guard,
// asserted at the composed query's own selector rather than in the abstract.
func TestComposed_SelectorStillCarriesExactlyThreeFields(t *testing.T) {
	// The TYPE half: no field may be added to Selector without this failing,
	// because a fourth field is where a pushed-down predicate would live.
	typ := reflect.TypeOf(propindex.Selector{})
	if typ.NumField() != 3 {
		names := make([]string, typ.NumField())
		for i := range names {
			names[i] = typ.Field(i).Name
		}
		t.Fatalf("propindex.Selector has %d fields (%v); ruling R-A and FR-135 fix it at three "+
			"(RecordType, Kind, PathPrefix) — a fourth is where a comparison would be smuggled into SQL",
			typ.NumField(), names)
	}

	// The VALUE half: the composed query names three file properties and a
	// formula, and NONE of them appears in what the store is told.
	f := newComposedFixture(t)
	req, views := composedRequest()
	set := f.set
	q, r := parse(withView(req, views), set, views.formulas, 0)
	if r != nil {
		t.Fatalf("the composed query did not parse: %v", r)
	}
	sel := q.selector("garden/")
	if sel.RecordType != "" {
		t.Errorf("selector.RecordType = %q, want empty: the composed view names no type", sel.RecordType)
	}
	if sel.Kind != propindex.KindNote {
		t.Errorf("selector.Kind = %q, want %q", sel.Kind, propindex.KindNote)
	}
	if sel.PathPrefix != "garden/" {
		t.Errorf("selector.PathPrefix = %q, want %q", sel.PathPrefix, "garden/")
	}
}

// withView applies the saved view the way Find does, so parse sees the same
// request the composed query gives it.
func withView(req generated.VaultFindRequest, views composedViews) generated.VaultFindRequest {
	if r := applyView(&req, views); r != nil {
		panic("the fixture view did not resolve: " + r.Error())
	}
	return req
}

// ---------------------------------------------------------------------------
// Small helpers — each one a fact about the response, never about the engine
// ---------------------------------------------------------------------------

func cellValue(row generated.VaultFindRow, property string) (string, bool) {
	for _, c := range row.Cells {
		if c.Property == property {
			return c.Value, true
		}
	}
	return "", false
}

// mustRat parses a rendered number back into an exact rational, discarding the
// thousands separators the compact-text projection adds. It never touches a
// binary float — FR-013 holds in the test as well as in the code, or the test
// would be checking a rounding of the answer.
func mustRat(t *testing.T, s string) *big.Rat {
	t.Helper()
	clean := strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	r, ok := new(big.Rat).SetString(clean)
	if !ok {
		t.Fatalf("could not read %q as an exact number", s)
	}
	return r
}

func assertTotal(t *testing.T, resp generated.VaultFindResponse, op, property string, want *big.Rat) {
	t.Helper()
	label := op + "()"
	if property != "" {
		label = op + "(" + property + ")"
	}
	for _, total := range resp.Totals {
		if total.Label != label {
			continue
		}
		if total.Refused != nil && *total.Refused {
			t.Fatalf("%s was refused: %s", label, total.Scope)
		}
		if got := mustRat(t, total.Value); got.Cmp(want) != 0 {
			t.Errorf("%s = %s, want %s", label, got, want)
		}
		return
	}
	labels := make([]string, 0, len(resp.Totals))
	for _, total := range resp.Totals {
		labels = append(labels, total.Label)
	}
	t.Errorf("no total labelled %q; got %v", label, labels)
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, g := range got {
		seen[g]++
	}
	for _, w := range want {
		seen[w]--
		if seen[w] < 0 {
			return false
		}
	}
	return true
}
