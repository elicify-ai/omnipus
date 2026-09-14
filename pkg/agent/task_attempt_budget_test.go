// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// task_attempt_budget_test.go pins the founder decision of 2026-09-14: the
// Settings -> Performance goal try limit (goal_max_rounds) is the ONE setting
// that bounds how many tries a goal gets — a goal set in chat AND a goal on a
// task — and "goals already running keep the limit they started with".
//
// Every expected number below is the value the test itself configures (5, 10,
// 2), never a value read back from the implementation. The task cases drive
// real ExecuteTask dispatches (worker turn + Judge turn per attempt); only the
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
	tryLimitWorkerReply   = "wrote the report\n" +
		"[goal:evidence] opened the report and checked every invoice against the ledger\n" +
		"TASK_STATUS: success\n" +
		"TASK_SUMMARY: The report is written."
)

// tryLimitJudge answers every verifier call UNMET for the acceptance criterion
// (perCriterionJudgeProvider, keyed by criterion text), so a task never
// completes and its attempt ceiling is the only thing that can stop it. It runs
// onFirstCall exactly once, before answering the first call — i.e. after the
// task's run has started and its goal record has been activated.
type tryLimitJudge struct {
	inner       *perCriterionJudgeProvider
	once        sync.Once
	onFirstCall func()
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
	return j.inner.Chat(ctx, msgs, defs, model, opts)
}

func (j *tryLimitJudge) GetDefaultModel() string { return "fake-judge-model" }

