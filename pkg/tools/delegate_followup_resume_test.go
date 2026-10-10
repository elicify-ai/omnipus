// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression tests for the delegate resume path (ADR-20261004 locked decision
// 4: `follow_up` was RENAMED to `resume` with no alias; `cancel` became
// `stop_all`). A resume continues the SAME session through the steering sink's
// ReviveStoppedSession for native AND external-CLI (3P) children alike — it
// never mints a new corrective session (DEL-31 / FR-043: the cold-replacement
// branch bypassed the creation-edge authorization and started a fresh CLI
// conversation after the edge was revoked).

package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// dispatchCountingLauncher records every Dispatch and fails any Launch: a
// resume or a 3P respond may use neither (a Dispatch of a NEW session id is the
// cold-replacement bypass this file pins shut).
type dispatchCountingLauncher struct {
	mu          sync.Mutex
	dispatchIDs []string
}

func (f *dispatchCountingLauncher) Launch(context.Context, steer.LaunchRequest) (steer.LaunchResult, error) {
	return steer.LaunchResult{}, fmt.Errorf("resume must not launch a second session")
}

func (f *dispatchCountingLauncher) Dispatch(_ context.Context, sessionID string, generation int) (steer.DispatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dispatchIDs = append(f.dispatchIDs, sessionID)
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: generation}, nil
}

func (f *dispatchCountingLauncher) dispatched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.dispatchIDs...)
}

// externalSteeringSink is a steering sink with every optional capability the
// 3P paths probe: plain enqueue, revive, and live external-CLI delivery. The
// revive/deliver outcomes are injected so each test drives both the success and
// the visible-refusal half.
type externalSteeringSink struct {
	mu            sync.Mutex
	reviveIDs     []string
	reviveText    []string
	reviveErr     error
	deliverIDs    []string
	deliverErr    error
	enqueuedCount int
}

func (f *externalSteeringSink) EnqueueSteeringMessage(string, string, providers.Message, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.enqueuedCount++
	return "corr_test", nil
}

func (f *externalSteeringSink) ReviveStoppedSession(_ context.Context, sessionID string, _ steer.Principal, instruction string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reviveIDs = append(f.reviveIDs, sessionID)
	f.reviveText = append(f.reviveText, instruction)
	if f.reviveErr != nil {
		return false, f.reviveErr
	}
	return true, nil
}

func (f *externalSteeringSink) DeliverExternalCLIInstruction(_ context.Context, sessionID, _ string, _ providers.Message, correlationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliverIDs = append(f.deliverIDs, sessionID)
	if f.deliverErr != nil {
		return "", f.deliverErr
	}
	return correlationID, nil
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

// lifecycleIDs lists every session id the lifecycle store knows, so a test can
// prove that no corrective session was minted.
func lifecycleIDs(t *testing.T, lc *session.LifecycleStore) []string {
	t.Helper()
	all, err := lc.List(session.LifecycleFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	ids := make([]string, 0, len(all))
	for i := range all {
		ids = append(ids, all[i].SessionID)
	}
	return ids
}

// Oracle: DEL-31 / FR-043 (session-core spec) — resuming a finished external
// CLI child continues the SAME session through the steering sink's revive and
// never creates a new child nor dispatches one directly.
func TestDelegateTool_Resume_3P_RevivesSameSessionAndNeverMintsAChild(t *testing.T) {
	launcher := &dispatchCountingLauncher{}
	sink := &externalSteeringSink{}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	tool.SetSteeringSink(sink)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(sessions)
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{"action": "resume", "session_id": childID, "text": "resume please"})
	if result.IsError {
		t.Fatalf("resume failed: %s", result.ForLLM)
	}
	if got := launcher.dispatched(); len(got) != 0 {
		t.Fatalf("resume dispatched %v directly: a 3P resume must go through the steering sink's revive, never Dispatch (creation-gate bypass)", got)
	}
	if len(sink.reviveIDs) != 1 || sink.reviveIDs[0] != childID {
		t.Fatalf("revive calls = %v, want exactly one for the SAME session %q", sink.reviveIDs, childID)
	}
	if sink.reviveText[0] != "resume please" {
		t.Errorf("revive instruction = %q, want the resume text verbatim", sink.reviveText[0])
	}
	if ids := lifecycleIDs(t, lc); len(ids) != 1 || ids[0] != childID {
		t.Fatalf("lifecycle sessions after resume = %v, want only the original %q (no corrective child)", ids, childID)
	}
}

// Oracle: FR-043 — when the native CLI conversation is gone the revive refuses
// and the refusal is visible; nothing is dispatched and no child is minted.
func TestDelegateTool_Resume_3P_RefusalIsVisibleAndStartsNothing(t *testing.T) {
	launcher := &dispatchCountingLauncher{}
	sink := &externalSteeringSink{reviveErr: errors.New("the external CLI conversation is no longer available; start a new delegation")}
	tool, lc, _, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	tool.SetSteeringSink(sink)
	sessions, parentID, childID := seed3PChild(t, lc)
	tool.SetSessionStore(sessions)
	ctx := WithTranscriptSessionID(context.Background(), parentID)

	result := tool.Execute(ctx, map[string]any{"action": "resume", "session_id": childID, "text": "continue"})
	if !result.IsError {
		t.Fatalf("a resume with no retained conversation must be an error, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "start a new delegation") {
		t.Errorf("refusal %q must tell the caller to start a new delegation", result.ForLLM)
	}
	if got := launcher.dispatched(); len(got) != 0 {
		t.Fatalf("refused resume still dispatched %v", got)
	}
	if ids := lifecycleIDs(t, lc); len(ids) != 1 {
		t.Fatalf("lifecycle sessions after a refused resume = %v, want only the original", ids)
	}
}

// Oracle: the same bypass through `respond` — a live external child receives
// the answer by interrupt + native-conversation resume on the SAME session.
func TestDelegateTool_Respond_3P_Live_DeliversToTheSameConversation(t *testing.T) {
	launcher := &dispatchCountingLauncher{}
	sink := &externalSteeringSink{}
	tool, lc, inbox, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	tool.SetSteeringSink(sink)
	seedOpenQuestion(t, inbox, "parent-1", "child-3p-resp", "corr-3p")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-3p-resp", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker-3p", Is3P: true,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-3p-resp", "correlation_id": "corr-3p", "text": "go",
	})
	if result.IsError {
		t.Fatalf("3P respond failed: %s", result.ForLLM)
	}
	if len(sink.deliverIDs) != 1 || sink.deliverIDs[0] != "child-3p-resp" {
		t.Fatalf("external deliveries = %v, want exactly one to the SAME session", sink.deliverIDs)
	}
	if got := launcher.dispatched(); len(got) != 0 {
		t.Fatalf("3P respond dispatched %v: it must never start a corrective session", got)
	}
	if ids := lifecycleIDs(t, lc); len(ids) != 1 {
		t.Fatalf("lifecycle sessions after respond = %v, want only the original", ids)
	}
	orig, err := lc.Load("child-3p-resp")
	if err != nil {
		t.Fatalf("Load original: %v", err)
	}
	if orig.State != session.LifecycleRunning || orig.Generation != 1 {
		t.Errorf("original after respond = (%s, gen %d), want untouched (running, 1)", orig.State, orig.Generation)
	}
}

