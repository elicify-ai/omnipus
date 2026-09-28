package agent

// RED tests for founder ruling Q2 B: a steered child that claims its goal is
// met while descendants still work must stay active and silent until its
// whole subtree is quiet. The parent then receives exactly one message: the
// child's final handback.
//
// Oracle source: coordination/logs/fix890-opus/arch-q2-design.md, Invariants
// and Tests required. Expected values below are derived from that design,
// never from the release implementation. These tests intentionally fail on
// origin/release/v0.1.1; GREEN and mutation proof belong to later stages.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const q2bReevaluationPrompt = "All delegated work has finished; review the handbacks and claim completion again if appropriate."

type q2bHarness struct {
	t          *testing.T
	al         *AgentLoop
	judgeInst  *AgentInstance
	judge      *fakeJudgeProvider
	lifecycle  *session.LifecycleStore
	inbox      *session.MessageInboxStore
	dispatches *goalDispatchRecorder
	parentID   string
	child      *session.LifecycleRecord
	childTurn  *turnState
}

func newQ2BHarness(t *testing.T, callID string) *q2bHarness {
	t.Helper()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)

	parent, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	child := launchGoalBearingChild947(t, al, parent.ID, callID)
	childTurn, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn(child): %v", err)
	}

	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: metGoalVerdict947(t, child)}, nil
	}}
	judgeInst.Provider = judge

	return &q2bHarness{
		t: t, al: al, judgeInst: judgeInst, judge: judge,
		lifecycle: lifecycle, inbox: inbox, dispatches: recordGoalDispatches(al),
		parentID: parent.ID, child: child, childTurn: childTurn,
	}
}

func (h *q2bHarness) claimMet(evidence string) *turnResult {
	h.t.Helper()
	result := &turnResult{finalContent: "[goal:evidence] " + evidence + "\nGOAL_STATUS: met"}
	h.al.checkGoalLoopAfterTurn(context.Background(), h.childTurn.agent, h.childTurn.opts, result)
	return result
}

func (h *q2bHarness) goalRecord() *goal.Goal {
	h.t.Helper()
	rec, err := resolveGoalRecordStore().Get(h.child.GoalRef)
	if err != nil {
		h.t.Fatalf("Get(goal %q): %v", h.child.GoalRef, err)
	}
	return rec
}

func (h *q2bHarness) parentMessages() []generated.SessionMessage {
	h.t.Helper()
	messages, _, _, err := h.inbox.Drain(h.parentID, h.child.SessionID, "", 32)
	if err != nil {
		h.t.Fatalf("Drain(parent): %v", err)
	}
	return messages
}

func (h *q2bHarness) parentWakeEvents() []AsyncNotifyEvent {
	h.t.Helper()
	var out []AsyncNotifyEvent
	for _, event := range h.dispatches.all() {
		if event.TranscriptSessionID == h.parentID && strings.HasPrefix(event.SourceKind, "message_parent:") {
			out = append(out, event)
		}
	}
	return out
}

func q2bLaunchDescendant(t *testing.T, h *q2bHarness, callID string, state session.LifecycleState) *session.LifecycleRecord {
	t.Helper()
	res, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: h.child.SessionID,
		TargetAgentID:     "native-agent",
		Task:              "finish delegated evidence",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	if err != nil {
		t.Fatalf("Launch(descendant): %v", err)
	}
	rec, err := h.lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(descendant): %v", err)
	}
	if state != session.LifecycleQueued {
		rec.State = state
		if err := h.lifecycle.Persist(rec); err != nil {
			t.Fatalf("Persist(descendant %s): %v", state, err)
		}
	}
	return rec
}

func q2bKeepTurnAlive(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	ts := &turnState{sessionKey: sessionID, finishedChan: make(chan struct{})}
	if !al.registerTurnIfAbsent(ts) {
		t.Fatalf("registerTurnIfAbsent(%s): already occupied", sessionID)
	}
	t.Cleanup(func() { al.activeTurnStates.CompareAndDelete(sessionID, ts) })
}

