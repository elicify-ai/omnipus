// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for ADR-053 §5.1's corrected delegate action set
// (inbox/inbox_ack/steer/respond/stop_all/resume/redirect/peek — ADR-20261004
// renamed follow_up→resume and cancel→stop_all with no alias) plus the illegal
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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// fakeSteeringSink implements DelegateSteeringSink for tests.
type fakeSteeringSink struct {
	mu        sync.Mutex
	delivered []providers.Message
	scopes    []string
}

func (f *fakeSteeringSink) EnqueueSteeringMessage(scope, agentID string, msg providers.Message, correlationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.delivered = append(f.delivered, msg)
	f.scopes = append(f.scopes, scope)
	if correlationID == "" {
		correlationID = "corr_test_" + strconv.Itoa(len(f.delivered))
	}
	return correlationID, nil
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
// deny gate, and a recording launcher — enough to exercise the full ADR-053 action
// set end to end.
func newADR053TestTool(t *testing.T) (*DelegateTool, *session.LifecycleStore, *session.MessageInboxStore, *fakeSteeringSink) {
	t.Helper()
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetSessionLauncher(&recordingSessionLauncher{})
	tool.SetDelegationDenyCheckerBackground(func(ctx context.Context, targetAgentID string) *DelegationDenial { return nil })
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

	return tool, lc, inbox, steer
}

// runAndExtractSessionID launches a delegated task and returns the durable
// session_id from the generated response payload.
func runAndExtractSessionID(t *testing.T, tool *DelegateTool, ctx context.Context, task string) string {
	t.Helper()
	// Delegation always names an explicit target (settled design); the caller's
	// own id is the natural target for these launch-path fixtures.
	result := tool.Execute(ctx, map[string]any{"task": task, "agent_id": ToolAgentID(ctx)})
	if result.IsError {
		t.Fatalf("run failed: %s", result.ForLLM)
	}
	payload, _, _ := strings.Cut(result.ForLLM, "\n")
	var response generated.DelegateSessionResponse
	if err := json.Unmarshal([]byte(payload), &response); err != nil {
		t.Fatalf("decode run response: %v; payload: %s", err, result.ForLLM)
	}
	if response.SessionId == "" {
		t.Fatalf("run response has empty session_id: %s", result.ForLLM)
	}
	return response.SessionId
}

func TestDelegateTool_Run_SnapshotOverCap_Rejected(t *testing.T) {
	tool, _, _, _ := newADR053TestTool(t) //nolint:dogsled // Only the tool is relevant from the four test fixtures.
	refs := make([]any, 100)              // over the default 50-ref cap
	for i := range refs {
		refs[i] = "ref"
	}
	result := tool.Execute(context.Background(), map[string]any{
		"task":     "do something",
		"agent_id": "worker",
		"snapshot": map[string]any{"references": refs},
	})
	if !result.IsError {
		t.Fatal("expected an error for an over-cap snapshot, got success")
	}
	if !strings.Contains(result.ForLLM, "narrow the snapshot") {
		t.Errorf("expected a narrow-the-snapshot error, got: %s", result.ForLLM)
	}
}

func TestDelegateTool_Run_PassesSteeringSessionToLauncher(t *testing.T) {
	tool, launcher := u14PermissiveTool(t)
	// FR-015: the lifecycle mint fails closed without a resolvable
	// delegating agent, so a run context must carry one — the agent loop
	// injects it unconditionally on the turn path (pkg/agent/loop.go).
	ctx := WithAgentID(WithTranscriptSessionID(context.Background(), "parent-1"), "mia")

	sessionID := runAndExtractSessionID(t, tool, ctx, "background task")
	if sessionID == "" {
		t.Fatal("expected a non-empty session_id")
	}

	if sessionID != launcher.launchRes.SessionID {
		t.Fatalf("response session_id = %q, launcher returned %q", sessionID, launcher.launchRes.SessionID)
	}
	if launcher.launchReq.SteeringSessionID != "parent-1" {
		t.Errorf("LaunchRequest.SteeringSessionID = %q, want %q", launcher.launchReq.SteeringSessionID, "parent-1")
	}
	if launcher.dispatchSessionID != sessionID || launcher.dispatchGeneration != launcher.launchRes.Generation {
		t.Errorf("Dispatch = (%q, %d), want (%q, %d)", launcher.dispatchSessionID, launcher.dispatchGeneration, sessionID, launcher.launchRes.Generation)
	}
}

func TestDelegateTool_Steer_DeliversViaSteeringSink(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	// Manually seed a running child record (bypassing run's async
	// completion race for a deterministic steer test).
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-x", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
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
		SessionID: "child-caps", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Rate cap 6/min; the body cap is left at the product default
	// (session.DefaultSteerBodyBytes, C-LIMIT 65,536 B — ADR-053 §Contract
	// Surface "Caps"), which is what the body half below exercises.
	tool.SetSteerCaps(6, session.DefaultSteerBodyBytes)

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
		SessionID: "child-body", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// The body cap is session.DefaultSteerBodyBytes (C-LIMIT 65,536 B), derived
	// here from the constant rather than hardcoded: one byte over the cap must
	// be rejected, one byte under must not. (The old literal was 17 KiB — well
	// UNDER the real 64 KiB cap, so it could never trip the check.)
	big := strings.Repeat("x", session.DefaultSteerBodyBytes+1)
	result := tool2.Execute(ctx, map[string]any{"action": "steer", "session_id": "child-body", "text": big})
	if !result.IsError {
		t.Fatalf("expected a %d-byte steer body (one over the %d-byte cap) to be rejected",
			session.DefaultSteerBodyBytes+1, session.DefaultSteerBodyBytes)
	}
}

func TestDelegateTool_Steer_RejectsCrossOwnerAccess(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-y", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-A", RootSessionID: "parent-A"},
		WorkspaceID: "ws-1", AgentID: "worker",
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

// TestDelegateTool_Steer_Succeeds proves steering a running delegation
// always succeeds — there is no launch-profile gate on it (the removed
// utility/specialist distinction, see ADR-053 Amendment).
func TestDelegateTool_Steer_Succeeds(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-steer", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-steer", "text": "focus on the tests",
	})
	if result.IsError {
		t.Fatalf("expected steer on a running delegation to succeed, got: %s", result.ForLLM)
	}
	msg, scope := steer.last()
	if msg.Content != "focus on the tests" {
		t.Errorf("delivered content = %q, want %q", msg.Content, "focus on the tests")
	}
	if scope != "child-steer" {
		t.Errorf("delivered scope = %q, want %q", scope, "child-steer")
	}
}

