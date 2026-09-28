package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

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
