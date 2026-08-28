// Omnipus — spec FR-073, FR-114, FR-115, AC-F3, AC-F4, FR-076a: the four
// responses that are not an ordinary row set.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultfind

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// refusalResponse is what the model reads when the query was refused.
//
// It is a COMPLETE response, not a husk: the verdict, the echo, the problem with
// its remedy, and next actions. `refused: true` with `complete: false` is the
// pair a caller branches on — "here is none of it, narrow and re-ask" as
// distinct from "here is some of it".
func refusalResponse(_ generated.VaultFindRequest, echo string, r *RefusalError) generated.VaultFindResponse {
	resp := generated.VaultFindResponse{
		Complete:       false,
		CompleteReason: str("the query was refused; no records were evaluated"),
		Refused:        true,
		QueryEcho:      echo,
		Counts:         generated.VaultFindCounts{},
		Rows:           []generated.VaultFindRow{},
		Totals:         []generated.VaultFindTotal{},
		Problems:       []generated.RecordProblem{r.Problem},
		Next:           []generated.VaultFindAction{},
	}
	resp.Next = refusalActions(r)
	return resp
}

// refusalActions turns a refusal's remedy into a call the caller can issue.
//
// A remedy that is only prose ends the loop: the model has to compose the next
// call from a sentence, and composing it is where it invents a property name.
// So every refusal offers vault_describe — the orientation call that answers
// "what is actually declared here" — alongside the specific remedy.
func refusalActions(r *RefusalError) []generated.VaultFindAction {
	out := []generated.VaultFindAction{}
	// ONLY A REAL CALL BELONGS HERE. The fix prose is already rendered inline
	// with the problem; repeating it under NEXT filled the block with sentences
	// like "call vault_describe to see the declared properties", which is advice
	// wearing the shape of a command and leaves the model to compose the call
	// itself — the exact step FR-126 exists to remove.
	if fix := deref(r.Problem.Fix); strings.HasPrefix(fix, "vault_") {
		out = append(out, generated.VaultFindAction{Label: "fix", Call: fix})
	}
	switch r.Problem.Code {
	case generated.UnknownProperty, generated.UnknownEnumValue, generated.UnknownRecordType:
		out = append(out, generated.VaultFindAction{Label: "describe", Call: "vault_describe"})
	case generated.EvaluationBoundExceeded, generated.CandidateCapExceeded:
		out = append(out, generated.VaultFindAction{
			Label: "total", Call: "vault_find aggregate=[{op:count}] (an aggregate-only query returns no rows)"})
	case generated.StaleCursor:
		out = append(out, generated.VaultFindAction{Label: "restart", Call: "vault_find (without cursor)"})
	default:
		out = append(out, generated.VaultFindAction{Label: "describe", Call: "vault_describe"})
	}
	return out
}

// ---------------------------------------------------------------------------
// EXPLAIN — FR-073, AC-F3
// ---------------------------------------------------------------------------

