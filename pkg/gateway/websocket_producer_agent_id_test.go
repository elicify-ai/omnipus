//go:build !cgo

// This test file uses //go:build !cgo so it compiles when CGO is disabled.
// See websocket_test.go for the same rationale.

package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// readTokenFrame drains bytes from ch and unmarshals them as a TokenFrame.
func readTokenFrame(t *testing.T, ch chan []byte) generated.TokenFrame {
	t.Helper()
	select {
	case raw := <-ch:
		var frame generated.TokenFrame
		require.NoError(t, json.Unmarshal(raw, &frame), "must unmarshal TokenFrame")
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for token frame")
		return generated.TokenFrame{}
	}
}

// TestWsStreamer_Update_AttributesTokenToProducerAgentID is the "point of
// emission" test for FIX 5a: a background delegate's streamed tokens must
// carry the DELEGATE's own agent ID on the wire, not the parent's (or any
// other "active session agent" guess).
//
// The scenario: wsStreamer is constructed with agentID "jim" (simulating the
// GetStreamer-time guess computed from the session's ActiveAgentID metadata
// — jim being the delegating parent the user is actively chatting with).
// The agent loop then calls SetProducerAgentID("ava-worker") immediately
// after obtaining the streamer for a background/delegated sub-turn's LLM
// streaming call (as stampStreamerProducerAgentID does in pkg/agent/turn.go).
//
// BDD:
//
//	Given a wsStreamer whose GetStreamer-time guess is agent "jim",
//	  And the agent loop has stamped the TRUE producer "ava-worker" via
//	    SetProducerAgentID (because this turn is a background delegate,
//	    per ADR-032 running as its own identity, never the parent's),
//	When Update streams a token,
//	Then the constructed TokenFrame carries agent_id "ava-worker" — NOT
//	  "jim" and NOT empty.
//
// Traces to: pkg/gateway/websocket.go wsStreamer.Update / SetProducerAgentID;
// pkg/agent/turn.go stampStreamerProducerAgentID.
func TestWsStreamer_Update_AttributesTokenToProducerAgentID(t *testing.T) {
	s, ch := buildWsStreamer(t)
	s.agentID = "jim" // GetStreamer-time guess: the parent the user is chatting with.

	// The agent loop stamps the TRUE producer before any token flows, exactly
	// as stampStreamerProducerAgentID does for a background delegate turn.
	s.SetProducerAgentID("ava-worker")

	require.NoError(t, s.Update(context.Background(), "hello from the delegate"))

	frame := readTokenFrame(t, ch)
	assert.Equal(t, "hello from the delegate", frame.Content)
	require.NotNil(t, frame.AgentId, "TokenFrame.AgentId must be populated")
	assert.Equal(t, "ava-worker", *frame.AgentId,
		"the live token must be attributed to the TRUE per-turn producer (the delegate), "+
			"never the parent/session-active-agent guess computed at GetStreamer time")
}

// TestWsStreamer_Update_FallsBackToConstructorAgentIDWhenNotOverridden is the
// regression/no-op-inputs complement: for an ordinary (non-delegated) turn,
// where the agent loop's stampStreamerProducerAgentID legitimately stamps the
// streamer with the SAME agent the GetStreamer-time guess already resolved
// (the common case: the visibly-active chat agent produces its own
// response), Update must still populate AgentId correctly from whatever the
// streamer's agentID currently holds.
//
// BDD:
//
//	Given a wsStreamer constructed with agentID "mia" and no
//	  SetProducerAgentID override,
//	When Update streams a token,
//	Then the constructed TokenFrame carries agent_id "mia".
func TestWsStreamer_Update_FallsBackToConstructorAgentIDWhenNotOverridden(t *testing.T) {
	s, ch := buildWsStreamer(t)
	s.agentID = "mia"

	require.NoError(t, s.Update(context.Background(), "hello from mia"))

	frame := readTokenFrame(t, ch)
	require.NotNil(t, frame.AgentId, "TokenFrame.AgentId must be populated")
	assert.Equal(t, "mia", *frame.AgentId)
}

// TestWsStreamer_Update_OmitsAgentIdWhenUnset verifies the AgentId pointer
// stays nil (omitted from the wire payload per the schema's omitempty) when
// no agentID was ever set — matching TokenFrame.yaml's optional field
// contract instead of emitting an empty string.
func TestWsStreamer_Update_OmitsAgentIdWhenUnset(t *testing.T) {
	s, ch := buildWsStreamer(t)
	// s.agentID left at its zero value ("").

	require.NoError(t, s.Update(context.Background(), "hello"))

	frame := readTokenFrame(t, ch)
	assert.Nil(t, frame.AgentId, "AgentId must be omitted (nil), never an empty-string pointer")
}

// TestWsStreamer_SetProducerAgentID_EmptyIsNoop verifies the documented
// no-op guard: calling SetProducerAgentID("") must not blank out an
// already-resolved agentID (defends against a caller that has no resolved
// agent for this specific call clobbering a previously-correct value).
func TestWsStreamer_SetProducerAgentID_EmptyIsNoop(t *testing.T) {
	s, ch := buildWsStreamer(t)
	s.agentID = "jim"

	s.SetProducerAgentID("") // must be a no-op

	require.NoError(t, s.Update(context.Background(), "hello"))
	frame := readTokenFrame(t, ch)
	require.NotNil(t, frame.AgentId)
	assert.Equal(t, "jim", *frame.AgentId, "an empty SetProducerAgentID call must not clobber the existing agentID")
}

// TestWsStreamer_Finalize_AttributesTranscriptToProducerAgentID proves FIX 5a
// closes the SAME bug for the persisted transcript entry written by Finalize
// (not just the live TokenFrame) — both read the streamer's agentID field,
// so overriding it via SetProducerAgentID must also fix transcript
// attribution when the WS connection is still alive at turn end (the
// complementary "connection still alive" case Fix 5d's test suite requires
// to keep working without regression).
//
// BDD:
//
//	Given a wsStreamer constructed with the GetStreamer-time guess "jim",
//	  And the agent loop has stamped the TRUE producer "ava-worker",
//	When Update streams content and Finalize persists the transcript,
//	Then the persisted assistant transcript entry's AgentID is
//	  "ava-worker" — not "jim".
func TestWsStreamer_Finalize_AttributesTranscriptToProducerAgentID(t *testing.T) {
	_, _, al := newTestWSHandler(t)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "jim")
	require.NoError(t, err, "create session")
	t.Cleanup(func() { _ = store.DeleteSession(meta.ID) })

	wc := &wsConn{
		sendCh:         make(chan []byte, 256),
		doneCh:         make(chan struct{}),
		replayDivertCh: make(chan []byte, replayLiveBufferCap),
	}

	s := &wsStreamer{
		conn:       wc,
		chatID:     "chat-producer-attr",
		sessionID:  meta.ID,
		agentStore: store,
		agentID:    "jim", // GetStreamer-time guess: the parent.
	}

	s.SetProducerAgentID("ava-worker") // the agent loop stamps the true delegate.

	require.NoError(t, s.Update(context.Background(), "Delegate's answer."))
	require.NoError(t, s.Finalize(context.Background(), "Delegate's answer."))

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err, "read transcript")

	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 1)
	assert.Equal(t, "ava-worker", assistantEntries[0].AgentID,
		"the persisted transcript entry must attribute to the TRUE producer, not the "+
			"session's active-agent guess")
}
