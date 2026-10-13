package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Oracle: FR-024 - a discarded queued message is reported to every tab as
// message_status discarded/stopped_before_delivery, and the turn-end flush can
// never afterwards call it working.
func TestAnnounceDiscardedInput_FramesTheClientAndBlocksALaterWorkingTick(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	handler.handleChatMessageWithClientID(context.Background(), "chat-u7-discard", "", "queued", "", nil,
		"", "", false, "client-u7-1", nil, wc)
	received := readMessageStatusFrames(t, wc, 1)
	sessionID := received[0].SessionId
	require.Equal(t, "received", received[0].State)
	bindTestConnToSession(handler, "chat-u7-discard", sessionID, wc)

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var entryID string
	for _, e := range entries {
		if e.ClientMessageID == "client-u7-1" {
			entryID = e.ID
		}
	}
	require.NotEmpty(t, entryID, "setup: the persisted user entry")

	handler.announceDiscardedInput(agent.DiscardedInput{SessionID: sessionID, MessageID: entryID})

	frames := readMessageStatusFrames(t, wc, 1)
	assert.Equal(t, "discarded", frames[0].State)
	require.NotNil(t, frames[0].Reason)
	assert.Equal(t, "stopped_before_delivery", *frames[0].Reason)
	assert.Equal(t, "client-u7-1", frames[0].ClientMessageId)

	handler.flushPendingMessageStatusesAsWorking(sessionID)
	select {
	case raw := <-wc.sendCh:
		t.Fatalf("a discarded message must never be reported working afterwards, got %s", raw)
	case <-time.After(300 * time.Millisecond):
	}
}

// Oracle: FR-024 - after reload the original message is still returned, with
// the read-only input_disposition, and the response validates against Message.yaml.
func TestGetSessionMessages_ShowsInputDispositionForADiscardedInput(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	meta, err := store.NewSession("chat", "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscript(meta.ID, sessionEntryForDisposition("m-keep", "kept", "")))
	require.NoError(t, store.AppendTranscript(meta.ID, sessionEntryForDisposition("m-gone", "discarded text", "c-gone")))
	require.NoError(t, store.RecordInputDiscarded(meta.ID, "m-gone", ""))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+meta.ID+"/messages", nil)
	r.URL.Path = "/api/v1/sessions/" + meta.ID + "/messages"
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var msgs []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &msgs))
	require.Len(t, msgs, 2)
	assert.NotContains(t, msgs[0], "input_disposition")
	assert.Equal(t, "discarded text", msgs[1]["content"], "the archived bytes are still returned")
	assert.Equal(t, map[string]any{
		"message_id": "m-gone", "client_message_id": "c-gone",
		"state": "discarded", "reason": "stopped_before_delivery",
	}, msgs[1]["input_disposition"])

	contractsDir := filepath.Join(filepath.Dir(gatewayTestCallerFile(t)), "..", "..", "contracts", "components", "schemas")
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(newYAMLSchemaLoader(t))
	schema, err := compiler.Compile("file://" + filepath.Join(contractsDir, "Message.yaml"))
	require.NoError(t, err)
	for _, m := range msgs {
		assert.NoError(t, schema.Validate(m), "each message validates against Message.yaml: %v", m)
	}
}

func sessionEntryForDisposition(id, content, clientID string) session.TranscriptEntry {
	return session.TranscriptEntry{
		ID: id, Role: "user", Content: content, AgentID: "mia",
		Timestamp: time.Now().UTC(), ClientMessageID: clientID,
	}
}

