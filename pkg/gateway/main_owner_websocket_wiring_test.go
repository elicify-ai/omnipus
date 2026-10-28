package gateway

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func ownerWiringSocket(t *testing.T, actor string) (*WSHandler, *bus.MessageBus, *session.UnifiedStore, *wsConn) {
	t.Helper()
	h, msgBus, al := l1WU1IdentitySocket(t, false)
	al.GetConfig().Gateway.Users = ownerWiringAccounts()
	t.Cleanup(h.Wait)
	t.Cleanup(msgBus.Close)
	wc := makeTestConn()
	wc.userID = actor // I2 explicitly supplies the already-authenticated connection principal.
	t.Cleanup(wc.close)
	h.mu.Lock()
	h.sessions["owner-wire-chat"] = wc // Connected but not attached, as before the first real frame.
	h.mu.Unlock()
	return h, msgBus, al.GetSessionStore(), wc
}

func ownerWiringSend(h *WSHandler, wc *wsConn, id, content string) {
	h.handleChatMessageWithClientID(context.Background(), "owner-wire-chat", id, content, "mia", nil,
		"", "default", "owner-wire-message", nil, wc)
}

func TestOwnerWiringI2_SavedSendAdoptsOnlyUnownedMainForPerson(t *testing.T) {
	for _, tc := range []struct {
		name, actor, original, want string
		main                        bool
	}{
		{name: "person_adopts_main", actor: "alice", main: true, want: "alice"},
		{name: "machine_does_not_adopt", actor: "cli", main: true, want: ""},
		{name: "anonymous_does_not_adopt", actor: "", main: true, want: ""},
		{name: "different_person_keeps_owner", actor: "bob", main: true, original: "alice", want: "alice"},
		{name: "non_main_remains_ownerless", actor: "alice", main: false, want: ""},
		{name: "non_main_keeps_owner", actor: "alice", main: false, original: "bob", want: "bob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, msgBus, store, wc := ownerWiringSocket(t, tc.actor)
			var meta *session.UnifiedMeta
			var err error
			if tc.main {
				meta, err = store.GetOrCreateMainSession("default", "mia")
			} else {
				meta, err = store.NewSession(session.SessionTypeChat, "webchat", "mia")
			}
			require.NoError(t, err)
			workspace := "default"
			require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{Owner: &tc.original, WorkspaceID: &workspace}))
			const content = "A real saved message for main-owner attribution."

			ownerWiringSend(h, wc, meta.ID, content)
			frames := issue1090DrainQueuedFrames(t, wc)
			require.NotContains(t, issue1090FrameTypes(frames), "error", "the send must reach durable intake, not an unrelated refusal")
			require.Contains(t, issue1090FrameTypes(frames), "user_message", "real saved-message echo proves the send path completed")
			msg := awaitInboundMessage(t, msgBus, "owner wiring saved message")
			require.Equal(t, meta.ID, msg.SessionID)
			require.Equal(t, tc.actor, msg.GatewayUserID, "the actual supplied principal reached intake")
			entries, err := store.ReadTranscript(meta.ID)
			require.NoError(t, err)
			require.Len(t, entries, 1, "the real user message must be saved exactly once")
			require.Equal(t, "user", entries[0].Role)
			require.Equal(t, content, entries[0].Content)
			cached, err := store.GetMeta(meta.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, cached.Owner, "send adoption updates only eligible human main ownership")
			persisted := ownerWiringFreshMeta(t, store, meta.ID)
			require.Equal(t, tc.want, persisted.Owner, "the same ownership must survive a fresh disk read")
			require.Equal(t, meta.Type, persisted.Type)
		})
	}
}

