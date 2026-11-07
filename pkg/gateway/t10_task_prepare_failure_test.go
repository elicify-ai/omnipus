package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The task boot preparation now runs inside startSchedulers (T10 FR-03), so
// the two gateway boot tests no longer assert its result on its own (CHECK2
// F6). This is the behavioural replacement: when the preparation fails, the
// boot stops with that error and the shared engine is never activated, so no
// timer of any kind is collected on an unprepared store.
func TestT10_TaskBootPreparationFailureStopsBootBeforeActivation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
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

	// The task store cannot be listed: its directory is a plain file, so the
	// preparation's task read fails with an error other than "not found".
	tasksDir := filepath.Join(home, "tasks")
	require.NoError(t, os.RemoveAll(tasksDir))
	require.NoError(t, os.WriteFile(tasksDir, []byte("not a directory"), 0o600))

	stage := &setupAndStartServicesState{
		ctx: context.Background(), cfg: cfg, agentLoop: al, msgBus: msgBus, homePath: home,
	}
	var stages []string
	stage.SetSchedulerBootObserverForTest(func(name string, _ *cron.CronService) { stages = append(stages, name) })
	_, stop, err := stage.startSchedulers()
	if stage.runningServices != nil {
		defer stopAndCleanupServices(stage.runningServices, 5*time.Second, false)
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "error preparing task trigger scheduler")
	assert.True(t, stop, "a failed task boot preparation stops the boot")
	assert.Equal(t, []string{"checked_load"}, stages,
		"the boot stops right after the checked load: no crash-flag clear, no reconciliation, no activation")
}
