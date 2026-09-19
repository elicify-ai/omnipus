package coreagent

import (
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ADR-090 environment setup spec (docs/internal/specs/adr-090-environment-setup-spec.md),
// ES-FR-01, founder ruling 2026-09-18: tool permissions are set at TWO levels
// — the global policy AND the per-agent policy — and for environment_setup
// BOTH levels ship "ask" for every agent permitted to set up environments.
// Concretely: the global ceiling carries an explicit ask; Mia, General
// Purpose (worker), Admin and NEW custom native agents carry an explicit
// stored per-agent ask (deliberate posture data — no bash/skill/agent-type
// derivation); Jim, Ava, Planner and Researcher carry an explicit persisted
// deny; Judge and Plan Supervisor stay locked deny via systemAgentSeed. A
// saved operator override at either level is preserved by existing machinery
// (ReconcileToolPolicyCeiling never overwrites; ordinary seeds never rewrite
// an existing agent's policies). Nothing here implements new permission
// logic — these tests pin configured DATA and the pre-existing resolver's
// behavior.

// TestEnvironmentSetup_InStaticCatalog pins ES-FR-01's registration in the
// static catalog (the deferred discovery tier follows automatically: the tool
// is ManifestLazy/ManifestSearchOnly — see pkg/tools' setup_tool_policy_test).
func TestEnvironmentSetup_InStaticCatalog(t *testing.T) {
	if !slices.Contains(allStaticToolNames, "environment_setup") {
		t.Fatal("environment_setup must be in allStaticToolNames (ES-FR-01: registered in the static catalog)")
	}
}

// TestEnvironmentSetup_GlobalCeilingShipsAsk pins level 1 (the global
// policy): the shipped ceiling carries an explicit environment_setup entry
// resolving Ask. The existing tool-approval mechanism is the approval — no
// new workflow.
func TestEnvironmentSetup_GlobalCeilingShipsAsk(t *testing.T) {
	ceiling := config.DefaultConfig().Sandbox.ToolPolicies
	got, ok := ceiling["environment_setup"]
	if !ok {
		t.Fatal("global ceiling must carry an explicit environment_setup entry (coverage)")
	}
	if got != string(config.ToolPolicyAsk) {
		t.Fatalf("global ceiling environment_setup = %q, want %q", got, string(config.ToolPolicyAsk))
	}
}

// TestEnvironmentSetup_FounderRoleMatrix pins the founder-confirmed per-role
// defaults in the declared inventory (ADR090RolePolicyInventory).
func TestEnvironmentSetup_FounderRoleMatrix(t *testing.T) {
	want := map[CoreAgentID]config.ToolPolicy{
		IDMia:            config.ToolPolicyAsk,
		IDWorker:         config.ToolPolicyAsk, // General Purpose
		IDAdmin:          config.ToolPolicyAsk,
		IDJim:            config.ToolPolicyDeny,
		IDAva:            config.ToolPolicyDeny,
		IDPlanner:        config.ToolPolicyDeny,
		IDResearcher:     config.ToolPolicyDeny,
		IDJudge:          config.ToolPolicyDeny, // locked (systemAgentSeed, re-enforced)
		IDPlanSupervisor: config.ToolPolicyDeny, // locked (systemAgentSeed, re-enforced)
	}
	for id, policy := range want {
		if got := ADR090RolePolicyInventory(id)["environment_setup"]; got != policy {
			t.Errorf("inventory %s environment_setup = %q, want %q", id, got, policy)
		}
	}
}

// TestEnvironmentSetup_PerAgentSeedsStoreExplicitPolicies pins level 2 (the
// per-agent policies) as PERSISTED DATA on a fresh install: the three Ask
// roles store an explicit "ask" of their own (deliberate posture — the entry
// is retained even though it equals the ceiling, so a later ceiling raise
// leaves them at Ask until the operator explicitly changes or removes it),
// and the four denied roles store an explicit "deny" (deny beats the ask
// ceiling under strictest-wins).
func TestEnvironmentSetup_PerAgentSeedsStoreExplicitPolicies(t *testing.T) {
	cfg := &config.Config{}
	if !SeedConfig(cfg) {
		t.Fatal("fresh SeedConfig must modify (seed) the config")
	}
	byID := map[string]config.AgentConfig{}
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a
	}
	askRoles := []string{string(IDMia), string(IDWorker), string(IDAdmin)}
	for _, id := range askRoles {
		a, ok := byID[id]
		if !ok {
			t.Fatalf("seeded roster missing %q", id)
		}
		if a.Tools == nil {
			t.Fatalf("%s has no tools config", id)
		}
		got, stored := a.Tools.Builtin.Policies["environment_setup"]
		if !stored {
			t.Fatalf("%s must STORE an explicit environment_setup ask (both-levels ruling: the per-agent entry is deliberate data, not an omission riding the ceiling)", id)
		}
		if got != config.ToolPolicyAsk {
			t.Errorf("%s environment_setup = %q, want ask", id, got)
		}
	}
	denied := []string{string(IDJim), string(IDAva), string(IDPlanner), string(IDResearcher)}
	for _, id := range denied {
		a, ok := byID[id]
		if !ok {
			t.Fatalf("seeded roster missing %q", id)
		}
		got, stored := a.Tools.Builtin.Policies["environment_setup"]
		if !stored {
			t.Fatalf("%s must carry an explicit persisted environment_setup Deny (ceiling is ask; absence would inherit ask)", id)
		}
		if got != config.ToolPolicyDeny {
			t.Errorf("%s environment_setup = %q, want deny", id, got)
		}
	}
}

