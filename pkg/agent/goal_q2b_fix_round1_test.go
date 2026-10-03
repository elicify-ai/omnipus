package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Without a lifecycle store a child cannot launch; an eligible met claim
// still reaches its original adjudication path.
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
