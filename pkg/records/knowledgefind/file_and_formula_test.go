// Omnipus — the seams the composed test does not reach: the prepass a FORMULA
// alone triggers, backlink scope, and the three-namespace refusal ladder.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// A: THE PREPASS A FORMULA ALONE TRIGGERS
//
// `file.tags` lives in a child table, so it exists in an answer only if the tag
// stream ran. Which prepasses run is decided from the properties the query
// NAMES — and a formula's operands are not among them: the query names
// `formula.flag`, and `file.tags` appears only inside the expression the view
// stored.
//
// So this is the composition's sharpest failure mode. Miss it and the formula
// evaluates against a FileMeta with no tags: `contains(file.tags, "keep")` is
// absent, `if()` reads an absent condition as false (R-14/FR-145), every record
// scores 0, and the total is a confident, plausible, completely wrong ZERO with
// no problem reported anywhere. That is the exact shape of answer this whole
// design exists to remove, and it would have shipped looking correct.
// ---------------------------------------------------------------------------

// tagCorpus. The oracle: FOUR notes carry the tag `keep`.
var tagCorpus = []struct {
	path string
	tags []string
}{
	{path: "garden/monstera.md", tags: []string{"indoor", "keep"}},
	{path: "garden/fern.md", tags: []string{"indoor"}},
	{path: "garden/spade.md", tags: []string{"shed", "keep"}},
	{path: "garden/rake.md", tags: []string{"shed"}},
	{path: "garden/notes.md", tags: []string{"keep"}},
	{path: "shed/hose.md", tags: []string{"keep"}},
}

const wantKeepCount = 4 // monstera, spade, notes, hose — counted by hand, above.

func TestFormulaOverAChildTableProperty_RunsThePrepassNothingElseAskedFor(t *testing.T) {
	store, text := plainIndex(t, func(path string) string {
		for _, n := range tagCorpus {
			if n.path == path {
				return frontmatterNote("", n.tags, nil)
			}
		}
		t.Fatalf("no corpus entry for %s", path)
		return ""
	}, pathsOfCorpus())

	flag := FormulaNamespace + "flag"
	views := composedViews{
		formulas: map[string]string{"flag": `if(contains(file.tags, "keep"), 1, 0)`},
	}

	// THE REQUEST NAMES NO FILE PROPERTY AT ALL. Only the formula does.
	req := generated.VaultFindRequest{
		View: viewName(composedView),
		Aggregate: &[]generated.VaultFindAggregate{
			{Op: generated.VaultFindAggregateOp(opSum), Property: &flag},
		},
	}

	resp, err := Find(context.Background(), Deps{
		Schemas: records.NewSchemaSet(), Store: store, Text: text, Views: views, Epoch: 1,
	}, req)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	assertTotal(t, resp, opSum, flag, big.NewRat(wantKeepCount, 1))
}

func pathsOfCorpus() []string {
	out := make([]string, 0, len(tagCorpus))
	for _, n := range tagCorpus {
		out = append(out, n.path)
	}
	return out
}

// ---------------------------------------------------------------------------
// B: BACKLINK SCOPE IS THE CALLER'S WORKSPACE, NOT THE VIEW'S FILTER (FR-132)
//
// Obsidian's backlinks are vault-wide. A note's references do not depend on
// what the reader happened to be filtering for, so deriving them under the
// view's own Selector would answer "which PLANTS link here" while rendering a
// column labelled "what references this" — a wrong answer that looks like a
// short one.
// ---------------------------------------------------------------------------

func TestBacklinks_AreDerivedOverTheWorkspaceNotTheViewsNarrowing(t *testing.T) {
	const target = "garden/monstera.md"
	sources := map[string]string{
		// A plant, which the view's own narrowing WOULD have kept.
		"garden/fern.md": "plant",
		// A tool, which the view's narrowing would have excluded — and which
		// must appear in the backlinks all the same.
		"shed/spade.md": "tool",
	}
	paths := []string{target, "garden/fern.md", "shed/spade.md"}

	store, text := plainIndex(t, func(path string) string {
		if path == target {
			return frontmatterNote("plant", nil, nil)
		}
		return frontmatterNote(sources[path], nil, []string{target})
	}, paths)

	backlinks := records.FileBacklinksProp
	typeName := "plant"
	req := generated.VaultFindRequest{
		Type:   &typeName,
		Select: &[]string{backlinks},
	}

	resp, err := Find(context.Background(), Deps{
		Schemas: composedSchemas(t), Store: store, Text: text, Epoch: 1,
	}, req)
	if err != nil {
		t.Fatalf("refused: %v", err)
	}

	var cell string
	found := false
	for _, row := range resp.Rows {
		if row.Path != target {
			continue
		}
		cell, found = cellValue(row, backlinks)
	}
	if !found {
		paths := make([]string, 0, len(resp.Rows))
		for _, r := range resp.Rows {
			paths = append(paths, r.Path)
		}
		t.Fatalf("no %s cell on %s; rows were %v", backlinks, target, paths)
	}

	for _, want := range []string{"garden/fern.md", "shed/spade.md"} {
		if !strings.Contains(cell, want) {
			t.Errorf("%s = %q, missing %q — the derivation was narrowed by the view (type=plant) "+
				"instead of by the caller's workspace scope (FR-132)", backlinks, cell, want)
		}
	}
}

