package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unlike a fabricated submitted record, this fixture runs the real registered
// request_secret tool in a helper and retains the actual original bus wake.
func wu4P0CParkedWithheldWake(t *testing.T) (*secretWakeAcceptanceFixture, secretrequest.Record, bus.InboundMessage) {
	t.Helper()
	f := newSecretWakeAcceptanceFixture(t, false, true)
	cards := make(chan secretrequest.Record, 1)
	f.al.SecretRequests().SetSink(func(rec secretrequest.Record) {
		if rec.Card.State == "pending" {
			cards <- rec
		}
	})
	child := f.launch(t, "p0c-withheld-secret-helper")
	dispatch, err := NewSteerLauncher(f.al).Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatch.State)
	var pending secretrequest.Record
	select {
	case pending = <-cards:
	case <-time.After(10 * time.Second):
		t.Fatal("instrument: the actual helper did not raise its secure card")
	}
	joinGoalFixtureRuns(t, f.al)
	require.Equal(t, child.SessionID, pending.Card.SessionId)
	require.Len(t, f.provider.calls(child.SessionID), 1, "one original parked turn before submission")
	submitted, wake := f.submit(t, pending)
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted), "the original wake is withheld, not consumed")
	return f, submitted, wake
}

func wu4P0CRequireRestartLanding(t *testing.T, f *secretWakeAcceptanceFixture, submitted secretrequest.Record) {
	t.Helper()
	f.boot(t)
	landed, err := f.al.GetSessionLifecycleStore().Load(submitted.Card.SessionId)
	require.NoError(t, err)
	require.True(t, landed.Stopped(), "instrument: real boot must land its interruption before the explicit Stop")
	require.NotNil(t, landed.StopNote)
	require.Equal(t, session.StopCauseRestart, landed.StopNote.Cause)
	require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted))
	require.Len(t, f.provider.calls(submitted.Card.SessionId), 1)
}

func wu4P0CExplicitStop(t *testing.T, f *secretWakeAcceptanceFixture, submitted secretrequest.Record, actor steer.Principal) {
	t.Helper()
	result, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: submitted.Card.SessionId, By: actor, Channel: "webchat",
	})
	require.NoError(t, err)
	require.NoError(t, result.RootErr)
	require.Empty(t, result.Report.Unreachable)
	require.True(t, result.Durable, "the explicit Stop must be accepted durably even if restart already landed")
	wu4P0RequireSavedReferences(t, f, submitted)
}

func wu4P0CDrainRequestWakes(f *secretWakeAcceptanceFixture, requestID string) []bus.InboundMessage {
	var wakes []bus.InboundMessage
	for {
		select {
		case wake := <-f.bus.InboundChan():
			if wake.Metadata[secretResumeMetadataKey] == requestID {
				wakes = append(wakes, wake)
			}
		default:
			return wakes
		}
	}
}

func wu4P0CRequireNoContinuation(t *testing.T, f *secretWakeAcceptanceFixture, submitted secretrequest.Record) {
	t.Helper()
	assert.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted), "P0-c: a later explicit Stop must veto ClaimResume despite the older restart landing")
	assert.Len(t, f.provider.calls(submitted.Card.SessionId), 1, "P0-c: only the original parked turn may exist; the stopped secret wake must run no continuation")
	wu4P0RequireSavedReferences(t, f, submitted)
}

func wu4P0CRequireStopOrdering(t *testing.T, queueBeforeStop bool, actor steer.Principal) {
	t.Helper()
	f, submitted, originalWake := wu4P0CParkedWithheldWake(t)
	var diagnostics []string
	defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
	wu4P0CRequireRestartLanding(t, f, submitted)
	wakeToProcess := originalWake
	if queueBeforeStop {
		reg := f.freshRegistry(t)
		require.NoError(t, reg.RetryResumes())
		wakeToProcess = f.takeWake(t, submitted.Card.RequestId)
		require.Equal(t, "system", wakeToProcess.Channel)
		require.Equal(t, 0, secretWakeClaimCount(t, f.al, submitted), "queue publication is not consumption")
	}
	wu4P0CExplicitStop(t, f, submitted, actor)
	// The fence must survive real metadata reload, not just a warm registry.
	reg := f.freshRegistry(t)
	retryErr := reg.RetryResumes()
	if retryErr != nil {
		diagnostics = append(diagnostics, retryErr.Error())
	}
	assert.NoError(t, retryErr, "Stop should hold an old committed receipt, not turn attach retry into a store error")
	afterStopWakes := wu4P0CDrainRequestWakes(f, submitted.Card.RequestId)
	assert.Empty(t, afterStopWakes, "P0-c: explicit Stop after restart must prevent re-dispatch of this saved request")
	// Drive the actual previously accepted wake even if RetryResumes correctly
	// publishes nothing. A stale in-flight bus item must be refused at use.
	_ = wu4P0ProcessWake(t, f, wakeToProcess, &diagnostics)
	wu4P0CRequireNoContinuation(t, f, submitted)
}

func TestWU4P0CExplicitStopAfterRestartVetoesWithheldSecretWake(t *testing.T) {
	for _, actor := range []steer.Principal{
		{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"},
		{Kind: steer.PrincipalKindAgent, ID: "omnipus"},
	} {
		t.Run(string(actor.Kind), func(t *testing.T) { wu4P0CRequireStopOrdering(t, false, actor) })
	}
}

func TestWU4P0CExplicitStopAfterRecoveredWakeQueuedVetoesConsumption(t *testing.T) {
	for _, actor := range []steer.Principal{
		{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"},
		{Kind: steer.PrincipalKindAgent, ID: "omnipus"},
	} {
		t.Run(string(actor.Kind), func(t *testing.T) { wu4P0CRequireStopOrdering(t, true, actor) })
	}
}

func TestWU4P0CPostRestartStopVetoSurvivesRevivalAndAnotherBoot(t *testing.T) {
	for _, actor := range []steer.Principal{
		{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"},
		{Kind: steer.PrincipalKindAgent, ID: "omnipus"},
	} {
		t.Run(string(actor.Kind), func(t *testing.T) {
			f, submitted, _ := wu4P0CParkedWithheldWake(t)
			var diagnostics []string
			defer wu4P0CaptureResumeLogs(t, f, &diagnostics)()
			wu4P0CRequireRestartLanding(t, f, submitted)
			wu4P0CExplicitStop(t, f, submitted, actor)
			_, err := f.al.steerCanceller().Revive(context.Background(), submitted.Card.SessionId,
				steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-secret-wake-owner"})
			require.NoError(t, err)
			revived, err := f.al.GetSessionLifecycleStore().Load(submitted.Card.SessionId)
			require.NoError(t, err)
			require.False(t, revived.Stopped(), "instrument: public revival really clears the session Stop")
			// A new real boot epoch and registry discard both lifecycle and
			// request-process caches as authorities for the old explicit veto.
			f.boot(t)
			reg := f.freshRegistry(t)
			require.NoError(t, reg.RetryResumes())
			wakes := wu4P0CDrainRequestWakes(f, submitted.Card.RequestId)
			assert.Empty(t, wakes, "P0-c: the old explicit Stop must survive revival, another boot and request reload")
			for _, wake := range wakes {
				_ = wu4P0ProcessWake(t, f, wake, &diagnostics)
			}
			wu4P0CRequireNoContinuation(t, f, submitted)
		})
	}
}
