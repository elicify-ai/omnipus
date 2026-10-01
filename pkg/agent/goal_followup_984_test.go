package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// This file carries the #984 post-merge follow-up regression tests (PR #984,
// #947 defect 1 "the hang", branch fix/984-followup). Each test pins one
// review finding or founder ruling from the design note
// (coordination/logs/fix890-opus/lc947-defect1-design-note.md) and the
// post-merge reviews (rev984-architect F1-F4, rev984-pta, rev984-sfh).

// TestGoalDelegation984_MetPathOneWakeVerdictAcked pins the founder Q1=A
// ruling (2026-09-28): "on the session-goal 'met' path only the hand-back
// wakes the parent; the goal_status verdict entry is delivered without a wake
// and ACKED at hand-back time so boot recovery does not re-wake it. Task
// goals are untouched."
//
// One judged-MET delegation must therefore produce EXACTLY ONE parent wake —
// the completion handback (whose wake carries the child's actual answer, the
// re-entry-worthy payload). The goal_status verdict entry is still delivered
// (the side panel keeps its status line) but is stored not woken and is
// acknowledged at hand-back time, so it can never reach a second wake through
// boot recovery (boot_sweep.go::unacknowledged re-delivers unacked
// wake-eligible entries). The two-ENTRY contract of design-note decision (b)
// stands unchanged; Q1=A corrects the WAKE count on top of it.
func TestGoalDelegation984_MetPathOneWakeVerdictAcked(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "prove the goal",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-q1a"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		t.Fatalf("Persist(running): %v", persistErr)
	}

	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	type criterionVerdict struct {
		ID     string `json:"id"`
		Met    bool   `json:"met"`
		Reason string `json:"reason"`
	}
	verdicts := make([]criterionVerdict, 0, len(g.Criteria)+len(g.DoD))
	for _, criterion := range append(append([]task.AcceptanceCriterion{}, g.Criteria...), g.DoD...) {
		verdicts = append(verdicts, criterionVerdict{ID: criterion.ID, Met: true, Reason: "verified"})
	}
	body, err := json.Marshal(map[string]any{"met": true, "criteria": verdicts})
	if err != nil {
		t.Fatalf("Marshal(verdict): %v", err)
	}
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: string(body)}, nil
	}}

	// Capture every parent wake the async notifier publishes, keyed by the
	// steer_message_id metadata WakeParentAlways attaches to each event.
	var mu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})

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
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	mu.Lock()
	defer mu.Unlock()
	assertGoal984OneParentWakeVerdictAcked(t, inbox, parentMeta.ID, rec, wakeIDs)
}

func assertGoal984OneParentWakeVerdictAcked(t *testing.T, inbox *session.MessageInboxStore, parentID string, rec *session.LifecycleRecord, wakeIDs []string) {
	t.Helper()
	handbackID := fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
	if len(wakeIDs) != 1 {
		t.Fatalf("parent wakes = %d (%v), want exactly 1 — Q1=A: on the session-goal met path only the handback wakes the parent, the verdict entry is stored not woken", len(wakeIDs), wakeIDs)
	}
	if wakeIDs[0] != handbackID {
		t.Fatalf("the single parent wake carries message id %q, want the handback %q", wakeIDs[0], handbackID)
	}

	// The verdict entry was stored (two entries stand — decision (b)) and is
	// acked at hand-back time; only the handback stays unacked for the parent
	// to consume.
	entries, err := inbox.Entries(parentID)
	if err != nil {
		t.Fatalf("inbox.Entries(parent): %v", err)
	}
	ackedIDs := make(map[string]bool)
	messageIDs := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch entry.Kind {
		case session.InboxEntryAck:
			for _, id := range entry.AckedIDs {
				ackedIDs[id] = true
			}
		case session.InboxEntryMessage:
			if entry.Message == nil {
				continue
			}
			messageIDs = append(messageIDs, messageIDOf(*entry.Message))
		}
	}
	if len(messageIDs) != 2 {
		t.Fatalf("parent inbox entries = %d (%v), want exactly 2 — decision (b)'s two-entry contract stands (verdict + handback)", len(messageIDs), messageIDs)
	}
	var verdictID string
	for _, id := range messageIDs {
		if id != handbackID {
			verdictID = id
		}
	}
	if verdictID == "" || !ackedIDs[verdictID] {
		t.Fatalf("verdict entry %q must exist and be acked at hand-back time (acked=%v) — Q1=A: the unacked verdict would be re-woken by boot recovery", verdictID, ackedIDs[verdictID])
	}
	if ackedIDs[handbackID] {
		t.Fatalf("handback %q must stay unacked for the parent to consume", handbackID)
	}
	unacked, _, _, err := inbox.Drain(parentID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	if len(unacked) != 1 || messageIDOf(unacked[0]) != handbackID {
		gotIDs := make([]string, 0, len(unacked))
		for _, msg := range unacked {
			gotIDs = append(gotIDs, messageIDOf(msg))
		}
		t.Fatalf("unacked entries = %v, want exactly the handback %q", gotIDs, handbackID)
	}
}

