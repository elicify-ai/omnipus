package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// Build the real source categories, not a fabricated inbox lookup result:
// synthetic goal UUID, durable handback plus actual parent notifier, or an
// unrelated durable entry that must not make the goal wake look retained.
func i1R5WakeForRetention(t *testing.T, h *d2bRoot, backing string) bus.InboundMessage {
	t.Helper()
	ls := h.al.GetSessionLifecycleStore()
	require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
		rec.State, rec.StopNote, rec.GoalRef = session.LifecycleRunning, nil, "i1-r5-goal"
		return nil
	}))
	if backing == "matching_entry" {
		const id = "i1-r5-child:1:final"
		message := bootHandback(t, "i1-r5-child", h.id, id)
		stored, err := h.al.GetMessageInboxStore().Append(h.id, message)
		require.NoError(t, err)
		require.True(t, stored.Accepted)
		require.NoError(t, h.al.asyncNotifier.WakeParentAlways(context.Background(), "handback", tools.MessageParentWakeEvent{
			Channel: "webchat", ChatID: h.id, AgentID: testDefaultAgentID,
			TranscriptSessionID: h.id, Content: deliverySummary(message), MessageID: id, Generation: 1,
		}))
	} else {
		inst, ok := h.al.GetRegistry().GetAgent(testDefaultAgentID)
		require.True(t, ok)
		const content = "Private goal follow-up content must not appear in the warning."
		ts := &turnState{agent: inst, agentID: testDefaultAgentID,
			opts: processOptions{TranscriptSessionID: h.id, TranscriptStore: h.al.GetSessionStore()}}
		result := turnResult{followUps: []bus.InboundMessage{{Channel: "system", ChatID: "steer:" + h.id,
			SessionID: h.id, Content: content, Sender: bus.SenderInfo{CanonicalID: goalLoopFollowUpSenderID}}}}
		h.al.finishSteeredGoalTurn(ts, h.load(t), &result, nil)
		if backing == "unrelated_entry" {
			message := bootHandback(t, "i1-r5-unrelated", h.id, "i1-r5-unrelated:1:final")
			stored, err := h.al.GetMessageInboxStore().Append(h.id, message)
			require.NoError(t, err)
			require.True(t, stored.Accepted)
		}
	}
	select {
	case wake := <-h.al.bus.InboundChan():
		require.Equal(t, h.id, wake.AsyncTranscriptSessionID)
		require.NotEmpty(t, wake.Content)
		require.NotEmpty(t, wake.Metadata["steer_message_id"])
		if backing == "matching_entry" {
			require.Equal(t, "i1-r5-child:1:final", wake.Metadata["steer_message_id"])
			require.Equal(t, "async:message_parent:handback", wake.Sender.CanonicalID)
		} else {
			require.Equal(t, "Private goal follow-up content must not appear in the warning.", wake.Content)
			require.Equal(t, goalLoopFollowUpSenderID, wake.Sender.CanonicalID)
		}
		return wake
	case <-time.After(5 * time.Second):
		t.Fatal("actual publisher must produce the wake under test")
		return bus.InboundMessage{}
	}
}
