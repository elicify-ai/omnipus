// Omnipus — the seams the composed test does not reach: the prepass a FORMULA
// alone triggers, backlink scope, and the three-namespace refusal ladder.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

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
		{
			name:   "a typed property with no record type names both escapes",
			req:    generated.VaultFindRequest{Filter: leaf("species")},
			wantIn: []string{"species", "no record type was given", "file.*"},
		},
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