// TestEnvironmentSetup_SavedOverridesSurviveReseed pins override
// preservation: an operator who changes a seeded role's environment_setup
// entry after creation (Ask → Deny here) keeps that value — the ordinary
// seeding loop applies sparse role policies only to agents that do not
// exist yet, never to stored policies of an existing agent.
func TestEnvironmentSetup_SavedOverridesSurviveReseed(t *testing.T) {
	cfg := &config.Config{}
	if !SeedConfig(cfg) {
		t.Fatal("fresh SeedConfig must seed the roster")
	}
	var mia *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDMia) {
			mia = &cfg.Agents.List[i]
		}
	}
	if mia == nil || mia.Tools == nil {
		t.Fatal("seeded roster missing Mia or her tools config")
	}
	mia.Tools.Builtin.Policies["environment_setup"] = config.ToolPolicyDeny
	if SeedConfig(cfg) && mia.Tools.Builtin.Policies["environment_setup"] != config.ToolPolicyDeny {
		t.Fatal("re-seed overwrote the operator's saved environment_setup deny")
	}
	if mia.Tools.Builtin.Policies["environment_setup"] != config.ToolPolicyDeny {
		t.Fatalf("saved override environment_setup = %q, want deny (saved overrides must be preserved)", mia.Tools.Builtin.Policies["environment_setup"])
	}
}

// TestEnvironmentSetup_HiddenAgentsLockedDeny pins ES-FR-01's "Judge / Plan
// Supervisor locked Deny": their capability sets are fixed and their whole
// tool policy is re-enforced from systemAgentSeed on every boot, so an
// operator (or tampered config) cannot unlock setup on them.
func TestEnvironmentSetup_HiddenAgentsLockedDeny(t *testing.T) {
	for _, id := range []CoreAgentID{IDJudge, IDPlanSupervisor} {
		got := systemAgentSeed(id)["environment_setup"]
		if got != config.ToolPolicyDeny {
			t.Errorf("systemAgentSeed(%s) environment_setup = %q, want deny", id, got)
		}
	}

	// Re-enforcement: a config where the Judge holds allow is corrected back
	// to the seeded deny on the next SeedConfig pass.
	cfg := &config.Config{}
	if !SeedConfig(cfg) {
		t.Fatal("fresh SeedConfig must seed")
	}
	reSeeded := false
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if a.ID != string(IDJudge) || a.Tools == nil {
			continue
		}
		a.Tools.Builtin.Policies["environment_setup"] = config.ToolPolicyAllow
		if SeedConfig(cfg) {
			reSeeded = true
		}
		if a.Tools.Builtin.Policies["environment_setup"] != config.ToolPolicyDeny {
			t.Fatal("Judge environment_setup allow must be re-enforced back to deny on boot (locked Deny)")
		}
	}
	if !reSeeded {
		t.Fatal("re-enforcement pass must report a modification (it corrected tampered policy)")
	}
}