// TestBoot984_FinishFromFinal_InboxFinalAloneCannotPromoteOrEndGoal pins
// D8.5/round-3 CRIT-001 (supersedes architect finding F2's pair-end, which
// this test's former name ...FinishFromFinalPairEndsSessionGoal asserted;
// changed-test list entry 3): an inbox final WITHOUT a matching committed
// lifecycle/outbox outcome cannot repair a working lifecycle record —
// "finishFromFinal is replaced by commit-based reconciliation, not an
// inbox-first promotion" (D8.5). The record stays non-terminal for the
// ordinary boot stop to land stopped(restart) (D8.3), its session-owned
// goal is untouched (D6: no stop of any kind ends a goal), and the FD1=A
// EndSessionGoal pair-end hook is gone (MIN-001: remove every
// restart/terminal-triggered session-goal ending).
func TestBoot984_FinishFromFinal_InboxFinalAloneCannotPromoteOrEndGoal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	h.persist(t, rec)
	goalID := activateTestGoalRecord(t, child, "boot final reconciliation")

	var hooked []string
	recovery := h.recovery()
	recovery.EndSessionGoal = func(sid, reason string) {
		hooked = append(hooked, sid)
		al.EndSessionOwnedGoalOnTerminal(sid, reason)
	}
	msg := bootHandback(t, child, parent, "boot-final-pairend")
	if err := recovery.finishFromFinal(rec, msg); err != nil {
		t.Fatalf("finishFromFinal: %v", err)
	}

	loaded, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if loaded.Terminal() {
		t.Fatalf("an inbox final with no committed lifecycle/outbox outcome promoted the record to %q — "+
			"D8.5: finishFromFinal must not turn a non-terminal record into done/failed merely because an inbox final exists",
			loaded.State)
	}
	g, err := resolveGoalRecordStore().Get(goalID)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	if g.State != generated.GoalStateActive {
		t.Errorf("session-owned goal state = %q after the boot repair — D6: no stop of any kind ends a goal; only clear_goal does", g.State)
	}
	if len(hooked) != 0 {
		t.Errorf("EndSessionGoal pair-end hook fired %v — MIN-001: every restart/terminal-triggered session-goal ending is removed", hooked)
	}
}

