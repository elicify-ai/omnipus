package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The live-to-L1 rehearsal found one extra internal trigger per scheduled task
// on every boot. Oracle: a task's schedule represents one job, regardless of
// restart count. Exercise the production gateway scheduler boot stage, not a
// copied constructor/reconcile sequence, against one persistent, fresh home.
func TestTaskTriggerGatewayBootIdempotency(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	store := task.New(filepath.Join(home, "tasks"))
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	due := time.Now().Add(48 * time.Hour).Truncate(time.Second).UnixMilli()
	definitions := make(map[string][]byte)
	for _, id := range []string{"boot-scheduled-a", "boot-scheduled-b"} {
		tsk := &task.Task{
			ID: id, Title: id, AgentID: "mia", WorkspaceID: "default",
			// Finished runs still own future recurrences. Keeping the tasks done
			// also prevents the unrelated queued-task drain from starting work.
			Status: task.StatusDone,
			Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{
				Rrule: &rule, DtstartMs: &due, Tz: &tz,
			}},
		}
		require.NoError(t, store.Create(tsk))
		data, err := os.ReadFile(filepath.Join(home, "tasks", id+".json"))
		require.NoError(t, err)
		definitions[id] = data
	}

	var firstJobs map[string]cron.CronJob
	for boot := 1; boot <= 3; boot++ {
		func() {
			cfg := &config.Config{
				Agents: config.AgentsConfig{
					Defaults: config.AgentDefaults{
						Home:         filepath.Join(home, "workspace"),
						DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
					},
					List: []config.AgentConfig{{ID: "mia"}},
				},
			}
			msgBus := bus.NewMessageBus()
			al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
			defer al.Close()
			stage := &setupAndStartServicesState{
				ctx: context.Background(), cfg: cfg, agentLoop: al,
				msgBus: msgBus, homePath: home,
			}
			// The production order: prepare (boot classification) before
			// startSchedulers starts any task producer (re-review N1).
			require.NoError(t, stage.prepareTaskTrigger())
			_, stop, err := stage.startSchedulers()
			if stage.runningServices != nil {
				defer stopAndCleanupServices(stage.runningServices, 5*time.Second, false)
			}
			require.NoError(t, err)
			require.False(t, stop)
			require.NotNil(t, stage.runningServices.TaskTrigger)
			require.NotNil(t, agent.GetTaskStore(al))

			data, err := os.ReadFile(filepath.Join(home, "tasks_triggers", "jobs.json"))
			require.NoError(t, err)
			var stored cron.CronStore
			require.NoError(t, json.Unmarshal(data, &stored))
			t.Logf("BOOT=%d scheduled_tasks=%d stored_trigger_jobs=%d", boot, len(definitions), len(stored.Jobs))
			assert.Len(t, stored.Jobs, 2, "each boot must retain exactly one job per scheduled task")
			jobs := make(map[string]cron.CronJob)
			counts := make(map[string]int)
			for _, job := range stored.Jobs {
				counts[job.Payload.Message]++
				jobs[job.Payload.Message] = job
				assert.Equal(t, &due, job.Schedule.AtMS)
				assert.Equal(t, &due, job.State.NextRunAtMS)
			}
			assert.Equal(t, map[string]int{"boot-scheduled-a": 1, "boot-scheduled-b": 1}, counts)
			if boot == 1 {
				firstJobs = jobs
			} else {
				assert.Equal(t, firstJobs, jobs, "a no-op restart must preserve the canonical jobs and their state")
			}
		}()
	}
	for id, before := range definitions {
		after, err := os.ReadFile(filepath.Join(home, "tasks", id+".json"))
		require.NoError(t, err)
		assert.Equal(t, before, after, "boot must not change the scheduled task definition")
	}
}
