// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_error_leak_test.go pins the rule that a provider's raw error
// response never reaches anything a person or a model reads.
//
// A provider error carries the provider's response body, and providers echo
// credential fragments ("Incorrect API key provided: sk-…") and request
// payloads in those bodies. common.ProviderError.Error() renders that body
// (body=%q) and providers.FailoverError.Error() wraps it, so ANY surface built
// from the error's text inherits it. The surfaces pinned here:
//
//   - a task run broken by a temporary error: the task result, the prompt of
//     the restarted run, the run's transcript, the goal record, the goal
//     outcome line and the owner wake;
//   - a Judge that stays unavailable for a transient reason: the reason its
//     adjudication reports (read by the task run, the chat goal pill and the
//     plan engine) and the task it leaves Failed;
//   - a chat turn broken by a provider error: the error event's message, the
//     transcript, and the Verbose-chat detail;
//   - the gateway log, which keeps the raw error for operators but must not
//     carry a registered credential.
//
// Oracles are the planted strings (a credential fragment and a request-payload
// echo, neither of which Omnipus ever writes itself) and the contract's plain
// message for the error's typed code (contracts/components/schemas/LLMError.yaml
// x-user-messages, via UserMessageForCode).
//
// Every test starts from NO registered credential (isolateSensitiveValueReplacer)
// so an absence proven by a surface test is the plain-message rule's doing, not
// a scrub; the two tests that do register one say so.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// leakSecret is a credential-shaped fragment planted in a provider error body.
const leakSecret = "sk-live-LEAK-4Rt8Zq19"

// leakPayloadEcho is a request-payload fragment a provider echoed back.
const leakPayloadEcho = "PAYLOAD-ECHO-7719"

// leakBody is a provider error body carrying both planted fragments.
const leakBody = `{"error":{"message":"Incorrect API key provided: ` + leakSecret +
	`","echo":"` + leakPayloadEcho + `"}}`

// isolateSensitiveValueReplacer clears the process-wide credential replacer for
// the test and restores it afterwards. Call it BEFORE the test registers a
// credential of its own.
func isolateSensitiveValueReplacer(t *testing.T) {
	t.Helper()
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
}

// assertNoProviderText fails when s carries either planted fragment.
func assertNoProviderText(t *testing.T, where, s string) {
	t.Helper()
	for _, planted := range []string{leakSecret, leakPayloadEcho} {
		if strings.Contains(s, planted) {
			t.Errorf("%s carries raw provider text %q:\n%s", where, planted, s)
		}
	}
}

// temporaryLeakErrors are the temporary turn errors a task run restarts on,
// each carrying the planted fragments in its text.
func temporaryLeakErrors() []struct {
	name     string
	err      error
	wantCode LLMErrorCode
} {
	return []struct {
		name     string
		err      error
		wantCode LLMErrorCode
	}{
		{"rate_limited_429_with_body", fmt.Errorf("LLM call failed after retries: %w", &providers.FailoverError{
			Reason: providers.FailoverRateLimit, Provider: "acme-llm", Model: "test-model", Status: 429,
			Wrapped: &common.ProviderError{Status: 429, Body: leakBody, BodyPreview: leakBody},
		}), CodeRateLimited},
		{"provider_outage_503_with_body", fmt.Errorf("LLM call failed after retries: %w",
			&common.ProviderError{Status: 503, Body: leakBody, BodyPreview: leakBody}), CodeNetwork},
		{"provider_stalled_after_partial_body", fmt.Errorf("LLM call failed after retries: %w",
			fmt.Errorf("stream from acme-llm went silent after %s: %w", leakBody, common.NewStallError(5*time.Minute))),
			CodeProviderStalled},
		{"unknown_404_with_body", fmt.Errorf("LLM call failed after retries: %w",
			&common.ProviderError{Status: 404, Body: leakBody, BodyPreview: leakBody}), CodeUnknown},
	}
}

