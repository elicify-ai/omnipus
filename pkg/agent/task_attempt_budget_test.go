// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_attempt_budget_test.go pins the founder decisions of 2026-09-14 (issue
// #710) on the two limits a task runs under. They are different things and
// must never be mixed up:
//
//   - Settings -> Tries per goal (goal_max_rounds) bounds how many tries a goal
//     gets — a goal set in chat AND the goal a task run works toward — and
//     "goals already running keep the limit they started with";
//   - the task attempt limit (per-task max_attempts, else the config-only
//     planning.task_max_attempts, default 3) bounds how many RUNS a task gets:
//     a run whose goal ends not met after all its tries fails as a whole and
//     the task restarts in a fresh run.
//
// Every expected number below is derived from the values the test itself
// configures, never read back from the implementation. The task cases drive
// real ExecuteTask dispatches (a worker turn and a Judge turn per try); only the
// LLM replies are canned.

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	tryLimitCriterionText = "the invoice report lists every open invoice"
	tryLimitDoDText       = "no credentials appear anywhere in the report"
	tryLimitEvidence      = "opened the report and checked every invoice against the ledger"
)

// tryLimitJudge answers every verifier call UNMET for the acceptance criterion
// (perCriterionJudgeProvider, keyed by criterion text), so a task never
// completes and its limits are the only thing that can stop it. It counts its
// calls — one per judged try — and runs onFirstCall exactly once, before
// answering the first call, i.e. after the task's run has started and its goal
// record has been activated.
type tryLimitJudge struct {
	inner       *perCriterionJudgeProvider
	once        sync.Once
	onFirstCall func()
	mu          sync.Mutex
	calls       int
}

func newTryLimitJudge(onFirstCall func()) *tryLimitJudge {
	return &tryLimitJudge{
		inner:       &perCriterionJudgeProvider{unmet: map[string]bool{tryLimitCriterionText: true}},
		onFirstCall: onFirstCall,
	}
}

func (j *tryLimitJudge) Chat(
	ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any,
) (*providers.LLMResponse, error) {
	j.once.Do(func() {
		if j.onFirstCall != nil {
			j.onFirstCall()
		}
	})
	j.mu.Lock()
	j.calls++
	j.mu.Unlock()
	return j.inner.Chat(ctx, msgs, defs, model, opts)
}

func (j *tryLimitJudge) GetDefaultModel() string { return "fake-judge-model" }

func (j *tryLimitJudge) callCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.calls
}

// newTryLimitLoop builds the goal-loop harness with the goal try limit set to
// tries and the global task attempt limit set to taskAttempts; its worker claims
// through goal_claim on every try. The agent home base is rooted under
// OMNIPUS_HOME because create_task resolves its workspace against
// filepath.Dir(agent home base), which must be the test home where the
// harness's membership workspace lives.
func newTryLimitLoop(t *testing.T, tries, taskAttempts int) (*AgentLoop, *AgentInstance, *claimingWorker) {
	t.Helper()
	worker := newClaimingWorker(turnClaimMet(tryLimitEvidence))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = tries
		cfg.Planning.TaskMaxAttempts = taskAttempts
		cfg.Agents.Defaults.Home = filepath.Join(config.OmnipusHomeDir(), "agents")
	})
	return al, judgeInst, worker
}

// createTaskThroughAgentTool creates a task exactly as an agent does: through
// the create_task tool REGISTERED on the agent by registerSharedTools — so the
// production SetGoalMaxRoundsFn wiring in pkg/agent/loop.go is what decides the
// paired goal record's max_rounds.
func createTaskThroughAgentTool(t *testing.T, al *AgentLoop) string {
	t.Helper()
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok || inst == nil {
		t.Fatal("native-agent is not registered")
	}
	tool, ok := inst.Tools.Get("create_task")
	if !ok {
		t.Fatal("create_task is not registered on native-agent")
	}
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "native-agent"), testHarnessWorkspaceMembershipID)
	res := tool.Execute(ctx, map[string]any{
		"title":    "invoice report",
		"prompt":   "write the invoice report",
		"agent_id": "native-agent",
		"criteria": []any{map[string]any{"kind": "prose", "text": tryLimitCriterionText}},
		"dod":      []any{map[string]any{"kind": "prose", "text": tryLimitDoDText}},
	})
	if res == nil {
		t.Fatal("create_task returned no result")
	}
	if res.IsError {
		t.Fatalf("create_task failed: %s", res.ForLLM)
	}
	var out struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal([]byte(res.ForLLM), &out); err != nil || out.TaskID == "" {
		t.Fatalf("create_task result carries no task_id (err=%v): %s", err, res.ForLLM)
	}
	return out.TaskID
}

func pairedGoalRecord(t *testing.T, taskID string) *goal.Goal {
	t.Helper()
	g, err := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, taskID)
	if err != nil {
		t.Fatalf("read paired goal record for task %q: %v", taskID, err)
	}
	return g
}

