// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression test for FIX 4 (sendfile-fix review): RequestCancel's
// descendants-consistency check (cancel.go) used to compare ONLY
// len(interrupted) != len(descendants), which cannot detect a same-size
// SWAP — one descendant finishing while a different one is added in the same
// narrow window between collectDescendantTurnIDs and InterruptSession. This
// file proves descendantSetsMatch (the set-based replacement) catches that
// exact case, which the old length-only check could not.

package agent

import "testing"

func TestDescendantSetsMatch_SameSizeSwap_IsNotAMatch(t *testing.T) {
	// The exact race FIX 4 closes: "turn-a" finished and was replaced by
	// "turn-c" in the window between collect and interrupt. Same length (2),
	// completely different membership. The old `len(a) != len(b)` check would
	// have reported this pair as "consistent" and stayed silent.
	preCollected := []string{"turn-a", "turn-b"}
	interrupted := []string{"turn-b", "turn-c"}

	if descendantSetsMatch(preCollected, interrupted) {
		t.Fatal("descendantSetsMatch: same-size swap must NOT be reported as a match — " +
			"this is exactly the race the check exists to catch")
	}
}

func TestDescendantSetsMatch_SameSetDifferentOrder_IsAMatch(t *testing.T) {
	// A genuinely consistent pair must not be flagged merely because the two
	// collection passes (collectDescendantTurnIDs vs InterruptSession) walked
	// activeTurnStates in a different order (sync.Map iteration order is not
	// guaranteed stable across two Range calls).
	a := []string{"turn-a", "turn-b", "turn-c"}
	b := []string{"turn-c", "turn-a", "turn-b"}

	if !descendantSetsMatch(a, b) {
		t.Fatal("descendantSetsMatch: same set in different order must be reported as a match")
	}
}

func TestDescendantSetsMatch_DifferentLength_IsNotAMatch(t *testing.T) {
	a := []string{"turn-a", "turn-b"}
	b := []string{"turn-a"}

	if descendantSetsMatch(a, b) {
		t.Fatal("descendantSetsMatch: different-length slices must never be reported as a match")
	}
}

func TestDescendantSetsMatch_BothEmpty_IsAMatch(t *testing.T) {
	if !descendantSetsMatch(nil, []string{}) {
		t.Fatal("descendantSetsMatch: two empty/nil slices must be reported as a match")
	}
}

func TestDescendantSetsMatch_DuplicateInOneListOnly_IsNotAMatch(t *testing.T) {
	// Defensive: a duplicate turn ID appearing in only one of the two lists is
	// itself a genuine mismatch (collectDescendantTurnIDs/InterruptSession
	// should never emit a duplicate) and must not be masked by a naive
	// distinct-membership-only comparison.
	a := []string{"turn-a", "turn-a", "turn-b"}
	b := []string{"turn-a", "turn-b", "turn-b"}

	if descendantSetsMatch(a, b) {
		t.Fatal("descendantSetsMatch: mismatched duplicate counts must not be reported as a match")
	}
}
