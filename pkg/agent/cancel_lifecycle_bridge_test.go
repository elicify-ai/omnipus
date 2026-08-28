// Package agent — cancel_lifecycle_bridge_test.go
//
// Defect #28 mutation tests: prove the cancel path transitions BOTH stores
// (LifecycleRecord → cancelled, UnifiedMeta → interrupted) via the single
// TransitionSession mediator, closing the orphan that the pre-fix cancel path
// produced by writing UnifiedMeta alone.
package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestRequestCancel_TransitionsLifecycleRecordToCancelled is the GREEN test for
// Defect #28: a cancel on a session that HAS a LifecycleRecord must transition
// that record to LifecycleCancelled — not orphan it. Before the fix, the cancel
// path wrote ONLY UnifiedMeta (interrupted), leaving the LifecycleRecord at
// running/queued until a future boot sweep caught it.
func TestRequestCancel_TransitionsLifecycleRecordToCancelled(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "bridge-test-model"},
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

	// Wire a durable LifecycleStore the same way the gateway boot seam does.
	ls := session.NewLifecycleStore(filepath.Join(tmpDir, "session_lifecycle"))
	al.SetSessionMessagingStores(nil, ls)

	store := al.GetSessionStore()
	require.NotNil(t, store)

	// Create a real chat session.
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	// Mint a LifecycleRecord (running) for this session — mirrors what a
	// task/delegate dispatch would have on disk at cancel time.
	require.NoError(t, ls.Persist(&session.LifecycleRecord{
		SessionID:      sessionID,
		Generation:     0,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		AgentID:        "main",
	}))

	// Register a synthetic root turnState so GetActiveTurnHookForSession finds
	// it and ClaimCancel succeeds (the cancel cascade's entry gate).
	ts := &turnState{
		turnID:              "turn-bridge-001",
		transcriptSessionID: sessionID,
		// ADR-057 FR-011/FR-015 fixture repair: GetActiveTurnHookForSession
		// (the role-B predicate RequestCancel uses to find and claim the
		// active turn) matches on routingSessionID, not transcriptSessionID.
		routingSessionID: session.RoutingSessionID(sessionID),
		depth:            0,
		finishedChan:     make(chan struct{}),
		transcriptStore:  store,
	}
	ts.providerCancel = func() { ts.Finish(false) }
	al.activeTurnStates.Store(sessionID, ts)
	defer al.activeTurnStates.Delete(sessionID)

	// Fire the cancel via the canonical entry point (session-scoped).
	_, err = al.RequestCancel(context.Background(),
		CancelScope{SessionID: sessionID},
		CancelCanceller{UserID: "tester", Channel: "web"},
		CancelHooks{}, // no SetSessionInterrupted hook — exercises the mediator path
	)
	require.NoError(t, err)

	// THE ASSERTION (Defect #28): the LifecycleRecord MUST have transitioned
	// to cancelled. Before the fix it stayed "running" (orphaned).
	rec, err := ls.Load(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleCancelled, rec.State,
		"LifecycleRecord must transition to cancelled on cancel, not stay orphaned at %q", rec.State)

	// The UnifiedMeta must ALSO have followed (interrupted) — the mediator's
	// canonical mapping (cancelled → interrupted).
	postMeta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.StatusInterrupted, postMeta.Status,
		"UnifiedMeta must mirror to interrupted when LifecycleRecord transitions to cancelled")
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
		// TestRequestCancel_TransitionsLifecycleRecordToCancelled above.
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

	// UnifiedMeta still mirrored (the mediator proceeds past ErrLifecycleNotFound).
	postMeta, err := store.GetMeta(sessionID)
	require.NoError(t, err)
	assert.Equal(t, session.StatusInterrupted, postMeta.Status,
		"UnifiedMeta must still mirror to interrupted even without a LifecycleRecord")
}
