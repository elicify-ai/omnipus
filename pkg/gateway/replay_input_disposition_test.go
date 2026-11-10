package gateway

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// FR-024: the replayed user message carries the read-only input_disposition
// label; a delivered message carries none.
func TestReplayEntryMessage_CarriesInputDisposition(t *testing.T) {
	sr := &streamReplayState{sessionID: "s1"}
	sr.buildEntryMessage(session.TranscriptEntry{ID: "m1", Role: "user", Content: "x", ClientMessageID: "c1",
		InputDisposition: &session.InputDisposition{MessageID: "m1", ClientMessageID: "c1", State: "discarded", Reason: "stopped_before_delivery"}})
	d := sr.msgFrame.InputDisposition
	require.NotNil(t, d)
	require.Equal(t, "m1", d.MessageId)
	require.Equal(t, "discarded", d.State)
	require.Equal(t, "stopped_before_delivery", d.Reason)
	require.NotNil(t, d.ClientMessageId)
	require.Equal(t, "c1", *d.ClientMessageId)

	plain := &streamReplayState{sessionID: "s1"}
	plain.buildEntryMessage(session.TranscriptEntry{ID: "m2", Role: "user", Content: "y"})
	require.Nil(t, plain.msgFrame.InputDisposition)
}
