// Omnipus — the two checks this importer makes on the engine's behalf, graded
// against the engine itself.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultimport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// THE IMPORTER MUST REFUSE WHAT THE ENGINE REFUSES
//
// Two of the three defects fixed alongside W4 are instances of one failure: a
// view was written ENABLED carrying a clause knowledge_find will not serve, so
// it imported clean and died the first time anybody opened it. A named loss is
// worse than nothing only if the loss is wrong; a view that cannot be served is
// worse than either.
//
// The fix for each is a check made at import time, and a check is only worth
// making if it gives the SAME answer the engine gives. Where the engine's own
// function is exported, this importer calls it and there is nothing to grade —
// relationLiteral calls records.ParseValue, which is literally the function
// Filter.Validate runs a literal through. Where it is not, the rule is
// restated and the restatement is put to a real knowledgefind.Find here, pair
// by pair. A disagreement in either direction is a failure: refusing what the
// engine serves loses a working view, and serving what the engine refuses is
// the defect this all started as.
// ---------------------------------------------------------------------------

// parityText is a text index that agrees with the properties index. It decides
// no row; it exists because Deps.Text is required.
type parityText struct{ hashes map[string]string }

func (s *parityText) Search(context.Context, string, int) ([]knowledgefind.TextHit, error) {
	return nil, nil
}

func (s *parityText) NearestTerms(context.Context, string, int) ([]generated.VaultTermCount, error) {
	return nil, nil
}

// Populated satisfies the interface's freshness contract for a stub that
// stands in for a BUILT index: these parity tests exercise search behaviour,
// not the unbuilt-index refusal, so the stub reports one completed build pass.
func (s *parityText) Populated(context.Context) (bool, error) { return true, nil }

func (s *parityText) SourceHash(_ context.Context, path string) (string, bool, error) {
	h, ok := s.hashes[path]
	return h, ok, nil
}

