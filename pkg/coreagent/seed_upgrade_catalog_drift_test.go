// Omnipus — VERDICT finding #7 regression lock: on an UPGRADED install the
// global "allow" ceiling silently granted all six ADR-068 knowledge tools to
// every agent that already existed in config.json.
//
// Why this file exists as its own suite, and why every case loads a real
// config.json from disk rather than starting from config.DefaultConfig():
//
// The defect is invisible on a fresh install. SeedConfig's fresh-seed path
// writes a fully-enumerated per-agent policy map, so the global ceiling is a
// ceiling exactly as pkg/config/defaults.go's comment claims. On an UPGRADE,
// the agents are already in the file; SeedConfig's existing-agent
// re-enforcement loop used to leave their tool-policy maps untouched; the six
// tool names are new, so no agent named them; and
// pkg/tools.resolveEffectivePolicyWith's `case a == "": return g` resolves a
// tool with a global entry and NO agent entry to the GLOBAL value. Every
// pre-existing agent therefore resolved "allow" on all six — including the
// four delegation-only subagents whose seed intends "deny" on all six, and
// Mia/Ray/Ava, whose seed intends "ask" on the three writes.
//
// config.ValidateToolPolicyCoverage does not catch it: it counts a global
// entry as coverage (deliberately — the Worker's sparse seed map depends on
// exactly that inheritance), so it reports no gap and
// RepairIncompleteToolPolicyCoverage never backfills anything.
//
// A test that only builds config.DefaultConfig() in memory passes against the
// bug, so each case here writes a config.json in the shape the PREVIOUS
// release left on disk (the current seed minus the six names that did not
// exist yet, in both the agent maps and the global ceiling), loads it through
// the real config.LoadConfig, and resolves through the real production
// compositor (tools.ResolveEffectivePolicy).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// adr068KnowledgeTools is the six-name family ADR-068 introduced — the tools
// that did not exist in the release that wrote the config.json each case here
// simulates.
var adr068KnowledgeTools = []string{
	"knowledge_describe",
	"knowledge_find",
	"knowledge_read",
	"knowledge_edit",
	"knowledge_restructure",
	"knowledge_configure",
}

// writePreADR068Home materialises an $OMNIPUS_HOME in the shape the PREVIOUS
// release left on disk, and returns its path.
//
// It reproduces both halves of a real install, because since ADR-054 the two
// live in different files and the finding needs both:
//
//   - config.json — carries the GLOBAL ceiling (sandbox.tool_policies) and
//     nothing else relevant here; AgentsConfig.List is json:"-", so the roster
//     is structurally absent from it.
//   - entities/agents/<id>.json — the per-agent records, written through the
//     real pkg/agentstore, each carrying that release's own fully-enumerated
//     tools.builtin.policies map.
//
// Both are built by taking the current fresh-install seed and REMOVING every
// trace of the six ADR-068 names — from each agent record and from the global
// ceiling alike. That is precisely what an older binary wrote: the names did
// not exist, so nothing enumerated them.
//
// extra agents (an operator-created agent from that release, say) are written
// to the store alongside the seeded roster.
func writePreADR068Home(t *testing.T, extra ...config.AgentConfig) string {
	t.Helper()

	home := t.TempDir()

	cfg := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(cfg), "fresh seed must populate the roster")

	store := agentstore.New(home)
	for i := range cfg.Agents.List {
		ac := cfg.Agents.List[i]
		if ac.Tools != nil {
			for _, name := range adr068KnowledgeTools {
				delete(ac.Tools.Builtin.Policies, name)
			}
		}
		require.NoError(t, store.Create(ac.ID, &ac))
	}
	for i := range extra {
		ac := extra[i]
		require.NoError(t, store.Create(ac.ID, &ac))
	}

	for _, name := range adr068KnowledgeTools {
		delete(cfg.Sandbox.ToolPolicies, name)
	}
	cfg.Agents.List = nil

	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), data, 0o600))

	return home
}

