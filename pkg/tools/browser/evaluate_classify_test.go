// Package browser — unit tests for classifyEvalResult.
//
// These tests exercise the serialization-detection helper without a live browser,
// proving that nil raw bytes → non-serializable note, []byte("null") → genuine null,
// and valid JSON bytes → correctly unmarshalled value.
//
// Bug: browser_evaluate returned {"result":null, IsError:false} for both DOM nodes
// and intentional JS null, misleading the agent.
// Fix: chromedp.Evaluate receives a *[]byte; nil means "no serializable value from CDP".
package browser

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// decodeEvalResult parses the ForLLM JSON string and returns the map.
func decodeEvalResult(t *testing.T, forLLM string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(forLLM), &m), "ForLLM must be valid JSON: %s", forLLM)
	return m
}

// TestClassifyEvalResult_NilRaw verifies that a nil raw slice (CDP returned no
// serializable value — e.g. a DOM node, function, or circular object) produces a
// non-error result with result=null and a "note" explaining the non-serialization.
//
// Before the fix, this case returned {"result":null} with no note, indistinguishable
// from intentional JS null.
func TestClassifyEvalResult_NilRaw(t *testing.T) {
	result := classifyEvalResult(nil)
	require.NotNil(t, result)
	assert.False(t, result.IsError,
		"non-serializable value must NOT set IsError (it is not a JS exception)")

	m := decodeEvalResult(t, result.ForLLM)

	// result field must be null (JSON null → Go nil).
	resultVal, hasResult := m["result"]
	assert.True(t, hasResult, "response must contain 'result' key")
	assert.Nil(t, resultVal, "result must be null for non-serializable values")

	// note field must be present and mention non-serializable.
	note, hasNote := m["note"].(string)
	assert.True(t, hasNote, "response must contain 'note' string field for non-serializable values")
	assert.Contains(t, note, "not JSON-serializable",
		"note must explain why result is null; got: %s", note)
}

// TestClassifyEvalResult_GenuineNull verifies that JSON bytes "null" (CDP returned
// an intentional JS null) produces {"result":null} WITHOUT a note field, so the
// agent can distinguish intentional null from a non-serializable value.
func TestClassifyEvalResult_GenuineNull(t *testing.T) {
	result := classifyEvalResult([]byte("null"))
	require.NotNil(t, result)
	assert.False(t, result.IsError, "genuine null must not be an error")

	m := decodeEvalResult(t, result.ForLLM)

	resultVal, hasResult := m["result"]
	assert.True(t, hasResult, "response must contain 'result' key")
	assert.Nil(t, resultVal, "genuine JS null must deserialize to Go nil")

	// note must NOT be present for intentional null — only for non-serializable.
	_, hasNote := m["note"]
	assert.False(t, hasNote,
		"genuine null must NOT have a 'note' field (only non-serializable values get the note)")
}

// TestClassifyEvalResult_StringValue verifies that a serializable string value
// passes through correctly with no note field.
func TestClassifyEvalResult_StringValue(t *testing.T) {
	raw, err := json.Marshal("Execute E2E Fixture")
	require.NoError(t, err)

	result := classifyEvalResult(raw)
	require.NotNil(t, result)
	assert.False(t, result.IsError, "string value must not be an error")

	m := decodeEvalResult(t, result.ForLLM)

	resultVal, hasResult := m["result"]
	assert.True(t, hasResult, "response must contain 'result' key")
	assert.Equal(t, "Execute E2E Fixture", resultVal,
		"string value must round-trip correctly")
	_, hasNote := m["note"]
	assert.False(t, hasNote, "serializable string must not have a 'note' field")
}

// TestClassifyEvalResult_NumberValue verifies that a number passes through.
func TestClassifyEvalResult_NumberValue(t *testing.T) {
	raw := []byte("42")
	result := classifyEvalResult(raw)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	m := decodeEvalResult(t, result.ForLLM)
	resultVal, ok := m["result"].(float64)
	assert.True(t, ok, "number must deserialize as float64, got %T: %v", m["result"], m["result"])
	assert.Equal(t, float64(42), resultVal)
}

// TestClassifyEvalResult_ObjectValue verifies that a JSON object passes through.
func TestClassifyEvalResult_ObjectValue(t *testing.T) {
	raw := []byte(`{"x":1,"y":2}`)
	result := classifyEvalResult(raw)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	m := decodeEvalResult(t, result.ForLLM)
	obj, ok := m["result"].(map[string]any)
	assert.True(t, ok, "object must deserialize as map, got %T", m["result"])
	assert.Equal(t, float64(1), obj["x"])
	assert.Equal(t, float64(2), obj["y"])
}

// TestClassifyEvalResult_BoolValue verifies that boolean values pass through.
func TestClassifyEvalResult_BoolValue(t *testing.T) {
	for _, b := range []bool{true, false} {
		raw, _ := json.Marshal(b)
		result := classifyEvalResult(raw)
		require.NotNil(t, result)
		assert.False(t, result.IsError, "bool must not be an error")

		m := decodeEvalResult(t, result.ForLLM)
		got, ok := m["result"].(bool)
		assert.True(t, ok, "bool must deserialize as bool, got %T: %v", m["result"], m["result"])
		assert.Equal(t, b, got)
	}
}

// TestClassifyEvalResult_MalformedJSON verifies that malformed raw bytes (which
// should not happen in practice but guards against CDP bugs) are treated as
// non-serializable rather than causing a panic or returning a bad result.
func TestClassifyEvalResult_MalformedJSON(t *testing.T) {
	result := classifyEvalResult([]byte("{not valid json"))
	require.NotNil(t, result)
	assert.False(t, result.IsError, "malformed JSON must not set IsError")

	m := decodeEvalResult(t, result.ForLLM)
	_, hasNote := m["note"]
	assert.True(t, hasNote, "malformed JSON must produce a note like non-serializable values")
}
