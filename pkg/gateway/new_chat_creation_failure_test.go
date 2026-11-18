package gateway

// SV-7 / SV-8, spec r8 §4.3.3: a failed creation write gives no success
// acknowledgement and leaves no listed chat. These independently runnable
// guards use the real filesystem edge, not a mocked store or private hook.
// They already pass on the pre-GREEN code: passing guards are not RED proof.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// obstructNewChatCreation forces mkdir to fail before identity publication,
// even when running as root. It preserves the original directory and restores
// it before listing, so an unavailable list cannot masquerade as an empty one.
func obstructNewChatCreation(t *testing.T, store *session.UnifiedStore) func() {
	t.Helper()
	base := store.BaseDir()
	backup := filepath.Join(t.TempDir(), "sessions-before-fault")
	require.NoError(t, os.Rename(base, backup))
	restored := false
	restore := func() {
		t.Helper()
		if restored {
			return
		}
		require.NoError(t, os.Remove(base))
		require.NoError(t, os.Rename(backup, base))
		restored = true
	}
	// Register before making the obstruction so assertion failures still restore.
	t.Cleanup(restore)
	require.NoError(t, os.WriteFile(base, []byte("creation-fault"), 0o600))
	var pathErr *os.PathError
	require.ErrorAs(t, os.MkdirAll(filepath.Join(base, "fault-instrument"), 0o700), &pathErr)
	require.Equal(t, "mkdir", pathErr.Op)
	t.Logf("instrument: real pre-identity creation mkdir fails: %v", pathErr)
	return restore
}

func requireOnlyControlChatListed(t *testing.T, store *session.UnifiedStore, controlID string) {
	t.Helper()
	// Both warm and cold readers must list the known control and no failed mint.
	cold, err := session.NewUnifiedStore(store.BaseDir())
	require.NoError(t, err)
	for label, reader := range map[string]*session.UnifiedStore{"warm": store, "cold": cold} {
		listed, err := reader.ListSessions()
		require.NoError(t, err, "%s listing must succeed after restoring the filesystem", label)
		ids := make([]string, 0, len(listed))
		for _, meta := range listed {
			ids = append(ids, meta.ID)
		}
		assert.Equal(t, []string{controlID}, ids, "%s: SV-7/8 failed creation must not add a listed chat", label)
	}
}

func TestNewChatKey_SV7_MintWriteFailureNotSavedNoAcknowledgement(t *testing.T) {
	handler, msgBus, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)
	drainAdmissionOneShot(t, conn)
	store := handler.agentLoop.GetSessionStore()
	control, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	requireOnlyControlChatListed(t, store, control.ID)
	restore := obstructNewChatCreation(t, store)

	const clientID = "sv7-failed-creation"
	soSend(t, conn, soMessage(nil, soNewChatOrigin(newChatKeyRestKey), clientID, "must not mint an unkeyed chat"))
	soSend(t, conn, generated.PingFrame{Type: "ping"})
	frames := soWindow(t, conn, 4*time.Second, "pong")
	require.NotEmpty(t, frames, "instrument: read-loop barrier must arrive")
	require.Equal(t, "pong", frames[len(frames)-1].Type, "instrument: inspect the complete creation-handling interval")
	frames = frames[:len(frames)-1]
	assert.Equal(t, []string{"error"}, soTypes(frames), "SV-7: not_saved only; no session_started, echo or receipt")
	if assert.Len(t, frames, 1) {
		var reply generated.ErrorFrame
		require.NoError(t, json.Unmarshal(frames[0].Raw, &reply))
		assert.Equal(t, "error", reply.Type)
		assert.Equal(t, strPtr("not_saved"), reply.FirstMessageError, "SV-7: creation write failure must be not_saved")
		assert.Equal(t, strPtr(clientID), reply.ClientMessageId)
		assert.Nil(t, reply.SessionId, "SV-7: do not acknowledge a failed mint")
	}
	assert.Empty(t, msgBus.InboundChan(), "SV-7: no turn admitted for the failed creation")
	restore()
	requireOnlyControlChatListed(t, store, control.ID)
}

func TestNewChatKey_SV8_RESTWriteFailureReturnsStorageErrorNo201(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	store := api.agentLoop.GetSessionStore()
	control, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	requireOnlyControlChatListed(t, store, control.ID)
	restore := obstructNewChatCreation(t, store)

	w := createSessionWithKey(t, api, `{"agent_id":"mia","new_chat_key":"`+newChatKeyRestKey+`"}`)
	// SV-8 specifies a storage error, not one exact HTTP error status.
	assert.GreaterOrEqual(t, w.Code, http.StatusInternalServerError, "SV-8: server-side storage failure must not return 201 or blame the request; body=%s", w.Body.String())
	assert.Less(t, w.Code, 600, "SV-8: failure must be a valid HTTP error status")
	assert.NotEmpty(t, w.Body.String(), "SV-8: return the storage error, not an empty success")
	restore()
	requireOnlyControlChatListed(t, store, control.ID)
}
