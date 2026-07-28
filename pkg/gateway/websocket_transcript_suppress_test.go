package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestWsStreamer_Finalize_SuppressesDuplicateTranscript is the regression test
// for the #416 gate fix: when a turn exits via max_tool_iterations exhaustion,
// the last executed round is a tool-call round whose narration the agent loop
// already wrote via appendIntermediateAssistantTranscript. That round's
// wsStreamer is the lastStreamer that gets finalized, and Finalize would
// UNCONDITIONALLY append the same accumulated content again — producing a
// duplicate assistant bubble on replay.
//
// After the fix, the agent loop calls SuppressTranscriptWrite() on that
// streamer; Finalize must then SKIP the transcript-append block while still
// sending the done frame.
//
// BDD:
//
//	Given a wsStreamer that accumulated narration content,
//	  And whose narration was already persisted (SuppressTranscriptWrite called),
//	When Finalize runs,
//	Then NO assistant transcript entry is written by the streamer.
//
// Traces to: pkg/gateway/websocket.go wsStreamer.Finalize (transcriptPersisted guard).
func TestWsStreamer_Finalize_SuppressesDuplicateTranscript(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	wc := &wsConn{
		sendCh:         make(chan []byte, 256),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	s := &wsStreamer{
		conn:       wc,
		chatID:     "chat-suppress",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "main",
		// channel is nil: markStreamed and fan-out short-circuit, which is fine —
		// we only assert on the transcript-append behavior here.
	}

	// Accumulate some narration (as Update would during a tool-call round).
	require.NoError(t, s.Update(context.Background(), "Okay, working on it."))

	// The agent loop already persisted this round's narration via
	// appendIntermediateAssistantTranscript and marked the streamer.
	s.SuppressTranscriptWrite()

	// Finalize must NOT append a (duplicate) assistant entry.
	require.NoError(t, s.Finalize(context.Background(), "Okay, working on it."))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	assert.Empty(t, assistantEntries,
		"Finalize must NOT write an assistant entry when SuppressTranscriptWrite was "+
			"called (the agent loop already persisted the narration) — #416 dup fix; got: %v",
		assistantEntries)
}

// TestWsStreamer_Finalize_WritesTranscriptWhenNotSuppressed is the
// complementary case proving the guard is conditional: a clean turn (final
// round has no tool calls, so appendIntermediateAssistantTranscript was NOT
// called for it) must still have its streamed content persisted by Finalize.
//
// BDD:
//
//	Given a wsStreamer that accumulated content,
//	  And SuppressTranscriptWrite was NOT called,
//	When Finalize runs,
//	Then exactly one assistant transcript entry is written.
func TestWsStreamer_Finalize_WritesTranscriptWhenNotSuppressed(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	wc := &wsConn{
		sendCh:         make(chan []byte, 256),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	s := &wsStreamer{
		conn:       wc,
		chatID:     "chat-write",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "main",
	}

	require.NoError(t, s.Update(context.Background(), "All done!"))

	// No SuppressTranscriptWrite — clean exit path.
	require.NoError(t, s.Finalize(context.Background(), "All done!"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 1,
		"Finalize must write exactly one assistant entry on the clean (non-suppressed) path")
	assert.Equal(t, "All done!", assistantEntries[0].Content)
}
