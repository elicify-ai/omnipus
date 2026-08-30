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
	"errors"
	"os"
	"path/filepath"
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
	// B.5 fix (FR-196 kill switch fails-closed when unwired): tests that exercise
	// the session-messaging plane must explicitly enable it. The default
	// kill-switch posture is fail-closed (production behavior).
	tool.SetSessionMessagingEnabled(func() bool { return true })

	lc := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	steer := &fakeSteeringSink{}
	tool.SetLifecycleStore(lc)
	tool.SetMessageInbox(inbox)
	tool.SetSteeringSink(steer)

	// Drain in-flight async delegation goroutines before the t.TempDir()
	// cleanups run. Registered AFTER the two t.TempDir() calls above so LIFO
	// cleanup ordering puts this FIRST — the goroutines finish writing before
	// RemoveAll deletes the directories they are writing into. Without this,
	// any test that reaches the async path (e.g. the 3P respond corrective
	// re-dispatch) races its own teardown and fails under -p contention with
	// "TempDir RemoveAll cleanup: directory not empty".
	t.Cleanup(tool.WaitForAsyncTasks)

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
	tool, _, _, _ := newADR053TestTool(t) //nolint:dogsled // Only the tool is relevant from the four test fixtures.
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
	tool, _, _, _ := newADR053TestTool(t) //nolint:dogsled // Only the tool is relevant from the four test fixtures.
	refs := make([]any, 100)              // over the default 50-ref cap
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
	// FR-015: the lifecycle mint fails closed without a resolvable
	// delegating agent, so a run context must carry one — the agent loop
	// injects it unconditionally on the turn path (pkg/agent/loop.go).
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")

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
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
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
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
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
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
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

// TestDelegateTool_Steer_UtilityProfileRefused is G-8's primary regression:
// launch_profile is documented as a real behavioral contract ("utility" =
// fire-and-collect, no steering; "specialist" = collaborating worker with
// steering) but nothing ever enforced it — a "utility" child could be
// steered exactly like a "specialist" one. This proves executeSteer now
// refuses the attempt, names the profile the child was actually launched
// with, and never reaches the steering sink.
func TestDelegateTool_Steer_UtilityProfileRefused(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-utility-steer", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-utility-steer", "text": "focus on the tests",
	})
	if !result.IsError {
		t.Fatal("G-8: expected steer on a \"utility\"-launched delegation to be refused, got success")
	}
	if !strings.Contains(result.ForLLM, "utility") || !strings.Contains(result.ForLLM, "fire-and-collect") {
		t.Errorf("expected the refusal to name the launch profile it was launched with (\"utility\"/"+
			"\"fire-and-collect\"), got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "specialist") {
		t.Errorf("expected the refusal to name the profile that WOULD permit steering (\"specialist\"), "+
			"got: %s", result.ForLLM)
	}
	if len(steer.delivered) != 0 {
		t.Errorf("expected NO steering message to reach the sink for a refused utility-profile steer, got %d",
			len(steer.delivered))
	}
}

// TestDelegateTool_Steer_SpecialistProfileSucceeds is the regression half of
// G-8: a "specialist"-launched delegation must continue to allow steering
// exactly as it did before the gate was added.
func TestDelegateTool_Steer_SpecialistProfileSucceeds(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-specialist-steer", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-specialist-steer", "text": "focus on the tests",
	})
	if result.IsError {
		t.Fatalf("expected steer on a \"specialist\"-launched delegation to succeed, got: %s", result.ForLLM)
	}
	msg, scope := steer.last()
	if msg.Content != "focus on the tests" {
		t.Errorf("delivered content = %q, want %q", msg.Content, "focus on the tests")
	}
	if scope != "child-specialist-steer" {
		t.Errorf("delivered scope = %q, want %q", scope, "child-specialist-steer")
	}
}