// Given a task whose worker turn keeps breaking on a temporary provider error
// whose response body carries a credential fragment and a request echo
// When every run fails and the task restarts until its attempt limit
// Then no task result, restarted prompt, transcript entry, goal record, goal
// outcome line or owner wake carries either fragment, and the operator reads
// the contract's plain message for the error instead.
func TestTaskRun_TemporaryErrorNeverLeaksProviderText(t *testing.T) {
	isolateSensitiveValueReplacer(t)
	for _, tc := range temporaryLeakErrors() {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranslateTurnError(tc.err).Code; got != tc.wantCode {
				t.Fatalf("BLOCKED: the fixture error translates to %q, want %q", got, tc.wantCode)
			}
			if !strings.Contains(tc.err.Error(), leakSecret) {
				t.Fatalf("BLOCKED: the fixture error's text does not carry the planted secret: %q", tc.err.Error())
			}
			plain := UserMessageForCode(tc.wantCode)

			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
			var wakesMu sync.Mutex
			var wakes []string
			al.asyncNotifier.registerObserver(func(ev AsyncNotifyEvent) {
				wakesMu.Lock()
				wakes = append(wakes, ev.Content)
				wakesMu.Unlock()
			})
			tk := newRunLoopTask(t, al, nil)
			store := al.GetAgentStore(tk.AgentID)

			const limit = 3 // the default task attempt limit
			var sessions, prompts []string
			for attempt := 1; attempt <= limit; attempt++ {
				claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
				if err != nil {
					t.Fatalf("run %d: ClaimForRun: %v", attempt, err)
				}
				sid, serr := al.taskExecutor.createTaskSessionSync(claimed)
				if serr != nil || sid == "" {
					t.Fatalf("run %d: createTaskSessionSync: sid=%q err=%v", attempt, sid, serr)
				}
				sessions = append(sessions, sid)
				al.taskExecutor.executeTaskRun(context.Background(), claimed, sid, "", nil,
					func(prompt string) (string, error) {
						prompts = append(prompts, prompt)
						return "", tc.err
					})
				got, gerr := al.taskStore.Get(tk.ID)
				if gerr != nil {
					t.Fatalf("run %d: re-read task: %v", attempt, gerr)
				}
				assertNoProviderText(t, fmt.Sprintf("the task result after run %d", attempt), got.Result)
				if !strings.Contains(got.Result, plain) {
					t.Errorf("run %d: the task result %q does not carry the plain message %q", attempt, got.Result, plain)
				}
			}

			final, err := al.taskStore.Get(tk.ID)
			if err != nil {
				t.Fatalf("re-read task: %v", err)
			}
			if final.Status != task.StatusFailed || final.AttemptCount != limit {
				t.Fatalf("status=%q attempt_count=%d, want failed after %d attempts (result %q)",
					final.Status, final.AttemptCount, limit, final.Result)
			}

			// The prompt a restarted run starts from carries why the previous
			// run failed — to the model.
			if len(prompts) != limit {
				t.Fatalf("worker turns = %d, want %d", len(prompts), limit)
			}
			for i, p := range prompts {
				assertNoProviderText(t, fmt.Sprintf("the prompt of run %d", i+1), p)
			}
			for i := 1; i < limit; i++ {
				if !strings.Contains(prompts[i], plain) {
					t.Errorf("the restarted run %d's prompt does not carry the plain message %q:\n%s", i+1, plain, prompts[i])
				}
			}

			for i, sid := range sessions {
				entries, rerr := store.ReadTranscript(sid)
				if rerr != nil {
					t.Fatalf("read transcript of run %d: %v", i+1, rerr)
				}
				sawPlain := false
				for _, e := range entries {
					assertNoProviderText(t, fmt.Sprintf("run %d transcript entry %q (%s)", i+1, e.ID, e.Role), e.Content)
					if e.Status == "error" && strings.Contains(e.Content, plain) {
						sawPlain = true
					}
				}
				if !sawPlain {
					t.Errorf("run %d's transcript has no error entry carrying the plain message %q", i+1, plain)
				}
			}

			rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
			assertNoProviderText(t, "the goal record's terminal reason", rec.TerminalReason)
			assertNoProviderText(t, "the goal record's latest reason", rec.LatestReason)
			for i, h := range rec.TerminalHistory {
				assertNoProviderText(t, fmt.Sprintf("the goal record's terminal history entry %d", i), h.TerminalReason)
			}
			last := sessions[len(sessions)-1]
			waitForGoalOutcomeEntry(t, store, last)
			outcome := requireOneGoalOutcome(t, store, last, rec.GoalID)
			assertNoProviderText(t, "the goal outcome line", outcome.Content)
			if outcome.GoalOutcome.JudgeReason != nil {
				assertNoProviderText(t, "the goal outcome line's judge reason", *outcome.GoalOutcome.JudgeReason)
			}
			if !strings.Contains(outcome.Content, plain) {
				t.Errorf("the goal outcome line %q does not carry the plain message %q", outcome.Content, plain)
			}

			wakesMu.Lock()
			defer wakesMu.Unlock()
			if len(wakes) == 0 {
				t.Fatal("the owner was never woken when the task's attempts ran out")
			}
			for i, w := range wakes {
				assertNoProviderText(t, fmt.Sprintf("owner wake %d", i), w)
			}
		})
	}
}

