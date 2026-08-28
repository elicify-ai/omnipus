// Package agent — cancel_background_bash_test.go
//
// Integration tests proving RequestCancel -> CancelHooks.KillBackgroundSessions
// -> tools.SessionManager.KillAllForSession fires correctly when wired
// (FR-B10/FR-B11, User Story 5: "Canceling a session also stops any
// background bash work it started"). The bash tool itself does not exist yet
// this wave (a later, parallel wave owns it), so these tests exercise the
// cascade at the level the current wave actually delivers: a ProcessSession
// registered directly against a tools.SessionManager, with
// CancelHooks.KillBackgroundSessions wired to that manager's
// KillAllForSession — the same shape production wiring uses in
// pkg/agent/cancel.go's killBackgroundSessionsForCancelSurface and
// pkg/gateway's websocket.go/schedules.go call sites.
//
// Spec: docs/internal/specs/bash-tool-spec.md, User Story 5 + FR-B10/FR-B11/FR-B14.

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// auditRecordRow decodes one audit.jsonl line far enough to inspect both the
// event name and its fields map — readCancelAuditEvents (cancel_test.go, same
// package) only decodes the event name, which is not enough to assert on
// background_sessions_killed below.
type auditRecordRow struct {
	Event  string         `json:"event"`
	Fields map[string]any `json:"fields"`
}

// readCancelAuditRecords parses audit.jsonl the same way readCancelAuditEvents
// does (reusing its splitCancelTestLines helper) but keeps the fields map for
// each row.
func readCancelAuditRecords(t *testing.T, dir string) []auditRecordRow {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var rows []auditRecordRow
	for _, line := range splitCancelTestLines(data) {
		if len(line) == 0 {
			continue
		}
		var r auditRecordRow
		if json.Unmarshal(line, &r) == nil && r.Event != "" {
			rows = append(rows, r)
		}
	}
	return rows
}

// newRunningBackgroundSession is a small helper building a ProcessSession that
// looks like a real detached background bash/exec session: Status "running",
// a PID chosen well outside any real process range in this sandbox (so
// killProcessGroup's underlying syscall.Kill(-pid, ...) reliably returns
// ESRCH, treated as a successful kill — see
// pkg/tools/session_process_unix.go), and the given OwnerSessionID.
func newRunningBackgroundSession(id, ownerSessionID string, pid int) *tools.ProcessSession {
	return &tools.ProcessSession{
		ID:             id,
		OwnerSessionID: ownerSessionID,
		Status:         "running",
		PID:            pid,
		StartTime:      time.Now().Unix(),
	}
}

// registerActiveTurn injects a minimal turnState for sessionID so RequestCancel
// finds an active turn to claim (mirrors the pattern used throughout
// cancel_test.go, e.g. TestRequestCancel_ActiveTurn_FiredTrue). It returns the
// registered *turnState so callers can manually fire onCancelFinish (set by
// RequestCancel's SetOnCancelFinish call) the same way
// TestRequestCancel_ActiveTurn_FiredTrue does, to produce the turn_canceled
// audit event without a real turn-processing goroutine.
func registerActiveTurn(t *testing.T, al *AgentLoop, sessionID, turnID string) *turnState {
	t.Helper()
	ts := &turnState{
		turnID: turnID,
		// ADR-057 FR-011/FR-015 fixture repair: GetActiveTurnHookForSession
		// (turn.go) — the role-B predicate RequestCancel uses to find the
		// active turn for a CancelScope.SessionID — now matches on
		// routingSessionID, not transcriptSessionID. Both are set to the same
		// value here because this fixture predates the D1/D2 identity split
		// (transcriptSessionID and routingSessionID coincide for a root turn).
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store(sessionID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(sessionID) })
	return ts
}

