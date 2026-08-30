// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for the native `delegate follow_up` warm-resume defect
// (UAT 2026-07-31/08-03, reproduced independently by two agents): follow_up
// on a terminal session ALWAYS routed the resume through
// agent.spawnSubTurn's CreateSessionWithID collision guard (FR-096) — since
// the "new" child session id it reused verbatim was the SAME session it
// meant to resume, the create always collided, spawnSubTurn returned a
// non-nil error, and the caller had already received a well-formed
// AsyncResult ack (correct session_id, "running in background") before that
// error ever surfaced anywhere. Nothing ever ran.
//
// This file proves the WIRING half at the pkg/tools boundary:
//   - spawnCorrectiveFollowUp now marks the dispatch tools.SubTurnConfig.
//     IsResume (true for a native resume, which reuses the session id
//     verbatim; false for a 3P cold respawn, which mints a brand new
//     session id and is a genuine create like any other dispatch).
//   - any async spawn failure is now logged unconditionally (grep-able in
//     gateway.log) regardless of whether a downstream callback happens to
//     do anything with the result, and the affected session's lifecycle
//     record is left in a discoverable Failed state.
//
// The end-to-end proof that a warm resume no longer collides with FR-096 —
// exercised against the REAL agent.spawnSubTurn and a REAL store-backed
// session, not this package's spy spawner — lives in
// pkg/agent/subturn_followup_resume_test.go. That test ALSO re-proves
// FR-096's collision guard is fully intact for a genuine create (IsResume
// left false) — the most important regression check for this change.
package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestDelegateTool_FollowUp_NativeSetsIsResume(t *testing.T) {
	spawner := &capturingDelegateSpawner{}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-resume-native", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker",
		Is3P: false,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-followup-resume-native", "text": "resume please",
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()

	cfg := spawner.lastConfig()
	if !cfg.IsResume {
		t.Error("native follow_up (Is3P:false) must set SubTurnConfig.IsResume=true — it reuses the " +
			"terminal session's own id verbatim, and the spawner must treat this as a warm resume rather " +
			"than attempt to create a session that already exists (the exact FR-096 collision this fix closes)")
	}
	if cfg.DelegateSessionID != "child-followup-resume-native" {
		t.Errorf("native follow_up must reuse the session id verbatim, got DelegateSessionID=%q", cfg.DelegateSessionID)
	}
}

func TestDelegateTool_FollowUp_3PDoesNotSetIsResume(t *testing.T) {
	spawner := &capturingDelegateSpawner{}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSpawner(spawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-resume-3p", State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "claude-code",
		Is3P: true,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": "child-followup-resume-3p", "text": "resume please",
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()

	cfg := spawner.lastConfig()
	if cfg.IsResume {
		t.Error("a 3P follow_up mints a brand-new session id (cold respawn, D5) — it is a genuine create " +
			"like any other dispatch and must NOT set IsResume")
	}
	if cfg.DelegateSessionID == "child-followup-resume-3p" {
		t.Error("a 3P follow_up must mint a NEW session id, not reuse the terminal one verbatim")
	}
}

// TestDelegateTool_FollowUp_SpawnFailure_LoggedUnconditionally proves the
// "kill the silent swallow" half: even when the underlying spawn genuinely
// fails (any reason — this test simulates it directly via a failing
// spawner, standing in for e.g. a resume target that vanished on disk
// between Load and dispatch), the failure is (a) logged at Error level
// UNCONDITIONALLY — not merely handed to a callback that might do nothing
// visible with it — and (b) reflected in the lifecycle record as Failed, so
// a subsequent delegate(status)/peek poll can discover it. Before this fix,
// executeAsync's goroutine only ever logged on error when cb was nil
// ("subturn failed with no callback") — a real agent-turn cb is never nil,
// so a doomed follow_up's failure had NO unconditional, cb-independent log
// line at all.
func TestDelegateTool_FollowUp_SpawnFailure_LoggedUnconditionally(t *testing.T) {
	getLogs := captureLogs(t)

	const sessionID = "child-followup-resume-logging"
	failingSpawner := spawnerFunc(func(_ context.Context, _ SubTurnConfig) (*ToolResult, error) {
		return nil, fmt.Errorf("subturn: resume child session %q: simulated store failure", sessionID)
	})

	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSpawner(failingSpawner)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: sessionID, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman, ParentDurableKey: "parent-1",
		WorkspaceID: "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	// Deliberately provide a NON-NIL callback via ExecuteAsync — the
	// realistic production shape (pkg/agent/loop.go's asyncCallback closure
	// is never nil for a real agent turn). Before this fix, executeAsync's
	// goroutine ONLY logged unconditionally when cb WAS nil ("delegate:
	// subturn failed with no callback") — a failure handed to a non-nil cb
	// had no cb-independent log line at all, regardless of what that
	// callback's own downstream (e.g. AsyncNotifier.Notify landing somewhere
	// an operator isn't watching) did with it. This callback intentionally
	// does NOTHING with the result — the strongest version of "downstream
	// does nothing observable" — so the ONLY way this test's log assertions
	// below can pass is via the new unconditional log line.
	cb := func(context.Context, *ToolResult) {}

	// The synchronous ack is still a well-formed AsyncResult (fire-and-forget
	// dispatch is unchanged design, documented at executeAsync's own return) —
	// this test is about what happens to the FAILURE, not this initial ack.
	result := tool.ExecuteAsync(ctx, map[string]any{
		"action": "follow_up", "session_id": sessionID, "text": "resume please",
	}, cb)
	if result.IsError {
		t.Fatalf("the synchronous follow_up ack itself must not be an error (fire-and-forget dispatch): %s", result.ForLLM)
	}
	tool.WaitForAsyncTasks()

	logs := getLogs()
	if !strings.Contains(logs, "async subturn spawn failed") {
		t.Errorf("a spawn failure must be logged unconditionally at Error level regardless of any downstream "+
			"callback — got logs:\n%s", logs)
	}
	if !strings.Contains(logs, sessionID) {
		t.Errorf("the failure log must name the affected session_id, got logs:\n%s", logs)
	}

	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after failure: %v", err)
	}
	if rec.State != session.LifecycleFailed {
		t.Errorf("a failed async resume must transition the lifecycle record to Failed so a later "+
			"delegate(status)/peek poll can discover it — got state=%s", rec.State)
	}
	if rec.FailedReason == "" {
		t.Error("a failed lifecycle record must carry a non-empty FailedReason")
	}
}