func TestOwnerWiringI2_FailedSaveDoesNotAdopt(t *testing.T) {
	h, msgBus, store, wc := ownerWiringSocket(t, "alice")
	main, err := store.GetOrCreateMainSession("default", "mia")
	require.NoError(t, err)
	require.Equal(t, "", main.Owner)
	before := ownerWiringIdentityBytes(t, store, main.ID)
	path := filepath.Join(store.BaseDir(), main.ID, "transcript.jsonl")
	if removeErr := os.Remove(path); removeErr != nil {
		require.ErrorIs(t, removeErr, os.ErrNotExist, "only an absent pristine transcript is harmless during setup")
	}
	require.NoError(t, os.Mkdir(path, 0o700), "a directory blocks append even when tests run as root")
	opened, openErr := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if opened != nil {
		require.NoError(t, opened.Close())
	}
	require.ErrorIs(t, openErr, syscall.EISDIR, "instrument control: append really cannot open this target")

	ownerWiringSend(h, wc, main.ID, "This unsaved message must not adopt its sender.")
	frames := issue1090DrainQueuedFrames(t, wc)
	require.Contains(t, issue1090FrameTypes(frames), "error", "save failure is actually reported")
	require.NotContains(t, issue1090FrameTypes(frames), "user_message", "an unsaved send is never echoed as saved")
	require.Len(t, msgBus.InboundChan(), 0, "failed save must not dispatch a turn")
	require.Equal(t, before, ownerWiringIdentityBytes(t, store, main.ID), "failure before saving must not stamp an owner or activity")
	require.Equal(t, "", ownerWiringFreshMeta(t, store, main.ID).Owner)
	// Restore only the faulted filesystem edge, then prove no entry was saved.
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	entries, err := store.ReadTranscript(main.ID)
	require.NoError(t, err)
	require.Empty(t, entries, "the failed send saved no user message")
}

func TestOwnerWiringI2_OpenAndAttachWithoutSendDoNotAdopt(t *testing.T) {
	h, _, store, wc := ownerWiringSocket(t, "alice")
	main, err := store.GetOrCreateMainSession("default", "mia")
	require.NoError(t, err)
	updated := ownerWiringHistoricalRecency(t, store, main.ID)
	before := ownerWiringIdentityBytes(t, store, main.ID)

	h.emitSessionState(wc, "") // The real connection-open state emission.
	openFrames := issue1090DrainQueuedFrames(t, wc)
	require.Contains(t, issue1090FrameTypes(openFrames), "session_state")
	require.Equal(t, "", ownerWiringFreshMeta(t, store, main.ID).Owner, "opening without a send never adopts")
	h.handleAttachSession(context.Background(), "owner-wire-chat", main.ID, nil, wc)
	attachFrames := issue1090DrainQueuedFrames(t, wc)
	require.Contains(t, issue1090FrameTypes(attachFrames), "catch_up_complete", "the real attach must complete, not refuse")
	require.NotContains(t, issue1090FrameTypes(attachFrames), "error")
	persisted := ownerWiringFreshMeta(t, store, main.ID)
	require.Equal(t, "", persisted.Owner, "a signed-in person attaching is still only reading")
	require.Equal(t, updated, persisted.UpdatedAt)
	require.Equal(t, before, ownerWiringIdentityBytes(t, store, main.ID), "open and attach do not rewrite the identity")
}

func TestOwnerWiringI2_AdoptionAfterSavedMessageDoesNotAddRecency(t *testing.T) {
	h, _, store, wc := ownerWiringSocket(t, "alice")
	main, err := store.GetOrCreateMainSession("default", "mia")
	require.NoError(t, err)
	entry := session.TranscriptEntry{ID: "owner-wire-prior-save", Role: "user", AgentID: "mia", Content: "Saved before adoption.", Timestamp: time.Now().UTC()}
	require.NoError(t, store.AppendTranscriptWithProvenance(main.ID, entry, "alice"))
	require.NoError(t, store.FlushSessionStats(main.ID))
	before := ownerWiringFreshMeta(t, store, main.ID)
	require.Equal(t, "", before.Owner, "a store append alone does not perform gateway adoption")
	stage := &wsHandlerHandleChatMessage{h: h, wc: wc, store: store, sessionID: main.ID}
	stage.adoptMainOwnerOnSend()
	after := ownerWiringFreshMeta(t, store, main.ID)
	require.Equal(t, "alice", after.Owner, "the post-save gateway adoption really runs")
	require.Equal(t, before.UpdatedAt, after.UpdatedAt, "adoption adds no recency beyond the saved message's own activity")
}
