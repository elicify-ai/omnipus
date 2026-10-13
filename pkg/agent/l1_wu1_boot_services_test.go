//go:build goolm && stdjson

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1BootAndReload_StateAndSkillsIndependentOfDefaultAgent(t *testing.T) {
	// FR-124 / BDD-13.6: actual NewAgentLoop and config reload publication,
	// actual file-backed state and shared skills. No agent owns these resources.
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	path := filepath.Join(home, "skills", "red-install-probe", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("---\nname: red-install-probe\ndescription: Install-wide positive fixture\n---\n# Shared fixture\n"), 0o600))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.List = nil // no agent can stand in for an install-level service
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), testutil.NewScenario())
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	t.Run("empty_registry_startup_positive_control", func(t *testing.T) {
		assert.Empty(t, al.GetRegistry().ListAgentIDs())
		assert.Empty(t, al.GetStartupInfo(), "§6.4: an empty registry gives explicit empty startup info, not a panic")
	})
	for _, phase := range []string{"boot", "reload"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "reload" {
				clone, err := cfg.Clone()
				require.NoError(t, err)
				al.SwapConfig(clone)
			}
			require.NoError(t, al.RecordLastChannel("telegram:red-control"), "install-wide channel recording must be writable without an agent")
			data, err := os.ReadFile(filepath.Join(home, "state", "state.json"))
			if assert.NoError(t, err, "FR-124: state belongs to cfg.AgentHomeBasePath(), never an agent's home") {
				var state map[string]any
				require.NoError(t, json.Unmarshal(data, &state))
				assert.Equal(t, "telegram:red-control", state["last_channel"])
			}
			found := 0
			for _, skill := range al.ListSkillsDetailed() {
				if skill.ID == "red-install-probe" {
					found++
					assert.Equal(t, "Install-wide positive fixture", skill.Description)
				}
			}
			assert.Equal(t, 1, found, "FR-124: installed global skill listing must not depend on any agent or grant")
		})
	}
}
