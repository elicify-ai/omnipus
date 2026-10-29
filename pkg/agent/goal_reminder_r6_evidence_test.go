package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q3 reuses the existing TASK-owned goal evidence partition. A task ID is
// supplied by the actual task/session mint; no List(goalID), new store or
// fake chat-evidence persistence is invented. Only external check-result data
// is supplied to the real production Record writer. Under conservative B, the
// owned positive records inside the minted task session's still-open canonical
// run and in a later second than its start; task ID alone is not ownership.
func grBrakeEvidenceProgress(t *testing.T) {
	for _, related := range []bool{true, false} {
		name := "NewEvidenceForActualGoalOwner"
		if !related {
			name = "OtherTaskEvidenceDoesNotReset"
		}
		t.Run(name, func(t *testing.T) {
			h := newTaskKeeperHarness(t, taskGoalCondition, recordedGoalCriteria(taskGoalCondition))
			_, err := goal.NewStore(config.OmnipusHomeDir()).Update(h.gid, func(g *goal.Goal) error { g.ZeroOutputPushes = 3; return nil })
			require.NoError(t, err)
			id := h.taskID
			if !related {
				other := mustCreateTask(t, h.al.taskStore, &task.Task{Title: "Unrelated evidence owner", WorkspaceID: "test-ws", AgentID: h.agentInst.ID, Status: task.StatusNext})
				id = other.ID
			}
			var ownedRun *task.TaskRun
			if related {
				ownedRun = grOpenTaskEvidenceWindow(t, h)
			}
			written, err := h.al.evidenceStore().Record(id, "r6-criterion", 1, "external deterministic check", "Verified external check output", 0, false, false)
			require.NoError(t, err)
			require.Equal(t, id, written.TaskID)
			reopened, err := task.NewEvidenceStore(config.OmnipusHomeDir(), nil).List(id)
			require.NoError(t, err)
			require.Len(t, reopened, 1)
			require.Equal(t, written.ID, reopened[0].ID)
			require.Equal(t, "Verified external check output", reopened[0].Output)
			if related {
				grRequireTaskEvidenceWindow(t, h, ownedRun, &reopened[0])
			}
			h.al.goalQuietWindowSettle(time.Now())
			want := 3 // no reminder completed here; unrelated evidence cannot erase prior completed failures
			if related {
				want = 0
			}
			assert.Equal(t, want, h.record(t).ZeroOutputPushes, "use evidence belonging to this actual goal's task, not all stored evidence")
			events := h.dispatch.all()
			require.Len(t, events, 1, "progress reset/no lifetime cap must reach the real notifier, not merely change a number")
			assert.Equal(t, h.sid, events[0].TranscriptSessionID)
			assert.Equal(t, "system:goal_loop", events[0].SenderCanonicalID)
			grTakePublishedReminder(t, h.al, h.sid)
			assert.Equal(t, 0, h.judge.callCount(), "new evidence never invents a timer Judge invocation")
		})
	}
}

func grOpenTaskEvidenceWindow(t *testing.T, h *taskKeeperHarness) *task.TaskRun {
	t.Helper()
	run, created, err := GetTaskStore(h.al).OpenRun(h.taskID, nil, task.RunKindManual, h.sid, nil)
	require.NoError(t, err)
	require.True(t, created, "SETUP: the owned check needs a new canonical run")
	require.Equal(t, h.sid, run.SessionID, "SETUP: bind to the task session minted by production")
	require.Nil(t, run.EndedAt, "SETUP: no completion event may mask evidence progress")
	started, err := time.Parse(time.RFC3339, run.StartedAt)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return time.Now().UTC().Truncate(time.Second).After(started)
	}, grWatchdog, 10*time.Millisecond, "SETUP: Record must run in a later second, not an ambiguous start bucket")
	return run
}

func grRequireTaskEvidenceWindow(t *testing.T, h *taskKeeperHarness, run *task.TaskRun, written *task.EvidenceRecord) {
	t.Helper()
	runs, err := GetTaskStore(h.al).ListRuns(h.taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.Equal(t, run.RunID, runs[0].RunID)
	require.Equal(t, h.sid, runs[0].SessionID)
	require.Nil(t, runs[0].EndedAt, "SETUP: the evidence must still be inside the open owned run")
	started, err := time.Parse(time.RFC3339, runs[0].StartedAt)
	require.NoError(t, err)
	at, err := time.Parse(time.RFC3339, written.RecordedAt)
	require.NoError(t, err)
	require.True(t, at.After(started), "SETUP: actual stored evidence must be later than the run start second")
}

func TestGoalReminderExistingEvidenceProgress(t *testing.T) { grBrakeEvidenceProgress(t) }