// parityVault writes two record types that declare the SAME property name, and
// returns both a live knowledge_find Deps over them and the SchemaIndex this
// importer would have built for the same vault.
func parityVault(t *testing.T, declA, declB string) (knowledgefind.Deps, *SchemaIndex) {
	t.Helper()
	root := t.TempDir()
	dir := records.SchemaDir(root)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, sc := range []struct{ typeName, decl string }{{"alpha", declA}, {"beta", declB}} {
		src := fmt.Sprintf("schema_version: 1\ntype: %s\nproperties:\n  p: %s\n", sc.typeName, sc.decl)
		if err := os.WriteFile(filepath.Join(dir, sc.typeName+".yaml"), []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	schemas, report, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !report.OK() {
		t.Fatalf("the fixture schemas were rejected: %v", report.Rejections)
	}

	store, err := propindex.Open(context.Background(), filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})
	text := &parityText{hashes: map[string]string{}}
	// One note of each type, so the query has a population to refuse over —
	// an empty index would answer nothing for a reason that is not the rule.
	for _, n := range []struct{ path, src string }{
		{"a.md", "---\ntype: alpha\n---\n"},
		{"b.md", "---\ntype: beta\n---\n"},
	} {
		data := []byte(n.src)
		rec := records.ParseRecord(n.path, data)
		sc, _ := schemas.Get(rec.TypeName())
		hash := propindex.SourceHash(data)
		if err = store.UpsertNote(context.Background(), propindex.BuildNoteRows(rec, sc, data, hash)); err != nil {
			t.Fatalf("UpsertNote: %v", err)
		}
		text.hashes[n.path] = hash
	}

	// The SchemaIndex this importer's own inference would have produced for the
	// same two declarations, read back OUT of the loaded schemas so the two
	// halves of the comparison cannot be spelled differently by hand.
	inferred := map[string][]InferredProperty{}
	for _, typeName := range schemas.Types() {
		sc, _ := schemas.Get(typeName)
		p, _ := sc.Property("p")
		inferred[typeName] = []InferredProperty{{Name: "p", Type: p.Type, Many: p.Many, To: p.To}}
	}

	return knowledgefind.Deps{
		Schemas: schemas,
		Store:   store,
		Text:    text,
		Epoch:   1,
	}, NewSchemaIndex(inferred)
}

// engineRefusesUntypedName reports whether a real, untyped knowledge_find
// refuses a filter naming `p`, and the reason it gave.
func engineRefusesUntypedName(t *testing.T, deps knowledgefind.Deps) (bool, string) {
	t.Helper()
	property := "p"
	// `IS NOT NULL` carries no value (FR-022d), so the leaf is built without
	// one — a leaf the engine refuses for its SHAPE would refuse every pair
	// alike and this comparison would measure nothing.
	op := generated.ISNOTNULL
	limit := 50
	resp, err := knowledgefind.Find(context.Background(), deps, generated.VaultFindRequest{
		Limit:  &limit,
		Filter: &generated.VaultFilterNode{Property: &property, Op: &op},
	})
	if err == nil && !resp.Refused {
		return false, ""
	}
	var parts []string
	for _, p := range resp.Problems {
		parts = append(parts, p.Reason)
	}
	if len(parts) == 0 && err != nil {
		parts = append(parts, err.Error())
	}
	return true, strings.Join(parts, "; ")
}

// TestUntypedSplitDomain_PredictsFindExactly is the grade on the restatement.
//
// Every pair below is put to BOTH surfaces: this importer's untypedSplitDomain,
// and a real Find over a vault declaring exactly that pair. They must agree on
// every one. Nothing here reads sameInferredDomain — the expectations are §8's
// own list of what decides a comparison's rule (type, arity, link target), and
// the engine's answer is observed rather than assumed.
func TestUntypedSplitDomain_PredictsFindExactly(t *testing.T) {
	for _, tc := range []struct {
		name, declA, declB string
		why                string
	}{
		{name: "identical text", declA: "{ type: text }", declB: "{ type: text }", why: "one domain"},
		{name: "text against enum", declA: "{ type: text }", declB: "{ type: enum, values: [a, b] }", why: "different declared type — this is the founder's `status`"},
		{name: "two enums, different sets", declA: "{ type: enum, values: [a, b] }", declB: "{ type: enum, values: [c, d] }", why: "one domain: the engine UNIONS the sets, a value set is not a domain"},
		{name: "text against many text", declA: "{ type: text }", declB: "{ type: text, many: true }", why: "different arity — R-9 is element-wise, R-13 refuses ordering"},
		{name: "integer against decimal", declA: "{ type: integer }", declB: "{ type: decimal }", why: "R-1 makes them ONE domain for comparison, but they are two DECLARED types"},
		{name: "relation to the same target", declA: "{ type: relation, to: alpha }", declB: "{ type: relation, to: alpha }", why: "one domain"},
		{name: "relation to different targets", declA: "{ type: relation, to: alpha }", declB: "{ type: relation, to: beta }", why: "R-8 joins on identity in the TARGET type"},
		{name: "date against checkbox", declA: "{ type: date }", declB: "{ type: checkbox }", why: "different declared type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps, index := parityVault(t, tc.declA, tc.declB)
			resolver := leafResolver{schemas: index}

			reason, importerSplit := resolver.untypedSplitDomain("p")
			engineRefused, engineReason := engineRefusesUntypedName(t, deps)

			if importerSplit != engineRefused {
				t.Fatalf("DISAGREEMENT on %s vs %s (%s): the importer says split=%v, knowledge_find says refused=%v (%s)",
					tc.declA, tc.declB, tc.why, importerSplit, engineRefused, engineReason)
			}
			if !importerSplit {
				return
			}
			// The refusal must be THIS one, not some other refusal that
			// happens to coincide — a check that agrees for the wrong reason
			// agrees by luck.
			if !strings.Contains(engineReason, "will not split one name across two domains") {
				t.Fatalf("the engine refused for a different reason than the split domain, so this pair proves nothing: %s", engineReason)
			}
			if !strings.Contains(reason, "alpha") || !strings.Contains(reason, "beta") {
				t.Errorf("the importer's loss does not name both record types, which is what an operator needs to act on it: %s", reason)
			}
		})
	}
}

