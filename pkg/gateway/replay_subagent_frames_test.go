// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// replay_subagent_frames_test.go — ADR-091 UAT defect 1: a subagent_message
// / subagent_state entry always lives in its PARENT's own transcript
// (steer_frames.go's persistSubagentEntry never writes anywhere else), so
// replaying it must always carry the PARENT's session_id — the SPA files a
// frame purely by whatever session_id it carries (src/store/chat/slices/
// frames.ts), so a frame addressed to the wrong session lands in a bucket
// with no row for it. Self-healing: the fix stamps sr.sessionID (the
// transcript actually being read) at replay time rather than trusting
// whatever SessionId the stored frame happens to carry — so an entry
// persisted BEFORE deliverSubagentMessage/deliverSubagentState's own fix
// (steer_frames.go), whose stored SessionId is the CHILD's, still replays
// correctly with no separate data migration.
package gateway

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestReplay_SubagentMessage_LegacyStoredChildSessionId_StampedToParentOnReplay(t *testing.T) {
	const parentID, childID = "session_parent_1", "session_child_1"

	// A frame persisted BEFORE the ADR-091 UAT defect 1 fix: SessionId is
	// the CHILD's own id, the exact bug this replayed entry must survive.
	legacyFrame := generated.SubagentMessageFrame{
		Type:            string(generated.WsFrameTypeSubagentMessage),
		MessageId:       "call-1:progress:msg:1",
		SessionId:       childID, // wrong — the pre-fix bug, baked into stored data
		SpanId:          "span_call-1",
		Kind:            "progress",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SenderIdentity:  "worker",
		UntrustedOrigin: true,
	}
	entry := session.TranscriptEntry{
		ID:              legacyFrame.MessageId,
		Type:            session.EntryTypeSystem,
		SystemSubtype:   session.SystemSubtypeSubagentMessage,
		Timestamp:       time.Now().UTC(),
		SubagentMessage: &legacyFrame,
	}

	sink := &sliceSink{}
	entries := []session.TranscriptEntry{entry}
	_, err := streamReplay(t.Context(), parentID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil)
	require.NoError(t, err)
	// streamReplay always appends a trailing "done" frame (FR-I-004) after
	// the content frames — find the subagent_message frame among whatever
	// else was emitted rather than assuming it is the only one.
	got := decodeFrameOfType[generated.SubagentMessageFrame](t, sink.frames, "subagent_message")
	require.Equal(t, parentID, got.SessionId,
		"replay must stamp the PARENT's session_id (the transcript actually being read) "+
			"even though the stored entry carries the pre-fix CHILD id — self-healing, no migration needed")
}

// decodeFrameOfType finds the single frame of wantType among raw and decodes
// it as T, failing the test if there is not exactly one.
func decodeFrameOfType[T any](t *testing.T, raw [][]byte, wantType string) T {
	t.Helper()
	var head struct {
		Type string `json:"type"`
	}
	var found *T
	for _, f := range raw {
		require.NoError(t, json.Unmarshal(f, &head))
		if head.Type != wantType {
			continue
		}
		require.Nil(t, found, "expected exactly one %q frame, got more than one", wantType)
		var v T
		require.NoError(t, json.Unmarshal(f, &v))
		found = &v
	}
	require.NotNil(t, found, "expected exactly one %q frame among %d emitted frames, found none", wantType, len(raw))
	return *found
}

func TestReplay_SubagentState_LegacyStoredChildSessionId_StampedToParentOnReplay(t *testing.T) {
	const parentID, childID = "session_parent_2", "session_child_2"

	legacyFrame := generated.SubagentStateFrame{
		Type:      string(generated.WsFrameTypeSubagentState),
		SessionId: childID, // wrong — the pre-fix bug, baked into stored data
		SpanId:    "span_call-2",
		State:     "running",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	entry := session.TranscriptEntry{
		ID:            "call-2:1:state:running",
		Type:          session.EntryTypeSystem,
		SystemSubtype: session.SystemSubtypeSubagentState,
		Timestamp:     time.Now().UTC(),
		SubagentState: &legacyFrame,
	}

	sink := &sliceSink{}
	entries := []session.TranscriptEntry{entry}
	_, err := streamReplay(t.Context(), parentID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil)
	require.NoError(t, err)
	got := decodeFrameOfType[generated.SubagentStateFrame](t, sink.frames, "subagent_state")
	require.Equal(t, parentID, got.SessionId,
		"replay must stamp the PARENT's session_id even though the stored entry carries "+
			"the pre-fix CHILD id — self-healing, no migration needed")
}
