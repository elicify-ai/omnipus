// Package agent — cancel_test.go
//
// Unit tests for RequestCancel (the canonical cancel state machine) and
// its primitive-argument adapters RequestCancelForSession and
// RequestCancelByChannelChat.
//
// Spec refs: FR-10, FR-11, FR-12, FR-13a, FR-15, FR-17, FR-18-21, FR-25a
// Resolves architect review finding B2.

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
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newCancelTestAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	// os.MkdirTemp + a best-effort RemoveAll, NOT t.TempDir: AgentLoop's own
	// background writers (recap drain, stats flusher, session bookkeeping)
	// are only BOUNDED-drained by al.Close() (e.g. waitRecapDrain's 30s
	// budget just logs a warning and proceeds on timeout — it does not
	// guarantee every writer has actually stopped touching Home by the time
	// Close() returns). t.TempDir()'s own cleanup calls os.RemoveAll and
	// FAILS THE TEST (t.Errorf) if a straggling writer still has the
	// directory non-empty at that instant — observed in practice as
	// "TempDir RemoveAll cleanup: ...: directory not empty" on
	// TestU15Cancel_KillsChildShellsNotSiblings_RealPIDs under full
	// pkg/agent suite load (never in isolation, where there's no contention
	// to delay the drain). plan_wake_delivery_test.go's newPlanWakeHarness
	// documents and works around the identical hazard the same way: a
	// plain, best-effort os.RemoveAll here silently tolerates the race
	// instead of failing the test over a harness cleanup timing quirk that
	// has nothing to do with the behavior under test.
	//
	// Nested under its own private outer container (not bare
	// os.MkdirTemp("", ...)) so filepath.Dir(tmpDir) — what NewAgentLoop
	// roots the shared session/task store at — stays THIS test's own
	// private directory, never the shared OS temp root every test in this
	// package that used to call os.MkdirTemp("", "agent-test-*") directly
	// shared (see loop_test.go's newTestAgentLoop doc comment).
	tmpDirOuter, err := os.MkdirTemp("", "agent-cancel-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpDirOuter) })
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}
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
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(func() { al.Close() })
	return al
}

func newAuditLoggerForCancelTest(t *testing.T, dir string) *audit.Logger {
	t.Helper()
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RetentionDays: 1})
	require.NoError(t, err)
	return logger
}

func readCancelAuditEvents(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	type row struct {
		Event string `json:"event"`
	}
	var events []string
	for _, line := range splitCancelTestLines(data) {
		if len(line) == 0 {
			continue
		}
		var r row
		if json.Unmarshal(line, &r) == nil && r.Event != "" {
			events = append(events, r.Event)
		}
	}
	return events
}

func splitCancelTestLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// ---------------------------------------------------------------------------
// RequestCancel validation
// ---------------------------------------------------------------------------

// TestRequestCancel_EmptyScope_ReturnsError — empty SessionID + empty (Channel,
// ChatID) must return an error (not silently succeed).
func TestRequestCancel_EmptyScope_ReturnsError(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)

	_, err := al.RequestCancel(
		context.Background(),
		CancelScope{}, // no session ID, no channel/chat
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.Error(t, err, "empty scope must return an error")
}