// TestDelegateTool_Steer_RejectsExternalCLI is a regression test for the 3P
// steer gap: every steering-queue drain site lives in the native turn engine
// (pkg/agent/loop.go, pkg/agent/steering.go) and runExternalCLISubTurn
// (pkg/agent/external_dispatch.go) never drains it, so a steer queued
// against an Is3P (external-CLI) child was previously accepted with a
// misleading "queued" success and then silently orphaned forever — no live
// consumer ever reads it. executeSteer must now reject it outright, before
// ever reaching the steering sink.
func TestDelegateTool_Steer_RejectsExternalCLI(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-3p", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker", Is3P: true,
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "steer", "session_id": "child-3p", "text": "focus on the tests",
	})
	if !result.IsError {
		t.Fatalf("expected steer on an external-CLI (3P) session to be rejected, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "external-CLI") && !strings.Contains(result.ForLLM, "external CLI") {
		t.Errorf("expected the rejection to name external-CLI/3P as the reason, got: %s", result.ForLLM)
	}
	if msg, _ := steer.last(); msg.Content != "" {
		t.Errorf("steer must never reach the steering sink for a 3P session, got delivered content %q", msg.Content)
	}
}

// TestDelegateTool_Respond_WorkingChild_QueuesWithoutStopping is the
// ADR-20261004 locked-decision-5 rule at this boundary: the parent's answer to
// a WORKING helper is an ordinary steering message. It lands in the child's
// steering queue (applied at the child's next tool boundary), never stops the
// child's live turn, never parks it, and never dispatches a second session.
func TestDelegateTool_Respond_WorkingChild_QueuesWithoutStopping(t *testing.T) {
	tool, lc, inbox, steer := newADR053TestTool(t)
	seedOpenQuestion(t, inbox, "parent-1", "child-z", "corr-1")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-z", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed lifecycle record failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-z", "correlation_id": "corr-1", "text": "yes, go ahead",
	})
	if result.IsError {
		t.Fatalf("respond failed: %s", result.ForLLM)
	}
	msg, scope := steer.last()
	if msg.Content != "yes, go ahead" {
		t.Errorf("steering sink content = %q, want the raw answer text (an ordinary message, not a parked-record replay)", msg.Content)
	}
	if scope != "child-z" {
		t.Errorf("steering sink scope = %q, want the child's own scope", scope)
	}
	rec, err := lc.Load("child-z")
	if err != nil {
		t.Fatalf("Load after respond failed: %v", err)
	}
	if rec.State != session.LifecycleRunning || rec.Generation != 1 {
		t.Errorf("state/generation after respond = (%s, %d), want the helper untouched (running, 1) — a mid-turn answer must not stop or restart the turn",
			rec.State, rec.Generation)
	}
	if rec.NeedsInput != nil {
		t.Error("a question no longer parks the helper: NeedsInput must stay nil (ADR-20261004 removed the person-question pause)")
	}
}

