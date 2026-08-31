// Omnipus — Core Agents
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec-3 roster re-cast: core agent roster v1 re-cast (v0.1.0 foundation).
// Tests the 4 base core agents (mia, jim, ava, ray) and their
// compiled-in metadata, prompts, tools, and deletion protection.
// Max was retired from the seeded base.

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// =====================================================================
// Test #15 — TestCoreAgentRoster
// =====================================================================

// TestCoreAgentCount verifies the base/worker split: All() returns the 4 base
// agents PLUS the seeded general-purpose worker (5 total); BaseAgents() returns
// just the 4 base agents.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — 4-base roster: Mia·Assistant,
// Jim·Orchestrator, Ray·Scout, Ava·Builder (Max retired) — plus the sub-agent
// worker tier (general-purpose Worker, Type=worker).
// BDD: "Given Omnipus default config, When All() called,
//
//	Then 4 base agents + 1 worker returned (base in display order, Mia first)"
func TestCoreAgentCount(t *testing.T) {
	base := coreagent.BaseAgents()
	require.Len(t, base, 4,
		"BaseAgents() must return exactly 4 base core agents (Spec-3: mia, jim, ava, ray — max retired)")

	all := coreagent.All()
	require.Len(t, all, 8,
		"All() must return the 4 base agents + 1 worker + 3 specialist subagents (M5/M6)")

	// The worker is present and is the only generic-worker entry.
	workerCount := 0
	specialistCount := 0
	for _, a := range all {
		if coreagent.IsWorkerID(a.ID) {
			workerCount++
		}
		if coreagent.IsSpecialistID(a.ID) {
			specialistCount++
		}
	}
	require.Equal(t, 1, workerCount, "All() must include exactly one worker (the seeded general-purpose worker)")
	require.Equal(t, 3, specialistCount, "All() must include the 3 specialist subagents (Planner/Explorer/Researcher)")
}

// TestCoreAgentDisplayOrder verifies Mia is first (default selection for new users).
//
// Traces to: wave5b-system-agent-spec.md — FR-012 (Mia first for default selection)
func TestCoreAgentDisplayOrder(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 664
	all := coreagent.All()
	require.NotEmpty(t, all)
	assert.Equal(t, coreagent.IDMia, all[0].ID,
		"Mia must be first in All() — she is the default selection for new users")
}

// TestCoreAgentIDs verifies all 4 expected IDs exist and are correct.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — 4-base roster.
// BDD: "Given the 4 base core agents, When ByID called for each,
//
//	Then each agent is found with the correct ID and re-cast name"
func TestCoreAgentIDs(t *testing.T) {
	tests := []struct {
		id   coreagent.CoreAgentID
		name string
	}{
		// Dataset: Spec-3 4-base roster. Names are the full display strings seeded
		// into config so users see the role alongside the name in the roster card.
		{coreagent.IDJim, "Jim — Planner & Orchestrator"},
		{coreagent.IDAva, "Ava — Builder"},
		{coreagent.IDMia, "Mia — Assistant"},
		{coreagent.IDRay, "Ray — Scout"},
	}

	for _, tc := range tests {
		t.Run(string(tc.id), func(t *testing.T) {
			agent := coreagent.ByID(tc.id)
			require.NotNil(t, agent, "%s must be registered as a core agent", tc.id)
			assert.Equal(t, tc.id, agent.ID, "ID field must match the lookup key")
			assert.Equal(t, tc.name, agent.Name, "Name must match expected re-cast display name")
		})
	}
}

