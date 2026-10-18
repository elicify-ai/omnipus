package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: the failure dispatch requires one dispatch and one real task run per
// due time after any number of boots. Serializing the cron lane and waiting for
// a real task's completion forces the fast-finish interleaving: overlap guards
// cannot disguise duplicate jobs by keeping the first task in progress.
func TestTaskTriggerRestart(t *testing.T) {
	t.Run("one_real_run_per_due_time_after_three_boots", func(t *testing.T) {
		al := newNativeTaskCompletionTestLoop(t, newClaimingWorker(turnClaimMet("verified the scheduled work")))
		t.Setenv("HOME", config.OmnipusHomeDir())
		clk := newTriggerFakeClock()
		due := secondAligned(clk.Now().Add(time.Minute))
		ids := make([]string, 0, 2)
		for range 2 { // distinct tasks intentionally share exactly the same schedule
			tk := makeRruleTask(t, al.taskStore, "native-agent", "FREQ=DAILY;COUNT=5", due, "UTC")
			pairTaskGoalRecordUsingTaskCriteria(t, al, tk.ID)
			ids = append(ids, tk.ID)
		}
		path := filepath.Join(config.OmnipusHomeDir(), "tasks_triggers", "jobs.json")
		var sched *TaskTriggerScheduler
		var dispatched []string
		var occurrences []int64
		for boot := 1; boot <= 3; boot++ {
			sched = NewTaskTriggerScheduler(path, al.taskStore, al.taskExecutor)
			sched.SetClock(clk)
			sched.cs.SetMaxConcurrentRuns(1)
			run := sched.dispatch
			sched.dispatch = func(ctx context.Context, id string, occurrence *int64) error {
				dispatched = append(dispatched, id)
				if occurrence != nil {
					occurrences = append(occurrences, *occurrence)
				}
				err := run(ctx, id, occurrence)
				al.taskExecutor.wg.Wait() // force completion before the next duplicate can fire
				return err
			}
			require.NoError(t, sched.Start())
			require.NoError(t, sched.Reconcile())
			t.Logf("BOOT=%d scheduled_tasks=2 stored_trigger_jobs=%d", boot, len(sched.cs.ListJobs(true)))
			if boot < 3 {
				sched.Stop()
			}
		}
		defer sched.Stop()
		clk.Advance(2 * time.Minute)
		sched.RunDueJobs(clk.Now())
		sched.WaitForLane()
		sched.RunDueJobs(clk.Now()) // same instant must not dispatch again
		sched.WaitForLane()
		counts := map[string]int{}
		for _, id := range dispatched {
			counts[id]++
		}
		t.Logf("DUE=%d dispatch_counts=%v", due, counts)
		assert.Equal(t, map[string]int{ids[0]: 1, ids[1]: 1}, counts)
		assert.Equal(t, []int64{due, due}, occurrences)
		for _, id := range ids {
			runs, err := al.taskStore.ListRuns(id)
			require.NoError(t, err)
			t.Logf("task=%s due=%d real_task_runs=%d", id, due, len(runs))
			assert.Len(t, runs, 1, "the real executor must not open a second run for this same due time")
			for _, run := range runs {
				assert.Equal(t, &due, run.OccurrenceMs)
				assert.NotNil(t, run.EndedAt)
				assert.Equal(t, task.StatusDone, run.Status)
			}
			assert.Len(t, jobsForTask(sched, id), 1, "the next occurrence stays armed once")
		}
	})

	t.Run("existing_duplicates_repaired_and_logged_once", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("OMNIPUS_HOME", home)
		store := task.New(filepath.Join(home, "tasks"))
		clk := newTriggerFakeClock()
		due := secondAligned(clk.Now().Add(time.Minute))
		tk := makeRruleTask(t, store, "agent-repair", "FREQ=DAILY", due, "UTC")
		path := filepath.Join(home, "tasks_triggers", "jobs.json")
		engine := cron.NewCronService(path)
		engine.SetClock(clk)
		var original *cron.CronJob
		var removed []string
		for i := 0; i < 3; i++ {
			job, err := engine.AddJobFull(cron.JobSpec{
				Name: "task:" + tk.ID, Message: tk.ID, AgentID: tk.AgentID,
				Schedule: cron.CronSchedule{Kind: "at", AtMS: &due},
			})
			require.NoError(t, err)
			if i == 0 {
				job.State.LastStatus = "ok"
				job.State.LastRunAtMS = int64P(due - time.Hour.Milliseconds())
				job.State.History = []cron.CronRunRecord{{RanAtMs: *job.State.LastRunAtMS, Status: "ok", SessionID: "prior-run"}}
				require.NoError(t, engine.UpdateJob(job))
				original = job
			} else {
				removed = append(removed, job.ID)
			}
		}
		beforeTask, err := os.ReadFile(filepath.Join(home, "tasks", tk.ID+".json"))
		require.NoError(t, err)
		var logs raceFreeLogBuffer
		old := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
		defer slog.SetDefault(old)
		for boot := 1; boot <= 2; boot++ {
			sched := NewTaskTriggerScheduler(path, store, nil)
			sched.SetClock(clk)
			require.NoError(t, sched.Start())
			reconcileErr := sched.Reconcile()
			sched.Stop()
			require.NoError(t, reconcileErr)
			assert.Equal(t, []cron.CronJob{*original}, sched.cs.ListJobs(true), "retain the original job, not a blank replacement")
		}
		afterTask, err := os.ReadFile(filepath.Join(home, "tasks", tk.ID+".json"))
		require.NoError(t, err)
		assert.Equal(t, beforeTask, afterTask, "duplicate repair must not edit the user's task")
		var logged []string
		// Unrelated session-store flushers can still log through the global handler.
		// Decode a locked snapshot, never the concurrently written sink itself.
		decoder := json.NewDecoder(strings.NewReader(logs.String()))
		for decoder.More() {
			var record map[string]any
			require.NoError(t, decoder.Decode(&record))
			if record["msg"] == "task_trigger: removed duplicate trigger job" {
				assert.Equal(t, tk.ID, record["task_id"])
				assert.Equal(t, original.ID, record["kept_job_id"])
				jobID, ok := record["job_id"].(string)
				require.True(t, ok, "cleanup logs must carry the removed job ID as a string")
				logged = append(logged, jobID)
			}
		}
		assert.ElementsMatch(t, removed, logged, "log every removed job exactly once, with the retained identity")
	})
}