// loadUpgraded performs the boot sequence pkg/gateway performs on an upgrade,
// up to (but not including) coreagent.SeedConfig: load config.json through the
// real loader, then populate cfg.Agents.List from the entity store — the same
// two steps as gateway.go's config.LoadConfig +
// populateAgentsListFromEntityStoreStrict pair.
//
// It then asserts the two preconditions the finding rests on, so a case that
// goes green can never do so because the setup stopped modelling an upgrade:
//
//  1. the CURRENT binary's DefaultConfig() reintroduces all six global
//     ceilings at "allow", and encoding/json merges the old file's own
//     sandbox.tool_policies INTO that non-nil map rather than replacing it,
//     so all six survive the load; and
//  2. not one agent record read from the store carries a per-agent entry for
//     any of the six.
func loadUpgraded(t *testing.T, home string) *config.Config {
	t.Helper()

	cfg, err := config.LoadConfig(filepath.Join(home, "config.json"))
	require.NoError(t, err)

	agents, skipped, err := agentstore.New(home).List()
	require.NoError(t, err)
	require.Empty(t, skipped, "no agent record written by the setup may fail to parse")
	require.NotEmpty(t, agents, "the upgrade roster must be non-empty")
	cfg.Agents.List = agents

	for _, name := range adr068KnowledgeTools {
		require.Equalf(t, "allow", cfg.Sandbox.ToolPolicies[name],
			"precondition: the current binary's default global ceiling for %q must survive "+
				"the merge over the old file at \"allow\" — without it this case no longer "+
				"reproduces the finding", name)
	}
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if ac.Tools == nil {
			continue
		}
		for _, name := range adr068KnowledgeTools {
			_, present := ac.Tools.Builtin.Policies[name]
			require.Falsef(t, present,
				"precondition: agent %q must arrive from the old store with NO entry for %q",
				ac.ID, name)
		}
	}
	return cfg
}

// findUpgradedAgent returns the named agent from cfg, failing the test when absent.
func findUpgradedAgent(t *testing.T, cfg *config.Config, id string) *config.AgentConfig {
	t.Helper()
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == id {
			return &cfg.Agents.List[i]
		}
	}
	t.Fatalf("agent %q not found in config", id)
	return nil
}

// resolveUpgraded resolves one (agent, tool) pair through the REAL production
// compositor merge — the same strictest-wins global x agent resolution the
// agent loop and the gateway approval hook both go through — rather than by
// inspecting the raw seed maps.
func resolveUpgraded(t *testing.T, cfg *config.Config, agentID, toolName string) string {
	t.Helper()
	ac := findUpgradedAgent(t, cfg, agentID)
	var perAgent map[string]config.ToolPolicy
	if ac.Tools != nil {
		perAgent = ac.Tools.Builtin.Policies
	}
	global := make(map[string]config.ToolPolicy, len(cfg.Sandbox.ToolPolicies))
	for k, v := range cfg.Sandbox.ToolPolicies {
		global[k] = config.ToolPolicy(v)
	}
	return tools.ResolveEffectivePolicy(&tools.ToolPolicyCfg{
		Policies:       perAgent,
		GlobalPolicies: global,
	}, toolName)
}

// customAgentFromOlderRelease returns an operator-created agent as the
// PREVIOUS release's POST /api/v1/agents handler would have persisted it:
// coreagent.NewCustomAgentToolsCfg()'s fully-enumerated deny-by-default map,
// minus the six names that release's catalog did not contain.
func customAgentFromOlderRelease(id string) config.AgentConfig {
	toolsCfg := coreagent.NewCustomAgentToolsCfg()
	for _, name := range adr068KnowledgeTools {
		delete(toolsCfg.Builtin.Policies, name)
	}
	return config.AgentConfig{
		ID:    id,
		Name:  "Notes Bot",
		Type:  config.AgentTypeCustom,
		Tools: toolsCfg,
	}
}