// TestCoreAgentMetadata verifies each agent has a non-empty subtitle, description,
// color, and icon — the fields needed for the UI roster card.
//
// Traces to: wave5b-system-agent-spec.md — FR-012 (UI metadata fields)
// BDD: "Given a core agent, When its fields are read,
//
//	Then Subtitle, Description, Color, and Icon are all non-empty"
func TestCoreAgentMetadata(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 664
	for _, agent := range coreagent.All() {
		// capture loop variable
		t.Run(string(agent.ID), func(t *testing.T) {
			assert.NotEmpty(t, agent.Subtitle,
				"core agent %s must have a non-empty Subtitle", agent.ID)
			assert.NotEmpty(t, agent.Description,
				"core agent %s must have a non-empty Description", agent.ID)
			assert.NotEmpty(t, agent.Color,
				"core agent %s must have a non-empty Color (hex code for avatar)", agent.ID)
			assert.NotEmpty(t, agent.Icon,
				"core agent %s must have a non-empty Icon (Phosphor icon name)", agent.ID)
		})
	}
}

// TestCoreAgentMetadataDifferentiation verifies each agent has a DIFFERENT color and
// subtitle, proving metadata is not hardcoded to a single value.
//
// Differentiation test: if all 4 agents returned the same color, this would catch it.
//
// Traces to: wave5b-system-agent-spec.md — FR-012 (each agent distinct identity)
func TestCoreAgentMetadataDifferentiation(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 664
	colors := make(map[string]coreagent.CoreAgentID)
	subtitles := make(map[string]coreagent.CoreAgentID)

	for _, agent := range coreagent.All() {
		// Each agent must have a unique color
		if prev, exists := colors[agent.Color]; exists {
			t.Errorf("agents %s and %s share the same color %q — each agent must have a distinct color",
				prev, agent.ID, agent.Color)
		}
		colors[agent.Color] = agent.ID

		// Each agent must have a unique subtitle
		if prev, exists := subtitles[agent.Subtitle]; exists {
			t.Errorf("agents %s and %s share the same subtitle %q — each agent must have a distinct role",
				prev, agent.ID, agent.Subtitle)
		}
		subtitles[agent.Subtitle] = agent.ID
	}
}

// TestCoreAgentPromptsHardcoded verifies that all core agent prompts are
// non-empty strings compiled into the binary (not external file paths).
//
// Traces to: wave5b-system-agent-spec.md — US-8 AC2 (prompt compiled into binary)
// BDD: "Given a core agent, When GetPrompt(id) called,
//
//	Then a non-empty system prompt is returned — compiled, not a file reference"
func TestCoreAgentPromptsHardcoded(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 152 (US-8 AC2)
	for _, agent := range coreagent.All() {
		// The worker is skipped here only because init()'s
		// mandatory-compiled-prompt invariant exempts it, and this test
		// mirrors that exemption.
		//
		// It is NO LONGER true that the worker has an intentional empty
		// prompt. It has a real compiled execution-discipline prompt (RC-6):
		// the empty entry meant BuildSystemPrompt fell past both the
		// compiled-prompt and SOUL branches to the generic "You are Worker, a
		// helpful AI assistant" fallback, so the most-used delegation target
		// was the only seeded agent with no role guidance — and it produced
		// the unbounded output that looked like a hang.
		//
		// A worker's soul remains OPTIONAL in the sense that a CUSTOM worker
		// may have none; that is unrelated to this loop, which iterates All()
		// (the seeded roster) and never sees a custom agent.
		// TestWorkerHasCompiledExecutionDisciplinePrompt_BootSucceeds in
		// worker_seed_test.go now asserts the seeded worker's prompt is
		// non-empty and is not the generic fallback.
		if coreagent.IsWorkerID(agent.ID) {
			continue
		}
		t.Run(string(agent.ID)+" has non-empty compiled prompt", func(t *testing.T) {
			prompt := coreagent.GetPrompt(string(agent.ID))
			assert.NotEmpty(t, prompt,
				"core agent %s must have a non-empty hardcoded prompt compiled into the binary", agent.ID)
			// Prompts must not be file paths themselves. Mentioning SOUL.md within
			// prompt instructions (e.g., Ava writing SOUL.md for new agents) is allowed.
			assert.NotContains(t, prompt, "/home/",
				"core agent prompt must NOT be a file path")
			assert.NotContains(t, prompt, "/var/",
				"core agent prompt must NOT be a filesystem path")
		})
	}
}

