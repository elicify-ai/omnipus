package agent

// Executable RED slices of tool-parity r17 §7.2 TK-30. These do not claim
// to replace the full production REST/tool/drain scenarios in r17 §13.1.
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8SchedulerBoundaries(t *testing.T) {
	t.Run("18.20 completion and deletion promotion preserve scheduled wait", func(t *testing.T) {
		for _, via := range []string{"completion", "deletion"} {
			t.Run(via, func(t *testing.T) {
				s := task.New(t.TempDir())
				d := &task.Task{Title: "predecessor", Status: task.StatusNext, Action: task.ActionLLM, AgentID: "a", WorkspaceID: "w"}
				require.NoError(t, s.Create(d))
				at := time.Now().Add(24 * time.Hour).UnixMilli()
				dep := &task.Task{Title: "scheduled dependent", Status: task.StatusBlocked, Action: task.ActionLLM, AgentID: "a", WorkspaceID: "w", BlockedBy: []string{d.ID}, Trigger: &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}}
				require.NoError(t, s.Create(dep))
				if via == "completion" {
					status := task.StatusDone
					_, err := s.Update(d.ID, task.Patch{Status: &status})
					require.NoError(t, err)
					_, err = s.AdvanceBlockedDependents(d.ID)
					require.NoError(t, err)
				} else {
					_, err := s.Delete(d.ID)
					require.NoError(t, err)
					_, err = s.AdvanceUnblocked(dep.ID)
					require.NoError(t, err)
				}
				got, err := s.Get(dep.ID)
				require.NoError(t, err)
				assert.Equal(t, task.StatusBlocked, got.Status, "18.20 future occurrence still holds dependent after %s", via)
				require.NotNil(t, got.Trigger)
				assert.Equal(t, at, *got.Trigger.Config.AtMs)
			})
		}
	})
	t.Run("TK-30 delivered occurrence writes scheduled-instant marker; not a physical-restart proof", func(t *testing.T) {
		sched, store, clock, rec := newTriggerSchedulerForTest(t)
		at := clock.Now().Add(time.Minute).UnixMilli()
		tk := makeTask(t, store, "a", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}})
		sched.OnTaskUpserted(tk)
		clock.Advance(2 * time.Minute)
		sched.RunDueJobs(clock.Now())
		sched.WaitForLane()
		require.Equal(t, []string{tk.ID}, rec.calls(), "positive control: occurrence really delivered")
		got, err := store.Get(tk.ID)
		require.NoError(t, err)
		raw, err := json.Marshal(got)
		require.NoError(t, err)
		var record map[string]any
		require.NoError(t, json.Unmarshal(raw, &record))
		assert.Equal(t, float64(at), record["last_delivered_at_ms"], "FR-010 marker stores the occurrence's scheduled instant, not wall delivery time")
	})
	t.Run("18.37 overdue uncollected registration kept on unchanged trigger", func(t *testing.T) {
		sched, store, clock, rec := newTriggerSchedulerForTest(t)
		at := clock.Now().Add(time.Minute).UnixMilli()
		tk := makeTask(t, store, "a", &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}})
		sched.OnTaskUpserted(tk)
		before := sched.cs.ListJobs(true)
		require.Len(t, before, 1)
		clock.Advance(2 * time.Minute) // deliberately do NOT collect yet (O1 overdue).
		title := "new title"
		updated, err := store.Update(tk.ID, task.Patch{Title: &title})
		require.NoError(t, err)
		sched.OnTaskUpserted(updated)
		after := sched.cs.ListJobs(true)
		require.Len(t, after, 1)
		assert.Equal(t, before[0].ID, after[0].ID, "18.37 O1 ownership keeps the same registration across an ordinary edit")
		sched.RunDueJobs(clock.Now())
		sched.WaitForLane()
		assert.Equal(t, []string{tk.ID}, rec.calls(), "original overdue owned occurrence delivers exactly once")
	})
}