// TestBoot984_SweepLeavesSteeredRecordsToBootRecovery pins D8.3's
// single-writer rule (supersedes architect finding F3's steered pair-end,
// formerly ...SweepPairEndsSteeredGoal; changed-test list entry 4):
// "PlanEngine.bootSweep still leaves steered records to SteerBootRecovery
// (avoids a second writer racing the same record)." The sweep must not land
// a steered record on failed(interrupted) and must not fire
// steeredGoalEndHook — the record stays for SteerBootRecovery to land
// stopped(restart) with its direct-parent notice, goal untouched (D6).
// Task-origin records keep the sweep's ordinary behaviour with their goal
// active, and standing roots stay exempt.
func TestBoot984_SweepLeavesSteeredRecordsToBootRecovery(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	h := newBootSweepHarness(t)

	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-steered-goal", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-task-origin", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-fix-1"},
	})
	persistLifecycle(t, h.ls, &session.LifecycleRecord{
		SessionID: "sess-standing-root", Generation: 1, State: session.LifecycleRunning,
		WorkspaceID: "ws", AgentID: "agent-1",
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	})
	steeredGoal := activateTestGoalRecord(t, "sess-steered-goal", "sweep leaves steered")
	taskSessionGoal := activateTestGoalRecord(t, "sess-task-origin", "task session goal stays")
	rootGoal := activateTestGoalRecord(t, "sess-standing-root", "standing root goal stays active")

	var pairEnded []string
	h.pe.SetSteeredGoalEndHook(func(sid, reason string) {
		pairEnded = append(pairEnded, sid)
		al.EndSessionOwnedGoalOnTerminal(sid, reason)
	})
	res := h.pe.runBootSweep(context.Background())
	_ = res

	steered, err := h.ls.Load("sess-steered-goal")
	if err != nil {
		t.Fatalf("Load(steered): %v", err)
	}
	if steered.State != session.LifecycleRunning {
		t.Fatalf("steered record state after the Plan Engine sweep = %q, want running — "+
			"D8.3: PlanEngine.bootSweep leaves steered records to SteerBootRecovery; "+
			"no second writer races the same record", steered.State)
	}
	if len(pairEnded) != 0 {
		t.Errorf("steeredGoalEndHook fired %v — MIN-001: the plan-sweep session-goal ending is removed; "+
			"the steered record's goal is untouched (D6)", pairEnded)
	}
	sg, err := resolveGoalRecordStore().Get(steeredGoal)
	if err != nil {
		t.Fatalf("Get(steeredGoal): %v", err)
	}
	if sg.State != generated.GoalStateActive {
		t.Fatalf("steered session's goal = %q after the sweep — D6: the sweep neither lands the record nor ends its goal", sg.State)
	}
	taskG, err := resolveGoalRecordStore().Get(taskSessionGoal)
	if err != nil {
		t.Fatalf("Get(taskSessionGoal): %v", err)
	}
	if !goal.IsActiveState(taskG.State) {
		t.Fatalf("task-origin session's goal = %q, want still active — the sweep's ordinary task behaviour ends no goal", taskG.State)
	}
	rootG, err := resolveGoalRecordStore().Get(rootGoal)
	if err != nil {
		t.Fatalf("Get(rootGoal): %v", err)
	}
	if !goal.IsActiveState(rootG.State) {
		t.Fatalf("standing root's goal = %q, want still active — ordinary roots are exempt from the sweep", rootG.State)
	}
}

// TestGoal984_GoalEnderRoutesDeferredChildThroughTail keeps the original F4
// deferred-child fixtures, but uses corrected sub-agent control-plane ADR D6:
// stopped is non-terminal and gets an independent direct-parent notice, not
// a fatal terminal completion. The dispatcher explicitly ruled on these
// already-exited turns; this is NOT permission to stop a live turn on clear.
// A deliberate clear ends its goal. Idle goal expiry is separately authorized
// by planning-goals-spec FR-064 and D6's explicit idle-policy exception.
func TestGoal984_GoalEnderRoutesDeferredChildThroughTail(t *testing.T) {
	t.Run("goal clear", testGoal984ClearDeferredChild)
	t.Run("idle expiry sweep", testGoal984IdleExpiryDeferredChild)
}