// TestCoreAgentPromptsDifferentiation verifies each agent has a DIFFERENT prompt.
// This catches a bug where all agents return the same compiled prompt.
//
// Differentiation test: call GetPrompt with two different IDs, assert different content.
//
// Traces to: Spec-3 — compiled prompts for the 4-base roster.
func TestCoreAgentPromptsDifferentiation(t *testing.T) {
	jimPrompt := coreagent.GetPrompt("jim")
	avaPrompt := coreagent.GetPrompt("ava")
	assert.NotEqual(t, jimPrompt, avaPrompt,
		"Jim and Ava must have different compiled prompts — not the same hardcoded string")

	miaPrompt := coreagent.GetPrompt("mia")
	rayPrompt := coreagent.GetPrompt("ray")
	assert.NotEqual(t, miaPrompt, rayPrompt,
		"Mia and Ray must have different compiled prompts")
}

// TestAvaPromptContainsWorkspaceSetupInterview verifies Ava's compiled prompt
// carries the workspace-setup kickoff interview instructions: the
// plain (user-role) message announcing a new workspace's team needs setting
// up must produce a first-person greeting from Ava, followed by a short
// interview and the update_workspace/create_agent team-assembly steps. The
// kickoff reaches Ava as an ordinary user-role chat message (pkg/gateway's
// handleChatMessage publishes it with Role: "user" — only the PERSISTED/
// REPLAYED transcript entry is recorded as a neutral system-role event), so
// the prompt must not claim it arrives "as a system message".
//
// Traces to: contracts/asyncapi.yaml metadata.workspace_setup_kickoff,
// pkg/workspace/workspace.go Workspace.SetupPending.
func TestAvaPromptContainsWorkspaceSetupInterview(t *testing.T) {
	avaPrompt := coreagent.GetPrompt("ava")
	require.NotEmpty(t, avaPrompt)

	assert.Contains(t, avaPrompt, "## Workspace setup interview",
		"Ava's prompt must have a dedicated section for the workspace-setup kickoff")
	assert.Contains(t, avaPrompt, "workspace-setup kickoff",
		"the section must name the kickoff trigger so Ava recognizes the message")
	assert.Contains(t, avaPrompt, "When a message announces that a workspace was just created",
		"the trigger phrasing must describe the kickoff as a plain message, not a system message")
	assert.NotContains(t, avaPrompt, "a system message announces",
		"the prompt must not claim the kickoff arrives as a system message — it is a plain user-role message")
	assert.Contains(t, avaPrompt, "Hi, I'm Ava",
		"Ava's first reply on kickoff must greet in first person")
	assert.Contains(t, avaPrompt, "update_workspace",
		"the interview must end in setting the workspace's core_team via update_workspace")
	assert.Contains(t, avaPrompt, "create_agent",
		"the interview must cover creating specialists that don't already exist")
	assert.Contains(
		t,
		avaPrompt,
		"default delegation trust edges are seeded automatically",
		"Ava's prompt must tell her that adding built-in roster members via update_workspace auto-seeds delegation edges",
	)
	assert.Contains(t, avaPrompt, "Team tab",
		"Ava's prompt must point the user to the Team tab to review/adjust the auto-seeded edges")
	assert.Contains(t, avaPrompt, "does NOT extend to custom specialists you create yourself with create_agent",
		"the prompt must be honest that custom agents get zero auto-seeded delegation edges")
	assert.Contains(t, avaPrompt, "delegation_seeded",
		"the prompt must tell Ava to check the update_workspace result's delegation_seeded note and relay it")
	assert.NotContains(
		t,
		avaPrompt,
		"so the team can delegate to each other out of the box",
		"the prompt must not overclaim that ALL team members (including custom specialists) can delegate out of the box",
	)
}

