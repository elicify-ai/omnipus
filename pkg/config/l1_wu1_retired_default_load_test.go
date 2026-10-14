package config_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestL1WU1ConfigLoad_OldDefaultAgentKeyIsInertAndWarnedOnEachLoad(t *testing.T) {
	// FR-120 / K17 / BDD-13.7: ignored with a warning, not an upgrade/migration.
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	path := filepath.Join(home, "config.json")
	body := fmt.Sprintf(`{"version":%d,"agents":{"defaults":{"default_agent_id":"mia","max_tool_iterations":200}}}`, config.CurrentVersion)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	logPath := filepath.Join(home, "load-log.jsonl")
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(logger.DisableFileLogging)
	logger.WarnC("l1-test-instrument", "known-warning-control")
	for _, pass := range []string{"initial_load", "reload"} {
		t.Run(pass, func(t *testing.T) {
			before, err := os.ReadFile(logPath)
			require.NoError(t, err)
			cfg, err := config.LoadConfig(path)
			require.NoError(t, err, "an ignored legacy key must not prevent load")
			raw, err := json.Marshal(cfg)
			require.NoError(t, err)
			var decoded map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &decoded))
			var agents map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(decoded["agents"], &agents))
			var defaults map[string]any
			require.NoError(t, json.Unmarshal(agents["defaults"], &defaults))
			assert.NotContains(t, defaults, "default_agent_id", "FR-120: old default selection must not survive in the effective config")
			after, err := os.ReadFile(logPath)
			require.NoError(t, err)
			assert.Contains(t, string(after), "known-warning-control", "log instrument is active")
			newLines := string(after[len(before):])
			assert.Contains(t, newLines, "default_agent_id", "each load must visibly name the ignored key")
			assert.Contains(t, strings.ToLower(newLines), "ignored", "the warning must say the setting is inert")
		})
	}
}