// Oracle: FR-043 — a live external child with no deliverable conversation
// refuses visibly; the question slot is released (a retry stays possible) and
// nothing is started.
func TestDelegateTool_Respond_3P_Live_UndeliverableRefusesWithoutStartingAnything(t *testing.T) {
	launcher := &dispatchCountingLauncher{}
	sink := &externalSteeringSink{deliverErr: errors.New("no live conversation")}
	tool, lc, inbox, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	tool.SetSteeringSink(sink)
	seedOpenQuestion(t, inbox, "parent-1", "child-3p-undeliv", "corr-u")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")
	if err := lc.Persist(&session.LifecycleRecord{
		SessionID: "child-3p-undeliv", Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman, SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID: "ws-1", AgentID: "worker-3p", Is3P: true,
	}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	args := map[string]any{"action": "respond", "session_id": "child-3p-undeliv", "correlation_id": "corr-u", "text": "go"}
	if result := tool.Execute(ctx, args); !result.IsError {
		t.Fatalf("an undeliverable 3P answer must be an error, got: %s", result.ForLLM)
	}
	if got := launcher.dispatched(); len(got) != 0 {
		t.Fatalf("refused respond dispatched %v", got)
	}
	if ids := lifecycleIDs(t, lc); len(ids) != 1 {
		t.Fatalf("lifecycle sessions = %v, want only the original", ids)
	}
	// The slot was given back: the same respond is NOT refused as already_answered.
	retry := tool.Execute(ctx, args)
	if strings.Contains(retry.ForLLM, "already_answered") {
		t.Fatalf("a refused respond must release the answer slot, retry said: %s", retry.ForLLM)
	}
}

// Oracle: a stopped/finished external recipient is answered through revive on
// the SAME session (resume-or-refuse), not a corrective session.
func TestDelegateTool_Respond_3P_Stopped_RevivesSameSession(t *testing.T) {
	launcher := &dispatchCountingLauncher{}
	sink := &externalSteeringSink{}
	tool, lc, inbox, _ := newADR053TestTool(t)
	tool.SetSessionLauncher(launcher)
	tool.SetSteeringSink(sink)
	seedStoppedChild(t, lc, "child-3p-stopped")
	rec, err := lc.Load("child-3p-stopped")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec.Is3P = true
	if err := lc.Persist(rec); err != nil {
		t.Fatalf("persist Is3P: %v", err)
	}
	seedOpenQuestion(t, inbox, "parent-1", "child-3p-stopped", "corr-s")
	ctx := WithTranscriptSessionID(context.Background(), "parent-1")

	result := tool.Execute(ctx, map[string]any{
		"action": "respond", "session_id": "child-3p-stopped", "correlation_id": "corr-s", "text": "yes",
	})
	if result.IsError {
		t.Fatalf("respond failed: %s", result.ForLLM)
	}
	if len(sink.reviveIDs) != 1 || sink.reviveIDs[0] != "child-3p-stopped" {
		t.Fatalf("revive calls = %v, want one for the SAME session", sink.reviveIDs)
	}
	if got := launcher.dispatched(); len(got) != 0 {
		t.Fatalf("respond dispatched %v directly", got)
	}
	if ids := lifecycleIDs(t, lc); len(ids) != 1 {
		t.Fatalf("lifecycle sessions = %v, want only the original", ids)
	}
}