// TestGetPromptUnknownID verifies GetPrompt returns empty string for unknown IDs.
// Callers fall back to SOUL.md on disk when the empty string is returned.
//
// Traces to: wave5b-system-agent-spec.md — US-8 AC2 (fallback for custom agents)
func TestGetPromptUnknownID(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 152
	result := coreagent.GetPrompt("nonexistent-agent")
	assert.Empty(t, result,
		"GetPrompt must return empty string for unknown IDs — caller falls back to SOUL.md")
}

// TestCoreAgentDefaultTools verifies each core agent has a defined default tool set.
//
// Traces to: wave5b-system-agent-spec.md — FR-012 (default tool sets per BRD D.9)
func TestCoreAgentDefaultTools(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 664
	for _, agent := range coreagent.All() {
		t.Run(string(agent.ID)+" has default tools", func(t *testing.T) {
			assert.NotEmpty(t, agent.DefaultTools,
				"core agent %s must have at least one default tool", agent.ID)
		})
	}
}

// TestStaticCatalog_ContainsRenamedDiscoveryTool pins ADR-071 D1 / spec
// FR-010 (W-D1 test 10): the boot-time tool-policy coverage validator
// resolves the renamed discovery capability against the static catalog, and
// the retired "load_tool" name is gone from it — a coverage gap here means
// startup validation aborts on every install (CLAUDE.md constraint #6).
func TestStaticCatalog_ContainsRenamedDiscoveryTool(t *testing.T) {
	names := coreagent.AllStaticToolNames()
	found := false
	for _, n := range names {
		if n == "ToolSearch" {
			found = true
		}
		if n == "load_tool" {
			t.Errorf("AllStaticToolNames() contains retired name %q", n)
		}
	}
	assert.True(t, found, "AllStaticToolNames() must contain \"ToolSearch\"")
}

// =====================================================================
// Test #16 — TestCoreAgentCannotDelete
// =====================================================================

// TestCoreAgentIsCoreAgentFunction verifies the IsCoreAgent function correctly
// identifies all 4 base core agents and rejects non-core IDs.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — Max retired; 4-base roster.
// BDD: "Given a core agent ID, When IsCoreAgent called,
//
//	Then true is returned; for custom IDs or retired Max, false"
func TestCoreAgentIsCoreAgentFunction(t *testing.T) {
	coreIDs := []struct {
		id       string
		isCore   bool
		scenario string
	}{
		// Dataset: the 4 base core agents that must be protected from deletion
		{"jim", true, "Jim is a core agent (Orchestrator)"},
		{"ava", true, "Ava is a core agent (Builder)"},
		{"mia", true, "Mia is a core agent (Assistant)"},
		{"ray", true, "Ray is a core agent (Scout)"},
		// Max is retired from the seeded base — must NOT be identified as core
		{"max", false, "Max was retired from the seeded base (Spec-3); not a core agent"},
		// Dataset: IDs that must NOT be identified as core
		{"omnipus-system", false, "omnipus-system is the system agent, protected separately"},
		{"financial-analyst", false, "custom agent is not a core agent"},
		{"devops-helper", false, "custom agent is not a core agent"},
		{"my-custom-agent", false, "custom agent is not a core agent"},
		{"", false, "empty string is not a core agent"},
		// Old IDs that no longer exist must return false
		{"general-assistant", false, "old ID removed in issue #45"},
		{"researcher", false, "old ID removed in issue #45"},
		{"content-creator", false, "old ID removed in issue #45"},
	}

	for _, tc := range coreIDs {
		t.Run(tc.scenario, func(t *testing.T) {
			got := coreagent.IsCoreAgent(tc.id)
			assert.Equal(t, tc.isCore, got,
				"IsCoreAgent(%q): expected %v, got %v — %s", tc.id, tc.isCore, got, tc.scenario)
		})
	}
}

// TestCoreAgentByIDNotFound verifies ByID returns nil for unknown IDs.
//
// Traces to: wave5b-system-agent-spec.md — FR-012 (ByID lookup safety)
func TestCoreAgentByIDNotFound(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 664
	result := coreagent.ByID(coreagent.CoreAgentID("nonexistent"))
	assert.Nil(t, result, "ByID must return nil for unknown agent IDs")
}

