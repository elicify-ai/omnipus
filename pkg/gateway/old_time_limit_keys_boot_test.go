package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestBoot_ConfigAndAgentEntityWithRetiredTimeLimitKeys is issue #1306's boot
// guarantee: the founder's live install carries agents.defaults.timeout_seconds,
// performance.delegation_timeout_minutes, schedules.run_timeout_seconds in
// config.json and a per-agent timeout_seconds in its agent entity files. The
// new binary must load all of it, put the agent on the roster and build a
// running agent loop — never refuse the old keys (#1293: a config the runtime
// refused made the gateway unbootable).
func TestBoot_ConfigAndAgentEntityWithRetiredTimeLimitKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspaceDir := filepath.Join(home, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))

	configPath := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(configPath, []byte(`{
  "version": 1,
  "agents": {"defaults": {"timeout_seconds": 900}},
  "performance": {"delegation_timeout_minutes": 30},
  "schedules": {"run_timeout_seconds": 300}
}`), 0o600))

	// An agent entity exactly as an older release wrote it: with its own
	// timeout_seconds.
	store := agentstore.New(home)
	require.NoError(t, store.Create("legacy-worker", &config.AgentConfig{ID: "legacy-worker", Name: "Legacy Worker"}))
	entityPath := filepath.Join(home, "entities", "agents", "legacy-worker.json")
	raw, err := os.ReadFile(entityPath)
	require.NoError(t, err)
	var entity map[string]any
	require.NoError(t, json.Unmarshal(raw, &entity))
	entity["timeout_seconds"] = 900
	legacyEntity, err := json.Marshal(entity)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(entityPath, legacyEntity, 0o600))

	cfg, err := config.LoadConfig(configPath)
	require.NoError(t, err, "config.json with retired time-limit keys must load")
	cfg.Agents.Defaults.Home = workspaceDir
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 4096

	require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, home),
		"an agent entity with a retired timeout_seconds must load onto the roster")
	found := false
	for _, a := range cfg.Agents.List {
		if a.ID == "legacy-worker" {
			found = true
		}
	}
	require.True(t, found, "legacy-worker missing from the roster: %+v", cfg.Agents.List)

	loop, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &restMockProvider{})
	if loop != nil {
		t.Cleanup(loop.Close)
	}
	require.NoError(t, err, "the agent loop must build from a config with retired time-limit keys")
	_, ok := loop.GetRegistry().GetAgent("legacy-worker")
	require.True(t, ok, "legacy-worker must be a running agent")
}
