// #1431 round-2 T1/T2: chief's exact answer-preservation and framework-notice
// oracles. The worker's answer spans the claiming TURN, not just its last reply.
package agent

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestTaskRun_UpheldClaimKeepsEarlierAssistantAnswerWhenFinalReplyIsEmpty(t *testing.T) {
	const answer = "REPORT-1431-PRE-CLAIM\nSupplier Alpha accepted the revised delivery date.\nSupplier Beta accepted the revised delivery date."
	for _, tc := range []struct{ name, finalReply string }{{"empty", ""}, {"whitespace", " \n\t "}} {
		t.Run(tc.name, func(t *testing.T) {
			worker := &round2ClaimWorker{firstAnswer: answer, finalAnswer: tc.finalReply}
			al, judgeInst := newGoalLoopTestLoop(t, worker, nil)
			judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
			judgeInst.Provider = judge
			tk := newRunLoopTask(t, al, nil)
			final := runTaskUntilTerminal(t, al, tk.ID, 1)
			al.taskExecutor.wg.Wait()

			assertRound2RecordedEmptyClaim(t, al, final, answer)
			if worker.callCount() != 3 || len(judge.snapshot()) != 1 {
				t.Fatalf("provider/Judge calls=%d/%d, want one tool call plus empty reply/retry and one upheld adjudication", worker.callCount(), len(judge.snapshot()))
			}
			if final.Status != task.StatusDone || final.Result != answer {
				t.Errorf("upheld claim with earlier deliverable: status/result=%q/%q, want Done with exact earlier ACTUAL answer %q, not empty_answer or framework text", final.Status, final.Result, answer)
			}
			if final.AttemptCount != 0 {
				t.Errorf("upheld earlier answer spent %d failed attempts, want zero", final.AttemptCount)
			}
		})
	}
}

func TestTaskRun_UpheldLastIterationClaimDoesNotDeliverToolLimitNotice(t *testing.T) {
	worker := &round2ClaimWorker{}
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Agents.Defaults.MaxToolIterations = 1 // first claim is the LAST permitted tool iteration
	})
	agent, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok || agent.MaxIterations != 1 {
		t.Fatalf("fixture: effective worker limit must be one, agent=%+v", agent)
	}
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait()

	assertRound2RecordedEmptyClaim(t, al, final, "")
	if worker.callCount() != 1 || len(judge.snapshot()) != 1 {
		t.Fatalf("worker/Judge calls=%d/%d, want exactly 1/1: no final model answer after the last tool iteration", worker.callCount(), len(judge.snapshot()))
	}
	const want = "empty_answer: the task produced no result"
	if final.Status != task.StatusFailed || final.Result != want {
		t.Errorf("no actual model answer at tool limit: status/result=%q/%q, want Failed/%q, never the framework's tool-limit notice", final.Status, final.Result, want)
	}
	if strings.Contains(final.Result, toolLimitResponse) {
		t.Errorf("toolLimitResponse was accepted as the worker's deliverable: %q", final.Result)
	}
	if final.AttemptCount != 0 {
		t.Errorf("empty-answer completion used %d failed Judge attempts, want zero", final.AttemptCount)
	}
}

// Sibling framework substitution: finalizeTurn assigns opts.DefaultResponse
// (normally defaultResponse) when there is no model text below the tool limit.
// Source: loop_run_turn.go::agentLoopRunTurnFinalize.finalizeTurn fallback arm.
func TestTaskRun_UpheldClaimDoesNotDeliverDefaultResponseNotice(t *testing.T) {
	worker := &round2ClaimWorker{}
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Agents.Defaults.MaxToolIterations = 3 // claim + empty final reply remain below the ceiling
	})
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the criterion"}
	judgeInst.Provider = judge
	tk := newRunLoopTask(t, al, nil)
	final := runTaskUntilTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait()

	assertRound2RecordedEmptyClaim(t, al, final, "")
	if worker.callCount() != 3 || len(judge.snapshot()) != 1 {
		t.Fatalf("worker/Judge calls=%d/%d, want 3/1 for claim then empty provider answer/retry below ceiling", worker.callCount(), len(judge.snapshot()))
	}
	const want = "empty_answer: the task produced no result"
	if final.Status != task.StatusFailed || final.Result != want {
		t.Errorf("no actual model answer below tool limit: status/result=%q/%q, want Failed/%q", final.Status, final.Result, want)
	}
	if strings.Contains(final.Result, defaultResponse) {
		t.Errorf("defaultResponse framework diagnostic was accepted as a deliverable: %q", final.Result)
	}
}
