package gateway

import (
	"context"
	"encoding/json"
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
