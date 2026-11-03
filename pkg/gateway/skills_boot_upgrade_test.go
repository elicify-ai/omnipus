package gateway

// In-place upgrade from a build with no skill bindings (68ac1a01a) to one with
// them: the first start must leave every built-in grant usable, in the stored
// record AND in the live in-memory roster the agents are built from. Drives
// the same two calls loadConfigAndProvider makes, in order:
// seedAndPersistAgentRoster, then bootSkills.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

func upgradeBootEnv(t *testing.T) (home, configPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	old := skillsBootReport
	t.Cleanup(func() { skillsBootReport = old })
	return home, filepath.Join(home, "config.json")
}

func upgradeFreshConfig(t *testing.T, home, configPath string) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	require.NoError(t, config.SaveConfig(configPath, cfg))
	return cfg
}

// builtinSkillsOf returns the built-in names in a.Skills.
func builtinSkillsOf(a *config.AgentConfig) []string {
	var out []string
	for _, n := range a.Skills {
		if skills.IsBuiltin(n) {
			out = append(out, n)
		}
	}
	return out
}

// grantUsable is the Skill tool's identity predicate
// (pkg/agent/skill_identity.go::grantedPredicate): the binding for the key
// equals the installed folder's current generation, which is non-empty.
func grantUsable(t *testing.T, root string, a *config.AgentConfig, name string) bool {
	t.Helper()
	gen, err := skills.ReadGeneration(filepath.Join(root, name))
	require.NoError(t, err)
	return gen != "" && a.SkillGrants[skills.SkillKey(name)] == gen
}

func TestBootUpgrade_FirstStartBindsBuiltinGrantsOnDiskAndInMemory(t *testing.T) {
	home, configPath := upgradeBootEnv(t)
	root := filepath.Join(home, "skills")

	// The 68ac1a01a state: records carry built-in skill names and no
	// skill_grants. Seeding a fresh home with no skill folders installed
	// persists exactly that (an absent name persists unbound, §5.13a).
	require.NoError(t, seedAndPersistAgentRoster(upgradeFreshConfig(t, home, configPath), home, configPath))
	// The release also changed core-agent identity text, so the upgraded
	// start's SeedConfig touches (and persists) those records. Folders are
	// not installed yet, so this write stays unbound too.
	store := agentstore.New(home)
	stale, _, err := store.List()
	require.NoError(t, err)
	for i := range stale {
		_, err = store.Update(stale[i].ID, func(a *config.AgentConfig) error {
			a.Description = "stale description from the previous release"
			return nil
		})
		require.NoError(t, err)
	}
	// The old build had the built-in folders on disk, without identity stamps.
	_, err = skills.SyncBuiltins(root, nil)
	require.NoError(t, err)
	stamps, err := filepath.Glob(filepath.Join(root, "*", ".omnipus-identity"))
	require.NoError(t, err)
	for _, s := range stamps {
		require.NoError(t, os.Remove(s))
	}
	before, skipped, err := store.List()
	require.NoError(t, err)
	require.Empty(t, skipped)
	withBuiltins := 0
	for i := range before {
		if len(builtinSkillsOf(&before[i])) > 0 {
			withBuiltins++
		}
		require.Empty(t, before[i].SkillGrants, "setup: agent %q must start unbound", before[i].ID)
	}
	require.NotZero(t, withBuiltins, "setup: some stored agent must hold a built-in skill")

	// First start of the upgraded binary: a new process, a fresh config.
	cfg := upgradeFreshConfig(t, home, configPath)
	require.NoError(t, seedAndPersistAgentRoster(cfg, home, configPath))
	_, err = bootSkills(cfg, home)
	require.NoError(t, err)

	disk, _, err := store.List()
	require.NoError(t, err)
	checked := 0
	for i := range disk {
		d := &disk[i]
		var mem *config.AgentConfig
		for j := range cfg.Agents.List {
			if cfg.Agents.List[j].ID == d.ID {
				mem = &cfg.Agents.List[j]
			}
		}
		require.NotNil(t, mem, "agent %q is in the in-memory roster", d.ID)
		for _, name := range builtinSkillsOf(d) {
			if _, statErr := os.Stat(filepath.Join(root, name)); statErr != nil {
				continue // not an installed built-in: nothing to bind
			}
			checked++
			assert.True(t, grantUsable(t, root, d, name),
				"stored agent %q: built-in grant %q must be bound to the installed generation", d.ID, name)
			assert.True(t, grantUsable(t, root, mem, name),
				"in-memory agent %q: built-in grant %q must be bound to the installed generation (SKILL_GRANT_STALE otherwise)", d.ID, name)
		}
	}
	require.NotZero(t, checked, "control: at least one installed built-in grant was checked")
}

func TestBootUpgrade_AlreadyBoundHomeStaysByteIdentical(t *testing.T) {
	home, configPath := upgradeBootEnv(t)
	root := filepath.Join(home, "skills")

	// Settle a home: install built-ins, then boot until bound on disk and in memory.
	_, err := skills.SyncBuiltins(root, nil)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		cfg := upgradeFreshConfig(t, home, configPath)
		require.NoError(t, seedAndPersistAgentRoster(cfg, home, configPath))
		_, err = bootSkills(cfg, home)
		require.NoError(t, err)
	}

	snapshot := func() map[string]string {
		out := map[string]string{}
		for _, glob := range []string{
			filepath.Join(home, "entities", "agents", "*.json"),
			filepath.Join(root, "*", ".omnipus-identity"),
		} {
			paths, gerr := filepath.Glob(glob)
			require.NoError(t, gerr)
			for _, p := range paths {
				b, rerr := os.ReadFile(p)
				require.NoError(t, rerr)
				out[p] = string(b)
			}
		}
		return out
	}
	diskBefore := snapshot()
	require.NotEmpty(t, diskBefore, "control: snapshot sees agent records")

	cfg := upgradeFreshConfig(t, home, configPath)
	require.NoError(t, seedAndPersistAgentRoster(cfg, home, configPath))
	_, err = bootSkills(cfg, home)
	require.NoError(t, err)
	assert.Equal(t, diskBefore, snapshot(), "a settled home's records and stamps do not change on another start")

	checked := 0
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		for _, name := range builtinSkillsOf(a) {
			if _, statErr := os.Stat(filepath.Join(root, name)); statErr != nil {
				continue
			}
			checked++
			assert.True(t, grantUsable(t, root, a, name), "in-memory %q grant %q stays usable", a.ID, name)
		}
	}
	require.NotZero(t, checked, "control: at least one in-memory built-in grant was checked")
}
