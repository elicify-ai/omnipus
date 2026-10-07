// replay_terminal_outcome_test.go — a turn's distinct terminal outcome sentence
// (iteration-cap notice after narration) must stay a separate message after a
// page reload. Live, done(<narration id>) closes the narration bubble before the
// terminal entry arrives; the replay stream has no such boundary, so the entry
// itself carries a durable terminal_outcome marker that replay emits on the
// replay_message frame. Oracle: tests/e2e/terminal-outcome.spec.ts.

package gateway

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestWsStreamer_Finalize_DistinctTerminalNotice_StampsTerminalOutcome: the
// narration was already persisted (SuppressTranscriptWrite) and the turn ends
// with a DIFFERENT final sentence — that sentence's entry, and only that one,
// is marked TerminalOutcome.
func TestWsStreamer_Finalize_DistinctTerminalNotice_StampsTerminalOutcome(t *testing.T) {
	_, _, al := newTestWSHandler(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	const narration = "Let me load the tool first."
	const terminal = "I've reached this agent's limit of tool steps for one turn without a final response."

	// The agent loop already persisted the narration round's entry.
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID: "entry-narration", Role: "assistant", AgentID: "main", TurnID: "turn-t1", Content: narration,
	}))

	s := &wsStreamer{chatID: "chat-terminal", sessionID: meta.ID, agentStore: store, agentID: "main"}
	s.SetTurnID("turn-t1")
	s.SetProducerAgentID("main")
	require.NoError(t, s.Update(context.Background(), narration))
	s.SuppressTranscriptWrite()
	require.NoError(t, s.Finalize(context.Background(), terminal))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var assistants []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistants = append(assistants, e)
		}
	}
	require.Len(t, assistants, 2, "the seeded narration plus exactly one terminal entry")
	assert.Equal(t, narration, assistants[0].Content)
	assert.False(t, assistants[0].TerminalOutcome, "narration must never be marked terminal")
	assert.Equal(t, terminal, assistants[1].Content)
	assert.NotEqual(t, assistants[0].ID, assistants[1].ID, "terminal entry has its own durable id")
	assert.True(t, assistants[1].TerminalOutcome, "the distinct terminal sentence must carry the marker")
}

// TestWsStreamer_Finalize_OrdinaryAnswer_NotTerminalOutcome is the control: a
// normal streamed answer is not a terminal outcome.
func TestWsStreamer_Finalize_OrdinaryAnswer_NotTerminalOutcome(t *testing.T) {
	_, _, al := newTestWSHandler(t)
	store := al.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	s := &wsStreamer{chatID: "chat-ordinary", sessionID: meta.ID, agentStore: store, agentID: "main"}
	require.NoError(t, s.Update(context.Background(), "All done!"))
	require.NoError(t, s.Finalize(context.Background(), "All done!"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var assistants []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistants = append(assistants, e)
		}
	}
	require.Len(t, assistants, 1)
	assert.False(t, assistants[0].TerminalOutcome)
}

// TestReplay_TerminalOutcomeEntry_EmitsMarker: replay puts terminal_outcome:true
// on the frame of a marked entry and omits the key on an unmarked one; both
// frames validate against the ReplayMessageFrame schema.
func TestReplay_TerminalOutcomeEntry_EmitsMarker(t *testing.T) {
	narration := assistantEntry("Let me load the tool first.", "main")
	narration.TurnID = "turn-t1"
	terminal := assistantEntry("I've reached this agent's limit of tool steps.", "main")
	terminal.TurnID = "turn-t1"
	terminal.TerminalOutcome = true

	entries := []session.TranscriptEntry{narration, terminal}
	sink := &sliceSink{}
	rs := computeReplayStats(entries)
	n, err := streamReplay(t.Context(), "session_terminal", entries, rs, sink.emit, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 2, n)

	schema := loadReplayMessageFrameSchema(t)
	var msgs []map[string]any
	for _, raw := range sink.frames {
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		if m["type"] == "replay_message" {
			assertValidatesAgainstReplayMessageFrameSchema(t, schema, raw)
			msgs = append(msgs, m)
		}
	}
	require.Len(t, msgs, 2)
	assert.NotContains(t, msgs[0], "terminal_outcome", "an ordinary entry carries no marker key")
	assert.Equal(t, true, msgs[1]["terminal_outcome"], "the terminal entry's frame carries terminal_outcome:true")
}
