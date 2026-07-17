// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_contract_test.go — round-2 test-analysis gap: no contract_test.go
// coverage existed for the live-browser-video-streaming (ADR-044) control
// frames. Verifies the generated Go wire types for the three JSON control
// frames marshal to schema-valid JSON matching
// contracts/components/schemas/*.yaml, following the exact pattern already
// established in contract_test.go (mustPassComponent / mustFailComponent /
// validateAgainstComponentSchemaRawJSON — all reused directly, same package,
// not redefined). Hermetic: no network, no gateway, no real Chrome — pure
// JSON marshal + jsonschema validation against the committed contract files.
//
// Frames covered:
//   - BrowserStreamInitFrame (server -> client, contracts/components/schemas/BrowserStreamInitFrame.yaml)
//   - BrowserIngestInitFrame (encoder-page -> gateway, .../BrowserIngestInitFrame.yaml)
//   - BrowserAttachFrame (client -> server, .../BrowserAttachFrame.yaml)

package generated

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── BrowserStreamInitFrame ────────────────────────────────────────────────
// Traces to: contracts/components/schemas/BrowserStreamInitFrame.yaml

func browserStreamInitFramePopulated() BrowserStreamInitFrame {
	sessionID := "sess-abc123"
	return BrowserStreamInitFrame{
		Type:             "browser_stream_init",
		SessionId:        &sessionID,
		Codec:            "avc1.4D4028",
		Width:            1280,
		Height:           720,
		KeyframeInterval: 60,
		HasAudio:         true,
	}
}

func browserStreamInitFrameEdge() BrowserStreamInitFrame {
	// Edge: no session_id (optional, omitted entirely via the pointer being
	// nil — schema allows this), vp8 fallback codec, audio unavailable.
	return BrowserStreamInitFrame{
		Type:             "browser_stream_init",
		Codec:            "vp8",
		Width:            320,
		Height:           240,
		KeyframeInterval: 1,
		HasAudio:         false,
	}
}

func TestContract_BrowserStreamInitFrame_Populated(t *testing.T) {
	mustPassComponent(t, "BrowserStreamInitFrame", browserStreamInitFramePopulated())
}

func TestContract_BrowserStreamInitFrame_ZeroValue(t *testing.T) {
	// type="" (violates const), codec="" (violates minLength:1), width=0 and
	// height=0 and keyframe_interval=0 (all violate minimum:1).
	mustFailComponent(t, "BrowserStreamInitFrame", BrowserStreamInitFrame{},
		"zero value violates the type const and the minLength/minimum constraints on codec/width/height/keyframe_interval")
}

func TestContract_BrowserStreamInitFrame_Edge(t *testing.T) {
	mustPassComponent(t, "BrowserStreamInitFrame", browserStreamInitFrameEdge())
}

func TestContract_BrowserStreamInitFrame_MissingSessionIDIsValid(t *testing.T) {
	// session_id is NOT in the schema's required list — a nil pointer (field
	// omitted from the JSON via omitempty) must still validate. This is the
	// "optional field absent, not merely empty" case, distinct from a
	// present-but-invalid value.
	f := browserStreamInitFrameEdge()
	raw, err := json.Marshal(f)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	_, present := m["session_id"]
	assert.False(t, present, "session_id must be OMITTED (not null) when nil, per omitempty")
	mustPassComponent(t, "BrowserStreamInitFrame", f)
}

func TestContract_BrowserStreamInitFrame_Differentiation(t *testing.T) {
	f1 := browserStreamInitFramePopulated()
	f2 := browserStreamInitFrameEdge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different fixtures must produce different JSON (differentiation test)")

	// Content check: the negotiated codec actually round-trips (not just
	// "some string" — the EXACT value the orchestrator negotiated).
	var decoded BrowserStreamInitFrame
	require.NoError(t, json.Unmarshal(raw1, &decoded))
	assert.Equal(t, "avc1.4D4028", decoded.Codec)
	assert.Equal(t, 1280, decoded.Width)
	assert.Equal(t, 720, decoded.Height)
	assert.True(t, decoded.HasAudio)

	mustPassComponent(t, "BrowserStreamInitFrame", f1)
	mustPassComponent(t, "BrowserStreamInitFrame", f2)
}

// ── BrowserIngestInitFrame ────────────────────────────────────────────────
// Traces to: contracts/components/schemas/BrowserIngestInitFrame.yaml

func browserIngestInitFramePopulated() BrowserIngestInitFrame {
	audioCodec := "opus"
	return BrowserIngestInitFrame{
		Type:       "browser_ingest_init",
		StreamId:   "stream-unguessable-abc123",
		Token:      "tok-capability-xyz789",
		VideoCodec: "avc1.4D4028",
		HasAudio:   true,
		AudioCodec: &audioCodec,
	}
}

func browserIngestInitFrameEdge() BrowserIngestInitFrame {
	// Edge: video-only (has_audio=false) — audio_codec MUST be absent per the
	// schema doc ("Present only when has_audio is true"); omitted via nil.
	return BrowserIngestInitFrame{
		Type:       "browser_ingest_init",
		StreamId:   "s",
		Token:      "t",
		VideoCodec: "vp8",
		HasAudio:   false,
	}
}

func TestContract_BrowserIngestInitFrame_Populated(t *testing.T) {
	mustPassComponent(t, "BrowserIngestInitFrame", browserIngestInitFramePopulated())
}