// TestBash_CancelCascade_KillsOwnedBackgroundSessions covers BDD Scenario
// "Canceling a session kills its background bash sessions" (User Story 5,
// Acceptance Scenario 1): a running background session owned by the canceled
// session must be killed as part of RequestCancel's existing graceful-cascade
// phase, and the kill must be observable via the counter (FR-B14) without
// relying on a subsequent poll.
func TestBash_CancelCascade_KillsOwnedBackgroundSessions(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	// Wire a real audit logger so we can assert the killed-count reaches the
	// turn_canceled audit event's fields map (background_sessions_killed).
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
	ts := registerActiveTurn(t, al, "sess-bg-cascade", "turn-bg-cascade")

	sm := tools.NewSessionManager()
	bgSession := newRunningBackgroundSession("bg-1", "sess-bg-cascade", 999991001)
	sm.Add(bgSession)

	countBefore := sm.KilledBackgroundSessionsCount()

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-bg-cascade"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) (int, int) {
				return sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "an active turn exists, so the cancel must fire")

	got, getErr := sm.Get("bg-1")
	require.NoError(t, getErr)
	assert.Equal(t, "canceled", got.GetStatus(),
		"the background session owned by the canceled chat session must be killed and relabeled canceled")

	assert.Equal(t, countBefore+1, sm.KilledBackgroundSessionsCount(),
		"FR-B14: the kill counter must increment so the cascade is observable without a subsequent poll")

	// Trigger the finish callback (mirrors TestRequestCancel_ActiveTurn_FiredTrue)
	// to produce the turn_canceled audit event, then assert the killed count
	// rode along in its fields map.
	require.NotNil(t, ts.onCancelFinish, "RequestCancel must have registered onCancelFinish")
	ts.onCancelFinish("graceful")

	records := readCancelAuditRecords(t, auditDir)
	var found bool
	for _, r := range records {
		if r.Event != audit.EventTurnCancelled {
			continue
		}
		found = true
		killed, ok := r.Fields["background_sessions_killed"]
		require.True(t, ok, "turn_canceled audit event must carry background_sessions_killed")
		assert.InDelta(t, float64(1), killed, 0,
			"one background session was killed, so the audited count must be 1")
	}
	assert.True(t, found, "expected a turn_canceled audit event")
}

// TestBash_CancelCascade_NoOpWhenNothingToKill covers BDD Scenario "Canceling
// a session with no background work is a no-op for the new cascade step"
// (User Story 5, Acceptance Scenario 2): existing cancel behavior (Fired,
// audit, transcript) must complete exactly as it does today, with no error or
// delay introduced by the new hook.
func TestBash_CancelCascade_NoOpWhenNothingToKill(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
	ts := registerActiveTurn(t, al, "sess-bg-noop", "turn-bg-noop")

	sm := tools.NewSessionManager() // deliberately empty — no background sessions at all

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-bg-noop"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) (int, int) {
				return sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "cancel must fire normally even with no background work")
	assert.Equal(t, int64(0), sm.KilledBackgroundSessionsCount(),
		"no background sessions exist, so the counter must stay at zero")

	require.NotNil(t, ts.onCancelFinish, "RequestCancel must have registered onCancelFinish")
	ts.onCancelFinish("graceful")

	records := readCancelAuditRecords(t, auditDir)
	var found bool
	for _, r := range records {
		if r.Event != audit.EventTurnCancelled {
			continue
		}
		found = true
		killed, ok := r.Fields["background_sessions_killed"]
		require.True(t, ok, "turn_canceled audit event must carry background_sessions_killed even when zero")
		assert.InDelta(t, float64(0), killed, 0,
			"no background sessions were killed, so the audited count must be 0, not simply absent")
	}
	assert.True(t, found, "expected a turn_canceled audit event")
}

// TestBash_CancelCascade_DoesNotAffectOtherSessions covers BDD Scenario
// "Canceling one session does not affect another session's background work"
// (User Story 5, Acceptance Scenario 3): canceling session A must kill only
// A's background session, leaving unrelated session B's background session
// running untouched.
func TestBash_CancelCascade_DoesNotAffectOtherSessions(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	registerActiveTurn(t, al, "sess-A", "turn-A")
	// Session B has no active turn registered; its background work must
	// simply be left alone by canceling A regardless.

	sm := tools.NewSessionManager()
	bgA := newRunningBackgroundSession("bg-A", "sess-A", 999992001)
	bgB := newRunningBackgroundSession("bg-B", "sess-B", 999992002)
	sm.Add(bgA)
	sm.Add(bgB)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-A"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) (int, int) {
				return sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired)

	gotA, err := sm.Get("bg-A")
	require.NoError(t, err)
	assert.Equal(t, "canceled", gotA.GetStatus(), "session A's background work must be killed and relabeled canceled")

	gotB, err := sm.Get("bg-B")
	require.NoError(t, err)
	assert.Equal(t, "running", gotB.GetStatus(), "session B's background work must be left untouched")
}

