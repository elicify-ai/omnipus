// Omnipus — FR-105's partition, under test. This file guards the single
// decision the broadening prohibition rests on: whether a named loss can
// change WHICH ROWS a view returns.
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
// DISABLED") and FR-106 ("an untranslatable expression in an annotation
// position cannot change which rows appear; the view imports ENABLED").
//
// The reasoning for each row, so a future reader can disagree with the
// classification rather than only with the boolean:
//
//	base outer filter — applies to every view in the file; dropping a
//	                    clause removes a restriction. MORE rows.
//	view filter       — the view's own restriction. MORE rows.
//	filter            — one clause of that restriction. MORE rows.
//	group_by          — how matched rows are bucketed. Same rows.
//	properties        — which columns are shown. Same rows.
//	sort              — what order they come back in. Same rows.
//	aggregates        — a number computed OVER the rows. Same rows.
//	layout            — table vs cards vs board. Same rows.
var expectedLossClassification = map[LossPosition]bool{
	LossBaseOuterFilter: true,
	LossViewFilter:      true,
	LossFilterLeaf:      true,

	LossGroupBy:    false,
	LossProperties: false,
	LossSort:       false,
	LossAggregates: false,
	LossLayout:     false,
}

// TestLossPositions_ArePartitioned is the test loss.go's header names: the
// closed enum and the classification map must cover EXACTLY each other. A
// position added to one and not the other is the way this prohibition would
// erode — a new loss kind that silently classifies as harmless keeps its
// view ENABLED and lets it broaden.
func TestLossPositions_ArePartitioned(t *testing.T) {
	inEnum := map[LossPosition]bool{}
	for _, p := range allLossPositions {
		if inEnum[p] {
			t.Errorf("allLossPositions lists %q twice", p)
		}
		inEnum[p] = true
	}

	for _, p := range allLossPositions {
		if _, classified := lossAffectsRowSet[p]; !classified {
			t.Errorf("loss position %q is emitted but NOT classified — lossPositionAffectsRowSet would have to guess, and a guess in this direction is a broadened view", p)
		}
	}
	for p := range lossAffectsRowSet {
		if !inEnum[p] {
			t.Errorf("loss position %q is classified but is not in allLossPositions — either it is dead, or the enum has drifted from what the importer actually emits", p)
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
		if got := lossAffectsRowSet[p]; got != want {
			t.Errorf("position %q: importer says affectsRowSet=%v, FR-105/FR-106 say %v", p, got, want)
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
		if want := expectedLossClassification[p]; lossPositionAffectsRowSet(line) != want {
			t.Errorf("lossPositionAffectsRowSet(%q) = %v, want %v", line, !want, want)
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
			t.Errorf("lossPositionAffectsRowSet(%q) = false — an unclassifiable loss must be treated as row-set-affecting, or forgetting to classify one silently ships a broadened view", line)
		}
	}
}

// TestLossPositionAffectsRowSet_SeparatesTheTwoKinds is the two-way rule the
// acceptance harness asserts, stated over the loss lines themselves: at
// least one position on each side, so the function cannot pass by answering
// a constant.
func TestLossPositionAffectsRowSet_SeparatesTheTwoKinds(t *testing.T) {
	var rowSet, annotation []string
	for _, p := range allLossPositions {
		line := lossf(p, "detail")
		if lossPositionAffectsRowSet(line) {
			rowSet = append(rowSet, string(p))
		} else {
			annotation = append(annotation, string(p))
		}
	}
	sort.Strings(rowSet)
	sort.Strings(annotation)
	if len(rowSet) == 0 {
		t.Error("no position affects the row set — lossPositionAffectsRowSet answers a constant false, and every untranslatable filter would ship ENABLED")
	}
	if len(annotation) == 0 {
		t.Error("every position affects the row set — lossPositionAffectsRowSet answers a constant true, and a lost colour would disable a view")
	}
	t.Logf("row-set-affecting: %s", strings.Join(rowSet, ", "))
	t.Logf("annotation:        %s", strings.Join(annotation, ", "))
}