// Given a Judge whose model keeps answering with a provider error whose body
// carries a credential fragment and a request echo
// When the adjudication gives up waiting to retry
// Then the reason it reports — which the task run writes onto the task, the
// chat goal paints onto its pill and the plan engine stores as its handover —
// carries neither fragment.
func TestJudgeCriteria_TransientErrorReasonNeverLeaksProviderText(t *testing.T) {
	isolateSensitiveValueReplacer(t)
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	judgeSleepFn = func(context.Context, time.Duration) error {
		return errors.New("leak test: stop waiting so the adjudication reports its reason")
	}
	for _, tc := range temporaryLeakErrors() {
		t.Run(tc.name, func(t *testing.T) {
			al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			judge := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) { return nil, tc.err }}
			judgeInst.Provider = judge

			res := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
				Scope:           task.VerdictScopeTask,
				TaskID:          "leak-task",
				AssigneeAgentID: "native-agent",
				Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "report.md lists all three quotes")},
				Attempt:         1,
				ClaimText:       "wrote report.md",
			})
			if judge.callCount() == 0 {
				t.Fatal("BLOCKED: the Judge's model was never called")
			}
			if !res.Unavailable {
				t.Fatalf("BLOCKED: the adjudication did not report the Judge unavailable (reason %q)", res.Reason)
			}
			assertNoProviderText(t, "the unavailable adjudication's reason", res.Reason)
			if strings.TrimSpace(res.Reason) == "" {
				t.Error("the unavailable adjudication reports no reason at all")
			}
		})
	}
}

// Given a task whose worker claims and whose Judge keeps failing on a provider
// error whose body carries a credential fragment and a request echo
// When the run gives up on the Judge
// Then the task result, the goal record and the goal outcome line carry
// neither fragment.
func TestTaskRun_TransientJudgeErrorNeverLeaksProviderText(t *testing.T) {
	isolateSensitiveValueReplacer(t)
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

	worker := newClaimingWorker(turnClaimMet("wrote report.md"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	judgeErr := fmt.Errorf("LLM call failed after retries: %w",
		&common.ProviderError{Status: 503, Body: leakBody, BodyPreview: leakBody})
	judgeInst.Provider = &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) { return nil, judgeErr }}
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	want := fmt.Sprintf("The Judge could not check this task's work after %d tries", judgeUnavailableRetryBound)
	if final.Status != task.StatusFailed || !strings.Contains(final.Result, want) {
		t.Fatalf("status=%q result=%q, want failed with %q", final.Status, final.Result, want)
	}
	assertNoProviderText(t, "the task result", final.Result)

	rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
	assertNoProviderText(t, "the goal record's terminal reason", rec.TerminalReason)
	store := al.GetAgentStore(tk.AgentID)
	waitForGoalOutcomeEntry(t, store, final.SessionID)
	outcome := requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
	assertNoProviderText(t, "the goal outcome line", outcome.Content)
}

// enableLeakTestLogFile points the gateway log at a file under the test's temp
// dir and returns a reader for what was written.
func enableLeakTestLogFile(t *testing.T) func() string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gateway.log")
	if err := logger.EnableFileLogging(path); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)
	return func() string {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read gateway log: %v", err)
		}
		return string(b)
	}
}

