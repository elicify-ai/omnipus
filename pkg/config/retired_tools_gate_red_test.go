package config

// RED tests — landing/skills (T9): the T-E9b retired-tools gate's
// defaults.go ceiling (§15 item 1): neither the sysagent policy family nor
// the merged global ceiling may name create_skill or remove_skill.
// Traces to: skills-spec-r15.md §15 item 1, §17.1 row 4, SC-002.
//
// RED expectation: both maps still seed the retired names — the assertions
// below fail on the baseline.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetiredTools_GoneFromDefaultsCeiling (§15 item 1).
// BDD: defaultToolPoliciesSysagent and the merged DefaultToolPolicyCeiling
// contain neither retired name.
// Traces to: skills-spec-r15.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromDefaultsCeiling(t *testing.T) {
	family := defaultToolPoliciesSysagent()
	require.NotEmpty(t, family, "the sysagent policy family is populated")
	for _, name := range []string{"create_skill", "remove_skill"} {
		assert.NotContains(t, family, name,
			"%s must be gone from defaultToolPoliciesSysagent (RED: it is still seeded)", name)
	}

	ceiling := DefaultToolPolicyCeiling()
	require.NotEmpty(t, ceiling, "the merged ceiling is populated")
	for _, name := range []string{"create_skill", "remove_skill"} {
		assert.NotContains(t, ceiling, name,
			"%s must be gone from the merged tool-policy ceiling (RED: it is still seeded)", name)
	}
}
