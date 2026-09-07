// Omnipus — FR-105's classification, under test. This file guards the single
// decision the broadening prohibition rests on: what a named loss can do to
// the set of rows a view returns.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"sort"
	"strings"
	"testing"
)

// expectedLossClassification is the ORACLE, and it is written from the
// specification rather than read off loss.go — FR-105 ("an untranslatable
// expression in any row-set-affecting position ... imports the view
// DISABLED"; "returning FEWER rows, with the loss named, is acceptable") and
// FR-106 ("an untranslatable expression in an annotation position cannot
// change which rows appear; the view imports ENABLED").
//
// The reasoning for each row, so a future reader can disagree with the
// classification rather than only with the value:
//
//	base outer filter   — applies to every view in the file; dropping a
//	                      clause removes a restriction. MORE rows.
//	view filter         — the view's own restriction. MORE rows.
//	filter              — one clause of that restriction. MORE rows.
//	group_by            — how matched rows are bucketed. Same rows.
//	properties          — which columns are shown. Same rows.
//	sort                — what order they come back in. Same rows.
//	aggregates          — a number computed OVER the rows. Same rows.
//	layout              — table vs cards vs board. Same rows.
//	limit               — the row-count bound the base declared. Dropping one
//	                      lets the view return every match where the operator
//	                      asked for the first N. MORE rows.
//	narrowed to nothing — the clause was TRANSLATED, is provably no wider,
//	                      and empties the conjunction it sits in. ZERO rows:
//	                      fewer, which FR-105 permits when it is named.
//	narrowed            — same proof, but under an `any:`, so the group thins
//	                      instead of emptying. FEWER rows.
//	unproven narrowing  — a narrowing was CLAIMED and not established. FR-105
//	                      has no tolerance for "probably narrower", so this
//	                      is treated exactly as if it broadened.
var expectedLossClassification = map[LossPosition]lossEffect{
	LossBaseOuterFilter:   lossEffectBroadens,
	LossViewFilter:        lossEffectBroadens,
	LossFilterLeaf:        lossEffectBroadens,
	LossLimit:             lossEffectBroadens,
	LossUnprovenNarrowing: lossEffectBroadens,

	LossNarrowedToNothing: lossEffectNarrows,
	LossNarrowedRowSet:    lossEffectNarrows,

	LossGroupBy:    lossEffectAnnotates,
	LossProperties: lossEffectAnnotates,
	LossSort:       lossEffectAnnotates,
	LossAggregates: lossEffectAnnotates,
	LossLayout:     lossEffectAnnotates,
}

// TestLossPositions_ArePartitioned is the test loss.go's header names: the
// closed enum and the classification map must cover EXACTLY each other. A
// position added to one and not the other is the way this prohibition would
// erode.
//
// THE TRI-STATE ADDED A THIRD WAY TO BE UNCLASSIFIED, and this test would have
// gone vacuous without covering it. Against the old `map[LossPosition]bool` a
// position was either present or absent. Against `map[LossPosition]lossEffect`
// a position can also be PRESENT AND PARKED AT THE ZERO VALUE — written as
// `LossFoo: lossEffectUnclassified`, or reached by a typo'd constant — which
// satisfies a bare "is there a key for it" check while recording no opinion at
// all. So case (c) below is not decoration: it is the specific check that
// keeps this guard guarding after the type changed under it.
func TestLossPositions_ArePartitioned(t *testing.T) {
	inEnum := map[LossPosition]bool{}
	for _, p := range allLossPositions {
		if inEnum[p] {
			t.Errorf("allLossPositions lists %q twice", p)
		}
		inEnum[p] = true
	}

	// (a) every emitted position is classified.
	for _, p := range allLossPositions {
		if _, classified := lossPositionEffects[p]; !classified {
			t.Errorf("loss position %q is emitted but NOT classified — lossDisablesView would have to guess, and a guess in this direction is a broadened view", p)
		}
	}
	// (b) every classified position is really emitted.
	for p := range lossPositionEffects {
		if !inEnum[p] {
			t.Errorf("loss position %q is classified but is not in allLossPositions — either it is dead, or the enum has drifted from what the importer actually emits", p)
		}
	}
	// (c) no position is classified as the ZERO VALUE. See this test's header.
	for _, p := range allLossPositions {
		if lossPositionEffects[p] == lossEffectUnclassified {
			t.Errorf("loss position %q classifies as %v — a key with no opinion behind it passes an is-it-present check while telling FR-105 nothing; give it broadens, narrows or annotates", p, lossEffectUnclassified)
		}
	}
}

// TestLossEffects_AreAllExercised stops the classification collapsing into a
// constant. A map where every position answers `broadens` would satisfy the
// partition test above perfectly and disable every view in the vault; one
// where every position answers `annotates` would ship every broadened view.
func TestLossEffects_AreAllExercised(t *testing.T) {
	seen := map[lossEffect]int{}
	for _, p := range allLossPositions {
		seen[lossPositionEffects[p]]++
	}
	for _, e := range allLossEffects {
		if e == lossEffectUnclassified {
			if seen[e] != 0 {
				t.Errorf("%d position(s) classify as %v; none may", seen[e], e)
			}
			continue
		}
		if seen[e] == 0 {
			t.Errorf("no loss position classifies as %v — the classification has collapsed toward a constant, which either disables every view or ships every broadened one", e)
		}
	}
}

