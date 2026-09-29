package agent

// Fix-round-1 RED coverage for founder ruling Q2 B.
//
// Oracle source: coordination/logs/fix890-opus/arch-q2-design.md, especially
// "Re-trigger behavior", "Invariants", and "Tests required". Restart
// expectations derive from founder ruling Q7=A and boot_sweep.go's documented
// failed(interrupted) boot behavior. No expected value below was copied from
// observed implementation output.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func q2bPendingClaimWithRunningDescendant(t *testing.T, h *q2bHarness, callID string) *session.LifecycleRecord {
	t.Helper()
	descendant := q2bLaunchDescendant(t, h, callID, session.LifecycleRunning)
	q2bKeepTurnAlive(t, h.al, descendant.SessionID)
	result := h.claimMet("completion must wait for the last descendant")
	if result.goalDeferredAdjudication != nil {
		t.Fatalf("initial met claim scheduled adjudication while descendant %q was running", descendant.SessionID)
	}
	if !h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Fatalf("goal %q completion phase is not waiting_descendants", h.child.GoalRef)
	}
	return descendant
}

func q2bAssertExactlyOneReevaluation(t *testing.T, h *q2bHarness) {
	t.Helper()
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 1 {
		t.Fatalf("completion re-evaluations for waiting parent child = %d, want exactly 1", got)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls before a fresh post-handback claim = %d, want 0", calls)
	}
}

func q2bBootRecoveryForHarness(h *q2bHarness) *SteerBootRecovery {
	return &SteerBootRecovery{
		Lifecycle:  h.lifecycle,
		Sessions:   h.al.GetSessionStore(),
		Inbox:      h.inbox,
		Classifier: NewSteerRecordClassifier(h.lifecycle, h.al.GetSessionStore()),
		Deliverer:  h.al.getUpwardDeliverer(),
		EndSessionGoal: func(sessionID, reason string) {
			h.al.EndSessionOwnedGoalOnTerminal(sessionID, reason)
		},
		DescendantTerminal: h.al.ResumeDeferredGoalAfterDescendantTerminal,
	}
}

func TestGoalQ2B_LastDescendantTerminalRoutesScheduleExactlyOneReevaluation(t *testing.T) {
	tests := []struct {
		name      string
		terminate func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord)
	}{
		{
			name: "completed",
			terminate: func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord) {
				t.Helper()
				if err := h.al.completeSteeredTurn(context.Background(), descendant, turnResult{finalContent: "done"}, nil); err != nil {
					t.Fatalf("completeSteeredTurn(completed): %v", err)
				}
			},
		},
		{
			name: "failed",
			terminate: func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord) {
				t.Helper()
				if err := h.al.completeSteeredTurn(context.Background(), descendant, turnResult{}, errors.New("injected worker failure")); err != nil {
					t.Fatalf("completeSteeredTurn(failed): %v", err)
				}
			},
		},
		{
			name: "timed_out",
			terminate: func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord) {
				t.Helper()
				if err := h.al.completeSteeredTurn(context.Background(), descendant, turnResult{}, context.DeadlineExceeded); err != nil {
					t.Fatalf("completeSteeredTurn(timed_out): %v", err)
				}
			},
		},
		{
			name: "cancelled_report",
			terminate: func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord) {
				t.Helper()
				h.al.reportSteeredSessionTerminalUpward(context.Background(), descendant.SessionID, descendant.Generation,
					session.LifecycleCancelled, steer.OutcomeInterrupted, "interrupted: cancelled by operator")
			},
		},
		{
			name: "boot_finish_from_final",
			terminate: func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord) {
				t.Helper()
				message := bootHandback(t, descendant.SessionID, h.child.SessionID,
					fmt.Sprintf("%s:%d:final", descendant.SessionID, descendant.Generation))
				if err := q2bBootRecoveryForHarness(h).finishFromFinal(descendant, message); err != nil {
					t.Fatalf("finishFromFinal: %v", err)
				}
			},
		},
		{
			name: "boot_fail_interrupted",
			terminate: func(t *testing.T, h *q2bHarness, descendant *session.LifecycleRecord) {
				t.Helper()
				if err := q2bBootRecoveryForHarness(h).failInterrupted(descendant); err != nil {
					t.Fatalf("failInterrupted: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newQ2BHarness(t, "q2b-terminal-route-"+tt.name)
			descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-terminal-route-descendant-"+tt.name)
			tt.terminate(t, h, descendant)

			loaded, err := h.lifecycle.Load(descendant.SessionID)
			if err != nil {
				t.Fatalf("Load(terminal descendant): %v", err)
			}
			if !loaded.Terminal() {
				t.Fatalf("descendant lifecycle after %s = %q, want terminal", tt.name, loaded.State)
			}
			q2bAssertExactlyOneReevaluation(t, h)
		})
	}
}

