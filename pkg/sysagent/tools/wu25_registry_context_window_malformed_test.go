package systools_test

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Chief fix round 2: rejection alone is insufficient; a malformed request must
// leave the actual entity JSON byte-identical. AgentUpdateRequest declares an
// integer with minimum 1, nullable for intentional clear, and no maximum.
// The storage type is *int, so 2^(IntSize-1) cannot fit a positive signed int;
// this representation case does not invent a product/model-specific token cap.
func TestWU25Registry_ContextWindowOverrideMalformedRefusesWithoutWrites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"string", "abc"},
		{"numeric_string", "256"}, // A numeric-looking string is still the wrong JSON kind.
		{"fractional", float64(1.5)},
		{"negative", float64(-1)}, // Below AgentUpdateRequest's minimum 1.
		{"zero", float64(0)},      // Minimum minus one: not an intentional null clear.
		{"boolean", true},
		{"int_overflow", math.Ldexp(1, strconv.IntSize-1)}, // First value above the signed-int positive range.
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, id := wu25F1SeedOverrides(t)
			before := h.state(t, id)
			readBefore := h.read(t, id)
			require.Equal(t, float64(wu25F1InitialContextTokens), readBefore["context_window_override"])
			require.Equal(t, before.Revision, readBefore["revision"])
			entityPath := filepath.Join(agentstore.New(h.home).Dir(), id+".json")
			beforeBytes, err := os.ReadFile(entityPath)
			require.NoError(t, err)
			require.NotEmpty(t, beforeBytes, "durable-byte instrument must see the real stored entity")
			beforeFiles := wu25Snapshot(t, h.home)
			result := h.call(t, "update_agent", map[string]any{
				"id": id, "revision": readBefore["revision"],
				"description":             "Must not be saved with an invalid context override.",
				"context_window_override": tc.value,
			})
			afterBytes, err := os.ReadFile(entityPath)
			require.NoError(t, err)
			fieldNamed := strings.Contains(result.ForLLM, "context_window_override")
			durableUnchanged := bytes.Equal(beforeBytes, afterBytes)
			t.Logf("WU25_MALFORMED_CASE input=%s IsError=%t field_named=%t durable_unchanged=%t", tc.name, result.IsError, fieldNamed, durableUnchanged)
			// Nonfatal assertions ensure M2's success cannot prevent the M3
			// zero-write assertion from being exercised in the same request.
			assert.True(t, result.IsError, "malformed context_window_override must be refused: %s", result.ForLLM)
			assert.Contains(t, result.ForLLM, "context_window_override", "diagnostic must name the field; exact wording is not the contract")
			assert.Equal(t, beforeBytes, afterBytes, "durable_unchanged: refused malformed context_window_override must not change even one stored config byte")
			assert.Equal(t, before, h.state(t, id), "refusal must preserve the whole saved configuration, SOUL and revision")
			assert.Equal(t, beforeFiles, wu25Snapshot(t, h.home), "refused sibling changes must not alter any durable file")
		})
	}
}

// Minimum and minimum-plus-one controls derive from AgentUpdateRequest's
// minimum 1. They prevent accidentally fixing malformed input by refusing all
// small integers. The original nil/4096/max-tool-clear controls stay unchanged.
func TestWU25Registry_ContextWindowOverrideMinimumValuesSet(t *testing.T) {
	for _, value := range []int{1, 2} {
		t.Run(strconv.Itoa(value), func(t *testing.T) {
			h, id := wu25F1SeedOverrides(t)
			before := h.state(t, id)
			readBefore := h.read(t, id)
			require.Equal(t, before.Revision, readBefore["revision"])
			files := wu25Snapshot(t, h.home)
			updated := wu25Success(t, h.call(t, "update_agent", map[string]any{
				"id": id, "revision": readBefore["revision"], "context_window_override": float64(value),
			}))
			assert.Equal(t, []any{"context_window_override"}, updated["changed_fields"])
			after := h.state(t, id)
			require.NotNil(t, after.Agent.ContextWindowOverride)
			assert.Equal(t, value, *after.Agent.ContextWindowOverride)
			want := *before.Agent
			want.ContextWindowOverride = &value
			wu25F1AssertOnlyTuningChanged(t, h, id, before, after, want, files)
			h.reopen(t)
			readAfter := h.read(t, id)
			assert.Equal(t, float64(value), readAfter["context_window_override"])
			assert.Equal(t, after.Revision, readAfter["revision"])
		})
	}
}
