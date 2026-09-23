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
	history map[string][]session.TranscriptEntry
	// appendErr, when non-nil, is what the transcript write fails with — the
	// "the new instruction did not land" case of DEFECT 4 (ADR-091 fix lane
	// RX-DELIVERY).
	appendErr error
	messages  map[string][]providers.Message
}

func (s *followUpHistoryStore) ReadTranscript(sessionID string) ([]session.TranscriptEntry, error) {
	return s.history[sessionID], nil
}

func (s *followUpHistoryStore) AddMessage(sessionID, role, content string) {
	if s.messages == nil {
		s.messages = make(map[string][]providers.Message)
	}
	s.messages[sessionID] = append(s.messages[sessionID], providers.Message{Role: role, Content: content})
}

// AppendTranscriptStrict is the write reconstructSteeredTurn actually reads
// back (steer_reconstruct.go scans transcript.jsonl for the last `user`
// entry), so appendFollowUpInstruction requires it.
func (s *followUpHistoryStore) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	if s.appendErr != nil {
		return s.appendErr
	}
	if s.history == nil {
		s.history = make(map[string][]session.TranscriptEntry)
	}
	s.history[sessionID] = append(s.history[sessionID], entry)
	return nil
}

func wireFollowUpTestLauncher(tool *DelegateTool) (*followUpLauncher, *followUpHistoryStore) {
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning}}
	history := &followUpHistoryStore{}
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
	// The instruction now lands in the child's own TRANSCRIPT (the write the
	// rebuilt turn reads back), and AppendTranscriptStrict refuses to write
	// against a session that does not exist — so the child must be a real
	// session in this store, as it always is in production.
	parentMeta, err := sessions.NewSession(session.SessionTypeChat, "webchat", "parent-agent")
	if err != nil {
		t.Fatalf("create steering session: %v", err)
	}
	if _, err := sessions.CreateSessionWithID("child-followup-resume-native", parentMeta.ID, session.SessionTypeDelegate, "webchat", "worker"); err != nil {
		t.Fatalf("create native child session: %v", err)
	}
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
	tool.SetSessionStore(&followUpHistoryStore{})
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

// --- ADR-091 fix lane RX-DELIVERY, DEFECT 4 (HIGH) ---
//
// appendFollowUpInstruction was fire-and-forget: UnifiedStore.AddMessage has
// no return value, a nil store or a failed type assertion was a silent
// no-op, and spawnCorrectiveFollowUp dispatched regardless. The dispatched
// turn is then rebuilt by agent/steer_reconstruct.go::reconstructSteeredTurn
// (wake == nil), which scans the TRANSCRIPT backwards for the last non-blank
// `user` entry — so a session whose new instruction never landed re-runs its
// PREVIOUS instruction and reports that answer upward as a real result.

// TestDelegateTool_FollowUp_RefusesDispatchWhenTheInstructionCannotLand is
// the refusal half: no dispatch may happen once the new instruction is known
// not to have landed.
func TestDelegateTool_FollowUp_RefusesDispatchWhenTheInstructionCannotLand(t *testing.T) {
	const sessionID = "child-followup-instruction-lost"
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 2}}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	tool.SetSessionStore(&followUpHistoryStore{appendErr: fmt.Errorf("transcript append failed: disk full")})
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
		"action": "follow_up", "session_id": sessionID, "text": "stop, do this instead: summarise the release notes",
	})

	if launcher.sessionID != "" {
		t.Fatalf("Dispatch was called for %q even though the new instruction never landed — "+
			"the session will re-run its PREVIOUS instruction and report that answer upward as a real result",
			launcher.sessionID)
	}
	if !result.IsError {
		t.Fatalf("follow_up reported success though the new instruction never landed: %s", result.ForLLM)
	}
	rec, err := lc.Load(sessionID)
	if err != nil {
		t.Fatalf("Load after refusal: %v", err)
	}
	if rec.State != session.LifecycleFailed || rec.FailedReason == "" {
		t.Errorf("a refused follow-up must leave a discoverable failed record, got state=%s reason=%q",
			rec.State, rec.FailedReason)
	}
}

// TestDelegateTool_FollowUp_WritesTheInstructionWhereTheRebuiltTurnReadsIt is
// the landing half: AddMessage alone writes context.jsonl, but the rebuilt
// turn takes its UserMessage from the TRANSCRIPT, so the instruction has to
// be written there too — exactly as SteerLauncher.Launch already writes the
// launch instruction to both.
func TestDelegateTool_FollowUp_WritesTheInstructionWhereTheRebuiltTurnReadsIt(t *testing.T) {
	const sessionID = "child-followup-instruction-transcript"
	const original = "ORIGINAL: audit the whole checkout flow"
	const replacement = "stop, do this instead: summarise the release notes"
	tool, lc, _, _ := newADR053TestTool(t)
	launcher, store := wireFollowUpTestLauncher(tool)
	if err := store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		ID: sessionID + "-task", Role: "user", AgentID: "worker", Content: original,
	}); err != nil {
		t.Fatalf("seed original instruction: %v", err)
	}
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
		"action": "follow_up", "session_id": sessionID, "text": replacement,
	})
	if result.IsError {
		t.Fatalf("follow_up failed: %s", result.ForLLM)
	}
	if launcher.sessionID != sessionID {
		t.Fatalf("Dispatch session = %q, want %q", launcher.sessionID, sessionID)
	}

	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	// The same backwards scan reconstructSteeredTurn performs.
	lastUser := ""
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "user" && strings.TrimSpace(entries[i].Content) != "" {
			lastUser = entries[i].Content
			break
		}
	}
	if lastUser == original {
		t.Fatalf("the rebuilt turn would re-run the ORIGINAL instruction %q — the new instruction never reached the transcript", lastUser)
	}
	if lastUser != replacement {
		t.Fatalf("last `user` transcript entry = %q, want the new instruction %q", lastUser, replacement)
	}
}
