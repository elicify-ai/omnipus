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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const breakerTurnStopNotice = "I stopped this turn: my call to `always_failing_tool` failed 5 times in a row with the " +
	"same arguments and the same result, further identical attempts were blocked, and I asked for it again " +
	"anyway. Tell me how you would like to proceed, or ask me to try a different approach."

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
		if strings.Contains(e.Content, "I stopped this turn: my call to `goal_claim` failed 5 times in a row") {
			sawReason = true
		}
	}
	assert.True(t, sawReason, "the run transcript must carry the visible breaker stop reason")
}
