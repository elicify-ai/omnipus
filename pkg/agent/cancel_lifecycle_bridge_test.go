// Package agent — cancel_lifecycle_bridge_test.go
//
// Frozen control-plane ADR D2/D4 and the one-stop decision: the admitted
// execution owns stopped settlement; coarse session status stays active.
// RequestCancel itself no longer writes another execution's lifecycle.
package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestStopSession_TransitionsLifecycleRecordAfterOwnerTail replaces the
// deleted RequestCancel mediator writer. Frozen D2 and the one-stop decision:
// Stop accepts a fence/note; only the real admitted owner lands stopped after
// its provider/output tail retires, retaining that note and clearing the fence.
func TestStopSession_TransitionsLifecycleRecordAfterOwnerTail(t *testing.T) {
	al, _, child, handle, provider := oneStopUncooperativeChild(t, "lifecycle-bridge")
	res, err := al.StopSession(context.Background(), StopRequest{
		SessionID: child.SessionID,
		By:        steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"},
		Channel:   "web",
	})
	require.NoError(t, err)
	require.NoError(t, res.RootErr)
	require.True(t, res.Durable, "the one Stop must accept a durable lifecycle fence")
	require.True(t, res.Fired, "the selected real execution must receive the Stop")
	require.Empty(t, res.StillRunning(), "no reached session may report an unreachable Stop")
	select {
	case <-provider.softCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the Stop never reached the owning provider's cancellation boundary")
	}

	// The external request cannot return until released: this is a real tail,
	// not a synthetic Finish() that gives an unowned writer permission to land.
	held := rootReopenedRecord(t, al, child.SessionID)
	require.True(t, handle.IsAlive(), "the real owner must still be live while its provider is held")
	require.Equal(t, session.LifecycleRunning, held.State, "D2: stopped must not land before the owning tail")
	require.NotNil(t, held.Stop, "D2: the current fence remains in flight until owner settlement")
	require.Equal(t, child.Generation, held.Stop.Generation)
	require.NotNil(t, held.StopNote, "D2: the lasting reason is accepted with the fence")
	require.Equal(t, session.StopCauseStop, held.StopNote.Cause)
	require.Equal(t, "human:tester", held.StopNote.By)
	require.False(t, held.StopNote.At.IsZero())
	require.Positive(t, held.StopNote.Seq)

	provider.open()
	joinGoalFixtureRuns(t, al)
	rec := rootReopenedRecord(t, al, child.SessionID)
	require.Equal(t, session.LifecycleStopped, rec.State, "the real owner must land stopped after its joined tail")
	require.False(t, rec.Terminal(), "Stop is resumable, never a terminal cancel")
	require.Equal(t, child.Generation, rec.Generation, "Stop never mints a generation")
	require.Nil(t, rec.Stop, "D2: the owner landing clears the in-flight fence")
	require.NotNil(t, rec.StopNote, "D2: the owner landing keeps the lasting note")
	assert.Equal(t, *held.StopNote, *rec.StopNote, "D2: landing keeps the original cause, actor, time and sequence")
	postMeta, err := al.GetSessionStore().GetMeta(child.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.StatusActive, postMeta.Status,
		"UnifiedMeta stays coarse-active when the owner's lifecycle lands stopped (ADR D4)")
}

// TestRequestCancel_LifecycleRecordMissing_DoesNotPanic verifies the cancel
// path tolerates a session with NO LifecycleRecord (a plain chat session that
// was never dispatched as a task/delegate). The mediator returns
// ErrLifecycleNotFound, the cancel path silences it, and UnifiedMeta is still
// mirrored. This is the normal web-chat cancel case.
func TestRequestCancel_LifecycleRecordMissing_DoesNotPanic(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "bridge-test-model-2"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	ls := session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle"))
	al.SetSessionMessagingStores(nil, ls)

	store := al.GetSessionStore()
	require.NotNil(t, store)

	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	// NOTE: no LifecycleRecord minted — this is a plain chat session.

	ts := &turnState{
		turnID:              "turn-bridge-002",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: see the identical note in
		// TestStopSession_TransitionsLifecycleRecordAfterOwnerTail above.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
		transcriptStore:  store,
	}
	ts.providerCancel = func() { ts.Finish(false) }
	al.activeTurnStates.Store(sessionID, ts)
	defer al.activeTurnStates.Delete(sessionID)

	_, err = al.RequestCancel(context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "tester", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err, "cancel must not error when no LifecycleRecord exists")

	// UnifiedMeta stays coarse-active (the mediator proceeds past
	// ErrLifecycleNotFound, but LifecycleStopped no longer mirrors at all —
	// ADR D4/MAJ-009).
	postMeta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.StatusActive, postMeta.Status,
		"UnifiedMeta must stay active even without a LifecycleRecord (ADR D4)")

	// qa-lead note (issue #1161, LEFT PARTIALLY STRENGTHENED — needs a
	// design decision, not guessed): the Status assertion above is still
	// vacuous against full-deletion of TransitionSession/the mediator call.
	// Unlike TestStopSession_TransitionsLifecycleRecordAfterOwnerTail above,
	// this scenario has NO LifecycleRecord to pair against (that absence is
	// the whole point of this test), and this call site deliberately passes
	// CancelHooks{} with no SetSessionInterrupted hook (see its own doc
	// comment: "exercises the mediator path"), so there is also no explicit
	// converge-write to seed-and-observe the way
	// TestCancel_AbandonedAfterHardTimeout and
	// TestConsumeTaskAttempt_SupersededSessionStaysActive do. Traced:
	// ls.Mutate(sessionID, fn) with fn returning ErrLifecycleNotFound makes
	// ZERO disk writes (pkg/session/lifecycle.go::(*LifecycleStore).Mutate
	// returns before persistLocked when next==nil is never set — rec stays
	// nil, fn returns the error, Mutate returns immediately), and that error
	// is silenced by cancel.go's caller (errors.Is check) without even a
	// Warn log — so nothing observable differs between "the mediator ran
	// and correctly no-opped" and "the mediator's call site was deleted."
	// Closing this needs a production seam (e.g. an injectable spy/counter
	// on the mediator call, or a hook fired unconditionally) — a design
	// decision for backend-lead/architect, not a test-file workaround.
	// The one real (if narrow) thing this test CAN still assert: a failed
	// Mutate must not have fabricated a phantom record.
	_, loadErr := ls.Load(sessionID)
	require.ErrorIs(t, loadErr, session.ErrLifecycleNotFound,
		"a cancel on a session with no LifecycleRecord must not create one as a side effect")
}
