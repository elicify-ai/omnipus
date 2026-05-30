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

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newCancelTestAgentLoop(t *testing.T) *AgentLoop {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
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
	ts := &turnState{
		turnID:              "turn-001",
		transcriptSessionID: "sess-active",
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

	ts := &turnState{
		turnID:              "turn-tier-b",
		transcriptSessionID: "sess-tier-b",
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

	ts := &turnState{
		turnID:              "turn-double",
		transcriptSessionID: "sess-double",
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

	ts := &turnState{
		turnID:              "turn-hooks",
		transcriptSessionID: "sess-hooks",
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
	_, err := al.RequestCancelForSession(context.Background(), "", "alice", "web")
	require.Error(t, err)
}

// TestRequestCancelForSession_NoActiveTurn_ReturnsFalseFired verifies the adapter
// propagates the "no active turn" no-op correctly.
func TestRequestCancelForSession_NoActiveTurn_ReturnsFalseFired(t *testing.T) {
	t.Parallel()
	al := newCancelTestAgentLoop(t)
	fired, err := al.RequestCancelForSession(context.Background(), "sess-empty", "alice", "web")
	require.NoError(t, err)
	assert.False(t, fired)
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
