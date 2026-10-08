package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R3: losing an execution-identity race is not a storage/revival failure and
// cannot poison the next stopped-parent refusal with the failure sentinel.
func TestI1R3StaleRevivalHasHonestAttribution(t *testing.T) {
	h := newD2bRoot(t)
	boot := h.al.bootEpochFor()
	ls := h.al.GetSessionLifecycleStore()
	require.NoError(t, ls.Persist(&session.LifecycleRecord{SessionID: h.id, Generation: 1, State: session.LifecycleStopped, OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: testHarnessWorkspaceMembershipID, AgentID: testDefaultAgentID,
		Origin: &session.Origin{Kind: session.OriginKindChat}, ExecutionID: &session.ExecutionIdentity{RunID: "selected-old", BootSeq: boot}, StopNote: &session.StopNote{At: time.Now().UTC(), By: session.StopActorRestart, Cause: session.StopCauseRestart, BootSeq: boot, Seq: 1}}))
	selected, err := ls.Load(h.id)
	require.NoError(t, err)
	require.NoError(t, ls.Mutate(h.id, func(rec *session.LifecycleRecord) error {
		rec.ExecutionID = &session.ExecutionIdentity{RunID: "newer-owner", BootSeq: boot}
		return nil
	}))
	before := h.journal(t)
	err = h.al.reviveOrdinaryRecordWithExecution(context.Background(), selected, session.ExecutionIdentity{RunID: "losing-attempt", BootSeq: boot}, steer.Principal{})
	require.ErrorIs(t, err, steer.ErrStaleGeneration)
	assert.Equal(t, before, h.journal(t), "stale snapshot cannot overwrite the newer owner")
	_, remembered := h.al.lastRevivalFailure(h.id)
	assert.False(t, remembered, "a race must not be recorded as a genuine failed resume")
	assert.Equal(t, previousReplyStillFinishingMessage, userVisibleTurnError(err), "use the existing concurrent-admission guidance")
	_, launchErr := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: h.id, TargetAgentID: testDefaultAgentID, Task: "Do not invent a storage failure", Origin: steer.Origin{Kind: steer.OriginKindDelegate}})
	assert.ErrorIs(t, launchErr, steer.ErrSteeringStopped)
	assert.False(t, errors.Is(launchErr, steer.ErrSteeringRevivalFailed), "later delegate refusal must not claim revival failed")

	// Healthy failure-memory control: real storage failures still retain their
	// diagnostic and cannot be silently demoted by the stale-race exception.
	storeErr := errors.New("fixture storage refusal")
	h.al.markRevivalFailure(h.id, storeErr)
	failure, ok := h.al.lastRevivalFailure(h.id)
	require.True(t, ok)
	assert.Equal(t, storeErr, failure.cause)
}
