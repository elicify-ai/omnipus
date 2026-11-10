package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SPEC.md Founder exclusion: the three system identities keep exactly their
// c90310f1f stored role policies. This is a baseline-preservation oracle,
// derived from that frozen commit's literals, not from running the new seed.
// Global ceiling changes are intentionally NOT frozen by this assertion.
func defaultToolsAssertSystemBaseline(t *testing.T) {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "default-tools", "system-policy-baseline-c90310f1f.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var expected map[string]map[string]config.ToolPolicy
	require.NoError(t, json.Unmarshal(raw, &expected))
	require.Len(t, expected, 3, "Founder exclusion names exactly Omnipus, Judge and Plan Supervisor")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = t.TempDir()
	cfg.Agents.List = nil
	coreagent.SeedConfig(cfg)
	for _, id := range []string{"omnipus", "judge", "plansupervisor"} {
		t.Run(id, func(t *testing.T) {
			var actual *config.AgentConfig
			for i := range cfg.Agents.List {
				if cfg.Agents.List[i].ID == id {
					actual = &cfg.Agents.List[i]
					break
				}
			}
			require.NotNil(t, actual, "excluded system identity must still exist")
			require.NotNil(t, actual.Tools)
			want, ok := expected[id]
			require.True(t, ok, "frozen baseline must cover every excluded identity")
			assert.Equal(t, want, actual.Tools.Builtin.Policies, "SPEC.md Founder exclusion: %s policy must equal c90310f1f", id)
		})
	}
}
