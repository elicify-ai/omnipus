// Omnipus — the end-to-end guard on FR-146's raised DEPTH cap.
//
// WHY THIS FILE EXISTS SEPARATELY FROM pkg/records/formula_depth_cap_test.go.
//
// Those tests call ValidateFormulaSet directly. They prove the cap's NUMBER is
// derived rather than guessed, and they would all keep passing if the importer
// stopped asking the validator anything at all — the same shape of escape
// enum_widening_reported_test.go's header names: a unit test that calls the
// function under test directly can never fail for a missing CALL to it.
//
// What the founder actually gets from raising the cap is a COLUMN in a view.
// That runs through the whole of Run: parse the `.base`, translate the
// formulas, validate the set against FR-146, write the view file, and leave no
// loss behind. This test asserts that outcome on a real vault on disk, which is
// the only level at which "the cap was raised but the column is still missing"
// can be caught.
//
// It also pins the FR-105 direction, which is the one thing raising a cap could
// get wrong. `team_name` is used in DISPLAY positions only, so admitting it must
// add a column and must NOT change which rows the view returns.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// deepLookupFormula is the founder's own `team_name` shape, reduced to the part
// that matters: an N-way lookup written as a nested `if`, because the formula
// grammar has no `switch`. Ten arms measure 51 nodes and 12 levels — inside
// FR-146's 64-node budget and outside the depth cap as it stood at 8.
const deepLookupFormula = `if(team == "t0", "T0", if(team == "t1", "T1", ` +
	`if(team == "t2", "T2", if(team == "t3", "T3", if(team == "t4", "T4", ` +
	`if(team == "t5", "T5", if(team == "t6", "T6", if(team == "t7", "T7", ` +
	`if(team == "t8", "T8", if(team == "t9", "T9", team))))))))))`

// vaultWithADeepDisplayOnlyFormula builds the smallest vault reproducing
// Tasks.base's situation: notes carrying a team code, and a base whose
// lookup formula is referenced only from `order:`.
func vaultWithADeepDisplayOnlyFormula(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	for _, n := range []struct{ name, team string }{
		{"a.md", "t0"}, {"b.md", "t3"}, {"c.md", "t9"},
	} {
		body := "---\ntype: task\nteam: " + n.team + "\n---\n\nbody\n"
		if err := os.WriteFile(filepath.Join(root, n.name), []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %v", n.name, err)
		}
	}

	base := `filters:
  and:
    - type == "task"
formulas:
  team_name: ` + deepLookupFormula + `
properties:
  formula.team_name:
    displayName: Team
views:
  - type: table
    name: By team
    order:
      - file.name
      - formula.team_name
`
	if err := os.WriteFile(filepath.Join(root, "Tasks.base"), []byte(base), 0o644); err != nil {
		t.Fatalf("writing Tasks.base: %v", err)
	}
	return root
}

// TestRun_CarriesADeepButCheapLookupFormula is the guard. Before FR-146's depth
// cap was re-derived this import dropped the column and named a size loss on a
// formula using 51 of its permitted 64 nodes.
func TestRun_CarriesADeepButCheapLookupFormula(t *testing.T) {
	// First: the fixture has to actually exercise the raised cap. If the
	// formula is within the OLD value of 8 this test proves nothing, and it
	// must say so rather than passing.
	root, err := records.ParseFormula(deepLookupFormula)
	if err != nil {
		t.Fatalf("the fixture formula does not parse: %v", err)
	}
	nodes, depth := records.FormulaNodeCount(root), records.FormulaDepth(root)
	if depth <= 8 {
		t.Fatalf("the fixture formula is only %d levels deep, so it would have been carried under the old cap of 8 too — this test would pass without exercising the change at all", depth)
	}
	if nodes > 64 {
		t.Fatalf("the fixture formula is %d nodes, over FR-146's 64-node cap — it would be refused on SIZE and this test would prove nothing about DEPTH", nodes)
	}

	vault := vaultWithADeepDisplayOnlyFormula(t)
	rep, runErr := Run(vault, true)
	if runErr != nil {
		t.Fatalf("import failed: %v", runErr)
	}

	// 1 — no loss anywhere mentions the formula.
	for _, b := range rep.Bases {
		for _, v := range b.Views {
			for _, l := range v.Losses {
				if strings.Contains(l, "team_name") {
					t.Errorf("view %q still reports a loss on the lookup formula: %s", v.DisplayName, l)
				}
			}
		}
	}

	// 2 — and the view was not disabled. A display-only formula decides no
	// rows, so admitting it must not move the view's FR-105 verdict.
	var view *ViewOutcome
	for i := range rep.Bases {
		for j := range rep.Bases[i].Views {
			if rep.Bases[i].Views[j].DisplayName == "By team" {
				view = &rep.Bases[i].Views[j]
			}
		}
	}
	if view == nil {
		t.Fatal(`the import produced no view named "By team"`)
	}
	if view.Disabled {
		t.Errorf("the view is DISABLED — admitting a DISPLAY-only formula must never change the row-set verdict: %v", view.Losses)
	}

	// 3 — the decisive one: the formula and its column are actually IN the
	// written view file. "No loss was reported" is not the same claim as "the
	// operator gets the column", and only this one is what he asked for.
	written := readWrittenViewFile(t, vault)
	if !strings.Contains(written, "team_name") {
		t.Fatalf("the written view file never mentions team_name, so the column is still missing however clean the report reads:\n%s", written)
	}
	if !strings.Contains(written, "formula.team_name") {
		t.Errorf("the view file names the formula but never references it as a column (`formula.team_name`):\n%s", written)
	}
}

// readWrittenViewFile returns the single view file this run wrote.
func readWrittenViewFile(t *testing.T, vaultRoot string) string {
	t.Helper()
	dir := records.ViewsDir(vaultRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the views this run wrote (%s): %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, e.Name()))
		if readErr != nil {
			t.Fatalf("reading %s: %v", e.Name(), readErr)
		}
		out = append(out, string(data))
	}
	if len(out) == 0 {
		t.Fatalf("this run wrote no view files at all into %s", dir)
	}
	return strings.Join(out, "\n---\n")
}
