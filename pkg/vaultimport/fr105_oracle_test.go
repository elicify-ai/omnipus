// Omnipus — FR-105's fixture-level oracle (spec test 98): imported views
// compared against HAND-DERIVED expected row sets committed as data, never
// against anything the importer produced.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHY THIS TEST IS SHAPED THE WAY IT IS
//
// FR-105 says an imported view must NEVER return more rows than its original.
// A prohibition without an oracle is a hope — and the obvious oracle, "ask
// the importer what the view should return and check that it returns it", is
// the importer grading itself: it would agree with any bug the importer
// happened to have.
//
// So the expectations live in testdata/fr105/expected_rows.yaml, worked out
// by a person from each `.base` file's documented Obsidian semantics, and are
// read here as DATA. Nothing in this file recomputes them, and the fixture
// notes are small enough (six) that a reviewer can redo the arithmetic.
//
// EVALUATION RUNS ON THE PRODUCT'S OWN CODE, NOT THE IMPORTER'S. The written
// view files are re-read by records.LoadViews (the real loader), turned into
// queries by records.NewViewFindLoader (the real view->find bridge), and each
// leaf is decided by records.Filter.Match (the real comparator, with the real
// FR-008 absence rule). The only test-owned code in the loop is the ~30-line
// walk over all/any/not, which carries no comparison logic of its own. So if
// the importer writes the WRONG filter, every product component below will
// faithfully evaluate the wrong filter and the row set will not match.
// ---------------------------------------------------------------------------

// fr105ExpectedView is one hand-derived expectation from expected_rows.yaml.
type fr105ExpectedView struct {
	Enabled bool     `yaml:"enabled"`
	Rows    []string `yaml:"rows"`
	// RowsIfWronglyEnabled is present only on a view expected to be
	// DISABLED: the rows the TRANSLATED filters would have matched had the
	// view shipped enabled. It is what turns "we disabled it" into a
	// measurement of the broadening that disabling prevents.
	RowsIfWronglyEnabled []string `yaml:"rows_if_wrongly_enabled"`
}

type fr105ExpectedRowsFile struct {
	Views map[string]fr105ExpectedView `yaml:"views"`
}

// fr105Fixture copies testdata/fr105 into a temp vault and imports it.
func fr105Fixture(t *testing.T) (root string, rep *Report) {
	t.Helper()
	root = t.TempDir()
	src := filepath.Join("testdata", "fr105")
	for _, sub := range []string{"notes", "bases"} {
		// THE NOTES LAND AT THE VAULT ROOT, not under a `notes/` folder, and
		// the difference is load-bearing now that folder filters translate:
		// Obsidian's `file.inFolder("99-Temp")` is a path from the VAULT ROOT,
		// so a fixture that nested the notes one level deeper would make the
		// clause match nothing and the hand-derived oracle wrong about a
		// layout detail rather than about semantics. The oracle's own header
		// states the layout it was derived against ("Scratch idea ... 99-Temp?
		// YES"), and this is what makes the tree on disk match it.
		base := filepath.Join(src, sub)
		relRoot := base
		if sub == "bases" {
			relRoot = src
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(relRoot, path)
			if relErr != nil {
				return relErr
			}
			data, readErr := os.ReadFile(path) //nolint:gosec // committed fixture path
			if readErr != nil {
				return readErr
			}
			dst := filepath.Join(root, rel)
			if mkErr := os.MkdirAll(filepath.Dir(dst), 0o755); mkErr != nil {
				return mkErr
			}
			return os.WriteFile(dst, data, 0o644)
		})
		if err != nil {
			t.Fatalf("copying fixture %s: %v", sub, err)
		}
	}
	var runErr error
	rep, runErr = Run(root, true)
	if runErr != nil {
		t.Fatalf("importing the fixture vault: %v", runErr)
	}
	return root, rep
}

