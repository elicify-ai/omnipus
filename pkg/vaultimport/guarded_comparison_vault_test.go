// Omnipus — W4 graded where it is USED: the two views it re-enables are served
// through knowledge_find itself, against an oracle this project did not compute.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package vaultimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// ---------------------------------------------------------------------------
// WHY THIS GRADER EXISTS BESIDE fr105_oracle_test.go's
//
// That one decides a leaf with records.Filter.Match and walks the tree itself.
// It cannot grade a view whose filter names a FORMULA — there is no declared
// property behind `formula.is_overdue`, and no clock to evaluate `today()` at.
// W4's whole effect is on two such views, so grading it there would have
// graded nothing.
//
// So this file serves the written views through knowledgefind.Find — the real
// engine, the real namespace resolution, the real formula evaluator, the real
// comparator — with Deps.Now PINNED to the instant the hand-derived oracle was
// computed against. Every component in the loop is the product's except the
// text-index stub, which decides no row: it exists because Deps.Text is
// REQUIRED for FR-020c's freshness check and it agrees with the properties
// index for every note, so freshness never removes a row.
//
// AND THEN THE GRADER IS ITSELF GRADED, which is the part that is easy to skip
// and is the reason two measurements on this vault have proved nothing. A view
// whose record type has no notes returns zero rows however wrong its filter
// is, and matching a zero-row oracle then looks like a pass. So every view
// here is ALSO re-served with every filter clause stripped except the one that
// scopes it, and a view that returns the same rows fully broadened is reported
// UNFALSIFIABLE and not counted.
// ---------------------------------------------------------------------------

// w4OracleClock is the instant the committed acceptance oracle was derived
// against. `today()` inside is_overdue reads it, so an unpinned clock would
// make this test's answer drift a row a day.
var w4OracleClock = time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)

// w4Text is a text index that agrees with the properties index about every
// note it is told about. It decides no row: Search is never reached (no view
// here carries `words`), and SourceHash exists so FR-020c's per-record
// freshness check finds the hashes equal rather than flagging every row.
type w4Text struct{ hashes map[string]string }

func (s *w4Text) Search(context.Context, string, int) ([]knowledgefind.TextHit, error) {
	return nil, nil
}

func (s *w4Text) NearestTerms(context.Context, string, int) ([]generated.VaultTermCount, error) {
	return nil, nil
}

func (s *w4Text) SourceHash(_ context.Context, path string) (string, bool, error) {
	h, ok := s.hashes[path]
	return h, ok, nil
}

// w4Served is one imported view, served.
type w4Served struct {
	deps  knowledgefind.Deps
	views *records.ViewSet
}

// w4Serve imports the fixture vault and opens everything knowledge_find needs
// over what the import WROTE — schemas and views re-read by the real loaders,
// notes re-indexed through the real row builder.
func w4Serve(t *testing.T) (root string, rep *Report, s w4Served) {
	t.Helper()
	root = fixtureVaultCopy(t)
	var err error
	rep, err = Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	schemas, schemaRep, err := records.LoadSchemas(root)
	if err != nil {
		t.Fatalf("loading the schemas this run wrote: %v", err)
	}
	if !schemaRep.OK() {
		t.Fatalf("the importer wrote schemas the real loader rejects: %v", schemaRep.Rejections)
	}
	views, viewRep, err := records.LoadViews(root, schemas)
	if err != nil {
		t.Fatalf("loading the views this run wrote: %v", err)
	}
	if !viewRep.OK() {
		for _, rej := range viewRep.Rejections {
			t.Errorf("the importer wrote a view the real loader rejects: %s", rej.String())
		}
		t.FailNow()
	}

	store, err := propindex.Open(context.Background(), filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("opening the properties index: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("closing the properties index: %v", cerr)
		}
	})

	text := &w4Text{hashes: map[string]string{}}
	// stems maps a note's identity-bearing filename stem to its path, so a
	// wikilink can be resolved the way R-8 needs.
	stems := map[string]string{}
	indexed := 0
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // a temp copy of the fixture vault
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		rec := records.ParseRecord(rel, data)
		sc, _ := schemas.Get(rec.TypeName())
		hash := propindex.SourceHash(data)
		rows := propindex.BuildNoteRows(rec, sc, data, hash)
		if upErr := store.UpsertNote(context.Background(), rows); upErr != nil {
			return upErr
		}
		text.hashes[rel] = hash
		stems[strings.TrimSuffix(filepath.Base(rel), ".md")] = rel
		indexed++
		return nil
	})
	if err != nil {
		t.Fatalf("indexing the imported vault: %v", err)
	}
	if indexed == 0 {
		t.Fatal("no note was indexed, so every query below would answer nothing for a reason that is not the filter")
	}

	resolve := func(l records.Wikilink) (string, bool) {
		p, ok := stems[strings.TrimSuffix(filepath.Base(l.Target), ".md")]
		return p, ok
	}

	return root, rep, w4Served{
		deps: knowledgefind.Deps{
			Schemas: schemas,
			Store:   store,
			Text:    text,
			Views:   records.NewViewFindLoader(views),
			Resolve: resolve,
			Epoch:   1,
			Now:     w4OracleClock,
		},
		views: views,
	}
}