// TestDelegateTool_Respond_UnmatchedCorrelation_IsRefused is the founder
// decision of 2026-10-07 (#1213), which REVERSES the ADR-20261004 rule this
// test used to pin ("correlation_id is address metadata, never a check; an
// unmatched id must still be delivered"). respond now resolves the id against
// the child's open questions and refuses an unknown one — the old behaviour
// told a parent it had unblocked a child when it had not. The refusal text and
// the already-answered case are covered in delegate_respond_correlation_test.go.
// FLAGGED FOR qa-lead CHECK: expectation changed by founder decision.
func TestDelegateTool_Respond_UnmatchedCorrelation_IsRefused(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-w", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-w", "correlation_id": "corr-WRONG", "text": "x",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "unknown_correlation_id") {
		t.Fatalf("respond with an unmatched correlation_id must be refused as unknown_correlation_id, got: %s", result.ForLLM)
	}
	if msg, _ := steer.last(); msg.Content != "" {
		t.Errorf("a refused respond delivered %q", msg.Content)
	}
}

// TestDelegateTool_Respond_RejectsCrossOwnerAccess (sec-MAJOR-3) proves the
// producer-side parent-of gate covers the respond action: a session that is
// NOT the recorded parent of the target child cannot respond to it. This is
// the steer/response counterpart of TestDelegateTool_Steer_RejectsCrossOwnerAccess
// — verifyCallerPrincipal (delegate.go) runs before any delivery, so ownership
// survives the ADR-20261004 removal of the question-authority check.
func TestDelegateTool_Respond_RejectsCrossOwnerAccess(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-co", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-A", RootSessionID: "parent-A"},
		WorkspaceID: "ws-1", AgentID: "worker",
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
	if !strings.Contains(result.ForLLM, "not steered") {
		t.Errorf("expected the rejection to cite ownership, got: %s", result.ForLLM)
	}
}

// stopAllReplyFor is the one reply text stop_all gives (founder one-stop
// decision, 2026-10-05; team-lead ruling). The old cooperative / hard / dropped
// -queued wordings are superseded: there is one stop, so there is one reply.
func stopAllReplyFor(sessionID string) string {
	return "Stop requested for session " + sessionID + " and its helpers; " +
		"they will show as stopped once their running work has shut down."
}

// stopHookRecorder is the single stop hook the tool calls. It records every
// call and, like the real owner, lands nothing synchronously.
type stopHookRecorder struct {
	mu    sync.Mutex
	calls []stopHookCall
}

type stopHookCall struct {
	sessionID string
	by        steer.Principal
	hint      string
}

func (r *stopHookRecorder) hook(reached ...string) func(string, steer.Principal, string) ([]string, error) {
	return func(sessionID string, by steer.Principal, hint string) ([]string, error) {
		r.mu.Lock()
		r.calls = append(r.calls, stopHookCall{sessionID: sessionID, by: by, hint: hint})
		r.mu.Unlock()
		if len(reached) == 0 {
			return []string{sessionID}, nil
		}
		return reached, nil
	}
}

func (r *stopHookRecorder) snapshot() []stopHookCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]stopHookCall(nil), r.calls...)
}

