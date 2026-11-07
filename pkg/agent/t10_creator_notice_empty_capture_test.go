//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CHECK3 F1 regression (T10 FR-18/19; written red by the implementing lead):
// a worker run started by a person (Run now) or by the scheduler captures no
// recipient — a person has no main and a worker is not a chat target — yet the
// agent that created the task is still owed one copy of the full notice.
// Stored recipients stay empty (contracts TaskRun.yaml recipient_session_ids).
func TestT10_CreatorNoticeWithNoCapturedRecipient(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "success"
		if failed {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			f := t10NoticeSetup(t, "parent-agent", "starter", false, true)
			te := f.al.taskExecutor
			// Production-shaped eligibility: the worker assignee has no
			// eligible main, and a person-started run has no starter agent.
			delete(f.resolver.elig, [2]string{f.task.WorkspaceID, "worker"})
			recipients := te.captureRunRecipients(context.Background(), f.task, nil)
			require.Empty(t, recipients, "precondition: a person-started worker run captures no recipient")
			occurrence := *f.task.Trigger.Config.AtMs
			f.run = te.openRun(f.task.ID, &occurrence, task.RunKindScheduled, f.task.SessionID, recipients...)
			require.NotNil(t, f.run, "real stored run must open")

			expected, statusWord := "full T10 result: alpha\nbeta", "done"
			status := task.StatusDone
			if failed {
				expected, statusWord, status = "specific failure: report could not finish", "failed", task.StatusFailed
			}
			losses := taskNoticeLosses.Load()
			applied := te.completeTaskWithResult(f.task, f.task.SessionID, task.StatusInProgress, !failed, expected, f.run)
			require.True(t, applied)

			want := fmt.Sprintf("Task run %s: %q (run %s)\n\n%s", statusWord, f.task.Title, f.run.runID, expected)
			assert.Equal(t, []string{want}, f.drain(t, f.main["parent-agent"]),
				"the creator gets one copy of the full notice even when no recipient was captured")
			assert.Empty(t, f.drain(t, f.main["starter"]))
			assert.Empty(t, f.drain(t, f.main["worker"]))
			assert.Equal(t, losses, taskNoticeLosses.Load(), "a delivered creator copy is not a lost notice")
			f.assertStoredRun(t, status, expected)
			assert.Empty(t, f.run.recipients, "the creator copy never becomes a captured recipient")
		})
	}
}
