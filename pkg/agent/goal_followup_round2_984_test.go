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

// TestGoalDelegation984_MetWithRunningDescendantWakesVerdict pins architect
// re-review F1: when the met completion tail cannot store its deterministic
// final hand-back because a descendant is still running, the suppressed goal
// verdict must stay unacknowledged and must wake the parent itself.
func TestGoalDelegation984_MetWithRunningDescendantWakesVerdict(t *testing.T) {
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
		Task:              "prove the goal while a descendant is still working",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-f1-running-descendant"},
		Goal: &steer.GoalSpec{
			Criteria: []steer.Criterion{{Text: "the work is complete"}},
			DoD:      []steer.Criterion{{Text: "the evidence is sufficient"}},
		},
	})
	if err != nil {
		t.Fatalf("Launch(goal child): %v", err)
	}
	child, err := lifecycle.Load(res.SessionID)
	if err != nil {
		t.Fatalf("Load(goal child): %v", err)
	}
	child.State = session.LifecycleRunning
	if persistErr := lifecycle.Persist(child); persistErr != nil {
		t.Fatalf("Persist(goal child running): %v", persistErr)
	}
	child = stampG5ExitedExecution(t, al, child)

	grandchild := &session.LifecycleRecord{
		SessionID:      "session_f1_running_grandchild",
		Generation:     1,
		State:          session.LifecycleRunning,
		Origin:         &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-f1-running-grandchild"},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: child.SessionID, RootSessionID: child.SteeredBy.RootSessionID},
		OwnerScopeKind: session.OwnerScopeParentSession,
		OwnerScopeID:   child.SessionID,
		WorkspaceID:    child.WorkspaceID,
		AgentID:        child.AgentID,
		ParentAgentID:  child.AgentID,
		OriginChannel:  child.OriginChannel,
		OriginChatID:   child.OriginChatID,
	}
	if persistErr := lifecycle.Persist(grandchild); persistErr != nil {
		t.Fatalf("Persist(grandchild running): %v", persistErr)
	}
	al.activeTurnStates.Store(grandchild.SessionID, &turnState{})
	t.Cleanup(func() { al.activeTurnStates.Delete(grandchild.SessionID) })
	if grandchild.SteeringSessionID() != child.SessionID {
		t.Fatalf("grandchild parent = %q, want %q", grandchild.SteeringSessionID(), child.SessionID)
	}

	g, err := resolveGoalRecordStore().Get(child.GoalRef)
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

	var mu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		mu.Lock()
		defer mu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})

	ts, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })
	al.finishSteeredGoalTurn(ts, child, &turnResult{finalContent: "[goal:evidence] verified\nGOAL_STATUS: met"}, nil)
	select {
	case got := <-done:
		if got != child.SessionID {
			t.Fatalf("adjudicated session = %q, want %q", got, child.SessionID)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delegated goal adjudication")
	}

	verdictID := goalVerdictUpwardMessageID(g.GoalID, 1)
	mu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	mu.Unlock()
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != verdictID {
		t.Fatalf("parent wakes = %v, want exactly the unacknowledged verdict %q when no final hand-back was stored", gotWakeIDs, verdictID)
	}
	assertUnackedMessageIDs(t, inbox, parentMeta.ID, child.SessionID, verdictID)
	loaded, err := lifecycle.Load(child.SessionID)
	if err != nil {
		t.Fatalf("Load(goal child after met): %v", err)
	}
	if loaded.Terminal() {
		t.Fatalf("goal child state = %q, want non-terminal while descendant %q is still running", loaded.State, grandchild.SessionID)
	}

	grandchild.State = session.LifecycleCompleted
	if persistErr := lifecycle.Persist(grandchild); persistErr != nil {
		t.Fatalf("Persist(grandchild completed): %v", persistErr)
	}
	al.activeTurnStates.Delete(grandchild.SessionID)
	if woke := al.completeSteeredTurnAfterGoal(context.Background(), child.SessionID, "verified", nil); !woke {
		t.Fatal("deferred goal-child hand-back did not wake the parent")
	}

	mu.Lock()
	gotWakeIDs = append([]string(nil), wakeIDs...)
	mu.Unlock()
	wantFinalID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if len(gotWakeIDs) != 2 || gotWakeIDs[0] != verdictID || gotWakeIDs[1] != wantFinalID {
		t.Fatalf("parent wakes after descendant completion = %v, want verdict %q then hand-back %q", gotWakeIDs, verdictID, wantFinalID)
	}
	assertUnackedMessageIDs(t, inbox, parentMeta.ID, child.SessionID, verdictID, wantFinalID)
}