// TestUntypedSplitDomain_AppliesToEveryPositionTheEngineResolves.
//
// resolveUntyped takes the POSITION as an argument and refuses from all of
// them, so a split name is fatal in a grouping, a sort, a select or an
// aggregate exactly as it is in a filter — and the refusal aborts the WHOLE
// request, not the one column. checkProperty is where this importer guards
// those four, and a mutation that removed its untyped branch survived the whole
// suite until this test existed.
func TestUntypedSplitDomain_AppliesToEveryPositionTheEngineResolves(t *testing.T) {
	deps, index := parityVault(t, "{ type: text }", "{ type: enum, values: [a, b] }")
	untyped := leafResolver{schemas: index}

	// The engine first: a GROUPING on the split name, with no filter naming it
	// at all, so nothing but the grouping can be the reason.
	limit := 50
	resp, err := knowledgefind.Find(context.Background(), deps, generated.VaultFindRequest{
		Limit:   &limit,
		GroupBy: &[]generated.VaultFindGroupBy{{Property: "p"}},
	})
	engineRefused := err != nil || resp.Refused
	reasons := make([]string, 0, len(resp.Problems))
	for _, pb := range resp.Problems {
		reasons = append(reasons, pb.Reason)
	}
	if !engineRefused {
		t.Fatalf("knowledge_find ANSWERED an untyped group_by on a split name, so this importer must not refuse it either — the check in checkProperty is wrong: %+v", resp.Problems)
	}
	if !strings.Contains(strings.Join(reasons, "; "), "will not split one name across two domains") {
		t.Fatalf("the engine refused the grouping for a different reason, so this proves nothing: %v", reasons)
	}

	// And now the importer, in both the comparison and the display readings of
	// the same name.
	for _, comparison := range []bool{true, false} {
		reason, ok := untyped.checkProperty("p", comparison)
		if ok {
			t.Errorf("checkProperty(%q, comparison=%v) allowed a name knowledge_find refuses", "p", comparison)
			continue
		}
		if !strings.Contains(reason, "alpha") || !strings.Contains(reason, "beta") {
			t.Errorf("the loss does not name both record types: %s", reason)
		}
	}

	// The control: a name only ONE type declares is untouched, so the check is
	// not simply refusing every untyped name.
	if reason, ok := untyped.checkProperty("file.name", true); !ok {
		t.Errorf("a reserved file property was refused by the split-domain check: %s", reason)
	}
}

// TestUntypedSplitDomain_IsNotAppliedToATypedView. The engine's rule is
// resolveUntyped's, and it exists BECAUSE there is no type to pick a domain
// with. A typed view has one, so applying the check there would refuse views
// that serve perfectly.
func TestUntypedSplitDomain_IsNotAppliedToATypedView(t *testing.T) {
	_, index := parityVault(t, "{ type: text }", "{ type: enum, values: [a, b] }")

	if _, split := (leafResolver{schemas: index}).untypedSplitDomain("p"); !split {
		t.Fatal("the fixture pair is not a split domain, so the negative below proves nothing")
	}
	typed := leafResolver{recordType: "alpha", schemas: index}
	if reason, ok := typed.checkProperty("p", true); !ok {
		t.Errorf("a TYPED view was refused a property its own record type declares: %s", reason)
	}
}

