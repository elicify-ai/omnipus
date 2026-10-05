package agent

// Specification oracles:
// ADR-088 D6: the keeper reaches a quiet goal and re-arms after real work.
// ADR-084 D12/D13 + judge-active-reviewer-spec FR-097: remind, never judge
// silence; adjudicate the real goal_claim only after the worker answers.
// ADR-091 D3/D6/D12, AC-6/AC-14: a goal-bearing child completes and hands
// back exactly once, or fails visibly. No parent polling is needed.
// ADR-086 D8/D9: met goal, verdict and criterion statuses remain on disk.
// RED only: GREEN and mutation proof are deferred to independent CHECK.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestUATD1_GoalHelperKeeperReachesChildAndHandsBackOnce(t *testing.T) {
	f := newUATD1Fixture(t, false, false)
	child := f.launch(t)
	g := f.finishUnclaimedAnswer(t, child)
	f.driveKeeper(t, child, g)
	f.assertMetHandback(t, child, 3) // answer, reminder's claim, final answer
}

func TestUATD1_GoalHelperKeeperProviderFailureReportsOnce(t *testing.T) {
	f := newUATD1Fixture(t, false, true)
	child := f.launch(t)
	g := f.finishUnclaimedAnswer(t, child)
	f.driveKeeper(t, child, g)
	f.awaitParentAndDrain(t)
	got := uatD1ReadLifecycle(t, f.al, child.SessionID)
	if got.State != session.LifecycleFailed || !got.Terminal() || got.Generation != child.Generation {
		t.Fatalf("D1 user expectation: the helper fails visibly, never stays running; actual state=%q terminal=%v generation=%d, want failed in generation %d", got.State, got.Terminal(), got.Generation, child.Generation)
	}
	msgs := f.inboxMessages(t)
	if len(msgs) != 1 {
		t.Fatalf("D1 parent failure messages=%d, want exactly one fatal error and no handback", len(msgs))
	}
	class, err := session.ClassifySessionMessage(msgs[0])
	if err != nil || class.Kind != "error" {
		t.Fatalf("D1 failure envelope=%+v error=%v, want error", class, err)
	}
	failure, err := msgs[0].AsSessionMessageError()
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	wantText := "failed: " + uatD1ProviderFailure
	if err != nil || !failure.Fatal || failure.MessageId != wantID || failure.Text != wantText || failure.SessionId != child.SessionID || string(failure.Direction) != "child_to_parent" || (failure.ParentSessionId != nil && *failure.ParentSessionId != f.parentID) {
		t.Fatalf("D1 visible failure=%+v error=%v, want exact fatal %q id=%q from child to its direct parent", failure, err, wantText, wantID)
	}
	if len(f.worker.Requests()) != 2 || f.judge.callCount() != 0 {
		t.Fatalf("D1 failure provider calls=%d Judge calls=%d, want answer+one failed continuation, zero Judge calls", len(f.worker.Requests()), f.judge.callCount())
	}
	f.assertParentReceivedOnce(t, wantText)
	f.assertNoRunningChild(t)
}

// Instrument control, not a RED claim. If this passes on the base, the
// already-claimed path is fixed there; do not bend it into a failure.
func TestUATD1_ClaimedGoalHelperCompletesWithoutKeeper(t *testing.T) {
	f := newUATD1Fixture(t, true, false)
	child := f.launch(t)
	f.worker.openFirst()
	f.assertMetHandback(t, child, 2) // claim, final answer; no keeper tick
}

func (f *uatD1Fixture) finishUnclaimedAnswer(t *testing.T, child *session.LifecycleRecord) *goal.Goal {
	t.Helper()
	f.worker.openFirst()
	joinGoalFixtureRuns(t, f.al) // real admitted goroutine, including disposal
	got := uatD1ReadLifecycle(t, f.al, child.SessionID)
	if got.State != session.LifecycleRunning || got.FinalDelivery != nil || f.al.getActiveTurnState(child.SessionID) != nil {
		t.Fatalf("SETUP unclaimed answer: state=%q outbox=%+v, want goal awaiting claim with its actual turn fully ended", got.State, got.FinalDelivery)
	}
	g := uatD1ReadGoal(t, child.GoalRef)
	if g.State != generated.GoalStateActive || g.LatestClaim != nil || g.LatestVerdict != nil || g.Round != 0 || g.ZeroOutputPushes != 0 || g.LastActivityAt.IsZero() || g.Prompt != uatD1Task {
		t.Fatalf("SETUP expected active, unclaimed goal before keeper: %+v", g)
	}
	if len(g.Criteria) != 1 || g.Criteria[0].Text != uatD1Criterion || len(g.DoD) != 1 || g.DoD[0].Text != uatD1DoD {
		t.Fatalf("SETUP explicit delegation criteria/DoD did not survive real launch: %+v/%+v", g.Criteria, g.DoD)
	}
	store := f.al.ResolveSessionStore(child.SessionID)
	if store == nil {
		t.Fatal("SETUP real child transcript store missing")
	}
	entries, err := store.ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("SETUP read real child transcript: %v", err)
	}
	answers := 0
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == uatD1Answer {
			answers++
		}
	}
	if answers != 1 || len(f.worker.Requests()) != 1 || f.judge.callCount() != 0 || len(f.parent.Requests()) != 0 || len(f.inboxMessages(t)) != 0 {
		t.Fatalf("SETUP boundary proof: persisted answers=%d worker requests=%d Judge calls=%d parent requests=%d, want 1/1/0/0 and empty parent inbox", answers, len(f.worker.Requests()), f.judge.callCount(), len(f.parent.Requests()))
	}
	t.Logf("D1 real helper answered and its whole turn ended: child=%s goal=%s state=%s parent_messages=0 provider_calls=1", child.SessionID, child.GoalRef, got.State)
	return g
}

