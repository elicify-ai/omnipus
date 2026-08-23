// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// judge_test.go covers the evidence-ladder judge (judge.go) at the
// AgentLoop.JudgeCriteria unit level — machine-check dispatch through the
// assignee's own bash tool machinery, the policy triad, timeout/exit-code
// classification, prose-judge fail-closed behavior, and judge-unavailability
// backoff (D7) — without needing a full task dispatch.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// --- fakes -------------------------------------------------------------

// fakeBashTool is a minimal tools.Tool double registered under the name
// "bash" so tests can prove the judge's machine checks dispatch through the
// SAME ToolRegistry.ExecuteWithContext path every other bash call in the
// system uses (D2 rule 1) — observed here via callCount/lastArgs — rather
// than a parallel/raw exec.Command path (which this fake makes structurally
// impossible to reach: there is no other way for JudgeCriteria to produce a
// result at all in these tests except by calling this registered tool).
type fakeBashTool struct {
	mu       sync.Mutex
	calls    int
	lastArgs map[string]any
	result   *tools.ToolResult
}

func (f *fakeBashTool) Name() string                 { return "bash" }
func (f *fakeBashTool) Description() string          { return "fake bash for tests" }
func (f *fakeBashTool) Parameters() map[string]any   { return nil }
func (f *fakeBashTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (f *fakeBashTool) Category() tools.ToolCategory { return tools.CategoryShell }

func (f *fakeBashTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	f.mu.Lock()
	f.calls++
	f.lastArgs = args
	f.mu.Unlock()
	return f.result
}

func (f *fakeBashTool) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeJudgeProvider is a providers.LLMProvider double for the Judge's own
// no-tools structured LLM call. chatFn receives the 1-based call number so
// tests can script different responses per attempt. lastCtx records the ctx
// of the most recent Chat call so a test can assert what the engine plumbed
// onto the verifier's turn (e.g. tools.WithVerifierSessionScope, ADR-052
// FR-033) actually reached the LLM call.
type fakeJudgeProvider struct {
	mu      sync.Mutex
	calls   int
	lastCtx context.Context
	chatFn  func(callNum int) (*providers.LLMResponse, error)
}

func (f *fakeJudgeProvider) Chat(
	ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.lastCtx = ctx
	f.mu.Unlock()
	return f.chatFn(n)
}

func (f *fakeJudgeProvider) GetDefaultModel() string { return "fake-judge-model" }

func (f *fakeJudgeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeJudgeProvider) capturedCtx() context.Context {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastCtx
}

// newGoalLoopTestLoop builds an AgentLoop with one native worker agent
// ("native-agent") and a Judge System Agent (coreagent.IDJudge, Type=system)
// resolvable via al.GetRegistry().GetAgent. mutateCfg (optional, may be nil)
// is applied to the config before the loop is constructed, letting a test
// configure e.g. cfg.Sandbox.RateLimits for the SEC-26 tests.
func newGoalLoopTestLoop(
	t *testing.T, workerProvider providers.LLMProvider, mutateCfg func(*config.Config),
) (*AgentLoop, *AgentInstance) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := t.TempDir()
	judgeHome := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeWorker, Home: workspace},
				{
					ID:   string(coreagent.IDJudge),
					Name: "Judge",
					Type: config.AgentTypeSystem,
					Home: judgeHome,
					// No Rubric field anymore (ADR-052 FR-038 deleted it —
					// one unified soul concept). ensureVerifierSoul
					// (verifier_adjudication.go) lazily seeds
					// coreagent.JudgeDefaultRubric into judgeHome/SOUL.md
					// on first real verifier dispatch, which already
					// includes the same "Return ONLY valid JSON..."
					// instruction this literal used to carry.
				},
			},
		},
	}
	if mutateCfg != nil {
		mutateCfg(cfg)
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), workerProvider)
	t.Cleanup(func() { al.Close() })

	judgeInst, ok := al.GetRegistry().GetAgent(string(coreagent.IDJudge))
	if !ok {
		t.Fatal("judge agent not registered")
	}
	return al, judgeInst
}