func TestGoalQ2B_NotificationFailureRestoresWaitingAndLaterTriggerRetriesOnce(t *testing.T) {
	h := newQ2BHarness(t, "q2b-notification-retry")
	descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-notification-retry-descendant")
	if err := h.lifecycle.Mutate(descendant.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		return nil
	}); err != nil {
		t.Fatalf("terminalise descendant without live hook: %v", err)
	}

	originalBus := h.al.bus
	t.Cleanup(func() { h.al.bus = originalBus })
	h.al.bus = nil
	h.al.resumeDeferredGoalForSession(h.child.SessionID, h.child.GoalRef)
	if !h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Fatalf("completion phase after failed notification is not waiting_descendants; later retry would be stranded")
	}
	if g := h.goalRecord(); g.State != generated.GoalStateActive {
		t.Fatalf("goal state after failed notification = %q, want active", g.State)
	}

	h.al.bus = originalBus
	h.al.resumeDeferredGoalForSession(h.child.SessionID, h.child.GoalRef)
	h.al.resumeDeferredGoalForSession(h.child.SessionID, h.child.GoalRef)
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 2 {
		t.Fatalf("notification attempts = %d, want one failed attempt plus exactly one successful retry", got)
	}
	if h.al.goalCompletionWaiting(h.child.GoalRef) {
		t.Fatal("completion phase remained waiting after the successful retry")
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls without a fresh claim = %d, want 0", calls)
	}
}

func TestGoalQ2B_QuietnessReadFailureFailsClosed(t *testing.T) {
	h := newQ2BHarness(t, "q2b-quietness-read-failure")
	descendant := q2bLaunchDescendant(t, h, "q2b-unreadable-descendant", session.LifecycleQueued)
	path := filepath.Join(h.lifecycle.Dir(), descendant.SessionID+".jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove descendant lifecycle fixture: %v", err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("replace descendant lifecycle file with unreadable directory: %v", err)
	}

	result := h.claimMet("subtree authority is unreadable")
	if result.goalDeferredAdjudication != nil {
		t.Fatal("unreadable subtree scheduled adjudication, want fail-closed pending claim")
	}
	if g := h.goalRecord(); g.State != generated.GoalStateActive || g.LatestVerdict != nil {
		t.Fatalf("goal after unreadable subtree = state %q verdict %+v, want active with no verdict", g.State, g.LatestVerdict)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Fatalf("Judge calls with unreadable subtree = %d, want 0", calls)
	}
	if messages := h.parentMessages(); len(messages) != 0 {
		t.Fatalf("parent messages with unreadable subtree = %d, want 0", len(messages))
	}
}

