package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R6 successor for the two retired engine-compile anchor tests. The ordinary
// agent set_goal anchor tests in goal_record_anchor_test.go are unchanged.
// Real reminders finish; a timer may publish the system pause, never invent
// an assistant set_goal registration or fallback narration.
func grRecordlessPauseAnchorContract(t *testing.T, channel bool) {
	h := grNewRecordlessBrake(t)
	if channel {
		h.al.recordGoalRouting(h.id, h.goalID, "telegram", "r6-no-compile-channel", "agent:"+testDefaultAgentID+":session:"+h.id, testDefaultAgentID)
	}
	before, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	for n := 0; n < 5; n++ {
		if n > 0 {
			h.age(t)
		}
		grTextReminder(t, h, h.reminder(t))
		require.Empty(t, goalRecordForSession(t, h.id).Criteria, "no timer registration before the unified fifth-outcome brake")
	}
	h.age(t)
	grAssertFifthPause(t, h)
	after, err := h.al.GetSessionStore().ReadTranscript(h.id)
	require.NoError(t, err)
	for _, entry := range after[len(before):] {
		for _, call := range entry.ToolCalls {
			assert.NotEqual(t, tools.SetGoalToolName, call.Tool, "keeper may not forge an assistant set_goal anchor")
		}
	}
	if channel {
		var pauseCount int
		for {
			select {
			case msg := <-h.al.bus.OutboundChan():
				assert.Equal(t, grPauseLine, msg.Content, "one visible pause replaces retired compile echo/narration")
				assert.Equal(t, "telegram", msg.Channel)
				assert.Equal(t, "r6-no-compile-channel", msg.ChatID)
				pauseCount++
			default:
				assert.Equal(t, 1, pauseCount)
				return
			}
		}
	}
}
