package agent

// r17 16.5, 21.7 and 21.8: production discovery, task writers/readers, and
// workspace trust graph. No tool/schema/search result is supplied by a fake.
import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT8Discovery_TaskDetailInTopThree(t *testing.T) {
	f := t8ReadLoop(t)
	r := f.call(t, "reader-a", testHarnessWorkspaceMembershipID, "ToolSearch", map[string]any{"query": "read the full details of a task"})
	require.False(t, r.IsError, "16.5 actual catalog discovery: %s", r.ForLLM)
	start := strings.Index(r.ForLLM, "{")
	require.NotEqual(t, -1, start, "instrument: discovery returned a structured match list: %s", r.ForLLM)
	var out struct {
		Matches []struct {
			Name string `json:"name"`
		} `json:"matches"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(r.ForLLM[start:])).Decode(&out))
	require.NotEmpty(t, out.Matches, "instrument: search actually returned candidates")
	var first []string
	for _, m := range out.Matches[:min(3, len(out.Matches))] {
		first = append(first, m.Name)
	}
	assert.Contains(t, first, "get_task", "16.5 exact r17 query finds the real task reader in the first three, not a hand-ranked test catalog")
}

func TestT8Omnipus_WeeklySelfTaskWaitAndMissingMiaEdge(t *testing.T) {
	for _, denied := range []bool{false, true} {
		name := "21.7 weekly self-task waits"
		if denied {
			name = "21.8 missing Mia edge"
		}
		t.Run(name, func(t *testing.T) {
			al, _, parent := t8SelfDelegateFixture(t)
			clock := newTriggerFakeClock()
			sched := NewTaskTriggerScheduler(filepath.Join(config.OmnipusHomeDir(), "tasks_triggers", "jobs.json"), al.taskStore, al.taskExecutor)
			sched.SetClock(clock)
			require.NoError(t, sched.Start())
			t.Cleanup(sched.Stop)
			al.SetTaskTriggerScheduler(sched)
			rows, present := workspace.LoadDelegation(config.OmnipusHomeDir(), "t8-default")
			require.True(t, present, "instrument: the actual workspace trust graph was loaded")
			for _, row := range rows {
				assert.False(t, row.FromAgent == "omnipus" && row.ToAgent == "mia", "instrument: there is no Omnipus-to-Mia edge")
			}
			inst, ok := al.GetRegistry().GetAgent("omnipus")
			require.True(t, ok)
			target := "omnipus"
			if denied {
				target = "mia"
			}
			at := clock.Now().Add(7 * 24 * time.Hour).UnixMilli()
			args := map[string]any{"title": "weekly self review", "prompt": "review and verify the workspace", "agent_id": target, "criteria": []any{map[string]any{"text": "the review was verified"}}, "dod": []any{map[string]any{"text": "the weekly review is complete"}}}
			if !denied {
				args["trigger"] = map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=WEEKLY", "dtstart_ms": at, "tz": "UTC"}}
			}
			before, err := al.taskStore.List(task.Filter{})
			require.NoError(t, err)
			beforeSessions := t8SessionCount(t, al)
			ctx := tools.WithWorkspaceID(tools.WithAgentID(tools.WithTranscriptSessionID(context.Background(), parent), "omnipus"), "t8-default")
			r := t8RegisteredCall(t, inst, ctx, "create_task", args)
			if denied {
				require.True(t, r.IsError, "21.8 creation without an edge is refused")
				assert.Contains(t, r.ForLLM, "mia", "existing delegation refusal identifies the prohibited target")
				assert.Contains(t, strings.ToLower(r.ForLLM), "delegat", "it is a delegation refusal, not an unrelated schema error")
				after, err := al.taskStore.List(task.Filter{})
				require.NoError(t, err)
				assert.Equal(t, before, after)
				assert.Empty(t, sched.cs.ListJobs(true))
				assert.Equal(t, beforeSessions, t8SessionCount(t, al))
				return
			}
			require.False(t, r.IsError, "21.7 seeded Omnipus can create a scheduled self-task: %s", r.ForLLM)
			tk := t8CreatedTask(t, al, "weekly self review")
			assert.Equal(t, task.StatusInbox, tk.Status)
			require.NotNil(t, tk.Trigger)
			assert.Equal(t, task.TriggerRecurring, tk.Trigger.Type)
			assert.Equal(t, &at, tk.Trigger.Config.DtstartMs)
			require.Len(t, jobsForTask(sched, tk.ID), 1, "the real creation notification arms one occurrence")
			got := t8ReadObject(t, t8RegisteredCall(t, inst, ctx, "get_task", map[string]any{"task_id": tk.ID, "fields": []any{"trigger", "status"}}))
			assert.Equal(t, map[string]any{"id": tk.ID, "status": "inbox", "trigger": map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=WEEKLY", "dtstart_ms": float64(at), "tz": "UTC"}}}, got)
			for range 3 {
				al.taskExecutor.CheckQueuedTasks(context.Background())
				al.taskExecutor.wg.Wait()
				sched.RunDueJobs(clock.Now())
				sched.WaitForLane()
			}
			runs, err := al.taskStore.ListRuns(tk.ID)
			require.NoError(t, err)
			assert.Empty(t, runs, "no run before the first weekly occurrence")
			assert.Equal(t, beforeSessions, t8SessionCount(t, al))
		})
	}
}
