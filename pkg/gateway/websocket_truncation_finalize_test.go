// websocket_truncation_finalize_test.go — ADR-087 WP C regression coverage:
// the streamed (webchat) path's D4a/D4b truncation-annotation write must
// happen INSIDE wsStreamer.Finalize (stamped via the streamerTruncationSetter
// probe in pkg/agent/turn.go's finalizeStreamer, immediately before
// Finalize), not via a post-hoc MarkLastEntryTruncated call after Finalize
// returns.
//
// Before this fix, a D4a zero-content truncation was never persisted at all
// on the streamed path (Finalize's own `content != ""` gate skipped writing
// any entry, so the post-hoc backward-walk had nothing to find), or — worse
// — the backward-walk matched and mis-stamped an EARLIER same-turn assistant
// entry (this turn's own TurnID, written by an earlier tool-calling round via
// appendIntermediateAssistantTranscript) as truncated instead of the actual
// cut-off round.
//
// Traces to: docs/internal/architecture/ADR-087-truncation-is-an-outcome-not-a-silence.md
// D2, D4a, D4b, D6.1.

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestWsStreamer_Finalize_TruncatedEmpty_WritesAnnotatedZeroContentEntry is
// the core D4a regression: a streamer whose Update() call(s) accumulated no
// content at all (or was never called) but whose agent loop stamped a
// truncation reason via SetTruncation before Finalize must still produce
// exactly one NEW assistant transcript entry — Role assistant, Content "",
// TurnID stamped, Truncated true, TruncationReason set — and must leave an
// EARLIER same-turn assistant entry byte-identical. That earlier-entry
// assertion is the exact regression the review named: the old post-hoc
// MarkLastEntryTruncated backward-walk, finding no entry for this round,
// used to walk back and mis-stamp that earlier completed narration instead.
func TestWsStreamer_Finalize_TruncatedEmpty_WritesAnnotatedZeroContentEntry(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	const turnID = "turn-d4a-1"

	// Seed an EARLIER same-turn assistant entry, exactly as an earlier
	// tool-calling round's appendIntermediateAssistantTranscript would have
	// written it — narration that completed normally and must never be
	// touched by this turn's truncation annotation.
	earlier := session.TranscriptEntry{
		ID:      "entry-earlier-narration",
		Role:    "assistant",
		AgentID: "ray",
		TurnID:  turnID,
		Content: "Let me look that up.",
	}
	require.NoError(t, store.AppendTranscriptStrict(meta.ID, earlier))

	before, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript before Finalize")
	require.Len(t, before, 1, "exactly the seeded earlier entry must exist before Finalize")
	beforeEarlier := before[0]

	// Build the streamer for the round that got cut off at the output-token
	// limit before producing any text at all — Update() is never called,
	// mirroring a genuine D4a turn.
	s := &wsStreamer{
		chatID:     "chat-d4a-empty",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "ray",
	}
	s.SetTurnID(turnID)
	s.SetProducerAgentID("ray")

	// Exactly what finalizeStreamer's streamerTruncationSetter probe does,
	// immediately before calling Finalize.
	s.SetTruncation("max_output_tokens")

	require.NoError(t, s.Finalize(context.Background(), ""))

	after, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript after Finalize")

	var assistantEntries []session.TranscriptEntry
	for _, e := range after {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 2,
		"the seeded earlier entry plus exactly one NEW annotated zero-content entry")

	// The earlier entry must be byte-identical — untouched by this turn's
	// truncation annotation. This is the regression assertion: before the
	// fix, the post-hoc MarkLastEntryTruncated backward-walk found no entry
	// for the empty round and instead mis-stamped THIS one.
	assert.Equal(t, beforeEarlier, assistantEntries[0],
		"an earlier same-turn assistant entry must be byte-identical after Finalize — "+
			"it must never be the target of this round's truncation annotation")

	newEntry := assistantEntries[1]
	assert.Equal(t, "ray", newEntry.AgentID)
	assert.Equal(t, turnID, newEntry.TurnID, "the new entry must carry the stamped TurnID")
	assert.Empty(t, newEntry.Content, "D4a: the entry must be zero-content")
	assert.True(t, newEntry.Truncated, "the new entry must be flagged Truncated")
	assert.Equal(t, "max_output_tokens", newEntry.TruncationReason)
}

// TestWsStreamer_Finalize_TruncatedNonEmpty_StampsInSameWrite is the D4b
// regression: a streamer that DID accumulate content (an auto-continuation
// that exhausted its retry budget, or was ineligible to continue) must
// persist that content WITH Truncated/TruncationReason stamped in the SAME
// write — a single entry, not a content-only write followed by a separate
// annotation pass.
func TestWsStreamer_Finalize_TruncatedNonEmpty_StampsInSameWrite(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	s := &wsStreamer{
		chatID:     "chat-d4b-nonempty",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "ray",
	}
	s.SetTurnID("turn-d4b-1")
	s.SetProducerAgentID("ray")

	require.NoError(t, s.Update(context.Background(), "partial"))
	s.SetTruncation("max_output_tokens")

	require.NoError(t, s.Finalize(context.Background(), "partial"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 1)
	entry := assistantEntries[0]
	assert.Equal(t, "partial", entry.Content)
	assert.True(t, entry.Truncated)
	assert.Equal(t, "max_output_tokens", entry.TruncationReason)
}

// TestWsStreamer_Finalize_NoTruncation_Unchanged is the negative control: an
// ordinary, non-truncated turn's Finalize behavior must be byte-identical to
// before this fix — no Truncated/TruncationReason fields set, and the
// zero-content write path must stay dormant (an empty-content, non-truncated
// round still writes nothing).
func TestWsStreamer_Finalize_NoTruncation_Unchanged(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	s := &wsStreamer{
		chatID:     "chat-no-trunc",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "ray",
	}
	s.SetTurnID("turn-clean-1")
	s.SetProducerAgentID("ray")

	require.NoError(t, s.Update(context.Background(), "All done!"))
	// SetTruncation is never called — ordinary clean turn.

	require.NoError(t, s.Finalize(context.Background(), "All done!"))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 1)
	entry := assistantEntries[0]
	assert.Equal(t, "All done!", entry.Content)
	assert.False(t, entry.Truncated, "an ordinary turn must not be flagged Truncated")
	assert.Empty(t, entry.TruncationReason)
}