// TestDelegateTool_Steer_UnsetLaunchProfileTreatedAsUtility proves a legacy/
// pre-ADR-053 record with an empty LaunchProfile (never minted with the
// field, or minted outside delegate.run) is treated as "utility" — the same
// default executeRun itself applies when the argument is omitted — never as
// an implicit steering grant.
func TestDelegateTool_Steer_UnsetLaunchProfileTreatedAsUtility(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-unset-profile", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", // LaunchProfile deliberately left unset.
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-unset-profile", "text": "hi",
	})
	if !result.IsError {
		t.Fatal("expected steer on a record with an unset launch_profile to be refused (treated as utility)")
	}
	if len(steer.delivered) != 0 {
		t.Errorf("expected NO steering message delivered, got %d", len(steer.delivered))
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
	// Seed the question the respond will answer with a self_ok authority so
	// the fail-closed authority check (MAJOR-2) can positively verify it.
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-z", "q-1", "corr-1",
		generated.SelfOk)); err != nil {
		t.Fatalf("seed question message failed: %v", err)
	}

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

	// HIGH-1 (14-reviewer sign-off): respond now genuinely REDISPATCHES the
	// child (reusing the isResume spawn machinery), not merely flips the
	// lifecycle record and enqueues a steering message nothing will ever
	// drain. That redispatch is asynchronous (mirrors follow_up's own
	// fire-and-forget dispatch), so the record legitimately passes through
	// `running` before mockDelegateSpawner's instant, error-free response
	// carries it straight on to `completed` — asserting `running` here would
	// be racing the dispatch goroutine (flaky depending on scheduling).
	// Waiting for the async task to actually finish and asserting the
	// TERMINAL state is the stronger proof: it can only be reached if a real
	// turn was dispatched and ran to completion, not just "1 message
	// pending" in a queue with no consumer.
	tool.WaitForAsyncTasks()
	rec, err := lc.Load("child-z")
	if err != nil {
		t.Fatalf("Load after respond failed: %v", err)
	}
	if rec.State != session.LifecycleCompleted {
		t.Errorf("state after respond's redispatch completed = %q, want %q", rec.State, session.LifecycleCompleted)
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

// TestDelegateTool_Respond_RejectsCrossOwnerAccess (sec-MAJOR-3) proves the
// producer-side parent-of gate covers the respond action: a session that is
// NOT the recorded parent of the target child cannot respond to it. This is
// the steer/response counterpart of TestDelegateTool_Steer_RejectsCrossOwnerAccess
// — verifyCallerOwnsSession (delegate.go) runs BEFORE the needs_input/
// correlation/authority checks, so a non-parent is rejected up front regardless
// of whether it guessed a valid correlation_id.
func TestDelegateTool_Respond_RejectsCrossOwnerAccess(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-co", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-A",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-co", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	// A DIFFERENT parent (parent-B) tries to respond to child-co (owned by
	// parent-A). It supplies the correct correlation_id to prove the rejection
	// is on OWNERSHIP, not a correlation mismatch.
	ctx := WithTranscriptSessionID(context.Background(), "parent-B")
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-co", "correlation_id": "corr-co", "text": "hijack answer",
	})
	if !result.IsError {
		t.Fatal("expected cross-owner respond to be rejected, got success")
	}
	if !strings.Contains(result.ForLLM, "not owned") {
		t.Errorf("expected the rejection to cite ownership, got: %s", result.ForLLM)
	}
}

// TestDelegateTool_Respond_UtilityProfileRefused is G-8's respond-side
// regression: respond is the same class of mid-task parent intervention as
// steer (it resumes a parked child with parent-supplied content), so it is
// gated by the identical launch_profile contract. The session must be left
// exactly as parked as it was — never resumed — by a refused respond.
func TestDelegateTool_Respond_UtilityProfileRefused(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-utility-respond", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-utility", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}
	// Seed a positively-verifiable self_ok question so a failure here can
	// ONLY be the launch_profile gate, not the separate R§8.2 authority check.
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-utility-respond", "q-utility",
		"corr-utility", generated.SelfOk)); err != nil {
		t.Fatalf("seed question message failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-utility-respond", "correlation_id": "corr-utility", "text": "go ahead",
	})
	if !result.IsError {
		t.Fatal("G-8: expected respond on a \"utility\"-launched delegation to be refused, got success")
	}
	if !strings.Contains(result.ForLLM, "utility") || !strings.Contains(result.ForLLM, "fire-and-collect") {
		t.Errorf("expected the refusal to name the launch profile it was launched with (\"utility\"/"+
			"\"fire-and-collect\"), got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "specialist") {
		t.Errorf("expected the refusal to name the profile that WOULD permit responding (\"specialist\"), "+
			"got: %s", result.ForLLM)
	}
	if len(steer.delivered) != 0 {
		t.Errorf("expected NO answer to reach the steering sink for a refused utility-profile respond, got %d",
			len(steer.delivered))
	}

	// The session must still be parked, exactly as it was — a refused
	// respond must never resume/consume the child's turn.
	rec, err := lc.Load("child-utility-respond")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Errorf("state after refused respond = %q, want still %q (must not resume)", rec.State, session.LifecycleNeedsInput)
	}
	if rec.NeedsInput == nil || rec.NeedsInput.CorrelationID != "corr-utility" {
		t.Error("NeedsInput/correlation_id must survive a launch_profile-refused respond unchanged, " +
			"so the caller can relaunch as specialist and retry")
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
			return []string{"child-cancel"}, nil
		},
		func(sessionID, hint string) ([]string, error) {
			mu.Lock()
			hardCalled = true
			mu.Unlock()
			return []string{"child-cancel"}, nil
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
		func(sessionID, hint string) ([]string, error) { hardCalled = true; return []string{"child-hard"}, nil },
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

// questionMsgForDelegateTest builds a SessionMessage of kind=question with the
// given authority tag — used to seed an inbox for respond-action fail-closed
// authority tests (MAJOR-2).
func questionMsgForDelegateTest(
	t *testing.T, sessionID, messageID, correlationID string,
	authority generated.SessionMessageQuestionAuthority,
) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId:      messageID,
		SessionId:      sessionID,
		CorrelationId:  correlationID,
		CreatedAt:      time.Now(),
		Depth:          1,
		Direction:      generated.SessionMessageQuestionDirectionChildToParent,
		Kind:           generated.SessionMessageQuestionKindQuestion,
		SenderIdentity: "child",
		Text:           "can I proceed?",
		Authority:      &authority,
		Wait:           true,
	}); err != nil {
		t.Fatalf("FromSessionMessageQuestion failed: %v", err)
	}
	return sm
}

