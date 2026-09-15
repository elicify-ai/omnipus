// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_assignee_readiness_test.go pins the founder decision of 2026-09-15 for
// the pre-run check (task_assignee_readiness.go):
//
//   - a task whose agent cannot finish it — a native worker denied goal_claim,
//     or a check the agent's bash policy cannot run — ends Failed BEFORE its
//     first turn: the founder's plain message naming the fix, no attempt used,
//     no restart, no model call, no Judge call, exactly one goal outcome line;
//   - an agent that can finish the task runs it normally;
//   - the agent task tools, through the loop's real wiring, refuse the same
//     assignment;
//   - the check-runner rule is the Judge's own, so the two cannot disagree.
//
// Oracles are the decision's observable outcomes and the founder's wording
// ("Worker isn't allowed to report tasks as done. Allow 'goal_claim' for it in
// Agents → Tools, or assign another agent."), with the harness agent's name.
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	nativeCannotClaimText = "Native Agent isn't allowed to report tasks as done. " +
		"Allow 'goal_claim' for it in Agents → Tools, or assign another agent."
	nativeCannotRunChecksAskText = "Native Agent can't run this task's checks. Checks run with nobody there to " +
		"approve them, so its 'bash' tool must be set to Allow, and it is set to Ask. " +
		"Allow 'bash' for it in Agents → Tools, or assign another agent."
)

// withAgentPolicies sets agentID's own (layer 2) tool policy entries.
func withAgentPolicies(agentID string, policies map[string]config.ToolPolicy) func(*config.Config) {
	return func(cfg *config.Config) {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				cfg.Agents.List[i].Tools = &config.AgentToolsCfg{
					Builtin: config.AgentBuiltinToolsCfg{Policies: policies},
				}
			}
		}
	}
}

func chainCfg(fns ...func(*config.Config)) func(*config.Config) {
	return func(cfg *config.Config) {
		for _, fn := range fns {
			fn(cfg)
		}
	}
}

func setCeiling(tool, policy string) func(*config.Config) {
	return func(cfg *config.Config) { cfg.Sandbox.ToolPolicies[tool] = policy }
}

// waitExecutorRunsDone blocks until every dispatch goroutine te tracks has
// finished, including a restart chain: consumeTaskAttempt's restart re-enters
// ExecuteTask, and so te.wg, from inside the ending run's own deferred cleanup,
// BEFORE that run releases its slot (TaskExecutor.wg's doc comment), so the
// counter never reads zero between a run ending and its restart starting.
// Unlike polling te.running — which is empty for exactly that instant — a
// "no restart" assertion made after this returns cannot race a restart. The
// caller must not start another dispatch while waiting.
func waitExecutorRunsDone(t *testing.T, te *TaskExecutor) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		te.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the task executor's runs did not finish within 15s")
	}
}

func newCheckTask(t *testing.T, al *AgentLoop) *task.Task {
	t.Helper()
	return createTaskWithGoal(t, al, &task.Task{
		Title: "report check", Prompt: "Write report.md.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default",
		Status:   task.StatusNext,
		Criteria: []task.AcceptanceCriterion{machineCriterion("", "test -f report.md", 0)},
	}, "no credentials appear in report.md")
}