// Given a registered credential that a provider echoes in an error body
// When a task run's worker turn breaks on that error
// Then the gateway log still records the failure for the operator, with the
// credential replaced by the scrub marker.
func TestTaskRun_GatewayLogScrubsRegisteredCredential(t *testing.T) {
	isolateSensitiveValueReplacer(t)
	// Registered the way boot registers it (ADR-004 step 6): on the config.
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.RegisterSensitiveValues([]string{leakSecret})
	})
	readLog := enableLeakTestLogFile(t)
	tk := newRunLoopTask(t, al, nil)
	claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
	if err != nil {
		t.Fatalf("ClaimForRun: %v", err)
	}
	sid, serr := al.taskExecutor.createTaskSessionSync(claimed)
	if serr != nil {
		t.Fatalf("createTaskSessionSync: %v", serr)
	}
	turnErr := fmt.Errorf("LLM call failed after retries: %w",
		&common.ProviderError{Status: 503, Body: leakBody, BodyPreview: leakBody})
	al.taskExecutor.executeTaskRun(context.Background(), claimed, sid, "", nil,
		func(string) (string, error) { return "", turnErr })

	logText := readLog()
	if !strings.Contains(logText, "Agent execution failed") {
		t.Fatalf("BLOCKED: the gateway log has no line for the failed run:\n%s", logText)
	}
	if strings.Contains(logText, leakSecret) {
		t.Errorf("the gateway log carries the registered credential %q", leakSecret)
	}
	if !strings.Contains(logText, "[FILTERED]") {
		t.Errorf("the gateway log does not show where the credential was scrubbed")
	}
}

// Given a chat turn whose provider answers with an error body carrying a
// registered credential and a request echo
// When the turn ends on that error
// Then the error event's message and the transcript carry neither fragment,
// and the Verbose-chat detail — the one surface allowed to show the provider's
// body — carries no registered credential.
func TestChatTurn_ProviderErrorNeverLeaksProviderText(t *testing.T) {
	isolateSensitiveValueReplacer(t)
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider := &failingWorker{err: func() error {
		return &common.ProviderError{Status: 404, Body: leakBody, BodyPreview: leakBody, ContentType: "application/json"}
	}}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}
	cfg.RegisterSensitiveValues([]string{leakSecret})
	readLog := enableLeakTestLogFile(t)

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("SETUP: default agent must be registered")
	}
	sub := al.SubscribeEvents(64)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "web", defaultAgent.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, runErr := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey:          "leak-session",
		Channel:             "web",
		ChatID:              meta.ID,
		UserMessage:         "trigger provider error",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: meta.ID,
		TranscriptStore:     store,
	})
	if runErr == nil {
		t.Fatal("BLOCKED: the turn did not end on the provider error")
	}
	if provider.callCount() == 0 {
		t.Fatal("BLOCKED: the provider was never called")
	}

	for _, e := range readTranscriptEntries(t, store, meta.ID) {
		assertNoProviderText(t, fmt.Sprintf("chat transcript entry %q (%s)", e.ID, e.Type), e.Content)
	}

	sawError := false
	for _, ev := range drainEvents(sub.C) {
		p, ok := ev.Payload.(ErrorPayload)
		if ev.Kind != EventKindError || !ok {
			continue
		}
		sawError = true
		assertNoProviderText(t, "the chat error event's message", p.Message)
		// The WS forwarder's own Detail rule (pkg/gateway/websocket.go).
		detail := TranslateLLMError(p.ProviderError, p.Message).Detail
		if p.Code != "" {
			detail = BuildDetail(p.ProviderError, p.Message)
		}
		if strings.Contains(detail, leakSecret) {
			t.Errorf("the Verbose-chat detail carries the registered credential: %q", detail)
		}
		if p.ProviderError != nil && !strings.Contains(detail, "[FILTERED]") {
			t.Errorf("the Verbose-chat detail %q does not show where the credential was scrubbed", detail)
		}
	}
	if !sawError {
		t.Fatal("BLOCKED: no error event was emitted for the failed turn")
	}

	logText := readLog()
	if strings.Contains(logText, leakSecret) {
		t.Errorf("the gateway log carries the registered credential %q", leakSecret)
	}
}