// TestUpgrade_KnowledgeTools_ResolveToTheSeededPosture_NotTheGlobalCeiling is
// the finding-#7 lock. After an upgrade boot (load the old file, run
// SeedConfig) every agent must resolve each of the six knowledge tools to the
// posture its own seed states — NOT to the global ceiling's "allow".
//
// The expectation matrix is read off the seed's stated intent
// (coreAgentSeed's "SEED RULE — KNOWLEDGE POSTURE" and each agent's case),
// never off the resolver's output.
func TestUpgrade_KnowledgeTools_ResolveToTheSeededPosture_NotTheGlobalCeiling(t *testing.T) {
	const customID = "notes-bot"
	home := writePreADR068Home(t, customAgentFromOlderRelease(customID))
	cfg := loadUpgraded(t, home)

	coreagent.SeedConfig(cfg)

	const (
		allow = "allow"
		ask   = "ask"
		deny  = "deny"
	)
	readTier := []string{"knowledge_describe", "knowledge_find", "knowledge_read"}
	writeTier := []string{"knowledge_edit", "knowledge_restructure", "knowledge_configure"}

	cases := []struct {
		agentID string
		read    string
		write   string
		why     string
	}{
		// Jim — the Orchestrator: the one seeded exception, allow on all six.
		{string(coreagent.IDJim), allow, allow, "Jim's seed grants all six unprompted"},
		// The three other base agents: retrieval allow, authoring ask.
		{string(coreagent.IDMia), allow, ask, "Mia's seed gates the three writes behind ask"},
		{string(coreagent.IDAva), allow, ask, "Ava's seed gates the three writes behind ask"},
		{string(coreagent.IDRay), allow, ask, "Ray's seed gates the three writes behind ask"},
		// The delegation-only tier: deny on all six. The Worker reaches it
		// through an EXPLICIT deny in its otherwise-sparse map precisely
		// because an absent key there inherits the global "allow".
		{string(coreagent.IDWorker), deny, deny, "the Worker's sparse seed denies all six explicitly"},
		{string(coreagent.IDPlanner), deny, deny, "the specialist tier is denied all six"},
		{string(coreagent.IDExplorer), deny, deny, "the specialist tier is denied all six"},
		{string(coreagent.IDResearcher), deny, deny, "the specialist tier is denied all six"},
		// System agents: already safe before this fix (seedSystemAgents
		// re-enforces their exact seeded map on EVERY boot), asserted here so
		// a change to that re-enforcement cannot silently open them.
		{string(coreagent.IDJudge), deny, deny, "a System Agent carries exactly its seeded verifier set"},
		{string(coreagent.IDPlanSupervisor), deny, deny, "a System Agent carries exactly its seeded set"},
		// An operator-created agent from the older release. It has no seed to
		// consult, so the only defensible value for a name its enumeration
		// never covered is the deny-by-default baseline every creation path
		// in this codebase writes (coreagent.NewCustomAgentToolsCfg).
		{customID, deny, deny, "a custom agent's catalog drift backfills to the deny baseline"},
	}

	for _, tc := range cases {
		for _, tool := range readTier {
			assert.Equalf(t, tc.read, resolveUpgraded(t, cfg, tc.agentID, tool),
				"(%s, %s) after an upgrade boot — %s", tc.agentID, tool, tc.why)
		}
		for _, tool := range writeTier {
			assert.Equalf(t, tc.write, resolveUpgraded(t, cfg, tc.agentID, tool),
				"(%s, %s) after an upgrade boot — %s", tc.agentID, tool, tc.why)
		}
	}
}