// TestGoal984_CompletionTailSingleShotDuringFinishedTurnRace pins architect
// re-review F4. Both competing paths pass preflight before either commits.
// The fixture selects D2's final-first winner deterministically: "If completion/
// outbox commits first, Stop observes done/failed, is superseded, and cannot
// create a stop note." Releasing both callers together let the cancelled goal
// tail win instead, which correctly produces a stop notice, not a final.
// The stopped-first winner is covered separately by the T11 stop-fence tests.
func TestGoal984_CompletionTailSingleShotDuringFinishedTurnRace(t *testing.T) {
	al, cleanup := newSteerAL(t)
	defer cleanup()
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-1")
	child := stampG5ExitedExecution(t, al, launchRunningChild(t, al, parentID, "call-f4-single-shot"))

	ts, err := al.reconstructSteeredTurn(child, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	ts.isFinished.Store(true)
	al.activeTurnStates.Store(child.SessionID, ts)
	t.Cleanup(func() { al.activeTurnStates.Delete(child.SessionID) })

	arrived := make(chan int, 2)
	releaseDirect, releaseDeferred := make(chan struct{}), make(chan struct{})
	var directOnce, deferredOnce sync.Once
	openDirect := func() { directOnce.Do(func() { close(releaseDirect) }) }
	openDeferred := func() { deferredOnce.Do(func() { close(releaseDeferred) }) }
	var seamMu sync.Mutex
	seamCalls := 0
	completeBeforeDeliveryTestHook = func(sessionID string) {
		if sessionID != child.SessionID {
			return
		}
		seamMu.Lock()
		seamCalls++
		call := seamCalls
		seamMu.Unlock()
		arrived <- call
		if call == 1 {
			<-releaseDirect
		} else {
			<-releaseDeferred
		}
	}
	t.Cleanup(func() { openDirect(); openDeferred(); completeBeforeDeliveryTestHook = nil })

	var wakeMu sync.Mutex
	var wakeIDs []string
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		wakeMu.Lock()
		defer wakeMu.Unlock()
		wakeIDs = append(wakeIDs, fmt.Sprint(event.Metadata["steer_message_id"]))
	})
	awaitPreflight := func(want int) {
		t.Helper()
		select {
		case got := <-arrived:
			if got != want {
				t.Fatalf("completion preflight = %d, want %d", got, want)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("completion path %d did not reach the deterministic pre-delivery seam", want)
		}
	}

	directDone := make(chan error, 1)
	go func() {
		directDone <- al.completeSteeredTurn(context.Background(), child, turnResult{finalContent: "child result"}, nil)
	}()
	awaitPreflight(1)
	deferredDone := make(chan struct{})
	go func() {
		al.completeSteeredTurnIfDeferredAtGate(child.SessionID)
		close(deferredDone)
	}()
	awaitPreflight(2)
	openDirect()
	if err := <-directDone; err != nil {
		t.Fatalf("normal completion path: %v", err)
	}
	openDeferred()
	select {
	case <-deferredDone:
	case <-time.After(10 * time.Second):
		t.Fatal("deferred completion path did not return")
	}

	wakeMu.Lock()
	gotWakeIDs := append([]string(nil), wakeIDs...)
	wakeMu.Unlock()
	wantID := fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	if len(gotWakeIDs) != 1 || gotWakeIDs[0] != wantID {
		t.Fatalf("parent wakes = %v, want exactly one deterministic final wake %q", gotWakeIDs, wantID)
	}
	committed, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil || committed.State != session.LifecycleCompleted || committed.StopNote != nil {
		t.Fatalf("final-first winner state/note = %+v, error = %v, want completed without a stop note", committed, err)
	}
}