// Given a task whose agent cannot finish it as configured
// When the task is dispatched
// Then it ends Failed before any turn, with the plain reason naming the fix, no
// attempt used, no restart, no model or Judge call, one goal outcome line, and
// the reason in the run's transcript.
func TestTaskRun_AssigneeCannotFinish_EndsFailedBeforeFirstTurn(t *testing.T) {
	cases := []struct {
		name      string
		mutateCfg func(*config.Config)
		checkTask bool
		want      string
	}{
		{
			name:      "goal_claim_explicitly_denied_for_the_agent",
			mutateCfg: withAgentPolicies("native-agent", map[string]config.ToolPolicy{"goal_claim": config.ToolPolicyDeny}),
			want:      nativeCannotClaimText,
		},
		{
			name:      "check_criterion_but_bash_is_ask",
			mutateCfg: setCeiling("bash", "ask"),
			checkTask: true,
			want:      nativeCannotRunChecksAskText,
		},
		{
			name: "both_at_once_names_both_fixes",
			mutateCfg: chainCfg(
				withAgentPolicies("native-agent", map[string]config.ToolPolicy{"goal_claim": config.ToolPolicyDeny}),
				setCeiling("bash", "ask")),
			checkTask: true,
			want:      nativeCannotClaimText + " " + nativeCannotRunChecksAskText,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			worker := &failingWorker{err: func() error { return errors.New("the worker's provider must not be reached") }}
			al, judgeInst := newGoalLoopTestLoop(t, worker, tc.mutateCfg)
			t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
			judge := &b6ScriptedJudge{metFromCall: 1, reason: "unused"}
			judgeInst.Provider = judge
			var tk *task.Task
			if tc.checkTask {
				tk = newCheckTask(t, al)
			} else {
				tk = newRunLoopTask(t, al, nil)
			}

			final := runTaskUntilTerminal(t, al, tk.ID, 1)

			if final.Status != task.StatusFailed {
				t.Fatalf("status = %q, want failed (result: %s)", final.Status, final.Result)
			}
			if final.Result != tc.want {
				t.Errorf("result = %q\nwant     %q", final.Result, tc.want)
			}
			if final.AttemptCount != 0 {
				t.Errorf("attempt_count = %d, want 0 — a task its agent cannot finish never used an attempt", final.AttemptCount)
			}
			if n := worker.callCount(); n != 0 {
				t.Errorf("worker provider calls = %d, want 0 — the run must not start", n)
			}
			if n := judge.callCount(); n != 0 {
				t.Errorf("Judge calls = %d, want 0", n)
			}

			// A restart would move the task back to next, mint a new session, use
			// an attempt and call the worker. Wait on the executor's own wait
			// group — a restart re-enters it before the ending run releases its
			// slot — rather than on a fixed sleep a slow machine can outrun.
			waitExecutorRunsDone(t, al.taskExecutor)
			after, err := al.taskStore.Get(tk.ID)
			if err != nil {
				t.Fatalf("re-read task: %v", err)
			}
			if after.Status != task.StatusFailed || after.AttemptCount != 0 || after.SessionID != final.SessionID ||
				worker.callCount() != 0 {
				t.Errorf("the task restarted: status=%q attempt_count=%d session %q -> %q, worker calls=%d",
					after.Status, after.AttemptCount, final.SessionID, after.SessionID, worker.callCount())
			}

			rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
			store := al.GetAgentStore(tk.AgentID)
			waitForGoalOutcomeEntry(t, store, final.SessionID)
			e := requireOneGoalOutcome(t, store, final.SessionID, rec.GoalID)
			if !strings.Contains(e.Content, final.Result) {
				t.Errorf("outcome line %q does not carry the task's reason %q", e.Content, final.Result)
			}
			entries, rerr := store.ReadTranscript(final.SessionID)
			if rerr != nil {
				t.Fatalf("read transcript: %v", rerr)
			}
			sawReason := false
			for _, en := range entries {
				if en.Status == "error" && en.Content == final.Result {
					sawReason = true
				}
			}
			if !sawReason {
				t.Errorf("the run's transcript has no error entry carrying the reason %q", final.Result)
			}
		})
	}
}

// Given an agent that is allowed to claim and a prose-judged task
// When the task is dispatched
// Then it runs normally: the worker works and claims, the Judge upholds it, and
// the task is done with no attempt used.
func TestTaskRun_AssigneeCanFinish_RunsNormally(t *testing.T) {
	worker := newClaimingWorker(turnClaimMet("wrote report.md with all three quotes"))
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "ok"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)

	final := runTaskUntilTerminal(t, al, tk.ID, 1)

	if final.Status != task.StatusDone {
		t.Fatalf("status = %q, want done (result: %s)", final.Status, final.Result)
	}
	if worker.turnsStarted() != 1 || judge.callCount() != 1 || final.AttemptCount != 0 {
		t.Errorf("worker turns=%d Judge calls=%d attempt_count=%d, want 1/1/0",
			worker.turnsStarted(), judge.callCount(), final.AttemptCount)
	}
}