// rows serves one request through knowledge_find and returns EVERY matching
// note's filename stem, sorted — the currency the oracle is compared in.
//
// IT PAGES. The engine caps a page at 200 rows and reports the clamp; a grader
// that read only the first page would score a 300-row broadening as a 200-row
// answer and could report agreement on a view it had not finished reading. So
// this follows `next_cursor` to exhaustion and fails on any answer that is not
// COMPLETE, because an incomplete answer is not a row set to compare.
func (s w4Served) rows(t *testing.T, req generated.VaultFindRequest) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for page := 0; ; page++ {
		if page > 400 {
			t.Fatalf("knowledge_find kept issuing cursors after %d pages", page)
		}
		resp, err := knowledgefind.Find(context.Background(), s.deps, req)
		if err != nil {
			t.Fatalf("knowledge_find REFUSED a request this importer wrote and left ENABLED: %v (problems: %+v)", err, resp.Problems)
		}
		if resp.Refused {
			t.Fatalf("knowledge_find refused without an error: %+v", resp.Problems)
		}
		// AN INCOMPLETE PAGE IS ONLY ACCEPTABLE WHEN THE BYTE BUDGET IS THE
		// REASON, and then only because the cursor is derived from what was
		// actually returned rather than from the page size (assemble.go's F8
		// note), so the rows the budget dropped come back on the next page
		// instead of being skipped. Every other incompleteness — a stale row,
		// an excluded record, a clamp — means the answer is not a row set to
		// grade, and is fatal here rather than quietly compared.
		for _, pb := range resp.Problems {
			t.Logf("knowledge_find problem on this answer: %+v", pb)
		}
		if resp.LimitClamped != nil && *resp.LimitClamped {
			t.Fatalf("the page size was clamped, so this answer is not the whole page it claims to be")
		}
		for _, r := range resp.Rows {
			stem := strings.TrimSuffix(filepath.Base(r.Path), ".md")
			if seen[stem] {
				continue
			}
			seen[stem] = true
			out = append(out, stem)
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			break
		}
		next := *resp.NextCursor
		req.Cursor = &next
	}
	sort.Strings(out)
	return out
}

// w4Request builds the request that serves one saved view by name.
func w4Request(slug string) generated.VaultFindRequest {
	limit := 200
	return generated.VaultFindRequest{View: &slug, Limit: &limit}
}

