package gateway

// ADR-059 W5 — the structured write_file refusal must reach the SPA as a typed
// object, identically live and on replay.
//
// The risk this guards is not "does the parser work". It is DIVERGENCE: the
// live path parses structured failures into an object and lifts the prose
// reason into the frame's error field, while replay reconstructs the frame
// from the persisted transcript, where the same payload is stored as a raw
// JSON string in ToolCall.Error. Without matching treatment, a page reload
// would replace a rendered sentence with a raw JSON blob — a regression a user
// only sees after refreshing, which is the hardest kind to notice in review.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

const writeRefusalPayload = `{"error":"file_exists","path":"/w/a.svg",` +
	`"reason":"file: /w/a.svg already exists. Set overwrite=true to replace.","tool":"write_file"}`

func TestParseStructuredToolFailure_RecognizesWriteRefusal(t *testing.T) {
	obj, reason, ok := parseStructuredToolFailure(writeRefusalPayload)
	require.True(t, ok, "write_file's refusal payload must be recognized as structured — otherwise "+
		"the SPA receives the raw JSON string where it used to receive a sentence")
	assert.Equal(t, "file_exists", obj["error"])
	assert.Equal(t, "write_file", obj["tool"])
	assert.Equal(t, "/w/a.svg", obj["path"])
	assert.Contains(t, reason, "already exists",
		"the prose reason must be lifted out for renderers that show only the error field")
}

func TestParseStructuredToolFailure_StillRecognizesDelegationDenial(t *testing.T) {
	obj, reason, ok := parseStructuredToolFailure(
		`{"error":"delegation_denied","reason":"not in trust set","policy":"trust_set","tool":"delegate"}`)
	require.True(t, ok, "generalizing the parser must not drop the case it was written for")
	assert.Equal(t, "delegation_denied", obj["error"])
	assert.Equal(t, "not in trust set", reason)
}

// TestParseStructuredToolFailure_IgnoresUnknownDiscriminators is the reason
// this is an allow-list rather than "any JSON object with an error field".
// Plenty of tools return JSON that happens to contain an `error` key; treating
// those as structured failures would reshape frames the SPA has no renderer
// for.
func TestParseStructuredToolFailure_IgnoresUnknownDiscriminators(t *testing.T) {
	for _, in := range []string{
		`{"error":"timeout"}`,
		`{"error":"file_exists_but_not_really"}`,
		`{"reason":"no discriminator at all"}`,
		"some plain tool error",
		"",
		`{"error":123}`,
	} {
		_, _, ok := parseStructuredToolFailure(in)
		assert.False(t, ok, "must not treat %q as a structured failure", in)
	}
}

// TestReplay_WriteRefusal_RendersAsObjectNotRawJSON drives the real replay
// reconstruction. It must FAIL if replay.go stops parsing structured failures:
// the persisted transcript stores the payload as a raw JSON STRING in
// ToolCall.Error (loop.go's RC-5 write stores contentForLLM verbatim, and for
// this tool contentForLLM IS the JSON), so without the parse a reload shows a
// JSON blob in the field the live view renders as a sentence.
func TestReplay_WriteRefusal_RendersAsObjectNotRawJSON(t *testing.T) {
	tc := session.ToolCall{
		ID:         "t-w5",
		Tool:       "write_file",
		Status:     "error",
		DurationMS: 3,
		Parameters: map[string]any{"path": "/w/a.svg"},
		Error:      writeRefusalPayload,
	}
	frames, _ := runReplay(t, []session.TranscriptEntry{assistantEntry("", "mia", tc)})

	result := findFrame(frames, "tool_call_result")
	require.NotNil(t, result, "expected a tool_call_result frame for the refused write")
	assert.Equal(t, "error", result.Status)

	assert.NotContains(t, result.Error, `{"error"`,
		"replay left the raw JSON payload in the frame's error field — a reload would show a blob "+
			"where the live view shows a sentence")
	assert.Contains(t, result.Error, "already exists",
		"the prose reason must be lifted into error, exactly as the live path does")

	obj, isObject := result.Result.(map[string]any)
	require.True(t, isObject,
		"replay must deliver the typed object in result, not a string — got %T", result.Result)
	assert.Equal(t, "file_exists", obj["error"], "the SPA matches on this discriminator")
	assert.Equal(t, "write_file", obj["tool"])
}

// TestReplay_PlainToolFailure_Unchanged proves the parse is narrow: an
// ordinary failure whose Error is prose must replay exactly as before, with
// the text left in error and no invented result object.
func TestReplay_PlainToolFailure_Unchanged(t *testing.T) {
	const plain = "disk full while writing output"
	tc := session.ToolCall{
		ID:     "t-w5-plain",
		Tool:   "write_file",
		Status: "error",
		Error:  plain,
	}
	frames, _ := runReplay(t, []session.TranscriptEntry{assistantEntry("", "mia", tc)})

	result := findFrame(frames, "tool_call_result")
	require.NotNil(t, result)
	assert.Equal(t, plain, result.Error)
	assert.Nil(t, result.Result, "a prose failure must not gain a fabricated result object")
}