// failingDrainInbox implements DelegateInboxStore; its Drain always returns an
// error, used to prove respond's authority check is fail-closed on a Drain
// error (MAJOR-2) rather than silently skipping.
type failingDrainInbox struct{}

func (failingDrainInbox) Drain(string, string, string, int) ([]generated.SessionMessage, string, bool, error) {
	return nil, "", false, errors.New("simulated inbox read failure")
}
func (failingDrainInbox) Ack(string, []string) error { return nil }
func (failingDrainInbox) AckDetailed(_ string, messageIDs []string) (*session.AckResult, error) {
	// Mirrors Ack's own unconditional-success no-op stance above: this fake
	// exists solely to prove respond's authority check is fail-closed on a
	// Drain error, so AckDetailed simply reports every requested id as
	// acknowledged rather than modeling the real store's known/unknown split.
	return &session.AckResult{Acknowledged: messageIDs}, nil
}
func (failingDrainInbox) UnackedCount(string, string) (int, error) {
	return 0, nil
}
func (failingDrainInbox) Peek(string, string) (*session.PeekSnapshot, error) {
	return &session.PeekSnapshot{}, nil
}

// MAJOR-1: cancel MUST be denied (not executed) when the lifecycle Load
// errors — the caller-ownership check can no longer be skipped by an induced
// read error (cross-tenant DoS). Previously cancel fell through to
// cancelHard/cancelSoft on ANY Load error.
func TestDelegateTool_Cancel_DeniedOnLifecycleLoadError(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	// Seed a record owned by parent-1, then CORRUPT its JSONL tail so Load
	// errors (the exact fail-open trigger MAJOR-1 targets).
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-corrupt", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Overwrite the lifecycle file with garbage so tail() scan-errors.
	if err := os.WriteFile(filepath.Join(lc.Dir(), "child-corrupt.jsonl"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt lifecycle file: %v", err)
	}

	cancelCalled := false
	tool.SetCancelHooks(
		func(string, string) ([]string, error) { cancelCalled = true; return nil, nil },
		func(string, string) ([]string, error) { cancelCalled = true; return nil, nil },
	)

	result := tool.Execute(ctx, map[string]any{"action": "cancel", "session_id": "child-corrupt", "hard": true})
	if !result.IsError {
		t.Fatal("expected cancel to be DENIED on a lifecycle Load error, got success (fail-open regression)")
	}
	if cancelCalled {
		t.Fatal("cancel hook was called despite the Load error — the cancel must NOT proceed")
	}
}

// MAJOR-1: cancel MUST be denied when the lifecycle store is not configured
// at all (can't verify ownership without it).
func TestDelegateTool_Cancel_DeniedWhenLifecycleUnconfigured(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSpawner(&mockDelegateSpawner{})
	tool.SetDelegationDenyCheckerBackground(func(ctx context.Context, targetAgentID string) *DelegationDenial { return nil })
	tool.SetCancelHooks(
		func(string, string) ([]string, error) { t.Fatal("soft hook must not fire"); return nil, nil },
		func(string, string) ([]string, error) { t.Fatal("hard hook must not fire"); return nil, nil },
	)
	result := tool.Execute(context.Background(), map[string]any{"action": "cancel", "session_id": "any", "hard": true})
	if !result.IsError {
		t.Fatal("expected cancel to be DENIED when no lifecycle store is configured, got success (fail-open)")
	}
}

// MAJOR-2: respond MUST be denied when the inbox Drain errors — the
// owner_required authority check can no longer be skipped by a Drain error.
func TestDelegateTool_Respond_DeniedOnInboxDrainError(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-drain-err", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-1", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Swap in an inbox whose Drain always errors — simulates a corrupt
	// tail / I/O failure on the durable inbox. The fail-open bug would have
	// let the respond proceed (skipping the authority check); fail-closed
	// MUST deny it.
	tool.SetMessageInbox(failingDrainInbox{})

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-drain-err", "correlation_id": "corr-1", "text": "x",
	})
	if !result.IsError {
		t.Fatal("expected respond to be DENIED on an inbox Drain error, got success (fail-open regression)")
	}
	// The record must still be parked at needs_input (not resumed).
	rec, err := lc.Load("child-drain-err")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Errorf("state after denied respond = %q, want still %q (must not resume)", rec.State, session.LifecycleNeedsInput)
	}
}