// TestRequestCancel_NoActiveTurn_FiredFalse — when no active turn is running for
// the requested session, RequestCancel must return Fired:false and emit exactly
// one turn_cancel_attempt audit event with was_fired=false.
func TestRequestCancel_NoActiveTurn_FiredFalse(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	// Wire a real audit logger so we can inspect emitted events.
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-no-active"},
		CancelCanceller{UserID: "alice", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.False(t, outcome.Fired, "no active turn → Fired must be false")

	events := readCancelAuditEvents(t, auditDir)
	attempts := 0
	for _, ev := range events {
		if ev == audit.EventTurnCancelAttempt {
			attempts++
		}
	}
	assert.Equal(t, 1, attempts, "must emit exactly one turn_cancel_attempt audit event")
	assert.NotContains(t, events, audit.EventTurnCancelled,
		"no active turn must not emit turn_canceled")
}

// TestRequestCancel_ActiveTurn_FiredTrue — registers a synthetic active turnState,
// calls RequestCancel, and verifies Fired:true + TurnID returned. The
// turn_cancel_attempt audit with was_fired=true must be emitted immediately.
// The turn_canceled audit fires only when the onCancelFinish callback runs.
func TestRequestCancel_ActiveTurn_FiredTrue(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	// Inject a minimal turnState for "sess-active" so RequestCancel finds it.
	// ADR-057 fixture repair: the role-B predicates (GetActiveTurnHookForSession
	// et al.) now match on routingSessionID, not transcriptSessionID — a
	// hand-built literal that omits it is invisible to RequestCancel.
	ts := &turnState{
		turnID:              "turn-001",
		transcriptSessionID: "sess-active",
		routingSessionID:    session.RoutingSessionID("sess-active"),
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store("sess-active", ts)
	defer al.activeTurnStates.Delete("sess-active")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-active"},
		CancelCanceller{UserID: "bob", Channel: "web"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "active turn → Fired must be true")
	assert.Equal(t, "turn-001", outcome.TurnID)

	events := readCancelAuditEvents(t, auditDir)
	assert.Contains(t, events, audit.EventTurnCancelAttempt,
		"must emit turn_cancel_attempt")

	// Trigger the finish callback manually to produce the turn_canceled audit.
	if ts.onCancelFinish != nil {
		ts.onCancelFinish("graceful")
	}

	// Re-read to include the turn_canceled event.
	events2 := readCancelAuditEvents(t, auditDir)
	assert.Contains(t, events2, audit.EventTurnCancelled,
		"onCancelFinish must emit turn_canceled audit")
}

// TestRequestCancel_TierBPath_ResolvesByChannelChat — when SessionID is empty
// but Channel+ChatID are set, RequestCancel must resolve the session from
// activeTurnStates and fire successfully.
func TestRequestCancel_TierBPath_ResolvesByChannelChat(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)
	auditDir := t.TempDir()
	al.auditLogger = newAuditLoggerForCancelTest(t, auditDir)

	// ADR-057 fixture repair: resolveSessionIDByChannelChat (Tier B) also
	// matches on routingSessionID.
	ts := &turnState{
		turnID:              "turn-tier-b",
		transcriptSessionID: "sess-tier-b",
		routingSessionID:    session.RoutingSessionID("sess-tier-b"),
		channel:             "telegram",
		chatID:              "chat-42",
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store("sess-tier-b", ts)
	defer al.activeTurnStates.Delete("sess-tier-b")

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{Channel: "telegram", ChatID: "chat-42"}, // no SessionID
		CancelCanceller{UserID: "@user", Channel: "telegram"},
		CancelHooks{},
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired, "Tier B path must resolve session and fire")
	assert.Equal(t, "turn-tier-b", outcome.TurnID)

	events := readCancelAuditEvents(t, auditDir)
	assert.Contains(t, events, audit.EventTurnCancelAttempt)
}

// TestRequestCancel_DoubleCancelReturnsFiredFalse — a second RequestCancel on the
// same session must return Fired:false (first-cancel-wins).
func TestRequestCancel_DoubleCancelReturnsFiredFalse(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)

	// ADR-057 fixture repair: see TestRequestCancel_ActiveTurn_FiredTrue.
	ts := &turnState{
		turnID:              "turn-double",
		transcriptSessionID: "sess-double",
		routingSessionID:    session.RoutingSessionID("sess-double"),
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store("sess-double", ts)
	defer al.activeTurnStates.Delete("sess-double")

	scope := CancelScope{SessionID: "sess-double"}
	canceller := CancelCanceller{UserID: "alice", Channel: "web"}

	outcome1, err1 := al.RequestCancel(context.Background(), scope, canceller, CancelHooks{})
	require.NoError(t, err1)
	assert.True(t, outcome1.Fired, "first cancel must fire")

	outcome2, err2 := al.RequestCancel(context.Background(), scope, canceller, CancelHooks{})
	require.NoError(t, err2)
	assert.False(t, outcome2.Fired, "second cancel must return Fired:false (double-cancel)")
}

// TestRequestCancel_HooksCalled — verifies that the CancelHooks callbacks are
// invoked during cancel processing.
func TestRequestCancel_HooksCalled(t *testing.T) {
	t.Parallel()

	al := newCancelTestAgentLoop(t)

	// ADR-057 fixture repair: see TestRequestCancel_ActiveTurn_FiredTrue.
	ts := &turnState{
		turnID:              "turn-hooks",
		transcriptSessionID: "sess-hooks",
		routingSessionID:    session.RoutingSessionID("sess-hooks"),
		depth:               0,
		finishedChan:        make(chan struct{}),
	}
	al.activeTurnStates.Store("sess-hooks", ts)
	defer al.activeTurnStates.Delete("sess-hooks")

	var stageFrames []string
	var approvalDenySID string
	var interruptedSID string

	hooks := CancelHooks{
		SendStageFrame: func(sid, stage string) {
			stageFrames = append(stageFrames, stage)
		},
		CancelPendingApprovals: func(sid, reason string) {
			approvalDenySID = sid
		},
		SetSessionInterrupted: func(sid string) {
			interruptedSID = sid
		},
	}

	outcome, err := al.RequestCancel(
		context.Background(),
		CancelScope{SessionID: "sess-hooks"},
		CancelCanceller{UserID: "carol", Channel: "slack"},
		hooks,
	)
	require.NoError(t, err)
	assert.True(t, outcome.Fired)

	// Graceful stage frame must be sent immediately.
	require.Contains(t, stageFrames, "graceful",
		"SendStageFrame must be called with 'graceful' on the graceful phase")
	assert.Equal(t, "sess-hooks", approvalDenySID,
		"CancelPendingApprovals must be called with the session ID")
	assert.Equal(t, "sess-hooks", interruptedSID,
		"SetSessionInterrupted must be called with the session ID")
}

// ---------------------------------------------------------------------------
// RequestCancelForSession adapter
// ---------------------------------------------------------------------------

// TestRequestCancelForSession_EmptySessionID_ReturnsError verifies the adapter
// validates its input.
func TestRequestCancelForSession_EmptySessionID_ReturnsError(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)
	_, _, err := al.RequestCancelForSession(context.Background(), "", "alice", "web")
	require.Error(t, err)
}

