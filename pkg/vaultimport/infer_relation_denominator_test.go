// Omnipus — FR-104a's DENOMINATOR, pinned. A dangling link counts against
// confidence; leaving it out of the ratio is what let the founder's own
// cited bad guess survive the threshold that was added to stop it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS — IT WAS FOUND BY A SURVIVING MUTATION
//
// The FR-104a suite next door tests the 2/3 threshold thoroughly, but every
// case in it resolves EVERY link. When every link resolves, the two candidate
// denominators (all links vs resolved links only) are the same number, so
// swapping one for the other changes no test's outcome. It was verified: the
// mutation `linkTotal -> resolvedTotal` SURVIVED the whole package suite.
//
// That is precisely the decision the requirement turns on. FR-104a's wording
// says ">= 2/3 of RESOLVED targets"; the same requirement states the purpose
// as stopping `contact.related` -> `to: task` on "a 2-of-5 plurality". In the
// founder's vault that property holds five links — 2 to a task, 1 to a
// person, 2 dangling. Under the narrow reading, task holds 2 of 3, clears 2/3
// exactly, and is declared AGAIN: the threshold would have been added and the
// guess it was written to stop would have walked straight through it.
//
// So these cases are the ones where the two readings DISAGREE, and every
// expectation below is derived from the requirement's stated purpose rather
// than from what the code currently returns.
// ---------------------------------------------------------------------------

// danglingIndex names `real<i>` notes as the given type and leaves every
// `ghost<i>` target out of the index entirely, so a link to one dangles.
func danglingIndex(byType map[string]int) *NameIndex {
	stems := map[string]string{}
	for typ, n := range byType {
		for i := 0; i < n; i++ {
			stems[typ+"-real-"+string(rune('a'+i))] = typ
		}
	}
	return nameIndexOf(stems)
}

func linksWithDangling(prop string, byType map[string]int, order []string, dangling int) *PropertyObservation {
	var targets []string
	for _, typ := range order {
		for i := 0; i < byType[typ]; i++ {
			targets = append(targets, typ+"-real-"+string(rune('a'+i)))
		}
	}
	for i := 0; i < dangling; i++ {
		targets = append(targets, "ghost-"+string(rune('a'+i)))
	}
	return linksTo(prop, targets...)
}