// TestRelationLiteral_MatchesFilterValidateExactly.
//
// relationLiteral asks records.ParseValue — the function Filter.Validate itself
// runs a literal through — so the two cannot disagree by construction. This
// asserts the construction, because "it calls the same function" is an
// implementation detail a refactor can quietly end, and the consequence of
// ending it is a view that imports clean and cannot be served.
func TestRelationLiteral_MatchesFilterValidateExactly(t *testing.T) {
	schema, rejection := records.ParseSchema("task.yaml", []byte(`schema_version: 1
type: task
properties:
  owner:  { type: relation, to: person }
  lead:   { type: person }
  labels: { type: text, many: true }
`))
	if rejection != nil {
		t.Fatalf("the fixture schema was rejected: %+v", rejection)
	}
	r := leafResolver{recordType: "task", schema: schema}

	for _, tc := range []struct {
		property, literal string
		want              bool
		why               string
	}{
		{property: "owner", literal: "[[Daniel Piatkowski]]", want: true, why: "a wikilink is what a relation holds"},
		{property: "owner", literal: "[[People/Daniel|Danny]]", want: true, why: "an aliased wikilink is still a wikilink; R-8 joins on the target"},
		{property: "owner", literal: "Daniel Piatkowski", want: false, why: "THE FOUNDER'S CLAUSE — a bare name is not a wikilink and the engine refuses it"},
		{property: "owner", literal: "", want: false, why: "ParseWikilink refuses the empty string"},
		{property: "lead", literal: "[[Daniel Piatkowski]]", want: true, why: "`person` holds a wikilink too"},
		{property: "lead", literal: "Daniel Piatkowski", want: false, why: "and refuses a bare name for the same reason"},
	} {
		t.Run(tc.property+"="+tc.literal, func(t *testing.T) {
			value, reason, ok := r.relationLiteral(tc.property, tc.literal)
			if ok != tc.want {
				t.Fatalf("relationLiteral(%q, %q) ok = %v, want %v — %s (reason: %s)", tc.property, tc.literal, ok, tc.want, tc.why, reason)
			}

			// THE GRADE: whatever this importer would WRITE has to be a literal
			// Filter.Validate accepts, and whatever it refuses has to be one
			// Filter.Validate refuses. That is the only property that matters.
			literal := tc.literal
			if ok {
				literal = value
			}
			f := records.Filter{Property: tc.property, Op: records.OpEqual, Literal: literal, LiteralGiven: true}
			_, _, verr := f.Validate(schema)
			if ok && verr != nil {
				t.Errorf("the importer would have WRITTEN %q, and Filter.Validate refuses it: %v", literal, verr)
			}
			if !ok && verr == nil {
				t.Errorf("the importer refused %q, but Filter.Validate accepts it — a working clause was thrown away", literal)
			}
			if !ok && !strings.Contains(reason, "not a wikilink") {
				t.Errorf("the loss does not quote the engine's own reason: %s", reason)
			}
		})
	}
}

// TestRelationLiteral_TheWikilinkFormIsWrittenWhole, which is the latent half
// of the same defect.
//
// This branch used to strip the brackets and write the bare TARGET. No `.base`
// file in the founder's vault carries a wikilink literal, so nothing exercised
// it — and the value it wrote is refused by exactly the same ParseValue that
// refuses a bare name. It is asserted here rather than left as untested,
// silently-wrong code that the next vault would hit.
func TestRelationLiteral_TheWikilinkFormIsWrittenWhole(t *testing.T) {
	schema, rejection := records.ParseSchema("task.yaml", []byte(`schema_version: 1
type: task
properties:
  owner: { type: relation, to: person }
`))
	if rejection != nil {
		t.Fatalf("the fixture schema was rejected: %+v", rejection)
	}
	r := leafResolver{recordType: "task", schema: schema}

	value, _, ok := r.relationLiteral("owner", "[[Daniel Piatkowski]]")
	if !ok {
		t.Fatal("a wikilink literal was refused")
	}
	if value != "[[Daniel Piatkowski]]" {
		t.Errorf("relationLiteral wrote %q — the brackets are what makes it a relation value to the engine; the bare target is refused by ParseValue exactly like a bare name", value)
	}
}

