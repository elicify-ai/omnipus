// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for ADR-053 §5.1's corrected delegate action set
// (inbox/inbox_ack/steer/respond/cancel/follow_up/peek) plus the illegal
// launch-flag-combo and curated-context-snapshot-cap rejections at `run`.
// pkg/tools/delegate_test.go's existing TestDelegate* suite already proves
// run/status regression (unchanged); this file covers only the NEW surface.

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// fakeSteeringSink implements DelegateSteeringSink for tests.
type fakeSteeringSink struct {
	mu        sync.Mutex
	delivered []providers.Message
	scopes    []string
}

func (f *fakeSteeringSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	return nil
}

func (f *fakeSteeringSink) last() (providers.Message, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.delivered) == 0 {
		return providers.Message{}, ""
	}
	return f.delivered[len(f.delivered)-1], f.scopes[len(f.scopes)-1]
}

// newADR053TestTool builds a DelegateTool wired with real (t.TempDir()-backed)
// session.LifecycleStore/session.MessageInboxStore, a permissive delegation-
// deny gate, and a mock spawner — enough to exercise the full ADR-053 action
// set end to end.
func newADR053TestTool(t *testing.T) (*DelegateTool, *session.LifecycleStore, *session.MessageInboxStore, *fakeSteeringSink) {
	t.Helper()
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetDelegationDenyCheckerBackground(func(ctx context.Context, targetAgentID string) *DelegationDenial { return nil })
	tool.SetDelegationDenyCheckerAwait(func(ctx context.Context, targetAgentID string) *DelegationDenial { return nil })

	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	steer := &fakeSteeringSink{}
	tool.SetLifecycleStore(lc)
	tool.SetMessageInbox(inbox)
	tool.SetSteeringSink(steer)

	return tool, lc, inbox, steer
}

// runAndExtractSessionID delegates a task via action=run(async=true) and
// returns the durable session_id from the ack message.
func runAndExtractSessionID(t *testing.T, tool *DelegateTool, ctx context.Context, task string) string {
	t.Helper()
	result := tool.Execute(ctx, map[string]any{"task": task})
	if result.IsError {
		t.Fatalf("run failed: %s", result.ForLLM)
	}
	const marker = "session_id: "
	idx := strings.Index(result.ForLLM, marker)
	if idx == -1 {
		t.Fatalf("expected %q in run ack, got: %s", marker, result.ForLLM)
	}
	rest := result.ForLLM[idx+len(marker):]
	end := strings.IndexAny(rest, ")\n")
	if end == -1 {
		end = len(rest)
	}
	return strings.TrimSpace(rest[:end])
}

func TestDelegateTool_Run_IllegalLaunchProfile_Rejected(t *testing.T) {
	tool, _, _, _ := newADR053TestTool(t)
	result := tool.Execute(context.Background(), map[string]any{
		"task": "do something", "launch_profile": "bogus-profile",
	})
	if !result.IsError {
		t.Fatal("expected an error for an illegal launch_profile, got success")
	}
	if !strings.Contains(result.ForLLM, "launch_profile") {
		t.Errorf("expected error to mention launch_profile, got: %s", result.ForLLM)
	}
}

func TestDelegateTool_Run_SnapshotOverCap_Rejected(t *testing.T) {
	tool, _, _, _ := newADR053TestTool(t)
	refs := make([]any, 100) // over the default 50-ref cap
	for i := range refs {
		refs[i] = "ref"
	}
	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do something",
		"snapshot": map[string]any{"references": refs},
	})
	if !result.IsError {
		t.Fatal("expected an error for an over-cap snapshot, got success")
	}
	if !strings.Contains(result.ForLLM, "narrow the snapshot") {
		t.Errorf("expected a narrow-the-snapshot error, got: %s", result.ForLLM)
	}
}

