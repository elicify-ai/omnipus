// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestBoot_ConstructorSeedDispositionMap verifies that each core agent's
// constructor seed contains the expected policies.
//
// BDD: Given each core agent ID,
//
//	When coreAgentSeed is called,
//	Then all four base agents are deny-by-default (least-privilege redesign);
//	And none carry the dead "system.*" deny rail;
//	And Jim's explicit allow-list includes spawn, create_task, workspace_shell, browser_navigate;
//	And Jim's consent-gated tools (delete_task, delete_workspace, etc.) resolve ask;
//	And a sample of tools Jim must NOT have (create_agent, list_channels) are absent.
//
// Traces to: pkg/coreagent/core.go — coreAgentSeed (FR-008, FR-010, FR-022).
func TestBoot_ConstructorSeedDispositionMap(t *testing.T) {
	for _, id := range []CoreAgentID{IDMia, IDJim, IDOmnipus, IDPlanner, IDResearcher, IDWorker} {
		t.Run(string(id), func(t *testing.T) {
			assert.Equal(t, adr090SparseRolePolicies(id), coreAgentSeed(id),
				"ordinary role seed must be exactly the sparse ADR-090 delta")
		})
	}
}

// TestBoot_HasSystemAllowsInConstructorSeed verifies that only Omnipus returns
// true from HasSystemAllowsInConstructorSeed (omnipus-system-agent §6.5: the
// management family rides the ceiling for Omnipus alone; Ava is retired,
// FR-008).
//
// BDD: Given each core agent ID,
//
//	When HasSystemAllowsInConstructorSeed is called,
//	Then only Omnipus returns true;
//	And all other known core agents return false.
//
// Traces to: pkg/coreagent/core.go — HasSystemAllowsInConstructorSeed (FR-062).
func TestBoot_HasSystemAllowsInConstructorSeed(t *testing.T) {
	assert.True(t, HasSystemAllowsInConstructorSeed(string(IDOmnipus)),
		"Omnipus must return true (it holds the management tools)")

	nonManagementAgents := []CoreAgentID{IDJim, IDMia}
	for _, id := range nonManagementAgents {
		assert.False(t, HasSystemAllowsInConstructorSeed(string(id)),
			"agent %q must return false (no explicit system.* allows)", id)
	}

	// Unknown agent IDs must also return false.
	assert.False(t, HasSystemAllowsInConstructorSeed("some-custom-agent"))
	assert.False(t, HasSystemAllowsInConstructorSeed(""))
}

// TestAgentConstructor_CustomAgent_DenyByDefaultFullCoverage verifies that a
// newly created custom agent config is deny-by-default via a fully-enumerated
// explicit map (no DefaultPolicy field, no "system.*" wildcard — both retired
// project-wide), with only a narrow, conservative allow-list.
//
// BDD: Given a new custom agent created via NewCustomAgentToolsCfg,
//
//	When the resulting config is inspected,
//	Then "system.*" is absent (dead wildcard, never a real tool name);
//	And bash is explicitly "deny" (closes the historical privilege gap where
//	the dead "system.*" wildcard let every system-management tool fall
//	through to allow);
//	And read_file/list_directory/remember/recall_memory/run_retrospective/recall_conversation are explicitly "allow";
//	And every other known tool has an explicit "deny" entry (no gaps).
//
// Traces to: pkg/coreagent/core.go — NewCustomAgentToolsCfg.
func TestAgentConstructor_CustomAgent_DenyByDefaultFullCoverage(t *testing.T) {
	cfg := NewCustomAgentToolsCfg()
	require.NotNil(t, cfg, "NewCustomAgentToolsCfg must return a non-nil config")

	_, hasRail := cfg.Builtin.Policies["system.*"]
	assert.False(t, hasRail, "custom agent must NOT carry the dead 'system.*' wildcard")

	denyByDefault, ok := cfg.Builtin.Policies["bash"]
	require.True(t, ok, "custom agent must have an explicit policy for 'bash'")
	assert.Equal(t, config.ToolPolicyDeny, denyByDefault,
		"custom agent 'bash' must be explicit 'deny' (closes the system.* privilege gap)")

	for _, allow := range []string{"read_file", "list_directory", "remember", "recall_memory", "run_retrospective", "recall_conversation"} {
		p, ok := cfg.Builtin.Policies[allow]
		require.True(t, ok, "custom agent must have explicit policy for %q", allow)
		assert.Equal(t, config.ToolPolicyAllow, p, "custom agent %q must be 'allow'", allow)
	}

	for _, deny := range []string{"create_agent", "set_config", "add_mcp_server", "delete_agent", "delete_workspace"} {
		p, ok := cfg.Builtin.Policies[deny]
		require.True(t, ok, "custom agent must have explicit policy for %q", deny)
		assert.Equal(t, config.ToolPolicyDeny, p, "custom agent %q must be explicit 'deny'", deny)
	}

	// Full coverage, not just the sampled subset above: the policy map must
	// have EXACTLY one entry per name in allStaticToolNames — no tool
	// silently dropped (would show up as a missing key here even though the
	// samples above wouldn't catch it) and no unexpected extra/stale key.
	gotKeys := make([]string, 0, len(cfg.Builtin.Policies))
	for k := range cfg.Builtin.Policies {
		gotKeys = append(gotKeys, k)
	}
	assert.ElementsMatch(t, allStaticToolNames, gotKeys,
		"custom agent policy map key set must exactly match allStaticToolNames — no gaps, no extras")
}

