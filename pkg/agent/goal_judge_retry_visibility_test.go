// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_judge_retry_visibility_test.go pins two UAT E-14 behaviours of a Judge
// adjudicating a chat goal in the BACKGROUND (JUDGE-FR-098's deferred
// dispatch — the worker's turn has already ended):
//
//   - a transient Judge failure is visible on the goal pill instead of a
//     `judging` card frozen for the whole retry cycle (E-14 run 2: 8 min 10 s
//     on `judging` across two timed-out Judge turns and 180 s of backoff);
//   - a cancel that claims the Judge's turn ends the adjudication — FR-082: "no
//     verdict is produced from a cancelled turn. A cancelled adjudication is
//     discarded whole" — rather than retrying it.
package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// goalClaimAwaitingJudge is a chat goal whose worker has claimed completion
// and whose deferred adjudication is ready to dispatch.
type goalClaimAwaitingJudge struct {
	al         *AgentLoop
	opts       processOptions
	agent      *AgentInstance
	store      *session.UnifiedStore
	sid        string
	goalID     string
	work       *goalDeferredAdjudicationWork
	events     *eventCollector
	stopEvents func()
}

// goalEventRecorderBuffer is sized for a whole Judge turn's event stream. The
// bus drops events for a full subscriber rather than blocking, and the shared
// newEventCollector's 16-slot buffer can lose goal_status frames under a
// verifier turn's burst of turn/LLM events.
const goalEventRecorderBuffer = 4096

// recordAgentEvents subscribes before anything under test runs. The returned
// stop unsubscribes and waits for the reader to drain, so every event emitted
// before it is called is in the collector afterwards.
func recordAgentEvents(t *testing.T, al *AgentLoop) (*eventCollector, func()) {
	t.Helper()
	c := &eventCollector{}
	sub := al.SubscribeEvents(goalEventRecorderBuffer)
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for evt := range sub.C {
			c.mu.Lock()
			c.events = append(c.events, evt)
			c.mu.Unlock()
		}
	}()
	stop := sync.OnceFunc(func() {
		al.UnsubscribeEvents(sub.ID)
		<-drained
	})
	t.Cleanup(stop)
	return c, stop
}

func setUpGoalClaimAwaitingJudge(t *testing.T, judge *fakeJudgeProvider) *goalClaimAwaitingJudge {
	t.Helper()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	// The verifier registry `/goal clear` fans out to lives on the PlanEngine.
	al.SetPlanEngine(NewPlanEngine(al, plan.New(t.TempDir()), nil, nil))

	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	events, stopEvents := recordAgentEvents(t, al)

	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	// Empty ladder → the single "goal-condition" criterion the canned verdict answers.
	clearGoalRecordCriteria(t, sid)
	goalID := goalRecordForSession(t, sid).GoalID

	judgeInst.Provider = judge

	result := &turnResult{finalContent: "[goal:evidence] all tests green\nGOAL_STATUS: met"}
	al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, result)
	if result.goalDeferredAdjudication == nil {
		t.Fatal("setup: a met+evidence claim must record deferred adjudication work")
	}
	return &goalClaimAwaitingJudge{
		al: al, opts: opts, agent: agentInst, store: store, sid: sid, goalID: goalID,
		work: result.goalDeferredAdjudication, events: events, stopEvents: stopEvents,
	}
}

// goalPillStates returns this goal's goal_status states in emission order. It
// stops event recording first, so call it only once the code under test has
// finished emitting.
func (g *goalClaimAwaitingJudge) goalPillStates() (states, reasons []string) {
	g.stopEvents()
	for _, p := range goalStatusPayloadsFor(g.events, g.sid) {
		if p.GoalID != g.goalID {
			continue
		}
		states = append(states, p.State)
		reasons = append(reasons, p.LatestReason)
	}
	return states, reasons
}

// containsInOrder reports whether want occurs in got as a subsequence.
func containsInOrder(got, want []string) bool {
	i := 0
	for _, s := range got {
		if i < len(want) && s == want[i] {
			i++
		}
	}
	return i == len(want)
}