// MAJOR-2: an owner_required question MUST NOT be answerable by respond even
// if the parent inbox_ack'd it first. Previously Drain("",0) excluded acked
// messages, so the owner_required check silently passed on an acked question.
// Fail-closed now denies it (the question can't be positively verified safe).
func TestDelegateTool_Respond_OwnerRequiredDeniedEvenWhenAcked(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-owner", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-owner", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Seed an owner_required question, then ACK it — the exact sequence that
	// used to bypass the authority check (Drain excludes acked messages).
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-owner", "q-owner",
		"corr-owner", generated.OwnerRequired)); err != nil {
		t.Fatalf("seed owner_required question failed: %v", err)
	}
	if err := inbox.Ack("parent-1", []string{"q-owner"}); err != nil {
		t.Fatalf("ack failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-owner", "correlation_id": "corr-owner", "text": "x",
	})
	if !result.IsError {
		t.Fatal("expected respond on an acked owner_required question to be DENIED, got success (fail-open regression)")
	}
	rec, err := lc.Load("child-owner")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleNeedsInput {
		t.Errorf("state after denied respond = %q, want still %q (owner_required must not resume)", rec.State, session.LifecycleNeedsInput)
	}
}

// MAJOR-2 regression: a self_ok question that IS present in the drain (not
// acked) MUST still be answerable — the fail-closed change must not break the
// legitimate happy path.
func TestDelegateTool_Respond_SelfOkQuestionAllowedWhenNotAcked(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-selfok", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileSpecialist,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-selfok", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-selfok", "q-selfok",
		"corr-selfok", generated.SelfOk)); err != nil {
		t.Fatalf("seed self_ok question failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-selfok", "correlation_id": "corr-selfok", "text": "go",
	})
	if result.IsError {
		t.Fatalf("respond on a present, unacked self_ok question should succeed, got: %s", result.ForLLM)
	}
}

