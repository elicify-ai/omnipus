package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Item 2's real duplicate is a parent-notice admission made after shutdown has
// already cancelled its context. It must not revive the completed root or take
// the report without a turn that can start. No return value is mocked.
func TestI1CancelledParentWakeKeepsRootAndInbox(t *testing.T) {
	h := newD2bRoot(t)
	h.seedState(t, session.LifecycleRunning)
	ls := h.al.GetSessionLifecycleStore()
	require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
		rec.ExecutionID = &session.ExecutionIdentity{RunID: "i1-finished-human", BootSeq: h.al.bootEpochFor()}
		rec.State = session.LifecycleCompleted
		return nil
	}))
	const messageID = "i1-stopped-child:1:notice"
	message := bootHandback(t, "i1-stopped-child", h.id, messageID)
	stored, err := h.al.GetMessageInboxStore().Append(h.id, message)
	require.NoError(t, err)
	require.True(t, stored.Accepted)
	before := h.load(t)
	journal := h.journal(t)
	transcript, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, wakeErr := h.al.processSteeredSystemWake(ctx, bus.InboundMessage{Channel: "system",
		AsyncTranscriptSessionID: h.id, Content: "The helper stopped.",
		Metadata: map[string]string{"steer_message_id": messageID, "steer_generation": "1"}})
	require.ErrorIs(t, wakeErr, context.Canceled)
	assert.Empty(t, response)
	assert.Empty(t, h.provider.calls(), "cancelled context cannot start a provider turn")
	assert.Equal(t, before, h.load(t), "no new phantom root execution/generation may be admitted")
	assert.Equal(t, journal, h.journal(t), "no running/stopped/failure line may be written for an unstarted turn")
	afterTranscript, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	assert.Equal(t, transcript, afterTranscript, "cancelled wake cannot write a consumed marker")
	pending, _, _, err := h.al.GetMessageInboxStore().Drain(h.id, "i1-stopped-child", "", 10)
	require.NoError(t, err)
	require.Len(t, pending, 1, "unstarted notice stays pending for a later permitted execution")
}
