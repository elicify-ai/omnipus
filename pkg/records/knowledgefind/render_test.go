// Omnipus — ADR-068 D22 / spec 4.2, AC-P1..P5, FR-072, FR-121..FR-127: the
// response the model reads.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// worked builds the corpus the rendering tests share: eight readable records,
// one whose value cannot be read, and a total over the readable ones.
//
// It is the SHAPE of spec 4.2's worked example — hits, an exclusion, and a
// scoped total — in this repository's own greenhouse vocabulary rather than the
// specification's illustrative CRM one (ADR-068 D0).
func worked(t *testing.T) (Deps, generated.VaultFindRequest) {
	t.Helper()
	f := newFixture(t)
	heights := []string{"180.00", "120.00", "95.00", "88.00", "70.00", "62.00", "58.00", "57.00"}
	for i, h := range heights {
		f.plant(i+1, "growing", h)
	}
	// The one that cannot be read. It is SELECTED by the narrowing and then
	// excluded by the comparator, which is the case the problem block exists for.
	f.write("garden/plant-0052.md", `---
type: plant
id: PL-0052
species: Monstera deliciosa
condition: growing
height_cm: 50k
bed: "[[Bed 1]]"
---
`)

	sel := []string{"condition", "height_cm"}
	sortDesc := generated.VaultFindSortDirection("desc")
	aggs := []generated.VaultFindAggregate{{Op: "sum", Property: strPtr("height_cm")}}
	sorts := []generated.VaultFindSort{{Property: "height_cm", Direction: &sortDesc}}

	r := req(withType("plant"), withFilter(generated.VaultFilterNode{
		All: &[]generated.VaultFilterNode{
			leaf("condition", "=", "growing"),
			leaf("height_cm", ">=", "50"),
		},
	}))
	r.Select = &sel
	r.Sort = &sorts
	r.Aggregate = &aggs
	return f.deps(), r
}

// TestRender_TheWorkedExample prints the literal bytes a model receives and
// asserts every rule spec 4.2 makes about them.
func TestRender_TheWorkedExample(t *testing.T) {
	d, r := worked(t)
	resp := mustFind(t, d, r)
	out := Render(resp)

	t.Logf("\n────────── the response a model reads (%d bytes) ──────────\n%s"+
		"───────────────────────────────────────────────────────────", len(out), out)

	t.Run("completeness comes FIRST", func(t *testing.T) {
		// A caveat that arrives after the evidence arrives after the conclusion.
		if !strings.HasPrefix(out, "COMPLETE: no — ") {
			t.Errorf("the first line is not the verdict and its reason:\n%s", firstLine(out))
		}
		if strings.Index(out, "PROBLEMS") < strings.Index(out, "COMPLETE") {
			t.Errorf("the problem block precedes the verdict")
		}
	})

	t.Run("the exclusion is named INLINE with its fix", func(t *testing.T) {
		if !strings.Contains(out, "PL-0052") {
			t.Errorf("the excluded record is not named:\n%s", out)
		}
		if !strings.Contains(out, `"50k"`) {
			t.Errorf("the problem does not quote what the note holds:\n%s", out)
		}
		if !strings.Contains(out, "where a decimal is required") {
			t.Errorf("the problem does not say what was expected:\n%s", out)
		}
		// "3 records excluded" with a count and no remedy is the failure this
		// block exists to remove.
		if strings.Contains(out, "records excluded") {
			t.Errorf("the problem block reports a COUNT rather than the records:\n%s", out)
		}
	})

	t.Run("the total states its scope in the same sentence", func(t *testing.T) {
		line := lineContaining(out, "TOTALS:")
		if line == "" {
			t.Fatalf("no total was rendered:\n%s", out)
		}
		if !strings.Contains(line, "sum(height_cm)") {
			t.Errorf("the total does not name what it reduced: %q", line)
		}
		if !strings.Contains(line, "evaluated rows") {
			t.Errorf("the total does not state its scope: %q. A total that does not say "+
				"what it covers is a bare number.", line)
		}
		// FR-125a — over the EVALUATED set, never the page. 730 is the eight
		// readable heights; the broken one contributes nothing and is named.
		if !strings.Contains(line, "730") {
			t.Errorf("the total is not the sum over the evaluated set: %q", line)
		}
	})

	t.Run("it ends with addressable next actions", func(t *testing.T) {
		i := strings.Index(out, "\nNEXT\n")
		if i < 0 {
			t.Fatalf("no NEXT block:\n%s", out)
		}
		tail := out[i:]
		if !strings.Contains(tail, "knowledge_") {
			t.Errorf("the NEXT block contains no issuable call: %q", tail)
		}
	})

	t.Run("it is text, never JSON", func(t *testing.T) {
		// FR-072. A rendered response that opened a JSON object would be the
		// ~91% context-token regression this projection exists to avoid.
		if strings.Contains(out, `{"`) || strings.Contains(out, `":`) {
			t.Errorf("the rendered response contains JSON:\n%s", out)
		}
	})
}

