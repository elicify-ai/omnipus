// #1431 gate T2 / CHECK F3: the exact ADR-084 section 13 MIN-001 row,
// two inner tries by two attempts, with fresh sessions and counter resets.
package agent

import (
	"context"
	"strings"
	"sync"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestTaskRun_EmptyEvidenceExhaustsTwoByTwoWithFreshSessions(t *testing.T) {
	const innerTries, outerAttempts = 2, 2 // ADR-084 section 13 MIN-001, not defaults
	const verdictReason = "the report still omits the required supplier acceptance"
	worker := newClaimingWorker(turnClaimMet(""))
	al, judgeInst := newGoalLoopTestLoop(t, worker, func(cfg *config.Config) { cfg.Planning.GoalMaxRounds = innerTries })
	budget := outerAttempts
	tk := createTaskWithGoal(t, al, &task.Task{
		ID: "task-gap1431-two-by-two", Title: "supplier report", Prompt: "Write the supplier report.",
		Action: task.ActionLLM, AgentID: "native-agent", Priority: 3, WorkspaceID: "default", Status: task.StatusNext,
		MaxAttempts: &budget, Criteria: []task.AcceptanceCriterion{proseCriterion("c1", "the report records the requested supplier acceptance")},
	}, "the report has been checked")
	type observation struct {
		sid      string
		round    int
		attempts int
		status   task.Status
		err      error
	}
	var mu sync.Mutex
	var observed []observation
	judge := &gap1431Judge{met: false, reason: verdictReason}
	judge.beforeReply = func(int) {
		cur, err := al.taskStore.Get(tk.ID)
		row := observation{err: err}
		if err == nil {
			row.sid, row.attempts, row.status = cur.SessionID, cur.AttemptCount, cur.Status
			rec, readErr := resolveGoalRecordStore().GetByOwner(generated.GoalOwnerKindTask, tk.ID)
			row.err = readErr
			if readErr == nil {
				row.round = rec.Round
			}
		}
		mu.Lock()
		observed = append(observed, row)
		mu.Unlock()
	}
	judgeInst.Provider = judge
	if err := al.taskExecutor.ExecuteTask(context.Background(), tk.ID, nil); err != nil {
		t.Fatalf("ExecuteTask: %v", err)
	}
	final := t3WaitForTerminal(t, al, tk.ID, innerTries*outerAttempts)
	al.taskExecutor.wg.Wait()
	mu.Lock()
	rows := append([]observation(nil), observed...)
	mu.Unlock()
	if len(rows) != 4 {
		t.Fatalf("completed unmet adjudications=%d, want 4 (2 inner tries x 2 attempts); observations=%+v", len(rows), rows)
	}
	wantRounds, wantAttempts := []int{0, 1, 0, 1}, []int{0, 0, 1, 1}
	for i, row := range rows {
		if row.err != nil {
			t.Fatalf("adjudication %d: read durable state: %v", i+1, row.err)
		}
		if row.round != wantRounds[i] || row.attempts != wantAttempts[i] || row.status != task.StatusInProgress {
			t.Errorf("before verdict %d: round/attempts/status=%d/%d/%q, want %d/%d/in_progress; task may fail only after verdict four", i+1, row.round, row.attempts, row.status, wantRounds[i], wantAttempts[i])
		}
	}
	if rows[0].sid == "" || rows[2].sid == "" || rows[0].sid != rows[1].sid || rows[2].sid != rows[3].sid || rows[0].sid == rows[2].sid {
		t.Fatalf("run session sequence=%q/%q/%q/%q, want A/A/B/B with non-empty A != B", rows[0].sid, rows[1].sid, rows[2].sid, rows[3].sid)
	}
	if final.Status != task.StatusFailed || final.AttemptCount != outerAttempts || final.SessionID != rows[2].sid {
		t.Errorf("final status/attempts/session=%q/%d/%q, want failed/2/%q", final.Status, final.AttemptCount, final.SessionID, rows[2].sid)
	}
	if !strings.Contains(final.Result, verdictReason) {
		t.Errorf("final failure=%q, want the last Judge reason %q", final.Result, verdictReason)
	}
	if worker.turnsStarted() != 4 || len(judge.snapshot()) != 4 {
		t.Errorf("worker turns/Judge calls=%d/%d, want exactly 4/4", worker.turnsStarted(), len(judge.snapshot()))
	}
	rec := taskGoalRecordOf(t, tk.ID)
	if rec.Round != innerTries || rec.State != generated.GoalStateExhausted || len(rec.TerminalHistory) != 1 || rec.TerminalHistory[0].Round != innerTries {
		t.Errorf("final goal round/state/history=%d/%q/%+v, want 2/exhausted/one prior run at round 2", rec.Round, rec.State, rec.TerminalHistory)
	}
	for _, sid := range []string{rows[0].sid, rows[2].sid} {
		entries, err := al.GetSessionStore().ReadTranscript(sid)
		if err != nil {
			t.Fatalf("read run session %q: %v", sid, err)
		}
		claims, verdicts := 0, 0
		for _, entry := range entries {
			if entry.Type == session.EntryTypeJudgeVerdict {
				verdicts++
			}
			for _, call := range entry.ToolCalls {
				if call.Tool == tools.GoalClaimToolName && call.Status == "success" {
					claims++
				}
			}
		}
		if claims != innerTries || verdicts != innerTries {
			t.Errorf("session %q successful empty claims/verdicts=%d/%d, want 2/2", sid, claims, verdicts)
		}
	}
}