func testGoal984ClearDeferredChild(t *testing.T) {
	t.Helper()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycleDir, inboxDir := t.TempDir(), t.TempDir()
	lifecycle := session.NewLifecycleStore(lifecycleDir)
	inbox := session.NewMessageInboxStore(inboxDir)
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	wakeCount := observeU1ParentNoticeWakes(t, al, parentMeta.ID)
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "deferred at the gate",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-f4-clear"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		t.Fatalf("Persist(running): %v", persistErr)
	}
	if ts := al.getActiveTurnState(rec.SessionID); ts != nil && ts.IsAlive() {
		t.Fatal("fixture must have no live turn: D6 says goal clear does not itself stop a live turn")
	}
	if g := goalRecordForSession(t, rec.SessionID); g.State != generated.GoalStateActive {
		t.Fatalf("fixture goal state = %q, want active", g.State)
	}

	reply := al.clearGoalByUser(rec.SessionID, al.GetSessionStore(), "native-agent")
	if reply == "" {
		t.Fatal("clearGoalByUser returned an empty reply, want the Goal cleared line")
	}
	assertCleared := func() {
		t.Helper()
		g, gerr := resolveGoalRecordStore().Get(rec.GoalRef)
		if gerr != nil {
			t.Fatalf("Get(deliberately cleared goal): %v", gerr)
		}
		if g.GoalID != rec.GoalRef || g.State != generated.GoalStateCleared {
			t.Errorf("deliberate clear goal id/state = %s/%q, want %s/cleared (D6 explicit-clear exception)", g.GoalID, g.State, rec.GoalRef)
		}
	}
	assertCleared()
	noticeID, stopNote := assertU1StoppedChildNotice(t, al, parentMeta.ID, rec, "", "")
	assertU1NoticeStable(t, al, parentMeta.ID, rec, "", "", noticeID, stopNote, wakeCount)

	// A repeated clear is not another stopped transition. The direct-parent
	// notice must also survive store reopening and repeated boot replay.
	al.clearGoalByUser(rec.SessionID, al.GetSessionStore(), "native-agent")
	assertU1NoticeStable(t, al, parentMeta.ID, rec, "", "", noticeID, stopNote, wakeCount)
	for range 2 { // D6/T6: repeated boot must not duplicate the notice/wake.
		replayU1StoppedNotices(t, al, lifecycleDir, inboxDir)
		assertU1NoticeStable(t, al, parentMeta.ID, rec, "", "", noticeID, stopNote, wakeCount)
		assertCleared()
	}
}

func testGoal984IdleExpiryDeferredChild(t *testing.T) {
	t.Helper()
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycleDir, inboxDir := t.TempDir(), t.TempDir()
	lifecycle := session.NewLifecycleStore(lifecycleDir)
	inbox := session.NewMessageInboxStore(inboxDir)
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	wakeCount := observeU1ParentNoticeWakes(t, al, parentMeta.ID)
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "deferred at the gate (idle)",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-f4-idle"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		t.Fatalf("Persist(running): %v", persistErr)
	}
	if ts := al.getActiveTurnState(rec.SessionID); ts != nil && ts.IsAlive() {
		t.Fatal("idle-expiry fixture must be deferred with no live turn")
	}
	if g := goalRecordForSession(t, rec.SessionID); g.State != generated.GoalStateActive {
		t.Fatalf("fixture goal state = %q, want active", g.State)
	}
	// FR-064/FR-038 independently authorize the default seven-day calendar
	// brake. This explicit aged-goal policy, not stopping, ends the goal.
	now := time.Now()
	gs := resolveGoalRecordStore()
	if _, uerr := gs.Update(rec.GoalRef, func(cur *goal.Goal) error {
		cur.LastActivityAt = now.Add(-7 * 24 * time.Hour)
		return nil
	}); uerr != nil {
		t.Fatalf("backdate activity: %v", uerr)
	}

	al.goalIdleExpirySweep(config.PlanningConfig{}, now)
	assertExpired := func() {
		t.Helper()
		g, gerr := gs.Get(rec.GoalRef)
		if gerr != nil {
			t.Fatalf("Get(idle-expired goal): %v", gerr)
		}
		if g.GoalID != rec.GoalRef || g.State != generated.GoalStateExpired {
			t.Errorf("idle policy goal id/state = %s/%q, want %s/expired (FR-064, D6 independent goal policy)", g.GoalID, g.State, rec.GoalRef)
		}
		reason := strings.ToLower(g.TerminalReason)
		if !strings.Contains(reason, "idle") || !strings.Contains(reason, "expir") {
			t.Errorf("goal reason = %q, want independent idle expiry, not closure because the session stopped", g.TerminalReason)
		}
	}
	assertExpired()
	noticeID, stopNote := assertU1StoppedChildNotice(t, al, parentMeta.ID, rec, "", "")
	assertU1NoticeStable(t, al, parentMeta.ID, rec, "", "", noticeID, stopNote, wakeCount)

	al.goalIdleExpirySweep(config.PlanningConfig{}, now)
	assertU1NoticeStable(t, al, parentMeta.ID, rec, "", "", noticeID, stopNote, wakeCount)
	for range 2 { // D6/T6: repeated boot must not duplicate the notice/wake.
		replayU1StoppedNotices(t, al, lifecycleDir, inboxDir)
		assertU1NoticeStable(t, al, parentMeta.ID, rec, "", "", noticeID, stopNote, wakeCount)
		assertExpired()
	}
}

