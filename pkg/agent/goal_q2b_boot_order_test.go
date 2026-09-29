package agent

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Q7=A: a boot sweep interrupts a pending owner; visiting its descendant first
// cannot schedule a completion re-evaluation or deliver a completion hand-back.
func TestGoalQ2B_BootDescendantFirstCannotResumePendingOwner(t *testing.T) {
	h := newQ2BHarness(t, "q2b-boot-descendant-first")
	descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-boot-descendant-first-worker")
	recovery := q2bBootRecoveryForHarness(h)
	recovery.orderSessionIDs = func(ids []string) []string {
		ordered := []string{descendant.SessionID}
		found := false
		for _, id := range ids {
			if id == descendant.SessionID {
				found = true
				continue
			}
			ordered = append(ordered, id)
		}
		if !found {
			t.Fatalf("boot session IDs omitted descendant %q", descendant.SessionID)
		}
		return ordered
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run(descendant first): %v", err)
	}

	for label, id := range map[string]string{"pending owner": h.child.SessionID, "descendant": descendant.SessionID} {
		rec, err := h.lifecycle.Load(id)
		if err != nil {
			t.Fatalf("Load(%s): %v", label, err)
		}
		if rec.State != session.LifecycleFailed || rec.FailedReason != "interrupted" {
			t.Errorf("%s after boot: state=%q reason=%q, want failed/interrupted", label, rec.State, rec.FailedReason)
		}
	}
	ended := h.goalRecord()
	if ended.State != generated.GoalStateCleared || !strings.Contains(ended.TerminalReason, "interrupted") {
		t.Errorf("owner goal after boot: state=%q reason=%q, want cleared/interrupted", ended.State, ended.TerminalReason)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls after descendant-first boot = %d, want 0", calls)
	}
	if reevaluations := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); reevaluations != 0 {
		t.Errorf("re-evaluations after descendant-first boot = %d, want 0", reevaluations)
	}
	for _, message := range h.parentMessages() {
		envelope, err := decodeBootMessage(message)
		if err != nil {
			t.Fatalf("decode parent boot message: %v", err)
		}
		if envelope.Kind == "handback" {
			t.Errorf("completion hand-back delivered to parent after restart: %+v", envelope)
		}
	}
	for _, wake := range h.parentWakeEvents() {
		if wake.SourceKind == "message_parent:handback" {
			t.Errorf("completion hand-back woke parent after restart: %+v", wake)
		}
	}
}

// Q7=A, owner-first order (fix-890 squad-lead contract E): the same ruling
// as TestGoalQ2B_BootDescendantFirstCannotResumePendingOwner, driven through
// the SAME real Run() entrypoint with the adversarial order reversed — the
// pending owner visited BEFORE its descendant. orderSessionIDs (boot_sweep.go)
// is the test seam that forces this: Run()'s own sessionIDs() scan is
// lexicographic by ULID-timestamped session id, so the owner (created first,
// lexicographically earlier) is already visited before its descendant in the
// harness's natural id order — this test forces the seam explicitly rather
// than relying on that incidental ordering, so it stays adversarial-by-
// construction even if a future harness change reorders creation. Q7=A does
// not distinguish visit order: whichever session Run() reaches first, the
// pending (non-terminal, non-NeedsInput) owner is always interrupted before
// any completion re-evaluation could reach it.
func TestGoalQ2B_BootOwnerFirstCannotResumePendingOwner(t *testing.T) {
	h := newQ2BHarness(t, "q2b-boot-owner-first")
	descendant := q2bPendingClaimWithRunningDescendant(t, h, "q2b-boot-owner-first-worker")
	recovery := q2bBootRecoveryForHarness(h)
	recovery.orderSessionIDs = func(ids []string) []string {
		ordered := []string{h.child.SessionID}
		found := false
		for _, id := range ids {
			if id == h.child.SessionID {
				found = true
				continue
			}
			ordered = append(ordered, id)
		}
		if !found {
			t.Fatalf("boot session IDs omitted pending owner %q", h.child.SessionID)
		}
		return ordered
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("Run(owner first): %v", err)
	}

	for label, id := range map[string]string{"pending owner": h.child.SessionID, "descendant": descendant.SessionID} {
		rec, err := h.lifecycle.Load(id)
		if err != nil {
			t.Fatalf("Load(%s): %v", label, err)
		}
		if rec.State != session.LifecycleFailed || rec.FailedReason != "interrupted" {
			t.Errorf("%s after boot: state=%q reason=%q, want failed/interrupted", label, rec.State, rec.FailedReason)
		}
	}
	ended := h.goalRecord()
	if ended.State != generated.GoalStateCleared || !strings.Contains(ended.TerminalReason, "interrupted") {
		t.Errorf("owner goal after boot: state=%q reason=%q, want cleared/interrupted", ended.State, ended.TerminalReason)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls after owner-first boot = %d, want 0", calls)
	}
	if reevaluations := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); reevaluations != 0 {
		t.Errorf("re-evaluations after owner-first boot = %d, want 0", reevaluations)
	}
	for _, message := range h.parentMessages() {
		envelope, err := decodeBootMessage(message)
		if err != nil {
			t.Fatalf("decode parent boot message: %v", err)
		}
		if envelope.Kind == "handback" {
			t.Errorf("completion hand-back delivered to parent after restart: %+v", envelope)
		}
	}
	for _, wake := range h.parentWakeEvents() {
		if wake.SourceKind == "message_parent:handback" {
			t.Errorf("completion hand-back woke parent after restart: %+v", wake)
		}
	}
}

// Two wakes queued at the admission cap must both reach the next promoted
// turn, in arrival order; the second must not overwrite the first.
func TestGoalQ2B_TwoQueuedWakesBothReachPromotedTurn(t *testing.T) {
	h := newQ2BHarness(t, "q2b-two-queued-wakes")
	fillers := q2bFillAdmissionCap(t, h.al)
	const first = "First wake: inspect the delegated report"
	const second = "Second wake: include the new evidence"
	for i, content := range []string{first, second} {
		msg := bus.InboundMessage{
			Channel: "system", AsyncTranscriptSessionID: h.child.SessionID,
			Content: content,
			Metadata: map[string]string{
				"steer_message_id": "q2b-two-wakes-" + strconv.Itoa(i),
				"steer_generation": strconv.Itoa(h.child.Generation),
			},
		}
		if _, err := h.al.processSteeredSystemWake(context.Background(), msg); err != nil {
			t.Fatalf("queue wake %d: %v", i, err)
		}
	}
	queued, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(queued child): %v", err)
	}
	if queued.State != session.LifecycleQueued {
		t.Fatalf("queued child state = %q, want queued", queued.State)
	}

	captured := make(chan string, 1)
	var once sync.Once
	oldHook := turnRegisteredTestHook
	turnRegisteredTestHook = func(sessionID string, ts *turnState) {
		if sessionID == h.child.SessionID {
			once.Do(func() { captured <- ts.opts.UserMessage })
		}
	}
	t.Cleanup(func() { turnRegisteredTestHook = oldHook })
	h.al.drainSteerQueue(fillers[0], 0)

	select {
	case message := <-captured:
		firstAt, secondAt := strings.Index(message, first), strings.Index(message, second)
		if firstAt < 0 || secondAt <= firstAt || strings.Count(message, first) != 1 || strings.Count(message, second) != 1 {
			t.Fatalf("promoted turn message = %q; want both wakes exactly once, in arrival order", message)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the queued session to be promoted")
	}
}
