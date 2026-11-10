package common_test

// effort_red_carrier_test.go — RED tests for the OpenRouter structured
// reasoning carrier (T-P5 / S-30): the decoder must keep every
// reasoning_details item losslessly (identity fields and opaque payloads),
// and the request serialisers must never emit details cross-route.
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// Traces to: S-30 (decoder capture half), S-31/S-32 (documented: their full
// decode → stored → codec → request loops need the private carrier that this
// decoder RED demands), S-33 (OpenRouter half), FR-013, T-P5.
//
// RED reason: ReasoningDetail keeps only format/index/type/text today, so
// id, signature and the opaque data payload are dropped at decode time.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

const reasoningDetailsBody = `{
	"choices": [{
		"message": {
			"role": "assistant",
			"content": "",
			"tool_calls": [{"id": "call_1", "type": "function",
				"function": {"name": "shell", "arguments": "{\"cmd\":\"ls\"}"}}],
			"reasoning_details": [
				{"type": "reasoning.text", "text": "step one", "format": "text", "index": 0},
				{"type": "reasoning.encrypted", "data": "OPAQUE-PAYLOAD-7731", "format": "encrypted", "index": 1},
				{"type": "reasoning.id", "id": "rs_abc123", "format": "id", "index": 2, "signature": "SIG-7731"}
			]
		},
		"finish_reason": "tool_calls"
	}],
	"usage": {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}
}`

// TestT_C6_S30_DecoderKeepsReasoningDetailsLosslessly pins S-30's capture
// half: a non-streaming response's reasoning_details survive the decoder with
// every field the provider sent — identity fields (id, format, index,
// signature) and each opaque payload (data) unchanged, in order.
// Traces to: S-30, FR-013, T-P5.
func TestT_C6_S30_DecoderKeepsReasoningDetailsLosslessly(t *testing.T) {
	resp, err := common.ParseResponse(strings.NewReader(reasoningDetailsBody), "openrouter")
	require.NoError(t, err, "the recorded OpenRouter response parses")
	require.NotNil(t, resp)
	require.Len(t, resp.ReasoningDetails, 3, "all three items are captured, in order — PIN")

	raw, err := json.Marshal(resp.ReasoningDetails)
	require.NoError(t, err)
	var items []map[string]any
	require.NoError(t, json.Unmarshal(raw, &items))

	assert.Equal(t, "OPAQUE-PAYLOAD-7731", items[1]["data"],
		"the encrypted item's opaque data payload survives the decoder (S-30) — RED: dropped by the lossy ReasoningDetail struct")
	assert.Equal(t, "SIG-7731", items[2]["signature"],
		"the identity item's signature survives the decoder (S-30) — RED")
	assert.Equal(t, "rs_abc123", items[2]["id"],
		"the identity item's id survives the decoder (S-30) — RED")
	assert.Equal(t, "step one", items[0]["text"], "the text item survives — PIN")
}

// TestT_C6_S33_SerialisersNeverEmitDetailsCrossRoute pins S-33's OpenRouter
// half at the shared serialisers: with the carrier fields absent from the
// message type, no serialised OpenAI-shaped request can emit reasoning_details
// for a history that only carries Anthropic Thinking Blocks, and vice versa
// the details never leave the OpenRouter route. These pins must hold at GREEN
// when the private carrier exists (the mutation "emit reasoning_details to
// every provider" must fail this pin).
// Traces to: S-33, US-5 AS3-4, T-P5.
func TestT_C6_S33_SerialisersNeverEmitDetailsCrossRoute(t *testing.T) {
	mixed := []common.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", Content: "", ThinkingBlocks: []string{`{"type":"thinking","thinking":"hidden steps","signature":"SIGNATURE-BLOCK-7731"}`},
			ToolCalls: []common.ToolCall{{ID: "call_1", Function: &common.FunctionCall{Name: "shell"}}}},
		{Role: "tool", Content: "done", ToolCallID: "call_1"},
	}
	raw, err := json.Marshal(common.SerializeCacheMessages(mixed))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "reasoning_details",
		"the OpenAI-shaped serialiser never emits reasoning_details (S-33) — PIN")
	assert.NotContains(t, string(raw), "SIGNATURE-BLOCK-7731",
		"Anthropic Thinking Blocks never leak into the OpenAI-shaped serialiser (S-33) — PIN")

	withContent := []common.Message{{Role: "assistant", Content: "answer", ReasoningContent: "hidden steps"}}
	raw2, err := json.Marshal(common.SerializeMessages(withContent))
	require.NoError(t, err)
	assert.NotContains(t, string(raw2), "reasoning_details",
		"display reasoning never becomes a reasoning_details member (S-33) — PIN")
}