// TestRender_BorrowedValuesAreMarkedAsBorrowed is FR-124.
//
// A borrowed value must never render as a column of this record. The assertion
// is on the SHAPE — `relation [[target]]: property value` — because that is what
// makes it unmistakable to a reader.
func TestRender_BorrowedValuesAreMarkedAsBorrowed(t *testing.T) {
	resp := generated.VaultFindResponse{
		Complete: true, QueryEcho: "type=plant",
		Counts: generated.VaultFindCounts{Selected: 1, Evaluated: 1, Shown: 1},
		Rows: []generated.VaultFindRow{{
			Id: strPtr("PL-0001"), Path: "garden/a.md", Title: "a",
			Cells: []generated.VaultFindCell{{Property: "condition", Value: "growing"}},
			Joins: []generated.VaultFindJoin{{
				Relation: "bed", Target: "[[Bed 1]]",
				Cells: []generated.VaultFindCell{{Property: "aspect", Value: "south"}},
			}},
		}},
		Totals: []generated.VaultFindTotal{}, Problems: []generated.RecordProblem{},
		Next: []generated.VaultFindAction{{Label: "read", Call: "knowledge_read path=\"garden/a.md\""}},
	}
	out := Render(resp)
	if !strings.Contains(out, "bed [[Bed 1]]: aspect south") {
		t.Errorf("a borrowed value does not render as borrowed:\n%s", out)
	}
	// The borrowed property must not appear as one of the record's own columns.
	row := lineContaining(out, "PL-0001")
	own := row[:strings.Index(row, "bed [[Bed 1]]")]
	if strings.Contains(own, "aspect") {
		t.Errorf("the borrowed property leaked into the record's own columns: %q", row)
	}
}

// TestRender_BudgetIsMeasuredInBytes is AC-P4.
//
// The measurement here is the SAME UNIT the implementation enforces. A test that
// counted tokens would fail this criterion even if it passed, because a token
// count is unenforceable without naming a tokenizer.
func TestRender_BudgetIsMeasuredInBytes(t *testing.T) {
	f := newFixture(t)
	for i := 1; i <= 120; i++ {
		f.plant(i, "growing", fmt.Sprintf("%d.00", i))
	}
	limit := 200
	r := req(withType("plant"))
	r.Limit = &limit

	resp := mustFind(t, f.deps(), r)
	out := Render(resp)

	if n := len(out); n > ResponseBudgetBytes {
		t.Errorf("the rendered response is %d bytes, over the %d-byte cap", n, ResponseBudgetBytes)
	}
	if resp.Elided == nil || *resp.Elided == 0 {
		t.Fatalf("120 records inside a %d-byte budget elided nothing", ResponseBudgetBytes)
	}
	// The shortfall is in the HEADER, so a reader learns the answer is partial
	// before reading a single row.
	if !strings.Contains(firstLine(out), "shown") {
		t.Errorf("the header does not say how many of how many were shown: %q", firstLine(out))
	}
	if !strings.Contains(out, "not shown") {
		t.Errorf("the elided rows are not accounted for:\n%s", out)
	}
	// counts.shown must equal the rows actually rendered — the wire object and
	// the text must not disagree about what came back.
	if resp.Counts.Shown != len(resp.Rows) {
		t.Errorf("counts.shown=%d rows=%d", resp.Counts.Shown, len(resp.Rows))
	}
}