// TestGoalJudgeRetry_ShowsRetryOnGoalPill: one transient Judge failure, then a
// met verdict. The pill must show the wait and the retry, not hold `judging`.
func TestGoalJudgeRetry_ShowsRetryOnGoalPill(t *testing.T) {
	origSleep, origBackoff := judgeSleepFn, judgeRetryBackoff
	t.Cleanup(func() { judgeSleepFn, judgeRetryBackoff = origSleep, origBackoff })
	judgeRetryBackoff = []time.Duration{60 * time.Second, 120 * time.Second, 300 * time.Second}
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	judge := &fakeJudgeProvider{chatFn: func(n int) (*providers.LLMResponse, error) {
		if n == 1 {
			return nil, errors.New("simulated transient provider failure: upstream 503")
		}
		return &providers.LLMResponse{Content: cannedJudgeVerdictJSON(true, "tests pass")}, nil
	}}
	g := setUpGoalClaimAwaitingJudge(t, judge)

	g.al.dispatchDeferredGoalAdjudication(g.work)

	states, reasons := g.goalPillStates()
	want := []string{goalPillJudging, goalPillJudgeUnavailable, goalPillJudging, goalPillDone}
	if !containsInOrder(states, want) {
		t.Fatalf("goal pill states = %v; want the subsequence %v — the retry must be visible, then the goal ends done", states, want)
	}
	var waitReason string
	for i, s := range states {
		if s == goalPillJudgeUnavailable {
			waitReason = reasons[i]
			break
		}
	}
	if !strings.Contains(waitReason, "60 s") || !strings.Contains(waitReason, "try 2") {
		t.Errorf("retry notice %q must tell the user the wait (60 s) and which try comes next (try 2)", waitReason)
	}
	if strings.Contains(waitReason, "503") || strings.Contains(waitReason, "simulated") {
		t.Errorf("retry notice %q leaks the raw provider error (ADR-051 §RD5 CRIT-001)", waitReason)
	}
	if n := judge.callCount(); n != 2 {
		t.Errorf("the Judge's provider was called %d times; want 2 (one failure, one retry)", n)
	}
}

// TestVerifierTurnCancel_EndsAdjudicationWithoutRetry: a cancel that claims the
// Judge's turn must end the adjudication even when nothing releases its
// verifier-registry entry (a plan Stop's session fan-out cancels sessions; it
// does not unregister the goal/task unit). The outer ctx is deliberately left
// alive, so the only way this adjudication can return is by ending itself
// rather than dispatching another Judge turn.
func TestVerifierTurnCancel_EndsAdjudicationWithoutRetry(t *testing.T) {
	const taskID = "t-e14-cancel-no-retry"
	unitID := verifierUnitForTask(taskID)

	al, resultCh, blockTool, _, registry := setUpBlockedVerifierTurn(t, taskID)

	sessions := registry.SessionsFor(unitID)
	if len(sessions) != 1 {
		t.Fatalf("exactly one verifier session must be registered for %q, got %v", unitID, sessions)
	}
	fired, _, err := al.RequestCancelForSession(context.Background(), sessions[0], "tester", "test")
	if err != nil || !fired {
		t.Fatalf("RequestCancelForSession(%q): fired=%v err=%v", sessions[0], fired, err)
	}
	select {
	case <-blockTool.ctxErr:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancel never reached the Judge's in-flight tool call")
	}

	select {
	case res := <-resultCh:
		if !res.Unavailable {
			t.Errorf("a cancelled adjudication must be unavailable (no round consumed), got %+v", res)
		}
		if res.Verdict != nil {
			t.Errorf("no verdict may be produced from a cancelled turn, got %+v", res.Verdict)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the adjudication did not end after its Judge turn was cancelled — it dispatched another " +
			"Judge turn instead of discarding the adjudication whole (FR-082)")
	}
}
