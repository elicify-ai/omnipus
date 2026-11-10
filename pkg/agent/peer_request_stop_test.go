package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Final-gate SG-01 (High): another agent's peer request carries no human
// authority. The envelope is produced by REAL SendPeer/AdmitRequest for the
// receiver's actual computed main, with no post-admission retargeting. However
// the person's Stop lands, a pending request must neither clear it nor run.

// Create the real destination pair/main BEFORE seeding its history or state.
// The shared d2b chat fixture intentionally creates an extra chat, not a main;
// changing only a later wake's SessionID would detach it from its capture.
func newPeerStopMainRoot(t *testing.T) *d2bRoot {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	store := al.GetSessionStore()
	main, err := store.GetOrCreateMainSession(testHarnessWorkspaceMembershipID, testDefaultAgentID)
	require.NoError(t, err)
	require.NotNil(t, main)
	require.Equal(t, session.SessionTypeMain, main.Type)
	require.Equal(t, testHarnessWorkspaceMembershipID, main.WorkspaceID)
	owner, title := "d2b-owner", "Peer Stop receiver main"
	require.NoError(t, store.SetMeta(main.ID, session.MetaPatch{Owner: &owner, Title: &title}))
	provider := &d2bProvider{al: al, id: main.ID}
	inst, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	inst.Provider = provider
	h := &d2bRoot{al: al, id: main.ID, provider: provider}
	h.prior = []session.TranscriptEntry{
		{ID: "peer-stop-prior-question", Role: "user", AgentID: testDefaultAgentID, Content: "Keep this earlier question.", Timestamp: time.Now().UTC()},
		{ID: "peer-stop-prior-answer", Role: "assistant", AgentID: testDefaultAgentID, Content: "Keep this earlier answer.", Timestamp: time.Now().UTC()},
	}
	for _, entry := range h.prior {
		require.NoError(t, store.AddMessage(main.ID, entry.Role, entry.Content))
		require.NoError(t, store.AppendTranscript(main.ID, entry))
	}
	prior, err := store.ReadTranscript(main.ID)
	require.NoError(t, err)
	require.Len(t, prior, len(h.prior), "SETUP: the receiver's old history is durable")
	h.prior = prior
	return h
}

func seedPeerStopMainState(t *testing.T, h *d2bRoot, state session.LifecycleState) {
	t.Helper()
	// Canonical creation retains the main's real origin/owner/workspace. Only
	// the saved prior state is seeded, as in the original d2b fixture; no
	// execution identity or admitted live turn is invented.
	gate := h.al.steerAdmission()
	gate.entryMu.Lock()
	_, err := h.al.ensureOrdinaryRootRecord(h.al.GetSessionLifecycleStore(), h.id, h.al.GetSessionStore())
	gate.entryMu.Unlock()
	require.NoError(t, err)
	require.NoError(t, h.al.GetSessionLifecycleStore().Mutate(h.id, func(rec *session.LifecycleRecord) error {
		rec.State = state
		if state == session.LifecycleStopped {
			rec.StopNote = &session.StopNote{At: time.Now().UTC(), By: "human:d2b-owner", Seq: uint64(rec.Generation), Cause: session.StopCauseStop}
		}
		return nil
	}))
	rec := h.load(t)
	require.Equal(t, state, rec.State)
	require.NotNil(t, rec.Origin)
	require.Equal(t, session.OriginKindMain, rec.Origin.Kind)
	require.Equal(t, testHarnessWorkspaceMembershipID, rec.WorkspaceID)
}

func peerRequestEnvelope(t *testing.T, h *d2bRoot) bus.InboundMessage {
	t.Helper()
	store := h.al.GetSessionStore()
	receiver, err := store.GetMeta(h.id)
	require.NoError(t, err)
	require.NotNil(t, receiver)
	require.Equal(t, session.SessionTypeMain, receiver.Type)
	deps := &fakeAddressDeps{eligible: map[string]bool{receiver.WorkspaceID + "/" + receiver.AgentID: true}}
	h.al.SetAddressDeps(deps)
	inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow}})
	asker, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	require.NoError(t, err)
	askerWorkspace := addrWS
	require.NoError(t, store.SetMeta(asker.ID, session.MetaPatch{WorkspaceID: &askerWorkspace}))
	receipt, err := h.al.NewAddressRouter().SendPeer(context.Background(), tools.PeerRequest{
		RecipientWorkspaceID: receiver.WorkspaceID, RecipientAgentID: receiver.AgentID, Content: "please review X",
		Sender: tools.SendOrigin{WorkspaceID: askerWorkspace, AgentID: "ray"}, SenderSessionID: asker.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, receipt.RequestID)
	require.Equal(t, h.id, receipt.SessionID, "real admission must choose this actual receiver main")
	capture, found, err := h.al.RequestLedger().Get(h.id, receipt.RequestID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, h.id, capture.ReceiverSessionID)
	require.Equal(t, receiver.WorkspaceID, capture.Receiver.WorkspaceID)
	require.Equal(t, receiver.AgentID, capture.Receiver.AgentID)
	require.Equal(t, asker.ID, capture.Source.SessionID)
	require.Equal(t, askerWorkspace, capture.Source.Owner.WorkspaceID)
	require.NotEqual(t, capture.Source.Owner.WorkspaceID, capture.Receiver.WorkspaceID,
		"instrument: a genuine different-source-workspace request still targets the receiver's own pair")
	select {
	case m := <-h.al.bus.InboundChan():
		require.Equal(t, h.id, m.SessionID, "consume the exact admitted wake, never rewrite its SessionID")
		require.Equal(t, receipt.RequestID, m.TranscriptEntryID)
		require.Equal(t, receiver.WorkspaceID, m.Metadata["workspace_id"])
		require.Equal(t, receiver.AgentID, m.Metadata["agent_id"])
		require.Equal(t, "1", m.Metadata[ownerWakeMetadataKey])
		require.False(t, m.UserInitiated)
		require.False(t, m.OperatorPrompt)
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("the peer request was not published")
		return bus.InboundMessage{}
	}
}