// allowBashPolicy grants agentInst's "bash" tool policy "allow" — needed
// since these minimal test configs have no policy entries at all, which
// resolves to "deny" fail-closed (CLAUDE.md hard constraint 6: no default
// policy fallback).
func allowBashPolicy(agentInst *AgentInstance) {
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"bash": config.ToolPolicyAllow},
	})
}

func machineCriterion(id, command string, expectedExit int) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		ID:     id,
		Kind:   task.KindCheck,
		Text:   "machine check " + id,
		Check:  &task.CriterionCheck{Command: command, ExpectedExitCode: expectedExit},
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
	}
}

func proseCriterion(id, text string) task.AcceptanceCriterion {
	return task.AcceptanceCriterion{
		ID:     id,
		Kind:   task.KindProse,
		Text:   text,
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "native-agent"},
	}
}

// --- machine checks: dispatch via the tool registry (D2 rule 1) ---------

func TestJudge_MachineCheck_DispatchesViaToolRegistry_NotRawExec(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found")
	}
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}}
	workerInst.Tools.RegisterReplacing(fakeBash) // overwrites the real ExecTool entry
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t1",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "echo ok", 0)},
		Attempt:         1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true (per-criterion: %+v)", result.Verdict.PerCriterion)
	}
	if fakeBash.callCount() != 1 {
		t.Fatalf("fake bash tool called %d times, want exactly 1 — the machine check must dispatch "+
			"through the assignee's registered tool, never a parallel exec path", fakeBash.callCount())
	}
	if fakeBash.lastArgs["command"] != "echo ok" {
		t.Errorf("bash args command = %v, want %q", fakeBash.lastArgs["command"], "echo ok")
	}
}

// --- policy triad (D2 rule 2) -------------------------------------------

func TestJudge_MachineCheck_PolicyTriad(t *testing.T) {
	cases := []struct {
		name     string
		policy   config.ToolPolicy
		wantRuns bool
	}{
		{"allow_runs_via_bash_machinery", config.ToolPolicyAllow, true},
		{"ask_resolves_to_deny_unattended", config.ToolPolicyAsk, false},
		{"deny", config.ToolPolicyDeny, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			workerInst, _ := al.GetRegistry().GetAgent("native-agent")
			fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}}
			workerInst.Tools.RegisterReplacing(fakeBash)
			workerInst.StoreToolPolicy(&tools.ToolPolicyCfg{
				Policies: map[string]config.ToolPolicy{"bash": tc.policy},
			})

			result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
				Scope:           task.VerdictScopeTask,
				TaskID:          "t-" + tc.name,
				AssigneeAgentID: "native-agent",
				Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "echo ok", 0)},
				Attempt:         1,
			})
			gotRan := fakeBash.callCount() == 1
			if gotRan != tc.wantRuns {
				t.Errorf("bash tool called %d times, want ran=%v (policy %q)", fakeBash.callCount(), tc.wantRuns, tc.policy)
			}
			if tc.wantRuns {
				// allow: the mechanism ran and formed a real judgment.
				if result.Unavailable {
					t.Fatalf("policy allow: unexpected Unavailable: %s", result.Reason)
				}
				if !result.Verdict.Met {
					t.Errorf("policy allow with matching exit code must be met=true, got %+v", result.Verdict.PerCriterion)
				}
			} else {
				// ask/deny (G-3/FR-116): the bash MECHANISM could not run under
				// the agent's own policy → unable_to_verify → Unavailable (round
				// not consumed, re-run), NEVER scored as absent evidence. The
				// old path fail-closed this to unmet (the blind-judge bug).
				if !result.Unavailable {
					t.Fatalf("policy %q: want Unavailable (unable_to_verify), got a verdict", tc.policy)
				}
				if !strings.Contains(result.Reason, "unable_to_verify") {
					t.Errorf("policy %q: reason %q should mention unable_to_verify", tc.policy, result.Reason)
				}
			}
		})
	}
}