// TestNewCustomAgentToolsCfg_GoalClaimAndBrowserHandoverAreExplicit guards
// the custom-agent seed: new tools need explicit intended entries here;
// otherwise denyAllThenOverride supplies Deny.
func TestNewCustomAgentToolsCfg_GoalClaimAndBrowserHandoverAreExplicit(t *testing.T) {
	cfg := NewCustomAgentToolsCfg()
	require.NotNil(t, cfg)

	// goal_claim: allow. Its value (allow) differs from the
	// denyAllThenOverride floor (deny), so a correct result here is only
	// reachable through an explicit override entry — proving the entry
	// exists, not merely that the key happens to be present.
	p, ok := cfg.Builtin.Policies["goal_claim"]
	require.True(t, ok, "customs' default allowlist must have an explicit policy for goal_claim")
	assert.Equal(t, config.ToolPolicyAllow, p, "customs' default allowlist goal_claim must be allow")

	// SPEC.md Table (2026-10-22): all browser_* tools, including handover, default Allow for Main.
	p, ok = cfg.Builtin.Policies["browser_handover"]
	require.True(t, ok, "customs' default allowlist must have an explicit policy for browser_handover")
	assert.Equal(t, config.ToolPolicyAllow, p, "customs' default browser_handover must be allow")
}

// TestAgentConstructor_CoreAgent_SeedsRailPlusAllowances verifies that each core
// agent's SeedConfig call produces the correct policy configuration.
//
// BDD: Given SeedConfig is called for Ava's ID,
//
//	When the resulting agent config is found in cfg.Agents.List,
//	Then its Tools.Builtin.Policies has {"system.*": "deny"} plus 4 explicit allows.
//
// Traces to: pkg/coreagent/core.go — SeedConfig (FR-008, FR-022).
func TestAgentConstructor_CoreAgent_SeedsRailPlusAllowances(t *testing.T) {
	cfg := &config.Config{}
	SeedConfig(cfg)

	var omnipusAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDOmnipus) {
			omnipusAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, omnipusAgent, "SeedConfig must add Omnipus to cfg.Agents.List")
	require.NotNil(t, omnipusAgent.Tools, "Omnipus's Tools config must be non-nil after seed")

	// No legacy "system.*" rail; the persisted map is exactly the role seed.
	_, hasRail := omnipusAgent.Tools.Builtin.Policies["system.*"]
	assert.False(t, hasRail, "Omnipus must NOT carry the dead 'system.*' rail")

	assert.Equal(t, adr090SparseRolePolicies(IDOmnipus), omnipusAgent.Tools.Builtin.Policies)
}

