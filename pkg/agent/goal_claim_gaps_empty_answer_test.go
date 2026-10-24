// #1431 answer/evidence separation must not remove the genuine-empty-answer
// protection. Chief's explicit clarification: evidence is never an answer fallback.
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestTaskRun_UpheldClaimWithEmptyAnswerDoesNotUseEvidenceAsResult(t *testing.T) {
	worker := newClaimingWorker(workerTurn{
		claim: tools.GoalClaimStatusMet, evidence: "The report was read and checked.", content: " \t ",
	})
	al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
	judgeInst.Provider = judge
	tk := createTaskWithGoal(t, al, &task.Task{
		ID: "task-gap1431-genuinely-empty", Title: "supplier report", Prompt: "Check the report and return the result.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the report was checked")},
	}, "the work has been verified")
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait()
	if len(judge.snapshot()) != 1 || worker.turnsStarted() != 1 {
		t.Fatalf("Judge calls/worker turns=%d/%d, want 1/1", len(judge.snapshot()), worker.turnsStarted())
	}
	const want = "empty_answer: the task produced no result"
	if final.Status != task.StatusFailed || final.Result != want {
		t.Errorf("genuinely empty answer: status/result=%q/%q, want failed/%q; non-empty evidence must not supply the result", final.Status, final.Result, want)
	}
	if final.AttemptCount != 0 {
		t.Errorf("empty-answer completion used %d failed Judge attempts, want zero", final.AttemptCount)
	}
}