// newTryLimitLoop builds the goal-loop harness with the goal try limit set to
// limit. The agent home base is rooted under OMNIPUS_HOME because create_task
// resolves its workspace against filepath.Dir(agent home base), which must be
// the test home where the harness's membership workspace lives.
func newTryLimitLoop(t *testing.T, limit int) (*AgentLoop, *AgentInstance) {
	t.Helper()
	return newGoalLoopTestLoop(t, &scriptedProvider{responseBody: tryLimitWorkerReply}, func(cfg *config.Config) {
		cfg.Planning.GoalMaxRounds = limit
		cfg.Agents.Defaults.Home = filepath.Join(config.OmnipusHomeDir(), "agents")
	})
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
// deadline for the LARGEST attempt count the case could produce if the code
// under test were wrong, so a regression fails on the count assertion rather
// than on a wait timeout.
func runTaskToTerminal(t *testing.T, al *AgentLoop, taskID string, waitFor int) *task.Task {
	t.Helper()
	if err := al.taskExecutor.ExecuteTask(context.Background(), taskID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	return t3WaitForTerminal(t, al, taskID, waitFor)
}

// (a) + (c): an agent-created task under a goal try limit of 5, no per-task
// override, every attempt judged unmet — its goal record says 5 from the moment
// it is created, and the task fails after exactly 5 attempts.
func TestTaskAttemptCeiling_AgentCreatedTaskFollowsGoalTryLimit(t *testing.T) {
	const limit = 5
	al, judgeInst := newTryLimitLoop(t, limit)
	judgeInst.Provider = newTryLimitJudge(nil)

	id := createTaskThroughAgentTool(t, al)
	if got := pairedGoalRecord(t, id).MaxRounds; got != limit {
		t.Fatalf("create_task wrote goal record max_rounds=%d, want %d: the agent tool must snapshot the "+
			"live goal try limit, not the shipped default", got, limit)
	}

	final := runTaskToTerminal(t, al, id, 20)
	if final.Status != task.StatusFailed {
		t.Fatalf("status = %q, want failed (every attempt was judged unmet)", final.Status)
	}
	if final.AttemptCount != limit {
		t.Fatalf("AttemptCount = %d, want %d: the task must stop at the Settings goal try limit", final.AttemptCount, limit)
	}
	if want := fmt.Sprintf("(max %d)", limit); !strings.Contains(final.Result, want) {
		t.Fatalf("handover must report the limit the task ran under %q; got: %s", want, final.Result)
	}
	if got := pairedGoalRecord(t, id).MaxRounds; got != limit {
		t.Fatalf("goal record max_rounds after the run = %d, want %d", got, limit)
	}
}

// A task created while the limit was 20 and started after it was lowered to 5
// runs under 5: a goal takes the limit in force when it STARTS, as a chat goal
// does at `/goal` set time.
func TestTaskAttemptCeiling_RunTakesTheLimitInForceWhenItStarts(t *testing.T) {
	const limit = 5
	al, judgeInst := newTryLimitLoop(t, limit)
	judgeInst.Provider = newTryLimitJudge(nil)

	id := createTaskThroughAgentTool(t, al)
	rec := pairedGoalRecord(t, id)
	if _, err := resolveGoalRecordStore().Update(rec.GoalID, func(g *goal.Goal) error {
		g.MaxRounds = 20 // written at creation, before the operator lowered the setting
		return nil
	}); err != nil {
		t.Fatalf("arrange: backdate the goal record's limit: %v", err)
	}

	final := runTaskToTerminal(t, al, id, 20)
	if final.AttemptCount != limit {
		t.Fatalf("AttemptCount = %d, want %d: a run must take the goal try limit in force when it starts", final.AttemptCount, limit)
	}
	if got := pairedGoalRecord(t, id).MaxRounds; got != limit {
		t.Fatalf("goal record max_rounds = %d, want %d: the run start must stamp the live limit onto the record", got, limit)
	}
}

// The lead's requested case: start a task with the limit at 5, change the
// setting to 10 while it is running, and the task still stops at 5 ("goals
// already running keep the limit they started with").
func TestTaskAttemptCeiling_SettingChangeMidRunDoesNotMoveARunningTask(t *testing.T) {
	const startLimit, changedLimit = 5, 10
	al, judgeInst := newTryLimitLoop(t, startLimit)
	mutated := make(chan error, 1)
	judgeInst.Provider = newTryLimitJudge(func() {
		mutated <- al.MutateConfig(func(cfg *config.Config) error {
			cfg.Planning.GoalMaxRounds = changedLimit
			return nil
		})
	})

	id := createTaskThroughAgentTool(t, al)
	final := runTaskToTerminal(t, al, id, changedLimit)

	select {
	case err := <-mutated:
		if err != nil {
			t.Fatalf("changing the goal try limit mid-run failed: %v", err)
		}
	default:
		t.Fatal("the Judge was never called, so the setting was never changed mid-run — the case proves nothing")
	}
	if got := goalTryLimit(al); got != changedLimit {
		t.Fatalf("live goal try limit = %d, want %d — the mid-run change did not land", got, changedLimit)
	}
	if final.AttemptCount != startLimit {
		t.Fatalf("AttemptCount = %d, want %d: a task already running must keep the limit it started with", final.AttemptCount, startLimit)
	}
	if got := pairedGoalRecord(t, id).MaxRounds; got != startLimit {
		t.Fatalf("goal record max_rounds = %d, want %d", got, startLimit)
	}
}

// (d): a per-task max_attempts (R-03, "stays as the per-task override") still
// wins over the global goal try limit.
func TestTaskAttemptCeiling_PerTaskMaxAttemptsStillWins(t *testing.T) {
	const limit, perTask = 5, 2
	al, judgeInst := newTryLimitLoop(t, limit)
	judgeInst.Provider = newTryLimitJudge(nil)

	id := createTaskThroughAgentTool(t, al)
	v := perTask
	vp := &v
	if _, err := al.taskStore.Update(id, task.Patch{MaxAttempts: &vp}); err != nil {
		t.Fatalf("arrange: set max_attempts=%d: %v", perTask, err)
	}

	final := runTaskToTerminal(t, al, id, limit)
	if final.AttemptCount != perTask {
		t.Fatalf("AttemptCount = %d, want %d: a per-task max_attempts must win over the goal try limit", final.AttemptCount, perTask)
	}
	if want := fmt.Sprintf("(max %d)", perTask); !strings.Contains(final.Result, want) {
		t.Fatalf("handover must report %q; got: %s", want, final.Result)
	}
}

// The divergence brake is twice the RESOLVED ceiling (FR-047, GOAL-FR-026
// "the 2 × effective budget hard ceiling"), so it follows the goal try limit.
// It cannot be observed end-to-end (the normal gate always trips first by
// construction — see consumeAttemptOrExhaust's doc comment), so it is pinned
// here, at the one function consumeAttemptOrExhaust calls.
func TestTaskAttemptHardCeiling_IsTwiceTheResolvedLimit(t *testing.T) {
	for _, tc := range []struct{ limit, want int }{{5, 10}, {2, 4}, {1, 2}, {20, 40}} {
		if got := taskAttemptHardCeiling(tc.limit); got != tc.want {
			t.Fatalf("taskAttemptHardCeiling(%d) = %d, want %d", tc.limit, got, tc.want)
		}
	}
}

// (b): the same goal try limit of 5 bounds a chat goal at 5 rounds.
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