// TestGoal984_RoundBoundArmRoutesChildThroughTail pins the judged
// round-bound arm (goal_triggers.go::runGoalAdjudication's
// `attempt >= maxRounds` block): an UNMET verdict at the round bound ends
// the goal (rounds_exhausted) and hands the child to the completion tail
// with a nil error — the child reaches Completed and the parent receives
// the deterministic handback. This arm landed in PR #984 (decision (b));
// the post-merge pr-test-analyzer asked for it to be pinned so a future
// edit cannot silently strand the child `running` above a settled goal.
func TestGoal984_RoundBoundArmRoutesChildThroughTail(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "prove the bound",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-roundbound"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		t.Fatalf("Persist(running): %v", persistErr)
	}

	// Push the record to one round before its bound so this single unmet
	// verdict exhausts the budget.
	if _, uerr := resolveGoalRecordStore().Update(rec.GoalRef, func(cur *goal.Goal) error {
		cur.Round = cur.MaxRounds - 1
		return nil
	}); uerr != nil {
		t.Fatalf("Update(Round): %v", uerr)
	}

	type criterionVerdict struct {
		ID     string `json:"id"`
		Met    bool   `json:"met"`
		Reason string `json:"reason"`
	}
	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	verdicts := make([]criterionVerdict, 0, len(g.Criteria)+len(g.DoD))
	for _, criterion := range append(append([]task.AcceptanceCriterion{}, g.Criteria...), g.DoD...) {
		verdicts = append(verdicts, criterionVerdict{ID: criterion.ID, Met: false, Reason: "not yet"})
	}
	body, err := json.Marshal(map[string]any{"met": false, "criteria": verdicts})
	if err != nil {
		t.Fatalf("Marshal(verdict): %v", err)
	}
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: string(body)}, nil
	}}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	result := turnResult{finalContent: "[goal:evidence] claiming done\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)
	select {
	case got := <-done:
		if got != rec.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, rec.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	g2, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal) after: %v", err)
	}
	if !goal.IsTerminalState(g2.State) {
		t.Fatalf("goal state = %q, want terminal after the round bound", g2.State)
	}
	if !strings.Contains(g2.TerminalReason, "round bound reached") {
		t.Fatalf("goal TerminalReason = %q, want the round-bound note", g2.TerminalReason)
	}
	loaded, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child) after: %v", err)
	}
	if loaded.State != session.LifecycleCompleted {
		t.Fatalf("child state = %q, want completed — the round-bound arm must route the child through the completion tail", loaded.State)
	}
	entries, err := inbox.Entries(parentMeta.ID)
	if err != nil {
		t.Fatalf("inbox.Entries(parent): %v", err)
	}
	handbackID := fmt.Sprintf("%s:%d:final", rec.SessionID, rec.Generation)
	var handbacks int
	var deliveredHandbackID string
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		cls, cerr := session.ClassifySessionMessage(*entry.Message)
		if cerr != nil {
			continue
		}
		if cls.Kind == "handback" {
			handbacks++
			handback, herr := entry.Message.AsSessionMessageHandback()
			if herr != nil {
				t.Fatalf("AsSessionMessageHandback: %v", herr)
			}
			deliveredHandbackID = handback.MessageId
		}
	}
	if handbacks != 1 {
		t.Fatalf("parent handback entries = %d, want exactly 1 (%s)", handbacks, handbackID)
	}
	if deliveredHandbackID != handbackID {
		t.Fatalf("handback MessageId = %q, want %q (deterministic <child>:<gen>:final)", deliveredHandbackID, handbackID)
	}
}

