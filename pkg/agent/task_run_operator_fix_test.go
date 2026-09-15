// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_run_operator_fix_test.go pins the founder decision of 2026-09-15 for
// task runs (task_run_loop.go, operator_only_turn_error.go):
//
//   - a worker turn refused for a reason only an operator can fix (rejected
//     credentials, an unknown provider, no model) ends the task Failed AT ONCE
//     with the reason and how to fix it: no attempt used, no restart, no Judge
//     call, exactly one goal outcome line, and no secret or raw provider body
//     in anything the user reads;
//   - a temporary error (a rate limit, a provider outage, a stalled stream, a
//     timeout) still breaks the run and restarts the task, up to its attempt
//     limit.
//
// Oracles are the decision's observable outcomes — task status, AttemptCount,
// whether the task restarted, worker and Judge call counts, the outcome line —
// and the founder's own wording for the rejected-key message.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// opFixSecret is a credential-shaped string planted in the provider's error
// body. It must never reach the task, its transcript or its goal record.
const opFixSecret = "sk-live-OPFIX-7Q2x9"

// opFixRawBody is the provider's raw 401 response body.
const opFixRawBody = `{"error":{"type":"invalid_api_key","message":"Incorrect API key provided: ` + opFixSecret + `"}}`

// failingWorker answers every request with err() and counts the requests that
// reached it.
type failingWorker struct {
	mu    sync.Mutex
	calls int
	err   func() error
}

func (w *failingWorker) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	w.calls++
	w.mu.Unlock()
	return nil, w.err()
}

func (w *failingWorker) GetDefaultModel() string { return "test-model" }

