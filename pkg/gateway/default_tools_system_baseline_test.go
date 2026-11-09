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

// SPEC.md Founder exclusion preserves the three system identities' c90310f1f
// role policies, with the explicit preview_document: deny addition required
// by office-preview AD-13/T-20. The fixture is literal spec data, never copied
// from the running seed. Global ceiling changes are not frozen here.
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
			assert.Equal(t, want, actual.Tools.Builtin.Policies, "SPEC.md Founder exclusion plus office-preview AD-13/T-20: %s policy must equal the literal baseline", id)
		})
	}
}
