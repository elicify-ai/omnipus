package agent

// Issue #1306 founder ruling: the Judge keeps its own time bounds, but "the
// judge needs to conclude, we can not just end turns". These tests drive a
// Judge that never answers through each path whose bound runs out, and pin
// that every one ends in a visible "Not verified" conclusion that ALSO
// carries the worker's actual result.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
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
// ends with a Result that carries the verdict AND the worker's evidence.
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

	worker := newClaimingWorker(turnClaimMet("RESULT-1306-TASK wrote report.md"))
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
}
