// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for the delegate resume path (ADR-20261004 locked decision
// 4: `follow_up` was RENAMED to `resume` with no alias; `cancel` became
// `stop_all`). The native half of the old warm-resume defect — a terminal
// session resumed through agent.spawnSubTurn's CreateSessionWithID collision
// guard (FR-096), silently doing nothing behind a well-formed ack — is now
// carried by the steering sink's ReviveStoppedSession (agent side) and is
// pinned in pkg/tools/delegate_adr053_test.go plus pkg/agent's revive tests;
// this file keeps the half that still lives at the pkg/tools boundary:
//
//   - a 3P child has no warm-resume primitive (D5): `resume` (and a 3P
//     `respond`) mint a NEW corrective session carrying the prior context,
//     and the launcher dispatch marks it as a genuine cold create.
//   - any async spawn failure is logged unconditionally (grep-able in
//     gateway.log) regardless of whether a downstream callback happens to
//     do anything with the result, and the affected session's lifecycle
//     record is left in a discoverable Failed state.
//   - the refusal half of ADR-091 fix lane RX-DELIVERY, DEFECT 4: when the
//     new instruction did not land, NOTHING is dispatched.
//   - the landing half: the instruction is written where the rebuilt turn
//     actually reads it — the TRANSCRIPT.

package tools

import (
	"context"
	"errors"
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
	return steer.LaunchResult{}, fmt.Errorf("resume must not launch a second native session")
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

// appendFailingStore is a real UnifiedStore whose transcript append fails —
// the process-edge fault for the 3P refusal test: the corrective identity
// clone succeeds (real store underneath), the instruction append cannot land.
type appendFailingStore struct {
	*session.UnifiedStore
	err error
}

func (s *appendFailingStore) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return s.err
}

// seed3PChild creates the parent + the external-CLI child session in a REAL
// UnifiedStore and persists the child's lifecycle record, mirroring what a
// real 3P delegation leaves on disk.
func seed3PChild(t *testing.T, lc *session.LifecycleStore) (*session.UnifiedStore, string, string) {
	t.Helper()
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
	const childID = "child-followup-resume-3p"
	if _, createErr := sessions.CreateSessionWithID(childID, parentID, session.SessionTypeDelegate, "webchat", "claude-code"); createErr != nil {
		t.Fatalf("create original external session: %v", createErr)
	}
	title, workspace := "External checkout audit", "ws-1"
	if setMetaErr := sessions.SetMeta(childID, session.MetaPatch{Title: &title, WorkspaceID: &workspace}); setMetaErr != nil {
		t.Fatalf("stamp original external session: %v", setMetaErr)
	}
	if persistErr := lc.Persist(&session.LifecycleRecord{
		SessionID: childID, Generation: 1, State: session.LifecycleCompleted,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parentID, RootSessionID: parentID},
		WorkspaceID:    "ws-1", AgentID: "claude-code",
		Is3P: true,
	}); persistErr != nil {
		t.Fatalf("seed failed: %v", persistErr)
	}
	return sessions, parentID, childID
}

// TestDelegateTool_Resume_3PDispatchesNewCorrectiveSession pins D5 at the
// resume action: an external-CLI child has no warm-resume primitive, so
// `resume` mints a NEW corrective session carrying the prior context, linked
// back via ResumedFrom.
func TestDelegateTool_Resume_3PDispatchesNewCorrectiveSession(t *testing.T) {
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 2}}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(sessions)
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{
		"action": "resume", "session_id": childID, "text": "resume please",
	})
	if result.IsError {
		t.Fatalf("resume failed: %s", result.ForLLM)
	}
	if launcher.sessionID == "" || launcher.sessionID == childID {
		t.Error("a 3P resume must mint a NEW session id, not reuse the terminal one verbatim")
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
	rec, err := lc.Load(launcher.sessionID)
	if err != nil {
		t.Fatalf("Load corrective record: %v", err)
	}
	if rec.ResumedFrom != childID {
		t.Errorf("corrective record ResumedFrom = %q, want %q", rec.ResumedFrom, childID)
	}
	history := sessions.GetHistory(launcher.sessionID)
	if len(history) == 0 || history[len(history)-1].Content != "resume please" {
		t.Fatalf("new external corrective session lacks follow-up instruction: %+v", history)
	}
}