// TestDelegateTool_Respond_3P_OriginalNotLeftRunning proves Correctness-MAJOR-2:
// for a 3P child, respond spawns a NEW corrective session (spawnCorrective-
// FollowUp) and must NOT flip the ORIGINAL record to `running` (which would
// leave a live-turn-less record the Phase-2 boot sweep re-classifies
// failed(interrupted), corrupting the terminal record). The original is
// instead marked terminal `cancelled` (superseded by corrective re-dispatch).
func TestDelegateTool_Respond_3P_OriginalNotLeftRunning(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-3p-resp", State: session.LifecycleNeedsInput,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker-3p", LaunchProfile: session.LaunchProfileSpecialist,
		Is3P:       true,
		NeedsInput: &session.NeedsInput{CorrelationID: "corr-3p", TTLDeadline: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := inbox.Append("parent-1", questionMsgForDelegateTest(t, "child-3p-resp", "q-3p", "corr-3p",
		generated.SelfOk)); err != nil {
		t.Fatalf("seed question failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-3p-resp", "correlation_id": "corr-3p", "text": "go",
	})
	if result.IsError {
		t.Fatalf("3P respond failed: %s", result.ForLLM)
	}

	// The ORIGINAL session must NOT be at running. It must be terminal
	// `cancelled` (superseded) — never a live-turn-less running record.
	orig, err := lc.Load("child-3p-resp")
	if err != nil {
		t.Fatalf("Load original: %v", err)
	}
	if orig.State == session.LifecycleRunning {
		t.Fatalf("ORIGINAL 3P session left at running with no live turn — Phase-2 boot sweep would " +
			"re-classify it failed(interrupted), corrupting the terminal record (Correctness-MAJOR-2)")
	}
	if orig.State != session.LifecycleCancelled {
		t.Errorf("original state = %q, want %q (superseded by corrective re-dispatch)", orig.State, session.LifecycleCancelled)
	}
	if !orig.Terminal() {
		t.Errorf("original state %q is not terminal — it must be terminal so it is never re-classified", orig.State)
	}
	if orig.FailedReason == "" {
		t.Error("expected a non-empty FailedReason recording the supersession")
	}

	// A NEW corrective session must exist, linked back via ResumedFrom, on a
	// DIFFERENT session_id (3P cold respawn, D5).
	all, err := lc.List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var successor *session.LifecycleRecord
	for i := range all {
		if all[i].ResumedFrom == "child-3p-resp" && all[i].SessionID != "child-3p-resp" {
			successor = &all[i]
			break
		}
	}
	if successor == nil {
		t.Fatal("expected a NEW corrective session (ResumedFrom=child-3p-resp, different id) to be spawned")
	}
}

// TestDelegateTool_Inbox_DeniedOnLifecycleLoadError proves Security-MAJOR-1:
// executeInbox MUST be denied (not drained) when the lifecycle Load errors —
// the caller-ownership check can no longer be skipped by an induced read error
// (cross-tenant inbox leak). Previously `if lerr == nil { verify }` skipped
// verification on ANY Load error and drained regardless.
func TestDelegateTool_Inbox_DeniedOnLifecycleLoadError(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-inbox-corrupt", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Seed a real message in the inbox that the fail-open bug WOULD have leaked.
	if _, err := inbox.Append("parent-1", progressMsgForDelegateTest(t, "child-inbox-corrupt", "leak-1")); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}
	// Corrupt the lifecycle tail so Load errors.
	if err := os.WriteFile(filepath.Join(lc.Dir(), "child-inbox-corrupt.jsonl"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{"action": "inbox", "session_id": "child-inbox-corrupt"})
	if !result.IsError {
		t.Fatal("expected inbox to be DENIED on a lifecycle Load error, got success (fail-open regression)")
	}
}

// TestDelegateTool_Inbox_DeniedWhenLifecycleUnconfigured proves the
// Security-MAJOR-1 fail-closed posture: no lifecycle store → no ownership
// verification → inbox denied (never leaks messages without verifying the
// caller owns the session).
func TestDelegateTool_Inbox_DeniedWhenLifecycleUnconfigured(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	// inbox configured but NO lifecycle store — the prior code treated
	// lifecycle as optional enrichment and drained anyway.
	inbox := session.NewMessageInboxStore(t.TempDir())
	tool.SetMessageInbox(inbox)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	result := tool.Execute(ctx, map[string]any{"action": "inbox", "session_id": "any"})
	if !result.IsError {
		t.Fatal("expected inbox DENIED when no lifecycle store configured, got success (fail-open)")
	}
}

// TestDelegateTool_Peek_DeniedOnLifecycleLoadError proves Security-MAJOR-1 for
// executePeek: a Load error MUST deny peek (the prior `if lerr == nil` pattern
// leaked the victim's lifecycle state + persisted messages on any read error).
func TestDelegateTool_Peek_DeniedOnLifecycleLoadError(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-peek-corrupt", State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker", LaunchProfile: session.LaunchProfileUtility,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if _, err := inbox.Append("parent-1", progressMsgForDelegateTest(t, "child-peek-corrupt", "peek-leak")); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(lc.Dir(), "child-peek-corrupt.jsonl"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{"action": "peek", "session_id": "child-peek-corrupt"})
	if !result.IsError {
		t.Fatal("expected peek DENIED on a lifecycle Load error, got success (fail-open regression)")
	}
	if strings.Contains(result.ForLLM, "peek-leak") {
		t.Errorf("peek leaked inbox content despite the Load error: %s", result.ForLLM)
	}
}
