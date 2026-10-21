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
// is supplied to the real production Record writer.
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
			written, err := h.al.evidenceStore().Record(id, "r6-criterion", 1, "external deterministic check", "Verified external check output", 0, false, false)
			require.NoError(t, err)
			require.Equal(t, id, written.TaskID)
			reopened, err := task.NewEvidenceStore(config.OmnipusHomeDir(), nil).List(id)
			require.NoError(t, err)
			require.Len(t, reopened, 1)
			require.Equal(t, written.ID, reopened[0].ID)
			require.Equal(t, "Verified external check output", reopened[0].Output)
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

func TestGoalReminderExistingEvidenceProgress(t *testing.T) { grBrakeEvidenceProgress(t) }
