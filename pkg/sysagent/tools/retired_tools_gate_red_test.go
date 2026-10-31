package systools_test

// RED tests — landing/skills (T9): the T-E9b retired-tools gate's "real
// registered tool set" surface (§15 item 1): the composition boot registers
// (systools.AllTools — the same function buildCentralBuiltinRegistry calls)
// must not contain a tool named create_skill or remove_skill.
// Traces to: skills-spec-r13.md §15 item 1, §17.1 row 4, SC-002.
//
// RED expectation: AllTools still returns both retired tools — the
// assertion below fails on the baseline.

import (
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetiredTools_GoneFromRegisteredSet (§15 item 1).
// BDD: the real registered tool set (AllTools, the system family boot
// registers) contains neither retired name.
// Traces to: skills-spec-r13.md §15 item 1, T-E9b.
func TestRetiredTools_GoneFromRegisteredSet(t *testing.T) {
	deps, _ := newTestDeps(t)
	registered := systools.AllTools(deps)
	require.NotEmpty(t, registered, "the system family registers tools")
	names := make([]string, 0, len(registered))
	for _, tool := range registered {
		names = append(names, tool.Name())
	}
	for _, name := range []string{"create_skill", "remove_skill"} {
		assert.NotContains(t, names, name,
			"%s must be gone from the real registered tool set (RED: it is still registered)", name)
	}
}
