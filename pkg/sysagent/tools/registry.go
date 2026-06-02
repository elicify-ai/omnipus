// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// AllTools returns all system tools as a flat slice.
// The slice preserves the canonical tool ordering from BRD Appendix D §D.4.1.
// Two additional tools (agent.read_metadata, agent.write_metadata) are included
// per issue #240 to provide the deterministic, self-validating accessors for
// agent metadata files.
func AllTools(d *Deps, navCb NavigateCallback) []tools.Tool {
	return []tools.Tool{
		// Agent management (8: 6 original + 2 metadata accessors from issue #240)
		NewAgentCreateTool(d),
		NewAgentUpdateTool(d),
		NewAgentDeleteTool(d),
		NewAgentListTool(d),
		NewAgentActivateTool(d),
		NewAgentDeactivateTool(d),
		NewAgentReadMetadataTool(d),
		NewAgentWriteMetadataTool(d),

		// Project management (4)
		NewProjectCreateTool(d),
		NewProjectUpdateTool(d),
		NewProjectDeleteTool(d),
		NewProjectListTool(d),

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

		// Skill management (4)
		NewSkillInstallTool(d),
		NewSkillRemoveTool(d),
		NewSkillSearchTool(d),
		NewSkillListTool(d),

		// MCP server management (3)
		NewMCPAddTool(d),
		NewMCPRemoveTool(d),
		NewMCPListTool(d),

		// Provider management (4)
		NewProviderConfigureTool(d),
		NewProviderListTool(d),
		NewProviderTestTool(d),
		NewModelsListTool(d),

		// Pin management (3)
		NewPinListTool(d),
		NewPinCreateTool(d),
		NewPinDeleteTool(d),

		// Config (2)
		NewConfigGetTool(d),
		NewConfigSetTool(d),

		// Diagnostics / utility (4)
		NewDoctorRunTool(d),
		NewBackupCreateTool(d),
		NewCostQueryTool(d),
		NewNavigateTool(d, navCb),
	}
}

// BuildRegistry creates a ToolRegistry containing all 41 system tools.
// Use this registry as the backing store for the SystemToolHandler.
func BuildRegistry(d *Deps, navCb NavigateCallback) *tools.ToolRegistry {
	reg := tools.NewToolRegistry()
	for _, t := range AllTools(d, navCb) {
		reg.Register(t)
	}
	return reg
}