func TestDelegateTool_Run_PersistsQueuedThenRunningLifecycleRecord(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	sessionID := runAndExtractSessionID(t, tool, ctx, "background task")
	if sessionID == "" {
		t.Fatal("expected a non-empty session_id")
	}

	// The async goroutine transitions queued -> running -> completed; give
	// it a moment (mockDelegateSpawner returns immediately, but the
	// goroutine scheduling is still async).
	var rec *session.LifecycleRecord
	for i := 0; i < 50; i++ {
		r, err := lc.Load(sessionID)
		if err == nil && r.Terminal() {
			rec = r
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if rec == nil {
		t.Fatal("expected the lifecycle record to reach a terminal state")
	}
	if rec.State != session.LifecycleCompleted {
		t.Errorf("state = %q, want %q", rec.State, session.LifecycleCompleted)
	}
	if rec.ParentDurableKey != "parent-1" {
		t.Errorf("ParentDurableKey = %q, want %q", rec.ParentDurableKey, "parent-1")
	}
}

func TestDelegateTool_Steer_DeliversViaSteeringSink(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	// Manually seed a running child record (bypassing run's async
	// completion race for a deterministic steer test).
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-x", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-x", "text": "focus on the tests",
	})
	if result.IsError {
		t.Fatalf("steer failed: %s", result.ForLLM)
	}
	msg, scope := steer.last()
	if msg.Content != "focus on the tests" {
		t.Errorf("delivered content = %q, want %q", msg.Content, "focus on the tests")
	}
	if scope != "child-x" {
		t.Errorf("delivered scope = %q, want %q", scope, "child-x")
	}
}

func TestDelegateTool_Steer_RateAndBodyCaps(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-caps", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	tool.SetSteerCaps(6, 16*1024)

	for i := 0; i < 6; i++ {
		result := tool.Execute(ctx, map[string]any{"action": "steer", "session_id": "child-caps", "text": "hint"})
		if result.IsError {
			t.Fatalf("steer #%d (within rate cap) failed: %s", i, result.ForLLM)
		}
	}
	seventh := tool.Execute(ctx, map[string]any{"action": "steer", "session_id": "child-caps", "text": "one too many"})
	if !seventh.IsError {
		t.Fatal("expected the 7th steer within 60s to be rate-limited")
	}

	tool2, lc2, _, _ := newADR053TestTool(t)
	if err := lc2.Persist(&session.LifecycleRecord{
		SessionID: "child-body", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	big := strings.Repeat("x", 17*1024)
	result := tool2.Execute(ctx, map[string]any{"action": "steer", "session_id": "child-body", "text": big})
	if !result.IsError {
		t.Fatal("expected an over-cap steer body to be rejected")
	}
}

func TestDelegateTool_Steer_RejectsCrossOwnerAccess(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-y", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-A",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	// A DIFFERENT parent tries to steer child-y.
	ctx := WithTranscriptSessionID(context.Background(), "parent-B")
	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-y", "text": "hijack attempt",
	})
	if !result.IsError {
		t.Fatal("expected cross-owner steer to be rejected, got success")
	}
}

func TestDelegateTool_Respond_ParksThenResumes(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-z", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-1", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}
	_ = inbox // question authority lookup degrades gracefully with an empty inbox

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-z", "correlation_id": "corr-1", "text": "yes, go ahead",
	})
	if result.IsError {
		t.Fatalf("respond failed: %s", result.ForLLM)
	}
	msg, _ := steer.last()
	if msg.Content != "yes, go ahead" {
		t.Errorf("delivered content = %q, want %q", msg.Content, "yes, go ahead")
	}

	rec, err := lc.Load("child-z")
	if err != nil {
		t.Fatalf("Load after respond failed: %v", err)
	}
	if rec.State != session.LifecycleRunning {
		t.Errorf("state after respond = %q, want %q", rec.State, session.LifecycleRunning)
	}
	if rec.NeedsInput != nil {
		t.Error("NeedsInput should be cleared after respond")
	}
}

func TestDelegateTool_Respond_WrongCorrelationRejected(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-w", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-real", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-w", "correlation_id": "corr-WRONG", "text": "x",
	})
	if !result.IsError {
		t.Fatal("expected respond with a mismatched correlation_id to be rejected")
	}
}