// --- timeout / exit-code classification ----------------------------------

func TestJudge_MachineCheck_TimeoutClassifiedUnableToVerify(t *testing.T) {
	// G-3/FR-116 (blocked-check honesty): a timeout means the verification
	// MECHANISM did not run to completion (the command was killed before it
	// could produce a readable exit code) → unable_to_verify → Unavailable
	// (round not consumed, re-run), NEVER scored as absent evidence. The old
	// path fail-closed this to unmet (D2 rule 4) — the silent blind-judge bug.
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{
		ForLLM:  "Command timed out after 60 seconds",
		IsError: true,
	}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-timeout",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "sleep 100", 0)},
		Attempt:         1,
	})
	if !result.Unavailable {
		t.Fatalf("a timed-out check must be unable_to_verify (Unavailable), got verdict met=%v", result.Verdict.Met)
	}
	if !strings.Contains(result.Reason, "unable_to_verify") {
		t.Errorf("reason = %q, want it to mention unable_to_verify", result.Reason)
	}
}

// TestJudge_MachineCheck_TimedOutField_IsAuthoritative is the fix-wave
// regression for finding 4 (14-reviewer sign-off): interpretBashResult must
// key its timeout classification on the STRUCTURED ToolResult.TimedOut
// field, never on a prose sniff over worker-controllable output. Pre-fix,
// the exact-marker sniff ran unconditionally BEFORE the IsError check, so a
// check that genuinely exited 0 (a real pass) whose own output happened to
// CONTAIN the timeout marker text — e.g. a log line narrating an earlier,
// unrelated retry that itself timed out — was misclassified
// unable_to_verify: a passing check silently blocked from ever being scored
// MET, resurfacing every round until the tracker's K-3 cap turned it into a
// permanent unmet.
func TestJudge_MachineCheck_TimedOutField_IsAuthoritative(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{
		ForLLM:  "retry log: command timed out after 5 seconds on attempt 1, succeeded on retry\n\n[Command exited with code 0]",
		IsError: false, // the REAL command genuinely exited 0 — TimedOut left at its zero value
	}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-timeout-prose-false-positive",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "x", 0)},
		Attempt:         1,
	})
	if result.Unavailable {
		t.Fatalf("a genuinely passing (exit 0, TimedOut=false) check must not be misclassified "+
			"unable_to_verify just because its own output mentions timeout prose: %s", result.Reason)
	}
	if !result.Verdict.PerCriterion[0].Met {
		t.Errorf("met = false, want true (reason: %s)", result.Verdict.PerCriterion[0].Reason)
	}
}

