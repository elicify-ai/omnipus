// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// AllTools returns all system tools as a flat slice.
// The slice preserves the canonical tool ordering from BRD Appendix D §D.4.1.
// One additional tool (agent.read_metadata) is included per issue #240 to
// provide the deterministic, self-validating accessor for agent metadata
// files. Its write counterpart (agent.write_metadata) was retired: it was a
// redundant, unguarded second door onto the same files update_agent already
// writes through a properly-guarded path (refusing locked core agents) — see
// the tool-manifest-tier-redesign review F6.
func AllTools(d *Deps) []tools.Tool {
	return []tools.Tool{
		// Agent management (4: 3 original + 1 metadata accessor from issue #240; list, activate, deactivate retired)
		NewAgentCreateTool(d),
		NewAgentUpdateTool(d),
		NewAgentDeleteTool(d),
		NewAgentReadMetadataTool(d),

		// Workspace management (5)
		NewWorkspaceCreateTool(d),
		NewWorkspaceUpdateTool(d),
		NewWorkspaceDeleteTool(d),
		NewWorkspaceListTool(d),
		NewWorkspaceGetTool(d),

		// Task management (4)
		NewTaskCreateTool(d),
		NewTaskUpdateTool(d),
		NewTaskDeleteTool(d),
		NewTaskListTool(d),

		// Channel management (5)
		NewChannelEnableTool(d),
		NewChannelConfigureTool(d),
		NewChannelDisableTool(d),
		NewChannelListTool(d),
		NewChannelTestTool(d),

		// Skill management (4: install and search retired)
		NewSkillRemoveTool(d),
		NewSkillListTool(d),
		// Skill authoring / self-improvement (Spec-6 U2): consent-gated +
		// versioned writes. Editing a built-in produces a user override.
		NewSkillCreateTool(d),
		NewSkillEditTool(d),

		// MCP server management (3)
		NewMCPAddTool(d),
		NewMCPRemoveTool(d),
		NewMCPListTool(d),

		// Provider management (4)
		NewProviderConfigureTool(d),
		NewProviderListTool(d),
		NewProviderTestTool(d),
		NewModelsListTool(d),

		// Config (2)
		NewConfigGetTool(d),
		NewConfigSetTool(d),

		// Diagnostics / utility (2) — backup.create retired (§6: no infra, ops/CLI concern)
		NewDoctorRunTool(d),
		NewUsageQueryTool(d),
	}
}

// BuildRegistry creates a ToolRegistry containing all 33 system tools.
// Use this registry as the backing store for the SystemToolHandler.
func BuildRegistry(d *Deps) *tools.ToolRegistry {
	reg := tools.NewToolRegistry()
	for _, t := range AllTools(d) {
		reg.Register(t)
	}
	return reg
}
