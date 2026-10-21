package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
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
	handler.handleChatMessageWithClientID(context.Background(), "chat-u7-discard", "", "queued", "mia", nil,
		"", "", "client-u7-1", nil, wc)
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

	historyMigrationPage(t, meta.ID, w.Body.Bytes(), "latest", false, false)
	msgs := historyMigrationEntryObjects(t, w.Body.Bytes(), "entries")
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
	handler.handleChatMessageWithClientID(context.Background(), "chat-s302", "", "/clear", "mia", nil,
		"", "", "client-s302-1", nil, wc)
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
//
// R3-S5-PTA-05: slog.Default() is process-global, so a goroutine left over from
// an earlier gateway test can log through this capture while the test reads it.
// The buffer is therefore mutex-protected on BOTH sides (Write from the
// handler, String from the test); a bare bytes.Buffer is a data race under -race.
func captureSlogWarn(t *testing.T) *syncLogBuffer {
	t.Helper()
	buf := &syncLogBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// syncLogBuffer is a bytes.Buffer safe for concurrent writers and readers.
type syncLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// The capture must survive a log call racing the read. Under -race a plain
// bytes.Buffer fails this test; the mutex-protected one does not.
func TestCaptureSlogWarn_ConcurrentLogAndReadIsRaceFree(t *testing.T) {
	logs := captureSlogWarn(t)
	stop := make(chan struct{})
	done := make(chan struct{})
	logged := make(chan struct{})
	go func() {
		defer close(done)
		first := true
		for {
			select {
			case <-stop:
				return
			default:
				slog.Warn("lingering goroutine from an earlier test", "k", "v")
				if first {
					first = false
					close(logged)
				}
			}
		}
	}()
	<-logged // the writer is running before the reads start, so they overlap its writes
	for i := 0; i < 200; i++ {
		_ = logs.String()
	}
	close(stop)
	<-done
	assert.Contains(t, logs.String(), "lingering goroutine from an earlier test")
}

func TestAnnounceClearNotPerformed_UnresolvableRowIsLoggedNotSilent(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	handler.handleChatMessageWithClientID(context.Background(), "chat-r2s2", "", "hello", "mia", nil,
		"", "", "client-r2s2-1", nil, wc)
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
	handler.handleChatMessageWithClientID(context.Background(), "chat-r2s2b", "", "hello", "mia", nil,
		"", "", "client-r2s2-2", nil, wc)
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
	handler.handleChatMessageWithClientID(context.Background(), "chat-held-clear", "", "/clear", "mia", nil,
		"", "", "client-held-clear-1", nil, wc)
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

// R4-SRV-CR-03: nothing moves a held /clear row on except its outcome. Once the
// hold is released the recorded state is no longer "held", so a client retry of
// the same id is not answered "held" again; a failure that raced ahead stays
// "failed".
func TestClearHeld_ReleaseMovesTheRecordedStateOnAndNeverOverwritesFailed(t *testing.T) {
	handler, _ := newTestWSHandlerForModelName(t, bus.NewMessageBus())
	wc := makeTestConn()
	seed := func(chatID, clientID string) (sessionID, entryID string) {
		handler.handleChatMessageWithClientID(context.Background(), chatID, "", "/clear", "mia", nil,
			"", "", clientID, nil, wc)
		received := readMessageStatusFrames(t, wc, 1)
		require.Equal(t, "received", received[0].State)
		sessionID = received[0].SessionId
		bindTestConnToSession(handler, chatID, sessionID, wc)
		store := handler.agentLoop.ResolveSessionStore(sessionID)
		require.NotNil(t, store)
		entries, err := store.ReadTranscript(sessionID)
		require.NoError(t, err)
		for _, e := range entries {
			if e.ClientMessageID == clientID {
				entryID = e.ID
			}
		}
		require.NotEmpty(t, entryID, "setup: the persisted /clear row")
		return sessionID, entryID
	}

	// Held, then released: the record moves on and no frame is published.
	sid, entry := seed("chat-rel-1", "client-rel-1")
	handler.agentLoop.NotifyClearHeldForTest(sid, entry)
	require.Equal(t, "held", readMessageStatusFrames(t, wc, 1)[0].State)
	require.Equal(t, "held", handler.acceptedMessageState(sid, "client-rel-1"), "setup: recorded held")
	handler.agentLoop.NotifyClearReleasedForTest(sid, entry)
	assert.Equal(t, "working", handler.acceptedMessageState(sid, "client-rel-1"),
		"a released /clear is no longer recorded as held")
	select {
	case raw := <-wc.sendCh:
		t.Fatalf("releasing a hold publishes no frame: %s", raw)
	case <-time.After(200 * time.Millisecond):
	}

	// Held, failed, then a late release: failed stays terminal.
	sid2, entry2 := seed("chat-rel-2", "client-rel-2")
	handler.agentLoop.NotifyClearHeldForTest(sid2, entry2)
	require.Equal(t, "held", readMessageStatusFrames(t, wc, 1)[0].State)
	handler.agentLoop.NotifyClearNotPerformedForTest(sid2, entry2)
	require.Equal(t, "failed", readMessageStatusFrames(t, wc, 1)[0].State)
	handler.agentLoop.NotifyClearReleasedForTest(sid2, entry2)
	assert.Equal(t, "failed", handler.acceptedMessageState(sid2, "client-rel-2"),
		"a late release must not overwrite the terminal failed state")
}