func TestDelegateTool_Cancel_SoftThenHardBackstop(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-cancel", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	var softCalled, hardCalled bool
	var mu sync.Mutex
	tool.SetCancelHooks(
		func(sessionID, hint string) ([]string, error) {
			mu.Lock()
			softCalled = true
			mu.Unlock()
			return nil, nil
		},
		func(sessionID, hint string) ([]string, error) {
			mu.Lock()
			hardCalled = true
			mu.Unlock()
			return nil, nil
		},
	)
	tool.SetCancelGrace(20 * time.Millisecond)

	result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-cancel"})
	if result.IsError {
		t.Fatalf("cancel failed: %s", result.ForLLM)
	}
	mu.Lock()
	sc := softCalled
	mu.Unlock()
	if !sc {
		t.Fatal("expected the soft-cancel hook to be called immediately")
	}

	// The session never reaches terminal on its own in this test, so the
	// hard backstop MUST fire after the grace window.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		hc := hardCalled
		mu.Unlock()
		if hc {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("expected the hard-cancel backstop to fire after the grace window elapsed")
}

func TestDelegateTool_Cancel_Hard_SkipsGrace(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-hard", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	var hardCalled bool
	tool.SetCancelHooks(
		func(sessionID, hint string) ([]string, error) {
			t.Fatal("soft hook must not be called for hard=true")
			return nil, nil
		},
		func(sessionID, hint string) ([]string, error) { hardCalled = true; return nil, nil },
	)

	result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-hard", "hard": true})
	if result.IsError {
		t.Fatalf("cancel(hard) failed: %s", result.ForLLM)
	}
	if !hardCalled {
		t.Fatal("expected the hard-cancel hook to be called immediately for hard=true")
	}
	rec, err := lc.Load("child-hard")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleCancelled {
		t.Errorf("state = %q, want %q", rec.State, session.LifecycleCancelled)
	}
}

func TestDelegateTool_InboxAndInboxAck(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-inbox", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	pctMsg := progressMsgForDelegateTest(t, "child-inbox", "pm-1")
	if _, err := inbox.Append("parent-1", pctMsg); err != nil {
		t.Fatalf("seed inbox message failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{"action": "inbox", "session_id": "child-inbox"})
	if result.IsError {
		t.Fatalf("inbox failed: %s", result.ForLLM)
	}
	var resp struct {
		Messages []json.RawMessage `json:"messages"`
		HasMore  bool              `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &resp); err != nil {
		t.Fatalf("failed to decode inbox response: %v (body=%s)", err, result.ForLLM)
	}
	if len(resp.Messages) != 1 {
		t.Fatalf("expected 1 message in inbox response, got %d", len(resp.Messages))
	}

	ackResult := tool.Execute(ctx, map[string]any{
		"action": "inbox_ack", "session_id": "child-inbox", "message_ids": []any{"pm-1"},
	})
	if ackResult.IsError {
		t.Fatalf("inbox_ack failed: %s", ackResult.ForLLM)
	}

	count, err := inbox.UnackedCount("parent-1", "child-inbox")
	if err != nil {
		t.Fatalf("UnackedCount failed: %v", err)
	}
	_ = count // progress doesn't count toward the ceiling; just confirming no crash

	drained, _, _, err := inbox.Drain("parent-1", "child-inbox", "", 10)
	if err != nil {
		t.Fatalf("Drain after ack failed: %v", err)
	}
	if len(drained) != 0 {
		t.Errorf("expected 0 unacked messages after inbox_ack, got %d", len(drained))
	}
}

func TestDelegateTool_Peek_NoLifecycleSideEffect(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-peek", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := inbox.Append("parent-1", progressMsgForDelegateTest(t, "child-peek", "peek-1")); err != nil {
		t.Fatalf("seed inbox failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{"action": "peek", "session_id": "child-peek"})
	if result.IsError {
		t.Fatalf("peek failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "still working") {
		t.Errorf("expected peek to surface the latest progress text, got: %s", result.ForLLM)
	}

	// Peek must not have acked anything.
	msgs, _, _, err := inbox.Drain("parent-1", "child-peek", "", 10)
	if err != nil {
		t.Fatalf("Drain after peek failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Errorf("expected peek to leave the message unacked, got %d remaining", len(msgs))
	}
}

func TestDelegateTool_FollowUp_RequiresTerminalSession(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-notdone", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	result := tool.Execute(ctx, map[string]any{"action": "follow_up", "session_id": "child-notdone"})
	if !result.IsError {
		t.Fatal("expected follow_up on a non-terminal session to be rejected")
	}
}

func TestDelegateTool_FollowUp_NativeReusesSessionID(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-done", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
		Is3P: false,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-done", "task": "one more thing",
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}

	// The new generation must be persisted under the SAME session_id
	// (native warm resume — D5/L-3).
	rec, err := lc.Load("child-done")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.Generation != 1 {
		t.Errorf("Generation = %d, want 1", rec.Generation)
	}
	if rec.ResumedFrom != "child-done" {
		t.Errorf("ResumedFrom = %q, want %q", rec.ResumedFrom, "child-done")
	}
}

func progressMsgForDelegateTest(t *testing.T, sessionID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId:      messageID,
		SessionId:      sessionID,
		CreatedAt:      time.Now(),
		Depth:          1,
		SenderIdentity: "child",
		Text:           "still working",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress failed: %v", err)
	}
	return sm
}
