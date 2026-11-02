package agent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// persistSkillGrantFixture supplies the real r15 §5.13a prerequisite for
// existing positive activation tests. It is opt-in: writeSkill and genuinely
// unstamped negative fixtures remain unchanged. Names absent from disk stay
// unbound; installed names bind through actual agent persistence, not a loader
// stamp or a hand-written grant map.
func persistSkillGrantFixture(t *testing.T, home string, agentCfg config.AgentConfig) config.AgentConfig {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	store := agentstore.New(home)
	require.NoError(t, store.Create(agentCfg.ID, &agentCfg), "persist fixture agent and its observed grants")
	persisted, err := store.Get(agentCfg.ID)
	require.NoError(t, err)
	require.Equal(t, agentCfg.Skills, persisted.Skills, "persistence retains the requested names")
	for _, name := range agentCfg.Skills {
		key := strings.ToLower(name)
		dir := filepath.Join(home, "skills", key)
		_, statErr := os.Stat(filepath.Join(dir, "SKILL.md"))
		if errors.Is(statErr, os.ErrNotExist) {
			require.NotContains(t, persisted.SkillGrants, key, "an absent fixture name must stay unbound")
			continue
		}
		require.NoError(t, statErr)
		stamp, readErr := os.ReadFile(filepath.Join(dir, ".omnipus-identity"))
		require.NoError(t, readErr, "grant persistence stamps an installed fixture")
		generation := strings.TrimSpace(string(stamp))
		require.NotEmpty(t, generation)
		require.Equal(t, generation, persisted.SkillGrants[key], "the grant binds the actual installed generation")
	}
	return *persisted
}