func fr105LoadOracle(t *testing.T) fr105ExpectedRowsFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "fr105", "expected_rows.yaml"))
	if err != nil {
		t.Fatalf("reading the hand-derived oracle: %v", err)
	}
	var out fr105ExpectedRowsFile
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("parsing the hand-derived oracle: %v", err)
	}
	if len(out.Views) == 0 {
		t.Fatal("the hand-derived oracle names no views — an empty oracle passes anything")
	}
	return out
}

// fr105Note is one fixture note: the parsed record, plus the VAULT-RELATIVE
// path the file.* layer resolves its metadata from.
type fr105Note struct {
	Rec     records.Record
	RelPath string
}

// fr105Notes reads every note in the imported vault back off disk through
// the product's own parser, keyed by filename stem.
func fr105Notes(t *testing.T, root string) map[string]fr105Note {
	t.Helper()
	inv, err := ScanVault(root)
	if err != nil {
		t.Fatalf("re-scanning the imported vault: %v", err)
	}
	out := map[string]fr105Note{}
	for _, abs := range inv.Notes {
		data, readErr := os.ReadFile(abs) //nolint:gosec // path from this run's own scan
		if readErr != nil {
			t.Fatalf("reading %s: %v", abs, readErr)
		}
		// Relative to the SCAN's own root, not the caller's: on macOS a temp
		// dir is reached through /var and resolves to /private/var, and
		// relativising against the unresolved spelling yields a path full of
		// `..` segments whose "folder" is nothing the view ever names.
		rel, relErr := filepath.Rel(inv.Root, abs)
		if relErr != nil {
			t.Fatalf("relativising %s: %v", abs, relErr)
		}
		stem := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
		out[stem] = fr105Note{Rec: records.ParseRecord(abs, data), RelPath: filepath.ToSlash(rel)}
	}
	return out
}

// fr105EvalNode decides one VaultFilterNode against one record.
//
// It is the only test-owned code in the evaluation loop, and it deliberately
// contains NO comparison logic: every leaf is handed to records.Filter, the
// product's own comparator, including FR-008's absence rule (a tree `not`
// over a leaf is the product's Filter.Negate, which is what that field is
// documented to mean).
func fr105EvalNode(t *testing.T, n generated.VaultFilterNode, schema *records.Schema, note fr105Note) bool {
	t.Helper()
	switch {
	case n.All != nil:
		for _, child := range *n.All {
			if !fr105EvalNode(t, child, schema, note) {
				return false
			}
		}
		return true
	case n.Any != nil:
		for _, child := range *n.Any {
			if fr105EvalNode(t, child, schema, note) {
				return true
			}
		}
		return false
	case n.Not != nil:
		inner := *n.Not
		if inner.Property != nil {
			// A `not` over a LEAF is the product's own Filter.Negate, which is
			// what that field is documented to mean and what carries FR-008's
			// absence rule.
			return fr105MatchLeaf(t, inner, true, schema, note)
		}
		// A `not` over a COMBINATOR is ordinary boolean negation of the
		// subtree — there is no leaf for Filter.Negate to attach to. FR-134's
		// folder translation produces exactly this shape (`not` over an
		// `any` of two file.folder leaves), and both of those leaves compare a
		// value that is PRESENT for every note (file.folder is "" at the vault
		// root, a present empty text value), so no absence rule applies inside
		// it.
		return !fr105EvalNode(t, inner, schema, note)
	case n.Property != nil:
		return fr105MatchLeaf(t, n, false, schema, note)
	default:
		t.Fatalf("filter node is neither a combinator nor a leaf: %+v", n)
		return false
	}
}