// TestGoalParkUpwardText_StripsControlLinesAndPrefersEvidence pins the
// helper's documented parent-facing text contract for both park outcomes:
// a waiting question falls back to final content without its GOAL_STATUS
// control line, while a blocked tool claim uses its evidence instead of the
// turn's prose.
func TestGoalParkUpwardText_StripsControlLinesAndPrefersEvidence(t *testing.T) {
	tests := []struct {
		name         string
		evidence     string
		finalContent string
		want         string
	}{
		{
			name:         "waiting question strips control line",
			evidence:     " \t ",
			finalContent: "Which storage format should I use?\n  GOAL_STATUS: waiting_on_user  ",
			want:         "Which storage format should I use?",
		},
		{
			name:         "blocked claim prefers trimmed evidence",
			evidence:     "  Missing credentials for the upstream API.  ",
			finalContent: "I cannot continue until credentials are configured.\nGOAL_STATUS: waiting_on_user",
			want:         "Missing credentials for the upstream API.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := goalParkUpwardText(tt.evidence, tt.finalContent); got != tt.want {
				t.Fatalf("goalParkUpwardText(%q, %q) = %q, want %q", tt.evidence, tt.finalContent, got, tt.want)
			}
		})
	}
}

// TestGoal984_BlockedParkDeliversBlockerUpward pins handleOutcome's blocked
// arm (goal_loop.go, JUDGE-FR-093 + design-note decision (d)): a goal_claim
// tool `blocked` claim on a goal-bearing steered child parks the goal
// WITHOUT ending it, records the claim on the record, and delivers a
// blocker message to the parent through the one upward path — never
// silence. Reachable only through the tool channel (FR-091 keeps `blocked`
// out of the prose marker parser).
func TestGoal984_BlockedParkDeliversBlockerUpward(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentMeta.ID,
		TargetAgentID:     "native-agent",
		Task:              "blocked park pin",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-blocked"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	rec, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	rec.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(rec); persistErr != nil {
		t.Fatalf("Persist(running): %v", persistErr)
	}

	// A blocked claim is reachable ONLY through the goal_claim tool channel:
	// append a successful tool-call entry the post-turn scan will find.
	goalID := ""
	if g, gerr := resolveGoalRecordStore().Get(rec.GoalRef); gerr == nil {
		goalID = g.GoalID
	}
	claimResult, merr := json.Marshal(map[string]any{
		"status":   "blocked",
		"evidence": "missing credentials for the upstream API",
		"goal_id":  goalID,
	})
	if merr != nil {
		t.Fatalf("Marshal(claim): %v", merr)
	}
	if terr := al.GetSessionStore().AppendTranscriptStrict(rec.SessionID, session.TranscriptEntry{
		ID:        "tc-blocked-1",
		Type:      session.EntryTypeToolCall,
		Role:      "assistant",
		Timestamp: time.Now(),
		ToolCalls: []session.ToolCall{{
			ID:     "tc-blocked-1",
			Tool:   tools.GoalClaimToolName,
			Status: "success",
			Result: map[string]any{"text": string(claimResult)},
		}},
	}); terr != nil {
		t.Fatalf("AppendTranscriptStrict(tool call): %v", terr)
	}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	result := turnResult{finalContent: "I am blocked: missing credentials for the upstream API."}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)

	// The park: the goal stays ACTIVE (a pause, not an ending) with the
	// blocked claim recorded on the record, and the blocked flag set.
	g, err := resolveGoalRecordStore().Get(rec.GoalRef)
	if err != nil {
		t.Fatalf("Get(goal) after: %v", err)
	}
	if !goal.IsActiveState(g.State) {
		t.Fatalf("goal state = %q, want active — a blocked park is a pause, not an ending", g.State)
	}
	if g.LatestClaim == nil || g.LatestClaim.Status != generated.GoalLatestClaimStatusBlocked {
		t.Fatalf("LatestClaim = %+v, want status blocked recorded on the record", g.LatestClaim)
	}
	if !al.goalIsBlocked(g.GoalID) {
		t.Fatal("goalIsBlocked = false, want true after the blocked claim")
	}
	loaded, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(child) after: %v", err)
	}
	if loaded.State != session.LifecycleRunning {
		t.Fatalf("child state = %q, want running — a park must not terminalise the child", loaded.State)
	}

	// The parent was told: exactly one blocker entry, carrying the claim's
	// evidence text, wake-eligible.
	entries, err := inbox.Entries(parentMeta.ID)
	if err != nil {
		t.Fatalf("inbox.Entries(parent): %v", err)
	}
	blockers := 0
	var blockerText string
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
			continue
		}
		cls, cerr := session.ClassifySessionMessage(*entry.Message)
		if cerr != nil || cls.Kind != "blocker" {
			continue
		}
		blockers++
		if blocker, berr := entry.Message.AsSessionMessageBlocker(); berr == nil {
			blockerText = blocker.Text
		}
	}
	if blockers != 1 {
		t.Fatalf("parent blocker entries = %d, want exactly 1 — decision (d): a blocked park tells the parent", blockers)
	}
	if !strings.Contains(blockerText, "missing credentials") {
		t.Fatalf("blocker text = %q, want the claim's evidence", blockerText)
	}
}

