// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package coreagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestBoot_ConstructorSeedDispositionMap verifies that each core agent's
// constructor seed contains the expected policies.
//
// BDD: Given each core agent ID,
//
//	When coreAgentSeed is called,
//	Then all four base agents are deny-by-default (least-privilege redesign);
//	And none carry the dead "system.*" deny rail;
//	And Jim's seeded sandbox_profile is workspace+net;
//	And Jim's explicit allow-list includes spawn, create_task, workspace_shell, browser_navigate;
//	And Jim's consent-gated tools (delete_task, delete_workspace, etc.) resolve ask;
//	And a sample of tools Jim must NOT have (create_agent, navigate) are absent.
//
// Traces to: pkg/coreagent/core.go — coreAgentSeed (FR-008, FR-010, FR-022).
func TestBoot_ConstructorSeedDispositionMap(t *testing.T) {
	// All four base agents are now LEAST-PRIVILEGE: deny-by-default, explicit
	// allow/ask for exactly their role's tools, no "system.*" rail.
	tests := []struct {
		id                   CoreAgentID
		expectDefaultPolicy  config.ToolPolicy
		expectSystemDeny     bool     // legacy "system.*": deny rail — must be absent for all redesigned agents
		expectExtraAllows    []string // must be present AND == allow
		expectAsk            []string // must be present AND == ask
		expectExplicitDenies []string
		expectSandboxProfile config.SandboxProfile
	}{
		{
			id:                  IDAva,
			expectDefaultPolicy: config.ToolPolicyDeny,
			expectSystemDeny:    false,
			expectExtraAllows: []string{
				"create_agent", "update_agent", "list_agents",
				"list_models", "search_web", "fetch_url",
				"remember", "recall_memory", "run_retrospective",
				"send_message", "hand_off", "return_to_default",
				"find_skills", "list_skills",
				"update_workspace", "list_workspaces", "get_workspace",
			},
			expectAsk: []string{"delete_agent", "create_skill", "edit_skill", "install_skill"},
		},
		{
			id:                  IDMia,
			expectDefaultPolicy: config.ToolPolicyDeny,
			expectSystemDeny:    false,
			expectExtraAllows: []string{
				"send_message", "hand_off", "return_to_default", "list_agents",
				"send_file", "navigate",
				"remember", "recall_memory", "run_retrospective",
				"create_task", "update_task", "list_tasks", "set_todos",
				"read_inbox", "read_message", "reply", "send_email", "search_email",
				"search_web", "fetch_url", "find_skills",
			},
			expectAsk: []string{"delete_task"},
		},
		{
			id:                  IDRay,
			expectDefaultPolicy: config.ToolPolicyDeny,
			expectSystemDeny:    false,
			expectExtraAllows: []string{
				"search_web", "fetch_url",
				"browser_navigate", "browser_click", "browser_type",
				"browser_get_text", "browser_wait", "browser_screenshot",
				"read_file", "list_directory", "write_file", "append_file", "edit_file",
				"spawn", "run_subagent", "check_spawn_status",
				"remember", "recall_memory", "run_retrospective",
				"send_message", "hand_off", "return_to_default", "send_file",
				"find_skills", "set_todos",
			},
		},
		{
			id:                  IDJim,
			expectDefaultPolicy: config.ToolPolicyDeny,
			expectSystemDeny:    false,
			// Jim's full explicit allow-list (a representative sample tested here).
			expectExtraAllows: []string{
				// File operations.
				"read_file", "write_file", "edit_file", "append_file", "list_directory",
				// Lookups.
				"search_web", "fetch_url",
				// Execution.
				"exec", "workspace_shell", "workspace_shell_bg", "serve_web",
				// Communication / routing.
				"send_message", "send_file", "hand_off", "return_to_default",
				// Memory.
				"remember", "recall_memory", "run_retrospective", "set_todos",
				// Delegation.
				"spawn", "run_subagent", "check_spawn_status", "list_agents",
				// Task management (current + cross-workspace).
				"create_task", "list_tasks", "update_task",
				"create_task_in_workspace", "list_tasks_in_workspace", "update_task_in_workspace",
				// Workspace lifecycle.
				"get_workspace", "list_workspaces", "update_workspace", "create_workspace",
				// Skill discovery + install.
				"find_skills", "list_skills", "install_skill",
				// MCP.
				"list_mcp_servers", "add_mcp_server",
				// Browser.
				"browser_navigate", "browser_click", "browser_type",
				"browser_wait", "browser_get_text", "browser_screenshot",
			},
			// Destructive/irreversible operations are consent-gated.
			expectAsk: []string{
				"delete_task", "delete_task_in_workspace",
				"delete_workspace", "remove_mcp_server",
			},
			expectSandboxProfile: config.SandboxProfileWorkspaceNet,
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.id), func(t *testing.T) {
			dp, policies, sandboxProfile := coreAgentSeed(tc.id)

			assert.Equal(t, tc.expectDefaultPolicy, dp,
				"agent %q default_policy mismatch", tc.id)

			// Legacy "system.*" deny rail — present only for not-yet-redesigned agents.
			if tc.expectSystemDeny {
				p, ok := policies["system.*"]
				require.True(t, ok, "policies must contain 'system.*' key")
				assert.Equal(t, config.ToolPolicyDeny, p, "'system.*' must be deny")
			} else {
				_, ok := policies["system.*"]
				assert.False(t, ok, "redesigned agent %q must NOT carry the dead 'system.*' rail", tc.id)
			}

			for _, toolName := range tc.expectExtraAllows {
				p, ok := policies[toolName]
				require.True(t, ok, "agent %q must have explicit policy for %q", tc.id, toolName)
				assert.Equal(t, config.ToolPolicyAllow, p,
					"agent %q policy for %q must be 'allow'", tc.id, toolName)
			}

			for _, toolName := range tc.expectAsk {
				p, ok := policies[toolName]
				require.True(t, ok, "agent %q must have explicit policy for %q", tc.id, toolName)
				assert.Equal(t, config.ToolPolicyAsk, p,
					"agent %q policy for %q must be 'ask'", tc.id, toolName)
			}

			for _, toolName := range tc.expectExplicitDenies {
				p, ok := policies[toolName]
				require.True(t, ok, "agent %q must have explicit deny for %q", tc.id, toolName)
				assert.Equal(t, config.ToolPolicyDeny, p,
					"agent %q explicit deny for %q must be 'deny'", tc.id, toolName)
			}

			if tc.expectSandboxProfile != "" {
				assert.Equal(t, tc.expectSandboxProfile, sandboxProfile,
					"agent %q seeded sandbox_profile must be %q", tc.id, tc.expectSandboxProfile)
			}
		})
	}
}

