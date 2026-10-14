package agent

// PR #1311 / RED dispatch G4 (R2-2 / SFH R2-4). The OUTER Judge round
// expires before the inner 600-second verifier limit. A real provider-boundary
// double keeps working until its context ends; real wrappers emit the exits
// and deliver the conclusions. testing/synctest controls only the clock, so
// the real ten-minute plan constant is exercised without a ten-minute sleep
// or any production edit. CHECK: remove an outer cancellation cause; lose a
// result-bearing conclusion; misclassify provider request timeout or Stop.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
)

type r3JudgeCall struct {
	started, ended time.Time
	parentErr      error
	cause          error
}

type r3ContextEndingJudge struct {
	mu             sync.Mutex
	calls          []r3JudgeCall
	entered        chan struct{}
	once           sync.Once
	requestTimeout time.Duration
}

func newR3ContextEndingJudge() *r3ContextEndingJudge {
	return &r3ContextEndingJudge{entered: make(chan struct{})}
}

func (p *r3ContextEndingJudge) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]any) (*providers.LLMResponse, error) {
	started := time.Now()
	p.once.Do(func() { close(p.entered) })
	requestCtx := ctx
	if p.requestTimeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, p.requestTimeout)
		defer cancel()
	}
	<-requestCtx.Done()
	p.mu.Lock()
	p.calls = append(p.calls, r3JudgeCall{started: started, ended: time.Now(),
		parentErr: ctx.Err(), cause: context.Cause(ctx)})
	p.mu.Unlock()
	return nil, requestCtx.Err()
}

func (*r3ContextEndingJudge) GetDefaultModel() string { return "r3-context-ending-judge" }

// The real goal_claim watermark uses strict timestamp ordering. Advance the
// FAKE clock at the provider boundary so a new worker's claim is genuinely
// newer than its run watermark even inside a frozen synctest bubble.
type r3ClaimClockProvider struct{ providers.LLMProvider }

func (p r3ClaimClockProvider) Chat(ctx context.Context, messages []providers.Message,
	tools []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	time.Sleep(time.Nanosecond)
	return p.LLMProvider.Chat(ctx, messages, tools, model, options)
}

func (p *r3ContextEndingJudge) snapshot() []r3JudgeCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]r3JudgeCall(nil), p.calls...)
}

func r3JudgeWireExitCodes(c *eventCollector) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, event := range c.events {
		p, ok := event.Payload.(ErrorPayload)
		if ok && event.Meta.AgentID == string(coreagent.IDJudge) && p.Stage == "llm" {
			out = append(out, WireLLMError(p).Code)
		}
	}
	return out
}

func r3AssertJudgeExitCodes(t *testing.T, events *eventCollector, want LLMErrorCode, why string) {
	t.Helper()
	codes := r3JudgeWireExitCodes(events)
	if len(codes) == 0 {
		t.Fatalf("%s: the real verifier emitted no classified terminal error; cannot prove classification", why)
	}
	for _, code := range codes {
		if code != string(want) {
			t.Errorf("%s: real verifier exit=%q, want %q (not provider timeout)", why, code, want)
		}
	}
}

func r3AssertResultBearingConclusion(t *testing.T, texts []string, result, why string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(text, "Not verified") && strings.Contains(text, result) {
			return
		}
	}
	t.Errorf("%s: no result-bearing Not verified conclusion was delivered; texts=%q want result=%q", why, texts, result)
}