// Core-agent delete protection (US-8 AC4: "Core agent cannot be deleted") is
// enforced at the TOOL level via IsCoreAgent and covered by
// pkg/sysagent/tools/agent_test.go::TestAgentDelete_RefusesLockedAgent. The
// former RBAC-layer assertion here was dropped with the retired sysagent package
// (its CheckRBAC was the dead Omnipus system-agent enforcement layer).

// =====================================================================
// Test: SeedConfig
// =====================================================================

// TestSeedConfigAddsAllCoreAgents verifies SeedConfig adds all 4 base core agents
// to an empty config with Locked=true and returns true (modified).
//
// Persistence test: write via SeedConfig, read back from cfg.Agents.List.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — seed on first boot, 4-base roster.
// BDD: "Given empty config, When SeedConfig called,
//
//	Then all 4 base core agents are in cfg.Agents.List with Locked=true"
func TestSeedConfigAddsAllCoreAgents(t *testing.T) {
	cfg := &config.Config{}
	modified := coreagent.SeedConfig(cfg)

	assert.True(t, modified,
		"SeedConfig must return true when agents are added to an empty config")
	// Derived from the two rosters rather than a literal count: every new
	// seeded agent used to require editing this number in three tests, which
	// tells you nothing about what actually changed. Today that is 4 base +
	// 1 worker + 3 specialists (M5/M6) + 2 System Agents (Judge ADR-049 D3,
	// PlanSupervisor ADR-055).
	wantSeeded := len(coreagent.All()) + len(coreagent.SystemAgents())
	assert.Len(t, cfg.Agents.List, wantSeeded,
		"SeedConfig must seed every agent in All() plus every agent in SystemAgents()")

	// Verify each core agent was seeded with Locked=true
	seededIDs := make(map[string]config.AgentConfig)
	for _, ac := range cfg.Agents.List {
		seededIDs[ac.ID] = ac
	}

	for _, ca := range coreagent.All() {
		t.Run(string(ca.ID)+" is seeded correctly", func(t *testing.T) {
			ac, found := seededIDs[string(ca.ID)]
			require.True(t, found, "agent %s must be present in seeded config", ca.ID)
			assert.True(t, ac.Locked,
				"agent %s must be seeded with Locked=true to protect identity fields", ca.ID)
			wantType := config.AgentTypeCore
			if coreagent.IsSubagentTierID(ca.ID) {
				wantType = config.AgentTypeWorker
			}
			assert.Equal(t, wantType, ac.Type,
				"agent %s must have Type=%s", ca.ID, wantType)
			assert.Equal(t, ca.Name, ac.Name,
				"agent %s name must match compiled metadata", ca.ID)
		})
	}
}

// TestSeedConfigIsIdempotent verifies SeedConfig does NOT duplicate agents when
// called twice on the same config.
//
// Traces to: Spec-3 (v0.1.0 roster re-cast) — SeedConfig idempotent, 4-base roster.
// BDD: "Given config with all 4 base core agents, When SeedConfig called again,
//
//	Then no agents are added and false is returned"
func TestSeedConfigIsIdempotent(t *testing.T) {
	cfg := &config.Config{}

	// First call: adds every agent in All() plus every System Agent.
	wantSeeded := len(coreagent.All()) + len(coreagent.SystemAgents())
	modified1 := coreagent.SeedConfig(cfg)
	assert.True(t, modified1, "first SeedConfig call must return true (agents added)")
	assert.Len(t, cfg.Agents.List, wantSeeded)

	// Second call: must be a no-op
	modified2 := coreagent.SeedConfig(cfg)
	assert.False(t, modified2,
		"second SeedConfig call must return false — all agents already present")
	assert.Len(t, cfg.Agents.List, wantSeeded,
		"SeedConfig must not duplicate agents on repeated calls")
}

