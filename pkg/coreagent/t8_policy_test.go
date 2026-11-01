package coreagent_test

// Oracle: tool-parity r17 §16/D-7, §7.12 and scenarios 16.1–16.2,
// 19.6, 19.13, 21.9, 21.13. These are fresh-install tests, not migrations.
import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t8Fresh(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	require.True(t, coreagent.SeedConfig(cfg), "fresh installation must seed agents")
	return cfg
}

func t8Agent(t *testing.T, cfg *config.Config, id string) *config.AgentConfig {
	t.Helper()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			return &cfg.Agents.List[i]
		}
	}
	t.Fatalf("missing seeded agent %q", id)
	return nil
}

func t8Policy(cfg *config.Config, a *config.AgentConfig, name string) string {
	ceiling := map[string]config.ToolPolicy{}
	for k, v := range cfg.Sandbox.ToolPolicies {
		ceiling[k] = config.ToolPolicy(v)
	}
	p := &tools.ToolPolicyCfg{GlobalPolicies: ceiling}
	if a.Tools != nil {
		p.Policies = a.Tools.Builtin.Policies
	}
	return tools.EffectiveToolPolicy(p, tools.ScopeCore, string(a.Type), name)
}

func TestPolicyMatrix_FourTools(t *testing.T) {
	cfg := t8Fresh(t)
	for _, id := range []string{"mia", "jim", "planner", "researcher", "worker", "omnipus", "judge", "plansupervisor"} {
		t.Run(id, func(t *testing.T) {
			a := t8Agent(t, cfg, id)
			for _, name := range []string{"get_task", "get_plan", "get_goal", "update_plan"} {
				want := "allow"
				if id == "judge" || id == "plansupervisor" || id == "omnipus" && name != "get_task" || id == "worker" && name == "update_plan" {
					want = "deny"
				}
				assert.Equal(t, "allow", cfg.Sandbox.ToolPolicies[name], "§16 ceiling must cover %s", name)
				assert.Equal(t, want, t8Policy(cfg, a, name), "D-7 %s/%s", id, name)
			}
		})
	}
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "custom Main", true: "native and external worker"}[worker], func(t *testing.T) {
			a := config.AgentConfig{Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
			if worker {
				a.Type = config.AgentTypeWorker
				coreagent.RestrictForWorkerTier(a.Tools)
			}
			for _, name := range []string{"get_task", "get_plan", "get_goal", "update_plan"} {
				want := "allow"
				if worker && name == "update_plan" {
					want = "deny"
				}
				assert.Equal(t, want, t8Policy(cfg, &a, name), "D-7 new agent/%s", name)
			}
		})
	}
}

func TestGetAgent_EveryAgentAllowed(t *testing.T) {
	cfg := t8Fresh(t)
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		assert.Equal(t, "allow", t8Policy(cfg, a, "get_agent"), "19.6 including hidden system agents: %s", a.ID)
	}
	a := config.AgentConfig{Type: config.AgentTypeWorker, Tools: coreagent.NewCustomAgentToolsCfg()}
	coreagent.RestrictForWorkerTier(a.Tools)
	assert.Equal(t, "allow", t8Policy(cfg, &a, "get_agent"), "new worker must read Mia")
}

func TestOmnipusTaskTools_PolicyAndSelfTask(t *testing.T) {
	cfg := t8Fresh(t)
	a := t8Agent(t, cfg, "omnipus")
	for _, name := range []string{"list_tasks", "get_task", "create_task", "update_task", "run_task", "delete_task", "list_jobs"} {
		want := "allow"
		if name == "delete_task" {
			want = "ask"
		}
		assert.Equal(t, want, t8Policy(cfg, a, name), "21.9 effective %s", name)
		if a.Tools != nil {
			assert.NotEqual(t, config.ToolPolicyAsk, a.Tools.Builtin.Policies[name], "no per-agent ask stored: %s", name)
		}
	}
	// 21.13 is ordinary preservation after a fresh-install operator change.
	a.Tools.Builtin.Policies["create_task"] = config.ToolPolicyDeny
	before, err := json.Marshal(a.Tools)
	require.NoError(t, err)
	coreagent.SeedConfig(cfg)
	after, err := json.Marshal(t8Agent(t, cfg, "omnipus").Tools)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "restart may not rewrite the person's deny")
	assert.Equal(t, "deny", t8Policy(cfg, t8Agent(t, cfg, "omnipus"), "create_task"))
}

func TestToolPolicySeeds_NoRetiredNames(t *testing.T) {
	cfg := t8Fresh(t)
	for _, old := range []string{"get_agent_tools", "read_agent_metadata"} {
		_, present := cfg.Sandbox.ToolPolicies[old]
		assert.False(t, present, "19.13 ceiling contains retired %s", old)
		for _, a := range cfg.Agents.List {
			if a.Tools != nil {
				_, present = a.Tools.Builtin.Policies[old]
				assert.False(t, present, "19.13 %s has retired %s", a.ID, old)
			}
		}
	}
	before, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.False(t, coreagent.SeedConfig(cfg), "second start is idempotent")
	after, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestSeedConfig_OmnipusSkillsReconciled(t *testing.T) {
	cfg := t8Fresh(t)
	// r17 §7.12.3 names these three shipped workflow grants independently.
	// The complete current/retired list is not enumerated in the sole oracle;
	// its missing-list case below is loud rather than implementation-derived.
	current := []string{"create-agent", "change-agent", "capability-plan"}
	a := t8Agent(t, cfg, "omnipus")
	a.Skills = []string{"my-extra-skill"}
	mia := t8Agent(t, cfg, "mia")
	mia.Skills = []string{}
	coreagent.SeedConfig(cfg)
	got := t8Agent(t, cfg, "omnipus").Skills
	for _, name := range current {
		assert.Contains(t, got, name, "14.1 current assignment")
	}
	t.Run("14.1 complete retired and current grant list", func(t *testing.T) {
		t.Fatal("BLOCKED: r17 references revision 1's complete current/retired Omnipus skill lists without enumerating them — required by scenario 14.1; no implementation-derived list is used as an oracle")
	})
	assert.Contains(t, got, "my-extra-skill", "14.2 personal extra retained")
	assert.Empty(t, t8Agent(t, cfg, "mia").Skills, "14.4 other agents untouched")
	before, err := json.Marshal(cfg)
	require.NoError(t, err)
	coreagent.SeedConfig(cfg)
	after, err := json.Marshal(cfg)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "14.3 second pass must change nothing")
}
