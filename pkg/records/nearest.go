// Omnipus — Damerau-Levenshtein "did you mean?" nearest-name matching.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

// NearestWithinOneEdit pairs each requested literal with an observed value one
// edit away, when there is one. "One edit" is Damerau-Levenshtein — insertion,
// deletion, substitution OR TRANSPOSITION — because a transposition is the
// single most common typing slip (`doen` for `done`) and plain Levenshtein
// scores it 2, which would miss exactly the case this check exists for.
//
// It decides nothing. The pairing is EVIDENCE handed to the operator, who
// holds the one fact this package does not: whether they meant it.
func NearestWithinOneEdit(requested, observed []string) map[string]string {
	out := map[string]string{}
	for _, r := range requested {
		for _, o := range observed {
			if withinOneEdit(FoldKey(r), FoldKey(o)) {
				out[r] = o
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// withinOneEdit reports whether a and b are at Damerau-Levenshtein distance 1
// or less.
func withinOneEdit(a, b string) bool {
	ar, br := []rune(a), []rune(b)
	switch d := len(ar) - len(br); {
	case d == 0:
		return equalRunes(ar, br) || oneSubstitutionApart(ar, br) || oneTranspositionApart(ar, br)
	case d == 1:
		return oneDeletionApart(ar, br)
	case d == -1:
		return oneDeletionApart(br, ar)
	}
	return false
}

// oneSubstitutionApart: equal lengths, differing in exactly one position.
func oneSubstitutionApart(a, b []rune) bool {
	diff := 0
	for i := range a {
		if a[i] != b[i] {
			diff++
			if diff > 1 {
				return false
			}
		}
	}
	return diff == 1
}

// oneTranspositionApart: equal lengths, identical except for two ADJACENT
// positions whose runes are swapped — `doen` against `done`.
func oneTranspositionApart(a, b []rune) bool {
	first := -1
	for i := range a {
		if a[i] != b[i] {
			if first < 0 {
				first = i
				continue
			}
			if i != first+1 || a[i] != b[first] || a[first] != b[i] {
				return false
			}
			// Everything after the swapped pair must match.
			for j := i + 1; j < len(a); j++ {
				if a[j] != b[j] {
					return false
				}
			}
			return true
		}
	}
	return false
}

// oneDeletionApart: `long` becomes `short` by deleting exactly one rune.
func oneDeletionApart(long, short []rune) bool {
	i, j, skipped := 0, 0, false
	for i < len(long) && j < len(short) {
		if long[i] == short[j] {
			i, j = i+1, j+1
			continue
		}
		if skipped {
			return false
		}
		skipped = true
		i++
	}
	return true
}

func equalRunes(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