// TestBoot_HasSystemAllowsInConstructorSeed verifies that only Ava returns true
// from HasSystemAllowsInConstructorSeed.
//
// BDD: Given each core agent ID,
//
//	When HasSystemAllowsInConstructorSeed is called,
//	Then only Ava returns true;
//	And all other known core agents return false.
//
// Traces to: pkg/coreagent/core.go — HasSystemAllowsInConstructorSeed (FR-062).
func TestBoot_HasSystemAllowsInConstructorSeed(t *testing.T) {
	assert.True(t, HasSystemAllowsInConstructorSeed(string(IDAva)),
		"Ava must return true (she has explicit system.* allows)")

	nonAvaAgents := []CoreAgentID{IDJim, IDMia, IDRay}
	for _, id := range nonAvaAgents {
		assert.False(t, HasSystemAllowsInConstructorSeed(string(id)),
			"agent %q must return false (no explicit system.* allows)", id)
	}

	// Unknown agent IDs must also return false.
	assert.False(t, HasSystemAllowsInConstructorSeed("some-custom-agent"))
	assert.False(t, HasSystemAllowsInConstructorSeed(""))
}

// TestAgentConstructor_CustomAgent_SeedsSystemDeny verifies that a newly created
// custom agent config has {"system.*": "deny"} in its policy map.
//
// BDD: Given a new custom agent created via NewCustomAgentToolsCfg,
//
//	When the resulting config is inspected,
//	Then default_policy is "allow" and policies["system.*"] is "deny".
//
// Traces to: pkg/coreagent/core.go — NewCustomAgentToolsCfg (FR-022).
func TestAgentConstructor_CustomAgent_SeedsSystemDeny(t *testing.T) {
	cfg := NewCustomAgentToolsCfg()
	require.NotNil(t, cfg, "NewCustomAgentToolsCfg must return a non-nil config")

	assert.Equal(t, config.ToolPolicyAllow, cfg.Builtin.DefaultPolicy,
		"custom agent default_policy must be 'allow' (FR-022)")

	p, ok := cfg.Builtin.Policies["system.*"]
	require.True(t, ok, "custom agent must have 'system.*' in Policies (FR-022)")
	assert.Equal(t, config.ToolPolicyDeny, p,
		"custom agent 'system.*' must be 'deny' (FR-022)")
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

	var avaAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDAva) {
			avaAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, avaAgent, "SeedConfig must add Ava to cfg.Agents.List")
	require.NotNil(t, avaAgent.Tools, "Ava's Tools config must be non-nil after seed")

	// Ava is redesigned: deny-by-default, no legacy "system.*" rail.
	assert.Equal(t, config.ToolPolicyDeny, avaAgent.Tools.Builtin.DefaultPolicy,
		"Ava's seeded default_policy must be deny (least-privilege)")
	_, hasRail := avaAgent.Tools.Builtin.Policies["system.*"]
	assert.False(t, hasRail, "Ava must NOT carry the dead 'system.*' rail after redesign")

	for _, allow := range []string{"create_agent", "update_agent", "list_models", "update_workspace"} {
		ap, aok := avaAgent.Tools.Builtin.Policies[allow]
		require.True(t, aok, "Ava must have explicit allow for %q", allow)
		assert.Equal(t, config.ToolPolicyAllow, ap)
	}
	// Destructive + authoring tools are consent-gated (ask).
	for _, ask := range []string{"delete_agent", "create_skill", "edit_skill"} {
		ap, aok := avaAgent.Tools.Builtin.Policies[ask]
		require.True(t, aok, "Ava must have explicit policy for %q", ask)
		assert.Equal(t, config.ToolPolicyAsk, ap)
	}
}