// runTaskToTerminal dispatches taskID and waits for it to end. waitFor sizes the
// deadline for the LARGEST number of worker turns the case could produce if the
// code under test were wrong, so a regression fails on the count assertion
// rather than on a wait timeout.
func runTaskToTerminal(t *testing.T, al *AgentLoop, taskID string, waitFor int) *task.Task {
	t.Helper()
	if err := al.taskExecutor.ExecuteTask(context.Background(), taskID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	return t3WaitForTerminal(t, al, taskID, waitFor)
}

// An agent-created task under 3 tries per goal and 2 task attempts, every try
// judged unmet: its goal record says 3 from the moment it is created, each run
// spends exactly 3 tries, and the task fails after exactly 2 runs — 6 judged
// tries in all.
func TestTaskAttemptCeiling_AgentCreatedTaskFollowsBothLimits(t *testing.T) {
	const tries, attempts = 3, 2
	al, judgeInst, worker := newTryLimitLoop(t, tries, attempts)
	judge := newTryLimitJudge(nil)
	judgeInst.Provider = judge

	id := createTaskThroughAgentTool(t, al)
	if got := pairedGoalRecord(t, id).MaxRounds; got != tries {
		t.Fatalf("create_task wrote goal record max_rounds=%d, want %d: the agent tool must snapshot the "+
			"live goal try limit, not the shipped default", got, tries)
	}

	final := runTaskToTerminal(t, al, id, 3*tries*attempts)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed (every try was judged unmet)", final.Status)
	}
	if final.AttemptCount != attempts {
		t.Fatalf("AttemptCount = %d, want %d: the task must stop at its attempt limit, not at the try limit",
			final.AttemptCount, attempts)
	}
	if got := judge.callCount(); got != tries*attempts {
		t.Fatalf("Judge calls = %d, want %d: each of the %d runs must spend exactly %d tries", got, tries*attempts, attempts, tries)
	}
	if got := worker.turnsStarted(); got != tries*attempts {
		t.Fatalf("worker turns = %d, want %d", got, tries*attempts)
	}
	if want := fmt.Sprintf("(max %d)", attempts); !strings.Contains(final.Result, want) {
		t.Fatalf("handover must report the attempt limit the task ran under %q; got: %s", want, final.Result)
	}
	if got := pairedGoalRecord(t, id).MaxRounds; got != tries {
		t.Fatalf("goal record max_rounds after the run = %d, want %d", got, tries)
	}
}

// A task created while the try limit was 20 and started after it was lowered to
// 3 runs under 3: a goal takes the limit in force when it STARTS, as a chat goal
// does at `/goal` set time.
func TestTaskAttemptCeiling_RunTakesTheTryLimitInForceWhenItStarts(t *testing.T) {
	const tries, attempts = 3, 1
	al, judgeInst, _ := newTryLimitLoop(t, tries, attempts)
	judge := newTryLimitJudge(nil)
	judgeInst.Provider = judge

	id := createTaskThroughAgentTool(t, al)
	rec := pairedGoalRecord(t, id)
	if _, err := resolveGoalRecordStore().Update(rec.GoalID, func(g *goal.Goal) error {
		g.MaxRounds = 20 // written at creation, before the operator lowered the setting
		return nil
	}); err != nil {
		t.Fatalf("arrange: backdate the goal record's limit: %v", err)
	}

	final := runTaskToTerminal(t, al, id, 20)
	if final.AttemptCount != attempts {
		t.Fatalf("AttemptCount = %d, want %d", final.AttemptCount, attempts)
	}
	if got := judge.callCount(); got != tries {
		t.Fatalf("Judge calls = %d, want %d: a run must take the goal try limit in force when it starts", got, tries)
	}
	if got := pairedGoalRecord(t, id).MaxRounds; got != tries {
		t.Fatalf("goal record max_rounds = %d, want %d: the run start must stamp the live limit onto the record", got, tries)
	}
}

// Start a task with the try limit at 3, change the setting to 6 while its first
// run is going: that run still stops after 3 tries ("goals already running keep
// the limit they started with"), and the restarted run — a new start — takes 6.
func TestTaskAttemptCeiling_SettingChangeMidRunDoesNotMoveARunningGoal(t *testing.T) {
	const startTries, changedTries, attempts = 3, 6, 2
	al, judgeInst, _ := newTryLimitLoop(t, startTries, attempts)
	mutated := make(chan error, 1)
	judge := newTryLimitJudge(func() {
		mutated <- al.MutateConfig(func(cfg *config.Config) error {
			cfg.Planning.GoalMaxRounds = changedTries
			return nil
		})
	})
	judgeInst.Provider = judge

	id := createTaskThroughAgentTool(t, al)
	final := runTaskToTerminal(t, al, id, 2*(startTries+changedTries))

	select {
	case err := <-mutated:
		if err != nil {
			t.Fatalf("changing the goal try limit mid-run failed: %v", err)
		}
	default:
		t.Fatal("the Judge was never called, so the setting was never changed mid-run — the case proves nothing")
	}
	if got := goalTryLimit(al); got != changedTries {
		t.Fatalf("live goal try limit = %d, want %d — the mid-run change did not land", got, changedTries)
	}
	if final.AttemptCount != attempts {
		t.Fatalf("AttemptCount = %d, want %d", final.AttemptCount, attempts)
	}
	if got, want := judge.callCount(), startTries+changedTries; got != want {
		t.Fatalf("Judge calls = %d, want %d: the running goal must keep %d tries and only the restarted run may take %d",
			got, want, startTries, changedTries)
	}
	if got := pairedGoalRecord(t, id).MaxRounds; got != changedTries {
		t.Fatalf("goal record max_rounds after the restarted run = %d, want %d", got, changedTries)
	}
}

