// replay_truncation_test.go — ADR-087 WP E, D2/Codex C8: replay must emit
// annotated assistant entries that carry Truncated/TruncationReason, INCLUDING
// entries whose Content is empty (D4a persists a zero-content entry stamped
// truncated/max_output_tokens when the answer was cut off before any text
// was produced). Before this fix, replay.go's `entry.Content != ""` gate
// silently dropped that entry from the replay stream entirely — a reconnect
// or page reload would show nothing where the live bubble had shown the
// "(cut off at the output limit)" notice.
//
// Traces to: docs/internal/architecture/ADR-087-truncation-is-an-outcome-not-a-silence.md
// §2.4, §2.8, D2, D6.1, §7 (test obligation 1), §9 (WP E).

package gateway

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// loadReplayMessageFrameSchema compiles the standalone
// contracts/components/schemas/ReplayMessageFrame.yaml component schema —
// the same schema pkg/api/generated/contract_test.go's
// TestContract_ReplayMessageFrame_Populated exercises against the generated
// Go type's fixture, here exercised against the RAW JSON replay.go actually
// emits, proving the two agree on the wire shape for a truncated entry.
// Mirrors sessions_wire_test.go's loadMessageSchema exactly (same package,
// reuses its newYAMLSchemaLoader/jsonifyYAML helpers).
func loadReplayMessageFrameSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	thisFile := gatewayTestCallerFile(t)
	contractsDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "contracts", "components", "schemas")
	loader := newYAMLSchemaLoader(t)
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(loader)

	schemaPath := filepath.Join(contractsDir, "ReplayMessageFrame.yaml")
	schemaURL := "file://" + schemaPath
	schema, err := compiler.Compile(schemaURL)
	require.NoError(t, err, "must be able to compile ReplayMessageFrame.yaml")
	return schema
}

// assertValidatesAgainstReplayMessageFrameSchema decodes raw JSON into
// map[string]any (jsonschema/v6's expected instance shape) and validates it
// against the ReplayMessageFrame.yaml schema (additionalProperties:false,
// required: type/session_id/content/role).
func assertValidatesAgainstReplayMessageFrameSchema(t *testing.T, schema *jsonschema.Schema, raw []byte) {
	t.Helper()
	var inst any
	require.NoError(t, json.Unmarshal(raw, &inst))
	assert.NoError(t, schema.Validate(inst), "frame JSON must validate against ReplayMessageFrame.yaml: %s", raw)
}

// TestReplay_TruncatedEmptyAssistantEntry_IsEmitted is the D4a/Codex-C8
// regression: an assistant entry with Truncated=true, TruncationReason=
// "max_output_tokens", and EMPTY Content must still produce exactly one
// replay_message frame (not be silently dropped by the `entry.Content != ""`
// gate), with `truncated` and `truncation_reason` both populated on the
// wire — and that raw JSON must validate against ReplayMessageFrame.yaml.
func TestReplay_TruncatedEmptyAssistantEntry_IsEmitted(t *testing.T) {
	entry := assistantEntry("", "ray")
	entry.Truncated = true
	entry.TruncationReason = "max_output_tokens"

	sink := &sliceSink{}
	rs := computeReplayStats([]session.TranscriptEntry{entry})
	n, err := streamReplay(t.Context(), "session_truncated_empty", []session.TranscriptEntry{entry}, rs, sink.emit, nil, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "exactly one content frame (the annotated empty entry) must be emitted")

	frames := sink.all()
	require.Equal(t, []string{"replay_message", "done"}, frameTypes(frames))

	msg := findFrame(frames, "replay_message")
	require.NotNil(t, msg)
	assert.Equal(t, "assistant", msg.Role)
	assert.Empty(t, msg.Content, "the persisted entry had no content — the frame must carry none either")
	require.NotNil(t, msg.Truncated, "truncated must be present on the wire")
	assert.True(t, *msg.Truncated)
	assert.Equal(t, "max_output_tokens", msg.TruncationReason)

	// Prove this exact wire shape — content absent-but-required-key, role
	// assistant, truncated true — validates against the real schema, not
	// just the hand-decoded test struct.
	require.Len(t, sink.frames, 2, "replay_message + done")
	schema := loadReplayMessageFrameSchema(t)
	assertValidatesAgainstReplayMessageFrameSchema(t, schema, sink.frames[0])
}

// TestReplay_TruncatedEntryCarriesReason verifies that a truncated assistant
// entry which DOES have content (the D4b "annotate the accumulated answer"
// case — auto-continue exhausted or ineligible) also carries `truncated`
// and `truncation_reason` on its replay_message frame; the field-population
// logic is not restricted to the empty-content special case.
func TestReplay_TruncatedEntryCarriesReason(t *testing.T) {
	entry := assistantEntry("here is the partial answer", "ray")
	entry.Truncated = true
	entry.TruncationReason = "max_output_tokens"

	frames, n := runReplay(t, []session.TranscriptEntry{entry})
	assert.Equal(t, 1, n)

	msg := findFrame(frames, "replay_message")
	require.NotNil(t, msg)
	assert.Equal(t, "here is the partial answer", msg.Content)
	require.NotNil(t, msg.Truncated)
	assert.True(t, *msg.Truncated)
	assert.Equal(t, "max_output_tokens", msg.TruncationReason)
}

// TestReplay_LegacyTruncatedNoReason_EmitsCancelled pins ADR-087 §2.4's
// legacy rule: every truncated entry written before TruncationReason existed
// has Truncated=true and TruncationReason="" on disk. Absent reason on a
// truncated entry means "cancelled" — replay must fill that default in on
// the wire rather than emitting an empty/omitted truncation_reason.
func TestReplay_LegacyTruncatedNoReason_EmitsCancelled(t *testing.T) {
	entry := assistantEntry("", "ray")
	entry.Truncated = true
	// entry.TruncationReason left unset ("") — the legacy shape.

	frames, n := runReplay(t, []session.TranscriptEntry{entry})
	assert.Equal(t, 1, n)

	msg := findFrame(frames, "replay_message")
	require.NotNil(t, msg)
	require.NotNil(t, msg.Truncated)
	assert.True(t, *msg.Truncated)
	assert.Equal(t, "cancelled", msg.TruncationReason,
		"an absent reason on a truncated entry must default to cancelled, per ADR-087 §2.4")
}

// TestReplay_NonTruncatedEmptyAssistantEntry_StillSkipped is the negative
// control: an ordinary empty-content assistant entry (Truncated=false, the
// overwhelmingly common case — e.g. a round that produced only tool calls)
// must NOT be emitted as a replay_message. Proves the D4a carve-out is
// scoped exactly to Truncated entries and did not loosen the general gate.
func TestReplay_NonTruncatedEmptyAssistantEntry_StillSkipped(t *testing.T) {
	entry := assistantEntry("", "ray") // Truncated defaults to false (zero value)

	frames, n := runReplay(t, []session.TranscriptEntry{entry})
	assert.Equal(t, 0, n, "a non-truncated empty entry must emit no content frames")
	require.Equal(t, []string{"done"}, frameTypes(frames))
	assert.Nil(t, findFrame(frames, "replay_message"))
}
