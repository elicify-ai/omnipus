// Omnipus — coverage for NearestWithinOneEdit, moved here from
// pkg/vaultimport where it previously had no direct unit tests of its own
// (it was covered only indirectly, through the enum-widening path). Now that
// it is exported records API, it gets real ones.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import "testing"

// nearestCase is one (requested, observed) pair fed to NearestWithinOneEdit
// as single-element slices, with the expected pairing (or none).
type nearestCase struct {
	name      string
	requested string
	observed  string
	// wantMatch: whether requested should pair with observed at all.
	wantMatch bool
}

// nearestCases exercises every edit family NearestWithinOneEdit is built on
// (via withinOneEdit's Damerau-Levenshtein distance-1 test), plus the
// non-match boundary at distance 2 and the fold/unicode behaviour layered on
// top of it.
var nearestCases = []nearestCase{
	{
		name:      "exact match",
		requested: "done",
		observed:  "done",
		wantMatch: true,
	},
	{
		name:      "one substitution",
		requested: "dane",
		observed:  "done",
		wantMatch: true,
	},
	{
		name:      "one deletion (requested is observed minus one rune)",
		requested: "don",
		observed:  "done",
		wantMatch: true,
	},
	{
		name:      "one insertion (requested is observed plus one rune)",
		requested: "dones",
		observed:  "done",
		wantMatch: true,
	},
	{
		// The case that motivated Damerau- over plain Levenshtein: a
		// transposition of two adjacent runes. Plain Levenshtein scores this
		// distance 2 (a delete + an insert) and would refuse it; if this test
		// is missing, a future simplification to plain Levenshtein passes
		// silently. See NearestWithinOneEdit's own doc comment.
		name:      "one transposition",
		requested: "doen",
		observed:  "done",
		wantMatch: true,
	},
	{
		// Two substitutions ("x" for "o" at index 1, "y" for "n" at index 2)
		// that are NOT an adjacent transposition of each other (x != n, y !=
		// o), so this is genuinely distance 2 and must NOT match.
		name:      "distance two is correctly not matched",
		requested: "dxye",
		observed:  "done",
		wantMatch: false,
	},
	{
		name:      "completely different strings do not match",
		requested: "abcd",
		observed:  "wxyz",
		wantMatch: false,
	},
	{
		// FoldKey does the comparison, not the raw bytes: an exact match
		// under folding pairs even though the literal strings differ.
		name:      "case/fold: exact match under folding",
		requested: "DONE",
		observed:  "done",
		wantMatch: true,
	},
	{
		// Folding happens BEFORE the edit-distance test, not instead of it:
		// this pairs on a folded one-substitution match, not on identity.
		name:      "case/fold: one substitution survives folding",
		requested: "DANE",
		observed:  "done",
		wantMatch: true,
	},
	{
		// "naïve" (n,a,ï,v,e — 5 runes) vs "naive" (n,a,i,v,e — 5 runes) is a
		// one-rune substitution at index 2. ï is a two-byte UTF-8 sequence
		// but a single rune; if the implementation compared bytes instead of
		// runes (or mis-sized a []byte conversion), this pair would come out
		// at the wrong length/offset and fail to match. Proves []rune
		// handling is real, not incidental.
		name:      "unicode: multi-byte rune substitution",
		requested: "naïve",
		observed:  "naive",
		wantMatch: true,
	},
	{
		// A second unicode case on the transposition arm specifically, so
		// multi-byte handling is checked on more than the substitution path.
		// "ïдone" is contrived only to keep runes multi-byte; what matters is
		// the adjacent swap of two multi-byte runes.
		name:      "unicode: transposition of multi-byte runes",
		requested: "äöx",
		observed:  "öäx",
		wantMatch: true,
	},
}

