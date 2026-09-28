package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
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
	entries, err := inbox.Entries(parentMeta.ID)
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
	unacked, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
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

// TestBoot984_FinishFromFinalPairEndsSessionGoal pins architect finding F2
// (rev984-architect): finishFromFinal's terminal write — the boot repair for
// "delivered-but-not-terminal" — carries no pair-end, so a child whose final
// report was delivered but crashed before its terminal state landed leaves
// its session-owned goal ACTIVE on a terminal session (FD1=A residue bounded
// only by the 7-day idle-expiry brake). Post-fix the terminal write ends the
// session-owned goal with the session, through the same EndSessionGoal seam
// failInterrupted already uses.
func TestBoot984_FinishFromFinalPairEndsSessionGoal(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	h := newBootRecoveryHarness(t)
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	h.persist(t, rec)
	goalID := activateTestGoalRecord(t, child, "boot final pair-end")

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
	if loaded.State != session.LifecycleCompleted {
		t.Fatalf("child state after finishFromFinal = %q, want completed", loaded.State)
	}
	g, err := resolveGoalRecordStore().Get(goalID)
	if err != nil {
		t.Fatalf("Get(goal): %v", err)
	}
	if !goal.IsTerminalState(g.State) {
		t.Fatalf("session-owned goal state = %q after its session went terminal — F2: the pair must end together at the boot repair", g.State)
	}
	if len(hooked) != 1 || hooked[0] != child {
		t.Fatalf("EndSessionGoal hook fired %v, want exactly once with %q", hooked, child)
	}
}

// TestBoot984_SweepPairEndsSteeredGoal pins architect finding F3: the Plan
// Engine boot sweep lands steered records on failed(interrupted) with no
// pair-end, so a steered child stranded at crash keeps its ACTIVE goal on a
// failed session. Post-fix sweepToFailedInterrupted fires the
// steeredGoalEndHook (wired to AgentLoop.EndSessionOwnedGoalOnTerminal at
// gateway boot) for steered records ONLY — never for task-origin records (no
// steered edge), and ordinary roots are exempt from the sweep entirely, so a
// standing root's goal stays active untouched.
func TestBoot984_SweepPairEndsSteeredGoal(t *testing.T) {
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
	steeredGoal := activateTestGoalRecord(t, "sess-steered-goal", "sweep pair-end")
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
	if err != nil || steered.State != session.LifecycleFailed {
		t.Fatalf("steered record state = %q (err=%v), want failed(interrupted)", steered.State, err)
	}
	g, err := resolveGoalRecordStore().Get(steeredGoal)
	if err != nil {
		t.Fatalf("Get(steeredGoal): %v", err)
	}
	if !goal.IsTerminalState(g.State) {
		t.Fatalf("steered session's goal = %q after the sweep — F3: a steered record swept to failed(interrupted) ends its session-owned goal", g.State)
	}
	if len(pairEnded) != 1 || pairEnded[0] != "sess-steered-goal" {
		t.Fatalf("steeredGoalEndHook fired %v, want exactly once with the steered id only", pairEnded)
	}
	taskG, err := resolveGoalRecordStore().Get(taskSessionGoal)
	if err != nil {
		t.Fatalf("Get(taskSessionGoal): %v", err)
	}
	if !goal.IsActiveState(taskG.State) {
		t.Fatalf("task-origin session's goal = %q, want still active — the pair-end is steered-only", taskG.State)
	}
	rootG, err := resolveGoalRecordStore().Get(rootGoal)
	if err != nil {
		t.Fatalf("Get(rootGoal): %v", err)
	}
	if !goal.IsActiveState(rootG.State) {
		t.Fatalf("standing root's goal = %q, want still active — ordinary roots are exempt from the sweep", rootG.State)
	}
}