// TestBash_CancelCascade_HookCalledEvenWithNoActiveTurn is the regression
// test for the root-cause bug this fix closes: a `bash
// run_in_background=true` call's turn ends immediately (the tool call
// returns right away), so by the time a user later clicks Cancel to stop the
// still-running background job, there is no active turn left for
// GetActiveTurnHookForSession to find — ClaimCancel never runs, wasFired is
// false. Before this fix, RequestCancel's wasFired-gated early return meant
// hooks.KillBackgroundSessions was NEVER reached in this scenario: a silent
// no-op that left the background process (and its process group) running
// forever. This test previously encoded the bug (asserted the hook was NOT
// called); it now asserts the opposite — the hook IS called for a
// resolvable session even with no active turn — while confirming the
// turn-cancel state machine itself still correctly reports Fired: false
// (there genuinely is no turn to cancel).
func TestBash_CancelCascade_HookCalledEvenWithNoActiveTurn(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	// Wire a real audit logger (7-reviewer gate finding: the no-active-turn
	// tests in this file previously left al.auditLogger nil, so
	// EventTurnCancelBackgroundKilled — the audit event RequestCancel emits
	// for the unconditional background-kill cascade, independent of
	// wasFired/Fired — had ZERO test coverage) so we can assert it fires
	// even on this no-active-turn path.
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
	// Deliberately do not register any turnState for this session — this is
	// the real "background work outlived its turn" state a completed
	// run_in_background=true call leaves behind.

	called := false
	var gotSessionID string
	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-never-active"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) (int, int) {
				called = true
				gotSessionID = sessionID
				// A non-zero killed count (rather than 0,0) so this test can
				// also exercise/assert the audit-emission gate (killed>0 ||
				// failed>0) below — a hook returning (0,0) would correctly
				// suppress the audit event as a no-op cancel, which is NOT
				// what this test is verifying.
				return 1, 0
			},
		},
	)
	require.NoError(t, err)
	assert.False(t, outcome.Fired,
		"no active turn means no claim, so the turn-cancel state machine itself does not fire")
	assert.True(t, called, "KillBackgroundSessions must be invoked even when no active turn is claimed — "+
		"this is the fix for the background-shell-never-canceled bug")
	assert.Equal(t, "sess-never-active", gotSessionID, "the hook must receive the resolved sessionID")
	assert.Equal(t, 1, outcome.BackgroundSessionsKilled,
		"CancelOutcome must carry the killed count even when Fired is false")
	assert.Equal(t, 0, outcome.BackgroundSessionsFailed)

	records := readCancelAuditRecords(t, auditDir)
	var found bool
	for _, r := range records {
		if r.Event != audit.EventTurnCancelBackgroundKilled {
			continue
		}
		found = true
		assert.Equal(t, "sess-never-active", r.Fields["session_id"],
			"background_killed audit event must carry the resolved session_id")
		killed, ok := r.Fields["background_sessions_killed"]
		require.True(t, ok, "background_killed audit event must carry background_sessions_killed")
		assert.InDelta(t, float64(1), killed, 0)
	}
	assert.True(t, found, "expected a turn.cancel.background_killed audit event even though no "+
		"active turn existed — this is the audit-trail gap the cascade's own doc comment describes")
}