// ---------------------------------------------------------------------------
// C: THE REFUSAL LADDER — every position, every namespace
//
// FR-024's posture is "reject, and say what would have worked". Each case below
// is a name a caller could plausibly write, and each must be REFUSED with the
// alternatives attached rather than answered with an empty result — which is
// the failure mode the whole tool is written against, and which every one of
// these would otherwise produce.
// ---------------------------------------------------------------------------

func TestNamespaceRefusals_NameWhatWouldHaveWorked(t *testing.T) {
	store, text := plainIndex(t, func(string) string { return frontmatterNote("plant", nil, nil) },
		[]string{"garden/monstera.md"})

	eq := generated.VaultFilterNodeOp("=")
	value := "x"
	leaf := func(property string) *generated.VaultFilterNode {
		p := property
		return &generated.VaultFilterNode{Property: &p, Op: &eq, Value: &value}
	}
	typeName := "plant"

	cases := []struct {
		name     string
		req      generated.VaultFindRequest
		formulas map[string]string
		wantIn   []string
	}{
		{
			name:   "a misspelled file property lists the twelve",
			req:    generated.VaultFindRequest{Type: &typeName, Filter: leaf("file.mtimes")},
			wantIn: []string{"file.mtimes", "file.mtime", "file.tags"},
		},
		{
			name:   "file.file is refused by name, not reported as unknown",
			req:    generated.VaultFindRequest{Type: &typeName, Filter: leaf(records.FileSelfProp)},
			wantIn: []string{"file.file", "display and formula operand only"},
		},
		// "a typed property with no record type" IS NO LONGER A REFUSAL, and the
		// case was deleted rather than weakened. FR-018b/FR-021e and the
		// ViewDef contract both say an untyped query resolves an ordinary name
		// BY NAME over the rows the index holds for every note — so
		// `{filter: species = x}` with no `type` now resolves in plant's own
		// text domain and runs. The one refusal that survives in the untyped
		// namespace is the SPLIT DOMAIN — two in-scope types declaring one name
		// differently — and it is exercised in
		// TestUntypedQuery_RefusesTwoConflictingDeclarations, which needs a
		// second fixture schema this ladder's corpus does not carry.
		{
			name:     "a formula the view does not define lists the ones it does",
			req:      generated.VaultFindRequest{View: viewName(composedView), Filter: leaf("formula.aeg")},
			formulas: map[string]string{"age": "file.size"},
			wantIn:   []string{"formula.aeg", "formula.age"},
		},
		{
			name:   "a formula with no view at all says where formulas come from",
			req:    generated.VaultFindRequest{Type: &typeName, Filter: leaf("formula.age")},
			wantIn: []string{"formula.age", "saved view"},
		},
		{
			name:     "a presentation formula is refused for comparison (R-16/FR-147)",
			req:      generated.VaultFindRequest{View: viewName(composedView), Filter: leaf("formula.badge")},
			formulas: map[string]string{"badge": `link("Acme")`},
			wantIn:   []string{"formula.badge", "presentation"},
		},
		{
			name: "a file property in SORT is refused the same way as in a filter",
			req: generated.VaultFindRequest{
				Type: &typeName,
				Sort: &[]generated.VaultFindSort{{Property: "file.modified"}},
			},
			wantIn: []string{"file.modified", "file.mtime"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Deps{Schemas: composedSchemas(t), Store: store, Text: text, Epoch: 1}
			if tc.req.View != nil {
				d.Views = composedViews{formulas: tc.formulas}
			}
			resp, err := Find(context.Background(), d, tc.req)
			if err == nil {
				t.Fatalf("the query was ANSWERED (%d rows), not refused — an empty or plausible answer to a "+
					"name that does not resolve is the exact failure FR-024 exists to end", len(resp.Rows))
			}
			if !resp.Refused {
				t.Errorf("the response is not marked refused, but an error was returned; the two halves must agree")
			}
			joined := err.Error()
			for _, p := range resp.Problems {
				joined += " | " + p.Reason
				if p.Permitted != nil {
					joined += " | " + strings.Join(*p.Permitted, ", ")
				}
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(joined, want) {
					t.Errorf("the refusal never mentions %q, so the caller cannot act on it:\n%s", want, joined)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Fixture helpers, shared by the three above
// ---------------------------------------------------------------------------

// plainIndex builds a live index over `paths`, taking each note's source from
// `source`. Stat metadata is written for every note, because a fixture with no
// stat would make `file.size` absent and every assertion about it vacuous.
func plainIndex(t *testing.T, source func(path string) string, paths []string) (propindex.Store, *stubText) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), dbPath, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	set := composedSchemas(t)
	text := &stubText{hits: map[string]TextHit{}}
	for _, p := range paths {
		b := []byte(source(p))
		rec := records.ParseRecord(p, b)
		schema, _ := set.Get(rec.TypeName())
		rows := propindex.BuildNoteRows(rec, schema, b, propindex.SourceHash(b))
		rows.Size = int64(len(b))
		rows.MtimeNanos = 1
		if err := store.UpsertNote(context.Background(), rows); err != nil {
			t.Fatalf("UpsertNote(%s): %v", p, err)
		}
		text.hits[p] = TextHit{Path: p, SourceHash: rows.SourceHash, Score: 1}
	}
	return store, text
}

func frontmatterNote(typeName string, tags, links []string) string {
	var b strings.Builder
	b.WriteString("---\n")
	if typeName != "" {
		b.WriteString("type: " + typeName + "\n")
	}
	if len(tags) > 0 {
		b.WriteString("tags: [" + strings.Join(tags, ", ") + "]\n")
	}
	b.WriteString("---\n\n# A note\n")
	for _, l := range links {
		b.WriteString("\nSee [[" + l + "]].\n")
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// D: THE SEAM, CLOSED
//
// A test called TestSeam_ProductionViewLoaderStillCannotCarryViewFormulas stood
// here. It asserted that records.ViewFindLoader did NOT implement
// ViewFormulaLoader, and it was written to FAIL the day that stopped being
// true, with instructions to delete it. That day is this one: the loader now
// has a Formulas method and translateViewQuery's ServeRefusalFormula branch is
// gone, so the test has been deleted as it asked to be.
//
// WHAT REPLACES IT IS STRONGER THAN WHAT IT ASSERTED. The old test proved a
// negative about an interface. The two below prove the positive about
// BEHAVIOUR — that the production loader carries a real view's formulas all the
// way to a served answer, and that the answer moves when the clock does.
// ---------------------------------------------------------------------------

// The production loader must satisfy the base interface AND the formula one.
// Asserted at compile time so a method removed by a later refactor is a build
// failure here rather than a silently formula-less vault.
var (
	_ ViewLoader        = (*records.ViewFindLoader)(nil)
	_ ViewFormulaLoader = (*records.ViewFindLoader)(nil)
)

// ---------------------------------------------------------------------------
// E: THE WHOLE PATH, THROUGH THE PRODUCTION LOADER, AGAINST TWO CLOCKS
//
// Everything in A–C proves the formula ENGINE works through a loader written
// for the test. These two prove the PRODUCT works: a view file on disk, read by
// records.LoadViews, wrapped in records.NewViewFindLoader — the same
// constructor pkg/vaultprops/find_tool.go wires into the real knowledge_find —
// and served.
//
// WHY THAT DISTINCTION IS THE POINT OF THIS SECTION. Until the seam closed,
// every formula test in this file passed while the capability was unreachable:
// the bridge refused any view declaring `formulas` outright, so a real vault's
// formula view was dropped from Names(), reported unknown by the `view`
// argument, and never reached this package at all. A test that cannot tell a
// working feature from an unreachable one is not evidence, and the file said so
// itself for as long as the gap was open.
// ---------------------------------------------------------------------------

// deadlineVault writes a schema, three notes and one saved view to a real
// vault root, and returns the pieces knowledge_find needs.
//
// EVERY ARTEFACT GOES THROUGH THE PRODUCTION READER. The schema through
// records.LoadSchemas, the notes through records.ParseRecord +
// propindex.BuildNoteRows, the view through records.LoadViews, and the loader
// through records.NewViewFindLoader. Nothing here is hand-assembled, because a
// hand-assembled view would skip exactly the validation (ValidateViewAgainstSchemas,
// validateViewFormulas) that a real imported file has to survive.
func deadlineVault(t *testing.T) (Deps, *records.ViewFindLoader) {
	t.Helper()
	root := t.TempDir()

	schemaDir := records.SchemaDir(root)
	if err := os.MkdirAll(schemaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(schemas): %v", err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "deadline.yaml"), []byte(`schema_version: 1
type: deadline
properties:
  title: { type: text }
  due:   { type: date }
`), 0o600); err != nil {
		t.Fatalf("WriteFile(schema): %v", err)
	}
	set, schemaReport, schemaErr := records.LoadSchemas(root)
	if schemaErr != nil {
		t.Fatalf("LoadSchemas: %v", schemaErr)
	}
	if !schemaReport.OK() {
		t.Fatalf("the fixture schema was rejected: %v", schemaReport.Rejections)
	}

	// The view is written in the shape pkg/vaultimport now produces for
	// Tasks.base's "Due <=7d": one formula over a date, referenced from the
	// filter AND shown as a column.
	viewDir := records.ViewsDir(root)
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(views): %v", err)
	}
	if err := os.WriteFile(filepath.Join(viewDir, "due-soon.yaml"), []byte(`name: due-soon
type: deadline
label: Due within a week
formulas:
  days_until_due: (date(due) - today()).days
filter:
  property: formula.days_until_due
  op: "<="
  value: "7"
properties:
  - title
  - due
  - formula.days_until_due
source: 06-Bases/Tasks.base
`), 0o600); err != nil {
		t.Fatalf("WriteFile(view): %v", err)
	}
	views, viewReport, err := records.LoadViews(root, set)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !viewReport.OK() {
		t.Fatalf("the fixture view was rejected by the LOADER, so nothing below tests serving: %v", viewReport.Rejections)
	}

	dbPath := filepath.Join(t.TempDir(), "properties.db")
	store, err := propindex.Open(context.Background(), dbPath, propindex.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	text := &stubText{hits: map[string]TextHit{}}
	for path, due := range map[string]string{
		"work/soon.md":  "2026-09-02",
		"work/later.md": "2026-09-20",
		"work/none.md":  "",
	} {
		src := "---\ntype: deadline\ntitle: " + filepath.Base(path) + "\n"
		if due != "" {
			src += "due: " + due + "\n"
		}
		src += "---\n\nbody\n"
		b := []byte(src)
		rec := records.ParseRecord(path, b)
		schema, _ := set.Get(rec.TypeName())
		rows := propindex.BuildNoteRows(rec, schema, b, propindex.SourceHash(b))
		rows.Size = int64(len(b))
		rows.MtimeNanos = 1
		if err := store.UpsertNote(context.Background(), rows); err != nil {
			t.Fatalf("UpsertNote(%s): %v", path, err)
		}
		text.hits[path] = TextHit{Path: path, SourceHash: rows.SourceHash, Score: 1}
	}

	loader := records.NewViewFindLoader(views)
	return Deps{Schemas: set, Store: store, Text: text, Views: loader, Epoch: 8814}, loader
}

// TestProductionLoader_ServesASavedViewsFormula is the seam closure's
// behavioural proof: the view is SERVABLE, it is LISTED, and the formula's
// value comes back in the row.
func TestProductionLoader_ServesASavedViewsFormula(t *testing.T) {
	deps, loader := deadlineVault(t)

	// 1 — the view is servable at all. Before the seam closed, every one of
	// these three assertions failed: Names() omitted the view, ServeRefusal
	// reported formula_not_representable, and View() answered ok=false.
	if names := loader.Names(); len(names) != 1 || names[0] != "due-soon" {
		t.Fatalf("Names() = %v, want exactly [due-soon] — a view declaring formulas must be servable", names)
	}
	if refusal, unservable := loader.ServeRefusal("due-soon"); unservable {
		t.Fatalf("ServeRefusal(due-soon) = %+v, want servable", refusal)
	}
	sources, ok := loader.Formulas("due-soon")
	if !ok || sources["days_until_due"] != "(date(due) - today()).days" {
		t.Fatalf("Formulas(due-soon) = %v, ok=%v — the loader must hand the SOURCE to knowledge_find", sources, ok)
	}

	// 2 — served, with the formula deciding the rows. On 2026-08-31 only
	// work/soon.md (due 2026-09-02, two days out) is within seven days;
	// work/later.md is nineteen out and work/none.md has no due date at all,
	// so its formula is ABSENT and R-2 makes the comparison false.
	deps.Now = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	viewName := "due-soon"
	resp := mustFind(t, deps, generated.VaultFindRequest{View: &viewName})
	if got := rowIDs(resp); len(got) != 1 || got[0] != "work/soon.md" {
		t.Fatalf("rows = %v, want exactly [work/soon.md]", got)
	}

	// 3 — the formula's VALUE is in the row, not merely used and discarded.
	var cell string
	var found bool
	for _, c := range resp.Rows[0].Cells {
		if c.Property == "formula.days_until_due" {
			cell, found = c.Value, true
		}
	}
	if !found {
		t.Fatalf("no `formula.days_until_due` cell in the row; cells = %+v", resp.Rows[0].Cells)
	}
	// FR-144's default scale: a formula number that no `round`/`toFixed`
	// declared crosses the boundary at ten decimal places, so the rendering is
	// `2.0000000000` and not `2`. Asserted EXACTLY rather than as a prefix —
	// the scale is part of the contract, and a prefix check would go on passing
	// if the value became 2.5 (a `.days` truncated to a component instead of
	// the documented total) or 20.
	if cell != "2.0000000000" {
		t.Errorf("formula.days_until_due = %q, want %q — 2026-09-02 is exactly two days after 2026-08-31, rendered at FR-144's default scale", cell, "2.0000000000")
	}
}

// TestSavedViewFormula_IsEvaluatedAgainstTheQuERYClock is the requirement the
// whole feature turns on, and it is the one a passing import cannot establish.
//
// A formula is written ONCE, at import, and evaluated EVERY TIME the view runs.
// `today()` in a saved view means "today when the view runs". If it ever meant
// "the day the vault was imported", the founder's "due in the next 7 days"
// would be frozen to a date in the past — and it would look completely normal,
// because a frozen view still returns rows, still renders, and still says
// nothing is wrong. There is no error to notice.
//
// THE ORACLE IS A CHANGE OF ANSWER, not a passing call. The same view, the same
// three notes, the same index, two clocks a fortnight apart, and the row set
// must MOVE. A single-clock test would pass identically against an evaluator
// that had baked the date in at import.
func TestSavedViewFormula_IsEvaluatedAgainstTheQueryClock(t *testing.T) {
	deps, _ := deadlineVault(t)
	viewName := "due-soon"

	// On 2026-08-31: soon.md is 2 days out (in), later.md is 20 days out (out).
	deps.Now = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	early := rowIDs(mustFind(t, deps, generated.VaultFindRequest{View: &viewName}))

	// On 2026-09-15: soon.md is 13 days PAST (still <= 7, in) and later.md is
	// 5 days out (now in). The row set has to grow by exactly one.
	deps.Now = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	late := rowIDs(mustFind(t, deps, generated.VaultFindRequest{View: &viewName}))

	if len(early) != 1 || early[0] != "work/soon.md" {
		t.Fatalf("at 2026-08-31 rows = %v, want [work/soon.md]", early)
	}
	sort.Strings(late)
	if len(late) != 2 || late[0] != "work/later.md" || late[1] != "work/soon.md" {
		t.Fatalf("at 2026-09-15 rows = %v, want [work/later.md work/soon.md]", late)
	}
	if strings.Join(early, ",") == strings.Join(late, ",") {
		t.Fatal("the same saved view returned the SAME rows against two clocks a fortnight apart.\n" +
			"`today()` is being resolved once and reused, so every date-relative view in the vault is\n" +
			"frozen to whatever day it was first evaluated — a wrong answer that looks entirely normal.")
	}

	// work/none.md must be absent from BOTH answers. Its `due` is absent, so
	// the formula is absent (R-14) and R-2 makes `absent <= 7` FALSE. A record
	// appearing here would mean absence had been coerced to a number, which is
	// the broadening direction.
	for _, got := range [][]string{early, late} {
		for _, id := range got {
			if id == "work/none.md" {
				t.Errorf("a record with no `due` matched `formula.days_until_due <= 7`; absence must not compare, it must be false (R-2/R-14). rows = %v", got)
			}
		}
	}
}

// TestServeRefusalFormula_IsNeverEmitted pins the retired refusal code.
//
// The constant is kept for readers (see its doc comment); what must not come
// back is a code path that PRODUCES it. This walks a real formula-bearing view
// through the same Unservable() report an operator reads.
func TestServeRefusalFormula_IsNeverEmitted(t *testing.T) {
	_, loader := deadlineVault(t)
	for _, r := range loader.Unservable() {
		if r.Code == records.ServeRefusalFormula {
			t.Fatalf("a view was refused as %s: %s\nThe formulas travel beside the request through the loader; refusing the view makes the importer's whole formula capability unreachable.", r.Code, r.Reason)
		}
	}
}