// TestLossPositions_MatchTheSpecOracle checks each position against the
// hand-derived table above rather than against the map under test. Both
// tables list every position, so neither can drift without the other
// noticing.
func TestLossPositions_MatchTheSpecOracle(t *testing.T) {
	if len(expectedLossClassification) != len(allLossPositions) {
		t.Fatalf("the oracle covers %d positions and the importer emits %d — one of them changed without the other", len(expectedLossClassification), len(allLossPositions))
	}
	for _, p := range allLossPositions {
		want, ok := expectedLossClassification[p]
		if !ok {
			t.Errorf("position %q has no entry in the spec-derived oracle", p)
			continue
		}
		if got := lossPositionEffects[p]; got != want {
			t.Errorf("position %q: importer says effect=%v, FR-105/FR-106 say %v", p, got, want)
		}
	}
}

// TestLossEffect_DisablesViewIsAnAllowList pins the direction of the
// fail-safe at the type level, independently of any position. Only `narrows`
// and `annotates` may ship; everything else — including the zero value and
// any effect a future change adds without thinking it through — disables.
func TestLossEffect_DisablesViewIsAnAllowList(t *testing.T) {
	cases := map[lossEffect]bool{
		lossEffectUnclassified: true,
		lossEffectBroadens:     true,
		lossEffectNarrows:      false,
		lossEffectAnnotates:    false,
		// An effect value nobody has defined yet. It must disable.
		lossEffect(200): true,
	}
	for e, want := range cases {
		if got := e.disablesView(); got != want {
			t.Errorf("lossEffect(%d).disablesView() = %v, want %v — a deny-list here would make every future effect default to shipping", uint8(e), got, want)
		}
	}
}

// TestLossPositionAffectsRowSet_ReadsBackWhatLossfWrote is the round trip
// that makes the report a human reads and the decision the code makes the
// SAME fact. If lossf's rendering and parseLossPosition's parsing ever
// disagree, every loss becomes "unknown prefix" and every view disables —
// safe, but only by accident.
func TestLossPositionAffectsRowSet_ReadsBackWhatLossfWrote(t *testing.T) {
	for _, p := range allLossPositions {
		line := lossf(p, "some detail with a ] bracket and a %s", "value")
		got, ok := parseLossPosition(line)
		if !ok {
			t.Errorf("a loss line rendered by lossf(%q, ...) does not parse back to a known position: %q", p, line)
			continue
		}
		if got != p {
			t.Errorf("lossf(%q, ...) parses back as %q", p, got)
		}
		wantEffect := expectedLossClassification[p]
		if gotEffect := lossLineEffect(line); gotEffect != wantEffect {
			t.Errorf("lossLineEffect(%q) = %v, want %v", line, gotEffect, wantEffect)
		}
		if got, want := lossPositionAffectsRowSet(line), wantEffect.disablesView(); got != want {
			t.Errorf("lossPositionAffectsRowSet(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestLossPositionAffectsRowSet_UnknownPrefixIsTreatedAsDangerous pins the
// direction of the fail-safe. An unclassifiable loss must answer TRUE (the
// view disables), never FALSE (the view ships and may broaden).
func TestLossPositionAffectsRowSet_UnknownPrefixIsTreatedAsDangerous(t *testing.T) {
	cases := []string{
		"",
		"no prefix at all",
		"[unclosed bracket",
		"[a position nobody classified] detail",
		"[] detail",
		" [sort] leading space means the bracket is not first",
	}
	for _, line := range cases {
		if !lossPositionAffectsRowSet(line) {
			t.Errorf("lossPositionAffectsRowSet(%q) = false — an unclassifiable loss must be treated as dangerous, or forgetting to classify one silently ships a broadened view", line)
		}
		if got := lossLineEffect(line); got != lossEffectUnclassified {
			t.Errorf("lossLineEffect(%q) = %v, want %v", line, got, lossEffectUnclassified)
		}
	}
}

// TestLossPositionAffectsRowSet_SeparatesTheThreeKinds is the rule the
// acceptance harness asserts, stated over the loss lines themselves: at least
// one position on each side, so the function cannot pass by answering a
// constant.
func TestLossPositionAffectsRowSet_SeparatesTheThreeKinds(t *testing.T) {
	byEffect := map[lossEffect][]string{}
	for _, p := range allLossPositions {
		line := lossf(p, "detail")
		byEffect[lossLineEffect(line)] = append(byEffect[lossLineEffect(line)], string(p))
	}
	for _, e := range []lossEffect{lossEffectBroadens, lossEffectNarrows, lossEffectAnnotates} {
		if len(byEffect[e]) == 0 {
			t.Errorf("no loss line reads as %v", e)
		}
		sort.Strings(byEffect[e])
		t.Logf("%-12s %s", e.String()+":", strings.Join(byEffect[e], ", "))
	}
}