// TestBoot984_FailInterruptedLandsStoppedRestartKeepsGoal pins D8.3 on the
// mid-flight arm (supersedes the FD1=A pair-end this test's former name
// ...FailInterruptedPairEnd asserted; changed-test list entry 5):
// interrupted-terminal recovery becomes an ordinary stop — the child lands
// `stopped` (non-terminal) with a stop note cause "restart", never
// failed(interrupted) (F0929-3). No goal-ending step exists in the
// restart-stop path: the child's session-owned goal stays active (D6) and
// the EndSessionGoal hook is removed (MIN-001). The ancestor's goal was
// never in scope and stays active.
func TestBoot984_FailInterruptedLandsStoppedRestartKeepsGoal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	h.persist(t, rec)
	childGoal := activateTestGoalRecord(t, child, "failInterrupted restart keep")
	parentGoal := activateTestGoalRecord(t, parent, "ancestor goal untouched")

	var hooked []string
	recovery := h.recovery()
	recovery.EndSessionGoal = func(sid, reason string) {
		hooked = append(hooked, sid)
		al.EndSessionOwnedGoalOnTerminal(sid, reason)
	}
	if err := recovery.failInterrupted(rec); err != nil {
		t.Fatalf("failInterrupted: %v", err)
	}

	loaded, err := h.lifecycle.Load(child)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if loaded.State != session.LifecycleStopped || loaded.Terminal() {
		t.Fatalf("child state after failInterrupted = %q (terminal=%v), want stopped non-terminal — "+
			"D8.3: an affected session becomes stopped with cause restart, an ordinary stop, not failed(interrupted)",
			loaded.State, loaded.Terminal())
	}
	if loaded.StopNote == nil || loaded.StopNote.Cause != session.StopCauseRestart {
		t.Fatalf("stop note after failInterrupted = %+v, want cause %q (D8.3: cause restart, not a restart_interrupt note)", loaded.StopNote, session.StopCauseRestart)
	}
	if len(hooked) != 0 {
		t.Errorf("EndSessionGoal hook fired %v — MIN-001/D8.3: no goal-ending step exists in the restart-stop path", hooked)
	}
	cg, err := resolveGoalRecordStore().Get(childGoal)
	if err != nil {
		t.Fatalf("Get(childGoal): %v", err)
	}
	if cg.State != generated.GoalStateActive {
		t.Fatalf("child's goal = %q, want active — D8.3: the restart stop leaves the session-owned goal untouched", cg.State)
	}
	pg, err := resolveGoalRecordStore().Get(parentGoal)
	if err != nil {
		t.Fatalf("Get(parentGoal): %v", err)
	}
	if !goal.IsActiveState(pg.State) {
		t.Fatalf("ancestor's goal = %q, want active — a child's restart stop never touches the ancestor's goal", pg.State)
	}
}
