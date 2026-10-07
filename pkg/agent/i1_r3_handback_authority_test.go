package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func i1R3RestartStopped(t *testing.T) *d2bRoot {
	t.Helper()
	h := newD2bRoot(t)
	boot := h.al.bootEpochFor()
	require.NoError(t, h.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{SessionID: h.id, Generation: 1, State: session.LifecycleStopped, WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID, OwnerScopeKind: session.OwnerScopeHuman, Origin: &session.Origin{Kind: session.OriginKindChat}, ExecutionID: &session.ExecutionIdentity{RunID: "i1-restart-old", BootSeq: boot}, StopNote: &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}}))
	return h
}

// W1 lower-boundary RED: helper hand-back authority is not normal schedule
// authority and cannot revive an Interrupted root even if it reaches admission.
func TestI1R3HandbackCannotContinueRestartStop(t *testing.T) {
	h := i1R3RestartStopped(t)
	before, journal := h.load(t), h.journal(t)
	ts := &turnState{opts: processOptions{SessionKey: "agent:" + testDefaultAgentID + ":session:" + h.id, TranscriptSessionID: h.id, TranscriptStore: h.al.GetSessionStore(), WorkspaceID: testHarnessWorkspaceMembershipID}}
	d, admitted, err := h.al.admitOrdinaryRootWake(context.Background(), bus.InboundMessage{Channel: "system", AsyncTranscriptSessionID: h.id}, ts)
	if d != nil {
		t.Cleanup(func() { _ = h.al.finishExecutionDisposition(d) })
	}
	require.ErrorIs(t, err, steer.ErrDispatchCancelled, "only normal scheduler/heartbeat authority may continue a restart stop")
	assert.False(t, admitted)
	assert.Nil(t, d)
	assert.Equal(t, before, h.load(t))
	assert.Equal(t, journal, h.journal(t))
	assert.Empty(t, h.provider.calls())
	response, triggerErr := h.scheduledTurn("heartbeat", "Normal next tick may continue.")
	require.NoError(t, triggerErr)
	assert.Equal(t, d2bReply, response)
	assert.Len(t, h.provider.calls(), 1, "restriction must preserve founder-approved normal trigger continuation")
}

// Healthy outer-path control: a replayed helper wake is kept pending, not
// consumed or acknowledged, while the parent is stopped by restart.
func TestI1R3StoppedRootKeepsReplayedHandback(t *testing.T) {
	h := i1R3RestartStopped(t)
	id := "i1-child:1:final"
	message := bootHandback(t, "i1-child", h.id, id)
	appended, err := h.al.GetMessageInboxStore().Append(h.id, message)
	require.NoError(t, err)
	require.True(t, appended.Accepted)
	before := h.journal(t)
	wake := bus.InboundMessage{Channel: "system", AsyncTranscriptSessionID: h.id, Content: "finished", Metadata: map[string]string{"steer_message_id": id, "steer_generation": "1"}}
	response, wakeErr := h.al.processSteeredSystemWake(context.Background(), wake)
	require.NoError(t, wakeErr)
	assert.Empty(t, response)
	assert.Empty(t, h.provider.calls(), "boot replay must do no compute on an Interrupted parent")
	assert.Equal(t, before, h.journal(t))
	pending, _, _, drainErr := h.al.GetMessageInboxStore().Drain(h.id, "i1-child", "", 10)
	require.NoError(t, drainErr)
	require.Len(t, pending, 1, "report remains available for the next explicit/normal turn")
	entries, readErr := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, readErr)
	for _, e := range entries {
		assert.NotEqual(t, "consumed "+id, e.Content, "held report cannot be marked consumed")
	}
}