// ---------------------------------------------------------------------------
// W4 AT THE TREE LEVEL
//
// resolveTree's header states the rule this file has to satisfy: `not:` is
// where a narrowing becomes a broadening, and knowledge_find negates a
// combinator as a bare `!inner.matched` with no absence rule of its own. So a
// rewrite is only safe at depth if it produces the SAME VALUE, not a subset.
//
// W4 produces the same value — that is the whole proof — and the one way it
// could fail is by producing ABSENCE where Obsidian's `false` else-branch
// produced a value. Absence and false are indistinguishable through `f == true`
// and distinguishable through `f == false`: absence compares false to
// everything (R-2), so an absent formula would fall into NEITHER set.
//
// The test below therefore does not inspect the formula at all. It imports a
// vault, serves three views through knowledge_find, and checks that the
// `== true` and `== false` row sets PARTITION the population. A W4 that
// returned absence would leave a hole in that partition, and the same hole
// would show up as extra rows under the `not:`.
// ---------------------------------------------------------------------------

const w4TreeBase = `filters:
  and:
    - type == "thing"
formulas:
  is_overdue: if(due, date(due) < today() && status != "done", false)
views:
  - type: table
    name: Positive
    filters:
      and:
        - formula.is_overdue == true
    order: [file.name]
  - type: table
    name: Negated
    filters:
      and:
        - not:
            - formula.is_overdue == true
    order: [file.name]
  - type: table
    name: IsFalse
    filters:
      and:
        - formula.is_overdue == false
    order: [file.name]
`

// w4TreeNotes covers every state W4's identity has to hold on: a past due date
// with and without a status, a future one, and — the rows the whole proof is
// about — notes with NO `due` at all, including one whose `due` is the empty
// string (FR-007a makes that absence on a date).
var w4TreeNotes = map[string]string{
	"past-todo.md":     "---\ntype: thing\ndue: 2020-01-01\nstatus: todo\n---\n",
	"past-done.md":     "---\ntype: thing\ndue: 2020-01-01\nstatus: done\n---\n",
	"past-nostatus.md": "---\ntype: thing\ndue: 2020-01-01\n---\n",
	"future-todo.md":   "---\ntype: thing\ndue: 2099-01-01\nstatus: todo\n---\n",
	"nodue-todo.md":    "---\ntype: thing\nstatus: todo\n---\n",
	"nodue-done.md":    "---\ntype: thing\nstatus: done\n---\n",
	"nodue-nothing.md": "---\ntype: thing\n---\n",
	"emptydue-todo.md": "---\ntype: thing\ndue: \"\"\nstatus: todo\n---\n",
}

