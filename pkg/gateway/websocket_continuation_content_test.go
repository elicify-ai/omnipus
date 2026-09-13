package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestWsStreamer_Finalize_PrefersContinuationContent is WP E's regression
// test for ADR-087 D6.1/§2.8. wsStreamer is per PROVIDER CALL, not per turn:
// WSHandler.GetStreamer constructs a new streamer on every call, and only
// the LAST one is finalized. On an auto-continuation (D6), that last
// streamer's own `accumulated` buffer holds only the continuation's suffix
// — never the prefix the user already read in an earlier bubble segment.
//
// SetContinuationContent lets the agent loop hand Finalize the full
// prefix+suffix text (turnState.continuationAccum) so what gets PERSISTED
// (and therefore what a reconnect/replay snapshot carries) matches what the
// live bubble actually showed, not just this one streamer's own fragment.
//
// BDD:
//
//	Given a wsStreamer whose own buffer accumulated only "suffix",
//	  And the agent loop calls SetContinuationContent("prefix suffix"),
//	When Finalize runs,
//	Then the persisted assistant transcript entry's content is
//	  "prefix suffix", not "suffix".
//
// Traces to: ADR-087 D6.1 ("what `finalizeStreamer` passes to `Finalize`,
// and `Finalize` must prefer it over its own buffer"), §9's E→C fixed
// interface (`SetContinuationContent(full string)`).
func TestWsStreamer_Finalize_PrefersContinuationContent(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	s := &wsStreamer{
		chatID:     "chat-continuation",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "main",
		// channel is nil: markStreamed, connection resolution, and the live
		// send loop all short-circuit (ADR-082 D2's bare-fixture degrade) —
		// this test only asserts on what Finalize persists.
	}

	// This streamer's own buffer only ever saw the continuation's suffix —
	// exactly what a real continuation's per-call streamer would hold.
	require.NoError(t, s.Update(context.Background(), "suffix"))

	// The agent loop hands Finalize the FULL accumulated answer.
	s.SetContinuationContent("prefix suffix")

	require.NoError(t, s.Finalize(context.Background(), "suffix"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 1, "Finalize must write exactly one assistant entry")
	assert.Equal(t, "prefix suffix", assistantEntries[0].Content,
		"Finalize must persist the continuation accumulator's full text, not its own buffer")
}

// TestWsStreamer_Finalize_WithoutContinuationContent_UsesBuffer is the
// inverse of the test above: when SetContinuationContent is never called
// (the ordinary, non-continuation case — every turn today), Finalize's
// content resolution must be byte-identical to its pre-ADR-087 behavior —
// the streamer's own accumulated buffer, falling back to finalContent only
// when the buffer is empty.
func TestWsStreamer_Finalize_WithoutContinuationContent_UsesBuffer(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "main")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	s := &wsStreamer{
		chatID:     "chat-no-continuation",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "main",
	}

	require.NoError(t, s.Update(context.Background(), "only what streamed"))

	require.NoError(t, s.Finalize(context.Background(), "only what streamed"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 1, "Finalize must write exactly one assistant entry")
	assert.Equal(t, "only what streamed", assistantEntries[0].Content,
		"without SetContinuationContent, Finalize must persist its own accumulated buffer unchanged")
}
