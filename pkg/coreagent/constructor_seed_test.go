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
//	Then defaultPolicy is "allow" for all core agents;
//	And the "system.*: deny" wildcard is present;
//	And Ava additionally has explicit allows for 4 system.* tools;
//	And Jim has workspace.shell + workspace.shell_bg + web_serve allowed;
//	And Jim's seeded sandbox_profile is workspace+net.
//
// Traces to: pkg/coreagent/core.go — coreAgentSeed (FR-008, FR-010, FR-022).
func TestBoot_ConstructorSeedDispositionMap(t *testing.T) {
	avaAllowedTools := []string{
		"system.agent.create",
		"system.agent.update",
		"system.agent.delete",
		"system.models.list",
	}

	tests := []struct {
		id                   CoreAgentID
		expectSystemDeny     bool
		expectExtraAllows    []string
		expectExplicitDenies []string
		expectSandboxProfile config.SandboxProfile
	}{
		{
			id:                IDAva,
			expectSystemDeny:  true,
			expectExtraAllows: avaAllowedTools,
		},
		{
			id:               IDJim,
			expectSystemDeny: true,
			// Step 7: Jim uses web_serve (unified tool), workspace.shell, workspace.shell_bg.
			expectExtraAllows:    []string{"workspace.shell", "workspace.shell_bg", "web_serve"},
			expectExplicitDenies: nil, // run_in_workspace removed; no explicit denies needed
			expectSandboxProfile: config.SandboxProfileWorkspaceNet,
		},
	}

	for _, tc := range tests {
		t.Run(string(tc.id), func(t *testing.T) {
			dp, policies, sandboxProfile := coreAgentSeed(tc.id)

			assert.Equal(t, config.ToolPolicyAllow, dp,
				"defaultPolicy must be 'allow' for all core agents (FR-022)")

			// system.* wildcard deny must be present.
			if tc.expectSystemDeny {
				p, ok := policies["system.*"]
				require.True(t, ok, "policies must contain 'system.*' key (FR-022)")
				assert.Equal(t, config.ToolPolicyDeny, p,
					"'system.*' must be set to deny (FR-022)")
			}

			// Ava gets 4 extra allows; Jim gets workspace.shell + workspace.shell_bg.
			for _, toolName := range tc.expectExtraAllows {
				p, ok := policies[toolName]
				require.True(t, ok, "agent %q must have explicit allow for %q", tc.id, toolName)
				assert.Equal(t, config.ToolPolicyAllow, p,
					"agent %q explicit allow for %q must be 'allow'", tc.id, toolName)
			}

			// Jim denies run_in_workspace explicitly.
			for _, toolName := range tc.expectExplicitDenies {
				p, ok := policies[toolName]
				require.True(t, ok, "agent %q must have explicit deny for %q", tc.id, toolName)
				assert.Equal(t, config.ToolPolicyDeny, p,
					"agent %q explicit deny for %q must be 'deny'", tc.id, toolName)
			}

			// Sandbox profile check.
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

	nonAvaAgents := []CoreAgentID{IDJim, IDMia, IDRay, IDMax}
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

	p, ok := avaAgent.Tools.Builtin.Policies["system.*"]
	require.True(t, ok, "Ava must have 'system.*' deny in seeded config")
	assert.Equal(t, config.ToolPolicyDeny, p)

	for _, allow := range []string{"system.agent.create", "system.agent.update", "system.agent.delete", "system.models.list"} {
		ap, aok := avaAgent.Tools.Builtin.Policies[allow]
		require.True(t, aok, "Ava must have explicit allow for %q", allow)
		assert.Equal(t, config.ToolPolicyAllow, ap)
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

// TestJimSeed_WebServeAndWorkspaceShellAllowed verifies that Jim's constructor
// seed allows workspace.shell, workspace.shell_bg, and web_serve (step 7
// migration from run_in_workspace to unified web_serve).
//
// BDD: Given coreAgentSeed(IDJim) is called,
//
//	When the policies map is inspected,
//	Then workspace.shell, workspace.shell_bg, and web_serve are "allow".
//
// Traces to: quizzical-marinating-frog.md Step 7.
func TestJimSeed_WebServeAndWorkspaceShellAllowed(t *testing.T) {
	_, policies, _ := coreAgentSeed(IDJim)

	for _, toolName := range []string{"workspace.shell", "workspace.shell_bg", "web_serve"} {
		p, ok := policies[toolName]
		require.True(t, ok, "Jim must have explicit policy for %q", toolName)
		assert.Equal(t, config.ToolPolicyAllow, p,
			"Jim's policy for %q must be 'allow'", toolName)
	}

	// run_in_workspace is deleted — no explicit deny entry should exist.
	_, hasRunIn := policies["run_in_workspace"]
	assert.False(t, hasRunIn, "run_in_workspace is removed; Jim must not have a policy entry for it")
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
		enabled := true
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{
						ID:      string(IDJim),
						Name:    "Jim — General Purpose",
						Locked:  true,
						Enabled: &enabled,
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
		enabled := true
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				List: []config.AgentConfig{
					{
						ID:             string(IDJim),
						Name:           "Jim — General Purpose",
						Locked:         true,
						Enabled:        &enabled,
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