// Restart resume is intentionally deferred to ADR-20260928-sub-agent-control-plane D8.
func TestGoalQ2B_RestartFailsPendingOwnerAsInterrupted(t *testing.T) {
	h := newQ2BHarness(t, "q2b-boot-pending-owner")
	descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-boot-pending-descendant")

	recovery := q2bBootRecoveryForHarness(h)
	notice := func(string, string) {}
	recovery.recoverSteered(context.Background(), h.child.SessionID, notice)
	recovery.recoverSteered(context.Background(), descendant.SessionID, notice)

	for label, sessionID := range map[string]string{
		"pending goal owner": h.child.SessionID,
		"running descendant": descendant.SessionID,
	} {
		record, err := h.lifecycle.Load(sessionID)
		if err != nil {
			t.Fatalf("Load(%s after restart): %v", label, err)
		}
		if record.State != session.LifecycleFailed || record.FailedReason != "interrupted" {
			t.Errorf("%s after restart = state %q reason %q, want failed/interrupted", label, record.State, record.FailedReason)
		}
	}

	endedGoal := h.goalRecord()
	if endedGoal.State != generated.GoalStateCleared {
		t.Errorf("pending owner's goal after restart = %q, want cleared", endedGoal.State)
	}
	if !strings.Contains(endedGoal.TerminalReason, "interrupted") {
		t.Errorf("pending owner's terminal reason = %v, want an interruption reason", endedGoal.TerminalReason)
	}

	parentMessages := h.parentMessages()
	if len(parentMessages) != 1 {
		t.Fatalf("parent messages from pending owner after restart = %d, want exactly 1", len(parentMessages))
	}
	parentEnvelope, err := decodeBootMessage(parentMessages[0])
	if err != nil {
		t.Fatalf("decode parent interruption: %v", err)
	}
	if parentEnvelope.Kind != "error" || !parentEnvelope.Fatal || !strings.HasPrefix(parentEnvelope.Text, "interrupted:") {
		t.Errorf("parent entry for pending owner = %+v, want one fatal interrupted error", parentEnvelope)
	}

	descendantMessagesAtParent, _, _, err := h.inbox.Drain(h.parentID, descendant.SessionID, "", 32)
	if err != nil {
		t.Fatalf("Drain(parent, descendant): %v", err)
	}
	if len(descendantMessagesAtParent) != 0 {
		t.Errorf("descendant reports delivered directly to top-level parent = %d, want 0", len(descendantMessagesAtParent))
	}
	descendantMessagesAtOwner, _, _, err := h.inbox.Drain(h.child.SessionID, descendant.SessionID, "", 32)
	if err != nil {
		t.Fatalf("Drain(pending owner, descendant): %v", err)
	}
	if len(descendantMessagesAtOwner) != 1 {
		t.Fatalf("descendant reports delivered to its direct owner = %d, want exactly 1", len(descendantMessagesAtOwner))
	}
	descendantEnvelope, err := decodeBootMessage(descendantMessagesAtOwner[0])
	if err != nil {
		t.Fatalf("decode descendant interruption: %v", err)
	}
	if descendantEnvelope.Kind != "error" || !descendantEnvelope.Fatal || !strings.HasPrefix(descendantEnvelope.Text, "interrupted:") {
		t.Errorf("descendant entry for direct owner = %+v, want one fatal interrupted error", descendantEnvelope)
	}

	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 0 {
		t.Errorf("Q2=B re-evaluations after restart = %d, want 0", got)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls after restart = %d, want 0", calls)
	}
	for _, wake := range h.parentWakeEvents() {
		if wake.SourceKind == "message_parent:handback" {
			t.Errorf("completion handback reached the parent after restart: %+v", wake)
		}
	}

	triggerState := goalTriggers()
	triggerState.mu.Lock()
	completionPhase, phasePresent := triggerState.completionPhase[h.child.GoalRef]
	triggerState.mu.Unlock()
	if phasePresent || completionPhase != goalCompletionNone {
		t.Errorf("process-local completion phase after restart = %v (present=%v), want cleared", completionPhase, phasePresent)
	}
}

