package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/require"
)

// A real helper asks and parks through the registered tool. Submit commits
// normally, but this fixture withholds the bus message as a process crash can.
func parkedSubmittedSecretWake(t *testing.T) (*secretWakeAcceptanceFixture, secretrequest.Record) {
	t.Helper()
	f := newSecretWakeAcceptanceFixture(t, false, true)
	cards := make(chan secretrequest.Record, 1)
	f.al.SecretRequests().SetSink(func(rec secretrequest.Record) {
		if rec.Card.State == "pending" {
			cards <- rec
		}
	})
	child := f.launch(t, "secret-restart-helper")
	dispatch, err := NewSteerLauncher(f.al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatch.State)
	var pending secretrequest.Record
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	select {
	case pending = <-cards:
	case <-deadline.C:
		t.Fatal("helper did not raise its pending secret card")
	}
	joinGoalFixtureRuns(t, f.al)
	require.Equal(t, child.SessionID, pending.Card.SessionId)
	require.Len(t, f.provider.calls(child.SessionID), 1)
	submitted, _ := f.submit(t, pending) // Original accepted message is not consumed.
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted))
	return f, submitted
}

// FR-087 / chief restart exception: a durable unconsumed receipt may continue
// through REAL boot recovery and revival, and a later boot never consumes twice.
func TestSecretWake_RestartStopRevivesUnconsumedReceiptOnlyOnceAcrossBoots(t *testing.T) {
	f, submitted := parkedSubmittedSecretWake(t)
	f.boot(t)
	interrupted, err := f.al.GetSessionLifecycleStore().Load(submitted.Card.SessionId)
	require.NoError(t, err)
	require.True(t, interrupted.Stopped())
	require.NotNil(t, interrupted.StopNote)
	require.Equal(t, session.StopCauseRestart, interrupted.StopNote.Cause)
	reg := f.freshRegistry(t)
	require.NoError(t, reg.RetryResumes())
	wake := f.takeWake(t, submitted.Card.RequestId)
	_, _, err = f.al.processMessage(context.Background(), wake)
	require.NoError(t, err, "restart-only Stop must permit the admitted secret revival path")
	joinGoalFixtureRuns(t, f.al)
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted))
	calls := f.provider.calls(submitted.Card.SessionId)
	require.Len(t, calls, 2, "one original parked request plus one secret continuation")
	requireSecretWakePrompt(t, calls[1], submitted)
	resumed, err := f.al.GetSessionLifecycleStore().Load(submitted.Card.SessionId)
	require.NoError(t, err)
	require.False(t, resumed.Stopped(), "actual revival must clear only the restart interruption")
	require.NotNil(t, resumed.ExecutionID)
	require.Equal(t, f.al.bootEpochFor(), resumed.ExecutionID.BootSeq)
	// New persisted boot and a fresh real registry, not a warm inFlight cache.
	f.boot(t)
	reg = f.freshRegistry(t)
	require.NoError(t, reg.RetryResumes())
	f.requireNoSecretWake(t, submitted.Card.RequestId)
	require.Len(t, f.provider.calls(submitted.Card.SessionId), 2, "second boot must not inject a consumed receipt")
	require.Equal(t, 1, secretWakeClaimCount(t, f.al, submitted))
	got, err := reg.Get(submitted.Card.RequestId, submitted.Owner)
	require.NoError(t, err)
	require.Equal(t, "submitted", got.Card.State)
}

// FR-067 / chief explicit-Stop ruling: a saved secret is not new human input.
// Clearing the session's Stop later must not resurrect the old continuation.
func TestSecretWake_ExplicitHumanOrToolStopVetoSurvivesRevivalAndBoot(t *testing.T) {
	for _, actor := range []steer.Principal{{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"}, {Kind: steer.PrincipalKindAgent, ID: "omnipus"}} {
		t.Run(string(actor.Kind), func(t *testing.T) {
			f, submitted := parkedSubmittedSecretWake(t)
			result, err := f.al.StopSession(context.Background(), StopRequest{SessionID: submitted.Card.SessionId, By: actor, Channel: "webchat"})
			require.NoError(t, err)
			require.NoError(t, result.RootErr)
			require.Empty(t, result.Report.Unreachable)
			stopped, err := f.al.GetSessionLifecycleStore().Load(submitted.Card.SessionId)
			require.NoError(t, err)
			require.True(t, stopped.Stopped())
			require.NotNil(t, stopped.StopNote)
			require.Equal(t, session.StopCauseStop, stopped.StopNote.Cause)
			f.boot(t)
			reg := f.freshRegistry(t)
			require.NoError(t, reg.RetryResumes())
			f.requireNoSecretWake(t, submitted.Card.RequestId)
			require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted))
			// Real public revival clears the session Stop; the separate durable
			// request fence must still win when the ordinary stopped check cannot.
			_, err = f.al.steerCanceller().Revive(context.Background(), submitted.Card.SessionId, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"})
			require.NoError(t, err)
			revived, err := f.al.GetSessionLifecycleStore().Load(submitted.Card.SessionId)
			require.NoError(t, err)
			require.False(t, revived.Stopped(), "positive control: the user really revived the session")
			reg = f.freshRegistry(t)
			require.NoError(t, reg.RetryResumes())
			f.requireNoSecretWake(t, submitted.Card.RequestId)
			require.Len(t, f.provider.calls(submitted.Card.SessionId), 1, "revival must not run the old secret continuation")
			require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted))
			got, err := reg.Get(submitted.Card.RequestId, submitted.Owner)
			require.NoError(t, err)
			require.Equal(t, "submitted", got.Card.State, "Stop never rolls back saved references")
			require.Equal(t, submitted.Card.ReferenceNames, got.Card.ReferenceNames)
			for _, ref := range got.Card.ReferenceNames {
				value, readErr := f.store.Get(ref)
				require.NoError(t, readErr)
				require.Equal(t, secretWakeAcceptanceValue, value)
			}
		})
	}
}
