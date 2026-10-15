package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskTriggerRestoreSafety(t *testing.T) {
	t.Run("once_trigger_survives_three_boots_and_fires_once", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("OMNIPUS_HOME", home)
		store := task.New(filepath.Join(home, "tasks"))
		clk := newTriggerFakeClock()
		due := clk.Now().Add(time.Minute).UnixMilli()
		tk := makeTask(t, store, "agent-once", &task.Trigger{
			Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &due},
		})
		path := filepath.Join(home, "jobs.json")
		rec := &dispatchRecorder{}
		var sched *TaskTriggerScheduler
		var firstID string
		for boot := 1; boot <= 3; boot++ {
			sched = NewTaskTriggerScheduler(path, store, nil)
			sched.SetClock(clk)
			sched.dispatch = rec.dispatch
			require.NoError(t, sched.Start())
			require.NoError(t, sched.Reconcile())
			jobs := jobsForTask(sched, tk.ID)
			require.Len(t, jobs, 1)
			assert.Equal(t, &due, jobs[0].State.NextRunAtMS)
			if boot == 1 {
				firstID = jobs[0].ID
			} else {
				assert.Equal(t, firstID, jobs[0].ID)
			}
			if boot < 3 {
				sched.Stop()
			}
		}
		defer sched.Stop()
		clk.Advance(2 * time.Minute)
		for range 2 {
			sched.RunDueJobs(clk.Now())
			sched.WaitForLane()
		}
		assert.Equal(t, []string{tk.ID}, rec.calls())
		assert.Empty(t, jobsForTask(sched, tk.ID))
	})

	t.Run("current_definition_wins_after_offline_edit", func(t *testing.T) {
		for _, change := range []string{"owner", "rule", "once", "manual", "clear", "heartbeat"} {
			t.Run(change, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HOME", home)
				t.Setenv("OMNIPUS_HOME", home)
				store := task.New(filepath.Join(home, "tasks"))
				clk := newTriggerFakeClock()
				due := secondAligned(clk.Now().Add(time.Minute))
				tk := makeRruleTask(t, store, "agent-before", "FREQ=DAILY", due, "UTC")
				path := filepath.Join(home, "tasks_triggers", "jobs.json")
				sched := NewTaskTriggerScheduler(path, store, nil)
				sched.SetClock(clk)
				require.NoError(t, sched.Start())
				require.NoError(t, sched.Reconcile())
				sched.Stop()
				later := due + time.Minute.Milliseconds()
				var replacement *task.Trigger
				patch := task.Patch{}
				wantOwner, wantDue, wantJobs := tk.AgentID, due, 1
				switch change {
				case "owner":
					wantOwner = "agent-after"
					patch.AgentID = &wantOwner
				case "rule":
					replacement = &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{
						Rrule: strP("FREQ=DAILY"), DtstartMs: &later, Tz: strP("UTC"),
					}}
					patch.Trigger, wantDue = &replacement, later
				case "once":
					replacement = &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &later}}
					patch.Trigger, wantDue = &replacement, later
				case "manual":
					replacement = &task.Trigger{Type: task.TriggerManual}
					patch.Trigger, wantJobs = &replacement, 0
				case "clear":
					patch.Trigger, wantJobs = &replacement, 0
				case "heartbeat":
					surface := task.SurfaceHeartbeat
					patch.Surface, wantJobs = &surface, 0
				}
				_, err := store.Update(tk.ID, patch)
				require.NoError(t, err)
				restored := NewTaskTriggerScheduler(path, store, nil)
				restored.SetClock(clk)
				rec := &dispatchRecorder{}
				restored.dispatch = rec.dispatch
				require.NoError(t, restored.Start())
				defer restored.Stop()
				require.NoError(t, restored.Reconcile())
				jobs := jobsForTask(restored, tk.ID)
				require.Len(t, jobs, wantJobs, "never keep both the old and edited trigger")
				if wantJobs == 1 {
					assert.Equal(t, wantOwner, jobs[0].AgentID)
					assert.Equal(t, &wantDue, jobs[0].Schedule.AtMS)
				}
				if wantJobs == 0 || wantDue > due {
					clk.Advance(time.UnixMilli(due).Sub(clk.Now()))
					restored.RunDueJobs(clk.Now())
					restored.WaitForLane()
					assert.Empty(t, rec.calls(), "a removed or edited old occurrence must not fire")
				}
			})
		}
	})

	t.Run("unreadable_store_refuses_start_without_overwrite", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("OMNIPUS_HOME", home)
		store := task.New(filepath.Join(home, "tasks"))
		clk := newTriggerFakeClock()
		due := secondAligned(clk.Now().Add(time.Minute))
		tk := makeRruleTask(t, store, "agent-a", "FREQ=DAILY", due, "UTC")
		path := filepath.Join(home, "jobs.json")
		seed := cron.NewCronService(path)
		seed.SetClock(clk)
		for range 2 {
			_, err := seed.AddJobFull(cron.JobSpec{
				Name: "task:" + tk.ID, Message: tk.ID, AgentID: tk.AgentID,
				Schedule: cron.CronSchedule{Kind: "at", AtMS: &due},
			})
			require.NoError(t, err)
		}
		valid, err := os.ReadFile(path)
		require.NoError(t, err)
		broken := bytes.Replace(valid, []byte(`"enabled": true`), []byte(`"enabled": "invalid"`), 1)
		require.NotEqual(t, valid, broken, "instrument: deliberately damage an actual job field")
		require.NoError(t, os.WriteFile(path, broken, 0o600))
		sched := NewTaskTriggerScheduler(path, store, nil)
		sched.SetClock(clk)
		err = sched.Start()
		defer sched.Stop()
		var typeErr *json.UnmarshalTypeError
		require.ErrorAs(t, err, &typeErr, "a load failure must reach the gateway, not become a fresh store")
		assert.Equal(t, "string", typeErr.Value)
		assert.Equal(t, "bool", typeErr.Type.String())
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, broken, after, "never overwrite damaged user scheduler data with a cleanup")
		assert.Equal(t, false, sched.cs.Status()["enabled"])
	})
}
