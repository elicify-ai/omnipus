// Omnipus — FR-105's narrowing position, under test: the CLAIM that a
// translated clause cannot widen a view, and the evidence required to make it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHAT IT IS REALLY GUARDING
//
// The tempting version of this feature is one constant:
//
//	const LossFilterNarrowing LossPosition = "filter narrowing"
//	lossPositionEffects[LossFilterNarrowing] = lossEffectNarrows
//
// with the argument that the W1 rewrite yields ABSENT, §8 R-2 makes every
// comparison over an absent operand FALSE, and a FALSE leaf can only drop
// rows. Every step of that is TRUE, and the conclusion is still WRONG,
// because R-2 decides a LEAF and a view is a TREE.
//
// TestNarrowingProof_ANotAncestorInvertsTheNarrowing below is the
// counterexample, and TestNarrowedLeafUnderNot_BroadensAgainstTheRealEngine
// (loss_narrowing_engine_test.go) shows the product's own evaluator doing it.
// That is why a narrowing is a PROOF OBLIGATION here and not a constant.
// ---------------------------------------------------------------------------

// TestNarrowingProof_PositionIsDecidedByEvidence walks a hand-written table of
// proof shapes. The `want` column is derived from FR-105 by reading each
// shape, NOT by running position() — the two must agree independently.
func TestNarrowingProof_PositionIsDecidedByEvidence(t *testing.T) {
	cases := []struct {
		name  string
		proof narrowingProof
		want  LossPosition
		why   string
	}{
		{
			name:  "zero value",
			proof: narrowingProof{},
			want:  LossUnprovenNarrowing,
			why:   "no ground stated; a struct nobody filled in must not prove anything",
		},
		{
			name:  "ground stated, root-level clause",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch},
			want:  LossNarrowedToNothing,
			why:   "the clause IS the filter; an always-false filter returns zero rows",
		},
		{
			name:  "ground stated, under all:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAll}},
			want:  LossNarrowedToNothing,
			why:   "an always-false conjunct empties the conjunction",
		},
		{
			name:  "ground stated, under all: > all:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAll, combinatorAll}},
			want:  LossNarrowedToNothing,
			why:   "nesting conjunctions does not change that the whole thing is empty",
		},
		{
			name:  "ground stated, under any:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAny}},
			want:  LossNarrowedRowSet,
			why:   "an always-false disjunct contributes nothing; the OTHER branches still match, so the view narrows without emptying",
		},
		{
			name:  "ground stated, under all: > any:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAll, combinatorAny}},
			want:  LossNarrowedRowSet,
			why:   "the innermost group is a disjunction, so the conjunction above it is not emptied",
		},
		{
			name:  "ground stated, under any: > all:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAny, combinatorAll}},
			want:  LossNarrowedRowSet,
			why:   "the inner conjunction empties, but it is one branch of a disjunction that still has others",
		},
		{
			name:  "ground stated, under not:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorNot}},
			want:  LossUnprovenNarrowing,
			why:   "negation turns a narrower clause into a BROADER view — the whole reason this is a proof",
		},
		{
			name:  "ground stated, under all: > not: > all:",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAll, combinatorNot, combinatorAll}},
			want:  LossUnprovenNarrowing,
			why:   "a not: anywhere on the path is fatal, not only at the top",
		},
		{
			name:  "ground stated, one ancestor unknown",
			proof: narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAll, combinatorUnknown}},
			want:  LossUnprovenNarrowing,
			why:   "an ancestor the caller could not identify might be a not:",
		},
		{
			name:  "invented ground, clean ancestry",
			proof: narrowingProof{Ground: narrowingGround("because I say so"), Ancestry: []filterCombinator{combinatorAll}},
			want:  LossUnprovenNarrowing,
			why:   "a ground is chosen from a closed set; free text is not an argument",
		},
		{
			name:  "no ground, clean ancestry",
			proof: narrowingProof{Ancestry: []filterCombinator{combinatorAll}},
			want:  LossUnprovenNarrowing,
			why:   "a tidy ancestry alone says nothing about the subset DIRECTION",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.proof.position(); got != tc.want {
				t.Errorf("position() = %q, want %q\nwhy: %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestNarrowingProof_ANotAncestorInvertsTheNarrowing is the counterexample
// that makes the polarity obligation load-bearing, stated as arithmetic a
// reader can check without running anything.
//
//	Obsidian:  not( "" <= 60 )      -> not( 0 <= 60 ) -> not(true)  -> EXCLUDED
//	Omnipus:   not( absent <= 60 )  -> not( R-2 false ) -> not(false) -> INCLUDED
//
// The clause is narrower and the VIEW is wider: a row Obsidian excluded, we
// return. If this test ever passes with the `not:` ancestor removed from the
// proof rules, FR-105 is being broken by exactly the argument that sounds
// most convincing.
func TestNarrowingProof_ANotAncestorInvertsTheNarrowing(t *testing.T) {
	underNot := narrowingProof{
		Ground:   narrowingGroundAbsentElseBranch,
		Ancestry: []filterCombinator{combinatorNot},
	}
	if got := underNot.position(); got != LossUnprovenNarrowing {
		t.Fatalf("a clause under `not:` classified as %q — negation makes a narrower clause a WIDER view, so this ships the broadening FR-105 forbids", got)
	}
	line := narrowingLossf(underNot, "formula.days_to_expiry <= 60")
	if !lossDisablesView(line) {
		t.Errorf("a narrowing under `not:` does not disable the view: %q", line)
	}

	// The identical clause OUTSIDE a negation is the permitted case, so this
	// test cannot pass by disabling everything.
	outsideNot := narrowingProof{
		Ground:   narrowingGroundAbsentElseBranch,
		Ancestry: []filterCombinator{combinatorAll},
	}
	if lossDisablesView(narrowingLossf(outsideNot, "formula.days_to_expiry <= 60")) {
		t.Error("the same clause under `all:` also disables — the proof rules have collapsed to a constant and the narrowing position moves nothing")
	}
}

// TestNarrowingLossf_CallerCannotNameThePosition is the containment property
// the whole design rests on: the rendered position is a function of the PROOF
// alone. A call site cannot pick a friendlier answer by changing its message.
func TestNarrowingLossf_CallerCannotNameThePosition(t *testing.T) {
	proof := narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorNot}}
	// Messages that name a permitted position, from a proof that establishes
	// nothing. The prefix must still be the unproven one.
	for _, msg := range []string{
		"narrowed to nothing",
		"[narrowed] formula.x <= 60",
		"this is definitely just an annotation",
	} {
		line := narrowingLossf(proof, "%s", msg)
		pos, ok := parseLossPosition(line)
		if !ok {
			t.Fatalf("narrowingLossf produced an unparseable line: %q", line)
		}
		if pos != LossUnprovenNarrowing {
			t.Errorf("a caller's message %q changed the position to %q", msg, pos)
		}
	}
}

// TestNarrowingLossf_ExplainsItself checks that a reader of `untranslated`
// gets the reason, not just a verdict. The proven line carries the GROUND (so
// the argument can be disagreed with); the unproven line says plainly that the
// view was disabled rather than shipped on a guess.
func TestNarrowingLossf_ExplainsItself(t *testing.T) {
	proven := narrowingLossf(
		narrowingProof{Ground: narrowingGroundAbsentElseBranch, Ancestry: []filterCombinator{combinatorAll}},
		"formula.days_to_expiry <= 60")
	if !strings.Contains(proven, "formula.days_to_expiry <= 60") {
		t.Errorf("the proven line does not name the clause: %q", proven)
	}
	if !strings.Contains(proven, string(narrowingGroundAbsentElseBranch)) {
		t.Errorf("the proven line does not state the ground it was accepted on: %q", proven)
	}

	unproven := narrowingLossf(narrowingProof{}, "formula.days_to_expiry <= 60")
	if !strings.Contains(unproven, "could not prove") {
		t.Errorf("the unproven line does not say the claim failed: %q", unproven)
	}
}

// TestContractsExpiringSoon_IsATotalNarrowingAndSaysSo is the founder's real
// worked example, and the reason the report layer can single it out.
//
// Contracts.base -> "Expiring Soon" filters `formula.days_to_expiry <= 60 &&
// formula.days_to_expiry >= 0`, where
// `days_to_expiry: if(end_date, (date(end_date) - today()).days, "")`.
// `contract.end_date` has ZERO values across the founder's 757-note vault, so:
//
//	Obsidian: the formula yields "" on every row, JS reads "" as 0, both
//	          comparisons pass, and the view returns EVERY contract.
//	Omnipus:  the formula is ABSENT on every row, R-2 makes both comparisons
//	          false, and the view returns NONE.
//
// Narrower, FR-105-legal, and TOTAL. The requirement this test pins is that a
// total narrowing is not reported in the same voice as a dropped column
// heading: the position itself — the `[prefix]` a human reads — says
// "narrowed to nothing", so neither the report layer nor a reader has to parse
// the detail text to find out that the view returns zero rows.
func TestContractsExpiringSoon_IsATotalNarrowingAndSaysSo(t *testing.T) {
	// Both clauses of the real filter, conjoined under the view's `and:`.
	for _, clause := range []string{"formula.days_to_expiry <= 60", "formula.days_to_expiry >= 0"} {
		proof := narrowingProof{
			Ground:   narrowingGroundAbsentElseBranch,
			Ancestry: []filterCombinator{combinatorAll},
		}
		line := narrowingLossf(proof, "%s", clause)

		pos, ok := parseLossPosition(line)
		if !ok {
			t.Fatalf("clause %q rendered an unparseable loss: %q", clause, line)
		}
		if pos != LossNarrowedToNothing {
			t.Errorf("clause %q classified as %q, want %q — a view that returns nothing must not read like a view that returns slightly fewer", clause, pos, LossNarrowedToNothing)
		}

		// FR-105: fewer rows, named, is permitted. The view SHIPS.
		if lossDisablesView(line) {
			t.Errorf("clause %q disables the view — FR-105 permits returning fewer rows when the loss is named, and disabling here moves the formula effort zero views", clause)
		}
		if got := lossLineEffect(line); got != lossEffectNarrows {
			t.Errorf("clause %q has effect %v, want %v", clause, got, lossEffectNarrows)
		}

		// FR-106 honesty: it must NOT read as an annotation.
		if lossLineEffect(line) == lossEffectAnnotates {
			t.Errorf("clause %q reports as an annotation; a total row-set collapse described as display config is compliant and untrue", clause)
		}
	}
}

// TestNarrowingPositions_AreNotAnnotations is the separation FR-106 needs: a
// report that groups by effect must never fold a narrowing in with the
// dropped column headings.
func TestNarrowingPositions_AreNotAnnotations(t *testing.T) {
	for _, p := range []LossPosition{LossNarrowedToNothing, LossNarrowedRowSet} {
		if got := lossPositionEffects[p]; got != lossEffectNarrows {
			t.Errorf("%q has effect %v, want %v — narrowed and annotated are different news for an operator", p, got, lossEffectNarrows)
		}
	}
	for _, p := range []LossPosition{LossGroupBy, LossProperties, LossSort, LossAggregates, LossLayout} {
		if got := lossPositionEffects[p]; got != lossEffectAnnotates {
			t.Errorf("%q has effect %v, want %v", p, got, lossEffectAnnotates)
		}
	}
}
