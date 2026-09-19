package tools

import (
	"slices"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// ADR-090 environment setup spec, ES-FR-01/ES-BDD-08: per-role effective
// environment_setup policy resolved through the REAL compositor (the same
// resolver the agent-loop filter and the gateway approval gate share), against
// a real SeedConfig-produced roster — declared inventories are deliberately
// not consulted here. The policy registration itself is owned by the policy
// lane (pkg/coreagent seeds + pkg/config ceiling); this file proves
// resolution. Founder ruling 2026-09-18: BOTH levels ship ask — the stored
// per-agent asks are deliberate data, so a raised global ceiling does NOT
// loosen any permitted role.

// seededRosterPolicies builds a fresh config, seeds it, and returns each
// agent's (stored builtin policies, wire type) by ID — the exact data a real
// install persists and resolves against the shipped ceiling.
func seededRosterPolicies(t *testing.T) (map[string]map[string]config.ToolPolicy, map[string]string, map[string]config.ToolPolicy) {
	t.Helper()
	cfg := &config.Config{}
	if !coreagent.SeedConfig(cfg) {
		t.Fatal("fresh SeedConfig must seed the roster")
	}
	policies := map[string]map[string]config.ToolPolicy{}
	types := map[string]string{}
	for _, a := range cfg.Agents.List {
		pol := map[string]config.ToolPolicy{}
		if a.Tools != nil {
			for k, v := range a.Tools.Builtin.Policies {
				pol[k] = v
			}
		}
		policies[a.ID] = pol
		types[a.ID] = string(a.Type)
	}
	global := map[string]config.ToolPolicy{}
	for k, v := range config.DefaultConfig().Sandbox.ToolPolicies {
		global[k] = config.ToolPolicy(v)
	}
	return policies, types, global
}

// TestEnvironmentSetup_EffectivePolicyPerRole is the ES-BDD-08 per-role
// effective-policy check against the real compositor. Mia, General Purpose
// and Admin resolve Ask from their STORED per-agent asks merging against the
// ask ceiling (ask+ask → ask under strictest-wins); Jim, Ava, Planner,
// Researcher, Judge and Plan Supervisor resolve Deny from their stored
// entries.
func TestEnvironmentSetup_EffectivePolicyPerRole(t *testing.T) {
	policies, types, global := seededRosterPolicies(t)
	want := map[string]string{
		"mia":            "ask",
		"worker":         "ask",
		"admin":          "ask",
		"jim":            "deny",
		"ava":            "deny",
		"planner":        "deny",
		"researcher":     "deny",
		"judge":          "deny",
		"plansupervisor": "deny",
	}
	for id, wantPolicy := range want {
		stored, ok := policies[id]
		if !ok {
			t.Fatalf("seeded roster missing agent %q", id)
		}
		polCfg := &ToolPolicyCfg{Policies: stored, GlobalPolicies: global}
		got := EffectiveToolPolicy(polCfg, ScopeGeneral, types[id], "environment_setup")
		if got != wantPolicy {
			t.Errorf("effective environment_setup for %s = %q, want %q", id, got, wantPolicy)
		}
	}
}

// TestEnvironmentSetup_CustomStoredAskHoldsUnderCeilingChanges pins the
// founder ruling's consequence (2026-09-18): the custom constructor's
// per-agent ask is deliberate DATA, so raising the global ceiling does NOT
// loosen a new custom agent — the stored ask holds until the operator
// explicitly changes or removes it via the normal tool-policy edit paths.
// An explicit operator Deny still wins under any ceiling (strictest-wins).
func TestEnvironmentSetup_CustomStoredAskHoldsUnderCeilingChanges(t *testing.T) {
	stored := coreagent.NewCustomAgentToolsCfg().Builtin.Policies

	// Shipped posture: stored ask under the ask ceiling resolves Ask.
	askCeiling := map[string]config.ToolPolicy{"environment_setup": config.ToolPolicyAsk}
	got := EffectiveToolPolicy(&ToolPolicyCfg{Policies: stored, GlobalPolicies: askCeiling}, ScopeGeneral, "custom", "environment_setup")
	if got != "ask" {
		t.Fatalf("custom stored ask under ceiling ask = %q, want ask", got)
	}

	// Ceiling raised to allow: the stored per-agent ask HOLDS — this is the
	// intended both-levels posture, not a defect. The operator removes or
	// changes the per-agent entry to let the raised ceiling flow through.
	allowCeiling := map[string]config.ToolPolicy{"environment_setup": config.ToolPolicyAllow}
	got = EffectiveToolPolicy(&ToolPolicyCfg{Policies: stored, GlobalPolicies: allowCeiling}, ScopeGeneral, "custom", "environment_setup")
	if got != "ask" {
		t.Fatalf("custom stored ask under ceiling allow = %q, want ask (the per-agent ask is deliberate data and must not be silently loosened)", got)
	}

	// An explicit user Deny survives any ceiling (strictest-wins).
	withDeny := map[string]config.ToolPolicy{}
	for k, v := range stored {
		withDeny[k] = v
	}
	withDeny["environment_setup"] = config.ToolPolicyDeny
	got = EffectiveToolPolicy(&ToolPolicyCfg{Policies: withDeny, GlobalPolicies: allowCeiling}, ScopeGeneral, "custom", "environment_setup")
	if got != "deny" {
		t.Fatalf("explicit custom deny under ceiling allow = %q, want deny (user overrides must survive)", got)
	}
}

// TestEnvironmentSetup_RaisedCeilingLeavesStoredAsk pins the both-levels
// ruling's headline consequence for the seeded roles: if the operator raises
// the global ceiling to allow, Mia, General Purpose and Admin STILL resolve
// Ask — their stored per-agent asks are deliberate data that only an
// explicit operator change (or removal) lifts. The denied roles stay denied
// regardless.
func TestEnvironmentSetup_RaisedCeilingLeavesStoredAsk(t *testing.T) {
	policies, types, _ := seededRosterPolicies(t)
	allowCeiling := map[string]config.ToolPolicy{"environment_setup": config.ToolPolicyAllow}
	for _, id := range []string{"mia", "worker", "admin"} {
		stored, ok := policies[id]
		if !ok {
			t.Fatalf("seeded roster missing agent %q", id)
		}
		polCfg := &ToolPolicyCfg{Policies: stored, GlobalPolicies: allowCeiling}
		got := EffectiveToolPolicy(polCfg, ScopeGeneral, types[id], "environment_setup")
		if got != "ask" {
			t.Errorf("%s under a raised allow ceiling = %q, want ask (the stored per-agent ask must hold)", id, got)
		}
	}
	for _, id := range []string{"jim", "ava", "planner", "researcher"} {
		polCfg := &ToolPolicyCfg{Policies: policies[id], GlobalPolicies: allowCeiling}
		got := EffectiveToolPolicy(polCfg, ScopeGeneral, types[id], "environment_setup")
		if got != "deny" {
			t.Errorf("%s under a raised allow ceiling = %q, want deny (the stored deny must hold)", id, got)
		}
	}
}

// TestEnvironmentSetup_ManifestTierAndAdministrativeClassification pins the
// deferred-discovery posture (ES-FR-01: the 37-tool upfront set stays
// unchanged) and the ADR-071 §3.2.1 administrative classification (fable
// review GS-22): shared-scope installation alters install-wide state, so the
// tool is classified administrative — which narrows only ToolSearch's
// speculative cross-category promotion; it is not a policy mechanism and does
// not make the tool Admin-only.
func TestEnvironmentSetup_ManifestTierAndAdministrativeClassification(t *testing.T) {
	const name = "environment_setup"
	if got := ToolManifestTier(name); got != ManifestLazy {
		t.Errorf("ToolManifestTier(%q) = %v, want ManifestLazy (deferred discovery tier)", name, got)
	}
	if got := ToolManifestVisibility(name); got != ManifestSearchOnly {
		t.Errorf("ToolManifestVisibility(%q) = %v, want ManifestSearchOnly (no preview line)", name, got)
	}
	if IsFullManifestTool(name) {
		t.Errorf("%q must not be a full-manifest (upfront) tool", name)
	}
	if slices.Contains(FullManifestToolNames(), name) || slices.Contains(PreviewedLazyToolNames(), name) || slices.Contains(InfraManifestToolNames(), name) {
		t.Errorf("%q must not join any upfront manifest set — the 37-tool upfront surface stays unchanged", name)
	}
	if !slices.Contains(AdministrativeToolNames(), name) {
		t.Errorf("%q must be classified administrative (ADR-071 §3.2.1: shared-scope install alters install-wide state)", name)
	}
}