func TestJudge_MachineCheck_ExitCodeClassification(t *testing.T) {
	cases := []struct {
		name            string
		forLLM          string
		isError         bool
		expectedExit    int
		wantMet         bool // valid when !wantUnavailable
		wantUnavailable bool // G-3: unreadable exit → unable_to_verify, re-run
	}{
		{"exit_zero_no_suffix_is_met", "all good", false, 0, true, false},
		{"exit_one_matches_expected_nonzero", "boom\n\n[Command exited with code 1]", true, 1, true, false},
		{"exit_one_mismatches_expected_zero", "boom\n\n[Command exited with code 1]", true, 0, false, false},
		// G-3/FR-137 (blocked-check honesty): a blocked-before-running result
		// (no readable exit code) is unable_to_verify → Unavailable (re-run),
		// NEVER scored as absent evidence. The old path fail-closed to unmet.
		{"blocked_before_running_unable_to_verify", "Command blocked by safety guard", true, 0, false, true},
		// review r1 M1 (exit-code spoof, CRITICAL): the real command failed
		// (IsError=true) but its own stdout embeds a fake
		// "[Command exited with code 0]" suffix trying to spoof success. With
		// no structured ExitCode field, IsError=true and a zero suffix cannot
		// be trusted → exit code unreadable → unable_to_verify (the mechanism
		// could not form a machine-checkable judgment). This is MORE honest
		// than the old fail-closed-unmet: we genuinely cannot determine the
		// exit, so it re-runs (bounded by the tracker) rather than silently
		// scoring absent evidence.
		{
			"spoofed_exit_zero_with_iserror_true_unable_to_verify",
			"all good, definitely\n\n[Command exited with code 0]", true, 0, false, true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			workerInst, _ := al.GetRegistry().GetAgent("native-agent")
			fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: tc.forLLM, IsError: tc.isError}}
			workerInst.Tools.RegisterReplacing(fakeBash)
			allowBashPolicy(workerInst)

			result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
				Scope:           task.VerdictScopeTask,
				TaskID:          "t-" + tc.name,
				AssigneeAgentID: "native-agent",
				Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "x", tc.expectedExit)},
				Attempt:         1,
			})
			if tc.wantUnavailable {
				if !result.Unavailable {
					t.Fatalf("want Unavailable (unable_to_verify), got verdict met=%v", result.Verdict.Met)
				}
				if !strings.Contains(result.Reason, "unable_to_verify") {
					t.Errorf("reason %q should mention unable_to_verify", result.Reason)
				}
				return
			}
			if result.Unavailable {
				t.Fatalf("unexpected Unavailable: %s", result.Reason)
			}
			if got := result.Verdict.PerCriterion[0].Met; got != tc.wantMet {
				t.Errorf("met = %v, want %v (reason: %s)", got, tc.wantMet, result.Verdict.PerCriterion[0].Reason)
			}
		})
	}
}

// TestJudge_MachineCheck_LargeOutputTruncationSpoof_FailsClosed is the review
// r2 HIGH-1 regression test. The r1 exit-code fix (spoofed_exit_zero_...
// above) assumed ExecTool always appends its real "[Command exited with code
// N]" suffix LAST in ForLLM, but shell.go's foreground paths actually append
// it BEFORE truncateOutput's head-first truncation — on a large output the
// real (authoritative) suffix, positioned at the very end of the
// pre-truncation text, gets cut away entirely while an earlier,
// worker-embedded fake suffix survives inside the visible (truncated) text.
// Before the fix, that fake suffix would become the "last occurrence" a
// regex scan finds, spoofing a MET verdict for a criterion expecting a
// non-zero code. This test constructs a ToolResult shaped exactly like what
// shell.go now produces for that scenario — ExitCode set structurally to
// the REAL exit code (7, truncation-immune), ForLLM containing a fake
// "[Command exited with code 5]" suffix within the visible >10k-char text
// but NOT the real one (truncated away) — and asserts the judge trusts the
// structured field, not the misleading text.
func TestJudge_MachineCheck_LargeOutputTruncationSpoof_FailsClosed(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")

	// The criterion expects exit code 5. A fake suffix claiming EXACTLY that
	// expected code is embedded early in a >10k-char output, trying to spoof
	// a MET verdict — the real suffix (for the ACTUAL exit code, 7) is
	// absent from ForLLM entirely, exactly as it would be after shell.go's
	// maxForegroundOutputLen truncation cut it away. Pre-fix, the regex
	// fallback would find the fake "[Command exited with code 5]" as the
	// only (and therefore "last") match and wrongly report met=true (5==5).
	const expectedExit = 5
	const realExitCode = 7 // the command's ACTUAL exit code — does NOT match expected
	fakeSuffix := "[Command exited with code 5]"
	// Realistic large log output (words + spaces + newlines) rather than an
	// unbroken character run — a long run of base64-alphabet characters with
	// almost no whitespace would trip pkg/tools/normalization.go's unrelated
	// looksLikeLargeBase64Payload heuristic and replace ForLLM entirely
	// before it ever reaches interpretBashResult, which is not what this
	// test is exercising.
	padding := strings.Repeat("line of ordinary command output text\n", 500)
	forLLM := fakeSuffix + "\n" + padding
	realExitCodeVal := realExitCode

	fakeBash := &fakeBashTool{result: &tools.ToolResult{
		ForLLM:   forLLM,
		IsError:  true,
		ExitCode: &realExitCodeVal,
	}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-truncation-spoof",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "x", expectedExit)},
		Attempt:         1,
	})

	if result.Verdict.PerCriterion[0].Met {
		t.Fatal("met = true, want false — the judge must trust the structured ExitCode (7, real) " +
			"over a spoofed fake suffix (5, matching the expected code) surviving in truncated text")
	}
	reason := result.Verdict.PerCriterion[0].Reason
	if !strings.Contains(reason, "7") {
		t.Errorf("reason = %q, want it to cite the REAL exit code (7) from the structured field, "+
			"not the spoofed fake (5) found in truncated text", reason)
	}
}