// TestGoal984_GoalEnderRoutesDeferredChildThroughTail pins architect finding
// F4: /goal clear and the idle-expiry sweep end a goal whose steered child
// sits deferred at the (a) completion gate (turn already exited, record still
// non-terminal) — and the gate only re-runs on a turn exit or cancel, so the
// child record stayed `running` until the next boot sweep repaired it. Both
// goal enders now route such a child through the completion tail
// (completeSteeredTurnIfDeferredAtGate): the parent is told (interrupted
// handback), the record goes terminal (cancelled), and ordinary roots are
// never touched (completeSteeredTurn refuses without a steered edge).
func TestGoal984_GoalEnderRoutesDeferredChildThroughTail(t *testing.T) {
	t.Run("goal clear", func(t *testing.T) {
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

		reply := al.clearGoalByUser(rec.SessionID, al.GetSessionStore(), "native-agent")
		if reply == "" {
			t.Fatal("clearGoalByUser returned an empty reply, want the Goal cleared line")
		}

		loaded, err := lifecycle.Load(rec.SessionID)
		if err != nil {
			t.Fatalf("Load(child) after clear: %v", err)
		}
		if !loaded.Terminal() {
			t.Fatalf("child state after /goal clear = %q — F4: a deferred steered child must reach a terminal state the parent sees", loaded.State)
		}
		if loaded.State != session.LifecycleCancelled {
			t.Fatalf("child state = %q, want cancelled (the operator ended the goal)", loaded.State)
		}
		entries, err := inbox.Entries(parentMeta.ID)
		if err != nil {
			t.Fatalf("inbox.Entries(parent): %v", err)
		}
		// The F4 tail's outcome for an operator-ended goal is interrupted →
		// completionMessage encodes it as a FATAL ERROR message (not a
		// handback — completionMessage only writes a handback for
		// final_answer), deterministic <child>:<gen>:final id, wake-eligible
		// (fatal errors wake). Count THAT.
		var fatalErrs int
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
				continue
			}
			cls, cerr := session.ClassifySessionMessage(*entry.Message)
			if cerr != nil || cls.Kind != "error" || !cls.Fatal {
				continue
			}
			fatalErrs++
		}
		if fatalErrs != 1 {
			t.Fatalf("parent fatal-error entries = %d, want exactly 1 (the interrupted report)", fatalErrs)
		}
	})

	t.Run("idle expiry sweep", func(t *testing.T) {
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
		// Backdate the goal's last activity past the idle-expiry horizon.
		gs := resolveGoalRecordStore()
		if _, uerr := gs.Update(rec.GoalRef, func(cur *goal.Goal) error {
			cur.LastActivityAt = time.Now().Add(-30 * 24 * time.Hour)
			return nil
		}); uerr != nil {
			t.Fatalf("backdate activity: %v", uerr)
		}

		al.goalIdleExpirySweep(config.PlanningConfig{}, time.Now())

		loaded, err := lifecycle.Load(rec.SessionID)
		if err != nil {
			t.Fatalf("Load(child) after sweep: %v", err)
		}
		if !loaded.Terminal() {
			t.Fatalf("child state after idle-expiry sweep = %q — F4: the sweep must route the deferred child through the tail", loaded.State)
		}
		if loaded.State != session.LifecycleCancelled {
			t.Fatalf("child state = %q, want cancelled", loaded.State)
		}
		entries, err := inbox.Entries(parentMeta.ID)
		if err != nil {
			t.Fatalf("inbox.Entries(parent): %v", err)
		}
		// The F4 tail's outcome for an operator-ended goal is interrupted →
		// completionMessage encodes it as a FATAL ERROR message (not a
		// handback — completionMessage only writes a handback for
		// final_answer), deterministic <child>:<gen>:final id, wake-eligible
		// (fatal errors wake). Count THAT.
		var fatalErrs int
		for _, entry := range entries {
			if entry.Kind != session.InboxEntryMessage || entry.Message == nil {
				continue
			}
			cls, cerr := session.ClassifySessionMessage(*entry.Message)
			if cerr != nil || cls.Kind != "error" || !cls.Fatal {
				continue
			}
			fatalErrs++
		}
		if fatalErrs != 1 {
			t.Fatalf("parent fatal-error entries = %d, want exactly 1", fatalErrs)
		}
	})
}