// TestDelegateTool_Resume_DispatchFailure_LoggedUnconditionally pins the
// immediate error, durable failed state, and operator-visible diagnostic for
// the 3P corrective dispatch.
func TestDelegateTool_Resume_DispatchFailure_LoggedUnconditionally(t *testing.T) {
	getLogs := captureLogs(t)

	tool, lc, _, _ := newADR053TestTool(t)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(sessions)
	failingLauncher := &followUpLauncher{err: errors.New("dispatch: simulated store failure")}
	tool.SetSessionLauncher(failingLauncher)
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{
		"action": "resume", "session_id": childID, "text": "resume please",
	})
	if !result.IsError {
		t.Fatalf("a Dispatch failure must be returned immediately, got: %s", result.ForLLM)
	}

	logs := getLogs()
	if !strings.Contains(logs, "follow-up dispatch failed") {
		t.Errorf("a Dispatch failure must be logged unconditionally, got logs:\n%s", logs)
	}
	// The corrective successor (the record the failed dispatch strands) is the
	// NEW session id, not the original — discoverable via its ResumedFrom link.
	all, listErr := lc.List(session.LifecycleFilter{})
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	var successor *session.LifecycleRecord
	for i := range all {
		if all[i].ResumedFrom == childID && all[i].SessionID != childID {
			successor = &all[i]
			break
		}
	}
	if successor == nil {
		t.Fatal("no corrective successor record was persisted for the failed dispatch")
	}
	if !strings.Contains(logs, successor.SessionID) {
		t.Errorf("the failure log must name the affected session_id, got logs:\n%s", logs)
	}
	if successor.State != session.LifecycleFailed {
		t.Errorf("a failed async resume must transition the lifecycle record to Failed so a later "+
			"delegate(status)/peek poll can discover it — got state=%s", successor.State)
	}
	if successor.FailedReason == "" {
		t.Error("a failed lifecycle record must carry a non-empty FailedReason")
	}
}

// --- ADR-091 fix lane RX-DELIVERY, DEFECT 4 (HIGH) ---
//
// appendFollowUpInstruction was fire-and-forget: a failed transcript write was
// invisible and spawnCorrectiveFollowUp dispatched regardless. The dispatched
// turn is then rebuilt by agent/steer_reconstruct.go::reconstructSteeredTurn
// (wake == nil), which scans the TRANSCRIPT backwards for the last non-blank
// `user` entry — so a session whose new instruction never landed re-runs its
// PREVIOUS instruction and reports that answer upward as a real result.

// TestDelegateTool_Resume_RefusesDispatchWhenTheInstructionCannotLand is
// the refusal half: no dispatch may happen once the new instruction is known
// not to have landed. The fault is injected at the process edge (the real
// store's transcript append), so the corrective identity clone still succeeds
// and only the instruction write fails.
func TestDelegateTool_Resume_RefusesDispatchWhenTheInstructionCannotLand(t *testing.T) {
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 2}}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(&appendFailingStore{UnifiedStore: sessions, err: fmt.Errorf("transcript append failed: disk full")})
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{
		"action": "resume", "session_id": childID, "text": "stop, do this instead: summarise the release notes",
	})

	if launcher.sessionID != "" {
		t.Fatalf("Dispatch was called for %q even though the new instruction never landed — "+
			"the session will re-run its PREVIOUS instruction and report that answer upward as a real result",
			launcher.sessionID)
	}
	if !result.IsError {
		t.Fatalf("resume reported success though the new instruction never landed: %s", result.ForLLM)
	}
	all, listErr := lc.List(session.LifecycleFilter{})
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	var successor *session.LifecycleRecord
	for i := range all {
		if all[i].ResumedFrom == childID && all[i].SessionID != childID {
			successor = &all[i]
			break
		}
	}
	if successor == nil {
		t.Fatal("no corrective successor record was persisted for the refused resume")
	}
	if successor.State != session.LifecycleFailed || successor.FailedReason == "" {
		t.Errorf("a refused resume must leave a discoverable failed record, got state=%s reason=%q",
			successor.State, successor.FailedReason)
	}
}

// TestDelegateTool_Resume_WritesTheInstructionWhereTheRebuiltTurnReadsIt is
// the landing half: AddMessage alone writes context.jsonl, but the rebuilt
// turn takes its UserMessage from the TRANSCRIPT, so the instruction has to
// be written there too — exactly as SteerLauncher.Launch writes the launch
// instruction to both.
func TestDelegateTool_Resume_WritesTheInstructionWhereTheRebuiltTurnReadsIt(t *testing.T) {
	const original = "ORIGINAL: audit the whole checkout flow"
	const replacement = "stop, do this instead: summarise the release notes"
	tool, lc, _, _ := newADR053TestTool(t)
	launcher := &followUpLauncher{dispatch: steer.DispatchResult{State: steer.DispatchRunning, Generation: 2}}
	tool.SetSessionLauncher(launcher)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(sessions)
	if err := sessions.AppendTranscriptStrict(childID, session.TranscriptEntry{
		ID: childID + "-task", Role: "user", AgentID: "claude-code", Content: original,
	}); err != nil {
		t.Fatalf("seed original instruction: %v", err)
	}
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{
		"action": "resume", "session_id": childID, "text": replacement,
	})
	if result.IsError {
		t.Fatalf("resume failed: %s", result.ForLLM)
	}
	if launcher.sessionID == "" || launcher.sessionID == childID {
		t.Fatalf("Dispatch session = %q, want the NEW corrective session", launcher.sessionID)
	}

	entries, err := sessions.ReadTranscript(launcher.sessionID)
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