// TestSeedConfigPreservesExistingAgents verifies SeedConfig adds missing core agents
// but does NOT overwrite existing ones (including custom agents).
//
// Traces to: wave5b-system-agent-spec.md — FR-012 (SeedConfig preserves custom agents)
func TestSeedConfigPreservesExistingAgents(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 664
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "my-custom-agent", Name: "My Custom Agent"},
			},
		},
	}

	modified := coreagent.SeedConfig(cfg)
	assert.True(t, modified, "SeedConfig must add missing core agents")
	// Every seeded agent (All() + SystemAgents()) plus the 1 pre-existing custom agent.
	assert.Len(t, cfg.Agents.List, len(coreagent.All())+len(coreagent.SystemAgents())+1,
		"SeedConfig must preserve existing agents while adding every seeded core, worker and System Agent")

	// Verify custom agent is still present and unchanged
	found := false
	for _, ac := range cfg.Agents.List {
		if ac.ID == "my-custom-agent" {
			found = true
			assert.Equal(t, "My Custom Agent", ac.Name,
				"existing custom agent name must not be overwritten by SeedConfig")
			assert.False(t, ac.Locked,
				"custom agent must NOT be locked by SeedConfig")
		}
	}
	assert.True(t, found, "existing custom agent must still be present after SeedConfig")
}

// TestToWireType is a table-driven test for coreagent.ToWireType, the
// response-side inverse of ResolveType. It maps the persisted config.AgentConfig
// to the canonical wire enum the SPA consumes.
//
// Mapping rules exercised:
//   - core      → core
//   - system    → system
//   - custom    → Main
//   - worker + native/no executor → Subagent
//   - worker + external-cli executor → subagent_3p
//   - empty Type + core ID → core (inferred via ResolveType)
//   - empty Type + non-core ID → Main (inferred via ResolveType)
//
// Traces to: pkg/coreagent/core.go ToWireType — the resolved-type switch closes
// the empty-Type gap that previously fell through to the default branch.
func TestToWireType(t *testing.T) {
	nativeKind := config.ExecutorKindNative
	externalKind := config.ExecutorKindExternalCLI

	cases := []struct {
		name     string
		ac       config.AgentConfig
		expected gen.AgentType
	}{
		{
			name:     "core",
			ac:       config.AgentConfig{ID: "mia", Type: config.AgentTypeCore},
			expected: gen.AgentTypeCore,
		},
		{
			name:     "system",
			ac:       config.AgentConfig{ID: "sys", Type: config.AgentTypeSystem},
			expected: gen.AgentTypeSystem,
		},
		{
			name:     "custom maps to Main",
			ac:       config.AgentConfig{ID: "myagent", Type: config.AgentTypeCustom},
			expected: gen.AgentTypeMain,
		},
		{
			name:     "worker no executor maps to Subagent",
			ac:       config.AgentConfig{ID: "w1", Type: config.AgentTypeWorker},
			expected: gen.AgentTypeSubagent,
		},
		{
			name: "worker native executor maps to Subagent",
			ac: config.AgentConfig{
				ID:        "w2",
				Type:      config.AgentTypeWorker,
				Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: nativeKind}},
			},
			expected: gen.AgentTypeSubagent,
		},
		{
			name: "worker external-cli executor maps to subagent_3p",
			ac: config.AgentConfig{
				ID:        "w3",
				Type:      config.AgentTypeWorker,
				Subagents: &config.SubagentsConfig{Executor: &config.ExecutorConfig{Kind: externalKind, CLI: "codex"}},
			},
			expected: gen.AgentTypeSubagent3p,
		},
		{
			name:     "empty type with core ID infers core",
			ac:       config.AgentConfig{ID: "mia", Type: ""},
			expected: gen.AgentTypeCore,
		},
		{
			name:     "empty type with non-core ID infers Main",
			ac:       config.AgentConfig{ID: "custom1", Type: ""},
			expected: gen.AgentTypeMain,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, coreagent.ToWireType(tc.ac))
		})
	}
}
