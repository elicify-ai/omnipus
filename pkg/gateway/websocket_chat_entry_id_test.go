// websocket_chat_entry_id_test.go: the inbound message published for a chat
// frame carries the id of the transcript entry persisted for that frame.

package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/stretchr/testify/require"
)

// The agent loop recognises the current turn's own user entry by this id, so
// the published message must carry exactly the id of the entry the handler
// durably wrote — for a session-minting first message and for a later one.
func TestHandleChatMessage_PublishedMessageCarriesPersistedEntryID(t *testing.T) {
	msgBus := bus.NewMessageBus()
	handler, _ := newTestWSHandlerForModelName(t, msgBus)
	wc := makeTestConn()

	send := func(chatID, sessionID, content string) bus.InboundMessage {
		t.Helper()
		handler.handleChatMessage(context.Background(), chatID, sessionID, content, "", nil, "", "", false, wc)
		select {
		case msg := <-msgBus.InboundChan():
			return msg
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for bus.InboundMessage")
			return bus.InboundMessage{}
		}
	}

	persistedID := func(sessionID, content string) string {
		t.Helper()
		store := handler.agentLoop.ResolveSessionStore(sessionID)
		require.NotNil(t, store)
		entries, err := store.ReadTranscript(sessionID)
		require.NoError(t, err)
		var ids []string
		for i := range entries {
			if entries[i].Role == "user" && entries[i].Content == content {
				ids = append(ids, entries[i].ID)
			}
		}
		require.Len(t, ids, 1, "exactly one persisted user entry for %q", content)
		return ids[0]
	}

	first := send("chat-entry-id", "", "FIRST-ENTRY-ID-MARKER")
	require.NotEmpty(t, first.SessionID)
	require.NotEmpty(t, first.TranscriptEntryID, "published message must carry the persisted entry id")
	require.Equal(t, persistedID(first.SessionID, "FIRST-ENTRY-ID-MARKER"), first.TranscriptEntryID)

	second := send("chat-entry-id", first.SessionID, "SECOND-ENTRY-ID-MARKER")
	require.Equal(t, first.SessionID, second.SessionID)
	require.NotEmpty(t, second.TranscriptEntryID)
	require.Equal(t, persistedID(second.SessionID, "SECOND-ENTRY-ID-MARKER"), second.TranscriptEntryID)
	require.NotEqual(t, first.TranscriptEntryID, second.TranscriptEntryID)
}