// TestDelegateTool_StopAll_CallsTheOneStopHookOnce_NoToolSideBackstop pins the
// agent entry of the unified stop: stop_all asks the ONE stop hook exactly
// once, with the verified caller as the recorded principal, answers with the
// single reply text, and the tool itself never lands a state or fires a second
// (grace/backstop) call afterwards -- the 3 s forced stop belongs to the one
// stop method, not to a tool-side timer.
func TestDelegateTool_StopAll_CallsTheOneStopHookOnce_NoToolSideBackstop(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-cancel", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	before, err := lc.Load("child-cancel")
	if err != nil {
		t.Fatalf("load before: %v", err)
	}

	rec := &stopHookRecorder{}
	// TARGET API (the implementer provides it): the delegate tool has exactly
	// ONE stop hook; SetCancelHooks(soft, hard) and SetCancelGrace are gone.
	tool.SetStopHook(rec.hook())

	result := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": "child-cancel"})
	if result.IsError {
		t.Fatalf("stop_all failed: %s", result.ForLLM)
	}
	if want := stopAllReplyFor("child-cancel"); result.ForLLM != want {
		t.Errorf("stop_all reply = %q, want exactly %q", result.ForLLM, want)
	}
	calls := rec.snapshot()
	if len(calls) != 1 {
		t.Fatalf("stop hook called %d times immediately, want exactly 1", len(calls))
	}
	if calls[0].sessionID != "child-cancel" {
		t.Errorf("stop hook asked to stop %q, want %q", calls[0].sessionID, "child-cancel")
	}
	// Principal recorded as today: the verified calling agent session.
	if want := (steer.Principal{Kind: steer.PrincipalKindAgent, ID: "parent-1"}); calls[0].by != want {
		t.Errorf("stop hook principal = %+v, want %+v", calls[0].by, want)
	}

	// The unified timeline's forced stop is 3 s; outlive it. A tool-side
	// backstop timer (the retired 5 s grace goroutine, or any replacement)
	// would show up as a second hook call or a tool-written state.
	time.Sleep(3500 * time.Millisecond)
	if n := len(rec.snapshot()); n != 1 {
		t.Errorf("stop hook called %d times after 3.5s, want still 1 -- the tool must not run its own backstop", n)
	}
	after, err := lc.Load("child-cancel")
	if err != nil {
		t.Fatalf("load after: %v", err)
	}
	if after.State != before.State || after.Stop != nil || after.StopNote != nil {
		t.Errorf("the tool changed the lifecycle record itself: before=%+v after=%+v -- only the owning execution lands a stop", before, after)
	}
}

// TestDelegateTool_StopAll_QueuedSessionToolWritesNothing: a stop reaching a
// still-queued session gets the same single reply and the tool lands nothing
// itself (the retired tool-side queued-drop writer). The owner lands it; the
// real queued behaviour is pinned in pkg/agent
// (TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts).
func TestDelegateTool_StopAll_QueuedSessionToolWritesNothing(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-queued", Generation: 1, State: session.LifecycleQueued,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	rec := &stopHookRecorder{}
	tool.SetStopHook(rec.hook())

	result := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": "child-queued"})
	if result.IsError {
		t.Fatalf("stop_all on a queued session failed: %s", result.ForLLM)
	}
	if want := stopAllReplyFor("child-queued"); result.ForLLM != want {
		t.Errorf("stop_all reply = %q, want exactly %q", result.ForLLM, want)
	}
	got, err := lc.Load("child-queued")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.State != session.LifecycleQueued || got.StopNote != nil || got.Stop != nil {
		t.Errorf("the tool wrote the queued record itself: %+v -- the owning stop lands it, never the tool", got)
	}
}

// TestDelegateTool_StopAll_HookErrorIsVisible: a failing stop is a visible
// error to the calling agent, never a quiet success.
func TestDelegateTool_StopAll_HookErrorIsVisible(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-fail", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	tool.SetStopHook(func(string, steer.Principal, string) ([]string, error) {
		return nil, errors.New("partial cascade: unreachable 1 (grand-1: unreadable record)")
	})
	result := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": "child-fail"})
	if !result.IsError {
		t.Fatalf("a failed stop was reported as success: %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "unreachable 1 (grand-1: unreadable record)") {
		t.Errorf("the error must carry the cascade failure detail, got %q", result.ForLLM)
	}
}

