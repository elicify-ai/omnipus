// Omnipus — ADR-068 D22 / spec 4.2, FR-072, FR-121..FR-127: the compact-text
// projection the model actually reads.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ---------------------------------------------------------------------------
// THIS FILE IS HALF THE FEATURE, NOT A PRESENTATION LAYER
//
// Moving results from inline text to a file collapsed measured agent accuracy
// from 93.1% to 55.2% — as large a swing as replacing the retriever outright.
// So the projection below is a set of REQUIREMENTS, and each line of it answers
// a specific way a reader gets a wrong answer from a correct response:
//
//   COMPLETENESS FIRST   a caveat at the bottom arrives after the conclusion has
//                        already formed. It goes in the first line, before any
//                        evidence.
//   QUERY AS EXECUTED    a clamp or a default the caller cannot see is a
//                        constraint they believe they applied and did not.
//   BORROWED IS MARKED   `company [[Acme Ltd]]: status active` — never merged
//                        into the row's own columns, because it is not a
//                        property of this record.
//   TOTALS CARRY SCOPE   in the same sentence as the number, so a reader cannot
//                        acquire the figure without the qualification.
//   PROBLEMS CARRY FIXES "arr is '50k' where a decimal is required — write
//                        50000", never "3 records excluded".
//   NEXT IS ADDRESSABLE  in an agentic loop every response is the prompt for the
//                        next call. A response with no next call makes the model
//                        invent arguments, and an invented property name is the
//                        failure the schema check exists to prevent.
//
// THE BUDGET IS IN BYTES, NOT TOKENS. A token cap is unenforceable without
// naming a tokenizer, and naming one would make the bound depend on a model. The
// measurement here and the measurement in the tests are the same unit.
//
// AND THE RENDERER READS ONLY THE WIRE OBJECT (AC-P3). Every value below comes
// from a field of generated.VaultFindResponse or is a constant of this file.
// There is no second lookup, no store call and no recomputation — a renderer
// that reaches around the contract produces a literal with no source, and the
// zero-value test catches it.
// ---------------------------------------------------------------------------

const (
	// ResponseBudgetBytes is the hard cap on a rendered response, in BYTES of
	// UTF-8. Roughly a thousand tokens of English, which is the stated target;
	// bytes are what is actually enforceable.
	ResponseBudgetBytes = 4000

	// minRenderedRows is the floor the budget may not trim past. A response that
	// showed zero rows because the problem list was long would be technically
	// within budget and useless — the caller would see only what failed.
	minRenderedRows = 3

	// minRenderedProblems is the same floor for the OTHER unbounded list.
	//
	// trimToBudget used to remove rows and only rows, so the budget was a cap on
	// half the response. The problem list has no cap of its own — the freshness
	// loop appends one entry per stale row directly, bypassing recordProblems'
	// dedup, and that dedup keys on record+code+property anyway, so distinct
	// records never collapse. With limit=200 over a stale corpus the response ran
	// to roughly two hundred problem lines against a stated hard cap of 4,000
	// bytes, and the one budget test used a fixture with `Problems: []`.
	//
	// The floor is three for the same reason the row floor is: a reader has to
	// see the KIND of thing that went wrong, and a response that reported only
	// "200 problems not shown" would name nothing to act on.
	minRenderedProblems = 3
)