func TestGoalQ2B_BootRepairsTerminalDescendantWhoseLiveHookDidNotRun(t *testing.T) {
	h := newQ2BHarness(t, "q2b-boot-missed-hook")
	descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-boot-missed-hook-descendant")
	finalID := fmt.Sprintf("%s:%d:final", descendant.SessionID, descendant.Generation)
	message := bootHandback(t, descendant.SessionID, h.child.SessionID, finalID)
	if _, err := h.inbox.Append(h.child.SessionID, message); err != nil {
		t.Fatalf("Append(descendant final before crash): %v", err)
	}
	if err := h.lifecycle.Mutate(descendant.SessionID, func(rec *session.LifecycleRecord) error {
		rec.State = session.LifecycleCompleted
		return nil
	}); err != nil {
		t.Fatalf("persist terminal descendant before crash: %v", err)
	}
	resetGoalTriggerStateForTest()
	t.Cleanup(resetGoalTriggerStateForTest)

	q2bBootRecoveryForHarness(h).recoverSteered(context.Background(), descendant.SessionID, func(string, string) {})
	q2bAssertExactlyOneReevaluation(t, h)
}

// TestGoalQ2B_MetClaimWithoutLifecycleStoreIsQuietAndAdjudicates supersedes
// the retired TestGoalQ2B_MetClaimWithoutLifecycleStoreFailsClosed (fix-890
// squad-lead contract A). OLD rule (wrong, per the pinned contract): a met
// claim with no lifecycle store must fail closed and never schedule
// adjudication. NEW rule: SteerLauncher.Launch (steer_launcher.go, the
// `lifecycle == nil || sessions == nil` refusal at the top of Launch, before
// launchOrdinaryRoot/launchSteered ever run) refuses EVERY child launch when
// the lifecycle store is nil — no descendant can ever exist on this session
// — so the completion subtree is quiet BY CONSTRUCTION, not merely
// "unreadable". hasRunningOrQueuedDescendant (steer_completion.go) encodes
// this exact reasoning: "lifecycle == nil: SteerLauncher.Launch refuses a
// child without a lifecycle store, so there is no descendant to wait for" —
// returns (false, nil), never an error. A met claim with no store therefore
// proceeds straight to adjudication: no panic, deferred adjudication IS
// recorded, and — driving the Judge with this harness's own fake provider —
// the goal reaches a verdict.
func TestGoalQ2B_MetClaimWithoutLifecycleStoreIsQuietAndAdjudicates(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	const verdictReason = "no descendant can exist without a lifecycle store"
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, verdictReason)}, nil
	}}
	judgeInst.Provider = judge

	store, sessionID := newGoalTestSession(t, al, agentInst.ID)
	goalID := activateTestGoalRecord(t, sessionID, "ordinary session goal remains active")
	al.SetSessionMessagingStores(nil, nil)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sessionID,
		Channel: "webchat", ChatID: "q2b-no-lifecycle", SessionKey: "q2b-no-lifecycle", UserInitiated: true,
	}
	result := &turnResult{finalContent: "[goal:evidence] work appears complete\nGOAL_STATUS: met"}

	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	}()
	if panicValue != nil {
		t.Fatalf("met claim panicked without lifecycle authority: %v", panicValue)
	}
	work := result.goalDeferredAdjudication
	if work == nil {
		t.Fatal("BUG: met claim without a lifecycle store did not record deferred adjudication — SteerLauncher.Launch refuses every child launch when the store is nil, so the subtree is quiet by construction and the claim must proceed to adjudication, not hold")
	}

	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sid string) { done <- sid }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	al.dispatchDeferredGoalAdjudication(work)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("adjudication with no lifecycle store did not finish")
	}

	if calls := judge.callCount(); calls != 1 {
		t.Fatalf("Judge calls with no lifecycle store = %d, want exactly 1", calls)
	}
	g, err := resolveGoalRecordStore().Get(goalID)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	if g.LatestVerdict == nil || !g.LatestVerdict.Met {
		t.Fatalf("goal after adjudication with no lifecycle store = verdict %+v, want a met verdict recorded", g.LatestVerdict)
	}
}

// TestGoalQ2B_QuietnessReadFailureFailsClosed (above, in this same file) is
// contract A's other half: a WIRED lifecycle store whose List fails still
// holds the claim (fail closed) — corrupting a real descendant's lifecycle
// file into an unreadable directory and asserting no deferred adjudication,
// no verdict, and no Judge call. Reused as-is; its scenario (a wired,
// unreadable store) is orthogonal to this test's (no store at all).