func (f *uatD1Fixture) driveKeeper(t *testing.T, child *session.LifecycleRecord, g *goal.Goal) {
	t.Helper()
	// Caller-supplied production tick clock, not a forged activity timestamp
	// or a global timing hook. Check min-1 and min at the ADR's 60s boundary.
	f.al.goalQuietWindowSettle(g.LastActivityAt.Add(uatD1QuietWindow - time.Nanosecond))
	before := uatD1ReadGoal(t, child.GoalRef)
	if len(f.worker.Requests()) != 1 || before.ZeroOutputPushes != 0 || before.Round != 0 || f.judge.callCount() != 0 {
		t.Fatalf("keeper acted before the quiet-window boundary: worker=%d pushes=%d rounds=%d Judge=%d, want 1/0/0/0", len(f.worker.Requests()), before.ZeroOutputPushes, before.Round, f.judge.callCount())
	}
	f.al.goalQuietWindowSettle(g.LastActivityAt.Add(uatD1QuietWindow))
	select {
	case <-f.worker.reminderEntered:
	case <-time.After(uatD1Watchdog):
		actualGoal := uatD1ReadGoal(t, child.GoalRef)
		actualChild := uatD1ReadLifecycle(t, f.al, child.SessionID)
		t.Fatalf("D1 user expectation: the finished goal-bearing helper must receive its keeper reminder and then hand back once or fail visibly; actual child_state=%q parent_messages=%d worker_provider_calls=%d keeper_pushes=%d goal_reason=%q (no child continuation reached the provider)", actualChild.State, len(f.inboxMessages(t)), len(f.worker.Requests()), actualGoal.ZeroOutputPushes, actualGoal.LatestReason)
	}
	requests := f.worker.Requests()
	if len(requests) < 2 || !uatD1RequestContains(requests[1], "Continue working toward the goal: "+uatD1Task) || !uatD1RequestContains(requests[1], "goal_claim") {
		t.Fatalf("D1 keeper reminder did not reach the actual child's assembled model input: %+v", requests)
	}
}

func (f *uatD1Fixture) awaitParentAndDrain(t *testing.T) {
	t.Helper()
	select {
	case <-f.parent.entered:
	case <-time.After(uatD1Watchdog):
		t.Fatalf("D1 parent never consumed a real completion/failure wake; actual requests=%d", len(f.parent.Requests()))
	}
	joinGoalFixtureRuns(t, f.al)
	ctx, cancel := context.WithTimeout(context.Background(), uatD1Watchdog)
	defer cancel()
	if !f.al.WaitForActiveRequestsContext(ctx) {
		t.Fatal("real parent/provider/system-wake requests did not join after their outcome")
	}
}

func (f *uatD1Fixture) inboxMessages(t *testing.T) []generated.SessionMessage {
	t.Helper()
	// Entries includes acknowledged events; Drain alone can hide a duplicate
	// already consumed by the real parent. Read back through a fresh store.
	entries, err := session.NewMessageInboxStore(filepath.Join(f.home, "session_messages")).Entries(f.parentID)
	if err != nil {
		t.Fatalf("read real persisted parent inbox: %v", err)
	}
	var msgs []generated.SessionMessage
	for _, entry := range entries {
		switch entry.Kind {
		case session.InboxEntryMessage:
			if entry.Message == nil {
				t.Fatal("persisted inbox message has no envelope")
			}
			msgs = append(msgs, *entry.Message)
		case session.InboxEntryAck:
		default:
			t.Fatalf("unexpected persisted inbox entry kind=%q", entry.Kind)
		}
	}
	return msgs
}

func uatD1RequestContains(messages []providers.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, text) {
			return true
		}
	}
	return false
}

func (f *uatD1Fixture) assertParentReceivedOnce(t *testing.T, expected string) {
	t.Helper()
	requests := f.parent.Requests()
	if len(requests) != 1 || !uatD1RequestContains(requests[0], expected) {
		t.Fatalf("D1 expected one parent wake containing %q; actual parent model inputs=%+v", expected, requests)
	}
}

func (f *uatD1Fixture) assertNoRunningChild(t *testing.T) {
	t.Helper()
	blocked, err := f.al.hasRunningOrQueuedDescendant(f.parentID)
	if err != nil || blocked {
		t.Fatalf("D1 parent remains blocked by helper: blocked=%v error=%v, want false,nil", blocked, err)
	}
}