func q2bAssertDeferredClaim(t *testing.T, h *q2bHarness, evidence string, work *goalDeferredAdjudicationWork) {
	t.Helper()
	// On the release code this dispatch exposes every forbidden side effect:
	// a Judge call, verdict, terminal goal, and parent-facing goal_status.
	// On Q2 B code work is nil because the claim waits on descendants.
	h.al.dispatchDeferredGoalAdjudication(work)

	g := h.goalRecord()
	if g.LatestClaim == nil || g.LatestClaim.Status != generated.GoalLatestClaimStatusMet || g.LatestClaim.Evidence != evidence {
		t.Errorf("latest claim = %+v, want durable met claim with evidence %q", g.LatestClaim, evidence)
	}
	if g.State != generated.GoalStateActive {
		t.Errorf("goal state = %q, want active while a descendant is running or queued", g.State)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls = %d, want 0 while the subtree is not quiet", calls)
	}
	if g.LatestVerdict != nil {
		t.Errorf("latest verdict = %+v, want nil while the claim waits on descendants", g.LatestVerdict)
	}
	if messages := h.parentMessages(); len(messages) != 0 {
		t.Errorf("parent messages = %d, want 0 before the whole child subtree is done", len(messages))
	}
	if wakes := h.parentWakeEvents(); len(wakes) != 0 {
		t.Errorf("parent wake events = %d, want 0 before the whole child subtree is done", len(wakes))
	}
	child, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if child.State != session.LifecycleRunning {
		t.Errorf("child lifecycle = %q, want running while completion waits", child.State)
	}
}

func q2bCountReevaluations(events []AsyncNotifyEvent, childID string) int {
	count := 0
	for _, event := range events {
		if event.TranscriptSessionID == childID && event.Content == q2bReevaluationPrompt {
			count++
		}
	}
	return count
}

func q2bChildCount(t *testing.T, lifecycle *session.LifecycleStore, parentID string) int {
	t.Helper()
	records, err := lifecycle.List(session.LifecycleFilter{SteeringSessionID: parentID})
	if err != nil {
		t.Fatalf("List(children of %s): %v", parentID, err)
	}
	return len(records)
}

func TestGoalQ2B_MetClaimWaitsForRunningDescendant(t *testing.T) {
	h := newQ2BHarness(t, "q2b-running")
	descendant := q2bLaunchDescendant(t, h, "q2b-running-descendant", session.LifecycleRunning)
	q2bKeepTurnAlive(t, h.al, descendant.SessionID)
	blocked, err := h.al.hasRunningOrQueuedDescendant(h.child.SessionID)
	if err != nil || !blocked {
		t.Fatalf("arrange: running descendant must block (blocked=%v err=%v)", blocked, err)
	}

	const evidence = "running descendant still owns required work"
	result := h.claimMet(evidence)
	q2bAssertDeferredClaim(t, h, evidence, result.goalDeferredAdjudication)
}

func TestGoalQ2B_MetClaimWaitsForQueuedDescendant(t *testing.T) {
	h := newQ2BHarness(t, "q2b-queued")
	q2bLaunchDescendant(t, h, "q2b-queued-descendant", session.LifecycleQueued)
	blocked, err := h.al.hasRunningOrQueuedDescendant(h.child.SessionID)
	if err != nil || !blocked {
		t.Fatalf("arrange: queued descendant must block (blocked=%v err=%v)", blocked, err)
	}

	const evidence = "queued descendant still owns required work"
	result := h.claimMet(evidence)
	q2bAssertDeferredClaim(t, h, evidence, result.goalDeferredAdjudication)
}

