// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for ending a turn when the model keeps asking for a
// call the identical-failure circuit breaker has already refused
// (tool_failure_circuit_breaker.go). Live case, release run 39391964355
// (2026-10-20): a task worker sent goal_claim(status:met) without evidence,
// was refused five times, was blocked from a sixth, and kept asking until
// the turn ran 109 iterations and the task sat in_progress for 300 s.
//
// Expected counts are the design values written as literals (five real
// dispatches, refusal at the sixth attempt, the turn ends after the round
// that asks for the refused call again), not read back from the constants.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const breakerTurnStopNotice = "I stopped this turn: my call to `always_failing_tool` with the same arguments failed " +
	"5 times this turn, further identical calls were blocked, and I asked for it again anyway. Tell me how you " +
	"would like to proceed, or ask me to try a different approach."

// TestRunTurn_RefusedIdenticalFailureAskedAgain_EndsTurnWithVisibleReason:
// one identical failing call per round. Rounds 1-5 dispatch and fail; round
// 6 is refused by the breaker (the model is told); round 7 asks for the same
// refused call again, so the turn ends after that round with a visible,
// honest reason instead of spending further provider rounds.
func TestRunTurn_RefusedIdenticalFailureAskedAgain_EndsTurnWithVisibleReason(t *testing.T) {
	provider := testutil.NewScenario()
	scriptRounds(provider, "brk", "always_failing_tool", "{}", 20)
	provider.WithText("unreachable: the turn must end after the refused call is asked for again")

	al := newRepeatStopLoop(t, provider)
	stub := &alwaysFailingStubTool{}
	al.RegisterTool(stub)
	allowToolsForAllAgents(t, al, "always_failing_tool")

	finalContent, err := al.processDirectPrimaryForTest(context.Background(),
		"run always_failing_tool until it works", "test-session-breaker-turn-stop")
	require.NoError(t, err)

	assert.Equal(t, 5, stub.callCount(), "five identical failures dispatch; the sixth attempt is refused")
	assert.Equal(t, 7, provider.CallCount(),
		"round 6 is refused and returned to the model; round 7 asks again and is the last provider round")
	assert.Equal(t, breakerTurnStopNotice, finalContent,
		"the turn must end on the visible breaker stop reason")
	assert.Positive(t, toolMessagesContaining(provider, "Blocked by dispatch-side circuit breaker"),
		"the model must have been shown the refusal before the turn was stopped")
}

// evidenceLessClaimWorker answers every LLM call with one goal_claim
// (status met, no evidence) — the live worker's shape. It never replies
// with text.
type evidenceLessClaimWorker struct {
	mu    sync.Mutex
	calls int
}

func (w *evidenceLessClaimWorker) Chat(
	context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any,
) (*providers.LLMResponse, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	args, _ := json.Marshal(map[string]any{"status": tools.GoalClaimStatusMet})
	fc := providers.FunctionCall{Name: tools.GoalClaimToolName, Arguments: string(args)}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: fmt.Sprintf("claim-%03d", w.calls), Type: "function", Function: &fc,
	}}}, nil
}

func (w *evidenceLessClaimWorker) GetDefaultModel() string { return "test-model" }

func (w *evidenceLessClaimWorker) callCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// TestTaskRun_EvidenceLessClaimLoop_TurnEndsAtBreakerAndTaskTerminates: a
// task worker that only ever sends an evidence-less met claim. Its turn must
// end at the breaker (seven provider rounds), with the visible reason in the
// run transcript, and the task must reach a terminal state instead of
// hanging in_progress until the iteration cap.
func TestTaskRun_EvidenceLessClaimLoop_TurnEndsAtBreakerAndTaskTerminates(t *testing.T) {
	worker := &evidenceLessClaimWorker{}
	al := newNativeTaskCompletionTestLoop(t, worker)
	require.NoError(t, al.MutateConfig(func(cfg *config.Config) error { cfg.Planning.GoalMaxRounds = 1; return nil }))
	tk := newCompletionContractTask(t, al, "native-agent", "evidence-less claim loop")
	one := 1
	onePtr := &one
	_, err := al.taskStore.Update(tk.ID, task.Patch{MaxAttempts: &onePtr})
	require.NoError(t, err, "pin max_attempts=1")

	require.NoError(t, al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil))
	final := waitForCompletionContractTerminal(t, al, tk.ID)

	assert.Equal(t, task.StatusFailed, final.Status,
		"a worker that never makes a valid claim must end Failed, not hang in_progress (result: %s)", final.Result)
	assert.Equal(t, 7, worker.callCount(),
		"one try, one attempt: the worker turn must end after seven provider rounds, not run to the iteration cap")

	entries, err := al.GetSessionStore().ReadTranscript(final.SessionID)
	require.NoError(t, err)
	var sawReason bool
	for _, e := range entries {
		if strings.Contains(e.Content, "I stopped this turn: my call to `goal_claim` with the same arguments failed 5 times this turn") {
			sawReason = true
		}
	}
	assert.True(t, sawReason, "the run transcript must carry the visible breaker stop reason")
}

