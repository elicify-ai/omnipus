package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exact stdout line from real OpenCode 1.18.18, supplied by hands-on UAT
// (triage-1899smoke/run.stdout; exit 1, empty stderr). Team-lead's diagnostic
// oracle requires the name, verbatim message and reference in the surfaced
// error. Their joining punctuation is deliberately not specified.
const opencode11818CapturedError = `{"type":"error","timestamp":1795147660495,"sessionID":"ses_e08dc8729ffexGiTHbDIAcf2Ue","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_0be266a4"}}}`

func TestOpencodeCapturedError_PreservesProviderDiagnosticAndLegacyShapes(t *testing.T) {
	// The existing 2.2.6 capture is an independent control for the typed
	// error.message shape; it is not synthesized from the parser's output.
	typedCapture, err := os.ReadFile(filepath.Join("testdata", "opencode-2.2.6", "badmodel.stdout.ndjson"))
	require.NoError(t, err)
	for _, tc := range []struct {
		name, input, exact, errType string
		parts                       []string
	}{
		{
			name:  "captured_1_18_18_nested_data",
			input: opencode11818CapturedError + "\n",
			parts: []string{"UnknownError", "Unexpected server error. Check server logs for details.", "err_0be266a4"},
		},
		{
			name:    "captured_2_2_6_typed_message",
			input:   string(typedCapture),
			exact:   "Model unavailable: openrouter/no-such-vendor/no-such-model",
			errType: "provider.no-route", // #1857 C2, matching TestOpencode226R2_LiveRunTerminalOutcome.
		},
		{
			name:  "legacy_top_level_message",
			input: "{\"type\":\"error\",\"message\":\"quota exceeded\"}\n",
			exact: "quota exceeded",
		},
		{
			name:  "legacy_message_with_non_object_error",
			input: "{\"type\":\"error\",\"error\":\"legacy failure\",\"message\":\"legacy diagnostic\"}\n",
			exact: "legacy diagnostic",
		},
		{
			name:  "legacy_message_with_empty_nested_error",
			input: "{\"type\":\"error\",\"error\":{},\"message\":\"top-level fallback\"}\n",
			exact: "top-level fallback",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const runID = "opencode-captured-error"
			// One error line uses no agent steps; a positive cap keeps the
			// production parser's cap validation real without masking errors.
			events := ParseOpencodeStreamJSON([]byte(tc.input), runID, 1)
			require.Len(t, events, 1, "a provider failure yields one fatal error, never a success End")
			assert.Equal(t, EventKindError, events[0].Kind)
			assert.Equal(t, runID, events[0].RunID)
			require.NotNil(t, events[0].Err, "the provider failure must have a public diagnostic payload")
			if tc.exact != "" {
				assert.Equal(t, &ErrorEvent{Message: tc.exact, Fatal: true, Type: tc.errType}, events[0].Err,
					"legacy and typed provider messages must remain verbatim fatal errors")
				return
			}
			assert.True(t, events[0].Err.Fatal, "the real provider failure cannot become a successful completion")
			for _, part := range tc.parts {
				assert.Contains(t, events[0].Err.Message, part, "the surfaced error must retain this captured provider diagnostic")
			}
		})
	}
}
