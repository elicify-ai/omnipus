// Omnipus — UAT 2026-09-13 Q-08 (re-test row, reclassified NEW DEFECT S3):
// the Library shows a saved view by its LABEL ("All Projects"), never by the
// slug it is stored under (projects--all-projects), and `knowledge_find`'s
// `view` argument resolved a slug only — so an agent told "use the All
// Projects view" got "no saved view named", flatly false about a view that
// both existed and had a human-readable name for exactly that purpose.
//
// records.ViewSet.Resolve (pkg/records/view.go) is the one shared fix:
// exact slug first, then exact display label, refusing rather than guessing
// when a label names more than one view. These tests prove the fix reaches
// knowledge_find through records.ViewFindLoader, the production bridge —
// not a hand-written test double that could silently drift from it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// TestKnowledgeFind_ResolvesSavedViewByDisplayLabel is the direct
// reproduction DO step 1 asks for: a saved view whose label
// ("Due within a week") differs from its slug ("due-soon"), queried both
// ways.
//
// deadlineVault (file_and_formula_test.go) already writes exactly this
// shape through the PRODUCTION loader (records.LoadViews +
// records.NewViewFindLoader), so no fixture here can silently diverge from
// what a real vault stores.
func TestKnowledgeFind_ResolvesSavedViewByDisplayLabel(t *testing.T) {
	deps, _ := deadlineVault(t)
	deps.Now = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	t.Run("by slug — must still work", func(t *testing.T) {
		slug := "due-soon"
		resp := mustFind(t, deps, generated.VaultFindRequest{View: &slug})
		if got := rowIDs(resp); len(got) != 1 || got[0] != "work/soon.md" {
			t.Fatalf("rows by slug = %v, want exactly [work/soon.md]", got)
		}
	})

	t.Run("by label — the Q-08 fix", func(t *testing.T) {
		label := "Due within a week"
		resp := mustFind(t, deps, generated.VaultFindRequest{View: &label})
		if got := rowIDs(resp); len(got) != 1 || got[0] != "work/soon.md" {
			t.Fatalf("rows by label = %v, want exactly [work/soon.md] — "+
				"the Library shows this view by its label and an agent told "+
				"to use it must get the same answer as the slug", got)
		}
		// The formula travels too (Formulas is resolved by the SAME name the
		// caller gave, per view_find_bridge.go's Formulas doc comment) — a
		// label-resolved view that silently dropped its formula would answer
		// a different, narrower question than the slug does.
		var sawFormula bool
		for _, c := range resp.Rows[0].Cells {
			if c.Property == "formula.days_until_due" {
				sawFormula = true
			}
		}
		if !sawFormula {
			t.Errorf("label-resolved view lost its formula cell; cells = %+v", resp.Rows[0].Cells)
		}
	})
}

// twoLabelSharingViews writes two views sharing ONE display label
// ("Every deadline") under two different slugs, through the production
// loader — the shape records.ViewSet.Resolve must refuse to guess between.
// Two views sharing a label is not itself rejected at LOAD (only a
// duplicate SLUG is, records.RejectViewDuplicateName): a vault predating
// D-21's write-time label-collision refusal, or a view imported outside
// knowledge_configure, can carry exactly this.
func twoLabelSharingViews(t *testing.T) (Deps, *records.ViewFindLoader) {
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

	viewDir := records.ViewsDir(root)
	if err := os.MkdirAll(viewDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(views): %v", err)
	}
	for _, slug := range []string{"deadlines-a", "deadlines-b"} {
		body := "name: " + slug + "\ntype: deadline\nlabel: Every deadline\n"
		if err := os.WriteFile(filepath.Join(viewDir, slug+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", slug, err)
		}
	}
	views, viewReport, err := records.LoadViews(root, set)
	if err != nil {
		t.Fatalf("LoadViews: %v", err)
	}
	if !viewReport.OK() {
		t.Fatalf("a fixture view was rejected by the loader: %v", viewReport.Rejections)
	}
	if views.Len() != 2 {
		t.Fatalf("both same-label views must load (only a duplicate SLUG is refused at load): got %d", views.Len())
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

	loader := records.NewViewFindLoader(views)
	return Deps{Schemas: set, Store: store, Text: text, Views: loader, Epoch: 1}, loader
}

// TestKnowledgeFind_AmbiguousLabelIsRefusedNotGuessed is the second half of
// Resolve's contract: a label naming MORE than one view must never be
// resolved silently to whichever view happened to load first — it must be
// refused, by name, listing every candidate's label and slug so the caller
// can repeat the request unambiguously.
func TestKnowledgeFind_AmbiguousLabelIsRefusedNotGuessed(t *testing.T) {
	deps, _ := twoLabelSharingViews(t)

	label := "Every deadline"
	resp := mustRefuse(t, deps, generated.VaultFindRequest{View: &label})

	reason := resp.Problems[0].Reason
	if strings.Contains(reason, "no saved view named") {
		t.Fatalf("an ambiguous label was reported as UNKNOWN, which is false — "+
			"two real views answer to it: %q", reason)
	}
	for _, want := range []string{"deadlines-a", "deadlines-b", "Every deadline"} {
		if !strings.Contains(reason, want) {
			t.Errorf("refusal does not name candidate %q: %q", want, reason)
		}
	}
	if resp.Problems[0].Fix == nil || *resp.Problems[0].Fix == "" {
		t.Errorf("an ambiguous-label refusal with no remedy leaves the caller stuck")
	}

	// Each slug alone is still unambiguous.
	for _, slug := range []string{"deadlines-a", "deadlines-b"} {
		s := slug
		resp := mustFind(t, deps, generated.VaultFindRequest{View: &s})
		if resp.Refused {
			t.Errorf("slug %q must resolve unambiguously even though its label is shared", s)
		}
	}
}

// TestKnowledgeFind_UnknownViewMessageListsLabelsAlongsideSlugs is the
// message-quality half of the fix: an agent that mistypes a view name is
// shown the LABEL it would recognise (what the Library rendered) next to
// the slug `view` actually accepts, not a bare list of internal slugs.
func TestKnowledgeFind_UnknownViewMessageListsLabelsAlongsideSlugs(t *testing.T) {
	deps, _ := deadlineVault(t)

	name := "nonexistent-view"
	resp := mustRefuse(t, deps, generated.VaultFindRequest{View: &name})

	reason := resp.Problems[0].Reason
	if !strings.Contains(reason, "Due within a week") {
		t.Errorf("unknown-view refusal does not name the LABEL a caller would recognise: %q", reason)
	}
	if !strings.Contains(reason, "due-soon") {
		t.Errorf("unknown-view refusal does not name the SLUG the argument actually accepts: %q", reason)
	}
}
