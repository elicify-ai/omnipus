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
//   - spawnCorrectiveFollowUp now marks the launcher dispatch.
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
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type followUpLauncher struct {
	sessionID  string
	generation int
	dispatch   steer.DispatchResult
	err        error
}

type followUpHistoryStore struct {
	history map[string][]providers.Message
}

func (s *followUpHistoryStore) ReadTranscript(string) ([]session.TranscriptEntry, error) {
	return nil, nil
}

func (s *followUpHistoryStore) AddMessage(sessionID, role, content string) {
	if s.history == nil {
		s.history = make(map[string][]providers.Message)
	}
	s.history[sessionID] = append(s.history[sessionID], providers.Message{Role: role, Content: content})
}

func wireFollowUpTestLauncher(tool *DelegateTool) (*followUpLauncher, *followUpHistoryStore) {
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning}}
	history := &followUpHistoryStore{history: make(map[string][]providers.Message)}
	tool.SetSessionLauncher(launcher)
	tool.SetSessionStore(history)
	return launcher, history
}

func (f *followUpLauncher) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, fmt.Errorf("follow_up must not launch a second native session")
}

func (f *followUpLauncher) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	f.sessionID = sessionID
	f.generation = generation
	result := f.dispatch
	if result.Generation == 0 {
		result.Generation = generation
	}
	return result, f.err
}

func TestDelegateTool_FollowUp_NativeBumpsGenerationThenDispatches(t *testing.T) {
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 2}}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	sessions, err := session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	tool.SetSessionStore(sessions)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-resume-native", Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "worker",
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
	if launcher.sessionID != "child-followup-resume-native" || launcher.generation != 2 {
		t.Fatalf("Dispatch = (%q, %d), want existing session at generation 2", launcher.sessionID, launcher.generation)
	}
	rec, err := lc.Load("child-followup-resume-native")
	if err != nil || rec.Generation != 2 || rec.State != session.LifecycleQueued {
		t.Fatalf("persisted generation before Dispatch = %+v, %v; want queued generation 2", rec, err)
	}
	history := sessions.GetHistory("child-followup-resume-native")
	if len(history) == 0 || history[len(history)-1].Role != "user" || history[len(history)-1].Content != "resume please" {
		t.Fatalf("follow-up instruction was not persisted before Dispatch: %+v", history)
	}
}

func TestDelegateTool_FollowUp_3PDispatchesNewCorrectiveSession(t *testing.T) {
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 2}}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	sessions, err := session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	t.Cleanup(func() { _ = sessions.Close() })
	parentMeta, err := sessions.NewSession(session.SessionTypeChat, "webchat", "parent-agent")
	if err != nil {
		t.Fatalf("create steering session: %v", err)
	}
	parentID := parentMeta.ID
	if _, err := sessions.CreateSessionWithID("child-followup-resume-3p", parentID, session.SessionTypeDelegate, "webchat", "claude-code"); err != nil {
		t.Fatalf("create original external session: %v", err)
	}
	title, workspace := "External checkout audit", "ws-1"
	if err := sessions.SetMeta("child-followup-resume-3p", session.MetaPatch{Title: &title, WorkspaceID: &workspace}); err != nil {
		t.Fatalf("stamp original external session: %v", err)
	}
	tool.SetSessionStore(sessions)
	ctx := WithTranscriptSessionID(context.Background(), parentID)
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-followup-resume-3p", Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parentID},
		WorkspaceID:    "ws-1", AgentID: "claude-code",
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
	if launcher.sessionID == "" || launcher.sessionID == "child-followup-resume-3p" {
		t.Error("a 3P follow_up must mint a NEW session id, not reuse the terminal one verbatim")
	}
	if launcher.generation != 2 {
		t.Fatalf("3P corrective Dispatch generation = %d, want 2", launcher.generation)
	}
	meta, err := sessions.GetMeta(launcher.sessionID)
	if err != nil {
		t.Fatalf("new external corrective session has no durable identity: %v", err)
	}
	if meta.ParentSessionID != parentID || meta.ActiveAgentID != "claude-code" || meta.WorkspaceID != "ws-1" {
		t.Fatalf("new external corrective identity = %+v; want copied parent, agent, and workspace", meta)
	}
	history := sessions.GetHistory(launcher.sessionID)
	if len(history) == 0 || history[len(history)-1].Content != "resume please" {
		t.Fatalf("new external corrective session lacks follow-up instruction: %+v", history)
	}
}

// TestDelegateTool_FollowUp_DispatchFailure_LoggedUnconditionally pins the
// immediate error, durable failed state, and operator-visible diagnostic.
func TestDelegateTool_FollowUp_DispatchFailure_LoggedUnconditionally(t *testing.T) {
	getLogs := captureLogs(t)

	const sessionID = "child-followup-resume-logging"
	failingLauncher := &followUpLauncher{err: fmt.Errorf("dispatch session %q: simulated store failure", sessionID)}

	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(failingLauncher)
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: sessionID, Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "worker",
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "follow_up", "session_id": sessionID, "text": "resume please",
	})
	if !result.IsError {
		t.Fatalf("a Dispatch failure must be returned immediately, got: %s", result.ForLLM)
	}

	logs := getLogs()
	if !strings.Contains(logs, "follow-up dispatch failed") {
		t.Errorf("a Dispatch failure must be logged unconditionally, got logs:\n%s", logs)
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
