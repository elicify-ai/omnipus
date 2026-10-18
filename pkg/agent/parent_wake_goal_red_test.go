// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Tests 6 and 9.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - §9.2 message ids
//   - FR-004
//   - BDD "Parent chat shows Goal met" and "The helper keeps its own outcome row"
//   - Planned test TestGoalOutcome_WrittenOnHelperAndParent
//
// A met ending is stored once on the helper and, when a parent exists, once
// on the parent. Different message ids. Same goal id and ending.
// Parent id: goal-outcome-<parent session id>-<goal id>-<ended-at nanos>.
// Helper id stays goal-outcome-<goal id>-<ended-at nanos>.
//
// Step 4 items 2–3 are deferred to CHECK.
package agent

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestGoalOutcome_WrittenOnHelperAndParent(t *testing.T) {
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "tests pass")}, nil
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)
	parentID := pwStampParent(t, g)

	g.al.dispatchDeferredGoalAdjudication(g.work)
	g.stopEvents()

	helper := requireOneGoalOutcome(t, g.store, g.sid, g.goalID)
	parentRows := pwGoalOutcomeRows(t, g.store, parentID)
	if len(parentRows) != 1 {
		t.Fatalf("parent transcript holds %d goal_outcome rows, want 1 — a met ending is written on the parent as well as the helper (FR-004 / §9.2 / Test 9)", len(parentRows))
	}
	parent := parentRows[0]
	if parent.ID == helper.ID {
		t.Fatalf("parent and helper share message id %q, want different ids (§9.2)", parent.ID)
	}
	wantParentID := fmt.Sprintf("goal-outcome-%s-%s-%d", parentID, g.goalID, helper.GoalOutcome.EndedAt.UnixNano())
	if parent.ID != wantParentID {
		t.Fatalf("parent outcome id = %q, want %q (§9.2)", parent.ID, wantParentID)
	}
	if parent.GoalOutcome == nil || parent.GoalOutcome.GoalId != helper.GoalOutcome.GoalId || parent.GoalOutcome.Ending != helper.GoalOutcome.Ending {
		t.Fatalf("parent payload = %+v, want the helper's goal id and ending %+v", parent.GoalOutcome, helper.GoalOutcome)
	}
	if parent.GoalOutcome.Ending != generated.GoalOutcomeEndingMet {
		t.Fatalf("ending = %q, want met", parent.GoalOutcome.Ending)
	}
}

func TestDeliver_MetVerdictSaveThenPlace(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	pwWireRuntime(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	rec := stampG5ExitedExecution(t, al, launchGoalBearingChild947(t, al, parentMeta.ID, "call-pw-met-save"))
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: metGoalVerdict947(t, rec)}, nil
	}}
	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

	result := turnResult{finalContent: "[goal:evidence] verified the work\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)
	select {
	case got := <-done:
		if got != rec.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("SETUP: met adjudication did not finish — the save cannot be observed")
	}

	got, err := lifecycle.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if got.State != session.LifecycleCompleted {
		t.Fatalf("SETUP: child state = %q, want completed — the met arm did not reach the parent save", got.State)
	}

	// Met ending id is <child>:<generation>:final (PW-D17). The save id prefixes it (PW-D16 / §9.5).
	wantID := fmt.Sprintf("saved-input-%s:%d:final", rec.SessionID, got.Generation)
	entries, err := al.GetSessionStore().ReadTranscript(parentMeta.ID)
	if err != nil {
		t.Fatalf("ReadTranscript parent: %v", err)
	}
	var saved *session.TranscriptEntry
	for i := range entries {
		if entries[i].ID == wantID {
			saved = &entries[i]
			break
		}
	}
	verdictID := goalVerdictUpwardMessageID(got.GoalRef, 1)
	acked := pwVerdictAcked(t, inbox, parentMeta.ID, verdictID)
	if saved == nil {
		t.Fatalf("parent has no chat-membership %s (verdict %s acked=%v) — the exact combined input must be saved before the verdict is marked read (PW-D16 / FR-005 / §9.5 step 2 / Test 6)", wantID, verdictID, acked)
	}
	if !acked {
		t.Fatalf("save %s exists but verdict %s is not acknowledged — ack follows a successful save (§9.5 step 3)", wantID, verdictID)
	}
	for _, piece := range []string{"the work is complete", "verified"} {
		if !strings.Contains(saved.Content, piece) {
			t.Fatalf("saved input = %q, want the verdict condition and Judge reason %q in the text the model will see (§9.5)", saved.Content, piece)
		}
	}
}