// TestBash_CancelCascade_KillsRealBackgroundProcessWithNoActiveTurn drives
// the exact end-to-end scenario the bug report identified, using a REAL
// tools.ProcessSession registered against a tools.SessionManager rather than
// a boolean callback flag: a background bash session owned by S is still
// "running", NO active turn is registered for S (its turn already ended),
// and the user cancels S. Before the fix, RequestCancel would early-return
// before ever calling hooks.KillBackgroundSessions, so the background
// process would keep running (silently) forever. After the fix, the process
// must actually be killed and relabeled "canceled" even though
// outcome.Fired is false.
func TestBash_CancelCascade_KillsRealBackgroundProcessWithNoActiveTurn(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	// Wire a real audit logger — see the identical note in
	// TestBash_CancelCascade_HookCalledEvenWithNoActiveTurn above; this test
	// additionally exercises the REAL KillAllForSession path (not a stubbed
	// count), so it is the strongest coverage of the audit event actually
	// firing with a genuine kill.
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)
	// Deliberately do not register any turnState for "sess-bg-orphaned" — the
	// background job's own turn already finished by the time Cancel fires.

	sm := tools.NewSessionManager()
	bgSession := newRunningBackgroundSession("bg-orphaned", "sess-bg-orphaned", 999993001)
	sm.Add(bgSession)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-bg-orphaned"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{
			KillBackgroundSessions: func(sessionID string) (int, int) {
				return sm.KillAllForSession(sessionID)
			},
		},
	)
	require.NoError(t, err)
	assert.False(t, outcome.Fired, "no active turn exists, so the turn-cancel state machine itself does not fire")

	got, getErr := sm.Get("bg-orphaned")
	require.NoError(t, getErr)
	assert.True(t, got.IsDone(),
		"the background process must be killed even though RequestCancel's own turn-cancel state machine never fired")
	assert.Equal(t, "canceled", got.GetStatus(),
		"KillAllForSession must relabel to the canceled terminal status even on this no-active-turn path")
	assert.Equal(t, 1, outcome.BackgroundSessionsKilled,
		"CancelOutcome must carry the killed count even when Fired is false")

	records := readCancelAuditRecords(t, auditDir)
	var found bool
	for _, r := range records {
		if r.Event != audit.EventTurnCancelBackgroundKilled {
			continue
		}
		found = true
		assert.Equal(t, "sess-bg-orphaned", r.Fields["session_id"],
			"background_killed audit event must carry the resolved session_id")
		killed, ok := r.Fields["background_sessions_killed"]
		require.True(t, ok, "background_killed audit event must carry background_sessions_killed")
		assert.InDelta(t, float64(1), killed, 0)
	}
	assert.True(t, found, "expected a turn.cancel.background_killed audit event for this REAL "+
		"kill even though no active turn existed")
}

// TestAgentLoop_Close_ReapsSharedSessionManagerBackgroundSessions is the
// integration-level regression test for AgentLoop.Close()'s shutdown reaper
// (pkg/agent/loop.go, ~"Shutdown reaper: kill every still-running background
// bash/exec session"): Close() reaches into tools.GetSharedSessionManager()
// — a PROCESS-WIDE singleton, NOT scoped to this *AgentLoop, see the
// load-bearing precondition comment at that call site — and kills every
// still-running background session. Before this test, the actual
// Close()->singleton wiring itself had NO coverage: every other test in this
// file drives SessionManager.KillAll/KillAllForSession directly against a
// throwaway tools.NewSessionManager(), never through a real AgentLoop.Close()
// call reaching the ACTUAL shared manager the production shutdown path uses.
//
// Deliberately NOT t.Parallel() and deliberately does NOT use
// newCancelTestAgentLoop (which registers its own t.Cleanup(func(){
// al.Close() }) — this test calls Close() itself, exactly once, in the test
// body, so it can assert on the shared SessionManager immediately afterward
// without a second Close() call on the same *AgentLoop landing concurrently
// or racing this one's assertions. Not parallel because it registers a
// session directly on the process-wide shared singleton and observes it
// deterministically right after Close() — running concurrently with other
// tests that also reach the shared manager (e.g. via their own
// AgentLoop.Close() in t.Cleanup) would not be incorrect (SessionManager is
// internally mutex-safe) but adds unnecessary timing noise to a test whose
// whole point is to observe ONE specific kill.
func TestAgentLoop_Close_ReapsSharedSessionManagerBackgroundSessions(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})

	sm := tools.GetSharedSessionManager()
	sess := newRunningBackgroundSession("close-reaper-session", "unused-owner-for-close-test", 999994001)
	sm.Add(sess)
	t.Cleanup(func() { sm.Remove("close-reaper-session") })

	al.Close()

	got, err := sm.Get("close-reaper-session")
	require.NoError(t, err)
	assert.True(t, got.IsDone(),
		"AgentLoop.Close() must reap every still-running session on the shared, process-wide SessionManager")
}
