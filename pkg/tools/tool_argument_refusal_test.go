package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolArgsCap_StructuredRefusal is spec test 12's family-shape half
// (ADR-066 D4, FR-016, B-19): the refusal the loop returns INSTEAD of
// executing an oversized tool call is a ToolArgumentRefusal payload — the
// ADR-060 family discriminator, the tool as the model named it, the size it
// sent and the live cap — routed through marshalWithinBudget so it stays
// under the 2000-rune downstream truncation cap whatever the tool name is.
// Enforcement (the > 64,000 check in runTurn) is T066-15's.
func TestToolArgsCap_StructuredRefusal(t *testing.T) {
	t.Run("populated shape", func(t *testing.T) {
		res := ToolArgumentRefusalResult("write_file", 64_001, 64_000)
		require.NotNil(t, res)
		assert.True(t, res.IsError, "a refusal is delivered as an error result so the frame status is error")
		require.Error(t, res.Err)

		var parsed map[string]any
		require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &parsed), "ForLLM must be the JSON payload: %s", res.ForLLM)
		assert.Equal(t, ToolArgumentsTooLargeCode, parsed["error"])
		assert.Equal(t, "tool_arguments_too_large", parsed["error"])
		assert.Equal(t, "write_file", parsed["tool"])
		assert.EqualValues(t, 64_001, parsed["size_chars"])
		assert.EqualValues(t, 64_000, parsed["cap_chars"])
		reason, _ := parsed["reason"].(string)
		assert.Contains(t, reason, "write_file")
		assert.Contains(t, reason, "64001")
		assert.Contains(t, reason, "64000")
		// additionalProperties:false — exactly the five contract fields.
		assert.Len(t, parsed, 5)
	})

	t.Run("hostile tool name stays under the downstream cap", func(t *testing.T) {
		hostile := strings.Repeat(`<t>"&\`, 600) + "\x01\x7f"
		raw, err := ToolArgumentRefusalPayload(hostile, 70_000, 64_000)
		require.NoError(t, err)
		assert.LessOrEqual(t, len([]rune(string(raw))), maxRefusalPayloadRunes)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed))
		assert.Equal(t, ToolArgumentsTooLargeCode, parsed["error"])
		tool, _ := parsed["tool"].(string)
		assert.NotEmpty(t, tool)
		assert.LessOrEqual(t, len([]rune(tool)), 128, "contract maxLength:128")
	})

	t.Run("zero values are defaulted to schema-valid minimums", func(t *testing.T) {
		raw, err := ToolArgumentRefusalPayload("", 0, 0)
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed))
		assert.NotEmpty(t, parsed["tool"])
		assert.EqualValues(t, 1, parsed["size_chars"])
		assert.EqualValues(t, 1, parsed["cap_chars"])
	})

	t.Run("registered in the family enumeration", func(t *testing.T) {
		assert.Contains(t, AllStructuredFailureCodes(), ToolArgumentsTooLargeCode)
		assert.Contains(t, AllStructuredFailureCodes(), ToolResultRecallMarkCode)
	})
}

// TestRecallMarkPayload_FamilyShape covers the pkg/tools half of FR-018:
// the two mark producers (capped / emptied) share one ToolResultRecallMark
// shape, sanitise their name and id fields, and default schema minimums.
func TestRecallMarkPayload_FamilyShape(t *testing.T) {
	p := RecallMarkParams{
		Tool:        "mcp_github_search",
		ToolCallID:  "call_7f3a",
		ArchiveLine: 41,
		SizeChars:   1_178_522,
		Turn:        9,
	}
	for state, build := range map[string]func(RecallMarkParams) ([]byte, error){
		"capped":  CappedMarkPayload,
		"emptied": EmptiedMarkPayload,
	} {
		raw, err := build(p)
		require.NoError(t, err, state)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed), state)
		assert.Equal(t, ToolResultRecallMarkCode, parsed["error"], state)
		assert.Equal(t, "tool_result_recall_mark", parsed["error"], state)
		assert.Equal(t, state, parsed["content_state"], state)
		assert.Equal(t, "mcp_github_search", parsed["tool"], state)
		assert.Equal(t, "call_7f3a", parsed["tool_call_id"], state)
		assert.EqualValues(t, 41, parsed["archive_line"], state)
		assert.EqualValues(t, 1_178_522, parsed["size_chars"], state)
		assert.EqualValues(t, 9, parsed["turn"], state)
		hint, _ := parsed["hint"].(string)
		assert.Contains(t, hint, "recall_conversation", state)
		assert.Contains(t, hint, "call_7f3a", state)
		assert.Contains(t, hint, "41", state)
		assert.Len(t, parsed, 8, "additionalProperties:false — exactly the eight contract fields")
		assert.LessOrEqual(t, len([]rune(string(raw))), maxRefusalPayloadRunes)
	}

	t.Run("sanitised fields", func(t *testing.T) {
		hostile := "evil\x00\x1b[31m" + strings.Repeat("x", 100) + "\n"
		raw, err := EmptiedMarkPayload(RecallMarkParams{Tool: hostile, ToolCallID: hostile})
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed))
		for _, k := range []string{"tool", "tool_call_id"} {
			v, _ := parsed[k].(string)
			assert.LessOrEqual(t, len([]rune(v)), 64, k)
			assert.NotContains(t, v, "\x00", k)
			assert.NotContains(t, v, "\x1b", k)
			assert.NotContains(t, v, "\n", k)
			assert.True(t, strings.HasPrefix(v, "evil[31mxxx"), "%s: non-printables stripped, head kept: %q", k, v)
		}
		assert.EqualValues(t, 0, parsed["archive_line"])
		assert.EqualValues(t, 1, parsed["size_chars"], "minimum:1 defaulted")
		assert.EqualValues(t, 1, parsed["turn"], "minimum:1 defaulted")
	})

	t.Run("all-control-character name does not go empty", func(t *testing.T) {
		raw, err := CappedMarkPayload(RecallMarkParams{Tool: "\x00\x01", ToolCallID: ""})
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(raw, &parsed))
		assert.NotEmpty(t, parsed["tool"])
		assert.NotEmpty(t, parsed["tool_call_id"])
	})
}
