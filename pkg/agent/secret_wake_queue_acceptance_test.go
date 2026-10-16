package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

// Chief/architect 2026-10-16: capacity acceptance is NOT consumption. This
// drives the production helper consumer and promotion, not ClaimResume itself.
// Already-fixed behaviour: red-first UNVERIFIED; mutations belong to fresh CHECK.
func TestSecretWake_QueuedHelperClaimsOnlyAfterAdmittedPromotion(t *testing.T) {
	f := newSecretWakeAcceptanceFixture(t, true, false)
	busy := f.launch(t, "secret-capacity-blocker")
	child := f.launch(t, "secret-capacity-recipient")
	admitted, err := NewSteerLauncher(f.al).Dispatch(context.Background(), busy.SessionID, busy.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, admitted.State)
	awaitSecretWakeSignal(t, f.provider.entered, "real busy provider")
	require.Equal(t, 1, f.al.steerAdmission().activeCount(), "one actual turn must occupy the only slot")
	submitted, wake := f.submit(t, f.pending(t, child))
	_, _, err = f.al.processMessage(context.Background(), wake)
	require.NoError(t, err, "actual helper continuation intake must queue at capacity")
	queued, err := f.al.GetSessionLifecycleStore().Load(child.SessionID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, queued.State)
	require.Nil(t, f.al.getActiveTurnState(child.SessionID), "queued input has no admitted helper turn")
	require.Equal(t, 1, f.al.steerAdmission().queueLen())
	require.Empty(t, f.provider.calls(child.SessionID), "queue acceptance must not call the helper model")
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted), "ZERO ClaimResume publications before promotion")
	// A repeated accepted wake retains one queue item/id without consuming it.
	_, _, err = f.al.processMessage(context.Background(), wake)
	require.NoError(t, err)
	require.Equal(t, 1, f.al.steerAdmission().queueLen())
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted))
	f.provider.open()
	joinGoalFixtureRuns(t, f.al) // Join actual completion AND queue promotion tail.
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted), "EXACTLY ONE durable claim after admitted consumption")
	calls := f.provider.calls(child.SessionID)
	require.Len(t, calls, 1, "queued promotion must execute exactly one helper continuation")
	requireSecretWakePrompt(t, calls[0], submitted)
	require.Len(t, f.provider.calls(busy.SessionID), 1)
	require.Zero(t, f.al.steerAdmission().activeCount())
	require.Zero(t, f.al.steerAdmission().queueLen())
	got, err := f.al.SecretRequests().Get(submitted.Card.RequestId, submitted.Owner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State, "consumption does not invent a new wire state")
}