func fr105MatchLeaf(t *testing.T, n generated.VaultFilterNode, negate bool, schema *records.Schema, note fr105Note) bool {
	t.Helper()
	if n.Op == nil {
		t.Fatalf("leaf on %q carries no operator", *n.Property)
	}
	f := records.Filter{
		Property: *n.Property,
		Op:       records.Operator(*n.Op),
		Negate:   negate,
	}
	if n.Value != nil {
		f.Literal, f.LiteralGiven = *n.Value, true
	}
	if n.Values != nil {
		f.Literals = *n.Values
	}

	// A `file.*` leaf resolves through the PRODUCT's own file-metadata layer
	// (records.ResolveFileProperty over records.FilePropertySchema) and is then
	// decided by the SAME comparator as every other leaf — which is the whole
	// design of FR-130: the comparator cannot tell a file property from a
	// declared one, so there is no second set of rules here for this test to
	// get wrong.
	if records.IsFileNamespace(f.Property) {
		left, err := records.ResolveFileProperty(f.Property, records.FileMeta{Path: note.RelPath})
		if err != nil {
			t.Fatalf("the product refused a file property the importer wrote (%s): %v", f.Property, err)
		}
		res, matchErr := f.MatchValue(records.Comparator{}, records.FilePropertySchema(), left)
		if matchErr != nil {
			t.Fatalf("the product's comparator refused a file filter the importer wrote (%s %s %q): %v", f.Property, f.Op, f.Literal, matchErr)
		}
		return res.Matched
	}

	res, err := f.Match(schema, note.Rec)
	if err != nil {
		t.Fatalf("the product's comparator refused a filter the importer wrote (%s %s %q): %v", f.Property, f.Op, f.Literal, err)
	}
	return res.Matched
}

// fr105RowsFor applies one request to every note of the view's record type and
// returns the matching notes' stems, sorted.
func fr105RowsFor(t *testing.T, req generated.VaultFindRequest, schemas *records.SchemaSet, notes map[string]fr105Note) []string {
	t.Helper()
	if req.Type == nil {
		t.Fatal("the bridge produced a request with no record type")
	}
	schema, ok := schemas.Get(*req.Type)
	if !ok {
		t.Fatalf("the bridge produced a request for record type %q, which the vault does not declare", *req.Type)
	}
	var out []string
	for stem, note := range notes {
		if note.Rec.TypeName() != *req.Type {
			continue
		}
		if req.Filter == nil || fr105EvalNode(t, *req.Filter, schema, note) {
			out = append(out, stem)
		}
	}
	sort.Strings(out)
	return out
}