func TestGoalQ2B_DeliveryBeforeTerminalSchedulesOneReevaluation(t *testing.T) {
	h := newQ2BHarness(t, "q2b-delivery-race")
	descendant := q2bLaunchDescendant(t, h, "q2b-delivery-race-descendant", session.LifecycleRunning)
	// Instrument control: the oracle counts exact prompt and target, so the
	// descendant handback delivered in this test cannot satisfy it.
	control := AsyncNotifyEvent{TranscriptSessionID: h.child.SessionID, Content: q2bReevaluationPrompt}
	if got := q2bCountReevaluations([]AsyncNotifyEvent{control}, h.child.SessionID); got != 1 {
		t.Fatalf("exact re-evaluation prompt control count = %d, want 1", got)
	}
	control.Content = "descendant finished"
	if got := q2bCountReevaluations([]AsyncNotifyEvent{control}, h.child.SessionID); got != 0 {
		t.Fatalf("unrelated handback control count = %d, want 0", got)
	}

	var claimResult *turnResult
	var hookOnce sync.Once
	completeStateWriteTestHook = func(sessionID string) {
		if sessionID != descendant.SessionID {
			return
		}
		hookOnce.Do(func() {
			stillRunning, err := h.lifecycle.Load(descendant.SessionID)
			if err != nil {
				t.Errorf("Load(descendant inside delivery-before-terminal window): %v", err)
				return
			}
			if stillRunning.State != session.LifecycleRunning {
				t.Errorf("descendant state inside delivery-before-terminal window = %q, want running", stillRunning.State)
			}
			claimResult = h.claimMet("descendant handback arrived before its terminal write")
		})
	}
	t.Cleanup(func() { completeStateWriteTestHook = nil })

	if err := h.al.completeSteeredTurn(context.Background(), descendant,
		turnResult{finalContent: "descendant finished"}, nil); err != nil {
		t.Fatalf("completeSteeredTurn(descendant): %v", err)
	}
	if claimResult == nil {
		t.Fatal("completion hook did not run the child claim in the delivery-before-terminal window")
	}
	if claimResult.goalDeferredAdjudication != nil {
		t.Errorf("claim scheduled immediate adjudication while descendant still read running; want pending re-evaluation")
	}
	finished, err := h.lifecycle.Load(descendant.SessionID)
	if err != nil {
		t.Fatalf("Load(descendant after completion): %v", err)
	}
	if finished.State != session.LifecycleCompleted {
		t.Errorf("descendant state after terminal write = %q, want completed", finished.State)
	}
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 1 {
		t.Errorf("post-terminal re-evaluations = %d, want exactly 1; a zero count strands the pending claim", got)
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls before a fresh re-evaluation claim = %d, want 0", calls)
	}
}