// w4OracleRows reads the hand-derived expectation for one base+view, in stems.
func w4OracleRows(t *testing.T, base, view string) ([]string, bool) {
	t.Helper()
	path := os.Getenv(fr105OracleEnv)
	if path == "" {
		t.Skipf("%s is unset — set it to the hand-derived expected-row-set JSON for the real vault", fr105OracleEnv)
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied acceptance oracle
	if err != nil {
		t.Fatalf("reading the oracle: %v", err)
	}
	var oracle fr105JSONOracle
	if err = json.Unmarshal(data, &oracle); err != nil {
		t.Fatalf("parsing the oracle: %v", err)
	}
	for _, b := range oracle.Bases {
		if b.Base != base {
			continue
		}
		for _, v := range b.Views {
			if v.Name == view {
				return fr105Sorted(fr105OracleStems(v.Rows)), true
			}
		}
	}
	return nil, false
}

// w4SlugFor finds the slug the import gave one base's view.
func w4SlugFor(t *testing.T, rep *Report, base, view string) (slug string, enabled bool) {
	t.Helper()
	for _, b := range rep.Bases {
		if filepath.Base(b.BaseRelPath) != base {
			continue
		}
		for _, v := range b.Views {
			if v.DisplayName != view {
				continue
			}
			if v.OutputRelPath == "" {
				t.Fatalf("the import produced no file for %q of %s", view, base)
			}
			return strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml"), !v.Disabled
		}
	}
	t.Fatalf("the import produced no view named %q in %s", view, base)
	return "", false
}

// TestFixtureVault_W4EnabledViewsMatchTheOracle grades the two views W4
// re-enables, and states for each whether the grading could have falsified it.
//
// THE FALSIFIABILITY COLUMN IS AN ASSERTION, not a note. A view whose record
// type has no notes returns zero rows however wrong its filter is, so matching
// a zero-row oracle there proves nothing — and saying so in a log line that
// nobody reads is how two measurements on this vault came to be reported as
// exact verdicts. Declaring it here means the day `compliance` notes appear,
// this test fails and tells somebody to start grading that view for real.
func TestFixtureVault_W4EnabledViewsMatchTheOracle(t *testing.T) {
	_, rep, s := w4Serve(t)

	for _, tc := range []struct {
		base, view  string
		falsifiable bool
		why         string
	}{
		{
			base: "Tasks.base", view: "Overdue", falsifiable: true,
			why: "121 notes declare `type: task`, and the unfiltered population is far larger than the 3 rows the oracle names",
		},
		{
			base: "Compliance.base", view: "Overdue", falsifiable: false,
			why: "the founder's vault contains NO note of type `compliance` at all, so this view returns zero rows whatever its filter says",
		},
	} {
		slug, enabled := w4SlugFor(t, rep, tc.base, tc.view)
		if !enabled {
			t.Errorf("W4 was supposed to enable %q of %s and it is still DISABLED", tc.view, tc.base)
			continue
		}
		want, known := w4OracleRows(t, tc.base, tc.view)
		if !known {
			t.Errorf("the oracle does not cover %q of %s — an ungraded view is exactly where a broadening hides", tc.view, tc.base)
			continue
		}
		got := s.rows(t, w4Request(slug))

		if extra := fr105MissingFrom(want, got); len(extra) > 0 {
			t.Errorf("FR-105 BROADENING in %q (%s): the imported view returns %d row(s) the Obsidian original does not: %v",
				tc.view, tc.base, len(extra), extra)
		}
		if missing := fr105MissingFrom(got, want); len(missing) > 0 {
			t.Logf("NARROWING (allowed by FR-105, recorded anyway) in %q (%s): the Obsidian original returns %d row(s) the import does not: %v",
				tc.view, tc.base, len(missing), missing)
		}

		// THE GRADER'S OWN POWER, measured rather than assumed: the same record
		// type with every filter clause stripped. If the answer does not grow,
		// no clause in that filter could have been wrong in a way the
		// comparison above would have caught.
		broad := s.rows(t, w4BroadRequestFor(t, s, slug))
		falsifiable := len(broad) > len(got)
		if falsifiable != tc.falsifiable {
			t.Errorf("%q of %s: filtered=%d unfiltered=%d, so falsifiable=%v — this test declares %v (%s). Update the declaration and grade it properly, or find out why the population changed",
				tc.view, tc.base, len(got), len(broad), falsifiable, tc.falsifiable, tc.why)
			continue
		}
		verdict := "FALSIFIABLE"
		if !falsifiable {
			verdict = "UNFALSIFIABLE — not counted: " + tc.why
		}
		t.Logf("GRADED %-16s %-10s oracle=%d imported=%d unfiltered=%d  %s",
			tc.base, tc.view, len(want), len(got), len(broad), verdict)
	}
}

// TestFixtureVault_DisabledViewsAreOnesTheEngineActuallyRefuses is the other
// half of "the importer must refuse what the engine refuses", and it is the
// half that cannot be argued.
//
// Both views below now import DISABLED with a named loss where they used to
// import ENABLED and silent. That is only the right outcome if the engine
// really would have refused them — a disabled view justified by a reason
// nobody checked is a precaution, not a fix. So each one's `disabled` flag is
// cleared and the view is served: knowledge_find MUST refuse it, and the
// refusal must name the property the importer's own loss names.
func TestFixtureVault_DisabledViewsAreOnesTheEngineActuallyRefuses(t *testing.T) {
	_, rep, s := w4Serve(t)

	for _, tc := range []struct {
		base, view, property, literal, want string
	}{
		{
			base: "Tasks.base", view: "Needs Daniel", property: "owner",
			literal: "Daniel Piatkowski",
			want:    "not a wikilink",
		},
		{
			base: "Inbox-Triage.base", view: "Triage Queue (Oldest First)", property: "status",
			literal: "unfiled",
			want:    "will not split one name across two domains",
		},
	} {
		slug, enabled := w4SlugFor(t, rep, tc.base, tc.view)
		if enabled {
			t.Errorf("%q of %s imported ENABLED, but the engine refuses it — that is the silent failure this change exists to end", tc.view, tc.base)
			continue
		}

		// The importer's own words first: the loss has to NAME the property,
		// or an operator reading the report cannot act on it.
		reason := w4LossReasonFor(t, rep, tc.base, tc.view)
		if !strings.Contains(reason, tc.property) {
			t.Errorf("%q of %s is disabled but its loss does not name %q: %s", tc.view, tc.base, tc.property, reason)
		}

		// THE VIEW IS SERVED WITH ITS FILTER RESTORED, not with the clause the
		// importer dropped. Serving what the importer WROTE would prove
		// nothing: the offending clause is not in it. So the original clause is
		// put back and the whole thing put to the engine.
		refusal := s.refusalFor(t, slug, tc.property, tc.literal)
		if refusal == "" {
			t.Errorf("knowledge_find ANSWERED a filter this importer refused to write for %q of %s — the loss is a precaution, not a fix", tc.view, tc.base)
			continue
		}
		if !strings.Contains(refusal, tc.want) {
			t.Errorf("knowledge_find refused %q of %s for a reason the importer did not predict: %s", tc.view, tc.base, refusal)
			continue
		}
		t.Logf("CONFIRMED %-18s %-30s disabled, and the engine refuses the dropped clause: %s", tc.base, tc.view, firstLine(refusal))
	}
}

// refusalFor serves one saved view with the clause the importer refused to
// write PUT BACK, and returns the engine's refusal text (empty if it answered).
//
// TWO THINGS HERE ARE LOAD-BEARING AND BOTH WERE WRONG ON THE FIRST ATTEMPT.
//
// (1) `disabled` IS CLEARED FIRST. A stored-disabled view refuses at the door,
// before any leaf is resolved, so serving it as stored proves only that
// disabling works — which nobody doubted.
//
// (2) THE REFUSAL IS CHECKED FOR THE ENGINE'S OWN WORDS, and the door refusal
// is rejected explicitly. The written view file CARRIES the importer's loss
// text in its `untranslated:` block, and that text is quoted back inside the
// stored-disabled refusal — so a substring check for "not a wikilink" matched
// the IMPORTER agreeing with itself, and the test passed while asserting
// nothing. That is the shape of a green that means nothing, and it was caught
// only because the logged refusal did not read like the engine's.
//
// The clause is re-added through the request's own `filter`, which
// VaultFindRequest documents as applied AFTER the view — so this is the real
// engine deciding the real leaf.
func (s w4Served) refusalFor(t *testing.T, slug, property, literal string) string {
	t.Helper()
	sv, ok := s.views.Get(slug)
	if !ok {
		t.Fatalf("no such view %q", slug)
	}
	restore := sv.Def.Disabled
	sv.Def.Disabled = nil
	defer func() { sv.Def.Disabled = restore }()

	limit := 200
	op := generated.Equal
	req := generated.VaultFindRequest{
		View:   &slug,
		Limit:  &limit,
		Filter: &generated.VaultFilterNode{Property: &property, Op: &op, Value: &literal},
	}
	if sv.Def.Type != nil {
		req.Type = sv.Def.Type
	}
	resp, err := knowledgefind.Find(context.Background(), s.deps, req)
	var out string
	switch {
	case err != nil:
		out = err.Error()
	case resp.Refused:
		parts := make([]string, 0, len(resp.Problems))
		for _, p := range resp.Problems {
			parts = append(parts, p.Reason)
		}
		out = strings.Join(parts, "; ")
	default:
		return ""
	}
	if strings.Contains(out, "is stored disabled") {
		t.Fatalf("the view refused at the door rather than on the clause, so this measured nothing: %s", out)
	}
	return out
}

// w4LossReasonFor returns everything the import said it lost on one view.
func w4LossReasonFor(t *testing.T, rep *Report, base, view string) string {
	t.Helper()
	for _, b := range rep.Bases {
		if filepath.Base(b.BaseRelPath) != base {
			continue
		}
		for _, v := range b.Views {
			if v.DisplayName == view {
				return strings.Join(v.Losses, " | ")
			}
		}
	}
	t.Fatalf("no view named %q in %s", view, base)
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// w4BroadRequestFor builds the same view's request with every filter clause
// removed, keeping only the record type. It is the strip test.
func w4BroadRequestFor(t *testing.T, s w4Served, slug string) generated.VaultFindRequest {
	t.Helper()
	sv, ok := s.views.Get(slug)
	if !ok {
		t.Fatalf("no such view %q", slug)
	}
	limit := 200
	req := generated.VaultFindRequest{Limit: &limit}
	if sv.Def.Type != nil {
		req.Type = sv.Def.Type
	}
	return req
}

// TestFixtureVault_EveryEnabledViewActuallyServes is the general form of the
// rule the three fixes above are instances of, and the reason it is worth
// having as a standing test rather than three specific ones: an ENABLED view
// that knowledge_find refuses is a promise this importer made and cannot keep,
// and it is invisible in the report — the view reads CONVERTED, the counts read
// clean, and the failure surfaces only when a person opens it.
//
// So every enabled view the founder's vault produces is SERVED, and the answer
// must not be a refusal.
//
// ONE EXCEPTION IS ALLOWED, BY NAME AND WITH ITS REASON, because pretending it
// is not there would be the same dishonesty one level up. `Most Connected`
// groups DESCENDING; the view file records that faithfully, and a find request
// has nowhere to ask for a group direction, so knowledge_find refuses rather
// than silently reordering the groups ascending (ServeRefusalGroupDirection).
// The report already names that loss and argues — correctly — that the importer
// did its job and the gap is in the REQUEST model. What it does not do is stop
// the view importing ENABLED, so this one still dies on first use.
//
// THAT IS AN OPEN DECISION, NOT A FIX MADE HERE. Storing it disabled would make
// the report honest at the cost of repurposing FR-105's mechanism — which is
// about ROW COUNTS — for a refusal that broadens nothing; dropping the grouping
// instead would serve the right rows in the wrong shape. Both are design calls
// above a translation change, and neither is made silently. The exception is
// pinned to ONE named view so that a SECOND one cannot join it unnoticed.
func TestFixtureVault_EveryEnabledViewActuallyServes(t *testing.T) {
	_, rep, s := w4Serve(t)

	// The open case above, and nothing else. A view added to this map is a
	// decision somebody has to make on purpose.
	known := map[string]string{
		"Most Connected": "group_direction_not_representable — a find request carries no group direction (see this test's header)",
	}

	enabled, refused := 0, 0
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			if v.OutputRelPath == "" || v.Disabled {
				continue
			}
			enabled++
			slug := strings.TrimSuffix(filepath.Base(v.OutputRelPath), ".yaml")
			limit := 200
			resp, err := knowledgefind.Find(context.Background(), s.deps, generated.VaultFindRequest{View: &slug, Limit: &limit})
			if err == nil && !resp.Refused {
				if why, listed := known[v.DisplayName]; listed {
					t.Errorf("%q of %s is on the known-refusal list (%s) but now SERVES — take it off the list", v.DisplayName, b.BaseRelPath, why)
				}
				continue
			}
			refused++
			if _, listed := known[v.DisplayName]; listed {
				continue
			}
			detail := ""
			if err != nil {
				detail = err.Error()
			}
			for _, pb := range resp.Problems {
				detail += " | " + pb.Reason
			}
			t.Errorf("A VIEW IMPORTED ENABLED AND KNOWLEDGE_FIND REFUSES IT: %q of %s — %s", v.DisplayName, b.BaseRelPath, detail)
		}
	}
	if enabled == 0 {
		t.Fatal("no enabled view was served, so this test asserted nothing")
	}
	t.Logf("SERVED %d enabled view(s); %d refused, all of them on the named exception list", enabled, refused)
}
