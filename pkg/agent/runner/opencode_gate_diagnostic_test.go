package runner

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TEST-ANALYSIS #2 / the documented legacy diagnostic promise: preserve the
// provider's supplied name, message and reference without duplicating its name.
// Literal inputs and expected parts come from the finding's missing classes,
// not from running opencodeErrorDiagnostic. Joining punctuation is unspecified
// when a name/reference is added; verbatim and ref-only messages are exact.
func TestOpenCodeGateB2_DiagnosticPreservesReferenceDeduplicatesNameAndIgnoresNonStringMessage(t *testing.T) {
	for _, tc := range []struct {
		name, input, exact string
		parts              []string
		nameOnce           bool
	}{
		{
			name:  "reference_without_message_with_name",
			input: `{"type":"error","error":{"name":"UnknownError","data":{"ref":"err_gate_b2"}}}`,
			parts: []string{"UnknownError", "ref err_gate_b2"}, nameOnce: true,
		},
		{
			name:  "reference_without_message_or_name",
			input: `{"type":"error","error":{"data":{"ref":"err_gate_ref_only"}}}`,
			exact: "ref err_gate_ref_only",
		},
		{
			name:  "name_already_in_nested_message",
			input: `{"type":"error","error":{"name":"UnknownError","data":{"message":"UnknownError: upstream service unavailable"}}}`,
			exact: "UnknownError: upstream service unavailable", nameOnce: true,
		},
		{
			name:  "non_string_message_preserves_nested_diagnostic",
			input: `{"type":"error","error":{"name":"UnknownError","message":42,"data":{"message":"nested diagnostic remains readable","ref":"err_gate_nested"}}}`,
			parts: []string{"UnknownError", "nested diagnostic remains readable", "ref err_gate_nested"}, nameOnce: true,
		},
		{
			name:  "non_string_message_preserves_legacy_top_level_fallback",
			input: `{"type":"error","message":"legacy top-level diagnostic remains readable","error":{"message":{"unexpected":"object"}}}`,
			exact: "legacy top-level diagnostic remains readable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const runID = "gate-b2-parser-run"
			events := ParseOpencodeStreamJSON([]byte(tc.input+"\n"), runID, 1)
			require.Len(t, events, 1, "one actual parser error, never an empty selection or success End")
			assert.Equal(t, EventKindError, events[0].Kind)
			assert.Equal(t, runID, events[0].RunID)
			require.NotNil(t, events[0].Err)
			assert.True(t, events[0].Err.Fatal)
			assert.Empty(t, events[0].Err.Type, "legacy name/data diagnostics must not invent a typed provider code")
			if tc.exact != "" {
				assert.Equal(t, &ErrorEvent{Message: tc.exact, Fatal: true}, events[0].Err)
			}
			for _, part := range tc.parts {
				assert.Contains(t, events[0].Err.Message, part, "retain this independent provider input")
			}
			if tc.nameOnce {
				assert.Equal(t, 1, strings.Count(events[0].Err.Message, "UnknownError"), "do not prepend a second copy of the name")
			}
		})
	}
}
