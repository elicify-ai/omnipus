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

// SPEC.md Founder exclusion: the three system identities keep their c90310f1f
// stored role policies, plus show_to_user deny. Chat-links D-F2 / BS-6.14:
// Omnipus does not hold this tool, and Judge and Plan Supervisor stay deny.
// The c90310f1f literals are unchanged; show_to_user is the one addition.
// This is not derived by copying a fresh seed run. Global ceiling changes
// are intentionally NOT frozen by this assertion.
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
			assert.Equal(t, want, actual.Tools.Builtin.Policies, "SPEC.md Founder exclusion: %s policy must equal the frozen baseline (c90310f1f plus show_to_user deny)", id)
		})
	}
}