func TestPeerRequest_AfterLandedStop_DoesNotReviveOrRun(t *testing.T) {
	h := newPeerStopMainRoot(t)
	req := peerRequestEnvelope(t, h)                      // pending ...
	seedPeerStopMainState(t, h, session.LifecycleStopped) // ... a person's Stop lands ...
	before := h.journal(t)
	recordBefore := h.load(t)
	store := h.al.GetSessionStore()
	archiveBefore, err := store.ReadArchive(t.Context(), h.id)
	require.NoError(t, err)
	require.NotEmpty(t, archiveBefore, "instrument: old model history is real")
	transcriptBefore, err := store.ReadTranscript(h.id)
	require.NoError(t, err)
	pendingBefore, err := h.al.RequestLedger().Undelivered(h.id)
	require.NoError(t, err)
	require.Len(t, pendingBefore, 1)
	require.Equal(t, req.TranscriptEntryID, pendingBefore[0].RequestID)

	reply, _, perr := h.al.processMessageAsPrimaryForTest(context.Background(), req) // ... then it is consumed

	require.NoError(t, perr, "a stop is not a failure: the retained request reports no error")
	assert.Empty(t, reply)
	assert.Empty(t, h.provider.calls(), "a peer request must never run the model on a stopped chat")
	got := h.load(t)
	assert.Equal(t, session.LifecycleStopped, got.State, "the person's Stop must still hold")
	assert.Equal(t, 1, got.Generation)
	assert.Equal(t, string(before), string(h.journal(t)), "the request must not append to the lifecycle journal")
	assert.Equal(t, recordBefore, got, "no field of the stopped record may change")
	archiveAfter, err := store.ReadArchive(t.Context(), h.id)
	require.NoError(t, err)
	assert.Equal(t, archiveBefore, archiveAfter, "stopped peer delivery cannot append to model history")
	transcriptAfter, err := store.ReadTranscript(h.id)
	require.NoError(t, err)
	assert.Equal(t, transcriptBefore, transcriptAfter, "the already-admitted request and old visible history stay intact")
	pendingAfter, err := h.al.RequestLedger().Undelivered(h.id)
	require.NoError(t, err)
	assert.Equal(t, pendingBefore, pendingAfter, "the request remains pending, not discarded or falsely delivered")
	_, delivered := h.al.deliveredRequests.Load(req.TranscriptEntryID)
	assert.False(t, delivered, "a stopped receiver never received the request")
}

func TestPeerRequest_IsNotAHumanTurnAndNeverAnOperatorPrompt(t *testing.T) {
	h := newPeerStopMainRoot(t)
	req := peerRequestEnvelope(t, h)
	assert.False(t, reviveInboundIsHumanTurn(req), "an agent's request carries no human revival authority")
	assert.False(t, req.OperatorPrompt)
	assert.False(t, req.UserInitiated)
}

func TestPeerRequest_ControlsLiveChatStillGetsItAndHumanStillRevives(t *testing.T) {
	h := newPeerStopMainRoot(t)
	seedPeerStopMainState(t, h, session.LifecycleCompleted) // an idle, unstopped chat
	before := h.journal(t)
	req := peerRequestEnvelope(t, h)
	_, _, err := h.al.processMessageAsPrimaryForTest(context.Background(), req)
	require.NoError(t, err)
	assert.Len(t, h.provider.calls(), 1, "an unstopped receiver must still run on a peer request")
	calls := h.provider.calls()
	require.Len(t, calls, 1)
	require.NoError(t, calls[0].readErr)
	require.NotNil(t, calls[0].record)
	assert.Equal(t, h.id, calls[0].record.SessionID)
	assert.True(t, calls[0].registered, "the admitted receiver owns the live provider round")
	assert.Contains(t, calls[0].lastUser, "please review X", "the model sees the actual peer body")
	h.assertHistoryPreserved(t, before)
	capture, found, err := h.al.RequestLedger().Get(h.id, req.TranscriptEntryID)
	require.NoError(t, err)
	require.True(t, found)
	assert.True(t, capture.Delivered, "the live receiver really received the admitted request")
	assert.False(t, capture.Discarded)

	h2 := newD2bRoot(t)
	h2.seedState(t, session.LifecycleStopped)
	humanBefore := h2.journal(t)
	_, err = h2.humanTurn(t, "carry on")
	require.NoError(t, err)
	assert.Len(t, h2.provider.calls(), 1, "a person's message must still continue a stopped chat")
	assert.NotEqual(t, session.LifecycleStopped, h2.load(t).State)
	h2.assertHistoryPreserved(t, humanBefore)
}