func (w *failingWorker) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// Given a task whose worker turn is refused for a reason only an operator can fix
// When the run handles that refusal
// Then the task ends Failed at once with the reason and the fix, no attempt is
// used, the task does not restart, the Judge is never asked, the run's session
// holds exactly one goal outcome line, and no secret or raw provider body
// reaches the task, its goal or its transcript.
func TestTaskRun_OperatorOnlyErrorEndsFailedAtOnce(t *testing.T) {
	cases := []struct {
		name         string
		mutateCfg    func(*config.Config)
		workerErr    func() error
		precondition func(t *testing.T, ag *AgentInstance)
		wantCalls    int
		checkResult  func(result string) error
	}{
		{
			name: "provider_key_rejected_401",
			workerErr: func() error {
				return &providers.FailoverError{
					Reason: providers.FailoverAuth, Provider: "acme-llm", Model: "test-model", Status: 401,
					Wrapped: &common.ProviderError{Status: 401, Body: opFixRawBody, BodyPreview: opFixRawBody},
				}
			},
			wantCalls: 1,
			checkResult: exactResult(
				"The task could not run: the provider key for acme-llm was rejected. " +
					"Fix it in Settings → Providers, then run the task again."),
		},
		{
			name: "provider_key_forbidden_403",
			workerErr: func() error {
				return &common.ProviderError{Status: 403, Body: opFixRawBody, BodyPreview: opFixRawBody}
			},
			wantCalls: 1,
			checkResult: func(result string) error {
				if !strings.HasPrefix(result, "The task could not run: ") || !strings.Contains(result, "rejected") ||
					!strings.HasSuffix(result, "Fix it in Settings → Providers, then run the task again.") {
					return fmt.Errorf("result %q does not say the key was rejected and where to fix it", result)
				}
				return nil
			},
		},
		{
			name: "unknown_provider",
			mutateCfg: func(cfg *config.Config) {
				for i := range cfg.Agents.List {
					if cfg.Agents.List[i].ID == "native-agent" {
						cfg.Agents.List[i].Model = &config.AgentModelConfig{
							Primary: "nonexistent-vendor/no-such-model-op", Provider: "no-such-provider-op",
						}
					}
				}
			},
			precondition: func(t *testing.T, ag *AgentInstance) {
				if needs, id := ag.needsProviderSnapshot(); !needs || id != "no-such-provider-op" {
					t.Fatalf("BLOCKED: the setup did not give the worker an unknown provider (needs=%v id=%q)", needs, id)
				}
			},
			wantCalls: 0,
			checkResult: exactResult(
				"The task could not run: the agent Native Agent uses the provider no-such-provider-op, which is not " +
					"configured. Configure it in Settings → Providers or pick a configured provider in the agent's " +
					"settings, then run the task again."),
		},
		{
			name: "no_model_assigned",
			mutateCfg: func(cfg *config.Config) {
				cfg.Agents.Defaults.DefaultModel = config.DefaultModel{}
			},
			precondition: func(t *testing.T, ag *AgentInstance) {
				if needs, _ := ag.needsProviderSnapshot(); needs || !ag.needsModelSnapshot() {
					t.Fatalf("BLOCKED: the setup did not give the worker a missing model and a known provider")
				}
			},
			wantCalls: 0,
			checkResult: exactResult(
				"The task could not run: the agent Native Agent has no model assigned. " +
					"Pick a model in the agent's settings, then run the task again."),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := &failingWorker{err: tc.workerErr}
			if worker.err == nil {
				worker.err = func() error { return errors.New("the worker's provider must not be reached") }
			}
			al, judgeInst := newGoalLoopTestLoop(t, worker, tc.mutateCfg)
			t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
			judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
			judgeInst.Provider = judge
			if tc.precondition != nil {
				ag, ok := al.GetRegistry().GetAgent("native-agent")
				if !ok {
					t.Fatal("native-agent not registered")
				}
				tc.precondition(t, ag)
			}
			tk := newRunLoopTask(t, al, nil) // the default attempt limit, 3

			final := runTaskUntilTerminal(t, al, tk.ID, 1)

			if final.Status != task.StatusFailed {
				t.Fatalf("status = %q, want failed (result: %s)", final.Status, final.Result)
			}
			if err := tc.checkResult(final.Result); err != nil {
				t.Error(err)
			}
			if final.AttemptCount != 0 {
				t.Errorf("attempt_count = %d, want 0 — a refusal only an operator can fix is not a failed attempt", final.AttemptCount)
			}
			if n := judge.callCount(); n != 0 {
				t.Errorf("Judge calls = %d, want 0 — there is no claim to judge", n)
			}
			if n := worker.callCount(); n != tc.wantCalls {
				t.Errorf("worker provider calls = %d, want %d", n, tc.wantCalls)
			}

			// A restart would move the task back to next, mint a new session,
			// use an attempt and call the worker again. Give a wrong restart
			// time to land before asserting none did.
			time.Sleep(300 * time.Millisecond)
			after, err := al.taskStore.Get(tk.ID)
			if err != nil {
				t.Fatalf("re-read task: %v", err)
			}
			if after.Status != task.StatusFailed || after.AttemptCount != 0 || after.SessionID != final.SessionID ||
				worker.callCount() != tc.wantCalls {
				t.Errorf("the task restarted: status=%q attempt_count=%d session %q -> %q, worker calls=%d",
					after.Status, after.AttemptCount, final.SessionID, after.SessionID, worker.callCount())
			}

			rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
			store := al.GetAgentStore(tk.AgentID)
			waitForGoalOutcomeEntry(t, store, final.SessionID)
			e := requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
			if e.GoalOutcome.Ending != generated.GoalOutcomeEndingOther {
				t.Errorf("outcome ending = %q, want other", e.GoalOutcome.Ending)
			}
			if !strings.Contains(e.Content, final.Result) {
				t.Errorf("outcome line %q does not carry the task's reason %q", e.Content, final.Result)
			}

			for _, leak := range []string{opFixSecret, "invalid_api_key", "Incorrect API key"} {
				if strings.Contains(final.Result, leak) || strings.Contains(rec.TerminalReason, leak) {
					t.Errorf("the task result or goal reason carries raw provider text %q", leak)
				}
			}
			entries, rerr := store.ReadTranscript(final.SessionID)
			if rerr != nil {
				t.Fatalf("read transcript: %v", rerr)
			}
			sawReason := false
			for _, en := range entries {
				if strings.Contains(en.Content, opFixSecret) || strings.Contains(en.Content, "invalid_api_key") {
					t.Errorf("transcript entry %q (%s) carries raw provider text: %q", en.ID, en.Role, en.Content)
				}
				if en.Status == "error" && en.Content == final.Result {
					sawReason = true
				}
			}
			if !sawReason {
				t.Errorf("the run's transcript has no error entry carrying the plain reason %q", final.Result)
			}
		})
	}
}

func exactResult(want string) func(string) error {
	return func(got string) error {
		if got != want {
			return fmt.Errorf("result = %q\nwant     %q", got, want)
		}
		return nil
	}
}