func r3GoalOuterLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		noJudgeWait(t)
		oldTimeout, oldRegistry := goalJudgeRoundTimeout, currentVerifierSessionRegistry()
		goalJudgeRoundTimeout = time.Second // outer is strictly below the legal inner 600-second bound
		t.Cleanup(func() { goalJudgeRoundTimeout = oldTimeout; SetVerifierSessionRegistry(oldRegistry) })
		g := setUpGoalClaimAwaitingJudge(t, &fakeJudgeProvider{})
		p := newR3ContextEndingJudge()
		judgeInst, _ := g.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
		judgeInst.Provider = p
		g.al.GetConfig().Judge.TimeoutSec = 600

		g.al.dispatchDeferredGoalAdjudication(g.work)
		g.stopEvents()
		if n := len(p.snapshot()); n != goalJudgeRedriveAttempts {
			t.Fatalf("SETUP real goal wrapper ended %d provider calls, want %d outer rounds", n, goalJudgeRedriveAttempts)
		}
		r3AssertJudgeExitCodes(t, g.events, CodeJudgeTimeLimit, "G4 goal outer Judge budget")
		entries, err := g.store.ReadTranscript(g.sid)
		if err != nil {
			t.Fatalf("ReadTranscript(goal): %v", err)
		}
		var texts []string
		for _, entry := range entries {
			texts = append(texts, entry.Content)
		}
		r3AssertResultBearingConclusion(t, texts, "all tests green", "G4 goal outer-limit conclusion")
		if got := mustGoalRecord(t, g.goalID); got.Round != 0 || got.State != "active" {
			t.Errorf("G4 unavailable goal spent a round or ended: state=%q round=%d", got.State, got.Round)
		}
	})
}

func r3TaskOuterLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		noJudgeWait(t)
		oldTimeout, oldRegistry := goalJudgeRoundTimeout, currentVerifierSessionRegistry()
		goalJudgeRoundTimeout = time.Second
		t.Cleanup(func() { goalJudgeRoundTimeout = oldTimeout; SetVerifierSessionRegistry(oldRegistry) })
		const evidence = "RESULT-R3-TASK: report.md contains the finished work"
		al, judgeInst := newGoalLoopTestLoop(t, r3ClaimClockProvider{newClaimingWorker(turnClaimMet(evidence))}, func(cfg *config.Config) {
			cfg.Judge.TimeoutSec = 600
		})
		p := newR3ContextEndingJudge()
		judgeInst.Provider = p
		events, stop := recordAgentEvents(t, al)
		tk := newRunLoopTask(t, al, nil)
		if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
			t.Fatalf("ExecuteTask: %v", err)
		}
		al.taskExecutor.wg.Wait() // join the COMPLETE run, not just its status write
		stop()
		final, err := al.taskStore.Get(tk.ID)
		if err != nil || final.Status != task.StatusFailed || final.AttemptCount != 0 {
			t.Fatalf("G4 unverified task = %+v err=%v, want failed with no attempt used", final, err)
		}
		if n := len(p.snapshot()); n != judgeUnavailableRetryBound {
			t.Fatalf("SETUP real task wrapper ended %d provider calls, want %d outer rounds", n, judgeUnavailableRetryBound)
		}
		r3AssertJudgeExitCodes(t, events, CodeJudgeTimeLimit, "G4 task outer Judge budget")
		r3AssertResultBearingConclusion(t, []string{final.Result}, evidence, "G4 task outer-limit conclusion")
	})
}

func r3ProviderTimeoutControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		noJudgeWait(t)
		p := newR3ContextEndingJudge()
		p.requestTimeout = time.Second // only a per-request timer, not an outer or inner Judge expiry
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) { cfg.Judge.TimeoutSec = 600 })
		judgeInst.Provider = p
		events, stop := recordAgentEvents(t, al)
		result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{Scope: task.VerdictScopeTask,
			TaskID: "r3-request-timeout", AssigneeAgentID: "native-agent", Attempt: 1,
			Criteria: []task.AcceptanceCriterion{proseCriterion("r3-c1", "the report exists")}, ClaimText: "wrote report.md"})
		stop()
		if !result.Unavailable || result.Verdict != nil {
			t.Fatalf("CONTROL provider request timeout produced a verdict: %+v", result)
		}
		if len(p.snapshot()) == 0 {
			t.Fatal("CONTROL provider was never called")
		}
		for _, call := range p.snapshot() {
			if call.parentErr != nil || call.cause != nil {
				t.Errorf("CONTROL the Judge context ended before the provider request: %+v", call)
			}
		}
		r3AssertJudgeExitCodes(t, events, CodeTurnTimedOut, "G4 provider-request-timeout control")
	})
}