// TaskAssigneeCannotFinish answers per agent kind and per judged set.
func TestTaskAssigneeCannotFinish_Answers(t *testing.T) {
	prose := []task.AcceptanceCriterion{proseCriterion("p1", "the report reads well")}
	check := []task.AcceptanceCriterion{proseCriterion("p1", "the report reads well"), machineCriterion("c1", "test -f report.md", 0)}
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["bash"] = "deny"
		cfg.Agents.List = append(cfg.Agents.List,
			config.AgentConfig{
				ID: "no-claim", Name: "No Claim", Type: config.AgentTypeWorker, Home: t.TempDir(),
				Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
					Policies: map[string]config.ToolPolicy{"goal_claim": config.ToolPolicyDeny}}},
			},
			config.AgentConfig{
				ID: "ext-agent", Name: "External Agent", Type: config.AgentTypeWorker, Home: t.TempDir(),
				Subagents: &config.SubagentsConfig{
					Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
				},
				Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{
					Policies: map[string]config.ToolPolicy{"goal_claim": config.ToolPolicyDeny}}},
			},
			config.AgentConfig{
				ID: "bash-ok", Name: "Bash Ok", Type: config.AgentTypeWorker, Home: t.TempDir(),
			},
		)
	})
	// "bash-ok" holds bash allow on its OWN policy snapshot; the ceiling's deny
	// is replaced for this instance only.
	bashOK, ok := al.GetRegistry().GetAgent("bash-ok")
	if !ok {
		t.Fatal("bash-ok not registered")
	}
	bashOK.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		"bash": config.ToolPolicyAllow, "goal_claim": config.ToolPolicyAllow,
	}})

	denyChecks := func(name string) string {
		return name + " can't run this task's checks. Checks run with nobody there to approve them, so its 'bash' " +
			"tool must be set to Allow, and it is set to Deny. Allow 'bash' for it in Agents → Tools, or assign another agent."
	}
	cases := []struct {
		name, agent string
		judged      []task.AcceptanceCriterion
		want        string
	}{
		{"allowed_agent_prose_only", "native-agent", prose, ""},
		{"unknown_agent_is_left_to_the_other_gates", "no-such-agent", check, ""},
		{"unassigned", "", check, ""},
		{"denied_claim", "no-claim", prose, "No Claim isn't allowed to report tasks as done. " +
			"Allow 'goal_claim' for it in Agents → Tools, or assign another agent."},
		{"external_cli_skips_the_claim_part", "ext-agent", prose, ""},
		{"external_cli_still_needs_the_check_runner", "ext-agent", check, denyChecks("External Agent")},
		{"check_with_bash_denied", "native-agent", check, denyChecks("Native Agent")},
		{"check_with_bash_allowed", "bash-ok", check, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := al.TaskAssigneeCannotFinish(tc.agent, tc.judged); got != tc.want {
				t.Errorf("TaskAssigneeCannotFinish(%q) = %q\nwant %q", tc.agent, got, tc.want)
			}
		})
	}
}

// The check-runner rule and the Judge's runMachineCheck must agree for every
// policy: a check the Judge refuses to run is exactly a check the pre-run check
// reports.
func TestTaskReadiness_CheckRunnerPolicyMatchesTheJudge(t *testing.T) {
	for _, policy := range []config.ToolPolicy{config.ToolPolicyAllow, config.ToolPolicyAsk, config.ToolPolicyDeny} {
		t.Run(string(policy), func(t *testing.T) {
			al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
			inst, ok := al.GetRegistry().GetAgent("native-agent")
			if !ok {
				t.Fatal("native-agent not found")
			}
			inst.Tools.RegisterReplacing(&fakeBashTool{result: &tools.ToolResult{ForLLM: "ok"}})
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
				"bash": policy, "goal_claim": config.ToolPolicyAllow,
			}})
			c := machineCriterion("c1", "echo ok", 0)

			_, _, nv := al.runMachineCheck(context.Background(), "native-agent", c, 1, "t1", "")
			judgeRefuses := nv == NonVerdictUnableToVerify
			reported := al.TaskAssigneeCannotFinish("native-agent", []task.AcceptanceCriterion{c}) != ""

			if judgeRefuses != reported {
				t.Fatalf("policy %q: the Judge refuses to run the check = %v, the pre-run check reports it = %v",
					policy, judgeRefuses, reported)
			}
			if wantRefused := policy != config.ToolPolicyAllow; judgeRefuses != wantRefused {
				t.Fatalf("policy %q: Judge refuses = %v, want %v (ADR-049 D2 rule 2)", policy, judgeRefuses, wantRefused)
			}
		})
	}
}

// Given an agent denied goal_claim
// When it calls its own create_task tool — as the loop wires it — to assign
// itself a task
// Then the create is refused with the readiness reason and nothing is written.
func TestCreateTaskTool_RealWiring_RefusesAnAssigneeThatCannotFinish(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{},
		withAgentPolicies("native-agent", map[string]config.ToolPolicy{"goal_claim": config.ToolPolicyDeny}))
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not found")
	}
	createTool, ok := inst.Tools.Get("create_task")
	if !ok {
		t.Fatal("create_task is not registered for native-agent")
	}
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), "default")

	res := createTool.Execute(ctx, map[string]any{
		"title": "self-assigned", "prompt": "write report.md", "agent_id": "native-agent",
		"criteria": []any{map[string]any{"kind": "prose", "text": "report.md exists"}},
		"dod":      []any{map[string]any{"kind": "prose", "text": "no credentials appear in report.md"}},
	})

	if !res.IsError || res.ForLLM != nativeCannotClaimText {
		t.Fatalf("create_task result = (error=%v) %q, want the refusal %q", res.IsError, res.ForLLM, nativeCannotClaimText)
	}
	var refusal *tools.AssigneeCannotFinishError
	if !errors.As(res.Err, &refusal) || refusal.Field != "agent_id" {
		t.Fatalf("the refusal must carry the structured error naming agent_id, got %v", res.Err)
	}
	all, err := al.taskStore.List(task.Filter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("a refused create wrote %d task(s)", len(all))
	}
}