// Given a task whose worker turn keeps ending on a temporary error
// When each run breaks
// Then each run uses one attempt and the task restarts, until the third failed
// run ends it Failed at the attempt limit.
//
// Driven at the run loop (executeTaskRun with the error runTurn returns) so the
// provider's own in-turn retry backoff does not stretch the test; the restart
// the loop asks for is exactly what runTask re-dispatches.
func TestTaskRun_TemporaryErrorStillRestartsUpToTheLimit(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode LLMErrorCode
	}{
		{"rate_limited_429", fmt.Errorf("LLM call failed after retries: %w", &providers.FailoverError{
			Reason: providers.FailoverRateLimit, Provider: "acme-llm", Model: "test-model", Status: 429,
			Wrapped: errors.New("rate limit exceeded"),
		}), CodeRateLimited},
		{"provider_outage_503", fmt.Errorf("LLM call failed after retries: %w",
			&common.ProviderError{Status: 503, Body: "upstream unavailable"}), CodeNetwork},
		{"provider_stalled", fmt.Errorf("LLM call failed after retries: %w",
			&common.StallError{SilentFor: 5 * time.Minute}), CodeProviderStalled},
		{"turn_timed_out", fmt.Errorf("%w: %w", ErrTurnTimedOut, context.DeadlineExceeded), CodeTurnTimedOut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TranslateTurnError(tc.err).Code; got != tc.wantCode {
				t.Fatalf("BLOCKED: the fixture error translates to %q, want %q", got, tc.wantCode)
			}
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			tk := newRunLoopTask(t, al, nil)

			const limit = 3 // the default task attempt limit
			for attempt := 1; attempt <= limit; attempt++ {
				claimed, err := al.taskStore.ClaimForRun(tk.ID, time.Now())
				if err != nil {
					t.Fatalf("attempt %d: ClaimForRun: %v", attempt, err)
				}
				sid, serr := al.taskExecutor.createTaskSessionSync(claimed)
				if serr != nil {
					t.Fatalf("attempt %d: createTaskSessionSync: %v", attempt, serr)
				}
				turns := 0
				redispatch := al.taskExecutor.executeTaskRun(context.Background(), claimed, sid, "", nil,
					func(string) (string, error) {
						turns++
						return "", tc.err
					})

				got, gerr := al.taskStore.Get(tk.ID)
				if gerr != nil {
					t.Fatalf("attempt %d: re-read task: %v", attempt, gerr)
				}
				if turns != 1 || got.AttemptCount != attempt {
					t.Fatalf("attempt %d: turns=%d attempt_count=%d, want 1 turn and %d attempt(s) used",
						attempt, turns, got.AttemptCount, attempt)
				}
				if attempt < limit {
					if redispatch != tk.ID || got.Status != task.StatusNext {
						t.Fatalf("attempt %d: redispatch=%q status=%q (result %q), want the task restarted",
							attempt, redispatch, got.Status, got.Result)
					}
					continue
				}
				if redispatch != "" || got.Status != task.StatusFailed ||
					!strings.Contains(got.Result, "Task failed after 3 attempt(s) (max 3)") {
					t.Fatalf("attempt %d: redispatch=%q status=%q result=%q, want Failed at the attempt limit",
						attempt, redispatch, got.Status, got.Result)
				}
			}
		})
	}
}

// TestClassifyOperatorOnlyTurnError pins the one classification both the Judge
// and a task run use: the typed code decides, never the error text, and a Stop
// or timeout wins over a refusal.
func TestClassifyOperatorOnlyTurnError(t *testing.T) {
	auth401 := &providers.FailoverError{Reason: providers.FailoverAuth, Status: 401, Wrapped: errors.New("invalid api key")}
	cases := []struct {
		name      string
		err       error
		wantCode  LLMErrorCode
		wantCause operatorFixCause
	}{
		{"nil", nil, "", operatorFixNone},
		{"credentials_401", fmt.Errorf("LLM call failed after retries: %w", auth401), CodeProviderAuthFailed, operatorFixCredentialsRejected},
		{"credentials_403", &common.ProviderError{Status: 403, Body: "forbidden"}, CodeProviderAuthFailed, operatorFixCredentialsRejected},
		{"sign_in_expired", fmt.Errorf("token source: %w", providers.ErrProviderNeedsSignIn), CodeNeedsProvider, operatorFixSignInExpired},
		{"provider_not_configured", fmt.Errorf("%w: agent_id=a provider=p", ErrAgentNeedsProvider), CodeNeedsProvider, operatorFixProviderNotConfigured},
		{"model_unassigned", fmt.Errorf("%w: agent_id=a", ErrAgentModelUnassigned), CodeModelUnassigned, operatorFixModelUnassigned},
		{"context_window_unknown", fmt.Errorf("%w: agent_id=a model=m", ErrContextWindowUnknown), CodeContextWindowUnknown, operatorFixContextWindowUnknown},
		{"agent_not_on_workspace", fmt.Errorf("resolve: %w", ErrAgentNotWorkspaceMember), CodeAgentNotConfigured, operatorFixAgentNotOnWorkspace},
		{"work_dir_unavailable", fmt.Errorf("resolve: %w", ErrWorkspaceWorkDirUnavailable), CodeWorkspaceUnavailable, operatorFixWorkDirUnavailable},
		{"agent_home_unavailable", fmt.Errorf("resolve: %w", ErrAgentHomeUnavailable), CodeWorkspaceUnavailable, operatorFixWorkDirUnavailable},

		{"rate_limited_429", &providers.FailoverError{Reason: providers.FailoverRateLimit, Status: 429, Wrapped: errors.New("slow down")}, CodeRateLimited, operatorFixNone},
		{"outage_503", &common.ProviderError{Status: 503, Body: "unavailable"}, CodeNetwork, operatorFixNone},
		{"provider_stalled", &common.StallError{SilentFor: time.Minute}, CodeProviderStalled, operatorFixNone},
		{"turn_timed_out", fmt.Errorf("%w: %w", ErrTurnTimedOut, context.DeadlineExceeded), CodeTurnTimedOut, operatorFixNone},
		{"unclassified", errors.New("something odd happened"), CodeUnknown, operatorFixNone},
		{"auth_words_without_a_status_are_not_trusted", errors.New("401 unauthorized: invalid api key"), CodeUnknown, operatorFixNone},
		{"stop_wins_over_a_refusal", fmt.Errorf("%w: %w", ErrTurnCanceled, auth401), CodeTurnCanceled, operatorFixNone},
		{"timeout_wins_over_a_refusal", fmt.Errorf("%w: %w", ErrTurnTimedOut, auth401), CodeTurnTimedOut, operatorFixNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, cause := classifyOperatorOnlyTurnError(tc.err)
			if code != tc.wantCode || cause != tc.wantCause {
				t.Fatalf("classifyOperatorOnlyTurnError = (%q, %d), want (%q, %d)", code, cause, tc.wantCode, tc.wantCause)
			}
		})
	}
}