// TestWsStreamer_SetTruncation_EmptyIsNoop mirrors
// TestWsStreamer_SetProducerAgentID_EmptyIsNoop / TestWsStreamer_SetTurnID_EmptyIsNoop
// for SetTruncation: an empty reason must not clobber an already-set value.
func TestWsStreamer_SetTruncation_EmptyIsNoop(t *testing.T) {
	s, _ := buildWsStreamer(t)
	s.truncationReason = "max_output_tokens"

	s.SetTruncation("") // must be a no-op

	s.statsMu.Lock()
	got := s.truncationReason
	s.statsMu.Unlock()
	assert.Equal(t, "max_output_tokens", got,
		"an empty SetTruncation call must not clobber the existing truncationReason")
}

// TestWsStreamer_Finalize_TruncationSetInLiveDoneFrame is the ADR-087 D2
// finding #10 regression: the LIVE done frame (not just the persisted
// transcript entry / replay's ReplayMessageFrame) must carry
// DoneStats.Truncated/TruncationReason, so a turn cut off while the user is
// still watching renders the "(cut off at the output limit)" notice
// immediately instead of only after a reload/reattach round-trip through
// replay.
func TestWsStreamer_Finalize_TruncationSetInLiveDoneFrame(t *testing.T) {
	s, ch := buildWsStreamer(t)

	s.SetTruncation("max_output_tokens")
	require.NoError(t, s.Finalize(context.Background(), ""))

	frame := readDoneFrame(t, ch)
	require.NotNil(t, frame.Stats, "done frame must carry Stats")
	require.NotNil(t, frame.Stats.Truncated, "Truncated must be set on the live done frame")
	assert.True(t, *frame.Stats.Truncated)
	require.NotNil(t, frame.Stats.TruncationReason, "TruncationReason must be set on the live done frame")
	assert.Equal(t, "max_output_tokens", *frame.Stats.TruncationReason)
}

// TestWsStreamer_Finalize_NoTruncation_DoneFrameOmitsFields is the negative
// control: an ordinary, non-truncated turn's done frame must omit both
// fields entirely (nil pointers), matching DoneStats.yaml's omitempty
// contract — no "(cut off...)" notice for a clean turn.
func TestWsStreamer_Finalize_NoTruncation_DoneFrameOmitsFields(t *testing.T) {
	s, ch := buildWsStreamer(t)

	// SetTruncation never called — ordinary clean turn.
	require.NoError(t, s.Finalize(context.Background(), "All done!"))

	frame := readDoneFrame(t, ch)
	require.NotNil(t, frame.Stats, "done frame must carry Stats")
	assert.Nil(t, frame.Stats.Truncated, "Truncated must be omitted (nil) on a normal turn")
	assert.Nil(t, frame.Stats.TruncationReason, "TruncationReason must be omitted (nil) on a normal turn")
}

// TestReplay_TruncatedEmptyAssistantEntry_ProducedByFinalize_IsEmitted
// extends TestReplay_TruncatedEmptyAssistantEntry_IsEmitted (replay_truncation_test.go,
// which hand-seeds the TranscriptEntry directly) to prove the SAME replay
// behavior holds for an entry actually PRODUCED by the real
// wsStreamer.Finalize write path — not just a hand-built fixture — closing
// the loop from "Finalize writes it" to "replay emits it" end to end.
func TestReplay_TruncatedEmptyAssistantEntry_ProducedByFinalize_IsEmitted(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	s := &wsStreamer{
		chatID:     "chat-replay-d4a",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "ray",
	}
	s.SetTurnID("turn-replay-d4a")
	s.SetProducerAgentID("ray")
	s.SetTruncation("max_output_tokens")
	require.NoError(t, s.Finalize(context.Background(), ""))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")
	require.Len(t, entries, 1, "Finalize must have written exactly one entry")

	frames, n := runReplay(t, entries)
	assert.Equal(t, 1, n, "the annotated zero-content entry must produce exactly one content frame")

	msg := findFrame(frames, "replay_message")
	require.NotNil(t, msg, "replay must emit the Finalize-produced truncated entry")
	assert.Equal(t, "assistant", msg.Role)
	assert.Empty(t, msg.Content)
	require.NotNil(t, msg.Truncated)
	assert.True(t, *msg.Truncated)
	assert.Equal(t, "max_output_tokens", msg.TruncationReason)
}