// TestRender_MinimalKeepsTheCaveatAndDropsTheColumns is FR-127.
func TestRender_MinimalKeepsTheCaveatAndDropsTheColumns(t *testing.T) {
	d, r := worked(t)
	minimal := generated.VaultFindRequestDetail("minimal")
	r.Detail = &minimal

	resp := mustFind(t, d, r)
	out := Render(resp)
	t.Logf("\n────────── detail=minimal (%d bytes) ──────────\n%s"+
		"──────────────────────────────────────────────", len(out), out)

	if !strings.HasPrefix(out, "COMPLETE: no") {
		t.Errorf("minimal dropped the verdict, which is the one thing a shorter answer "+
			"must not lose:\n%s", out)
	}
	if !strings.Contains(out, "PROBLEMS (") {
		t.Errorf("minimal dropped the problem count:\n%s", out)
	}
	for _, row := range resp.Rows {
		if len(row.Cells) != 0 {
			t.Errorf("minimal kept the columns: %+v", row.Cells)
		}
	}
}

// TestRender_ZeroValuedWireObjectProducesNoUnsourcedLiteral is AC-P3.
//
// Every field the renderer reads MUST be reachable from the generated wire type.
// Rendering a ZERO-VALUED response and checking that every word in the output is
// a constant of the renderer catches the defect the criterion is for: a renderer
// that reaches around the contract — a second lookup, a store call, a computed
// value not on the wire — produces a literal with no source.
func TestRender_ZeroValuedWireObjectProducesNoUnsourcedLiteral(t *testing.T) {
	out := Render(generated.VaultFindResponse{})

	// The complete vocabulary this renderer may emit from a zero value. Every
	// one of them is a literal in render.go.
	allowed := map[string]bool{
		"COMPLETE:": true, "no": true, "—": true, "0": true, "records": true,
		"matched": true, "QUERY:": true,
	}
	for _, word := range strings.Fields(out) {
		if !allowed[word] {
			t.Errorf("the renderer emitted %q from a ZERO-VALUED wire object. "+
				"Every field it reads must be reachable from generated.VaultFindResponse; "+
				"a literal with no source means the renderer computed something the "+
				"contract does not carry.\nfull output:\n%s", word, out)
		}
	}
}

// TestRender_NoResponseEverEndsWithoutNext is FR-126, over every shape this
// package produces.
func TestRender_NoResponseEverEndsWithoutNext(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.text.terms = []generated.VaultTermCount{{Term: "monstera", Documents: 4}}

	words := "nothingmatchesthis"
	zero := req(withType("plant"))
	zero.Words = &words

	explain := req(withType("plant"), withFilter(leaf("condition", "=", "growing")))
	yes := true
	explain.Explain = &yes

	for name, r := range map[string]generated.VaultFindRequest{
		"rows":    req(withType("plant")),
		"zero":    zero,
		"explain": explain,
	} {
		t.Run(name, func(t *testing.T) {
			out := Render(mustFind(t, f.deps(), r))
			if !strings.Contains(out, "\nNEXT\n") {
				t.Errorf("no NEXT block; in an agentic loop every response is the prompt "+
					"for the next call:\n%s", out)
			}
		})
	}
}

func lineContaining(out, needle string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// TestRender_RefusalArtifact publishes the literal bytes a model receives when
// its query is refused.
//
// It is a rendered ARTIFACT rather than a set of substring assertions because
// the refusal path is where the response format earns its keep: a refusal the
// model cannot read is a refusal it cannot act on, and the next call then gets
// composed out of prose.
func TestRender_RefusalArtifact(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")

	resp := mustRefuse(t, f.deps(), req(withType("plant"), withFilter(leaf("conditon", "=", "growing"))))
	out := Render(resp)
	t.Logf("\n────────── a refused query, as the model reads it ──────────\n%s"+
		"────────────────────────────────────────────────────────────", out)

	// The three things that make it actionable, asserted rather than admired.
	if !strings.Contains(out, "conditon") {
		t.Errorf("the refusal does not quote what the caller wrote:\n%s", out)
	}
	if !strings.Contains(out, "condition") {
		t.Errorf("the refusal does not offer the name that would have worked:\n%s", out)
	}
	if !strings.Contains(out, "knowledge_describe") {
		t.Errorf("the refusal does not give the caller a call to make next:\n%s", out)
	}
}
