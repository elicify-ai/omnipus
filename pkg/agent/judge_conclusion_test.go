package agent

// Issue #1306 founder ruling: the Judge keeps its own time bounds, but "the
// judge needs to conclude, we can not just end turns". These tests drive a
// Judge that never answers through each path whose bound runs out, and pin
// that every one ends in a visible "Not verified" conclusion that ALSO
// carries the worker's actual result.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestNotVerifiedConclusion_CarriesVerdictCauseAndResult(t *testing.T) {
	got := notVerifiedConclusion("judge unavailable after 3 attempts", "Claim again later.", "wrote report.md")
	for _, want := range []string{"Not verified", "judge unavailable after 3 attempts", "Claim again later.", "wrote report.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("conclusion %q is missing %q", got, want)
		}
	}
	long := strings.Repeat("é", judgeConclusionResultCap)
	capped := notVerifiedConclusion("x", "", long)
	if !strings.Contains(capped, "[truncated]") || len(capped) > judgeConclusionResultCap+200 {
		t.Errorf("an over-long result was not capped (len %d)", len(capped))
	}
	if !strings.HasPrefix(notVerifiedConclusion("x", "", ""), "Not verified") || strings.Contains(notVerifiedConclusion("x", "", ""), "work result") {
		t.Error("a conclusion with no work result must not invent a result section")
	}
}

// Steered helper with a goal: the Judge never answers. The parent must get the
// verdict AND the helper's claimed result in the same wake-eligible message.
func TestJudgeUnavailable_SteeredHelperConclusionCarriesTheWorkResult(t *testing.T) {
	origSleep, origBackoff := judgeSleepFn, judgeRetryBackoff
	t.Cleanup(func() { judgeSleepFn, judgeRetryBackoff = origSleep, origBackoff })
	judgeRetryBackoff = []time.Duration{time.Millisecond}
	judgeSleepFn = func(context.Context, time.Duration) error {
		return errors.New("test: no wait between judge attempts")
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)
	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	rec := stampG5ExitedExecution(t, al, launchGoalBearingChild947(t, al, parentMeta.ID, "call-1306-judge-conclusion"))
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}}

	ts, err := al.reconstructSteeredTurn(rec, nil)
	if err != nil {
		t.Fatalf("reconstructSteeredTurn: %v", err)
	}
	done := make(chan string, 1)
	oldDone := goalDeferredAdjudicationDoneFn
	goalDeferredAdjudicationDoneFn = func(sessionID string) { done <- sessionID }
	t.Cleanup(func() { goalDeferredAdjudicationDoneFn = oldDone })

	result := turnResult{finalContent: "[goal:evidence] RESULT-1306 shipped the parser\nGOAL_STATUS: met"}
	al.finishSteeredGoalTurn(ts, rec, &result, nil)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the delegated goal adjudication")
	}

	messages, _, _, err := inbox.Drain(parentMeta.ID, rec.SessionID, "", 10)
	if err != nil {
		t.Fatalf("Drain(parent): %v", err)
	}
	var conclusion string
	for _, msg := range messages {
		if v, aerr := msg.AsSessionMessageError(); aerr == nil && strings.Contains(v.Text, "Not verified") {
			conclusion = v.Text
		}
	}
	if conclusion == "" {
		t.Fatalf("the parent never received a \"Not verified\" conclusion (%d messages)", len(messages))
	}
	if !strings.Contains(conclusion, "RESULT-1306 shipped the parser") {
		t.Errorf("the conclusion lost the helper's work result: %q", conclusion)
	}
}

// Interactive /goal chat (no parent): the Judge never answers. The user must
// get a durable "Not verified" conclusion in the chat transcript, with the
// claimed result — not a pill that a reconnect erases.
func TestJudgeUnavailable_InteractiveGoalChatConcludesInTranscript(t *testing.T) {
	origSleep, origBackoff := judgeSleepFn, judgeRetryBackoff
	t.Cleanup(func() { judgeSleepFn, judgeRetryBackoff = origSleep, origBackoff })
	judgeRetryBackoff = []time.Duration{time.Millisecond}
	judgeSleepFn = func(context.Context, time.Duration) error {
		return errors.New("test: no wait between judge attempts")
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}}

	al.redriveGoalAdjudication(&goalDeferredAdjudicationWork{
		agentInst: agentInst, sessionID: sid, claimText: "RESULT-1306-CHAT all tests pass",
	})

	entries, err := store.ReadTranscript(sid)
	if err != nil {
		t.Fatalf("ReadTranscript: %v", err)
	}
	var conclusion string
	for _, e := range entries {
		if strings.Contains(e.Content, "Not verified") {
			conclusion = e.Content
		}
	}
	if conclusion == "" {
		t.Fatal("the interactive goal chat ended with no \"Not verified\" conclusion in its transcript")
	}
	if !strings.Contains(conclusion, "RESULT-1306-CHAT all tests pass") {
		t.Errorf("the conclusion lost the claimed work result: %q", conclusion)
	}
	if mustActiveGoalForSession(t, sid) == nil {
		t.Error("the goal must stay active after a not-verified conclusion (claim again later)")
	}
}

