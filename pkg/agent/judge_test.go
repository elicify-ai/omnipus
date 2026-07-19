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
// tests can script different responses per attempt.
type fakeJudgeProvider struct {
	mu     sync.Mutex
	calls  int
	chatFn func(callNum int) (*providers.LLMResponse, error)
}

func (f *fakeJudgeProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	return f.chatFn(n)
}

func (f *fakeJudgeProvider) GetDefaultModel() string { return "fake-judge-model" }

func (f *fakeJudgeProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
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
			Defaults: config.AgentDefaults{Home: t.TempDir(), ModelName: "test-model"},
			List: []config.AgentConfig{
				{ID: "native-agent", Name: "Native Agent", Type: config.AgentTypeWorker, Home: workspace},
				{
					ID:   string(coreagent.IDJudge),
					Name: "Judge",
					Type: config.AgentTypeSystem,
					Home: judgeHome,
					Rubric: `Return ONLY valid JSON of the shape ` +
						`{"met": <bool>, "criteria": [{"id": "<id>", "met": <bool>, "reason": "<why>"}]}`,
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
	workerInst.Tools.Register(fakeBash) // overwrites the real ExecTool entry
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
			workerInst.Tools.Register(fakeBash)
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
			if result.Unavailable {
				t.Fatalf("unexpected Unavailable: %s", result.Reason)
			}
			gotRan := fakeBash.callCount() == 1
			if gotRan != tc.wantRuns {
				t.Errorf("bash tool called %d times, want ran=%v (policy %q)", fakeBash.callCount(), tc.wantRuns, tc.policy)
			}
			if !tc.wantRuns && result.Verdict.Met {
				t.Errorf("policy %q must fail closed (met=false), got met=true", tc.policy)
			}
			if tc.wantRuns && !result.Verdict.Met {
				t.Errorf("policy allow with matching exit code must be met=true, got %+v", result.Verdict.PerCriterion)
			}
			if !tc.wantRuns && !strings.Contains(result.Verdict.PerCriterion[0].Reason, string(tc.policy)) &&
				tc.policy != config.ToolPolicyAsk {
				t.Errorf("reason %q should mention the denying policy", result.Verdict.PerCriterion[0].Reason)
			}
		})
	}
}

// --- timeout / exit-code classification ----------------------------------

func TestJudge_MachineCheck_TimeoutClassifiedFailedClosed(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	workerInst, _ := al.GetRegistry().GetAgent("native-agent")
	fakeBash := &fakeBashTool{result: &tools.ToolResult{
		ForLLM:  "Command timed out after 60 seconds",
		IsError: true,
	}}
	workerInst.Tools.Register(fakeBash)
	allowBashPolicy(workerInst)

	result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
		Scope:           task.VerdictScopeTask,
		TaskID:          "t-timeout",
		AssigneeAgentID: "native-agent",
		Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "sleep 100", 0)},
		Attempt:         1,
	})
	if result.Verdict.Met {
		t.Fatal("a timed-out check must be unmet (fail-closed, D2 rule 4)")
	}
	if !strings.Contains(result.Verdict.PerCriterion[0].Reason, "timed out") {
		t.Errorf("reason = %q, want it to mention the timeout", result.Verdict.PerCriterion[0].Reason)
	}
}

func TestJudge_MachineCheck_ExitCodeClassification(t *testing.T) {
	cases := []struct {
		name         string
		forLLM       string
		isError      bool
		expectedExit int
		wantMet      bool
	}{
		{"exit_zero_no_suffix_is_met", "all good", false, 0, true},
		{"exit_one_matches_expected_nonzero", "boom\n\n[Command exited with code 1]", true, 1, true},
		{"exit_one_mismatches_expected_zero", "boom\n\n[Command exited with code 1]", true, 0, false},
		{"blocked_before_running_fails_closed", "Command blocked by safety guard", true, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			workerInst, _ := al.GetRegistry().GetAgent("native-agent")
			fakeBash := &fakeBashTool{result: &tools.ToolResult{ForLLM: tc.forLLM, IsError: tc.isError}}
			workerInst.Tools.Register(fakeBash)
			allowBashPolicy(workerInst)

			result := al.JudgeCriteria(context.Background(), JudgeCriteriaInput{
				Scope:           task.VerdictScopeTask,
				TaskID:          "t-" + tc.name,
				AssigneeAgentID: "native-agent",
				Criteria:        []task.AcceptanceCriterion{machineCriterion("c1", "x", tc.expectedExit)},
				Attempt:         1,
			})
			if got := result.Verdict.PerCriterion[0].Met; got != tc.wantMet {
				t.Errorf("met = %v, want %v (reason: %s)", got, tc.wantMet, result.Verdict.PerCriterion[0].Reason)
			}
		})
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
	workerInst.Tools.Register(fakeBash)
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