// TestImport_UntranslatableFilterDisablesNeverBroadens is spec test 98.
//
// Four assertions, and the fourth is the one that makes the other three
// worth having:
//
//  1. every view the oracle names was actually produced;
//  2. each view's enabled/disabled state is what the oracle says;
//  3. every ENABLED view returns EXACTLY its hand-derived row set;
//  4. the DISABLED view's own translated filters WOULD have returned
//     strictly MORE rows than its Obsidian original — the broadening,
//     measured, which is what makes disabling it a fix and not a precaution.
func TestImport_UntranslatableFilterDisablesNeverBroadens(t *testing.T) {
	root, rep := fr105Fixture(t)
	oracle := fr105LoadOracle(t)

	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading the schemas this run wrote: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("reloading the views this run wrote: %v", err)
	}
	if !viewRep.OK() {
		for _, rej := range viewRep.Rejections {
			t.Errorf("the importer wrote a view the real loader rejects: %s", rej.String())
		}
		t.FailNow()
	}
	notes := fr105Notes(t, root)

	// The importer's own three-way outcome, cross-checked against the
	// oracle's enabled/disabled column.
	disabledByImporter := map[string]bool{}
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.OutputRelPath == "" {
				continue
			}
			slug := strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			disabledByImporter[slug] = v.Disabled
		}
	}

	for name, want := range oracle.Views {
		t.Run(name, func(t *testing.T) {
			sv, ok := views.Get(name)
			if !ok {
				t.Fatalf("the oracle names view %q but the import produced no such view (produced: %s)", name, strings.Join(views.Names(), ", "))
			}
			gotDisabled := sv.Def.Disabled != nil && *sv.Def.Disabled
			if gotDisabled == want.Enabled {
				t.Fatalf("view %q: written to disk with disabled=%v, the hand-derived oracle says enabled=%v", name, gotDisabled, want.Enabled)
			}
			if importerSaid, seen := disabledByImporter[name]; seen && importerSaid != gotDisabled {
				t.Errorf("view %q: the report says disabled=%v but the file on disk says disabled=%v", name, importerSaid, gotDisabled)
			}

			loader := records.NewViewFindLoader(views)
			req, servable := loader.View(name)

			if !want.Enabled {
				// FR-105's read half, enforced by the PRODUCT: a disabled
				// view is not servable at all, and the refusal names why.
				if servable {
					t.Errorf("view %q is disabled but the view->find bridge served it anyway — the broadening prohibition is not enforced end to end", name)
				}
				refusal, hasRefusal := loader.ServeRefusal(name)
				if !hasRefusal || refusal.Code != records.ServeRefusalDisabled {
					t.Errorf("view %q: applying it was refused with code %q, want %q", name, refusal.Code, records.ServeRefusalDisabled)
				}

				// THE MEASUREMENT. Clear `disabled` in memory only and ask
				// the same bridge what the translated filters would have
				// matched. This is what shipped before FR-105 was enforced.
				sv.Def.Disabled = nil
				wrongReq, nowServable := loader.View(name)
				if !nowServable {
					t.Fatalf("view %q could not be evaluated even with disabled cleared, so the broadening cannot be measured", name)
				}
				got := fr105RowsFor(t, wrongReq, schemas, notes)
				if diff := fr105DiffRows(fr105Sorted(want.RowsIfWronglyEnabled), got); diff != "" {
					t.Errorf("view %q, evaluated as if it had shipped enabled: %s", name, diff)
				}
				original := fr105Sorted(want.Rows)
				if len(got) <= len(original) {
					t.Errorf("view %q: the hand-derived original returns %d rows and the translated filters return %d — this fixture is supposed to DEMONSTRATE broadening, and it no longer does",
						name, len(original), len(got))
				}
				t.Logf("BROADENING MEASURED: %q — Obsidian original %d rows %v, translated filters %d rows %v. Extra: %v. The view is stored DISABLED, so nothing ever sees the extra row.",
					name, len(original), original, len(got), got, fr105MissingFrom(original, got))
				return
			}

			if !servable {
				refusal, _ := loader.ServeRefusal(name)
				t.Fatalf("view %q is enabled but the view->find bridge will not serve it: %s", name, refusal.String())
			}
			got := fr105RowsFor(t, req, schemas, notes)
			if diff := fr105DiffRows(fr105Sorted(want.Rows), got); diff != "" {
				t.Errorf("view %q: %s", name, diff)
			}
		})
	}
}

// TestImport_EveryProducedViewIsCoveredByTheOracle stops the oracle from
// going quietly out of date. A view the importer writes and the expectation
// file does not mention is untested, and an untested view is exactly where a
// broadening would hide.
func TestImport_EveryProducedViewIsCoveredByTheOracle(t *testing.T) {
	root, _ := fr105Fixture(t)
	oracle := fr105LoadOracle(t)

	schemas, _, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("reloading schemas: %v", err)
	}
	views, _, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("reloading views: %v", err)
	}
	for _, name := range views.Names() {
		if _, covered := oracle.Views[name]; !covered {
			t.Errorf("view %q was written by the importer but testdata/fr105/expected_rows.yaml does not name it — derive its row set by hand and add it", name)
		}
	}
	if len(views.Names()) != len(oracle.Views) {
		t.Errorf("the importer produced %d views and the oracle covers %d", len(views.Names()), len(oracle.Views))
	}
}

func fr105Sorted(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

func fr105DiffRows(want, got []string) string {
	if strings.Join(want, "\x00") == strings.Join(got, "\x00") {
		return ""
	}
	return "row set is " + strings.Join(got, " | ") +
		"\n            hand-derived expectation is " + strings.Join(want, " | ") +
		"\n            unexpectedly present: " + strings.Join(fr105MissingFrom(want, got), ", ") +
		"\n            unexpectedly absent:  " + strings.Join(fr105MissingFrom(got, want), ", ")
}

// fr105MissingFrom returns everything in b that is not in a.
func fr105MissingFrom(a, b []string) []string {
	in := map[string]bool{}
	for _, s := range a {
		in[s] = true
	}
	var out []string
	for _, s := range b {
		if !in[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