// Render projects the response into the compact text a model reads (FR-072).
//
// It never emits JSON. The wire type stays contract-defined because Hard
// Constraint #8 requires it; what crosses into the model's context is this.
func Render(resp generated.VaultFindResponse) string {
	var b strings.Builder
	writeHeader(&b, resp)
	writeRows(&b, resp)
	writeTotals(&b, resp)
	writeGroups(&b, resp)
	writePlan(&b, resp)
	writeProblems(&b, resp)
	writeNext(&b, resp)
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// writeHeader is FR-121: the verdict precedes the evidence.
func writeHeader(b *strings.Builder, r generated.VaultFindResponse) {
	verdict := "yes"
	if !r.Complete {
		verdict = "no"
	}
	b.WriteString("COMPLETE: " + verdict)

	switch {
	case r.Refused:
		b.WriteString(" — REFUSED, nothing was evaluated")
	case r.CompleteReason != nil && *r.CompleteReason != "":
		b.WriteString(" — " + *r.CompleteReason)
	case r.Counts.Evaluated == 0:
		// "0 records matched" and "0 records were returned because we gave up"
		// must not read the same. This branch is only reached when the verdict
		// is yes, so it is genuinely the first.
		b.WriteString(" — 0 records matched")
	default:
		b.WriteString(fmt.Sprintf(" — %d of %d shown", r.Counts.Shown, r.Counts.Evaluated))
	}
	if r.NextCursor != nil && *r.NextCursor != "" {
		b.WriteString(" (more: cursor " + *r.NextCursor + ")")
	}
	b.WriteString("\n")

	b.WriteString("QUERY: " + r.QueryEcho + "\n")

	if r.Index != nil {
		b.WriteString(fmt.Sprintf("INDEX: %d of %d returned records agree across both indexes",
			r.Index.Agreeing, r.Index.Returned))
		if r.Index.Epoch != nil {
			b.WriteString("; index_epoch " + strconv.FormatInt(*r.Index.Epoch, 10))
		}
		b.WriteString("\n")
	}

	if r.NearestTerms != nil && len(*r.NearestTerms) > 0 {
		parts := make([]string, 0, len(*r.NearestTerms))
		for _, t := range *r.NearestTerms {
			parts = append(parts, fmt.Sprintf("%s (%d)", t.Term, t.Documents))
		}
		// The vocabulary the index HOLDS, reported instead of broadening the
		// query. The system does not re-ask on the caller's behalf (FR-114).
		b.WriteString("NEAREST INDEXED TERMS: " + strings.Join(parts, ", ") + "\n")
	}
}

func writeRows(b *strings.Builder, r generated.VaultFindResponse) {
	if len(r.Rows) == 0 {
		return
	}
	b.WriteString("\n")

	idW, titleW := 0, 0
	for _, row := range r.Rows {
		if n := len(rowID(row)); n > idW {
			idW = n
		}
		if n := len(row.Title); n > titleW {
			titleW = n
		}
	}
	for _, row := range r.Rows {
		b.WriteString(renderRowLine(row, idW, titleW))
		b.WriteString("\n")
	}

	if r.Elided != nil && *r.Elided > 0 {
		b.WriteString(fmt.Sprintf("… %d more row(s) evaluated, not shown — beyond the response budget",
			*r.Elided))
		if r.ElidedSummary != nil && *r.ElidedSummary != "" {
			b.WriteString(" (" + *r.ElidedSummary + ")")
		}
		b.WriteString("\n")
	}
}

// rowID is the identifier a row is addressed by. An ordinary note carries none
// (FR-005), and falling back to the path is the normal case rather than a
// degraded one — a row a caller cannot name is a row that ends the loop.
func rowID(row generated.VaultFindRow) string {
	if row.Id != nil && *row.Id != "" {
		return *row.Id
	}
	return row.Path
}

func renderRowLine(row generated.VaultFindRow, idW, titleW int) string {
	var b strings.Builder
	b.WriteString(pad(rowID(row), idW))

	// A TASK ROW renders its line number, so a reader is never able to mistake
	// it for the note that contains it (FR-076a's narrow amendment).
	if row.Line != nil {
		status := ""
		if row.Status != nil {
			status = string(*row.Status)
		}
		text := ""
		if row.Text != nil {
			text = *row.Text
		}
		b.WriteString(fmt.Sprintf(":%d  [%s]  %s", *row.Line, status, text))
		return b.String()
	}

	b.WriteString("  " + pad(row.Title, titleW))
	for _, c := range row.Cells {
		b.WriteString("  " + c.Property + " " + c.Value)
	}
	// BORROWED, VISIBLY. `company [[Acme Ltd]]: status active` — never merged
	// into the columns above, because it is not a property of this record and a
	// reader who takes it for one has been told something false.
	for _, j := range row.Joins {
		b.WriteString("  " + j.Relation + " " + j.Target + ":")
		for _, c := range j.Cells {
			b.WriteString(" " + c.Property + " " + c.Value)
		}
	}
	return b.String()
}

func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

// writeTotals is FR-125: the scope travels in the same sentence as the number.
func writeTotals(b *strings.Builder, r generated.VaultFindResponse) {
	if len(r.Totals) == 0 {
		return
	}
	b.WriteString("\n")
	for _, t := range r.Totals {
		if t.Refused != nil && *t.Refused {
			b.WriteString("TOTALS: " + t.Label + " — " + t.Scope + "\n")
			continue
		}
		b.WriteString("TOTALS: " + t.Label + " = " + t.Value + " " + t.Scope + "\n")
	}
}

func writeGroups(b *strings.Builder, r generated.VaultFindResponse) {
	if r.Groups == nil || len(*r.Groups) == 0 {
		return
	}
	b.WriteString("\nGROUPS\n")
	for _, g := range *r.Groups {
		b.WriteString("  " + g.Property + " " + groupLabel(g.Key, g.Absent) +
			fmt.Sprintf("  %d row(s)\n", g.Count))
		if g.Subgroups == nil {
			continue
		}
		for _, s := range *g.Subgroups {
			b.WriteString("    " + s.Property + " " + groupLabel(s.Key, s.Absent) +
				fmt.Sprintf("  %d row(s)\n", s.Count))
		}
	}
}

// groupLabel names the absent group rather than rendering it as an empty
// string. An empty string is itself a value (R-3), so the two must not collide:
// "(no value)" is a group of records, and "" is a group of records that hold "".
func groupLabel(key string, absent *bool) string {
	if absent != nil && *absent {
		return "(no value)"
	}
	if key == "" {
		return `""`
	}
	return key
}

func writePlan(b *strings.Builder, r generated.VaultFindResponse) {
	if r.Plan == nil || len(*r.Plan) == 0 {
		return
	}
	b.WriteString("\nPLAN (explain: nothing was evaluated)\n")
	for _, s := range *r.Plan {
		b.WriteString("  " + pad(string(s.Stage), 9))
		if s.Property != nil {
			b.WriteString(pad(*s.Property, 12))
		} else {
			b.WriteString(pad("-", 12))
		}
		if s.Source != nil {
			b.WriteString(pad(string(*s.Source), 18))
		}
		b.WriteString(s.Detail + "\n")
	}
}

// writeProblems is FR-025/FR-026: one record, one reason, one FIX, per line.
//
// The fix is on the SAME LINE as the reason, deliberately. A problem list that
// states causes and defers remedies to a footnote makes the reader hold two
// places in their head, and in an agentic loop it makes the model compose the
// next call out of prose.
func writeProblems(b *strings.Builder, r generated.VaultFindResponse) {
	if len(r.Problems) == 0 {
		return
	}
	b.WriteString(fmt.Sprintf("\nPROBLEMS (%d)\n", len(r.Problems)))
	for _, p := range r.Problems {
		b.WriteString("  ")
		if len(p.Records) > 0 {
			b.WriteString(strings.Join(p.Records, ", ") + "  ")
		}
		b.WriteString(p.Reason)
		if p.Fix != nil && *p.Fix != "" {
			b.WriteString(" — " + *p.Fix)
		}
		b.WriteString("\n")
	}
}

func writeNext(b *strings.Builder, r generated.VaultFindResponse) {
	if len(r.Next) == 0 {
		return
	}
	b.WriteString("\nNEXT\n")
	w := 0
	for _, a := range r.Next {
		if len(a.Label) > w {
			w = len(a.Label)
		}
	}
	for _, a := range r.Next {
		b.WriteString("  " + pad(a.Label, w) + "  " + a.Call + "\n")
	}
}

// ---------------------------------------------------------------------------
// THE BUDGET
// ---------------------------------------------------------------------------

// trimToBudget drops rows until the RENDERED response fits, and states what it
// dropped.
//
// It runs before the response is returned rather than inside Render, so that
// `counts.shown` and `len(rows)` are the same number on the wire. Trimming
// inside the renderer would leave the wire object claiming rows the text does
// not contain — a response whose two halves disagree about what it returned.
//
// It measures the whole rendered response, not an estimate per row, because the
// header, the totals, the problem list and the next block are all part of what
// the model receives and a per-row estimate ignores every one of them.
func trimToBudget(resp *generated.VaultFindResponse) {
	if len(Render(*resp)) <= ResponseBudgetBytes {
		return
	}
	carried := 0
	if resp.Elided != nil {
		carried = *resp.Elided
	}
	dropped := 0
	var summary []string

	// THE ACCOUNTING IS UPDATED INSIDE THE LOOP, not after it.
	//
	// Setting it afterwards measured a response that did not yet contain the
	// "… N more rows" line, then added roughly sixty bytes of it and returned
	// over budget — measured at 4,126 against a 4,000-byte cap. A budget check
	// that does not measure the thing it is about to add is not a budget check.
	for len(resp.Rows) > minRenderedRows && len(Render(*resp)) > ResponseBudgetBytes {
		last := resp.Rows[len(resp.Rows)-1]
		resp.Rows = resp.Rows[:len(resp.Rows)-1]
		if len(summary) < 5 {
			summary = append(summary, rowID(last))
		}
		dropped++

		resp.Counts.Shown = len(resp.Rows)
		total := carried + dropped
		resp.Elided = &total
		s := strings.Join(summary, " · ")
		if dropped > len(summary) {
			s += " · …"
		}
		resp.ElidedSummary = &s
	}

	trimProblemsToBudget(resp)
}

// trimProblemsToBudget is the same trim for the other unbounded list.
//
// It runs AFTER the rows because rows are the answer and problems are the
// caveat: a response that dropped every row to make room for two hundred
// freshness lines would be within budget and would have thrown away what the
// caller asked for. The elision is STATED — a problem list silently cut short
// is the same class of defect as a silently truncated row set, and it is worse
// here because a reader who sees three problems concludes there are three.
func trimProblemsToBudget(resp *generated.VaultFindResponse) {
	kept := resp.Problems
	omitted := 0
	for len(kept) > minRenderedProblems && len(Render(*resp)) > ResponseBudgetBytes {
		kept = kept[:len(kept)-1]
		omitted++
		trimmed := make([]generated.RecordProblem, 0, len(kept)+1)
		trimmed = append(trimmed, kept...)
		trimmed = append(trimmed, problemsElided(omitted))
		resp.Problems = trimmed
	}
}

// problemsElided names what the budget removed, and what to do to see it.
func problemsElided(n int) generated.RecordProblem {
	return problem(generated.ScopeTruncated,
		fmt.Sprintf("%d further problem(s) not shown — the response budget is %d bytes",
			n, ResponseBudgetBytes),
		"re-run with a smaller limit, or narrow the query, to see the rest")
}