func TestNearestWithinOneEdit_Cases(t *testing.T) {
	for _, c := range nearestCases {
		t.Run(c.name, func(t *testing.T) {
			got := NearestWithinOneEdit([]string{c.requested}, []string{c.observed})
			pairedTo, matched := got[c.requested]
			if matched != c.wantMatch {
				t.Fatalf("NearestWithinOneEdit(%q, %q) matched=%v (paired to %q), want matched=%v",
					c.requested, c.observed, matched, pairedTo, c.wantMatch)
			}
			if c.wantMatch && pairedTo != c.observed {
				t.Fatalf("NearestWithinOneEdit(%q, %q) paired requested with %q, want %q",
					c.requested, c.observed, pairedTo, c.observed)
			}
		})
	}
}

// TestNearestWithinOneEdit_EmptyInputs covers empty inputs on both sides: an
// empty requested slice, an empty observed slice, both empty, and an empty
// STRING on each side of the edit-distance test itself (which withinOneEdit
// must handle without panicking on out-of-range rune slices).
func TestNearestWithinOneEdit_EmptyInputs(t *testing.T) {
	t.Run("empty requested slice", func(t *testing.T) {
		got := NearestWithinOneEdit(nil, []string{"done"})
		if got != nil {
			t.Fatalf("NearestWithinOneEdit(nil, [done]) = %v, want nil", got)
		}
	})
	t.Run("empty observed slice", func(t *testing.T) {
		got := NearestWithinOneEdit([]string{"done"}, nil)
		if got != nil {
			t.Fatalf("NearestWithinOneEdit([done], nil) = %v, want nil", got)
		}
	})
	t.Run("both empty", func(t *testing.T) {
		got := NearestWithinOneEdit(nil, nil)
		if got != nil {
			t.Fatalf("NearestWithinOneEdit(nil, nil) = %v, want nil", got)
		}
	})
	t.Run("empty string requested matches empty string observed (distance 0)", func(t *testing.T) {
		got := NearestWithinOneEdit([]string{""}, []string{""})
		v, ok := got[""]
		if !ok || v != "" {
			t.Fatalf("NearestWithinOneEdit([\"\"], [\"\"]) = %v, want {\"\": \"\"}", got)
		}
	})
	t.Run("empty string requested is one deletion from a single-rune observed", func(t *testing.T) {
		got := NearestWithinOneEdit([]string{""}, []string{"a"})
		v, ok := got[""]
		if !ok || v != "a" {
			t.Fatalf("NearestWithinOneEdit([\"\"], [\"a\"]) = %v, want {\"\": \"a\"}", got)
		}
	})
	t.Run("empty string requested is distance two from a two-rune observed, no match", func(t *testing.T) {
		got := NearestWithinOneEdit([]string{""}, []string{"ab"})
		if got != nil {
			t.Fatalf("NearestWithinOneEdit([\"\"], [\"ab\"]) = %v, want nil", got)
		}
	})
}

// TestNearestWithinOneEdit_MultipleRequestedPickFirstMatch confirms the
// documented "pairs each requested literal ... when there is one" behaviour
// holds across a realistic multi-item call, not just the single-pair cases
// above — this is closer to how vaultimport actually calls it.
func TestNearestWithinOneEdit_MultipleRequestedPickFirstMatch(t *testing.T) {
	requested := []string{"actvie", "pending", "unrelated"}
	observed := []string{"active", "pending", "closed"}
	got := NearestWithinOneEdit(requested, observed)

	if got["actvie"] != "active" {
		t.Errorf("expected %q -> %q, got %q", "actvie", "active", got["actvie"])
	}
	if _, ok := got["pending"]; ok {
		// "pending" is an exact match in observed, but nearestWithinOneEdit is
		// meant for MISMATCHES against a closed vocabulary; an exact hit
		// still legitimately pairs (distance 0 is "within one edit"), so this
		// only documents current behaviour rather than asserting it must be
		// excluded.
		if got["pending"] != "pending" {
			t.Errorf("expected exact match %q -> %q, got %q", "pending", "pending", got["pending"])
		}
	}
	if _, ok := got["unrelated"]; ok {
		t.Errorf("expected %q to have no pairing, got %q", "unrelated", got["unrelated"])
	}
}
