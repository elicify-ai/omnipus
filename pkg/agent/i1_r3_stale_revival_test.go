package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// R3: losing an execution-identity race is not a storage/revival failure.
//
// SPEC CHANGE — ADR-093 amendment 2026-10-23: the revival-failure ledger
// (recordRevivalFailure / lastRevivalFailure) "loses its only production
// reader and is deleted with the backstop", and D2's launch refusal (whose
// revival-failed variant this test's Launch assertions pinned) is removed.
// OLD: the race must not poison the next stopped-parent refusal, and a later
// Launch under the raced parent was still refused ErrSteeringStopped (never
// ErrSteeringRevivalFailed). NEW: the stale race keeps its honest
// attribution (ErrStaleGeneration, journal untouched, honest user-visible
// error) and the later Launch PUBLISHES the child (Decision 1) — there is no
// refusal left to poison. The ledger memory assertions are dropped with the
// deleted symbols.
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
	assert.Equal(t, previousReplyStillFinishingMessage, userVisibleTurnError(err), "use the existing concurrent-admission guidance")

	// And the launch after the race publishes the child (amendment 2026-10-23
	// Decision 1: D2's backstop — refused-launch/revival-failed variant — is
	// gone; the old assertion that it still refused ErrSteeringStopped is
	// replaced by the publish outcome).
	res, launchErr := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: h.id, TargetAgentID: testDefaultAgentID, Task: "Delegate after the race", Origin: steer.Origin{Kind: steer.OriginKindDelegate}})
	if launchErr != nil {
		t.Fatalf("Launch after the stale-selection race = %v — ADR-093 amendment 2026-10-23 Decision 1: the launch publishes the child (old spec: ErrSteeringStopped refusal)", launchErr)
	}
	child, err := ls.Load(res.SessionID)
	require.NoError(t, err, "child lifecycle record after the post-race launch")
	require.Falsef(t, child.Stopped(), "child must not be stamped at launch (Q1 option A): state=%q stop=%+v", child.State, child.Stop)
}