// pathProbeTool fails for any path but "good" and succeeds for "good" - a
// call whose corrected arguments work.
type pathProbeTool struct {
	tools.BaseTool
	good, bad atomic.Int32
}

func (p *pathProbeTool) Name() string        { return "path_probe" }
func (p *pathProbeTool) Description() string { return "test stub: fails unless path is good" }
func (p *pathProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"path": map[string]any{"type": "string", "description": "path"},
	}}
}
func (p *pathProbeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *pathProbeTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	if path, _ := args["path"].(string); path == "good" {
		p.good.Add(1)
		return tools.NewToolResult("contents of good")
	}
	p.bad.Add(1)
	return tools.ErrorResult("no such file")
}

// flakyTool fails its first four calls, succeeds on the fifth, then fails
// again - a transient failure that recovers.
type flakyTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (f *flakyTool) Name() string        { return "flaky_tool" }
func (f *flakyTool) Description() string { return "test stub: fails four times, then succeeds once" }
func (f *flakyTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f *flakyTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (f *flakyTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	if f.calls.Add(1) == 5 {
		return tools.NewToolResult("recovered")
	}
	return tools.ErrorResult("temporarily unavailable")
}

func oneCall(id, name, args string) providers.ToolCall {
	fc := providers.FunctionCall{Name: name, Arguments: args}
	return providers.ToolCall{ID: id, Function: &fc}
}

// TestRunTurn_RefusedCallAskedAgainInAProductiveRound_KeepsTheTurnGoing
// (silent-failure SF-1): the round that asks again for the refused call also
// runs a different call that succeeds. That success is progress the model
// must be able to use, so the turn is not stopped; it continues to the
// model's final answer.
func TestRunTurn_RefusedCallAskedAgainInAProductiveRound_KeepsTheTurnGoing(t *testing.T) {
	provider := testutil.NewScenario()
	scriptRounds(provider, "brk", "always_failing_tool", "{}", 6) // 5 failures, round 6 refused
	provider.WithToolCalls([]providers.ToolCall{
		oneCall("mixed-refused", "always_failing_tool", "{}"),
		oneCall("mixed-good", "path_probe", `{"path":"good"}`),
	})
	provider.WithText("answer built from the good path")

	al := newRepeatStopLoop(t, provider)
	stub := &alwaysFailingStubTool{}
	probe := &pathProbeTool{}
	al.RegisterTool(stub)
	al.RegisterTool(probe)
	allowToolsForAllAgents(t, al, "always_failing_tool", "path_probe")

	finalContent, err := al.processDirectPrimaryForTest(context.Background(),
		"read the file", "test-session-breaker-mixed-round")
	require.NoError(t, err)

	assert.Equal(t, "answer built from the good path", finalContent,
		"a round with a successful call must not end the turn; the model answers from that result")
	assert.Equal(t, int32(1), probe.good.Load(), "the corrected call ran once")
	assert.Equal(t, 5, stub.callCount(), "the refused call never dispatched again")
	assert.Equal(t, 8, provider.CallCount(), "seven tool rounds plus the final answer")
}

// TestRunTurn_ChangedArgumentsAfterFailures_DoNotTripTheBreakerStop (T3): five
// failures with one argument, then the corrected argument succeeds; the turn
// ends on the model's answer, not a stop.
func TestRunTurn_ChangedArgumentsAfterFailures_DoNotTripTheBreakerStop(t *testing.T) {
	provider := testutil.NewScenario()
	scriptRounds(provider, "bad", "path_probe", `{"path":"bad"}`, 5)
	scriptRounds(provider, "good", "path_probe", `{"path":"good"}`, 1)
	provider.WithText("found it")

	al := newRepeatStopLoop(t, provider)
	probe := &pathProbeTool{}
	al.RegisterTool(probe)
	allowToolsForAllAgents(t, al, "path_probe")

	finalContent, err := al.processDirectPrimaryForTest(context.Background(),
		"find the file", "test-session-breaker-changed-args")
	require.NoError(t, err)

	assert.Equal(t, "found it", finalContent)
	assert.Equal(t, int32(5), probe.bad.Load())
	assert.Equal(t, int32(1), probe.good.Load())
	assert.Equal(t, 7, provider.CallCount())
}

// TestRunTurn_TransientFailureThatRecovers_ResetsTheBreaker (T3): four
// failures, a success of the same call, four more failures - never five in
// a row, so nothing is refused and the turn ends on the model's answer.
func TestRunTurn_TransientFailureThatRecovers_ResetsTheBreaker(t *testing.T) {
	provider := testutil.NewScenario()
	scriptRounds(provider, "flaky", "flaky_tool", "{}", 9)
	provider.WithText("gave up politely")

	al := newRepeatStopLoop(t, provider)
	flaky := &flakyTool{}
	al.RegisterTool(flaky)
	allowToolsForAllAgents(t, al, "flaky_tool")

	finalContent, err := al.processDirectPrimaryForTest(context.Background(),
		"try the flaky thing", "test-session-breaker-transient")
	require.NoError(t, err)

	assert.Equal(t, "gave up politely", finalContent)
	assert.Equal(t, int32(9), flaky.calls.Load(), "every call dispatched: the success reset the streak")
	assert.Zero(t, toolMessagesContaining(provider, "Blocked by dispatch-side circuit breaker"),
		"no call may be refused")
}

// steerWhileRunningTool stands in for a person typing while the round runs:
// its execution queues an instruction for the running turn, then fails, so
// the round itself makes no progress.
type steerWhileRunningTool struct {
	tools.BaseTool
	al *AgentLoop
}

const queuedInstruction = "stop retrying that tool and summarise what you have"

func (s *steerWhileRunningTool) Name() string { return "steer_while_running" }
func (s *steerWhileRunningTool) Description() string {
	return "test stub: queues an instruction, then fails"
}
func (s *steerWhileRunningTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (s *steerWhileRunningTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (s *steerWhileRunningTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	if err := s.al.Steer(providers.Message{Role: "user", Content: queuedInstruction}); err != nil {
		return tools.ErrorResult("steer failed: " + err.Error())
	}
	return tools.ErrorResult("still not available")
}

// TestRunTurn_QueuedInstructionAtTheBreakerStop_IsDeliveredNotLost (T3): in the
// round that asks again for the refused call - the round that would end the
// turn - a person's instruction arrives after the refusal. The instruction
// wins: the turn continues, the model receives it exactly once, and the turn
// ends on the model's answer.
func TestRunTurn_QueuedInstructionAtTheBreakerStop_IsDeliveredNotLost(t *testing.T) {
	provider := testutil.NewScenario()
	scriptRounds(provider, "brk", "always_failing_tool", "{}", 6) // 5 failures, round 6 refused
	provider.WithToolCalls([]providers.ToolCall{
		oneCall("again-refused", "always_failing_tool", "{}"),
		oneCall("person-types", "steer_while_running", "{}"),
	})
	provider.WithText("summary as asked")

	al := newRepeatStopLoop(t, provider)
	stub := &alwaysFailingStubTool{}
	al.RegisterTool(stub)
	al.RegisterTool(&steerWhileRunningTool{al: al})
	allowToolsForAllAgents(t, al, "always_failing_tool", "steer_while_running")

	finalContent, err := al.processDirectPrimaryForTest(context.Background(),
		"run always_failing_tool until it works", "test-session-breaker-queued")
	require.NoError(t, err)

	assert.Equal(t, "summary as asked", finalContent, "the queued instruction must supersede the stop")
	require.Equal(t, 8, provider.CallCount(), "the turn continued for exactly one more round")
	delivered := 0
	for _, m := range provider.AllRequests()[7] {
		if m.Role == "user" && strings.Contains(m.Content, queuedInstruction) {
			delivered++
		}
	}
	assert.Equal(t, 1, delivered, "the queued instruction reaches the model exactly once")
}