func TestGoalQ2B_ConcurrentDescendantsReevaluateOnceThenWakeParentOnce(t *testing.T) {
	h := newQ2BHarness(t, "q2b-concurrent")
	first := q2bLaunchDescendant(t, h, "q2b-concurrent-1", session.LifecycleQueued)
	second := q2bLaunchDescendant(t, h, "q2b-concurrent-2", session.LifecycleQueued)

	initial := h.claimMet("both descendants still have required work")
	if initial.goalDeferredAdjudication != nil {
		t.Errorf("initial claim scheduled immediate adjudication, want a pending completion claim")
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, item := range []struct {
		rec    *session.LifecycleRecord
		answer string
	}{{first, "first result"}, {second, "second result"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- h.al.completeSteeredTurn(context.Background(), item.rec, turnResult{finalContent: item.answer}, nil)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("completeSteeredTurn(concurrent descendant): %v", err)
		}
	}
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 1 {
		t.Errorf("concurrent last-descendant re-evaluations = %d, want exactly 1", got)
	}

	fresh := h.claimMet("fresh claim after reviewing both descendant handbacks")
	if fresh.goalDeferredAdjudication == nil {
		t.Fatal("fresh post-handback claim did not schedule adjudication")
	}
	h.al.dispatchDeferredGoalAdjudication(fresh.goalDeferredAdjudication)
	if calls := h.judge.callCount(); calls != 1 {
		t.Errorf("Judge calls after one fresh claim = %d, want exactly 1", calls)
	}

	wakes := h.parentWakeEvents()
	if len(wakes) != 1 {
		t.Errorf("final parent wakes = %d, want exactly 1", len(wakes))
	} else if wakes[0].SourceKind != "message_parent:handback" {
		t.Errorf("only parent wake kind = %q, want message_parent:handback", wakes[0].SourceKind)
	}
	child, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if child.State != session.LifecycleCompleted {
		t.Errorf("child lifecycle after fresh met verdict = %q, want completed", child.State)
	}
}

func TestGoalQ2B_CompletionFenceRefusesLaunchAndUnmetClears(t *testing.T) {
	t.Run("waiting descendants refuses before record creation", func(t *testing.T) {
		h := newQ2BHarness(t, "q2b-fence-waiting")
		q2bLaunchDescendant(t, h, "q2b-fence-existing", session.LifecycleQueued)
		pending := h.claimMet("existing descendant remains queued")
		if pending.goalDeferredAdjudication != nil {
			t.Errorf("pending claim scheduled adjudication before the subtree was quiet")
		}

		before := q2bChildCount(t, h.lifecycle, h.child.SessionID)
		_, err := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: h.child.SessionID,
			TargetAgentID:     "native-agent",
			Task:              "must be refused behind the completion fence",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-fence-refused-waiting"},
		})
		if err == nil {
			t.Errorf("launch while completion waits on descendants returned nil error, want refusal")
		}
		if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before {
			t.Errorf("child records after refused launch = %d, want unchanged %d", after, before)
		}
	})

	t.Run("adjudicating refuses and unmet clears fence", func(t *testing.T) {
		h := newQ2BHarness(t, "q2b-fence-adjudicating")
		entered := make(chan struct{})
		release := make(chan struct{})
		var enteredOnce sync.Once
		h.judge = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
			enteredOnce.Do(func() { close(entered) })
			<-release
			return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(false, "more work required")}, nil
		}}
		h.judgeInst.Provider = h.judge

		work := h.claimMet("ready for Judge review").goalDeferredAdjudication
		if work == nil {
			t.Fatal("quiet-subtree claim did not schedule adjudication")
		}
		done := make(chan string, 1)
		oldDone := goalDeferredAdjudicationDoneFn
		goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
		t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
		go h.al.dispatchDeferredGoalAdjudication(work)
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("Judge did not enter adjudication")
		}

		before := q2bChildCount(t, h.lifecycle, h.child.SessionID)
		_, blockedErr := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: h.child.SessionID,
			TargetAgentID:     "native-agent",
			Task:              "must be refused during adjudication",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-fence-refused-adjudicating"},
		})
		if blockedErr == nil {
			t.Errorf("launch during adjudication returned nil error, want refusal")
		}
		if after := q2bChildCount(t, h.lifecycle, h.child.SessionID); after != before {
			t.Errorf("child records after adjudicating refusal = %d, want unchanged %d", after, before)
		}

		close(release)
		select {
		case got := <-done:
			if got != h.child.SessionID {
				t.Errorf("adjudication completed for %q, want %q", got, h.child.SessionID)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for unmet adjudication")
		}
		g := h.goalRecord()
		if g.State != generated.GoalStateActive || g.LatestVerdict == nil || g.LatestVerdict.Met {
			t.Fatalf("unmet adjudication left goal = state %q verdict %+v, want active/unmet", g.State, g.LatestVerdict)
		}

		beforeAllowed := q2bChildCount(t, h.lifecycle, h.child.SessionID)
		_, allowedErr := NewSteerLauncher(h.al).Launch(context.Background(), steer.LaunchRequest{
			SteeringSessionID: h.child.SessionID,
			TargetAgentID:     "native-agent",
			Task:              "allowed after unmet clears the fence",
			Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "q2b-fence-allowed-after-unmet"},
		})
		if allowedErr != nil {
			t.Errorf("launch after unmet verdict = %v, want allowed", allowedErr)
		}
		if afterAllowed := q2bChildCount(t, h.lifecycle, h.child.SessionID); afterAllowed != beforeAllowed+1 {
			t.Errorf("child records after allowed launch = %d, want %d", afterAllowed, beforeAllowed+1)
		}
	})
}

