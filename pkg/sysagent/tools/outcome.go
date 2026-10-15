// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import "github.com/elicify-ai/omnipus/pkg/tools"

// Package-local short names for the shared FR-030 change-tool verdict
// (pkg/tools/change_outcome.go) — every system change tool uses them.
type changeOutcome = tools.ChangeOutcome

const outcomeWorking = tools.OutcomeWorking

func working(checkScope string, notTested ...string) changeOutcome {
	return tools.ChangeWorking(checkScope, notTested...)
}

func savedNotWorking(reason, checkScope string, notTested ...string) changeOutcome {
	return tools.ChangeSavedNotWorking(reason, checkScope, notTested...)
}

func partlySaved(reason, checkScope string, missing []string, notTested ...string) changeOutcome {
	return tools.ChangePartlySaved(reason, checkScope, missing, notTested...)
}

func changeSuccess(fields map[string]any, o changeOutcome) *tools.ToolResult {
	return tools.ChangeSuccessResult(fields, o)
}

func changeErrorWithOutcome(fields map[string]any, o changeOutcome) *tools.ToolResult {
	return tools.ChangeErrorResult(fields, o)
}

func finalizeChangeResult(tool string, res *tools.ToolResult) *tools.ToolResult {
	return tools.FinalizeChangeResult(tool, res)
}

// Every system change tool is a tools.ChangeTool, so the registry's own
// argument-schema refusal also carries the "failed: nothing saved" envelope.
var (
	_ tools.ChangeTool = (*AgentCreateTool)(nil)
	_ tools.ChangeTool = (*AgentUpdateTool)(nil)
	_ tools.ChangeTool = (*AgentDeleteTool)(nil)
	_ tools.ChangeTool = (*AgentPermissionsTool)(nil)
	_ tools.ChangeTool = (*WorkspaceCreateTool)(nil)
	_ tools.ChangeTool = (*WorkspaceUpdateTool)(nil)
	_ tools.ChangeTool = (*WorkspaceDeleteTool)(nil)
	_ tools.ChangeTool = (*ProviderConfigureTool)(nil)
	_ tools.ChangeTool = (*MCPAddTool)(nil)
	_ tools.ChangeTool = (*MCPRemoveTool)(nil)
	_ tools.ChangeTool = (*ChannelConfigureTool)(nil)
	_ tools.ChangeTool = (*ChannelEnableTool)(nil)
	_ tools.ChangeTool = (*ChannelDisableTool)(nil)
	_ tools.ChangeTool = (*SkillCreateTool)(nil)
	_ tools.ChangeTool = (*SkillEditTool)(nil)
	_ tools.ChangeTool = (*SkillRemoveTool)(nil)
)

func (t *AgentCreateTool) ReportsChangeOutcome()       {}
func (t *AgentUpdateTool) ReportsChangeOutcome()       {}
func (t *AgentDeleteTool) ReportsChangeOutcome()       {}
func (t *AgentPermissionsTool) ReportsChangeOutcome()  {}
func (t *WorkspaceCreateTool) ReportsChangeOutcome()   {}
func (t *WorkspaceUpdateTool) ReportsChangeOutcome()   {}
func (t *WorkspaceDeleteTool) ReportsChangeOutcome()   {}
func (t *ProviderConfigureTool) ReportsChangeOutcome() {}
func (t *MCPAddTool) ReportsChangeOutcome()            {}
func (t *MCPRemoveTool) ReportsChangeOutcome()         {}
func (t *ChannelConfigureTool) ReportsChangeOutcome()  {}
func (t *ChannelEnableTool) ReportsChangeOutcome()     {}
func (t *ChannelDisableTool) ReportsChangeOutcome()    {}
func (t *SkillCreateTool) ReportsChangeOutcome()       {}
func (t *SkillEditTool) ReportsChangeOutcome()         {}
func (t *SkillRemoveTool) ReportsChangeOutcome()       {}
