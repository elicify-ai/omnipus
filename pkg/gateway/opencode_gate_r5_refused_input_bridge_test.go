package gateway

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-R5-2: exercise the actual hub translator with the real pre-change event
// shape and, once implemented, the explicitly identified internal fields from
// team-lead's follow-up. Missing fields cannot create a false pass: the old
// uncorrelated ErrorFrame must fail the non-terminal wire assertions below.
func TestOpenCodeR5_RefusedInputBridgeNeverSendsTurnEndingErrorToSecondView(t *testing.T) {
	// The integrated bridge now resolves correlation from the actual archive.
	// Seed a real saved human row with no client id, not a nil store or a
	// made-up absent entry. No task classifier is invoked in this bridge unit.
	f := opencodeR4GatewayFixture(t, "opencode")
	h, sid, firstFrames := f.handler, f.sid, f.conn.sendCh
	const entryID = "r5-actual-refused-human-row"
	require.NoError(t, h.agentLoop.ResolveSessionStore(sid).AppendTranscriptStrict(sid, session.TranscriptEntry{
		ID: entryID, Role: "user", Content: "Input without a client id", Timestamp: time.Now().UTC(),
	}))
	clientID, found := h.clientMessageIDLookup(sid, entryID)
	require.True(t, found, "instrument: saved human row is really present")
	require.Empty(t, clientID, "instrument: the original client id is truly unavailable")
	second, secondFrames := makeForwarderTestConn(16)
	parent, parentFrames := makeForwarderTestConn(16)
	bindTestConnToSession(h, "r5-finishing-view-b", sid, second)
	bindTestConnToSession(h, "r5-parent-view", "r5-parent", parent)
	// W14 catalogue oracle is authorized by team-lead's prior ruling. Keep
	// this synthetic event valid so WireLLMError cannot rewrite its sentence.
	const code = "delegation_not_resumable"
	text := strings.ReplaceAll(generated.LLMErrorUserMessages[code], "{cli}", "OpenCode")
	payload := agent.ErrorPayload{SessionID: sid, ChatID: sid, Stage: "human_input",
		Code: code, Message: text}
	// These are internal fields, not handwritten gateway/SPA wire types. Do
	// not substitute an unrelated id for an absent original client id.
	value := reflect.ValueOf(&payload).Elem()
	if marker := value.FieldByName("RefusedInput"); marker.IsValid() {
		require.Equal(t, reflect.Bool, marker.Kind())
		marker.SetBool(true)
	}
	if entry := value.FieldByName("RefusedInputEntryID"); entry.IsValid() {
		require.Equal(t, reflect.String, entry.Kind())
		entry.SetString(entryID)
	}
	h.hubSyncTap(agent.Event{Kind: agent.EventKindError,
		Meta: agent.EventMeta{SessionKey: sid}, Payload: payload})
	require.Len(t, firstFrames, 1, "refusal must reach the sender view")
	require.Len(t, secondFrames, 1, "refusal must reach the other view of the same helper")
	assert.Empty(t, parentFrames, "no parent notice")
	firstRaw, secondRaw := <-firstFrames, <-secondFrames
	assert.Equal(t, firstRaw, secondRaw)
	var envelope wsTypeOnly
	require.NoError(t, json.Unmarshal(secondRaw, &envelope))
	switch envelope.Type {
	case string(generated.WsFrameTypeReplayMessage):
		var notice generated.ReplayMessageFrame
		require.NoError(t, json.Unmarshal(secondRaw, &notice))
		assert.Equal(t, "system", notice.Role)
		assert.Equal(t, sid, notice.SessionId)
		assert.Equal(t, payload.Message, notice.Content)
		require.NotNil(t, notice.Id)
		assert.NotEmpty(t, *notice.Id)
	case string(generated.WsFrameTypeError):
		var frame generated.ErrorFrame
		require.NoError(t, json.Unmarshal(secondRaw, &frame))
		require.Equal(t, &sid, frame.SessionId)
		// No original client id is available here. A turn-ending error with
		// only a session tag is the exact SFH-R5-2 regression, not a notice.
		assert.Nil(t, frame.ClientMessageId, "do not fabricate original client correlation")
		// The generated contract has no wire-level refused-input flag. A
		// session-only ErrorFrame is terminal in the actual reducer; an
		// internal ErrorPayload.RefusedInput flag cannot change that.
		t.Fatalf("SFH-R5-2: actual second-view frame is an uncorrelated turn-ending error, not a supported non-terminal carrier: %s", secondRaw)
	default:
		t.Fatalf("SFH-R5-2: unsupported refused-input carrier %q", envelope.Type)
	}
}

