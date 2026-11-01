package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// S-04 coverage repair: a genuine delegated child executes, finishes and publishes to
// the actual parent's durable inbox. A returned root-turn string is not used
// as a stand-in for parent delivery.
func TestT_C3_S04_RealDelegatedParentReceivesEmptyAnswerSentinel(t *testing.T) {
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parent := newTestSteeringSession(t, al, "effort-parent-workspace")
	p := &effortScriptedProvider{reasoning: redMarker + " private child reasoning", finish: "stop"}
	ag, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	ag.Provider = p
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent, TargetAgentID: testDefaultAgentID, Task: "produce delegated answer", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "effort-parent-call-7731"}})
	require.NoError(t, err)
	admitted, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, admitted.State)
	waitForCond(t, 5*time.Second, "actual child final publication", func() bool {
		rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
		return err == nil && rec.Terminal() && rec.FinalDelivery != nil
	})
	msgs, _, more, err := al.GetMessageInboxStore().Drain(parent, child.SessionID, "", 10)
	require.NoError(t, err)
	require.False(t, more)
	require.Len(t, msgs, 1, "one final handback, not a fabricated root response")
	result, err := msgs[0].AsSessionMessageHandback()
	require.NoError(t, err)
	assert.Equal(t, redDefaultResponse, result.ResultSoFar)
	assert.NotContains(t, result.ResultSoFar, redMarker)
	assert.Equal(t, 2, p.callCount(), "empty non-truncated child retries exactly once")
	entries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
	require.NoError(t, err)
	answer := ""
	for _, entry := range entries {
		if entry.Role == "assistant" {
			answer = entry.Content
		}
	}
	assert.Equal(t, redDefaultResponse, answer, "actual child transcript has the same sentinel")
}