// TestJudgeDispatchNeedsOperator_WordsEachCauseForTheJudge pins the Judge's
// messages. The first five are the wording UAT E-7 shipped (commit 0120d3cf) and
// must not drift now that the classification is shared.
func TestJudgeDispatchNeedsOperator_WordsEachCauseForTheJudge(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("x: %w", providers.ErrProviderNeedsSignIn),
			"the Judge's provider sign-in expired; sign in again under Settings → Providers"},
		{fmt.Errorf("%w: p", ErrAgentNeedsProvider),
			"the Judge's provider is not configured; give the Judge agent a configured provider and model"},
		{fmt.Errorf("%w: a", ErrAgentModelUnassigned),
			"the Judge has no model assigned; assign one on the Judge agent"},
		{fmt.Errorf("%w: m", ErrContextWindowUnknown),
			"the Judge's model reports no context window; set a context-window override for it"},
		{&common.ProviderError{Status: 401, Body: opFixRawBody},
			"the provider rejected the Judge's credentials; update the key under Settings → Providers"},
		{fmt.Errorf("x: %w", ErrAgentNotWorkspaceMember),
			"the Judge agent is not on any workspace team; add it to one"},
		{fmt.Errorf("x: %w", ErrAgentHomeUnavailable),
			"the Judge's working folder could not be opened; check that the disk has space and the folder is writable"},
	}
	for _, tc := range cases {
		_, msg, needs := judgeDispatchNeedsOperator(tc.err)
		if !needs || msg != tc.want {
			t.Errorf("judgeDispatchNeedsOperator(%v) = (%q, %v), want (%q, true)", tc.err, msg, needs, tc.want)
		}
	}
	if _, msg, needs := judgeDispatchNeedsOperator(&common.ProviderError{Status: 429, Body: "slow down"}); needs || msg != "" {
		t.Errorf("a rate limit must keep the Judge's retry backoff, got (%q, %v)", msg, needs)
	}
}

// TestTaskOperatorFixText_NamesTheFixForEveryCause: every operator-only cause
// renders a plain reason that says the task could not run, names what to
// change, and ends by telling the operator to run the task again.
func TestTaskOperatorFixText_NamesTheFixForEveryCause(t *testing.T) {
	for cause, fix := range map[operatorFixCause]string{
		operatorFixCredentialsRejected:   "Settings → Providers",
		operatorFixSignInExpired:         "Settings → Providers",
		operatorFixProviderNotConfigured: "Settings → Providers",
		operatorFixModelUnassigned:       "agent's settings",
		operatorFixContextWindowUnknown:  "Settings → Models → Model overrides → Context length",
		operatorFixAgentNotOnWorkspace:   "workspace team",
		operatorFixWorkDirUnavailable:    "disk has space",
	} {
		for _, names := range [][3]string{{"Native Agent", "acme-llm", "test-model"}, {"", "", ""}} {
			got := taskOperatorFixText(cause, names[0], names[1], names[2])
			if !strings.HasPrefix(got, "The task could not run: ") || !strings.Contains(got, fix) ||
				!strings.HasSuffix(got, ", then run the task again.") {
				t.Errorf("cause %d with names %q: %q does not state the failure, the fix (%q) and the re-run", cause, names, got, fix)
			}
			if strings.Contains(got, "  ") || strings.Contains(got, " ,") || strings.Contains(got, " .") {
				t.Errorf("cause %d with names %q: %q has a gap where a name was left out", cause, names, got)
			}
		}
	}
}
