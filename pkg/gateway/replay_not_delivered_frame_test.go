// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracle: session-core FR-013/BDD-04.3 + FR-033 — a not_delivered
// subagent_message frame persisted in the parent's transcript is replayed to a
// reloading client unchanged in kind, with untrusted_origin false (it is
// server-authored) and the parent session stamped on it. This is what lets the
// SPA show a rejected arrival after a reload.
func TestReplay_NotDeliveredFrame_SurvivesReloadAsServerAuthored(t *testing.T) {
	const parentID, childID = "session_parent_nd", "session_child_nd"
	text := "progress report not delivered: the report rate limit was reached"
	childRef := childID
	frame := generated.SubagentMessageFrame{
		Type:            string(generated.WsFrameTypeSubagentMessage),
		MessageId:       "call-nd:not_delivered:msg:1",
		SessionId:       parentID,
		ChildSessionId:  &childRef,
		SpanId:          "span_call-nd",
		Kind:            "not_delivered",
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		SenderIdentity:  "worker",
		UntrustedOrigin: false,
		Text:            &text,
	}
	entry := session.TranscriptEntry{
		ID: frame.MessageId, Type: session.EntryTypeSystem, SystemSubtype: session.SystemSubtypeSubagentMessage,
		Timestamp: time.Now().UTC(), SubagentMessage: &frame,
	}
	sink := &sliceSink{}
	entries := []session.TranscriptEntry{entry}
	_, err := streamReplay(t.Context(), parentID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil, nil)
	require.NoError(t, err)

	got := decodeFrameOfType[generated.SubagentMessageFrame](t, sink.frames, "subagent_message")
	require.Equal(t, "not_delivered", string(got.Kind))
	require.False(t, got.UntrustedOrigin, "a server-authored line is not marked untrusted")
	require.Equal(t, parentID, got.SessionId)
	require.NotNil(t, got.ChildSessionId)
	require.Equal(t, childID, *got.ChildSessionId)
	require.NotNil(t, got.Text)
	require.Equal(t, text, *got.Text)
}