// Task run with a goal: the Judge never answers within its bound. The task
// ends with a Result that carries the verdict AND the worker's actual answer,
// independently of the distinct evidence summary supplied to goal_claim.
func TestJudgeUnavailable_TaskResultCarriesVerdictAndWorkResult(t *testing.T) {
	origTimeout, origSleep := goalJudgeRoundTimeout, judgeSleepFn
	t.Cleanup(func() { goalJudgeRoundTimeout, judgeSleepFn = origTimeout, origSleep })
	goalJudgeRoundTimeout = 300 * time.Millisecond
	judgeSleepFn = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return nil
		}
	}

	const evidence = "EVIDENCE-1431-UNAVAILABLE checked the report criteria"
	worker := newClaimingWorker(workerTurn{
		claim: tools.GoalClaimStatusMet, evidence: evidence, content: "RESULT-1306-TASK wrote report.md",
	})
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	var mu sync.Mutex
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		mu.Lock()
		defer mu.Unlock()
		return nil, errors.New("upstream returned 503 service unavailable")
	}}
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed (the check could not complete)", final.Status)
	}
	for _, want := range []string{"Not verified", "RESULT-1306-TASK wrote report.md"} {
		if !strings.Contains(final.Result, want) {
			t.Errorf("task result %q is missing %q", final.Result, want)
		}
	}
	assertRound2ConclusionSeparatesAnswerAndEvidence(t, al, final, evidence, "RESULT-1306-TASK wrote report.md")
}

// assertTaskConclusionKeepsResult checks a task that ended on a Judge failure:
// Failed, no attempt used, the plain Judge reason first, and the
// "Not verified" conclusion carrying the worker's actual answer (issue #1306,
// CR F2 / SFH F3: every terminal Judge failure keeps the work result).
func assertTaskConclusionKeepsResult(t *testing.T, final *task.Task, answer string) {
	t.Helper()
	if final.Status != task.StatusFailed || final.AttemptCount != 0 {
		t.Fatalf("status=%q attempts=%d, want failed with no attempt used (result: %s)", final.Status, final.AttemptCount, final.Result)
	}
	if !strings.HasPrefix(final.Result, "The Judge could not run: ") {
		t.Errorf("result %q must lead with the plain Judge reason", final.Result)
	}
	for _, want := range []string{"Not verified", answer} {
		if !strings.Contains(final.Result, want) {
			t.Errorf("task result %q is missing %q — the worker's result was discarded", final.Result, want)
		}
	}
}

func TestJudgeMisconfigured_TaskResultKeepsTheWorkResult(t *testing.T) {
	e7RecordJudgeBackoffWaits(t)
	const evidence = "EVIDENCE-1431-MISCONFIGURED checked the report criteria"
	worker := newClaimingWorker(workerTurn{
		claim: tools.GoalClaimStatusMet, evidence: evidence, content: "RESULT-1306-MISCONF wrote report.md",
	})
	al, _ := newGoalLoopTestLoop(t, worker, e7BreakJudgeModel)
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	assertTaskConclusionKeepsResult(t, final, "RESULT-1306-MISCONF wrote report.md")
	assertRound2ConclusionSeparatesAnswerAndEvidence(t, al, final, evidence, "RESULT-1306-MISCONF wrote report.md")
}

func TestJudgeTruncatedVerdict_TaskResultKeepsTheWorkResult(t *testing.T) {
	e7RecordJudgeBackoffWaits(t)
	const evidence = "EVIDENCE-1431-TRUNCATED checked the report criteria"
	worker := newClaimingWorker(workerTurn{
		claim: tools.GoalClaimStatusMet, evidence: evidence, content: "RESULT-1306-TRUNC wrote report.md",
	})
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: e7CutOffVerdict, FinishReason: "length"}, nil
	}}
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	assertTaskConclusionKeepsResult(t, final, "RESULT-1306-TRUNC wrote report.md")
	assertRound2ConclusionSeparatesAnswerAndEvidence(t, al, final, evidence, "RESULT-1306-TRUNC wrote report.md")
}

