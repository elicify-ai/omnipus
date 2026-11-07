package gateway

// Oracle: tool-parity r17 §7.2 TK-30, 18.39 and 18.40.
// Real gateway boot/reload functions. The clock is the only scheduler fake.
// These tests pin the production lifecycle boundary; they do not substitute
// for the queued-delivery, collection-save and session-count campaign rows.
import (
	"bytes"
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
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t8GatewayClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *t8GatewayClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *t8GatewayClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestTaskTrigger_ReloadKeepsLiveScheduler_ProductionPath(t *testing.T) {
	for _, shape := range []string{"18.40a overdue once", "18.40a-b overdue recurring", "18.40c damaged store"} {
		t.Run(shape, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("OMNIPUS_HOME", home)
			t.Setenv("OMNIPUS_BEARER_TOKEN", "")
			path := filepath.Join(home, "config.json")
			seed := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0}, Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), MaxTokens: 4096}}}
			require.NoError(t, config.SaveConfig(path, seed))
			creds := newUnlockedStore(t, home)
			cfg, err := config.LoadConfigWithStore(path, creds)
			require.NoError(t, err)
			msgBus := bus.NewMessageBus()
			al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
			rs, err := setupAndStartServices(context.Background(), cfg, credentials.SecretBundle{}, al, msgBus, home, creds, &SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
			require.NoError(t, err)
			t.Cleanup(func() { stopAndCleanupServices(rs, 5*time.Second, false) })
			rs.loadConfigForSwap = newReloadConfigLoader(path, home, creds, rs)
			// Install a clock BEFORE starting this fixture's engine. All captured
			// dependencies are the production loop's shared store and executor.
			rs.TaskTrigger.Stop()
			storePath := filepath.Join(home, "tasks_triggers", "jobs.json")
			original := agent.NewTaskTriggerScheduler(storePath, agent.GetTaskStore(al), agent.GetTaskExecutor(al))
			clock := &t8GatewayClock{now: time.Now().Add(24 * time.Hour).Truncate(time.Second)}
			original.SetClock(clock)
			require.NoError(t, original.Start())
			t.Cleanup(original.Stop)
			rs.TaskTrigger = original
			al.SetTaskTriggerScheduler(original)
			at := clock.Now().Add(time.Minute).UnixMilli()
			trigger := &task.Trigger{Type: task.TriggerOnce, Config: task.TriggerConfig{AtMs: &at}}
			if shape == "18.40a-b overdue recurring" {
				rule, tz := "FREQ=DAILY;COUNT=3", "UTC"
				trigger = &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}
			}
			tk := &task.Task{Title: "reload occurrence", Status: task.StatusInbox, Action: task.ActionLLM, AgentID: "mia", WorkspaceID: "default", Trigger: trigger}
			require.NoError(t, agent.GetTaskStore(al).Create(tk))
			original.OnTaskUpserted(tk)
			before, err := os.ReadFile(storePath)
			require.NoError(t, err)
			var armed cron.CronStore
			require.NoError(t, json.Unmarshal(before, &armed))
			require.Len(t, armed.Jobs, 1)
			require.NotNil(t, armed.Jobs[0].State.NextRunAtMS, "instrument: genuinely armed, not an empty scheduler")
			if shape == "18.40c damaged store" {
				before = []byte(`{"version":1,"jobs":[{"enabled":"invalid"}]}`)
				require.NoError(t, os.WriteFile(storePath, before, 0o600))
			}
			clock.advance(2 * time.Minute) // deliberately leave the armed occurrence uncollected
			provider := providers.LLMProvider(&restMockProvider{})
			reloadErr := handleConfigReload(context.Background(), al, cfg, &provider, rs, msgBus, true)
			assert.NoError(t, reloadErr, "r17 reload has no scheduler-load error path")
			assert.True(t, original == rs.TaskTrigger, "18.40: configuration reload must retain the exact scheduler instance; no Stop/reconstruct/Start")
			after, err := os.ReadFile(storePath)
			require.NoError(t, err)
			assert.Equal(t, before, after, "18.40 reload reads/writes no scheduler store, including damaged bytes and overdue armed state")
		})
	}
}

func TestTaskTrigger_BootRestorationPrecedesReconciliation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: filepath.Join(home, "workspace"), MaxTokens: 4096}, List: []config.AgentConfig{{ID: "mia"}}}}
	store := task.New(filepath.Join(home, "tasks"))
	rule, tz := "FREQ=DAILY;COUNT=3", "UTC"
	at := time.Now().Add(48 * time.Hour).Truncate(time.Second).UnixMilli()
	tk := &task.Task{Title: "valid recurring task", Status: task.StatusInbox, AgentID: "mia", WorkspaceID: "default", Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}}
	require.NoError(t, store.Create(tk))
	path := filepath.Join(home, "tasks_triggers", "jobs.json")
	seed := cron.NewCronService(path)
	_, err := seed.AddJobFull(cron.JobSpec{Kind: cron.KindTask, Name: "task:" + tk.ID, Message: tk.ID, AgentID: tk.AgentID, Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}})
	require.NoError(t, err)
	valid, err := os.ReadFile(path)
	require.NoError(t, err)
	broken := bytes.Replace(valid, []byte(`"enabled": true`), []byte(`"enabled": "invalid"`), 1)
	require.NotEqual(t, valid, broken, "instrument: damage a real enabled field")
	require.NoError(t, os.WriteFile(path, broken, 0o600))
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	stage := &setupAndStartServicesState{ctx: context.Background(), cfg: cfg, agentLoop: al, msgBus: msgBus, homePath: home}
	// 6b moved checked restoration into prepareTaskTrigger; its error is what
	// setupAndStartServices returns to the boot caller.
	err = stage.prepareTaskTrigger()
	var typeErr *json.UnmarshalTypeError
	require.ErrorAs(t, err, &typeErr, "18.39 checked restoration error must reach the production boot caller")
	assert.Equal(t, "string", typeErr.Value)
	assert.Equal(t, "bool", typeErr.Type.String())
	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, broken, after, "18.39 reconciliation must never repair/overwrite an unreadable store before checked restoration")
}
