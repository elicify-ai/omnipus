package agent

import (
	"strings"
	"testing"
)

// Oracle data comes from founder R1-R3 and the approved 2026-10-21 prompt-owner
// facts, not production output or compactGuidanceFactPattern's return values.
// Opposites and negated wrappers must fail even when all fact keywords remain.
type compactGuidanceFactFixture struct {
	name, fact, positive, opposite, wildcardTrap string
}

func compactGuidanceFactFixtures() []compactGuidanceFactFixture {
	return []compactGuidanceFactFixture{
		{
			name: "Authority", fact: "authority",
			positive:     "This current workspace roster alone defines your permitted delegation routes.",
			opposite:     "This current workspace roster is not authoritative for permitted delegation routes.",
			wildcardTrap: "This current workspace roster alone does not define your permitted delegation routes.",
		},
		{
			name: "ExclusiveRoutes", fact: "exclusive",
			positive:     "This current workspace roster alone defines your permitted delegation routes.",
			opposite:     "These are not your only permitted delegation targets.",
			wildcardTrap: "This current workspace roster alone does not define your permitted delegation routes.",
		},
		{
			name: "NoDiscovery", fact: "no discovery",
			positive:     "do not use list_agents or memory to find additional routes.",
			opposite:     "Use list_agents or memory to find additional routes.",
			wildcardTrap: "Do not avoid list_agents or memory to find additional routes.",
		},
		{
			name: "NoExtraGrants", fact: "no extra grants",
			positive:     "Descriptions or names elsewhere grant no additional access.",
			opposite:     "Descriptions or names elsewhere grant additional access.",
			wildcardTrap: "Descriptions or names elsewhere are not limited to no additional access.",
		},
		{
			name: "RuntimeChecks", fact: "runtime checks",
			positive:     "Runtime graph, mode, depth and tool checks still apply, including each listed edge's maximum chain depth.",
			opposite:     "Runtime graph, mode, depth and tool checks do not apply, including each listed edge's maximum chain depth.",
			wildcardTrap: "Runtime graph, mode, depth and tool checks never apply, including each listed edge's maximum chain depth.",
		},
	}
}

func compactOracleGuidance(body string) string {
	return "<delegation_guidance>\n" + body + "\n</delegation_guidance>"
}

func TestCompactGuidanceFactOracle(t *testing.T) {
	t.Run("FactPolarity", testCompactGuidanceFactPolarity)
	t.Run("UnlistedDenialPolarity", testCompactGuidanceUnlistedDenialPolarity)
	t.Run("ExclusivityIndex", testCompactGuidanceExclusivityIndexPolarity)
	t.Run("RuntimeWithoutEdgeQualifier", func(t *testing.T) {
		guidance := compactOracleGuidance("Runtime graph, mode, depth and tool checks still apply.")
		if !compactGuidanceHasFact(t, guidance, "runtime checks") {
			t.Fatal("the complete affirmative runtime-check fact does not require the optional edge qualifier")
		}
	})
}

func testCompactGuidanceFactPolarity(t *testing.T) {
	fixtures := compactGuidanceFactFixtures()
	if len(fixtures) != 5 { // one positive/negative pack for each swept fact class
		t.Fatalf("fact fixture classes = %d, want 5", len(fixtures))
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			cases := []struct {
				name, statement string
				want            bool
			}{
				{"Approved", fixture.positive, true},
				{"CaseInsensitive", strings.ToUpper(fixture.positive), true},
				{"SentenceBoundary", "Context.\n" + fixture.positive, true},
				{"ClauseBoundary", "Context; " + fixture.positive, true},
				{"Opposite", fixture.opposite, false},
				{"WildcardNegation", fixture.wildcardTrap, false},
				{"NegatedWrapper", "It is false that " + fixture.positive, false},
				{"Empty", "", false},
				{"Unrelated", "Use completion messages for progress.", false},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					got := compactGuidanceHasFact(t, compactOracleGuidance(tc.statement), fixture.fact)
					if got != tc.want {
						t.Errorf("fact %q for %q = %v, want %v", fixture.fact, tc.statement, got, tc.want)
					}
				})
			}
			t.Run("PositiveOutsideGuidanceCannotRescueOpposite", func(t *testing.T) {
				got := compactOracleGuidance(fixture.opposite) + "\n<team_directory>\n" + fixture.positive + "\n</team_directory>"
				if compactGuidanceHasFact(t, got, fixture.fact) {
					t.Errorf("fact %q was incorrectly rescued by positive words outside guidance", fixture.fact)
				}
			})
		})
	}
}

func testCompactGuidanceUnlistedDenialPolarity(t *testing.T) {
	var required []compactGuidanceFactFixture
	for _, fixture := range compactGuidanceFactFixtures() {
		switch fixture.fact {
		case "exclusive", "no extra grants", "runtime checks":
			required = append(required, fixture)
		}
	}
	// The existing denial oracle requires precisely these three positive facts.
	if len(required) != 3 {
		t.Fatalf("denial fixture prerequisites = %d, want 3", len(required))
	}
	positive := make([]string, len(required))
	for i, fixture := range required {
		positive[i] = fixture.positive
	}
	if !compactGuidanceDeniesUnlisted(t, compactOracleGuidance(strings.Join(positive, "\n"))) {
		t.Fatal("all three approved denial facts must satisfy the positive control")
	}
	for i, fixture := range required {
		t.Run(fixture.name, func(t *testing.T) {
			cases := []struct{ name, statement string }{
				{"Opposite", fixture.opposite},
				{"WildcardNegation", fixture.wildcardTrap},
				{"NegatedWrapper", "It is false that " + fixture.positive},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					statements := append([]string(nil), positive...)
					statements[i] = tc.statement
					if compactGuidanceDeniesUnlisted(t, compactOracleGuidance(strings.Join(statements, "\n"))) {
						t.Errorf("denial oracle accepted false %q fact %q while other facts stayed positive", fixture.fact, tc.statement)
					}
				})
			}
		})
	}
}

func testCompactGuidanceExclusivityIndexPolarity(t *testing.T) {
	const beforeRule = "## Delegation\n<delegation_guidance>\nUnrelated preface.\n"
	const afterRule = "\n</delegation_guidance>\n\nmax chain depth: 10"
	found := false
	for _, fixture := range compactGuidanceFactFixtures() {
		if fixture.fact != "exclusive" {
			continue
		}
		found = true
		cases := []struct {
			name, statement string
			want            int
		}{
			{"Approved", fixture.positive, len(beforeRule)},
			{"Opposite", fixture.opposite, -1},
			{"WildcardNegation", fixture.wildcardTrap, -1},
			{"NegatedWrapper", "It is false that " + fixture.positive, -1},
			{"Missing", "Unrelated statement.", -1},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := compactGuidanceExclusivityIndex(t, beforeRule+tc.statement+afterRule)
				if got != tc.want {
					t.Errorf("exclusive-rule index for %q = %d, want %d", tc.statement, got, tc.want)
				}
			})
		}
	}
	if !found {
		t.Fatal("exclusive-rule fixture is missing; index cases did not run")
	}
}
