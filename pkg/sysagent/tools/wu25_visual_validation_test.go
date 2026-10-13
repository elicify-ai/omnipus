package systools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wu25InvalidVisual struct {
	field string
	name  string
	value any
}

// Derived equivalence classes: closed-set miss, case/label/artwork confusion,
// empty, null, and each non-string JSON kind. These are not guessed valid values.
func wu25InvalidVisuals(update bool) []wu25InvalidVisual {
	cases := []wu25InvalidVisual{
		{"figure", "unknown", "Dragon"}, {"figure", "art_key", "octopus"},
		{"figure", "case", "robot"}, {"figure", "empty", ""}, {"figure", "whitespace", " Robot "},
		{"role", "unknown", "administrator"}, {"role", "display_label", "General assistant"},
		{"role", "case", "Writer"}, {"role", "empty", ""}, {"role", "whitespace", " writer "},
		{"color", "outside_palette", "#22C55E"}, {"color", "retired_palette", "#3B82F6"},
		{"color", "malformed", "not-a-hex"}, {"color", "short", "#123"},
		{"color", "long", "#6E8BA800"}, {"color", "label", "Bronze"}, {"color", "whitespace", " #6E8BA8 "},
	}
	for _, field := range []string{"figure", "role", "color"} {
		for _, bad := range []struct {
			name  string
			value any
		}{
			{"null", nil}, {"number", float64(0)}, {"boolean", false},
			{"array", []any{"Robot"}}, {"object", map[string]any{"value": "Robot"}},
		} {
			cases = append(cases, wu25InvalidVisual{field: field, name: bad.name, value: bad.value})
		}
	}
	if update {
		// AgentColor.yaml: explicit invalid update is rejected, omission preserves.
		// Empty CREATE color has a prior named default decision; not overridden here.
		cases = append(cases, wu25InvalidVisual{field: "color", name: "empty", value: ""})
	}
	return cases
}

func wu25AssertInvalidReason(t *testing.T, body, field string) {
	t.Helper()
	assert.Contains(t, body, field, "R1: the model-visible reason identifies the rejected visual")
	tokens := wu25ChoiceTokens(body)
	for _, value := range wu25CatalogFor(t, field).values {
		assert.True(t, tokens[value], "R1: invalid %s reason must list valid choice %q; got %s", field, value, body)
	}
	assert.NotContains(t, body, "crashed with panic", "rejection must be a validation result, not a panic")
	assert.NotContains(t, body, "not found", "registered-tool control must prevent unrelated missing-tool failures")
}

// R1: a model must receive the complete existing choice list, not a silent
// ignored field or an unknown-parameter error. Mixed ordinary/invalid writes
// are zero-write, using a snapshot over all durable files as the oracle.
func TestWU25CreateAgent_InvalidVisualsExplainChoicesAndWriteNothing(t *testing.T) {
	for _, tc := range wu25InvalidVisuals(false) {
		t.Run(tc.field+"/"+tc.name, func(t *testing.T) {
			h := newWU25Harness(t)
			before := wu25Snapshot(t, h.home)
			args := wu25CreateArgs("Refused Visual", "Main")
			args["heartbeat"] = "Must not be persisted."
			args[tc.field] = tc.value
			result := h.call(t, "create_agent", args)
			require.True(t, result.IsError, "R1: invalid %s %s must be refused, got %s", tc.field, tc.name, result.ForLLM)
			wu25AssertInvalidReason(t, result.ForLLM, tc.field)
			assert.Equal(t, before, wu25Snapshot(t, h.home), "invalid create must not write any durable part")
			stored, unreadable, err := agentstore.New(h.home).List()
			require.NoError(t, err)
			require.Empty(t, unreadable, "unreadable records must not hide a create")
			assert.Empty(t, stored, "invalid create must not publish an agent")
		})
	}
}

func TestWU25UpdateAgent_InvalidVisualsExplainChoicesAndWriteNothing(t *testing.T) {
	for _, tc := range wu25InvalidVisuals(true) {
		t.Run(tc.field+"/"+tc.name, func(t *testing.T) {
			h := newWU25Harness(t)
			id := wu25SeedAgent(t, h, "Main")
			state := h.state(t, id)
			before := wu25Snapshot(t, h.home)
			result := h.call(t, "update_agent", map[string]any{
				"id": id, "revision": state.Revision,
				"description": "Must not change on refusal.", "soul": "Must not be persisted.",
				tc.field: tc.value,
			})
			require.True(t, result.IsError, "R1: invalid %s %s must be refused, got %s", tc.field, tc.name, result.ForLLM)
			wu25AssertInvalidReason(t, result.ForLLM, tc.field)
			assert.Equal(t, state, h.state(t, id), "R1: invalid update must preserve revision and all fields")
			assert.Equal(t, before, wu25Snapshot(t, h.home), "R1: invalid update must not change any durable file")
		})
	}
}