// TestEnvironmentSetup_CustomConstructorStampsAsk pins the founder ruling's
// custom-agent half (2026-09-18, superseding the earlier omit-from-map
// approach): a new custom native agent gets an explicit stored per-agent
// "ask" — the same deliberate-posture data the permitted roles carry — from
// the ONE constructor all custom creation paths share, so the REST create
// route, the create_agent tool route and the get_agent_tools preview cannot
// drift. Nothing derives the value from bash, skills or agent type.
func TestEnvironmentSetup_CustomConstructorStampsAsk(t *testing.T) {
	policies := NewCustomAgentToolsCfg().Builtin.Policies
	got, stored := policies["environment_setup"]
	if !stored {
		t.Fatal("NewCustomAgentToolsCfg must stamp an explicit environment_setup entry — custom native agents carry the per-agent ask as data")
	}
	if got != config.ToolPolicyAsk {
		t.Fatalf("NewCustomAgentToolsCfg environment_setup = %q, want ask", got)
	}
}

// TestEnvironmentSetup_ConstructorOtherDefaultsUnchanged guards against
// scope creep: adding the environment_setup ask must not silently change any
// other custom-agent default.
func TestEnvironmentSetup_ConstructorOtherDefaultsUnchanged(t *testing.T) {
	policies := NewCustomAgentToolsCfg().Builtin.Policies
	unchanged := map[string]config.ToolPolicy{
		"bash":          config.ToolPolicyDeny,
		"grep":          config.ToolPolicyAllow,
		"ToolSearch":    config.ToolPolicyAllow,
		"Skill":         config.ToolPolicyAllow,
		"request_mount": config.ToolPolicyAsk,
		"read_file":     config.ToolPolicyAllow,
		"create_agent":  config.ToolPolicyDeny,
	}
	for name, want := range unchanged {
		got, ok := policies[name]
		if !ok {
			t.Errorf("custom constructor lost its %q entry", name)
			continue
		}
		if got != want {
			t.Errorf("custom constructor %q = %q, want %q (unrelated defaults must not change)", name, got, want)
		}
	}
}

// TestEnvironmentSetup_SparseRolePoliciesMatchInventory proves the sparse
// delta mechanism carries the new tool's inventory value into every ordinary
// role's persisted seed — INCLUDING the ceiling-equal asks, which the
// deliberate-posture list retains instead of pruning.
func TestEnvironmentSetup_SparseRolePoliciesMatchInventory(t *testing.T) {
	for _, id := range []CoreAgentID{IDMia, IDJim, IDAva, IDAdmin, IDPlanner, IDResearcher, IDWorker} {
		intended := ADR090RolePolicyInventory(id)["environment_setup"]
		sparse, stored := adr090SparseRolePolicies(id)["environment_setup"]
		if !stored {
			t.Fatalf("%s: environment_setup must be retained in the sparse seed (deliberate posture for the ask roles; persisted deny for the rest)", id)
		}
		if sparse != intended {
			t.Errorf("%s: sparse seed environment_setup = %q, want %q (the inventory value)", id, sparse, intended)
		}
	}
}

// TestEnvironmentSetup_PostureListRetainsCeilingEqualAsk pins the retention
// MECHANISM itself: with the shipped ceiling at ask, the ask roles' entries
// equal the ceiling — and must still be present in the sparse map. If this
// fails with "absent", the deliberate-posture list in adr090SparseRolePolicies
// lost environment_setup and the both-levels ruling silently regressed into
// ceiling-riding.
func TestEnvironmentSetup_PostureListRetainsCeilingEqualAsk(t *testing.T) {
	ceiling := config.DefaultConfig().Sandbox.ToolPolicies["environment_setup"]
	if ceiling != string(config.ToolPolicyAsk) {
		t.Fatalf("precondition: shipped ceiling environment_setup = %q, want ask", ceiling)
	}
	for _, id := range []CoreAgentID{IDMia, IDWorker, IDAdmin} {
		sparse, stored := adr090SparseRolePolicies(id)["environment_setup"]
		if !stored {
			t.Fatalf("%s: ceiling-equal environment_setup ask was pruned — the deliberate-posture list must retain it", id)
		}
		if sparse != config.ToolPolicyAsk {
			t.Errorf("%s: retained environment_setup = %q, want ask", id, sparse)
		}
	}
}