func TestJudgeNotRegistered_TaskResultKeepsTheWorkResult(t *testing.T) {
	const evidence = "EVIDENCE-1431-NO-JUDGE checked the report criteria"
	worker := newClaimingWorker(workerTurn{
		claim: tools.GoalClaimStatusMet, evidence: evidence, content: "RESULT-1306-NOJUDGE wrote report.md",
	})
	al, _ := newGoalLoopTestLoop(t, worker, nil)
	if !al.GetRegistry().RemoveAgent(string(coreagent.IDJudge)) {
		t.Fatal("SETUP: the Judge agent was not registered to begin with")
	}
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	assertTaskConclusionKeepsResult(t, final, "RESULT-1306-NOJUDGE wrote report.md")
	assertRound2ConclusionSeparatesAnswerAndEvidence(t, al, final, evidence, "RESULT-1306-NOJUDGE wrote report.md")
}

// noJudgeWait makes every Judge retry/backoff wait return at once.
func noJudgeWait(t *testing.T) {
	t.Helper()
	origSleep, origBackoff := judgeSleepFn, judgeRetryBackoff
	t.Cleanup(func() { judgeSleepFn, judgeRetryBackoff = origSleep, origBackoff })
	judgeRetryBackoff = []time.Duration{time.Millisecond}
	judgeSleepFn = func(context.Context, time.Duration) error { return errors.New("test: no wait") }
}

// newChannelGoal starts an interactive /goal on the given channel/chat and
// breaks the Judge so every adjudication is unavailable.
func newChannelGoal(t *testing.T, channel, chatID string) (*AgentLoop, *AgentInstance, *session.UnifiedStore, string) {
	t.Helper()
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: channel, ChatID: chatID, SessionKey: "sk-" + channel, UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}}
	return al, agentInst, store, sid
}

// goalConclusionErrors returns the goal_conclusion error events published so far.
func goalConclusionErrors(sub EventSubscription) []string {
	var msgs []string
	for {
		select {
		case evt, ok := <-sub.C:
			if !ok {
				return msgs
			}
			if p, isErr := evt.Payload.(ErrorPayload); isErr && p.Stage == "goal_conclusion" {
				msgs = append(msgs, p.Message)
			}
		default:
			return msgs
		}
	}
}

// CR F3: a /goal started on Telegram gets the Not-verified conclusion on
// Telegram — not only in the web transcript and the web pill.
func TestJudgeUnavailable_ChannelGoalConclusionReachesItsChannel(t *testing.T) {
	noJudgeWait(t)
	al, agentInst, _, sid := newChannelGoal(t, "telegram", "tg-1306")
	drainOutbound(al.bus)

	al.redriveGoalAdjudication(&goalDeferredAdjudicationWork{
		agentInst: agentInst, sessionID: sid, claimText: "RESULT-1306-TG all tests pass",
	})

	var delivered *bus.OutboundMessage
	for _, msg := range drainOutbound(al.bus) {
		if msg.Channel == "telegram" && strings.Contains(msg.Content, "Not verified") {
			m := msg
			delivered = &m
		}
	}
	if delivered == nil {
		t.Fatal("no Not-verified conclusion was published to the goal's Telegram chat")
	}
	if delivered.ChatID != "tg-1306" || !strings.Contains(delivered.Content, "RESULT-1306-TG all tests pass") {
		t.Fatalf("conclusion delivered as %+v, want chat tg-1306 carrying the claimed result", delivered)
	}
}

