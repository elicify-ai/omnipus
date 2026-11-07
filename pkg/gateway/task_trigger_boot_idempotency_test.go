package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
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
			// startSchedulers runs the task boot classification itself
			// (T10 FR-03; re-review N1).
			_, stop, err := stage.startSchedulers()
			if stage.runningServices != nil {
				defer stopAndCleanupServices(stage.runningServices, 5*time.Second, false)
			}
			require.NoError(t, err)
			require.False(t, stop)
			require.NotNil(t, stage.runningServices.TaskTrigger)
			require.NotNil(t, agent.GetTaskStore(al))

			data, err := os.ReadFile(filepath.Join(cfg.AgentHomeBasePath(), "cron", "jobs.json")) // T10 FR-04: the shared store
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

// CHECK2 F6: the setup assertion on task preparation was removed when the
// preparation moved inside startSchedulers. Its error oracle is restored here
// as a behavioural probe of the REAL boot: a preparation failure (here: the
// store refuses the write that removes a duplicate trigger job) must abort the
// boot with the preparation error, nothing activated, and the store unchanged.
func TestTaskTriggerGatewayBootAbortsWhenPreparationFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("directory permissions do not bind root; the injected write failure needs a non-root user")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	store := task.New(filepath.Join(home, "tasks"))
	rule, tz := "FREQ=DAILY;COUNT=5", "UTC"
	due := time.Now().Add(48 * time.Hour).Truncate(time.Second).UnixMilli()
	require.NoError(t, store.Create(&task.Task{
		ID: "prep-fail-task", Title: "prep-fail-task", AgentID: "mia", WorkspaceID: "default",
		Status: task.StatusDone,
		Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{
			Rrule: &rule, DtstartMs: &due, Tz: &tz,
		}},
	}))
	newStage := func() (*setupAndStartServicesState, *config.Config, func()) {
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
		stage := &setupAndStartServicesState{ctx: context.Background(), cfg: cfg, agentLoop: al, msgBus: msgBus, homePath: home}
		return stage, cfg, func() {
			if stage.runningServices != nil {
				stopAndCleanupServices(stage.runningServices, 5*time.Second, false)
			}
			al.Close()
			msgBus.Close()
		}
	}

	// Boot 1 (healthy) writes the task's trigger job.
	stage, cfg, closeStage := newStage()
	_, stop, err := stage.startSchedulers()
	require.NoError(t, err)
	require.False(t, stop)
	closeStage()
	storePath := filepath.Join(cfg.AgentHomeBasePath(), "cron", "jobs.json")
	data, err := os.ReadFile(storePath)
	require.NoError(t, err)
	var stored cron.CronStore
	require.NoError(t, json.Unmarshal(data, &stored))
	require.Len(t, stored.Jobs, 1, "SETUP: the healthy boot wrote exactly one trigger job")

	// Make a duplicate trigger job for the same task, then forbid store writes.
	dup := stored.Jobs[0]
	dup.ID = dup.ID + "-duplicate"
	stored.Jobs = append(stored.Jobs, dup)
	data, err = json.MarshalIndent(stored, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(storePath, data, 0o600))
	before, err := os.ReadFile(storePath)
	require.NoError(t, err)
	cronDir := filepath.Dir(storePath)
	require.NoError(t, os.Chmod(cronDir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(cronDir, 0o755) })

	stage2, _, closeStage2 := newStage()
	defer closeStage2()
	var mu sync.Mutex
	var events []string
	observer, ok := any(stage2).(interface {
		SetSchedulerBootObserverForTest(func(string, *cron.CronService))
	})
	require.True(t, ok, "BLOCKED: pre-activation boot observer not implemented")
	observer.SetSchedulerBootObserverForTest(func(label string, _ *cron.CronService) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, label)
	})
	defer observer.SetSchedulerBootObserverForTest(nil)

	_, stop, err = stage2.startSchedulers()
	require.Error(t, err, "a failed task preparation must fail the boot")
	assert.True(t, stop)
	assert.Contains(t, err.Error(), "error preparing task trigger scheduler")
	mu.Lock()
	defer mu.Unlock()
	assert.NotContains(t, events, "task_reconciled")
	assert.NotContains(t, events, "activate", "nothing may be activated after a failed preparation")
	after, err := os.ReadFile(storePath)
	require.NoError(t, err)
	assert.Equal(t, before, after, "a failed preparation leaves the store as the dead process left it")
}