func r3ExplicitStopControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		noJudgeWait(t)
		oldRegistry := currentVerifierSessionRegistry()
		t.Cleanup(func() { SetVerifierSessionRegistry(oldRegistry) })
		p := newR3ContextEndingJudge()
		al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) { cfg.Judge.TimeoutSec = 600 })
		judgeInst.Provider = p
		pe := NewPlanEngine(al, plan.New(t.TempDir()), al.taskStore, al.taskExecutor)
		al.SetPlanEngine(pe)
		events, stop := recordAgentEvents(t, al)
		resultCh := make(chan JudgeCriteriaResult, 1)
		go func() {
			resultCh <- al.JudgeCriteria(context.Background(), JudgeCriteriaInput{Scope: task.VerdictScopeTask,
				TaskID: "r3-explicit-stop", AssigneeAgentID: "native-agent", Attempt: 1,
				Criteria: []task.AcceptanceCriterion{proseCriterion("r3-c1", "the report exists")}, ClaimText: "wrote report.md"})
		}()
		<-p.entered
		sessions := pe.registry().SessionsFor(verifierUnitForTask("r3-explicit-stop"))
		if len(sessions) != 1 {
			t.Fatalf("SETUP active verifier sessions=%v, want one", sessions)
		}
		fired, _, err := al.RequestCancelForSession(context.Background(), sessions[0], "tester", "webchat")
		if err != nil || !fired {
			t.Fatalf("CONTROL explicit Stop fired=%v err=%v", fired, err)
		}
		result := <-resultCh
		stop()
		if !result.Unavailable || result.Verdict != nil || result.Reason != VerifierAdjudicationCancelledReason {
			t.Errorf("CONTROL explicit Stop result=%+v, want unavailable/cancelled with no verdict", result)
		}
		r3AssertJudgeExitCodes(t, events, CodeTurnCanceled, "G4 explicit-Stop control")
	})
}

func TestR3OuterJudgeRound_GoalAndTaskUseJudgeTimeLimitWithResult(t *testing.T) {
	t.Run("goal_outer_budget", r3GoalOuterLimit)
	t.Run("task_outer_budget", r3TaskOuterLimit)
	t.Run("provider_request_timeout_is_still_turn_timed_out", r3ProviderTimeoutControl)
	t.Run("explicit_stop_is_still_cancelled", r3ExplicitStopControl)
}

func TestR3OuterJudgeRound_PlanUsesJudgeTimeLimitAndDeliversResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		noJudgeWait(t)
		h := newR3PlanHarness(t)
		p := newR3ContextEndingJudge()
		judgeInst, _ := h.al.GetRegistry().GetAgent(string(coreagent.IDJudge))
		judgeInst.Provider = p
		// Spend one virtual second gathering inputs. The unchanged production
		// outer round is then STRICTLY earlier than the inner 600-second timer.
		h.pe.judgeRoundReadFault = func(stage string) error {
			if stage == "plan" {
				time.Sleep(time.Second)
			}
			return nil
		}
		h.pe.processPlan(context.Background(), h.p.ID)
		h.pe.judgeWG.Wait()
		h.pe.wakeWG.Wait()
		h.stop()
		calls := p.snapshot()
		if len(calls) != 1 || calls[0].ended.Sub(calls[0].started) != planJudgeRoundTimeout-time.Second {
			t.Fatalf("SETUP outer plan clock did not win before inner verifier clock: calls=%+v", calls)
		}
		r3AssertJudgeExitCodes(t, h.events, CodeJudgeTimeLimit, "G4 plan outer Judge budget")
		after, err := h.plans.Get(h.p.ID)
		if err != nil || after.JudgeRounds != 0 || after.State != plan.StateRunning {
			t.Fatalf("G4 unverified plan = %+v err=%v, want running/no round debit", after, err)
		}
		texts := []string{after.HandoverText}
		entries, err := h.al.GetSessionStore().ReadTranscript(h.owner)
		if err != nil {
			t.Fatalf("read owner conclusion: %v", err)
		}
		for _, entry := range entries {
			texts = append(texts, entry.Content)
		}
		for _, msg := range drainOutbound(h.al.bus) {
			if msg.SessionID == h.owner {
				texts = append(texts, msg.Content)
			}
		}
		r3AssertResultBearingConclusion(t, texts, h.member.Result, "G4 plan outer-limit conclusion")
		h.assertRoundReleased(t)
	})
}