// A per-task max_attempts ("stays as the per-task override") wins over the
// global task attempt limit, and does not touch the try limit.
func TestTaskAttemptCeiling_PerTaskMaxAttemptsStillWins(t *testing.T) {
	const tries, globalAttempts, perTask = 2, 3, 1
	al, judgeInst, _ := newTryLimitLoop(t, tries, globalAttempts)
	judge := newTryLimitJudge(nil)
	judgeInst.Provider = judge

	id := createTaskThroughAgentTool(t, al)
	v := perTask
	vp := &v
	if _, err := al.taskStore.Update(id, task.Patch{MaxAttempts: &vp}); err != nil {
		t.Fatalf("arrange: set max_attempts=%d: %v", perTask, err)
	}

	final := runTaskToTerminal(t, al, id, tries*globalAttempts)
	if final.AttemptCount != perTask {
		t.Fatalf("AttemptCount = %d, want %d: a per-task max_attempts must win over the global task attempt limit",
			final.AttemptCount, perTask)
	}
	if got := judge.callCount(); got != tries*perTask {
		t.Fatalf("Judge calls = %d, want %d", got, tries*perTask)
	}
	if want := fmt.Sprintf("(max %d)", perTask); !strings.Contains(final.Result, want) {
		t.Fatalf("handover must report %q; got: %s", want, final.Result)
	}
}

// The divergence brake on the OUTER counter is twice the resolved task attempt
// limit (FR-047, GOAL-FR-026 "the 2 × effective budget hard ceiling"). It cannot
// be observed end-to-end in the normal flow (the attempt limit always trips
// first by construction), so it is pinned here, at the one function the run
// loop calls; TestTaskExecutor_AttemptHardCeiling_StopsUnconditionally covers
// the dispatch that starts past it.
func TestTaskAttemptHardCeiling_IsTwiceTheResolvedLimit(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{{5, 10}, {2, 4}, {1, 2}, {3, 6}} {
		if got := taskAttemptHardCeiling(tc.limit); got != tc.want {
			t.Fatalf("taskAttemptHardCeiling(%d) = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

// The same goal try limit of 5 bounds a chat goal at 5 rounds.
func TestChatGoal_GoalTryLimitBoundsRoundsAtFive(t *testing.T) {
	const limit = 5
	al, judgeInst := newGoalLoopTestLoop(t, &mockProvider{}, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = limit
	})
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	opts := processOptions{
		TranscriptStore: store, TranscriptSessionID: sid,
		Channel: "webchat", ChatID: "c1", SessionKey: "sk1", UserInitiated: true,
	}
	al.applyGoalCommandPrompt(context.Background(),
		bus.InboundMessage{Content: "/goal make the tests pass", UserInitiated: true}, agentInst, &opts)
	activatePendingGoal(t, al, agentInst, &opts)
	if got := goalRecordForSession(t, sid).MaxRounds; got != limit {
		t.Fatalf("chat goal record max_rounds = %d, want %d", got, limit)
	}
	al.recordGoalRouting(sid, "", "webchat", "c1", "sk1", agentInst.ID)
	judgeInst.Provider = unmetJudgeProvider("still unmet")

	for round := 1; round <= limit; round++ {
		r := &turnResult{finalContent: fmt.Sprintf("[goal:evidence] attempt %d\nGOAL_STATUS: met", round)}
		al.checkGoalLoopAfterTurn(context.Background(), agentInst, opts, r)
		if r.goalDeferredAdjudication == nil {
			t.Fatalf("round %d: a met+evidence claim must record deferred adjudication work", round)
		}
		al.dispatchDeferredGoalAdjudication(r.goalDeferredAdjudication)
		rec := goalRecordForSessionOrNil(sid)
		if round < limit {
			if rec == nil || rec.Round != round {
				t.Fatalf("round %d (< limit %d): the goal must still be active at round %d, got %+v", round, limit, round, rec)
			}
			select {
			case <-al.bus.InboundChan():
			case <-time.After(2 * time.Second):
				t.Fatalf("round %d: expected a follow-up round delivered via the async-notifier", round)
			}
			continue
		}
		if rec != nil {
			t.Fatalf("round %d (== limit %d): the goal must have ended, but it is still active at round %d", round, limit, rec.Round)
		}
	}
}
