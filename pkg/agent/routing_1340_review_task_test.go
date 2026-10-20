package agent

import (
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SFH F7: actual task execution, not just its shared classifier, must stop
// without using an attempt or asking the Judge to validate a failed request.
func TestRouting1340_ReviewTaskBadModelUsesNoAttemptOrJudge(t *testing.T) {
	worker := &failingWorker{err: func() error {
		return &providers.FailoverError{
			Reason: providers.FailoverBadModel, Provider: "openrouter", Model: "test-model", Status: 400,
			Wrapped: &common.ProviderError{Status: 400, Body: `{"error":{"code":"model_not_found","message":"sk-proj-review1340SECRET"}}`},
		}
	}}
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	t.Cleanup(tools.SetTaskGoalEndedHook(al.recordTaskGoalOutcome))
	judge := &b6ScriptedJudge{metFromCall: 1, reason: "must not run"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	if final.Status != task.StatusFailed || final.AttemptCount != 0 || worker.callCount() != 1 || judge.callCount() != 0 {
		t.Fatalf("task did not stop on operator fault: status=%s attempts=%d worker=%d Judge=%d", final.Status, final.AttemptCount, worker.callCount(), judge.callCount())
	}
	if !strings.Contains(final.Result, "openrouter") || !strings.Contains(final.Result, "test-model") || !strings.Contains(final.Result, "settings") || strings.Contains(final.Result, "sk-proj") {
		t.Errorf("task result lacks safe named fix: %q", final.Result)
	}
	rec := waitForGoalState(t, tk.ID, generated.GoalStateExhausted)
	if strings.Contains(rec.TerminalReason, "sk-proj") {
		t.Fatalf("provider canary persisted in goal reason: %q", rec.TerminalReason)
	}
}