func TestContract_BrowserIngestInitFrame_ZeroValue(t *testing.T) {
	// type="" (violates const), stream_id="" and token="" and video_codec=""
	// (all violate minLength:1).
	mustFailComponent(t, "BrowserIngestInitFrame", BrowserIngestInitFrame{},
		"zero value violates the type const and the minLength constraints on stream_id/token/video_codec")
}

func TestContract_BrowserIngestInitFrame_Edge(t *testing.T) {
	mustPassComponent(t, "BrowserIngestInitFrame", browserIngestInitFrameEdge())
}

func TestContract_BrowserIngestInitFrame_VideoOnlyOmitsAudioCodec(t *testing.T) {
	f := browserIngestInitFrameEdge()
	raw, err := json.Marshal(f)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	_, present := m["audio_codec"]
	assert.False(t, present, "audio_codec must be omitted (not null/empty string) for a video-only stream")
	mustPassComponent(t, "BrowserIngestInitFrame", f)
}

func TestContract_BrowserIngestInitFrame_Differentiation(t *testing.T) {
	f1 := browserIngestInitFramePopulated()
	f2 := browserIngestInitFrameEdge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different fixtures must produce different JSON (differentiation test)")

	var decoded BrowserIngestInitFrame
	require.NoError(t, json.Unmarshal(raw1, &decoded))
	assert.Equal(t, "stream-unguessable-abc123", decoded.StreamId)
	assert.Equal(t, "tok-capability-xyz789", decoded.Token)
	require.NotNil(t, decoded.AudioCodec)
	assert.Equal(t, "opus", *decoded.AudioCodec)

	mustPassComponent(t, "BrowserIngestInitFrame", f1)
	mustPassComponent(t, "BrowserIngestInitFrame", f2)
}

// ── BrowserAttachFrame ────────────────────────────────────────────────────
// Traces to: contracts/components/schemas/BrowserAttachFrame.yaml

func browserAttachFramePopulated() BrowserAttachFrame {
	return BrowserAttachFrame{
		Type:      "browser_attach",
		SessionId: "sess-abc123",
		AgentId:   "agent-mia",
		VideoCaps: []string{"avc1.4D4028", "vp8"},
		AudioCaps: []string{"opus"},
	}
}

func browserAttachFrameEdge() BrowserAttachFrame {
	// Edge: pre-video-relay wire-compat client — video_caps/audio_caps both
	// absent (nil, omitempty). Schema treats this as "no video-capable codec"
	// (unavailable state), never a validation error.
	return BrowserAttachFrame{
		Type:      "browser_attach",
		SessionId: "s",
		AgentId:   "a",
	}
}

func TestContract_BrowserAttachFrame_Populated(t *testing.T) {
	mustPassComponent(t, "BrowserAttachFrame", browserAttachFramePopulated())
}

func TestContract_BrowserAttachFrame_ZeroValue(t *testing.T) {
	// type="" (violates const), session_id="" and agent_id="" (violate minLength:1).
	mustFailComponent(t, "BrowserAttachFrame", BrowserAttachFrame{},
		"zero value violates the type const and the minLength constraints on session_id/agent_id")
}

func TestContract_BrowserAttachFrame_Edge(t *testing.T) {
	mustPassComponent(t, "BrowserAttachFrame", browserAttachFrameEdge())
}

func TestContract_BrowserAttachFrame_MissingCapsAreValid(t *testing.T) {
	f := browserAttachFrameEdge()
	raw, err := json.Marshal(f)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	_, vcPresent := m["video_caps"]
	_, acPresent := m["audio_caps"]
	assert.False(t, vcPresent, "video_caps must be omitted (not null) when nil, per omitempty")
	assert.False(t, acPresent, "audio_caps must be omitted (not null) when nil, per omitempty")
	mustPassComponent(t, "BrowserAttachFrame", f)
}

func TestContract_BrowserAttachFrame_AdditionalPropertiesRejected(t *testing.T) {
	// The schema declares additionalProperties: false — an extra field must
	// fail validation even though every required field is present and valid.
	raw := []byte(
		`{"type":"browser_attach","session_id":"s","agent_id":"a","unexpected_field":"nope"}`,
	)
	err := validateAgainstComponentSchemaRawJSON(t, "BrowserAttachFrame", raw)
	assert.Error(t, err, "an unexpected field must be rejected — schema declares additionalProperties: false")
}

func TestContract_BrowserAttachFrame_Differentiation(t *testing.T) {
	f1 := browserAttachFramePopulated()
	f2 := browserAttachFrameEdge()
	raw1, err := json.Marshal(f1)
	require.NoError(t, err)
	raw2, err := json.Marshal(f2)
	require.NoError(t, err)
	assert.NotEqual(t, string(raw1), string(raw2),
		"two different fixtures must produce different JSON (differentiation test)")

	var decoded BrowserAttachFrame
	require.NoError(t, json.Unmarshal(raw1, &decoded))
	assert.Equal(t, "agent-mia", decoded.AgentId)
	assert.Equal(t, []string{"avc1.4D4028", "vp8"}, decoded.VideoCaps)

	mustPassComponent(t, "BrowserAttachFrame", f1)
	mustPassComponent(t, "BrowserAttachFrame", f2)
}
