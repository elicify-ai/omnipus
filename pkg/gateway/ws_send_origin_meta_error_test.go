package gateway

// SV-9b / MU-26, spec r8 §4.3.1 and §6.1: an actual metadata READ ERROR
// (not a missing chat) is a correlated origin_unverified refusal. The refused
// message produces no other frame and cannot interrupt an unrelated stream.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type sv9bStreamingProvider struct {
	next chan struct{}
	stop chan struct{}
	once sync.Once
}

func (*sv9bStreamingProvider) GetDefaultModel() string { return "sv9b-stream" }
func (*sv9bStreamingProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return nil, errors.New("SV-9b requires a real streaming turn")
}
func (p *sv9bStreamingProvider) ChatStream(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	chunk("SV9b before refusal")
	select {
	case <-p.next:
	case <-p.stop:
		return nil, context.Canceled
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	chunk("SV9b before refusal; still streaming after refusal")
	select {
	case <-p.stop:
		return nil, context.Canceled
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *sv9bStreamingProvider) shutdown() { p.once.Do(func() { close(p.stop) }) }

// failSV9bMetaRead evicts the valid cache entry, then makes only the target's
// meta.json unreadable as a file. A directory gives a real error even as root.
func failSV9bMetaRead(t *testing.T, store *session.UnifiedStore, sessionID string) func() {
	t.Helper()
	meta, err := store.GetMeta(sessionID)
	require.NoError(t, err, "instrument: metadata is readable before the fault")
	require.NotNil(t, meta)
	require.NoError(t, store.FlushAndEvictSessionMeta(sessionID))
	path := filepath.Join(store.BaseDir(), sessionID, "meta.json")
	backup := path + ".sv9b-saved"
	require.NoError(t, os.Rename(path, backup))
	restored := false
	restore := func() {
		t.Helper()
		if restored {
			return
		}
		require.NoError(t, os.Remove(path))
		require.NoError(t, os.Rename(backup, path))
		restored = true
	}
	t.Cleanup(restore)
	require.NoError(t, os.Mkdir(path, 0o700))
	failed, err := store.GetMeta(sessionID)
	require.Nil(t, failed)
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "instrument: GetMeta must reach the actual filesystem failure")
	require.Equal(t, path, pathErr.Path)
	require.False(t, errors.Is(err, os.ErrNotExist), "instrument: read-error and missing-chat rows are distinct")
	t.Logf("instrument: real GetMeta read error, not cache or not-exist: %v", err)
	return restore
}

func requireSV9bToken(t *testing.T, conn *websocket.Conn, sessionID, content string) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(lifecycleEW)))
	for {
		_, raw, err := conn.ReadMessage()
		require.NoError(t, err, "SV-9b: the unrelated turn must keep streaming: %s", content)
		var peek wsTypeOnly
		require.NoError(t, json.Unmarshal(raw, &peek))
		require.NotEqual(t, "done", peek.Type, "SV-9b: unrelated turn must not end at refusal")
		if peek.Type == "token" {
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &token))
			require.Equal(t, sessionID, token.SessionId)
			require.Equal(t, content, token.Content)
			return
		}
	}
}

func testSV9bMetaReadError(t *testing.T) {
	provider := &sv9bStreamingProvider{next: make(chan struct{}), stop: make(chan struct{})}
	s := newSendOriginSrv(t, provider)
	t.Cleanup(provider.shutdown)
	conn := s.dialBrowserConn(t)
	drainAdmissionOneShot(t, conn)
	unrelated := s.mintChat(t, conn, soKeyA, "SV9b unrelated streaming turn")
	requireSV9bToken(t, conn, unrelated, "SV9b before refusal")
	turnID, _, _, active := s.al.ActiveForegroundTurnInfo(unrelated)
	require.True(t, active, "instrument: unrelated foreground turn is genuinely live")
	store := s.al.GetSessionStore()
	target, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	before, err := store.ReadTranscript(target.ID)
	require.NoError(t, err)
	restore := failSV9bMetaRead(t, store, target.ID)

	const clientID = "sv9b-unverified-message"
	origin := soNewChatOrigin(soKeyB)
	soSend(t, conn, soMessage(&target.ID, origin, clientID, "must not reach the target"))
	soSend(t, conn, generated.PingFrame{Type: "ping"})
	frames := soWindow(t, conn, 4*time.Second, "pong")
	require.NotEmpty(t, frames, "instrument: the raw pre-check must return to the read loop")
	require.Equal(t, "pong", frames[len(frames)-1].Type, "instrument: complete refusal interval must be observed")
	frames = frames[:len(frames)-1]
	assert.Equal(t, []string{"send_refused"}, soTypes(frames), "SV-9b: exactly one correlated refusal, no generic error, status, snapshot or turn effect")
	if assert.Len(t, frames, 1) {
		want, marshalErr := json.Marshal(generated.SendRefusedFrame{
			Type: "send_refused", Reason: "origin_unverified", RefusedFrame: "message",
			SessionId: &target.ID, ClientMessageId: strPtr(clientID), Origin: origin,
		})
		require.NoError(t, marshalErr)
		assert.JSONEq(t, string(want), string(frames[0].Raw), "SV-9b/MU-26: exact unsequenced correlated origin_unverified refusal")
	}
	restore()
	after, err := store.ReadTranscript(target.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "SV-9b: refusal must leave target history untouched")
	afterTurn, _, _, stillActive := s.al.ActiveForegroundTurnInfo(unrelated)
	assert.True(t, stillActive, "SV-9b: the unrelated foreground turn remains live")
	assert.Equal(t, turnID, afterTurn, "SV-9b: no replacement turn may hide interruption")
	close(provider.next)
	// StreamingProvider receives accumulated text; TokenFrame.yaml requires
	// append-only wire chunks, so only this newly generated suffix is expected.
	requireSV9bToken(t, conn, unrelated, "; still streaming after refusal")
	t.Log("instrument: same unrelated turn streamed a new token after the complete refusal interval")
}