// --- prose judge fail-closed ----------------------------------------------

func TestJudge_ProseJudge_UnevidencedClaimFailsClosed(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{
			Content: `{"met": false, "criteria": [{"id":"c1","met":false,"reason":"no evidence for this claim"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-prose",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "the feature works")},
		Attempt:         1,
		ClaimText:       "I did it, trust me.",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("an unevidenced prose claim must score unmet (OBS-003)")
	}
	if fake.callCount() != 1 {
		t.Errorf("judge LLM called %d times, want exactly 1", fake.callCount())
	}
}

func TestJudge_ProseJudge_MalformedResponse_FailsClosedNotUnavailable(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return &providers.LLMResponse{Content: "not json at all, sorry"}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-malformed",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	// A malformed-but-SUCCESSFUL LLM call is "ran but produced no valid
	// verdict" (NFR-2 fail-closed unmet) — NOT "unavailable" (D7). The
	// distinction matters: this consumes the attempt, D7 unavailability does
	// not.
	if result.Unavailable {
		t.Fatalf("a malformed-but-successful judge call must not be Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("a malformed judge response must fail closed to unmet")
	}
}

func TestJudge_ProseJudge_MissingCriterionInResponse_FailsClosed(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		// Judge's response omits "c2" entirely.
		return &providers.LLMResponse{
			Content: `{"met": true, "criteria": [{"id":"c1","met":true,"reason":"evidenced"}]}`,
		}, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-missing",
		AssigneeAgentID: "native-agent",
		Criteria: []task.AcceptanceCriterion{
			proseCriterion("c1", "criterion one"),
			proseCriterion("c2", "criterion two"),
		},
		Attempt:   1,
		ClaimText: "both done",
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("overall verdict must be unmet when the judge omits a criterion (fail-closed, NFR-2)")
	}
	var c2 *task.CriterionVerdict
	for i := range result.Verdict.PerCriterion {
		if result.Verdict.PerCriterion[i].CriterionID == "c2" {
			c2 = &result.Verdict.PerCriterion[i]
		}
	}
	if c2 == nil || c2.Met {
		t.Errorf("criterion c2 (omitted by the judge) must be recorded as unmet, got %+v", c2)
	}
}

// --- judge unavailability (D7): attempt not consumed ----------------------

func TestJudge_Unavailable_ProviderError_RetriesWithBackoff(t *testing.T) {
	origBackoff := judgeRetryBackoff
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeRetryBackoff = origBackoff; judgeSleepFn = origSleep })
	judgeRetryBackoff = []time.Duration{60 * time.Second, 120 * time.Second, 300 * time.Second}

	var mu sync.Mutex
	var recorded []time.Duration
	judgeSleepFn = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		recorded = append(recorded, d)
		n := len(recorded)
		mu.Unlock()
		if n >= 3 {
			return errors.New("test: giving up after 3 backoff waits")
		}
		return nil // no real sleep
	}

	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		return nil, errors.New("simulated provider outage")
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-unavail",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if !result.Unavailable {
		t.Fatalf("expected Unavailable=true when the judge keeps failing, got verdict=%+v", result.Verdict)
	}
	if result.Verdict != nil {
		t.Error("no verdict must be recorded when the judge is unavailable (NFR-2/D7)")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{60 * time.Second, 120 * time.Second, 300 * time.Second}
	if len(recorded) != len(want) {
		t.Fatalf("recorded %d backoff waits, want %d: %v", len(recorded), len(want), recorded)
	}
	for i, d := range want {
		if recorded[i] != d {
			t.Errorf("backoff[%d] = %v, want %v", i, recorded[i], d)
		}
	}
	if fake.callCount() != 3 {
		t.Errorf("judge LLM called %d times, want 3 (one per backoff cycle)", fake.callCount())
	}
}

func TestJudge_Unavailable_SEC26RateLimited_NotAttemptConsuming(t *testing.T) {
	origSleep := judgeSleepFn
	t.Cleanup(func() { judgeSleepFn = origSleep })
	var mu sync.Mutex
	var recorded []time.Duration
	judgeSleepFn = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		recorded = append(recorded, d)
		mu.Unlock()
		return errors.New("test: give up after first backoff")
	}

	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour = 1
	})

	// Pre-consume the Judge's single allowed slot for this hour, so the
	// runtime's OWN internal SEC-26 check inside JudgeCriteria is denied.
	allowed, _, reason := al.checkJudgeSEC26("system", string(coreagent.IDJudge))
	if !allowed {
		t.Fatalf("pre-consuming check unexpectedly denied: %s", reason)
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-sec26",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
		ClaimText:       "done",
	})
	if !result.Unavailable {
		t.Fatalf("expected Unavailable=true when SEC-26 denies the judge's LLM call, got verdict=%+v", result.Verdict)
	}
	if result.Verdict != nil {
		t.Error("no verdict must be recorded when SEC-26 denies the call (D7 unavailability, not fail-closed unmet)")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 || recorded[0] != judgeRetryBackoff[0] {
		t.Errorf("recorded backoffs = %v, want exactly one entry of %v", recorded, judgeRetryBackoff[0])
	}
}

// --- machine-only criteria never call the Judge LLM at all ----------------

func TestJudge_AllMachineCriteria_NeverCallsJudgeLLM(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("the Judge LLM must never be called when all criteria are machine-checkable")
		return nil, nil
	}}
	judgeInst.Provider = fake

	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}}
	workerInst.Tools.RegisterReplacing(fakeBash)
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-machine-only",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "echo ok", 0)},
		Attempt:         1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if !result.Verdict.Met {
		t.Fatalf("verdict.Met = false, want true, per-criterion: %+v", result.Verdict.PerCriterion)
	}
	if fake.callCount() != 0 {
		t.Errorf("judge LLM called %d times, want 0", fake.callCount())
	}
}

// --- unknown criterion kind fails closed, never dropped (review r1) -------

func TestJudge_UnknownCriterionKind_FailsClosed(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("the Judge LLM must never be called for an unknown-kind criterion (nothing to prose-judge)")
		return nil, nil
	}}
	judgeInst.Provider = fake

	unknown := task.AcceptanceCriterion{
		ID:     "c-unknown",
		Kind:   task.CriterionKind("mystery-kind"),
		Text:   "some future criterion kind this build doesn't understand yet",
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "tester"},
	}

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-unknown-kind",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{unknown},
		Attempt:         1,
	})
	if result.Unavailable {
		t.Fatalf("unexpected Unavailable: %s", result.Reason)
	}
	if result.Verdict.Met {
		t.Fatal("an unknown criterion kind must never be silently dropped from adjudication " +
			"(would let the overall verdict come back met=true, NFR-2 fail-closed violation)")
	}
	if len(result.Verdict.PerCriterion) != 1 {
		t.Fatalf("perCriterion has %d entries, want exactly 1 (the unknown-kind criterion must still "+
			"be recorded)", len(result.Verdict.PerCriterion))
	}
	if result.Verdict.PerCriterion[0].Met {
		t.Error("unknown-kind criterion must be recorded as unmet")
	}
	if fake.callCount() != 0 {
		t.Errorf("judge LLM called %d times, want 0", fake.callCount())
	}
}

// --- JudgeCriteriaInput.validate() (7-reviewer gate item 9) ---------------

func TestJudgeCriteriaInput_Validate(t *testing.T) {
	cases := []struct {
		name      string
		in        JudgeCriteriaInput
		wantValid bool
	}{
		{"task_scope_ok", JudgeCriteriaInput{Scope: task.VerdictScopeTask, TaskID: "t1"}, true},
		{"task_scope_missing_taskid", JudgeCriteriaInput{Scope: task.VerdictScopeTask}, false},
		{"task_scope_also_carries_planid", JudgeCriteriaInput{Scope: task.VerdictScopeTask, TaskID: "t1", PlanID: "p1"}, false},
		{"task_scope_also_carries_goalsession", JudgeCriteriaInput{Scope: task.VerdictScopeTask, TaskID: "t1", GoalSessionID: "s1"}, false},
		{"plan_scope_ok", JudgeCriteriaInput{Scope: task.VerdictScopePlan, PlanID: "p1"}, true},
		{"plan_scope_missing_planid", JudgeCriteriaInput{Scope: task.VerdictScopePlan}, false},
		{"plan_scope_also_carries_taskid", JudgeCriteriaInput{Scope: task.VerdictScopePlan, PlanID: "p1", TaskID: "t1"}, false},
		{"goal_scope_ok", JudgeCriteriaInput{Scope: task.VerdictScopeGoal, GoalSessionID: "s1"}, true},
		{"goal_scope_missing_sessionid", JudgeCriteriaInput{Scope: task.VerdictScopeGoal}, false},
		{"goal_scope_also_carries_planid", JudgeCriteriaInput{Scope: task.VerdictScopeGoal, GoalSessionID: "s1", PlanID: "p1"}, false},
		{"unknown_scope", JudgeCriteriaInput{Scope: "bogus", TaskID: "t1"}, false},
		{"empty_scope", JudgeCriteriaInput{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.validate()
			if tc.wantValid && got != "" {
				t.Errorf("validate() = %q, want valid (empty string)", got)
			}
			if !tc.wantValid && got == "" {
				t.Error("validate() = \"\", want a non-empty violation reason")
			}
		})
	}
}

// TestJudgeCriteria_InvalidInput_FailsClosedNotUnavailable proves the
// validate() call site in JudgeCriteria: a scope/id mismatch returns a real
// (Unavailable=false), fail-closed-unmet verdict — never Unavailable=true
// (which would tell a caller not to consume an attempt/round, incorrectly
// implying a transient condition worth retrying) — and never dispatches the
// Judge LLM at all.
func TestJudgeCriteria_InvalidInput_FailsClosedNotUnavailable(t *testing.T) {
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	fake := &fakeJudgeProvider{chatFn: func(int) (*providers.LLMResponse, error) {
		t.Fatal("the Judge LLM must never be called for an invalid JudgeCriteriaInput")
		return nil, nil
	}}
	judgeInst.Provider = fake

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		// Scope says task, but carries a PlanID instead of a TaskID —
		// exactly the "mismatched scope" shape validate() must reject.
		Scope:           task.VerdictScopeTask,
		PlanID:          "p1",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{proseCriterion("c1", "x")},
		Attempt:         1,
	})
	if result.Unavailable {
		t.Fatalf("an invalid input must be a real fail-closed verdict, not Unavailable: %s", result.Reason)
	}
	if result.Verdict == nil || result.Verdict.Met {
		t.Fatalf("expected a non-nil, unmet verdict, got %+v", result.Verdict)
	}
	if len(result.Verdict.PerCriterion) != 1 || result.Verdict.PerCriterion[0].Met {
		t.Fatalf("expected exactly one unmet per-criterion entry, got %+v", result.Verdict.PerCriterion)
	}
	if fake.callCount() != 0 {
		t.Errorf("judge LLM called %d times, want 0", fake.callCount())
	}
}
