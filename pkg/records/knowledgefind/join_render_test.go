// Omnipus — D2 (vault-tools-report S1 / Issue 7): a `join` must render its
// borrowed relation by default, never silently produce rows with no borrowed
// column at all. Before this, q.join was validated, planned and advertised
// ("rendered as borrowed") but row.Joins was never populated — and the joined
// property was not even decoded — so `join=bed` added nothing to any row.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func withJoin(props ...string) func(*generated.VaultFindRequest) {
	return func(r *generated.VaultFindRequest) {
		j := append([]string{}, props...)
		r.Join = &j
	}
}

// TestJoin_RendersBorrowedRelationByDefault: type=plant + join=bed must attach
// the borrowed bed relation to every plant row, marked as borrowed, WITHOUT the
// caller having to also add `bed` to select.
func TestJoin_RendersBorrowedRelationByDefault(t *testing.T) {
	f := gardenCorpus(t)

	resp := mustFind(t, f.deps(), req(withType("plant"), withJoin("bed")))
	if len(resp.Rows) == 0 {
		t.Fatal("no rows returned for type=plant")
	}
	for _, row := range resp.Rows {
		if len(row.Joins) == 0 {
			t.Fatalf("row %q has no borrowed join though join=bed was requested — the borrowed column is silently omitted", row.Path)
		}
		j := row.Joins[0]
		if j.Relation != "bed" {
			t.Errorf("borrowed join relation = %q, want \"bed\"", j.Relation)
		}
		if !strings.Contains(j.Target, "[[") {
			t.Errorf("borrowed join target = %q, want the related record's wikilink identity", j.Target)
		}
	}

	// And it must be visible in the rendered text a model actually reads,
	// marked as borrowed (render.go: `bed [[...]]:`), not merged into the
	// record's own columns.
	out := Render(resp)
	if !strings.Contains(out, "bed [[") {
		t.Errorf("the rendered output does not show the borrowed bed relation:\n%s", out)
	}
}

// TestJoin_BorrowedRelationIsNotAlsoAnOwnColumn: the borrowed relation must not
// leak into the row's own Cells — it is another record's, not this one's.
func TestJoin_BorrowedRelationIsNotAlsoAnOwnColumn(t *testing.T) {
	f := gardenCorpus(t)
	resp := mustFind(t, f.deps(), req(withType("plant"), withJoin("bed")))
	for _, row := range resp.Rows {
		for _, c := range row.Cells {
			if c.Property == "bed" {
				t.Errorf("row %q renders bed as its OWN column; a joined relation must render as borrowed only", row.Path)
			}
		}
	}
}