// S3-02 server half: a /clear the worker refused or could not carry out is
// framed to every bound tab as message_status failed for the /clear's own row.
// The path under test is the real one: the WS handler installs itself as the
// agent loop's not-performed receiver at construction, and the receiver
// resolves the client id from the persisted entry.
func TestClearNotPerformed_IsFramedAsAFailedMessageStatus(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	handler.handleChatMessageWithClientID(context.Background(), "chat-s302", "", "/clear", "", nil,
		"", "", false, "client-s302-1", nil, wc)
	received := readMessageStatusFrames(t, wc, 1)
	sessionID := received[0].SessionId
	bindTestConnToSession(handler, "chat-s302", sessionID, wc)

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var entryID string
	for _, e := range entries {
		if e.ClientMessageID == "client-s302-1" {
			entryID = e.ID
		}
	}
	require.NotEmpty(t, entryID, "setup: the persisted /clear row")

	// Through the loop's own seam, not by calling the method directly: this is
	// what proves the constructor wired the receiver.
	handler.agentLoop.NotifyClearNotPerformedForTest(sessionID, entryID)

	frames := readMessageStatusFrames(t, wc, 1)
	assert.Equal(t, "failed", frames[0].State)
	assert.Equal(t, "client-s302-1", frames[0].ClientMessageId)
	assert.Equal(t, sessionID, frames[0].SessionId)

	// A row with no client id (not a web send) frames nothing.
	handler.announceClearNotPerformed(sessionID, "no-such-entry")
	select {
	case raw := <-wc.sendCh:
		t.Fatalf("an unknown row must frame nothing, got %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
}

// R2-S2-01: a refused /clear whose row cannot be signalled must never be silent.
// With the SPA timer gone, "no failed frame" reads as "held", so every web case
// that cannot publish the frame leaves a Warn naming the session and the entry.
func captureSlogWarn(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestAnnounceClearNotPerformed_UnresolvableRowIsLoggedNotSilent(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	handler.handleChatMessageWithClientID(context.Background(), "chat-r2s2", "", "hello", "", nil,
		"", "", false, "client-r2s2-1", nil, wc)
	sessionID := readMessageStatusFrames(t, wc, 1)[0].SessionId

	logs := captureSlogWarn(t)
	handler.announceClearNotPerformed(sessionID, "no-such-entry")
	out := logs.String()
	assert.Contains(t, out, `"level":"WARN"`)
	assert.Contains(t, out, sessionID)
	assert.Contains(t, out, "no-such-entry")
}

func TestAnnounceClearNotPerformed_RowWithoutClientIDIsLoggedNotSilent(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	handler.handleChatMessageWithClientID(context.Background(), "chat-r2s2b", "", "hello", "", nil,
		"", "", false, "client-r2s2-2", nil, wc)
	sessionID := readMessageStatusFrames(t, wc, 1)[0].SessionId
	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NoError(t, store.AppendTranscript(sessionID, session.TranscriptEntry{
		ID: "clear-row-noid", Role: "user", Content: "/clear", Timestamp: time.Now().UTC(),
	}))

	logs := captureSlogWarn(t)
	handler.announceClearNotPerformed(sessionID, "clear-row-noid")
	out := logs.String()
	assert.Contains(t, out, `"level":"WARN"`, "a refused /clear with no client_message_id cannot be framed; that must be logged")
	assert.Contains(t, out, "clear-row-noid")
}

// R3 follow-up (founder, option A): the held /clear is framed as the existing
// message_status state=held for the /clear's own row (sessionId + the row's
// client_message_id), after "received" and before any "failed". A held tick that
// arrives after the row failed is dropped: failed is terminal.
func TestClearHeld_IsFramedAsAHeldMessageStatusAndNeverFollowsFailed(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	handler.handleChatMessageWithClientID(context.Background(), "chat-held-clear", "", "/clear", "", nil,
		"", "", false, "client-held-clear-1", nil, wc)
	received := readMessageStatusFrames(t, wc, 1)
	require.Equal(t, "received", received[0].State)
	sessionID := received[0].SessionId
	bindTestConnToSession(handler, "chat-held-clear", sessionID, wc)

	store := handler.agentLoop.ResolveSessionStore(sessionID)
	require.NotNil(t, store)
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	var entryID string
	for _, e := range entries {
		if e.ClientMessageID == "client-held-clear-1" {
			entryID = e.ID
		}
	}
	require.NotEmpty(t, entryID, "setup: the persisted /clear row")

	handler.agentLoop.NotifyClearHeldForTest(sessionID, entryID)
	frames := readMessageStatusFrames(t, wc, 1)
	assert.Equal(t, "held", frames[0].State)
	assert.Equal(t, "client-held-clear-1", frames[0].ClientMessageId)
	assert.Equal(t, sessionID, frames[0].SessionId)

	handler.agentLoop.NotifyClearNotPerformedForTest(sessionID, entryID)
	assert.Equal(t, "failed", readMessageStatusFrames(t, wc, 1)[0].State)

	handler.agentLoop.NotifyClearHeldForTest(sessionID, entryID) // late: the row already failed
	select {
	case raw := <-wc.sendCh:
		t.Fatalf("a held frame was sent after failed: %s", raw)
	case <-time.After(200 * time.Millisecond):
	}
}
