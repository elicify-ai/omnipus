// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// dod_distinct.go implements the DISTINCTNESS half of the task
// Definition-of-Done rule (GOAL-FR-021/FR-047/FR-048, operator decision D-C).
//
// The count half — at least one acceptance criterion and at least one
// definition-of-done item — was enforced from the start, on every creation and
// edit surface. The distinctness half was only ever ADVERTISED, in the very
// sentence the product uses to refuse a missing DoD:
//
//	"dod is required: a task must have at least one definition-of-done item,
//	 distinct from its acceptance criteria (GOAL-FR-021/FR-048)"
//
// A UAT tester pasted the same sentence into both boxes and the task saved
// with HTTP 200. This file is the check that sentence describes.
//
// WHY IT MATTERS, not just that the message said so: since commit fe4f42f2 the
// judge scores the UNION of Criteria and DoD, and the overall verdict is an
// AND-fold over the per-criterion results. A DoD item that restates a
// criterion therefore spends a second judged slot on a sentence already being
// scored, and contributes nothing a reader could act on. The DoD exists to say
// something the acceptance criteria do not.
//
// This rule lives in pkg/task rather than in any one handler because all three
// task-authoring surfaces must apply the same rule — the REST handler
// (pkg/gateway/rest_tasks.go), the create_task/update_task tools
// (pkg/tools/task.go) and their cross-workspace twins
// (pkg/sysagent/tools/task.go). It is deliberately a pure function over two
// criterion lists: it holds no store, performs no I/O, and can be called
// BEFORE any write, which is what lets every surface refuse a bad pair without
// leaving a half-written task behind.
//
// It cannot live on the goal record (pkg/goal), which is the one place that
// holds both lists at once, because a goal record is only written AFTER the
// task record is — a refusal there would already be too late.
package task

import "strings"

// ValidateDoDDistinct reports whether every definition-of-done item is
// distinct from every acceptance criterion (GOAL-FR-021/FR-047/FR-048, D-C).
//
// THE RULE, stated exactly:
//
//	A DoD item collides with an acceptance criterion when the two items' TEXT
//	is equal after (a) trimming leading and trailing whitespace, (b) collapsing
//	every internal run of whitespace to one space, and (c) ASCII/Unicode
//	lower-casing. Nothing else is compared, and nothing else is normalised.
//
// IT MATCHES THE IDENTITY RULE THIS CODEBASE ALREADY USES. The comparison is
// deliberately not invented here: pkg/agent/goal_compile.go's amendment diff
// already decides "is this the same criterion?" with normalizeCritText —
// strings.ToLower(strings.TrimSpace(s)) — and treats a same-text criterion
// whose Check/Behavior payload differs as a CHANGE to that same criterion, not
// as a different one. So by the project's own settled definition, a DoD item
// carrying a criterion's text IS that criterion restated, whatever payload it
// hangs off it. The only deliberate difference here is collapsing INTERNAL
// whitespace runs as well as trimming the ends, which catches a paste that
// picked up a line break in the middle.
//
// WHY THAT RULE AND NOT A WIDER ONE. Exact byte equality is the floor — it is
// the case UAT actually hit — but on its own it is trivially defeated by the
// same copy-paste picking up a trailing space or a capitalised first letter,
// which is not a different Definition of Done by any reading. Whitespace and
// case are precisely the noise a copy-paste introduces, so folding them costs
// nothing and closes the restatement of the same defect.
//
// WHY IT STOPS THERE — no stemming, no token overlap, no edit distance, no
// similarity threshold. A DoD item is very often a legitimate near-paraphrase
// of a criterion at a different altitude ("the tests pass" as a criterion, "the
// test suite passes in CI on a clean checkout" as the DoD). Any fuzzy rule
// would have to draw a line somewhere on that continuum, and the author has no
// way to see where the line is: they would be told their Definition of Done is
// "too similar" with no way to predict what passes. A rule a person cannot
// predict is worse than one that under-catches — the same reasoning
// frozenTaskDefinitionFields already applies to over-freezing a running task.
// Under-catching leaves a weak-but-honest DoD; over-catching refuses a correct
// one and teaches the author to write nonsense to get past the gate.
//
// SCOPE. The comparison is CROSS-LIST and PER-PAIR: one duplicated item among
// several is a violation, because a rule that only fired when the two lists
// were wholly identical would be defeated by adding one filler item. Duplicates
// WITHIN either list are deliberately not this function's business — a repeated
// criterion is a different (and harmless) authoring slip, and rejecting it here
// would widen the rule past what D-C asks for.
//
// An item whose comparable text is empty is skipped rather than matched, so two
// blank items never "collide" — emptiness is caught by validateCriterion's own
// text-length rule, and reporting it here would name the wrong defect.
//
// Returns an ErrValidation-wrapped error naming the offending text, so every
// caller can map it to the same refusal (HTTP 400 on the REST surface) and the
// author can see WHICH item to change. Returns nil when either list is empty —
// the count rule owns that case and reports it in its own words.
func ValidateDoDDistinct(criteria, dod []AcceptanceCriterion) error {
	if len(criteria) == 0 || len(dod) == 0 {
		return nil
	}
	byText := make(map[string]string, len(criteria))
	for _, c := range criteria {
		key := comparableCriterionText(c.Text)
		if key == "" {
			continue
		}
		// Keep the FIRST criterion's original text for the message: it is the
		// one an author reading top-down will find first.
		if _, seen := byText[key]; !seen {
			byText[key] = c.Text
		}
	}
	for i, d := range dod {
		key := comparableCriterionText(d.Text)
		if key == "" {
			continue
		}
		if criterionText, clash := byText[key]; clash {
			return verr("dod[%d]: %q restates the acceptance criterion %q — a definition-of-done "+
				"item must be distinct from every acceptance criterion (GOAL-FR-021/FR-048). "+
				"Say what must be TRUE of the finished work that the criteria do not already say",
				i, d.Text, criterionText)
		}
	}
	return nil
}

// comparableCriterionText reduces a criterion's text to the key
// ValidateDoDDistinct compares on: whitespace-collapsed and lower-cased.
//
// strings.Fields splits on every Unicode space character and discards empty
// runs, so joining its result with a single space performs the trim and the
// internal-collapse in one pass — including tabs and newlines, which a paste
// out of a textarea routinely carries.
func comparableCriterionText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