// TestJimSeed_SandboxProfileIsWorkspacePlusNet verifies that Jim's constructor
// seed produces sandbox_profile=workspace+net (PR 5 acceptance criterion).
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the returned sandboxProfile is inspected,
//	Then it equals SandboxProfileWorkspaceNet.
//
// Traces to: quizzical-marinating-frog.md PR 5 — "Jim's seeded sandbox_profile is workspace+net".
func TestJimSeed_SandboxProfileIsWorkspacePlusNet(t *testing.T) {
	_, _, profile := coreAgentSeed(IDJim)
	assert.Equal(t, config.SandboxProfileWorkspaceNet, profile,
		"Jim's seeded sandbox_profile must be workspace+net (PR 5 migration)")
}

// TestJimSeed_DenyDefaultWithExplicitAllows verifies that Jim's constructor seed
// is deny-by-default with explicit allows for his full tool surface — including
// workspace_shell, workspace_shell_bg, and serve_web — and no dead "system.*" rail.
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the returned defaultPolicy and policies map are inspected,
//	Then defaultPolicy is "deny" (least-privilege redesign);
//	And workspace_shell, workspace_shell_bg, and serve_web are "allow";
//	And the dead "system.*" rail is absent;
//	And run_in_workspace is not present.
//
// Traces to: quizzical-marinating-frog.md Step 7 + Jim least-privilege redesign.
func TestJimSeed_DenyDefaultWithExplicitAllows(t *testing.T) {
	dp, policies, _ := coreAgentSeed(IDJim)

	assert.Equal(t, config.ToolPolicyDeny, dp,
		"Jim must be deny-by-default (least-privilege redesign)")

	for _, toolName := range []string{"workspace_shell", "workspace_shell_bg", "serve_web"} {
		p, ok := policies[toolName]
		require.True(t, ok, "Jim must have explicit policy for %q", toolName)
		assert.Equal(t, config.ToolPolicyAllow, p,
			"Jim's policy for %q must be 'allow'", toolName)
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
//	Then delete_task, delete_task_in_workspace, delete_workspace, remove_mcp_server
//	are all "ask" (standing rule: delete/remove operations require confirmation).
//
// Traces to: Jim least-privilege redesign — delete/remove standing rule.
func TestJimSeed_ConsentGatedDeleteTools(t *testing.T) {
	_, policies, _ := coreAgentSeed(IDJim)

	for _, toolName := range []string{
		"delete_task", "delete_task_in_workspace",
		"delete_workspace", "remove_mcp_server",
	} {
		p, ok := policies[toolName]
		require.True(t, ok, "Jim must have explicit policy for consent-gated tool %q", toolName)
		assert.Equal(t, config.ToolPolicyAsk, p,
			"Jim's policy for %q must be 'ask' (consent-gated)", toolName)
	}
}

// TestJimSeed_DeniedToolsAbsent verifies that tools outside Jim's scope are
// absent from his explicit policy map (they resolve "deny" via the default_policy).
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the policies map is inspected for out-of-scope tools,
//	Then create_agent, navigate, configure_provider are all absent from the map
//	(they fall through to the deny default_policy, not listed as explicit denies).
//
// Traces to: Jim least-privilege redesign.
func TestJimSeed_DeniedToolsAbsent(t *testing.T) {
	dp, policies, _ := coreAgentSeed(IDJim)

	require.Equal(t, config.ToolPolicyDeny, dp, "Jim must be deny-by-default")

	// These tools must NOT appear in the map — they are implicitly denied by default_policy.
	for _, toolName := range []string{"create_agent", "navigate", "configure_provider"} {
		_, present := policies[toolName]
		assert.False(
			t,
			present,
			"Jim must NOT have an explicit entry for %q (implicitly denied by default_policy=deny)",
			toolName,
		)
	}
}

// TestSeedConfig_JimProfileApplied verifies that SeedConfig seeds Jim with
// sandbox_profile=workspace+net when creating a fresh entry.
//
// BDD: Given an empty config, When SeedConfig is called,
//
//	Then Jim's entry has SandboxProfile=workspace+net.
//
// Traces to: quizzical-marinating-frog.md PR 5 acceptance criteria.
func TestSeedConfig_JimProfileApplied(t *testing.T) {
	cfg := &config.Config{}
	SeedConfig(cfg)

	var jimAgent *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == string(IDJim) {
			jimAgent = &cfg.Agents.List[i]
			break
		}
	}
	require.NotNil(t, jimAgent, "SeedConfig must add Jim to cfg.Agents.List")
	assert.Equal(t, config.SandboxProfileWorkspaceNet, jimAgent.SandboxProfile,
		"Jim must be seeded with sandbox_profile=workspace+net")
}

// TestSeed_BrowserSpecialistsHaveBrowserTools is a regression guard for the incident
// where Ray reported "restricted for my agent" after a load_tool failure for
// browser_navigate / browser_screenshot — a fabrication, because both tools are
// explicitly allowed in Ray's seed. This test ensures that Ray AND Jim retain
// browser_navigate and browser_screenshot as ALLOW in their seeded LoadToolPolicy,
// so any future change that strips browser tools from the "browser specialists"
// fails loudly here instead of surfacing as a confusing agent lie.
//
// BDD: Given coreAgentSeed is called for Ray (IDRay) and Jim (IDJim),
//
//	When the returned policies map is inspected for browser tools,
//	Then browser_navigate and browser_screenshot are both "allow" for Ray;
//	And browser_navigate and browser_screenshot are both "allow" for Jim.
//
// Traces to: incident fix-7 / fix-8 (browser capability fabrication + reflexive handoff).
func TestSeed_BrowserSpecialistsHaveBrowserTools(t *testing.T) {
	browserTools := []string{"browser_navigate", "browser_screenshot"}

	for _, agentID := range []CoreAgentID{IDRay, IDJim} {
		agentID := agentID
		t.Run(string(agentID), func(t *testing.T) {
			_, policies, _ := coreAgentSeed(agentID)

			for _, toolName := range browserTools {
				p, ok := policies[toolName]
				require.True(t, ok,
					"agent %q must have an explicit policy entry for %q (browser specialist)",
					agentID, toolName)
				assert.Equal(t, config.ToolPolicyAllow, p,
					"agent %q policy for %q must be 'allow' — browser tools must not be stripped from browser specialists",
					agentID, toolName)
			}
		})
	}
}

// TestSeedConfig_JimProfileMigration verifies the idempotent profile migration:
// if Jim already exists in config with an empty SandboxProfile, SeedConfig fills
// it with the seed value. Operator-set profiles are left unchanged.
//
// BDD: Given Jim exists with SandboxProfile="" (pre-PR5 config),
//
//	When SeedConfig is called,
//	Then Jim's SandboxProfile is set to workspace+net and modified=true.
//
// BDD: Given Jim exists with SandboxProfile="host" (operator override),
//
//	When SeedConfig is called,
//	Then Jim's SandboxProfile remains "host" and the operator choice is preserved.
//
// Traces to: quizzical-marinating-frog.md PR 5 — idempotent migration.
func TestSeedConfig_JimProfileMigration(t *testing.T) {
	t.Run("empty profile is filled with seed", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{
						ID:     string(IDJim),
						Name:   "Jim — Planner & Orchestrator",
						Locked: true,
						// SandboxProfile intentionally empty (pre-PR5 config)
					},
				},
			},
		}
		modified := SeedConfig(cfg)
		assert.True(t, modified, "SeedConfig must return true when migration applies profile")

		var jim *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(IDJim) {
				jim = &cfg.Agents.List[i]
				break
			}
		}
		require.NotNil(t, jim)
		assert.Equal(t, config.SandboxProfileWorkspaceNet, jim.SandboxProfile,
			"migration must fill empty SandboxProfile with workspace+net")
	})

	t.Run("operator-set profile is preserved", func(t *testing.T) {
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{
						ID:             string(IDJim),
						Name:           "Jim — Planner & Orchestrator",
						Locked:         true,
						SandboxProfile: config.SandboxProfileHost, // operator override
					},
				},
			},
		}
		SeedConfig(cfg)

		var jim *config.AgentConfig
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(IDJim) {
				jim = &cfg.Agents.List[i]
				break
			}
		}
		require.NotNil(t, jim)
		assert.Equal(t, config.SandboxProfileHost, jim.SandboxProfile,
			"operator-set SandboxProfile must not be overwritten by SeedConfig migration")
	})
}
