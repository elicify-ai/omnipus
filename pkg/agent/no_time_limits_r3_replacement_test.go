package agent

// PR #1311 / RED dispatch G2 (CR stale conclusion / SFH R2-2).
// Oracle: A's final unavailable conclusion is historical data, not permission
// to change a replacement goal B. The only test seam gates the filesystem
// append and then delegates to the original REAL append; it never fabricates
// a saved conclusion. CHECK: remove the goal-identity fence; drop the history;
// emit a stale judge_unavailable frame addressed to B.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestR3JudgeConclusion_ReplacementGoalIsUntouchedAndOldConclusionIsHistorical(t *testing.T) {
	noJudgeWait(t)
	oldRegistry := currentVerifierSessionRegistry()
	t.Cleanup(func() { SetVerifierSessionRegistry(oldRegistry) })
	judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)
	// The helper's real after-turn claim is [goal:evidence] all tests green;
	// do not replace the deferred payload with a hand-made identity fixture.
	const evidenceA = "all tests green"

	originalAppend := appendGoalConclusionFn
	inAppend, releaseAppend, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(releaseAppend) })
	appendGoalConclusionFn = func(al *AgentLoop, store *session.UnifiedStore, sid, aid, text string) error {
		if sid == g.sid && strings.Contains(text, "Not verified") {
			close(inAppend)
			<-releaseAppend
		}
		return originalAppend(al, store, sid, aid, text)
	}
	t.Cleanup(func() { appendGoalConclusionFn = originalAppend })
	go func() { defer close(done); g.al.dispatchDeferredGoalAdjudication(g.work) }()
	// Cleanup unblocks and joins before resetting the seam or deleting stores,
	// even if a setup assertion fails while the append is paused.
	t.Cleanup(func() { release(); waitClosed(t, done, "the gated adjudication cleanup") })
	waitClosed(t, inAppend, "A's final conclusion append")
	if n := judge.callCount(); n != goalJudgeRedriveAttempts {
		t.Fatalf("SETUP A's provider calls=%d, want final unavailable attempt %d", n, goalJudgeRedriveAttempts)
	}

	g.clearGoal(t)
	optsB := g.opts
	matched, _, _ := g.al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal produce a replacement report B", UserInitiated: true}, g.agent, &optsB)
	if !matched {
		t.Fatal("SETUP replacement /goal command was not matched")
	}
	activatePendingGoal(t, g.al, g.agent, &optsB)
	beforeB := goalRecordForSession(t, g.sid)
	if beforeB.GoalID == g.goalID {
		t.Fatal("SETUP clearing A and starting B did not mint a different goal")
	}
	release()
	waitClosed(t, done, "A's late conclusion to finish")
	g.stopEvents()

	afterB := mustGoalRecord(t, beforeB.GoalID)
	if afterB.LatestReason != beforeB.LatestReason {
		t.Errorf("G2 A's late conclusion overwrote B.LatestReason: got %q, want unchanged %q",
			afterB.LatestReason, beforeB.LatestReason)
	}
	if afterB.State != beforeB.State || afterB.Round != beforeB.Round || afterB.LatestVerdict != nil ||
		afterB.Prompt != beforeB.Prompt || afterB.ActiveSessionID != beforeB.ActiveSessionID {
		t.Errorf("G2 replacement B's status/identity changed: before=%+v after=%+v", beforeB, afterB)
	}
	for _, frame := range goalStatusPayloadsFor(g.events, g.sid) {
		if frame.GoalID == beforeB.GoalID && frame.State == goalPillJudgeUnavailable {
			t.Errorf("G2 B received A's judge_unavailable status frame: %+v", frame)
		}
	}
	if old := mustGoalRecord(t, g.goalID); old.State != generated.GoalStateCleared {
		t.Errorf("G2 A's clear was overwritten: state=%q, want cleared", old.State)
	}
	entries, err := g.store.ReadTranscript(g.sid)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	historyKept := strings.Contains(mustGoalRecord(t, g.goalID).LatestReason, "Not verified") &&
		strings.Contains(mustGoalRecord(t, g.goalID).LatestReason, evidenceA)
	for _, entry := range entries {
		if strings.Contains(entry.Content, "Not verified") && strings.Contains(entry.Content, evidenceA) {
			historyKept = true
		}
	}
	if !historyKept {
		t.Error("G2 A's result-bearing Not verified conclusion was dropped instead of retained historically")
	}
}