func pwVerdictAcked(t *testing.T, inbox *session.MessageInboxStore, parentID, verdictID string) bool {
	t.Helper()
	entries, err := inbox.Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(parent): %v", err)
	}
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryAck {
			continue
		}
		for _, id := range entry.AckedIDs {
			if id == verdictID {
				return true
			}
		}
	}
	return false
}

func pwStampParent(t *testing.T, g *goalClaimAwaitingJudge) string {
	t.Helper()
	parent, err := g.store.NewSession(session.SessionTypeChat, "main", g.agent.ID)
	if err != nil {
		t.Fatalf("NewSession parent: %v", err)
	}
	if err = g.store.SetMeta(g.sid, session.MetaPatch{ParentSessionID: &parent.ID}); err != nil {
		t.Fatalf("SetMeta parent: %v", err)
	}
	ls := g.al.GetSessionLifecycleStore()
	if ls == nil {
		home := g.al.GetConfig().Agents.Defaults.Home
		ls = session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
		inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
		g.al.SetSessionMessagingStores(inbox, ls)
	}
	// PW-D2/PW-D16: the real Judge/completion path needs the same upward
	// deliverer, launcher, goal reference and producing execution as boot.
	pwWireRuntime(t, g.al)
	ws := testHarnessWorkspaceMembershipID
	for _, id := range []string{parent.ID, g.sid} {
		if err = g.store.SetMeta(id, session.MetaPatch{WorkspaceID: &ws}); err != nil {
			t.Fatalf("SETUP: goal-session workspace: %v", err)
		}
	}
	if err = ls.Persist(&session.LifecycleRecord{
		SessionID: parent.ID, Generation: 1, State: session.LifecycleCompleted,
		Origin: &session.Origin{Kind: session.OriginKindChat}, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: ws, AgentID: g.agent.ID,
	}); err != nil {
		t.Fatalf("persist parent: %v", err)
	}
	if err = ls.Persist(&session.LifecycleRecord{
		SessionID: g.sid, Generation: 1, State: session.LifecycleRunning,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parent.ID,
		WorkspaceID: ws, AgentID: g.agent.ID, ParentAgentID: g.agent.ID, GoalRef: g.goalID,
		SteeredBy: &session.SteeredBy{
			SteeringSessionID: parent.ID, RootSessionID: parent.ID,
			ReportingTarget: session.ReportingTarget{Channel: "webchat", ChatID: parent.ID, SessionID: parent.ID},
		},
	}); err != nil {
		t.Fatalf("persist helper edge: %v", err)
	}
	producing, err := ls.Load(g.sid)
	if err != nil {
		t.Fatalf("SETUP: load producing helper: %v", err)
	}
	producing = stampG5ExitedExecution(t, g.al, producing)
	g.work.producer = executionClaim{SessionID: g.sid, Generation: producing.Generation,
		RunID: producing.ExecutionID.RunID, BootSeq: producing.ExecutionID.BootSeq}
	g.work.workspaceID = ws
	return parent.ID
}

func pwGoalOutcomeRows(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(%s): %v", sessionID, err)
	}
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.SystemSubtype == session.SystemSubtypeGoalOutcome {
			out = append(out, e)
		}
	}
	return out
}
