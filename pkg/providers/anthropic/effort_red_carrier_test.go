package anthropicprovider

// effort_red_carrier_test.go — RED tests for the native-Anthropic effort wire
// (T-P5): the reasoning_effort option must drive the thinking/output_config
// encoding, the legacy thinking_level option is deleted (FR-056), Thinking
// Blocks stay the route's only carrier, and no-effort requests stay untouched.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-34, S-90, S-33 (Anthropic half), S-69 (wire half), S-35
// (Bedrock shares this builder), FR-014, FR-047, FR-056, T-P5.
//
// RED reason: buildParams reads only the legacy thinking_level option today;
// a reasoning_effort option is ignored entirely, so every assertion that the
// effort reaches the wire fails. The Thinking Blocks replay and the
// no-option pin pass at baseline by design.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func marshalParams(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	return m
}

// TestT_P5_S34_S90_EffortDrivesTheAnthropicWire pins S-34/S-90's wire half:
// effort High enables thinking with the effort encoding; effort Off sends
// thinking {type: "disabled"} and no output_config; no effort sends neither.
// Traces to: S-34, S-90, FR-014, FR-047, T-P5.
func TestT_P5_S34_S90_EffortDrivesTheAnthropicWire(t *testing.T) {
	simple := []Message{{Role: "user", Content: "hello"}}

	t.Run("high enables thinking with the effort encoding", func(t *testing.T) {
		params, err := buildParams(simple, nil, "claude-m", map[string]any{"reasoning_effort": "high"})
		require.NoError(t, err)
		m := marshalParams(t, params)
		require.Contains(t, m, "thinking",
			"effort high reaches the wire as a thinking config (S-34/S-90) — RED: the reasoning_effort option is ignored")
		_, hasOutputConfig := m["output_config"]
		assert.True(t, hasOutputConfig, "the effort encoding carries output_config (S-34) — RED")
	})

	t.Run("off sends thinking disabled and no output_config", func(t *testing.T) {
		params, err := buildParams(simple, nil, "claude-m", map[string]any{"reasoning_effort": "off"})
		require.NoError(t, err)
		m := marshalParams(t, params)
		thinking, has := m["thinking"]
		require.True(t, has, "Off is encoded explicitly as thinking disabled (S-90/FR-047) — RED")
		tm, ok := thinking.(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "disabled", tm["type"], "thinking.type is disabled (S-90) — RED")
		assert.NotContains(t, m, "output_config", "no output_config on an Off request (S-90) — RED")
	})

	t.Run("no effort sends neither (S-34 base pin)", func(t *testing.T) {
		params, err := buildParams(simple, nil, "claude-m", map[string]any{})
		require.NoError(t, err)
		m := marshalParams(t, params)
		assert.NotContains(t, m, "thinking", "no thinking without an effort option — PIN")
		assert.NotContains(t, m, "output_config", "no output_config without an effort option — PIN")
	})

	t.Run("tool loop without ThinkingBlocks gets no enabling effort (S-34 guard pin at the wire)", func(t *testing.T) {
		loop := []Message{
			{Role: "user", Content: "go"},
			{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "t1", Function: &FunctionCall{Name: "shell"}}}},
			{Role: "tool", Content: "done", ToolCallID: "t1"},
		}
		params, err := buildParams(loop, nil, "claude-m", map[string]any{"reasoning_effort": "high"})
		require.NoError(t, err)
		m := marshalParams(t, params)
		assert.NotContains(t, m, "thinking",
			"a current loop lacking its own blocks gets no enabling thinking (S-34) — RED: the effort option is ignored wholesale today, so the guard cannot be distinguished from the bug; this pin must hold at GREEN")
	})
}

// TestT_D1_S69_ThinkingLevelOptionDeleted pins FR-056's wire half: after the
// deletion the legacy thinking_level option must no longer request thinking.
// Traces to: S-69, FR-056, T-D1.
func TestT_D1_S69_ThinkingLevelOptionDeleted(t *testing.T) {
	params, err := buildParams(
		[]Message{{Role: "user", Content: "hello"}}, nil, "claude-m",
		map[string]any{"thinking_level": "high"},
	)
	require.NoError(t, err, "the old option does not break the request — PIN")
	m := marshalParams(t, params)
	assert.NotContains(t, m, "thinking",
		"the legacy thinking_level option requests nothing after the deletion (S-69/FR-056) — RED: it still drives thinking today")
}

// TestT_P5_S33_ThinkingBlocksAreTheOnlyAnthropicCarrier pins S-33's Anthropic
// half: the Anthropic request replays Thinking Blocks and never
// reasoning_details, and a reasoning_content message never leaks a detail.
// Traces to: S-33, US-5 AS3-4, T-P5.
func TestT_P5_S33_ThinkingBlocksAreTheOnlyAnthropicCarrier(t *testing.T) {
	blocks := []Message{
		{Role: "assistant", ThinkingBlocks: []string{`{"type":"thinking","thinking":"hidden steps","signature":"SIGNATURE-BLOCK-7731"}`}, Content: "", ToolCalls: []ToolCall{{ID: "t1", Function: &FunctionCall{Name: "shell"}}}},
		{Role: "tool", Content: "done", ToolCallID: "t1"},
	}
	params, err := buildParams(blocks, nil, "claude-m", map[string]any{})
	require.NoError(t, err)
	raw, err := json.Marshal(params)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "SIGNATURE-BLOCK-7731",
		"the Anthropic request replays its own Thinking Blocks (S-33) — PIN")

	contentOnly := []Message{{Role: "assistant", Content: "text", ReasoningContent: "hidden steps"}}
	params2, err := buildParams(contentOnly, nil, "claude-m", map[string]any{})
	require.NoError(t, err)
	raw2, err := json.Marshal(params2)
	require.NoError(t, err)
	assert.NotContains(t, string(raw2), "reasoning_details",
		"the Anthropic request never carries reasoning_details (S-33) — PIN")
	assert.NotContains(t, string(raw2), "hidden steps",
		"display reasoning text never reaches the wire (S-33) — PIN")
}