// explainResponse reports the plan and EVALUATES NOTHING.
//
// Three properties are load-bearing, and each exists because a weaker version of
// this criterion passed for a stub:
//
//  1. It names EVERY property the query touches and the index each would be
//     answered from — so an implementation that returns a constant returns the
//     WRONG thing rather than nothing.
//  2. It performs ZERO candidate retrievals. Note what this function does not
//     take: it has no Deps, so it cannot reach a store even by accident. That is
//     the assertion made structural rather than tested.
//  3. It carries NO index_epoch. An explain evaluates nothing, so it observes no
//     epoch — and two explain calls over an unchanged schema are therefore
//     byte-identical, including across a corpus mutation chosen to change the
//     plan if evaluation were happening.
func explainResponse(q *query, echo string) generated.VaultFindResponse {
	var plan []generated.VaultFindPlanStep

	plan = append(plan, generated.VaultFindPlanStep{
		Stage:  generated.Scope,
		Source: sourcePtr(generated.VaultFindPlanStepSourceSchema),
		Detail: "workspace scope is resolved from the calling agent, never from an argument",
	})

	narrow := []string{}
	if q.recordType != "" {
		narrow = append(narrow, "record type = "+q.recordType)
	}
	narrow = append(narrow, "note kind = "+q.kind)
	plan = append(plan, generated.VaultFindPlanStep{
		Stage:  generated.Narrow,
		Source: sourcePtr(generated.VaultFindPlanStepSourcePropertiesIndex),
		Detail: "narrowed on " + strings.Join(narrow, ", ") +
			" — set membership over indexed columns only; no comparison is pushed down",
	})

	if q.words != "" {
		plan = append(plan, generated.VaultFindPlanStep{
			Stage:  generated.Retrieve,
			Source: sourcePtr(generated.VaultFindPlanStepSourceTextIndex),
			Detail: fmt.Sprintf("plain words %q ranked, then INTERSECTED with the typed set", q.words),
		})
	}

	if q.near != "" {
		plan = append(plan, generated.VaultFindPlanStep{
			Stage:  generated.Retrieve,
			Source: sourcePtr(generated.VaultFindPlanStepSourcePropertiesIndex),
			Detail: fmt.Sprintf("relation graph walked from %s, undirected, up to %d hop(s) — "+
				"the reachable set is then INTERSECTED with the typed set, exactly like words",
				q.near, q.hops),
		})
	}

	if q.filter != nil {
		for _, leaf := range q.filter.leaves() {
			p := leaf.Property
			plan = append(plan, generated.VaultFindPlanStep{
				Stage:    generated.Compare,
				Property: str(p.Name),
				Source:   sourcePtr(generated.VaultFindPlanStepSourceGoComparator),
				Detail: fmt.Sprintf("%s %s evaluated in Go over the %s column",
					p.Name, string(leaf.Filter.Op), p.Type),
			})
		}
	}
	for _, j := range q.join {
		plan = append(plan, generated.VaultFindPlanStep{
			Stage:    generated.Join,
			Property: str(j),
			Source:   sourcePtr(generated.VaultFindPlanStepSourcePropertiesIndex),
			Detail:   "columns borrowed through the relation " + j + ", rendered as borrowed",
		})
	}
	for _, g := range q.groupBy {
		plan = append(plan, generated.VaultFindPlanStep{
			Stage:    generated.Group,
			Property: str(g),
			Source:   sourcePtr(generated.VaultFindPlanStepSourceGoComparator),
			Detail:   "grouped on " + g + " by folded key; a record with several values joins several groups",
		})
	}
	for _, s := range q.sort {
		dir := "asc"
		if s.desc {
			dir = "desc"
		}
		plan = append(plan, generated.VaultFindPlanStep{
			Stage:    generated.Sort,
			Property: str(s.property),
			Source:   sourcePtr(generated.VaultFindPlanStepSourceGoComparator),
			Detail:   "ordered " + dir + " in Go; no ORDER BY is emitted, and an enum orders on its folded form",
		})
	}
	for _, a := range q.aggregates {
		step := generated.VaultFindPlanStep{
			Stage:  generated.Aggregate,
			Source: sourcePtr(generated.VaultFindPlanStepSourceGoComparator),
			Detail: a.label() + " computed over the full evaluated set, never the rendered page",
		}
		if a.property != "" {
			step.Property = str(a.property)
		}
		plan = append(plan, step)
	}

	plan = append(plan, generated.VaultFindPlanStep{
		Stage:  generated.Render,
		Source: sourcePtr(generated.VaultFindPlanStepSourceNone),
		Detail: fmt.Sprintf("up to %d rows as compact text, completeness verdict first", q.limit),
	})

	return generated.VaultFindResponse{
		Complete:  true,
		Refused:   false,
		QueryEcho: echo,
		Counts:    generated.VaultFindCounts{},
		Rows:      []generated.VaultFindRow{},
		Totals:    []generated.VaultFindTotal{},
		Problems:  []generated.RecordProblem{},
		Plan:      &plan,
		Next: []generated.VaultFindAction{
			{Label: "run", Call: "vault_find " + echo + " (drop explain to evaluate)"},
		},
	}
}

func sourcePtr(s generated.VaultFindPlanStepSource) *generated.VaultFindPlanStepSource { return &s }

// ---------------------------------------------------------------------------
// ZERO HITS — FR-114, FR-115, AC-F4
// ---------------------------------------------------------------------------

// zeroHitResponse never renders as an empty success with nothing else in it.
//
// It reports the vocabulary the index ACTUALLY HOLDS and STOPS there. It does
// not expand the query on the caller's behalf: a user who searched for one thing
// and received results for a broader thing has been given a wrong answer with no
// error channel, and no amount of helpfulness makes that recoverable.
//
// The verdict is `complete: yes — 0 records`. That is AC-F4, and it carries the
// one stated exception to the completeness guarantee: a workspace-scoped miss is
// indistinguishable from an empty vault, deliberately, so the error channel
// cannot be used to probe for records the caller may not see.
func zeroHitResponse(ctx context.Context, d Deps, q *query, echo string) generated.VaultFindResponse {
	resp := generated.VaultFindResponse{
		Complete:  true,
		Refused:   false,
		QueryEcho: echo,
		Counts:    generated.VaultFindCounts{},
		Rows:      []generated.VaultFindRow{},
		Totals:    []generated.VaultFindTotal{},
		Problems:  []generated.RecordProblem{},
		Next:      []generated.VaultFindAction{},
	}

	var terms []generated.VaultTermCount
	if d.Text != nil && q.words != "" {
		if t, err := d.Text.NearestTerms(ctx, q.words, 5); err == nil {
			terms = t
		}
	}
	if len(terms) > 0 {
		resp.NearestTerms = &terms
		resp.Next = append(resp.Next, generated.VaultFindAction{
			Label: "retry", Call: `vault_find words="` + terms[0].Term + `"`,
		})
	}
	if q.schema != nil {
		resp.Next = append(resp.Next, generated.VaultFindAction{
			Label: "describe", Call: "vault_describe record_type=" + q.schema.Type,
		})
	} else {
		resp.Next = append(resp.Next, generated.VaultFindAction{
			Label: "describe", Call: "vault_describe",
		})
	}
	return resp
}

