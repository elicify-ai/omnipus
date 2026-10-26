// #1431 gate T1 / silent-failure finding 1: present-empty evidence is not
// an empty work result. Oracles: ADR-084 section 13 and the chief's (a).
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestGoalClaimWiring_EmptyEvidenceUsesToolResultAndCompletesChat(t *testing.T) {
	g := newGap1431Chat(t)
	const decisive = "supplier Cide accepted clause 7.2 verbatim; receipt GC-1431-chat"
	const answer = "The requested report is ready for your review."
	// The decisive value is nowhere but the persisted tool result. The
	// request inspector below observes the full real Judge dispatch path.
	if err := g.store.AppendTranscriptStrict(g.sid, session.TranscriptEntry{
		ID: "gap1431-result-only", Type: session.EntryTypeToolCall, Role: "assistant", Timestamp: time.Now().UTC(),
		ToolCalls: []session.ToolCall{{ID: "gap1431-read", Tool: "read_file", Status: "success",
			Parameters: map[string]any{"path": "report.md"}, Result: map[string]any{"content": decisive}}},
	}); err != nil {
		t.Fatalf("persist result-only evidence: %v", err)
	}
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the requested criterion"}
	g.judge.Provider = judge
	dispatches := recordGoalDispatches(g.al)
	work := g.emptyClaim(t, "gap1431-chat-met", answer)
	g.al.dispatchDeferredGoalAdjudication(work)

	requireGap1431ResultInJudgeRequest(t, judge, decisive)
	rec := mustGoalRecord(t, g.gid)
	if rec.State != generated.GoalStateMet || rec.Round != 1 {
		t.Errorf("upheld empty claim left state=%q round=%d, want met/1", rec.State, rec.Round)
	}
	if rec.LatestClaim == nil || rec.LatestClaim.Evidence != "" {
		t.Errorf("LatestClaim = %+v, want the recorded empty evidence", rec.LatestClaim)
	}
	if rec.LatestVerdict == nil || !rec.LatestVerdict.Met {
		t.Errorf("LatestVerdict = %+v, want the upheld verdict", rec.LatestVerdict)
	}
	if got := dispatches.all(); len(got) != 0 {
		t.Errorf("upheld claim dispatched %d further worker turns, want zero: %+v", len(got), got)
	}
	out := requireOneGoalOutcome(t, g.store, g.sid, g.gid)
	if out.GoalOutcome.Ending != generated.GoalOutcomeEndingMet {
		t.Errorf("goal ending = %q, want met", out.GoalOutcome.Ending)
	}
}

func TestTaskRun_UpheldEmptyEvidenceCompletesWithWorkerAnswer(t *testing.T) {
	const decisive = "supplier Cide accepted clause 7.2 verbatim; receipt GC-1431-task"
	const answer = "The supplier report is complete. All requested clauses were checked."
	worker := &gap1431ResultWorker{answer: answer}
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) {
		cfg.Sandbox.ToolPolicies["read_file"] = "allow"
	})
	agent, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent is not registered")
	}
	worker.path = filepath.Join(agent.Home, "report.md")
	if err := os.WriteFile(worker.path, []byte(decisive), 0o600); err != nil {
		t.Fatalf("write result-only input file: %v", err)
	}
	judge := &gap1431Judge{met: true, reason: "the recorded work satisfies the requested criterion"}
	judgeInst.Provider = judge
	tk := createTaskWithGoal(t, al, &task.Task{
		ID: "task-gap1431-upheld-empty", Title: "supplier report", Prompt: "Check the supplier report and return your answer.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the report records the requested supplier acceptance")},
	}, "the requested report has been checked")
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, tk.ID, 1)
	al.taskExecutor.wg.Wait() // the archive alone is not the completion tail

	requireGap1431ResultInJudgeRequest(t, judge, decisive)
	if final.Status != task.StatusDone {
		t.Errorf("upheld empty-evidence claim: status=%q result=%q, want done with the worker answer (not empty_answer)", final.Status, final.Result)
	}
	if final.Result != answer {
		t.Errorf("task result = %q, want worker's real answer %q, never its empty evidence", final.Result, answer)
	}
	if final.AttemptCount != 0 {
		t.Errorf("upheld claim spent %d failed attempts, want zero", final.AttemptCount)
	}
	if n := worker.callCount(); n != 3 {
		t.Errorf("worker provider calls = %d, want exactly 3 in one turn (read_file, goal_claim, answer); no restart", n)
	}
	rec := taskGoalRecordOf(t, tk.ID)
	if rec.State != generated.GoalStateMet || rec.Round != 1 {
		t.Errorf("task goal state=%q round=%d, want met/1", rec.State, rec.Round)
	}
	if rec.LatestClaim == nil || rec.LatestClaim.Evidence != "" {
		t.Errorf("LatestClaim = %+v, want empty evidence kept separate from answer", rec.LatestClaim)
	}
	entries, err := al.GetSessionStore().ReadTranscript(final.SessionID)
	if err != nil {
		t.Fatalf("read task transcript: %v", err)
	}
	resultCalls, verdicts := 0, 0
	for _, entry := range entries {
		if entry.Type == session.EntryTypeJudgeVerdict {
			verdicts++
		}
		for _, call := range entry.ToolCalls {
			if call.Tool == "read_file" && call.Status == "success" {
				resultCalls++
			}
		}
		if strings.Contains(entry.Content, decisive) {
			t.Errorf("result-only canary leaked into narration %q; the evidence fixture no longer proves result-only input", entry.Content)
		}
	}
	if resultCalls != 1 || verdicts != 1 {
		t.Errorf("recorded successful reads/verdicts = %d/%d, want 1/1", resultCalls, verdicts)
	}
}