func TestGoalQ2B_CancelPendingChildClearsWithoutReevaluation(t *testing.T) {
	h := newQ2BHarness(t, "q2b-cancel-pending")
	ancestorGoalID := activateTestGoalRecord(t, h.parentID, "ancestor remains active")
	q2bLaunchDescendant(t, h, "q2b-cancel-descendant", session.LifecycleQueued)
	pending := h.claimMet("claim must wait for the queued descendant")
	if pending.goalDeferredAdjudication != nil {
		t.Errorf("pending child claim scheduled immediate adjudication")
	}
	if calls := h.judge.callCount(); calls != 0 {
		t.Errorf("Judge calls before cancellation = %d, want 0", calls)
	}

	if _, err := h.al.steerCanceller().CancelSubtree(context.Background(), h.child.SessionID,
		steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-q2b"}); err != nil {
		t.Fatalf("CancelSubtree(child): %v", err)
	}
	child, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(cancelled child): %v", err)
	}
	if child.State != session.LifecycleCancelled {
		t.Errorf("cancelled child lifecycle = %q, want cancelled", child.State)
	}
	childGoal := h.goalRecord()
	if !goal.IsTerminalState(childGoal.State) || childGoal.State == generated.GoalStateMet {
		t.Errorf("cancelled child's goal state = %q, want terminal non-met", childGoal.State)
	}
	if got := q2bCountReevaluations(h.dispatches.all(), h.child.SessionID); got != 0 {
		t.Errorf("re-evaluations after cancelling the pending child = %d, want 0", got)
	}
	ancestor, err := resolveGoalRecordStore().Get(ancestorGoalID)
	if err != nil {
		t.Fatalf("Get(ancestor goal): %v", err)
	}
	if ancestor.State != generated.GoalStateActive {
		t.Errorf("ancestor goal state = %q, want active", ancestor.State)
	}
}

func TestGoalQ2B_EndToEndOneParentWakeAndCompletedChild(t *testing.T) {
	h := newQ2BHarness(t, "q2b-e2e")
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

	result := turnResult{finalContent: "[goal:evidence] all delegated work is finished\nGOAL_STATUS: met"}
	h.al.finishSteeredGoalTurn(h.childTurn, h.child, &result, nil)
	select {
	case got := <-done:
		if got != h.child.SessionID {
			t.Errorf("adjudicated session = %q, want %q", got, h.child.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for final adjudication")
	}

	wakes := h.parentWakeEvents()
	if len(wakes) != 1 {
		t.Errorf("parent wakes for completed child = %d, want exactly 1", len(wakes))
	} else if wakes[0].SourceKind != "message_parent:handback" {
		t.Errorf("only parent wake kind = %q, want message_parent:handback", wakes[0].SourceKind)
	}
	messages := h.parentMessages()
	if len(messages) != 1 {
		t.Errorf("parent inbox messages = %d, want exactly 1 final handback", len(messages))
	} else {
		class, err := session.ClassifySessionMessage(messages[0])
		if err != nil {
			t.Errorf("ClassifySessionMessage: %v", err)
		} else if class.Kind != "handback" {
			t.Errorf("only parent inbox message kind = %q, want handback", class.Kind)
		}
	}

	child, err := h.lifecycle.Load(h.child.SessionID)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if child.State != session.LifecycleCompleted || !child.Terminal() {
		t.Errorf("child lifecycle = %q (terminal=%v), want one completed lifecycle", child.State, child.Terminal())
	}
	children, err := h.lifecycle.List(session.LifecycleFilter{SteeringSessionID: h.parentID})
	if err != nil {
		t.Fatalf("List(parent children): %v", err)
	}
	if len(children) != 1 || children[0].SessionID != h.child.SessionID {
		t.Errorf("parent child lifecycles = %v, want exactly child %q", children, h.child.SessionID)
	}
	if calls := h.judge.callCount(); calls != 1 {
		t.Errorf("Judge calls = %d, want exactly 1", calls)
	}
}