func TestW4_TheReducedFormulaIsFalseAndNotAbsentUnderANegation(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "06-Bases"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "06-Bases", "Things.base"), []byte(w4TreeBase), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	for name, src := range w4TreeNotes {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.Disabled {
				t.Fatalf("view %q imported DISABLED, so nothing below can be served: %v", v.DisplayName, v.DisablingLosses)
			}
		}
	}

	deps, views := w4TreeDeps(t, root)
	positive := w4TreeRows(t, deps, views, rep, "Positive")
	negated := w4TreeRows(t, deps, views, rep, "Negated")
	isFalse := w4TreeRows(t, deps, views, rep, "IsFalse")

	// THE PARTITION. Every note of the type must land in exactly one of
	// `== true` and `== false`. A note in NEITHER is a formula that evaluated
	// to ABSENCE — the one way W4 could be wrong — and it is precisely the note
	// a `not:` would then re-admit.
	all := make([]string, 0, len(w4TreeNotes))
	for name := range w4TreeNotes {
		all = append(all, strings.TrimSuffix(name, ".md"))
	}
	inEither := map[string]bool{}
	for _, r := range positive {
		inEither[r] = true
	}
	for _, r := range isFalse {
		if inEither[r] {
			t.Errorf("%q is in BOTH `== true` and `== false`, which no boolean is", r)
		}
		inEither[r] = true
	}
	for _, name := range all {
		if !inEither[name] {
			t.Errorf("%q is in NEITHER `formula.is_overdue == true` nor `== false`, so the formula is ABSENT there — W4 turned a `false` else-branch into absence, and a `not:` over this clause would re-admit the note", name)
		}
	}

	// AND THE NEGATION ITSELF: `not: [f == true]` must be exactly the
	// complement, over the same population.
	if len(positive)+len(negated) != len(all) {
		t.Errorf("`f == true` returned %d and `not: [f == true]` returned %d over %d notes — a negation that is not a complement is where a narrowing becomes a broadening (positive=%v negated=%v)",
			len(positive), len(negated), len(all), positive, negated)
	}
	for _, r := range positive {
		for _, n := range negated {
			if r == n {
				t.Errorf("%q is in both the clause and its negation", r)
			}
		}
	}

	// The positive set is derived by hand from the base's own semantics, so
	// this is not the engine grading itself: `past-todo` and `past-nostatus`
	// have a due date before the clock and a status that is not `done`.
	// `past-nostatus` is the `!=`-over-absent row — R-2 answers FALSE inside a
	// formula where JavaScript answers true, so ours EXCLUDES it. Fewer rows,
	// the direction FR-105 permits, and named in the report where it is used.
	want := []string{"past-todo"}
	if strings.Join(positive, ",") != strings.Join(want, ",") {
		t.Errorf("`formula.is_overdue == true` = %v, want %v — see the `!=` divergence in translate.go's W4 header for why `past-nostatus` is excluded", positive, want)
	}
}

// w4TreeDeps opens knowledge_find over a vault the importer has just written.
func w4TreeDeps(t *testing.T, root string) (knowledgefind.Deps, *records.ViewSet) {
	t.Helper()
	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("LoadSchemas: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !viewRep.OK() {
		t.Fatalf("the importer wrote views the loader rejects: %v", viewRep.Rejections)
	}
	store, err := propindex.Open(context.Background(), filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})
	text := &parityText{hashes: map[string]string{}}
	for name := range w4TreeNotes {
		data, rerr := os.ReadFile(filepath.Join(root, name)) //nolint:gosec // a temp fixture this test wrote
		if rerr != nil {
			t.Fatalf("ReadFile: %v", rerr)
		}
		rec := records.ParseRecord(name, data)
		sc, _ := schemas.Get(rec.TypeName())
		hash := propindex.SourceHash(data)
		if uerr := store.UpsertNote(context.Background(), propindex.BuildNoteRows(rec, sc, data, hash)); uerr != nil {
			t.Fatalf("UpsertNote: %v", uerr)
		}
		text.hashes[name] = hash
	}
	return knowledgefind.Deps{
		Schemas: schemas,
		Store:   store,
		Text:    text,
		Views:   records.NewViewFindLoader(views),
		Epoch:   1,
		Now:     w4OracleClock,
	}, views
}

// w4TreeRows serves one of the three views by its display name.
func w4TreeRows(t *testing.T, deps knowledgefind.Deps, views *records.ViewSet, rep *Report, display string) []string {
	t.Helper()
	slug := ""
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.DisplayName == display {
				slug = strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			}
		}
	}
	if slug == "" {
		t.Fatalf("the import produced no view named %q", display)
	}
	if _, ok := views.Get(slug); !ok {
		t.Fatalf("the loader did not load %q", slug)
	}
	limit := 200
	resp, err := knowledgefind.Find(context.Background(), deps, generated.VaultFindRequest{View: &slug, Limit: &limit})
	if err != nil {
		t.Fatalf("knowledge_find refused %q: %v", display, err)
	}
	out := make([]string, 0, len(resp.Rows))
	for _, r := range resp.Rows {
		out = append(out, strings.TrimSuffix(filepath.Base(r.Path), ".md"))
	}
	sort.Strings(out)
	return out
}