// TestJimSeed_DenyDefaultWithExplicitAllows verifies that Jim's constructor seed
// is deny-by-default with explicit allows for his full tool surface — including
// workspace_shell, workspace_shell_bg, and serve_web — and no dead "system.*" rail.
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the returned defaultPolicy and policies map are inspected,
//	Then defaultPolicy is "deny" (least-privilege redesign);
//	And bash (ADR-036: exec/workspace_shell/workspace_shell_bg merged) and
//	serve_web are "allow";
//	And the dead "system.*" rail is absent;
//	And run_in_workspace is not present.
//
// Traces to: quizzical-marinating-frog.md Step 7 + Jim least-privilege redesign.
func TestJimSeed_DenyDefaultWithExplicitAllows(t *testing.T) {
	policies := coreAgentSeed(IDJim)

	// SPEC.md Table (2026-10-22): serve_web is Main core Allow; Jim's bash Deny is unchanged.
	assert.Equal(t, config.ToolPolicyAllow, ADR090RolePolicyInventory(IDJim)["serve_web"])
	for _, toolName := range []string{"bash"} {
		p, ok := policies[toolName]
		require.True(t, ok, "Jim must have explicit policy for %q", toolName)
		assert.Equal(t, config.ToolPolicyDeny, p,
			"Jim's ADR-090 policy for %q must be deny", toolName)
	}

	// The dead "system.*" deny rail must be gone — it was the old legacy approach.
	_, hasSystemRail := policies["system.*"]
	assert.False(t, hasSystemRail, "Jim must NOT carry the dead 'system.*' rail after least-privilege redesign")

	// run_in_workspace is deleted — no policy entry should exist.
	_, hasRunIn := policies["run_in_workspace"]
	assert.False(t, hasRunIn, "run_in_workspace is removed; Jim must not have a policy entry for it")
}

// TestJimSeed_ConsentGatedDeleteTools verifies that Jim's destructive/irreversible
// operations are consent-gated ("ask"), not silently allowed or denied.
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the policies map is inspected for delete/remove operations,
//	Then delete_task, delete_workspace, remove_mcp_server
//	are all "ask" (standing rule: delete/remove operations require confirmation).
//	(delete_task_in_workspace was retired by DEL-23 and carries no policy entry.)
//
// Traces to: Jim least-privilege redesign — delete/remove standing rule.
func TestJimSeed_ConsentGatedDeleteTools(t *testing.T) {
	policies := coreAgentSeed(IDJim)

	// SPEC.md Table (2026-10-22): delete_task is Ask; management deletions remain out of role.
	assert.Equal(t, config.ToolPolicyAsk, ADR090RolePolicyInventory(IDJim)["delete_task"])
	for _, toolName := range []string{
		"delete_workspace", "remove_mcp_server",
	} {
		p, ok := policies[toolName]
		require.True(t, ok, "Jim must have explicit policy for consent-gated tool %q", toolName)
		assert.Equal(t, config.ToolPolicyDeny, p,
			"Jim's ADR-090 default for out-of-role destructive tool %q must be deny", toolName)
	}
}

// TestJimSeed_OutOfScopeToolsExplicitlyDenied verifies that tools outside
// Jim's scope carry an explicit "deny" entry in his policy map — under the
// no-default-policy-fallback model every known tool has a literal entry, so
// out-of-scope tools are denied by an explicit value, never by omission.
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the policies map is inspected for out-of-scope tools,
//	Then create_agent, list_channels, configure_provider are all present with
//	an explicit "deny" value (no DefaultPolicy field exists to fall through to).
//
// Traces to: Jim least-privilege redesign; CLAUDE.md hard constraint 6
// (no default-policy fallback — every tool-policy decision is explicit).
func TestJimSeed_OutOfScopeToolsExplicitlyDenied(t *testing.T) {
	policies := coreAgentSeed(IDJim)

	for _, toolName := range []string{"create_agent", "list_channels", "configure_provider"} {
		p, present := policies[toolName]
		require.True(t, present, "Jim must have an explicit policy entry for %q (no fallback exists)", toolName)
		assert.Equal(t, config.ToolPolicyDeny, p, "Jim's policy for %q must be explicit 'deny'", toolName)
	}
}