// ---------------------------------------------------------------------------
// kind: task — FR-076a, AC-F7
// ---------------------------------------------------------------------------

// findTasks returns checkbox ROWS, not notes.
//
// NO COLLECTION WALK OCCURS. `knowledge_tasks` walked the collection, read every
// file and re-matched a regex per line on every call, which is why it needed a
// 5,000-file read cap. Checkboxes are indexed now, so this streams the same
// child table every other row streams — and the freshness comparison, the
// bounds, the paging and the rendering all apply unchanged.
func findTasks(ctx context.Context, d Deps, q *query, echo string) (generated.VaultFindResponse, error) {
	if d.Store == nil {
		ref := refuse(problem(generated.IndexUnavailable,
			"the properties index is not open, so checkbox rows cannot be read",
			"re-open the vault; run vault_describe check_integrity to see the index state"), nil)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}

	sel := q.selector(d.PathPrefix)
	total, err := d.Store.CountCandidates(ctx, sel)
	if err != nil {
		ref := refuse(problem(generated.IndexUnavailable,
			fmt.Sprintf("the properties index could not count candidates: %v", err),
			"run vault_describe check_integrity"), err)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}
	if total > propindex.BoundNarrowedCandidates {
		ref := refuse(problem(generated.EvaluationBoundExceeded,
			fmt.Sprintf("this query would evaluate %s notes for checkbox rows; the limit is %s",
				group3(total), group3(propindex.BoundNarrowedCandidates)),
			"narrow the scope to a collection or path"), nil)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}

	var hits []propindex.TaskHit
	seen := 0
	err = d.Store.Tasks(ctx, sel, func(h propindex.TaskHit) error {
		seen++
		if seen > propindex.BoundSurvivors {
			return &propindex.BoundError{
				Bound: "B2", Count: seen, Limit: propindex.BoundSurvivors,
				Remedy: "narrow the scope to a collection or path",
			}
		}
		hits = append(hits, h)
		return nil
	})
	if err != nil {
		if propindex.IsBoundExceeded(err) {
			ref := refuse(problem(generated.CandidateCapExceeded,
				fmt.Sprintf("this query matched more than %s checkbox rows; the limit is %s",
					group3(propindex.BoundSurvivors), group3(propindex.BoundSurvivors)),
				"narrow the scope to a collection or path"), err)
			return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
		}
		ref := refuse(problem(generated.IndexUnavailable,
			fmt.Sprintf("the properties index could not stream checkbox rows: %v", err),
			"run vault_describe check_integrity"), err)
		return refusalResponse(generated.VaultFindRequest{}, echo, ref), ref
	}

	evaluated := len(hits)
	offset := cursorOffset(q.cursor)
	page := hits
	if offset > 0 && offset < len(page) {
		page = page[offset:]
	} else if offset >= len(page) && offset > 0 {
		page = nil
	}
	if len(page) > q.limit {
		page = page[:q.limit]
	}

	rows := make([]generated.VaultFindRow, 0, len(page))
	for _, h := range page {
		line := h.Task.Line
		status := generated.VaultFindRowStatus(h.Task.Status)
		rows = append(rows, generated.VaultFindRow{
			Path: h.Path,
			// The TITLE is the note; the TEXT is the checkbox. Keeping them in
			// separate fields is what stops a task row reading as a note — the
			// narrow amendment to "a row is one note" that FR-076a makes.
			Title:  titleOf(h.Path),
			Line:   &line,
			Status: &status,
			Text:   str(h.Task.Text),
			Cells:  []generated.VaultFindCell{},
			Joins:  []generated.VaultFindJoin{},
		})
	}

	resp := generated.VaultFindResponse{
		Complete:  true,
		Refused:   false,
		QueryEcho: echo,
		Counts: generated.VaultFindCounts{
			// Every indexed checkbox row is readable by construction, so
			// selected and evaluated are the same number here. `total` is the
			// NOTE population B1 bounded, which is a different quantity and
			// would read as "rows we could not evaluate" if it were used.
			Selected: evaluated, Evaluated: evaluated, Shown: len(rows),
		},
		Rows:     rows,
		Totals:   []generated.VaultFindTotal{},
		Problems: []generated.RecordProblem{},
		Index:    &generated.VaultIndexState{Returned: len(rows), Agreeing: len(rows), Epoch: &d.Epoch},
	}
	applied := q.limit
	resp.LimitApplied = &applied
	if q.clamped {
		resp.LimitClamped = boolPtr(true)
		asked := q.limitAsked
		resp.LimitRequested = &asked
	}
	if consumed := offset + len(rows); consumed < evaluated {
		c := encodeCursor(consumed, d.Epoch)
		resp.NextCursor = &c
	}
	trimToBudget(&resp)
	finishVerdict(&resp, q)
	resp.Next = nextActions(q, &resp)
	return resp, nil
}

var _ = records.StateAbsent
