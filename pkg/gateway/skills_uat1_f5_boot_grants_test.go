package gateway

// UAT-1 F-5 (skills spec r15 §5.13a, SD-30): on a fresh install the seeded
// agents' grants of the built-in skills are bound to the built-in identity
// stamps, in the stored records AND in the in-memory roster, after the first
// boot and after a second boot from the saved config; a real Skill load by
// Mia then succeeds. A user-skill grant saved while its folder was absent
// stays unbound (SD-30).
//
// Real start-up: rc.loadConfigAndProvider runs seedAndPersistAgentRoster and
// bootSkills in the order boot does; nothing here writes a grant or a stamp.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func f5Boot(t *testing.T, home string) *runContextWithOptions {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", strings.Repeat("0", 64)) // synthetic fixture only
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rc := &runContextWithOptions{ctx: ctx, homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	if rc.credStore != nil {
		t.Cleanup(rc.credStore.Close)
	}
	require.NoError(t, err, "real credential/config/roster/skills start-up")
	require.False(t, stop)
	return rc
}

func f5Stamp(t *testing.T, home, key string) (string, bool) {
	t.Helper()
	dir := filepath.Join(home, "skills", key)
	if _, err := os.Stat(filepath.Join(dir, ".omnipus-builtin")); err != nil {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, ".omnipus-identity"))
	require.NoError(t, err, "an installed built-in carries its identity stamp: %s", key)
	return strings.TrimSpace(string(raw)), true
}

// f5AssertBound checks every seeded agent's grants of installed built-ins in
// the stored record and the in-memory roster; it returns how many it checked.
func f5AssertBound(t *testing.T, home string, cfg *config.Config, label string) int {
	t.Helper()
	store := agentstore.New(home)
	checked := 0
	for _, a := range cfg.Agents.List {
		stored, err := store.Get(a.ID)
		require.NoError(t, err, "%s: stored record for %s", label, a.ID)
		for _, name := range a.Skills {
			key := strings.ToLower(name)
			stamp, builtin := f5Stamp(t, home, key)
			if !builtin {
				continue
			}
			checked++
			assert.Equal(t, stamp, stored.SkillGrants[key], "%s: %s's stored grant of built-in %s is bound to the stamp on disk", label, a.ID, key)
			assert.Equal(t, stamp, a.SkillGrants[key], "%s: %s's in-memory grant of built-in %s is bound to the stamp on disk", label, a.ID, key)
		}
	}
	return checked
}

func TestFreshHomeTwoBoots_SeededAgentsAreBoundToBuiltinStamps(t *testing.T) {
	home := t.TempDir()
	// The home is set before DefaultConfig is built: the TestMain home-leak
	// guard fails a test that resolves the default home outside its own.
	t.Setenv(config.EnvHome, home)
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), config.DefaultConfig()))

	rc1 := f5Boot(t, home)
	n1 := f5AssertBound(t, home, rc1.cfg, "first boot")
	require.Positive(t, n1, "instrument: the seeded agents grant at least one installed built-in")

	// A user-skill grant saved while the folder is absent (SD-30: stays unbound).
	store := agentstore.New(home)
	_, err := store.Update("mia", func(a *config.AgentConfig) error {
		a.Skills = append(a.Skills, "f5-user-skill")
		return nil
	})
	require.NoError(t, err)
	before, err := store.Get("mia")
	require.NoError(t, err)
	require.Contains(t, before.Skills, "f5-user-skill")
	require.NotContains(t, before.SkillGrants, "f5-user-skill", "positive control: saved while absent, so unbound")

	rc2 := f5Boot(t, home)
	n2 := f5AssertBound(t, home, rc2.cfg, "second boot")
	require.Positive(t, n2)
	after, err := store.Get("mia")
	require.NoError(t, err)
	assert.Contains(t, after.Skills, "f5-user-skill", "the name is kept")
	assert.NotContains(t, after.SkillGrants, "f5-user-skill", "SD-30: an absent user skill's grant stays unbound across a boot")

	// A real Skill load by Mia of a built-in she is granted.
	rc2.provider = &restMockProvider{}
	err, stop := rc2.initializeAgentLoop()
	if rc2.agentLoop != nil {
		t.Cleanup(rc2.agentLoop.Close)
	}
	if rc2.liveLimits != nil {
		t.Cleanup(rc2.liveLimits.Close)
	}
	require.NoError(t, err)
	require.False(t, stop)
	inst, ok := rc2.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	var miaSkills []string
	for _, a := range rc2.agentLoop.GetConfig().Agents.List {
		if a.ID == "mia" {
			miaSkills = a.Skills
		}
	}
	meta, err := rc2.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	tool, ok := inst.Tools.Get("Skill")
	require.True(t, ok)
	loaded := 0
	for _, name := range miaSkills {
		if _, builtin := f5Stamp(t, home, strings.ToLower(name)); !builtin {
			continue
		}
		loaded++
		res := tool.Execute(tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "mia"), meta.ID), map[string]any{"name": name})
		assert.False(t, res.IsError, "Mia loads her granted built-in %s: %s", name, res.ForLLM)
		assert.NotContains(t, res.ForLLM, "SKILL_GRANT_STALE", name)
	}
	require.Positive(t, loaded, "Mia is granted at least one installed built-in")
}