// TestDelegateTool_StopAll_RejectsHardArgument: the agent-only `hard` option is
// removed (one stop mechanism). A call carrying it fails the tool's existing
// schema validation as an unexpected property, before any hook runs, and the
// published schema no longer declares the property. The positive control
// proves the same call without `hard` reaches the hook, so the rejection is
// about the argument, not about the fixture.
func TestDelegateTool_StopAll_RejectsHardArgument(t *testing.T) {
	for _, hard := range []bool{true, false} {
		tool, lc, _, _ := newADR053TestTool(t)
		ctx := WithTranscriptSessionID(context.Background(), "parent-1")
		if err := lc.Persist(&session.LifecycleRecord{
			SessionID: "child-hard", Generation: 1, State: session.LifecycleRunning,
			OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
			WorkspaceID: "ws-1", AgentID: "worker",
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
		rec := &stopHookRecorder{}
		tool.SetStopHook(rec.hook())
		reg := NewToolRegistry()
		reg.Register(tool)

		props, _ := tool.Parameters()["properties"].(map[string]any)
		if _, declared := props["hard"]; declared {
			t.Errorf("hard=%v: the delegate schema still declares a `hard` property", hard)
		}

		result := reg.ExecuteWithContext(ctx, "delegate",
			map[string]any{"action": "stop_all", "session_id": "child-hard", "hard": hard}, "", "", nil)
		if result == nil || !result.IsError {
			t.Fatalf("hard=%v: stop_all carrying `hard` was accepted: %+v", hard, result)
		}
		if !strings.Contains(result.ForLLM, `invalid arguments for tool "delegate"`) ||
			!strings.Contains(result.ForLLM, `unexpected property "hard"`) {
			t.Errorf("hard=%v: want the schema-validation rejection naming the unexpected property, got %q", hard, result.ForLLM)
		}
		if n := len(rec.snapshot()); n != 0 {
			t.Errorf("hard=%v: the stop hook ran %d time(s) for a rejected call", hard, n)
		}

		// Positive control: same call minus `hard` is accepted and stops once.
		ok := reg.ExecuteWithContext(ctx, "delegate",
			map[string]any{"action": "stop_all", "session_id": "child-hard"}, "", "", nil)
		if ok == nil || ok.IsError {
			t.Fatalf("hard=%v control: stop_all without `hard` failed: %+v", hard, ok)
		}
		if n := len(rec.snapshot()); n != 1 {
			t.Errorf("hard=%v control: the stop hook ran %d times, want 1", hard, n)
		}
	}
}

func TestDelegateTool_InboxAndInboxAck(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-inbox", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
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
		SessionID: "child-peek", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
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

// TestDelegateTool_Resume_WorkingSession_IsNonErrorNothingStarted pins locked
// decision 4's working row: a resume names only stopped and done/failed
// recipients. A working helper has nothing to resume — the answer is a
// NON-ERROR pointing at steer (an error here drives orchestrating agents into
// retry loops, the RC-3 amplification shape).
func TestDelegateTool_Resume_WorkingSession_IsNonErrorNothingStarted(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-notdone", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	result := tool.Execute(ctx, map[string]any{"action": "resume", "session_id": "child-notdone"})
	if result.IsError {
		t.Fatalf("resume on a working helper must be a non-error no-op, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "already running") {
		t.Errorf("resume on a working helper must say it is already running, got: %s", result.ForLLM)
	}
	if msg, _ := steer.last(); msg.Content != "" {
		t.Errorf("resume must not enqueue anything into the steering sink, got %q", msg.Content)
	}
}

// TestDelegateTool_Resume_NativeStoppedAndFinished_RevivesViaSink pins the
// native resume contract at this boundary (locked decision 4): BOTH recipient
// shapes go through the steering sink's ReviveStoppedSession — a stopped
// helper continues on the same conversation and generation, a done/failed one
// starts its next round — and the optional text rides along as the newest
// input. The generation mint itself is the agent-side reviver's job; the
// tool's launcher must never fire for a native resume.
func TestDelegateTool_Resume_NativeStoppedAndFinished_RevivesViaSink(t *testing.T) {
	for _, tc := range []struct {
		name     string
		state    session.LifecycleState
		wantText string
	}{
		{
			name:     "stopped helper continues on the same conversation",
			state:    session.LifecycleStopped,
			wantText: "resumed on the same conversation and generation",
		},
		{
			name:     "finished helper starts its next round",
			state:    session.LifecycleCompleted,
			wantText: "a next round has been started",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool, lc, _, _ := newADR053TestTool(t)
			frs := &fakeReviverSink{}
			tool.SetSteeringSink(frs)
			ctx := WithTranscriptSessionID(context.Background(), "parent-1")
			seed := &session.LifecycleRecord{
				SessionID: "child-resume-" + string(tc.state), Generation: 1, State: tc.state,
				OwnerScopeKind: session.OwnerScopeHuman,
				SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
				WorkspaceID:    "ws-1", AgentID: "worker",
				Is3P: false,
			}
			if tc.state == session.LifecycleStopped {
				// persistLocked requires a StopNote on every stopped landing
				// (D2/CRIT-001) — the exact shape SteerCanceller leaves behind.
				seed.StopNote = &session.StopNote{
					At: time.Now().UTC(), By: "human:dan", Seq: 1, Cause: session.StopCauseStop,
				}
			}
			if err := lc.Persist(seed); err != nil {
				t.Fatalf("seed failed: %v", err)
			}

			result := tool.Execute(ctx, map[string]any{
				"action": "resume", "session_id": seed.SessionID, "text": "carry on from here",
			})
			if result.IsError {
				t.Fatalf("resume failed: %s", result.ForLLM)
			}
			revived, sessionID, instruction, delivered := frs.snapshot()
			if !revived {
				t.Fatalf("native resume must revive through the steering sink's ReviveStoppedSession (delivered=%d via the plain queue instead)", delivered)
			}
			if sessionID != seed.SessionID {
				t.Errorf("ReviveStoppedSession session = %q, want %q (the SAME conversation)", sessionID, seed.SessionID)
			}
			if instruction != "carry on from here" {
				t.Errorf("ReviveStoppedSession instruction = %q, want the resume text carried through verbatim", instruction)
			}
			if !strings.Contains(result.ForLLM, tc.wantText) {
				t.Errorf("resume result = %q, want it to say %q", result.ForLLM, tc.wantText)
			}
		})
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

// questionMsgForDelegateTest was deleted with the person-question pause
// (ADR-20261004): the generated SessionMessageQuestion no longer carries
// wait/authority fields, and no respond-path test needs a seeded question any
// more — an answer is an ordinary steering message, verified by no inbox
// state.

// failingDrainInbox implements DelegateInboxStore; its Drain always returns an
// error. It now proves the ADR-20261004 ordinary-message rule from the negative
// side: respond never consults the parent inbox at all (the question-authority
// check it used to feed is deleted), so a broken inbox cannot block an answer.
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

// MAJOR-1: stop_all MUST be denied (not executed) when the lifecycle Load
// errors — the caller-ownership check can no longer be skipped by an induced
// read error (cross-tenant DoS). Previously the stop fell through to
// cancelHard/cancelSoft on ANY Load error.
func TestDelegateTool_StopAll_DeniedOnLifecycleLoadError(t *testing.T) {
	tool, lc, _, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	// Seed a record owned by parent-1, then CORRUPT its JSONL tail so Load
	// errors (the exact fail-open trigger MAJOR-1 targets).
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-corrupt", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	// Overwrite the lifecycle file with garbage so tail() scan-errors.
	if err := os.WriteFile(filepath.Join(lc.Dir(), "child-corrupt.jsonl"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("corrupt lifecycle file: %v", err)
	}

	cancelCalled := false
	tool.SetStopHook(func(string, steer.Principal, string) ([]string, error) { cancelCalled = true; return nil, nil })

	result := tool.Execute(ctx, map[string]any{"action": "stop_all", "session_id": "child-corrupt"})
	if !result.IsError {
		t.Fatal("expected stop_all to be DENIED on a lifecycle Load error, got success (fail-open regression)")
	}
	if cancelCalled {
		t.Fatal("stop hook was called despite the Load error — the stop must NOT proceed")
	}
}

// MAJOR-1: stop_all MUST be denied when the lifecycle store is not configured
// at all (can't verify ownership without it).
func TestDelegateTool_StopAll_DeniedWhenLifecycleUnconfigured(t *testing.T) {
	tool := NewDelegateTool("test-model", 0, 0)
	tool.SetDelegationDenyCheckerBackground(func(ctx context.Context, targetAgentID string) *DelegationDenial { return nil })
	tool.SetStopHook(func(string, steer.Principal, string) ([]string, error) {
		t.Fatal("stop hook must not fire")
		return nil, nil
	})
	result := tool.Execute(context.Background(), map[string]any{"action": "stop_all", "session_id": "any"})
	if !result.IsError {
		t.Fatal("expected stop_all to be DENIED when no lifecycle store is configured, got success (fail-open)")
	}
}

// TestDelegateTool_Respond_RefusesWhenTheInboxCannotVerify: since the founder
// decision of 2026-10-07 (#1213) respond resolves correlation_id against the
// inbox, so an inbox that cannot do so (here a fake lacking the lookup) makes
// respond fail closed and visibly — it never delivers an unverifiable answer.
// This REPLACES the old DoesNotConsultTheInbox expectation; FLAGGED FOR
// qa-lead CHECK.
func TestDelegateTool_Respond_RefusesWhenTheInboxCannotVerify(t *testing.T) {
	tool, lc, _, steer := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-drain-err", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	tool.SetMessageInbox(failingDrainInbox{})

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-drain-err", "correlation_id": "corr-1", "text": "x",
	})
	if !result.IsError || !strings.Contains(result.ForLLM, "cannot verify correlation_id") {
		t.Fatalf("respond must fail closed when the inbox cannot verify, got: %s", result.ForLLM)
	}
	if msg, _ := steer.last(); msg.Content != "" {
		t.Errorf("an unverifiable answer was delivered: %q", msg.Content)
	}
}

// TestDelegateTool_Respond_OpenQuestionNeedsNoOwnerAuthority keeps the part
// of ADR-20261004 locked decision 6 that stands: there is no owner-only
// question and no authority state — an open question is answered by any
// authorized caller and the helper is left untouched (running, same
// generation). Only the correlation_id check is new (founder 2026-10-07,
// #1213). Replaces HasNoQuestionAuthorityGate, whose empty-inbox success is
// now refused; FLAGGED FOR qa-lead CHECK.
func TestDelegateTool_Respond_OpenQuestionNeedsNoOwnerAuthority(t *testing.T) {
	tool, lc, inbox, _ := newADR053TestTool(t)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-owner", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	seedOpenQuestion(t, inbox, "parent-1", "child-owner", "corr-owner")

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-owner", "correlation_id": "corr-owner", "text": "x",
	})
	if result.IsError {
		t.Fatalf("respond to an open question must not be gated on any authority state, got: %s", result.ForLLM)
	}
	rec, err := lc.Load("child-owner")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if rec.State != session.LifecycleRunning || rec.Generation != 1 {
		t.Errorf("state/generation after respond = (%s, %d), want the helper untouched (running, 1)", rec.State, rec.Generation)
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
		SessionID: "child-inbox-corrupt", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
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
		SessionID: "child-peek-corrupt", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker",
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

// TestDelegateStopAllContract_HasNoHardProperty: contract-first (Hard
// Constraint 8). The wire contract for the delegate stop_all action must stop
// advertising the removed agent-only `hard` option (founder one-stop decision,
// 2026-10-05) -- it declares additionalProperties:false, so a client generated
// from a contract that still lists `hard` would keep sending a field the tool
// now rejects. Oracle: the decision file, read through the contract YAML.
func TestDelegateStopAllContract_HasNoHardProperty(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "components", "schemas", "DelegateStopAllAction.yaml"))
	if err != nil {
		t.Fatalf("read the stop_all contract: %v", err)
	}
	var doc struct {
		Properties map[string]any `yaml:"properties"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse the stop_all contract: %v", err)
	}
	if len(doc.Properties) == 0 {
		t.Fatalf("instrument: the contract parsed with no properties (action/session_id expected)")
	}
	if _, ok := doc.Properties["session_id"]; !ok {
		t.Fatalf("instrument: the contract parsed without session_id; the parse is not reading the schema")
	}
	if _, stillThere := doc.Properties["hard"]; stillThere {
		t.Errorf("DelegateStopAllAction still declares a `hard` property; the agent-only hard option is removed")
	}
	if strings.Contains(string(raw), "cancel_grace") {
		t.Errorf("DelegateStopAllAction still documents session_messaging.cancel_grace; the setting is removed")
	}
}
