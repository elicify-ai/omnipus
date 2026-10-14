package systools_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wu25F1InitialContextTokens = 8192 // Stored value in independent CHECK F1's scenario.
	wu25F1SetContextTokens     = 4096 // Valid positive token count, distinct from 8192 so a no-op cannot pass.
	wu25F1InitialOwnToolLimit  = 1    // Minimum valid own limit in AgentUpdateRequest's contract.
)

// Arrange only the pre-state through the real store. Every operation under
// test uses the policy-filtered Omnipus registry, not a direct tool.Execute.
func wu25F1SeedOverrides(t *testing.T) (*wu25Harness, string) {
	t.Helper()
	h := newWU25Harness(t)
	id := wu25SeedAgent(t, h, "Main")
	state := h.state(t, id)
	_, err := agentstore.New(h.home).MutateState(id, state.Revision, func(a *config.AgentConfig) error {
		window := wu25F1InitialContextTokens
		a.ContextWindowOverride = &window
		a.MaxToolIterations = wu25F1InitialOwnToolLimit
		return nil
	}, nil)
	require.NoError(t, err)
	return h, id
}

// F1's independent oracle: the callable contract and AgentUpdateRequest both
// explicitly promise JSON null clears context_window_override. A direct Execute
// test cannot catch rejection by the registry before the tool is invoked.
func TestWU25Registry_ContextWindowOverrideNullClears(t *testing.T) {
	h, id := wu25F1SeedOverrides(t)
	before := h.state(t, id)
	readBefore := h.read(t, id)
	require.Equal(t, float64(wu25F1InitialContextTokens), readBefore["context_window_override"], "nondefault starting override must actually exist")
	require.Equal(t, before.Revision, readBefore["revision"])
	files := wu25Snapshot(t, h.home)
	result := h.call(t, "update_agent", map[string]any{
		"id": id, "revision": readBefore["revision"], "context_window_override": nil,
	})
	require.False(t, result.IsError, "CHECK F1: documented context_window_override:null must clear through the real registry, got %s", result.ForLLM)
	updated := wu25Success(t, result)
	assert.Equal(t, []any{"context_window_override"}, updated["changed_fields"])
	after := h.state(t, id)
	assert.Nil(t, after.Agent.ContextWindowOverride, "null must durably clear the override, not be ignored")
	want := *before.Agent
	want.ContextWindowOverride = nil
	wu25F1AssertOnlyTuningChanged(t, h, id, before, after, want, files)
	h.reopen(t)
	readAfter := h.read(t, id)
	value, present := readAfter["context_window_override"]
	require.True(t, present, "get_agent must explicitly return the cleared override")
	assert.Nil(t, value, "fresh get_agent readback must show no override, not the former token count")
	assert.Equal(t, after.Revision, readAfter["revision"])
}

// Positive control for F1: a real integer request reaches the writer and changes
// the saved/read-back value, so a null-clear failure is not a broken fixture.
func TestWU25Registry_ContextWindowOverrideIntegerSets(t *testing.T) {
	h, id := wu25F1SeedOverrides(t)
	before := h.state(t, id)
	readBefore := h.read(t, id)
	require.Equal(t, float64(wu25F1InitialContextTokens), readBefore["context_window_override"])
	require.Equal(t, before.Revision, readBefore["revision"])
	files := wu25Snapshot(t, h.home)
	updated := wu25Success(t, h.call(t, "update_agent", map[string]any{
		"id": id, "revision": readBefore["revision"], "context_window_override": float64(wu25F1SetContextTokens),
	}))
	assert.Equal(t, []any{"context_window_override"}, updated["changed_fields"])
	after := h.state(t, id)
	require.NotNil(t, after.Agent.ContextWindowOverride)
	assert.Equal(t, wu25F1SetContextTokens, *after.Agent.ContextWindowOverride)
	want := *before.Agent
	window := wu25F1SetContextTokens
	want.ContextWindowOverride = &window
	wu25F1AssertOnlyTuningChanged(t, h, id, before, after, want, files)
	h.reopen(t)
	readAfter := h.read(t, id)
	assert.Equal(t, float64(wu25F1SetContextTokens), readAfter["context_window_override"], "integer control must survive a fresh registry/store read")
	assert.Equal(t, after.Revision, readAfter["revision"])
}

// Regression control: fixing context-window nullable input must not break the
// already documented max_tool_iterations:null clear (0 = no own limit).
func TestWU25Registry_MaxToolIterationsNullClears(t *testing.T) {
	h, id := wu25F1SeedOverrides(t)
	before := h.state(t, id)
	require.Equal(t, wu25F1InitialOwnToolLimit, before.Agent.MaxToolIterations, "control starts with a real own limit")
	readBefore := h.read(t, id)
	require.Equal(t, before.Revision, readBefore["revision"])
	files := wu25Snapshot(t, h.home)
	updated := wu25Success(t, h.call(t, "update_agent", map[string]any{
		"id": id, "revision": readBefore["revision"], "max_tool_iterations": nil,
	}))
	assert.Equal(t, []any{"max_tool_iterations"}, updated["changed_fields"])
	after := h.state(t, id)
	assert.Equal(t, 0, after.Agent.MaxToolIterations, "null clears the persisted own value so the agent inherits the global limit")
	want := *before.Agent
	want.MaxToolIterations = 0
	wu25F1AssertOnlyTuningChanged(t, h, id, before, after, want, files)
	h.reopen(t)
	readAfter := h.read(t, id)
	assert.Equal(t, float64(wu25F1InitialContextTokens), readAfter["context_window_override"], "clearing the own tool cap must not also clear the context override")
	assert.Equal(t, after.Revision, readAfter["revision"])
}

func wu25F1AssertOnlyTuningChanged(t *testing.T, h *wu25Harness, id string, before, after *agentstore.State, want config.AgentConfig, files map[string]string) {
	t.Helper()
	actual := *after.Agent
	want.UpdatedAt, actual.UpdatedAt = nil, nil // Edit timestamp is bookkeeping, not a tuning/capability change.
	assert.Equal(t, want, actual, "only the specifically requested tuning field may change; visuals/permissions/runtime/mandate are preserved")
	assert.Equal(t, before.Soul, after.Soul)
	assert.NotEqual(t, before.Revision, after.Revision, "the test changes a stored value; a no-op success is not acceptable")
	wu25AssertOnlyEntityChanged(t, h, id, files)
}