// TestRequestCancelForSession_NoActiveTurn_ArmsLatch verifies the adapter
// surfaces Armed rather than discarding it: with no turn registered yet for
// sessionID, RequestCancel arms a pre-registration cancel latch
// (cancel_prearm.go) instead of silently no-op'ing, and this primitive
// adapter must carry that signal all the way through to the caller — the
// exact structural gap the widened (fired, armed, err) signature closes
// (finding: RequestCancelForSession used to flatten CancelOutcome to a bare
// bool, discarding Armed at this adapter boundary).
func TestRequestCancelForSession_NoActiveTurn_ArmsLatch(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)

	// Construct the precondition turnImminentForIdentity now requires: a
	// message genuinely in flight for this identity, not merely "no active
	// turn registered" (see cancel_prearm_test.go's file-level PREMISE
	// UPDATE comment).
	primeImminentSessionWorker(al, "sess-empty")

	fired, armed, err := al.RequestCancelForSession(context.Background(), "sess-empty", "alice", "web")
	require.NoError(t, err)
	assert.False(t, fired, "no active turn exists — fired must be false")
	assert.True(t, armed, "no active turn for a resolvable session id must arm a pre-registration cancel latch, and the adapter must report it")
}

// ---------------------------------------------------------------------------
// cancelAbuseDetector (agent-level)
// ---------------------------------------------------------------------------

// TestAgentCancelAbuse_BurstEmitsOnce verifies the agent-level abuse detector
// emits cancel.abuse_pattern when the burst threshold is reached.
func TestAgentCancelAbuse_BurstEmitsOnce(t *testing.T) {
	t.Parallel()

	auditDir := t.TempDir()
	auditLogger := newAuditLoggerForCancelTest(t, auditDir)

	d := newCancelAbuseDetector()
	d.burstAt = 3
	d.window = 10 * time.Second

	ctx := context.Background()
	now := time.Now()

	for i := 0; i < 3; i++ {
		d.recordAttempt(ctx, "alice", "telegram", now.Add(time.Duration(i)*100*time.Millisecond), auditLogger)
	}

	events := readCancelAuditEvents(t, auditDir)
	abuseCount := 0
	for _, ev := range events {
		if ev == audit.EventCancelAbusePattern {
			abuseCount++
		}
	}
	assert.Equal(t, 1, abuseCount,
		"burst of 3 must emit exactly one cancel.abuse_pattern event")
}
