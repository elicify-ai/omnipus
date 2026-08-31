// Omnipus — a clause dropped under a combinator must still say WHY it went.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// THE DEFECT THIS FILE GUARDS, AND WHY ITS ORACLE IS A COMPARISON
//
// `status != "shipped"` is not stored as a leaf. leaf.go desugars `!=` into a
// tree NEGATION (nodeFromRawLeaf), so what reaches the resolution pass is a
// `not:` wrapping one leaf — and the combinator branch reports the GROUP's own
// verbatim text, discarding whatever the leaf underneath diagnosed. The founder
// was told an expression went; he was not told why. Six clauses across three of
// his eighteen bases landed in the import report's "DROPPED WITH NO STATED
// REASON" bucket for exactly this reason and no other.
//
// The oracle here is deliberately NOT the text of any message this package
// happens to emit — reading an expected string off the implementation would
// make the test agree with whatever the code does. It is an EQUIVALENCE
// instead: the same clause written positively (`status == "shipped"`) and
// negatively (`status != "shipped"`) fails for the same reason, because it is
// the same leaf against the same schema. So the negated form's loss line must
// carry the positive form's diagnosis, whatever that diagnosis turns out to
// say. If the message is reworded tomorrow, both halves move together and this
// test still measures the thing it is about.
//
// WHAT IS DELIBERATELY NOT CHANGED HERE. `not:` is where narrowing becomes
// broadening — a clause refused at the leaf and then wrapped in a negation
// would INCLUDE rows Obsidian excluded if it were carried anyway. So this is a
// REPORTING fix and nothing else: the loss still exists, still sits in a
// row-set-affecting position, and still disables the view. The last two
// assertions in TestCombinatorLoss_KeepsDisablingTheView exist to prove the
// reason did not arrive at the cost of the prohibition.
// ---------------------------------------------------------------------------

// reasonPropagationBase is one base whose views isolate the four cases: the
// same failing clause positively and negatively, the same clause inside an
// `or:` group, and a `not:` that resolves cleanly and must stay silent.
//
// `shipped` is not among decisionSchema()'s declared `status` values, so the
// leaf fails at schema resolution — not at parse time. That distinction is the
// whole point: a clause lost at PARSE time is already reported verbatim with no
// diagnosis to lose, and would not exercise this path at all.
const reasonPropagationBase = `
filters:
  and:
    - type == "decision"
views:
  - type: table
    name: Positive
    filters:
      and:
        - status == "shipped"

  - type: table
    name: Negated
    filters:
      and:
        - status != "shipped"

  - type: table
    name: Disjunction
    filters:
      and:
        - or:
            - status == "shipped"
            - priority >= 3

  - type: table
    name: CleanNegation
    filters:
      and:
        - not:
            - status == "accepted"
`

func reasonPropagationViews(t *testing.T) map[string]ViewOutcome {
	t.Helper()
	pb, err := ParseBaseFile([]byte(reasonPropagationBase))
	if err != nil {
		t.Fatalf("the fixture base does not parse: %v", err)
	}
	outcome, _ := TranslateBase(pb, "Decisions.base", decisionSchema(), NewSlugRegistry())
	byName := map[string]ViewOutcome{}
	for _, v := range outcome.Views {
		byName[v.DisplayName] = v
		t.Logf("%-14q %-28s disabled=%v", v.DisplayName, v.Status, v.Disabled)
		for _, l := range v.Losses {
			t.Logf("               loss: %s", l)
		}
	}
	return byName
}

// lossReasonHalf returns everything a loss line says AFTER the first " — ",
// i.e. the importer's own diagnosis with the operator's expression and the
// position prefix stripped off. It returns "" for a line that carries no
// diagnosis at all — which is precisely the defect under test.
func lossReasonHalf(line string) string {
	const sep = " — "
	if i := strings.Index(line, sep); i >= 0 {
		return strings.TrimSpace(line[i+len(sep):])
	}
	return ""
}

// soleLoss returns the one loss a view carries, failing when it carries a
// different number — an assertion made against "the first of several" would
// pass while the interesting one went unreported.
func soleLoss(t *testing.T, views map[string]ViewOutcome, name string) string {
	t.Helper()
	v, ok := views[name]
	if !ok {
		t.Fatalf("the fixture has no view named %q", name)
	}
	if len(v.Losses) != 1 {
		t.Fatalf("%q carries %d losses, want exactly 1: %v", name, len(v.Losses), v.Losses)
	}
	return v.Losses[0]
}

