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

// CHECK R5's supplied held-out probe, using actual root/helper admission,
// lifecycle edges and StopSession. Only external provider I/O is controlled.
func Test1534Shown_StopAllCheckpointReachesOnlyRoot(t *testing.T) {
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 2
	al.GetConfig().Performance.MaxDelegationDepth = 1
	provider := &u15OwnerTailProvider{}
	for i := 0; i < 2; i++ {
		gate := &originalGraceProvider{entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{})}
		provider.gates = append(provider.gates, gate)
		t.Cleanup(gate.open)
	}
	instance, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok, "fixture's actual agent must be registered")
	instance.Provider = provider
	root := rootLaunchWithGoal(t, al)
	dispatchChild(t, al, root.SessionID, root.Generation, true)
	childID := ""
	for i, gate := range provider.gates {
		if i > 0 {
			child := launchGoalBearingChild(t, al, root.SessionID, "1534-checkpoint-helper", goalChildLaunchOptions{live: true})
			childID = child.SessionID
		}
		select {
		case <-gate.entered:
		case <-time.After(5 * time.Second):
			t.Fatalf("owner %d never entered its provider", i)
		}
	}
	rootTS := al.getActiveTurnState(root.SessionID)
	childTS := al.getActiveTurnState(childID)
	require.NotNil(t, rootTS, "the root must have a real live turn")
	require.NotNil(t, childTS, "the helper must have a real live turn")

	result, err := al.StopSession(context.Background(), StopRequest{
		SessionID: root.SessionID, Tree: true, Channel: "web",
		By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "checkpoint-owner"},
		// The design's empty-placeholder checkpoint: deliberately no IDs.
		Shown: &StopShown{CodePoints: 0},
	})
	require.NoError(t, err)
	require.NoError(t, result.RootErr)
	require.True(t, result.Fired)
	for i, gate := range provider.gates {
		select {
		case <-gate.softCancelled:
		case <-time.After(2 * time.Second):
			t.Fatalf("Stop all did not reach owner %d's provider cancellation", i)
		}
	}
	rootTS.cancelMu.Lock()
	rootCP := rootTS.stopCheckpoint
	rootTS.cancelMu.Unlock()
	childTS.cancelMu.Lock()
	childCP := childTS.stopCheckpoint
	childTS.cancelMu.Unlock()
	require.NotNil(t, rootCP, "the requested session's own turn must keep the screen checkpoint")
	assert.Equal(t, StopShown{CodePoints: 0}, rootCP.shown)
	assert.Nil(t, childCP, "a helper reached by Stop all gets no screen checkpoint")
	keep, limited := childTS.stopKeepFor("any-unsaved-helper-message")
	assert.False(t, limited, "the helper's unsaved answer must not be cut to the root's screen (keep=%d)", keep)
	for _, gate := range provider.gates {
		gate.open()
	}
	joinGoalFixtureRuns(t, al)
}

// CHECK R1/R3/R4 and StopRequest.Shown's design table: the fallback sites
// carry the root checkpoint, but continuing an accepted control carries none.
func Test1534Shown_StopSessionCheckpointSites(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		missingRecord, continuation bool
	}{
		{"no_lifecycle_store_forwards_shown", false, false},
		{"store_without_record_forwards_shown", true, false},
		{"accepted_control_continuation_omits_shown", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var al *AgentLoop
			var ts *turnState
			var sid string
			var intent *session.UnfinishedStopIntent
			var finish func()
			if tc.continuation {
				al, ts, sid, intent, finish = checkpoint1534AcceptedControlFixture(t)
			} else {
				al, ts, sid = checkpoint1534NativeCancelFixture(t)
				finish = func() { ts.Finish(false) }
				if tc.missingRecord {
					lifecycle := session.NewLifecycleStore(t.TempDir())
					al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
					_, err := lifecycle.Load(sid)
					require.ErrorIs(t, err, session.ErrLifecycleNotFound, "fixture must enter the missing-record fallback")
				} else {
					require.Nil(t, al.GetSessionLifecycleStore(), "fixture must enter the no-store fallback")
				}
			}
			id := ts.nextRoundMessageID()
			shown := &StopShown{TurnID: ts.turnID, MessageID: id, CodePoints: 2} // two visible code points
			req := StopRequest{SessionID: sid, Channel: "web", Continue: intent, Shown: shown,
				By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "checkpoint-owner"}}
			result, err := al.StopSession(context.Background(), req)
			require.NoError(t, err)
			require.NoError(t, result.RootErr)
			require.True(t, result.Root.Fired, "this real Stop must claim the registered native turn")
			assert.Equal(t, tc.continuation, result.Durable, "only the accepted-control case has a durable record")
			ts.cancelMu.Lock()
			checkpoint := ts.stopCheckpoint
			ts.cancelMu.Unlock()
			if tc.continuation {
				assert.Nil(t, req.rootShown(), "an accepted control owns no later client's screen")
				assert.Nil(t, checkpoint, "Continue must store no client checkpoint in the real turn")
				_, limited := ts.stopKeepFor(id)
				assert.False(t, limited, "Continue must use the receipt cut, not the supplied screen")
			} else {
				require.NotNil(t, checkpoint, "the fallback Stop must store the root's checkpoint")
				assert.Equal(t, *shown, checkpoint.shown, "fallback must forward every checkpoint field unchanged")
				keep, limited := ts.stopKeepFor(id)
				assert.Equal(t, 2, keep)
				assert.True(t, limited, "the forwarded checkpoint must actually limit this message")
			}
			finish()
		})
	}
}

// Establish a genuine accepted-but-unlanded control without a mock canceller
// or manufactured fence. AcceptStopControl writes the ledger; Continue's real
// ResumeAcceptedStop supplies the fence and fires the selected running turn.
func checkpoint1534AcceptedControlFixture(t *testing.T) (*AgentLoop, *turnState, string, *session.UnfinishedStopIntent, func()) {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	gate := &originalGraceProvider{entered: make(chan struct{}), softCancelled: make(chan struct{}), release: make(chan struct{})}
	instance, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)
	instance.Provider = gate
	t.Cleanup(gate.open)
	root := rootLaunchWithGoal(t, al)
	dispatchChild(t, al, root.SessionID, root.Generation, true)
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the real root did not enter its provider")
	}
	ts := al.getActiveTurnState(root.SessionID)
	require.NotNil(t, ts)
	lifecycle := al.GetSessionLifecycleStore()
	outcome, grant, generation, err := lifecycle.AcceptStopControl(root.SessionID,
		session.StopControlIntent{Cause: session.StopCauseStop, Actor: "human:checkpoint-owner"}, nil)
	require.NoError(t, err)
	require.Equal(t, session.StopAcceptGranted, outcome)
	require.Equal(t, root.Generation, generation)
	intents, err := lifecycle.UnfinishedStopIntents(root.SessionID)
	require.NoError(t, err)
	require.Len(t, intents, 1, "the continuation must come from the actual accepted-control ledger")
	require.Equal(t, grant.ControlID, intents[0].Selection.Effect.ControlID)
	return al, ts, root.SessionID, &intents[0], func() {
		gate.open()
		joinGoalFixtureRuns(t, al)
	}
}