// SFH F4: when the conclusion cannot be saved to the transcript, that failure
// is raised visibly, and the conclusion still lands durably on the goal record
// (which a reconnect rehydrates) and on the goal's channel.
func TestJudgeUnavailable_ConclusionSaveFailureIsVisibleAndStillDurable(t *testing.T) {
	noJudgeWait(t)
	orig := appendGoalConclusionFn
	t.Cleanup(func() { appendGoalConclusionFn = orig })
	appendGoalConclusionFn = func(*AgentLoop, *session.UnifiedStore, string, string, string) error {
		return errors.New("disk full")
	}
	al, agentInst, _, sid := newChannelGoal(t, "webchat", "c1")
	sub := al.SubscribeEvents(256)
	defer al.UnsubscribeEvents(sub.ID)
	drainOutbound(al.bus)

	al.redriveGoalAdjudication(&goalDeferredAdjudicationWork{
		agentInst: agentInst, sessionID: sid, claimText: "RESULT-1306-SAVE all tests pass",
	})

	// Accepted architect R2-3 safe-text decision: ordinary Message is
	// catalogue-only; the same specific cause survives in internal/Verbose Detail.
	payloads := r3DrainConclusionPayloads(sub)
	if len(payloads) == 0 {
		t.Fatal("a failed conclusion save was not raised visibly; no goal_conclusion payload")
	}
	for _, p := range payloads {
		wire := WireLLMError(p)
		if p.Code != r3ConclusionFailedCode || wire.Code != r3ConclusionFailedCode {
			t.Errorf("save-failure payload/wire Code=%q/%q, want judge_conclusion_failed", p.Code, wire.Code)
		}
		if want := r3ConclusionCatalogueCopy(t); p.Message != want || wire.Message != want {
			t.Errorf("save-failure Message must be catalogue-only: payload=%q wire=%q want=%q", p.Message, wire.Message, want)
		}
		detail := r3InternalConclusionDetail(t, p)
		if !strings.Contains(detail, "could not be saved") || !strings.Contains(detail, "disk full") {
			t.Errorf("save-failure payload Detail=%q, want both could not be saved and disk full", detail)
		}
		if wire.Detail == nil || !strings.Contains(*wire.Detail, "could not be saved") || !strings.Contains(*wire.Detail, "disk full") {
			t.Errorf("save-failure WireLLMError Detail=%v, want the same specific cause", wire.Detail)
		}
	}
	g := mustActiveGoalForSession(t, sid)
	if g == nil || !strings.Contains(g.LatestReason, "Not verified") || !strings.Contains(g.LatestReason, "RESULT-1306-SAVE") {
		t.Fatalf("goal record latest reason = %q, want the durable Not-verified conclusion with the result", g.LatestReason)
	}
	published := false
	for _, msg := range drainOutbound(al.bus) {
		if strings.Contains(msg.Content, "Not verified") {
			published = true
		}
	}
	if !published {
		t.Error("the conclusion was not delivered to the goal's chat")
	}
}

// SFH F1: a steered helper whose lifecycle record exists but cannot be read
// must not be treated as a parentless chat. The conclusion is recorded with
// the reason it could not be delivered, and the failure is raised visibly.
func TestJudgeUnavailable_UnreadableAudienceIsReportedNotGuessed(t *testing.T) {
	noJudgeWait(t)
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	lifecycleDir := t.TempDir()
	lifecycle := session.NewLifecycleStore(lifecycleDir)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	wireSteerCompletionDeps(t, al)
	parentMeta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "native-agent")
	if err != nil {
		t.Fatalf("NewSession(parent): %v", err)
	}
	child := launchGoalBearingChild947(t, al, parentMeta.ID, "call-1306-unreadable")
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated judge outage")
	}}
	if werr := os.WriteFile(filepath.Join(lifecycleDir, child.SessionID+".jsonl"), []byte("{not a lifecycle record\n"), 0o600); werr != nil {
		t.Fatalf("SETUP: corrupt the lifecycle record: %v", werr)
	}
	if _, lerr := lifecycle.Load(child.SessionID); lerr == nil || errors.Is(lerr, session.ErrLifecycleNotFound) {
		t.Fatalf("SETUP: want an unreadable (not missing) lifecycle record, Load err = %v", lerr)
	}
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	sub := al.SubscribeEvents(256)
	defer al.UnsubscribeEvents(sub.ID)

	al.redriveGoalAdjudication(&goalDeferredAdjudicationWork{
		agentInst: agentInst, sessionID: child.SessionID, claimText: "RESULT-1306-AUD shipped the parser",
	})

	entries, err := al.GetSessionStore().ReadTranscript(child.SessionID)
	if err != nil {
		t.Fatalf("ReadTranscript(child): %v", err)
	}
	var note string
	for _, e := range entries {
		if strings.Contains(e.Content, "Not verified") {
			note = e.Content
		}
	}
	if !strings.Contains(note, "could not be delivered") || !strings.Contains(note, "RESULT-1306-AUD shipped the parser") {
		t.Fatalf("child transcript conclusion = %q, want the result plus why it could not be delivered", note)
	}
	if errs := goalConclusionErrors(sub); len(errs) == 0 {
		t.Error("the undeliverable conclusion was not raised as a visible error")
	}
}
