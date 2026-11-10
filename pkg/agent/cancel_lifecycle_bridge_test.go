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
	// Not load-bearing on its own (#1161): every session starts active, so this
	// line cannot fail if the landing code is deleted. The rec.State/StopNote
	// assertions above are the load-bearing proof. It is kept because it does
	// fail if a landing ever starts mirroring a distinct status (the retired
	// interrupted-mirror).
	assert.Equal(t, session.StatusActive, postMeta.Status,
		"UnifiedMeta stays coarse-active when the owner's lifecycle lands stopped (ADR D4)")
}

// TestRequestCancel_LifecycleRecordMissing_DoesNotPanic verifies the cancel
// path tolerates a session with NO LifecycleRecord (a plain chat session that
// was never dispatched as a task/delegate) — the normal web-chat cancel case.
// RequestCancel only asks turns to stop and writes no lifecycle state and no
// session status (cancel.go: "No lifecycle write here"; the durable stop is
// StopSession's, landed by the owning execution), so with no record there is
// nothing for it to create, mutate or mirror.
//
// Issue #1161: a bare `Status == StatusActive` assertion could not fail here,
// because every session starts active. The session is therefore seeded to a
// DIFFERENT status first; the cancel must leave it exactly as seeded. That
// assertion fails if the cancel path ever starts writing UnifiedMeta.Status
// (the retired interrupted-mirror, or any converge-write to active) — and the
// no-phantom-record and no-error checks fail if it starts touching or
// surfacing the lifecycle store for a record-less session.
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

	// Seed a status that is NOT the new-session default, so "unchanged after
	// the cancel" is a claim the assertion below can actually falsify.
	seeded := session.StatusFailed
	require.NoError(t, store.SetMeta(sessionID, session.MetaPatch{Status: &seeded}))

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

	postMeta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, seeded, postMeta.Status,
		"RequestCancel must not write UnifiedMeta.Status for a record-less session (ADR D4: a cancel never moves the coarse status)")

	_, loadErr := ls.Load(sessionID)
	require.ErrorIs(t, loadErr, session.ErrLifecycleNotFound,
		"a cancel on a session with no LifecycleRecord must not create one as a side effect")
}