// TestUpgrade_KnowledgeTools_EveryAgentCarriesAnExplicitEntry asserts the
// other half of CLAUDE.md hard constraint 6: after the upgrade boot the
// posture is not merely correct at resolution time, it is EXPLICIT, literal
// and per-agent in the persisted config — so an operator reading config.json
// (or the Agents -> Tools screen, which renders this map) sees the real
// posture instead of an absent key that silently resolves from the ceiling.
func TestUpgrade_KnowledgeTools_EveryAgentCarriesAnExplicitEntry(t *testing.T) {
	const customID = "notes-bot"
	home := writePreADR068Home(t, customAgentFromOlderRelease(customID))
	cfg := loadUpgraded(t, home)

	coreagent.SeedConfig(cfg)

	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		require.NotNilf(t, ac.Tools, "agent %q must carry a tools config after the upgrade boot", ac.ID)
		for _, name := range adr068KnowledgeTools {
			got, present := ac.Tools.Builtin.Policies[name]
			require.Truef(t, present,
				"agent %q has no explicit entry for %q after the upgrade boot — it would resolve "+
					"from the global ceiling, which is the silent runtime default hard constraint 6 bans",
				ac.ID, name)
			assert.Containsf(t, []config.ToolPolicy{
				config.ToolPolicyAllow, config.ToolPolicyAsk, config.ToolPolicyDeny,
			}, got, "agent %q entry for %q must be one of allow/ask/deny", ac.ID, name)
		}
	}
}

// TestUpgrade_OperatorPolicyEditIsNeverOverwritten guards the fix from the
// opposite failure: the backfill fills GAPS only. An operator who deliberately
// granted Mia unprompted knowledge_edit on the older install keeps that grant
// across the upgrade — a migration that re-imposed the seed would silently
// revert operator decisions on every boot.
func TestUpgrade_OperatorPolicyEditIsNeverOverwritten(t *testing.T) {
	home := writePreADR068Home(t)
	// The operator's own edit, as the older release persisted it into Mia's
	// entity record.
	store := agentstore.New(home)
	_, err := store.Update(string(coreagent.IDMia), func(ac *config.AgentConfig) error {
		ac.Tools.Builtin.Policies["read_file"] = config.ToolPolicyAsk
		return nil
	})
	require.NoError(t, err)
	cfg := loadUpgraded(t, home)

	coreagent.SeedConfig(cfg)

	mia := findUpgradedAgent(t, cfg, string(coreagent.IDMia))
	assert.Equal(t, config.ToolPolicyAsk, mia.Tools.Builtin.Policies["read_file"],
		"the operator's own read_file=ask must survive the upgrade backfill unchanged")
	assert.Equal(t, "ask", resolveUpgraded(t, cfg, string(coreagent.IDMia), "read_file"),
		"and must still resolve ask through the real compositor")
}

// TestUpgrade_WorkerSparseCeilingInheritanceIsPreserved guards the fix from
// over-tightening. The Worker's seed map is SPARSE by deliberate,
// operator-confirmed design (coreAgentSeed's IDWorker branch, via
// tightenGlobalCeiling): every tool it does NOT name is meant to track the
// global ceiling. "delegate" is the documented example — the Worker leaves it
// absent so it inherits the ceiling's "allow". A migration that backfilled
// every absent catalog name to "deny" would silently retire that design.
func TestUpgrade_WorkerSparseCeilingInheritanceIsPreserved(t *testing.T) {
	home := writePreADR068Home(t)
	cfg := loadUpgraded(t, home)

	coreagent.SeedConfig(cfg)

	worker := findUpgradedAgent(t, cfg, string(coreagent.IDWorker))
	_, present := worker.Tools.Builtin.Policies["delegate"]
	assert.False(t, present,
		"the Worker's seed deliberately leaves \"delegate\" absent so it inherits the global "+
			"ceiling — the upgrade backfill must not write an entry the seed does not name")
	assert.Equal(t, "allow", resolveUpgraded(t, cfg, string(coreagent.IDWorker), "delegate"),
		"and it must still resolve to the ceiling's allow")
}
