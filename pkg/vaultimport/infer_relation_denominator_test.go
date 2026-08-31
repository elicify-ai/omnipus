// Omnipus — FR-104a's DENOMINATOR, pinned. A dangling link counts against
// confidence; leaving it out of the ratio is what let the founder's own
// cited bad guess survive the threshold that was added to stop it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"testing"
)

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

// TestInferRelationTarget_DanglingLinksCountAgainstTheMajority is the exact
// case the founder's ruling names.
func TestInferRelationTarget_DanglingLinksCountAgainstTheMajority(t *testing.T) {
	cases := []struct {
		name string
		// byType is resolved links per target type; dangling is links that
		// resolve to no note at all.
		byType   map[string]int
		order    []string
		dangling int
		// wantTo is the `to:` FR-104a's PURPOSE requires. "" means the
		// property must be declared text with the fix named.
		wantTo string
		// strictWouldSay is what the narrow "resolved targets only" reading
		// would have produced — recorded so a future reader can see that
		// these rows discriminate between the two, which is the only reason
		// they are here.
		strictWouldSay string
		why            string
	}{
		{
			name:           "the founder's own case: 2 task, 1 person, 2 dangling",
			byType:         map[string]int{"task": 2, "person": 1},
			order:          []string{"task", "person"},
			dangling:       2,
			wantTo:         "",
			strictWouldSay: "task",
			why:            "task holds 2 of the property's 5 links. FR-104a exists to stop exactly this being declared `to: task`.",
		},
		{
			name:           "3 task, 0 other, 2 dangling — still short of 2/3",
			byType:         map[string]int{"task": 3},
			order:          []string{"task"},
			dangling:       2,
			wantTo:         "",
			strictWouldSay: "task",
			why:            "3 of 5 is 0.6. Every link that resolved agreed, but two thirds of the property's links did not point at a task, and a schema saying `to: task` would make the vault's own validator report the other two.",
		},
		{
			name:           "4 task, 0 other, 2 dangling — clears 2/3 of six",
			byType:         map[string]int{"task": 4},
			order:          []string{"task"},
			dangling:       2,
			wantTo:         "task",
			strictWouldSay: "task",
			why:            "4 of 6 is exactly 2/3, which the rule admits.",
		},
		{
			name:           "8 task, 1 person, 1 dangling — comfortably over",
			byType:         map[string]int{"task": 8, "person": 1},
			order:          []string{"task", "person"},
			dangling:       1,
			wantTo:         "task",
			strictWouldSay: "task",
			why:            "8 of 10.",
		},
		{
			name:           "1 task, 9 dangling — one resolved link is not evidence",
			byType:         map[string]int{"task": 1},
			order:          []string{"task"},
			dangling:       9,
			wantTo:         "",
			strictWouldSay: "task",
			why:            "the narrow reading calls this UNANIMOUS (1 of 1) and writes `to: task` off a single link out of ten. That is the failure mode with the largest gap between the two readings.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := danglingIndex(tc.byType)
			po := linksWithDangling("related", tc.byType, tc.order, tc.dangling)

			gotTo, rep := inferRelationTarget("contact", po, idx)

			if gotTo != tc.wantTo {
				t.Errorf("to = %q, want %q.\n  %s", gotTo, tc.wantTo, tc.why)
			}
			if tc.wantTo == "" && tc.strictWouldSay != "" && gotTo == tc.strictWouldSay {
				t.Errorf("the narrow \"resolved targets only\" reading of FR-104a is in force: it declared to=%q, and this case exists because the requirement's stated purpose says it must not", gotTo)
			}

			if rep == nil {
				t.Fatal("a non-unanimous or partly-dangling property must always carry a report")
			}
			resolved := 0
			for _, n := range tc.byType {
				resolved += n
			}
			if rep.ResolvedTotal != resolved {
				t.Errorf("ResolvedTotal = %d, want %d", rep.ResolvedTotal, resolved)
			}
			if want := resolved + tc.dangling; rep.LinkTotal != want {
				t.Errorf("LinkTotal = %d, want %d — the denominator must count every link, not only the ones that resolved", rep.LinkTotal, want)
			}
			if rep.Unresolved != tc.dangling {
				t.Errorf("Unresolved = %d, want %d", rep.Unresolved, tc.dangling)
			}

			if tc.wantTo == "" {
				if rep.Declared != "text" {
					t.Errorf("Declared = %q, want text — FR-034 rejects a relation with an empty `to:` outright", rep.Declared)
				}
				if rep.Remedy == "" {
					t.Error("a property FR-104a refuses to type must name the one-line knowledge_configure fix")
				}
			} else {
				if rep.Declared != "relation" {
					t.Errorf("Declared = %q, want relation", rep.Declared)
				}
				if rep.MajorityType != tc.wantTo {
					t.Errorf("MajorityType = %q, want %q", rep.MajorityType, tc.wantTo)
				}
			}
		})
	}
}

// TestInferRelationTarget_BothRatiosAreCarried checks that the narrower
// reading stays VISIBLE. The two readings disagree on real properties in the
// founder's vault, so an operator reading the report must be able to see what
// the other reading would have said without taking a code comment's word for
// it.
func TestInferRelationTarget_BothRatiosAreCarried(t *testing.T) {
	byType := map[string]int{"task": 2, "person": 1}
	idx := danglingIndex(byType)
	po := linksWithDangling("related", byType, []string{"task", "person"}, 2)

	to, rep := inferRelationTarget("contact", po, idx)
	if to != "" {
		t.Fatalf("expected the founder's case to be refused, got to=%q", to)
	}
	if rep.StrictNumerator != 2 || rep.StrictDenominator != 3 {
		t.Errorf("the narrow reading's ratio is reported as %d of %d, want 2 of 3 — without it the report cannot show which reading was applied",
			rep.StrictNumerator, rep.StrictDenominator)
	}
	if rep.LinkTotal != 5 {
		t.Errorf("LinkTotal = %d, want 5", rep.LinkTotal)
	}
}

// TestInferRelationTarget_UnanimityRequiresEveryLinkToResolve pins the one
// branch that returns a nil report. A property where every RESOLVED link
// agreed but some links dangled is NOT unanimous — it has a shortfall an
// operator should see — so it must still carry a report.
func TestInferRelationTarget_UnanimityRequiresEveryLinkToResolve(t *testing.T) {
	byType := map[string]int{"task": 4}

	t.Run("no dangling links: unanimous, nothing to report", func(t *testing.T) {
		to, rep := inferRelationTarget("contact", linksWithDangling("related", byType, []string{"task"}, 0), danglingIndex(byType))
		if to != "task" {
			t.Errorf("to = %q, want task", to)
		}
		if rep != nil {
			t.Errorf("a genuinely unanimous property should carry no split report, got %+v", rep)
		}
	})

	t.Run("two dangling links: reported, not silent", func(t *testing.T) {
		to, rep := inferRelationTarget("contact", linksWithDangling("related", byType, []string{"task"}, 2), danglingIndex(byType))
		if to != "task" {
			t.Errorf("to = %q, want task (4 of 6 clears 2/3)", to)
		}
		if rep == nil {
			t.Fatal("4 of 6 links resolved to task and 2 dangled — that shortfall must be reported, not swallowed by the unanimity branch")
		}
		if rep.Rule != RelationSupermajority {
			t.Errorf("Rule = %q, want %q", rep.Rule, RelationSupermajority)
		}
		if rep.Unresolved != 2 {
			t.Errorf("Unresolved = %d, want 2", rep.Unresolved)
		}
	})
}