// TestNegatedLeafLoss_CarriesTheLeafsReason is the reproduction. Before the
// fix the negated view's loss line was `[view filter] status != "shipped"` and
// nothing else, so `lossReasonHalf` came back empty and the founder's report
// could only say the clause went.
func TestNegatedLeafLoss_CarriesTheLeafsReason(t *testing.T) {
	views := reasonPropagationViews(t)

	positive := soleLoss(t, views, "Positive")
	wantReason := lossReasonHalf(positive)
	if wantReason == "" {
		t.Fatalf("the POSITIVE form of the clause carries no reason either (%q) — this test's oracle has gone, and the negated case below would pass vacuously", positive)
	}

	negated := soleLoss(t, views, "Negated")
	if got := lossReasonHalf(negated); got == "" {
		t.Errorf("the negated clause was dropped with NO stated reason.\n  got:  %q\n  want: a line carrying the same diagnosis the positive form gives, which is %q", negated, wantReason)
	} else if !strings.Contains(negated, wantReason) {
		t.Errorf("the negated clause names a DIFFERENT reason from the identical positive clause.\n  negated:  %q\n  positive: %q", negated, positive)
	}

	// The operator's own text must survive alongside the reason: a line that
	// gave the diagnosis but no longer said which clause it was about would
	// trade one unanswerable question for another.
	if !strings.Contains(negated, `status != "shipped"`) {
		t.Errorf("the negated clause's loss line no longer quotes the expression the operator wrote: %q", negated)
	}

	// And it must not quote it twice. `!=` desugars into a `not:` whose own
	// verbatim IS the leaf's source text, so a naive concatenation prints
	// `status != "shipped" — status != "shipped" — value ...`.
	if strings.Count(negated, `status != "shipped"`) != 1 {
		t.Errorf("the expression is quoted %d times in one loss line; want exactly 1: %q", strings.Count(negated, `status != "shipped"`), negated)
	}
}

// TestDisjunctionLoss_CarriesTheFailingBranchsReason is the same propagation
// through `any:` rather than `not:`. The group is still lost WHOLE — keeping
// the translatable branch would narrow the view to one side of an "either" the
// operator wrote — but the reader is now told which diagnosis cost them the
// group.
func TestDisjunctionLoss_CarriesTheFailingBranchsReason(t *testing.T) {
	views := reasonPropagationViews(t)

	wantReason := lossReasonHalf(soleLoss(t, views, "Positive"))
	if wantReason == "" {
		t.Fatal("the positive control carries no reason — oracle gone")
	}

	group := soleLoss(t, views, "Disjunction")
	if !strings.Contains(group, wantReason) {
		t.Errorf("the `or:` group was dropped without naming the branch diagnosis that cost it.\n  got:  %q\n  want it to contain: %q", group, wantReason)
	}
	// The group's own text still has to be there — a reader must be able to
	// see WHICH `or:` block went missing, not merely that a clause inside one
	// failed.
	if !strings.Contains(group, "or:") {
		t.Errorf("the `or:` group's loss line no longer shows the group it lost: %q", group)
	}
}

// TestCombinatorLoss_KeepsDisablingTheView is the FR-105 half. A better
// sentence must not have bought itself an enabled view: the loss stays in a
// row-set-affecting position and the view stays disabled.
func TestCombinatorLoss_KeepsDisablingTheView(t *testing.T) {
	views := reasonPropagationViews(t)

	for _, name := range []string{"Negated", "Disjunction"} {
		v, ok := views[name]
		if !ok {
			t.Fatalf("the fixture has no view named %q", name)
		}
		if !v.Disabled {
			t.Errorf("%q is ENABLED — a clause that could not be translated is still missing from the filter, so the view would return MORE rows than the Obsidian original (FR-105)", name)
		}
		for _, l := range v.Losses {
			if !lossPositionAffectsRowSet(l) {
				t.Errorf("%q: loss %q is classified as an annotation, but it is a filter clause that decides the row set", name, l)
			}
			if _, ok := parseLossPosition(l); !ok {
				t.Errorf("%q: loss %q has no recognised position prefix — the report classifies on that prefix, so a line without one is unclassifiable", name, l)
			}
		}
	}
}

// TestResolvedNegation_ReportsNothing is the other direction, and it is what
// stops the fix above from being satisfied by attaching a reason to every
// combinator whether or not anything failed. `not: [status == "accepted"]` is
// entirely translatable — "accepted" IS a declared enum value — so the view
// must import clean and enabled.
func TestResolvedNegation_ReportsNothing(t *testing.T) {
	views := reasonPropagationViews(t)

	v, ok := views["CleanNegation"]
	if !ok {
		t.Fatal("the fixture has no view named \"CleanNegation\"")
	}
	if len(v.Losses) != 0 {
		t.Errorf("a fully translatable `not:` reported losses: %v", v.Losses)
	}
	if v.Disabled {
		t.Errorf("a fully translatable `not:` left the view DISABLED — disabling losses: %v", v.DisablingLosses)
	}
	if v.Status != OutcomeConverted {
		t.Errorf("status = %s, want %s", v.Status, OutcomeConverted)
	}
}