// C2 concerns the displayed sender row, not TranscriptEntry.Status. This
// bridge unit uses the real store/lookup and two actual hub subscribers; the
// finishing-window producer is driven separately in pkg/agent's regression.
func TestOpenCodeR5_RefusedHelperInputReconcilesOriginalRowExactlyOnce(t *testing.T) {
	f := opencodeR4GatewayFixture(t, "opencode")
	store := f.handler.agentLoop.ResolveSessionStore(f.sid)
	const entryID, clientID = "r5-archived-followup", "r5-original-client-id"
	require.NoError(t, store.AppendTranscriptStrict(f.sid, session.TranscriptEntry{
		ID: entryID, ClientMessageID: clientID, Role: "user", Content: "Refuse this follow-up, not the healthy answer.", Timestamp: time.Now().UTC(),
	}))
	gotClient, found := f.handler.clientMessageIDLookup(f.sid, entryID)
	require.True(t, found, "instrument: the real archived human row is readable")
	require.Equal(t, clientID, gotClient)
	// Represent the other half of an admitted web send: intake remembers its
	// received state as well as saving its row. The echo is unused by this
	// bridge unit; no writer/lookup/state method is mocked.
	f.handler.rememberAcceptedMessage(f.sid, clientID, nil)
	require.Equal(t, "received", f.handler.acceptedMessageState(f.sid, clientID), "instrument: the sender row starts received, not already failed")
	before := qa2ReopenHelperTranscript(t, store, f.sid)
	second, secondFrames := makeForwarderTestConn(16)
	bindTestConnToSession(f.handler, "r5-correlated-second-view", f.sid, second)
	const code = "delegation_not_resumable"
	text := strings.ReplaceAll(generated.LLMErrorUserMessages[code], "{cli}", "OpenCode")
	warning := "I couldn't save the refusal above, so it may be missing after a reload. Copy it before reloading."
	for _, p := range []agent.ErrorPayload{
		{SessionID: f.sid, Stage: "human_input", Code: code, Message: text},
		{SessionID: f.sid, Stage: "human_input_not_saved", Message: warning},
	} {
		value := reflect.ValueOf(&p).Elem()
		if marker := value.FieldByName("RefusedInput"); marker.IsValid() {
			require.Equal(t, reflect.Bool, marker.Kind())
			marker.SetBool(true)
		}
		if entry := value.FieldByName("RefusedInputEntryID"); entry.IsValid() {
			require.Equal(t, reflect.String, entry.Kind())
			entry.SetString(entryID)
		}
		f.handler.hubSyncTap(agent.Event{Kind: agent.EventKindError, Meta: agent.EventMeta{SessionKey: f.sid}, Payload: p})
	}
	for _, frames := range []chan []byte{f.conn.sendCh, secondFrames} {
		require.Len(t, frames, 3, "C2: original refusal, one failed tick and separate save warning; no duplicate tick")
		var original generated.ErrorFrame
		require.NoError(t, json.Unmarshal(<-frames, &original))
		assert.Equal(t, "error", original.Type)
		assert.Equal(t, &f.sid, original.SessionId)
		assert.Equal(t, &gotClient, original.ClientMessageId, "SFH-R5-2: use the original client id, never the archive-entry id")
		assert.Equal(t, text, original.Message)
		require.NotNil(t, original.Payload)
		require.NotNil(t, original.Payload.LlmError)
		assert.Equal(t, code, original.Payload.LlmError.Code)
		var tick generated.MessageStatusFrame
		require.NoError(t, json.Unmarshal(<-frames, &tick))
		assert.Equal(t, "message_status", tick.Type)
		assert.Equal(t, f.sid, tick.SessionId)
		assert.Equal(t, clientID, tick.ClientMessageId)
		assert.Equal(t, "failed", tick.State)
		var notice generated.ErrorFrame
		require.NoError(t, json.Unmarshal(<-frames, &notice))
		assert.Equal(t, "error", notice.Type)
		assert.Equal(t, &f.sid, notice.SessionId)
		assert.Equal(t, &gotClient, notice.ClientMessageId)
		assert.Equal(t, warning, notice.Message)
		assert.Nil(t, notice.Payload, "approved unsaved-history sentence is uncoded")
	}
	assert.Equal(t, "failed", f.handler.acceptedMessageState(f.sid, clientID), "C2: a retry sees the settled sender disposition")
	assert.Equal(t, before, qa2ReopenHelperTranscript(t, store, f.sid), "C2 wire status is not a rewrite of the archived person's text")
	f.assertNoAdmission(t)
}
