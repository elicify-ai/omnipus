package systools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief Q1 A, 2026-10-14: CREATE omitted/null/empty identity values request the
// exact existing defaults; they are not invalid catalog choices. UPDATE keeps
// its strict null/empty refusal. Oracles: chief ruling and docs/agents.md's
// "How an agent looks", not the result of a create_agent execution.
func TestWU25CreateAgent_DefaultRequestsPersistExactDefaults(t *testing.T) {
	defaults := map[string]string{
		"figure": coreagent.DefaultFigure,
		"role":   coreagent.DefaultRole,
		"color":  coreagent.DefaultColor,
	}
	require.Equal(t, map[string]string{
		"figure": "Omnipus", "role": "general", "color": "#8E9196",
	}, defaults, "Q1 A reuses the documented shipped defaults, not a new identity choice")
	for _, runtimeType := range []string{"Main", "Subagent"} {
		for _, target := range []string{"figure", "role", "color", "all"} {
			for _, request := range []struct {
				name    string
				omitted bool
				value   any
			}{
				{name: "omitted", omitted: true},
				{name: "null", value: nil},
				{name: "empty", value: ""},
			} {
				t.Run(runtimeType+"/"+target+"/"+request.name, func(t *testing.T) {
					h := newWU25Harness(t)
					args := wu25CreateArgs("Default Visual Creation", runtimeType)
					// Sibling visuals are deliberately nondefault. A default request
					// for one field must not reset the other supplied choices.
					want := map[string]string{"figure": "Robot", "role": "writer", "color": "#A97142"}
					for field, value := range want {
						args[field] = value
					}
					fields := []string{target}
					if target == "all" {
						fields = []string{"figure", "role", "color"}
					}
					for _, field := range fields {
						want[field] = defaults[field]
						if request.omitted {
							delete(args, field)
						} else {
							args[field] = request.value
						}
					}
					result := h.call(t, "create_agent", args)
					require.False(t, result.IsError, "Q1 A: CREATE %s %s must select its exact default: %s", target, request.name, result.ForLLM)
					created := wu25Success(t, result)
					id, ok := created["id"].(string)
					require.True(t, ok)
					require.NotEmpty(t, id)
					state := h.state(t, id)
					assert.Equal(t, want, wu25StoredVisuals(state.Agent), "Q1 A: defaults must be persisted, not empty values hidden by a read fallback")
					assert.Equal(t, "Keep the original mandate.", state.Agent.Description)
					assert.Equal(t, "Only prepare drafts; never change permissions.", state.Soul)
					require.NotNil(t, state.Agent.Model)
					assert.Equal(t, "test/model", state.Agent.Model.Primary)
					h.reopen(t)
					read := h.read(t, id)
					wu25AssertVisuals(t, read, want)
					assert.Equal(t, state.Revision, read["revision"])
					assert.Equal(t, runtimeType, read["type"], "default visuals must not select a different runtime")
				})
			}
		}
	}
}
