// Package systools — Category overrides for all system tools.
//
// Every tool in this package manages privileged platform infrastructure and
// returns CategorySystem, which distinguishes them from core agent tools
// (CategoryCore) in the tool registry and catalog.
//
// This file contains ONLY the Category overrides. All other Tool interface
// methods live in their respective source files.

package systools

import "github.com/dapicom-ai/omnipus/pkg/tools"

// Agent tools

func (*AgentCreateTool) Category() tools.ToolCategory     { return tools.CategorySystem }
func (*AgentUpdateTool) Category() tools.ToolCategory     { return tools.CategorySystem }
func (*AgentDeleteTool) Category() tools.ToolCategory     { return tools.CategorySystem }
func (*AgentListTool) Category() tools.ToolCategory       { return tools.CategorySystem }
func (*AgentActivateTool) Category() tools.ToolCategory   { return tools.CategorySystem }
func (*AgentDeactivateTool) Category() tools.ToolCategory { return tools.CategorySystem }

// Channel tools

func (*ChannelEnableTool) Category() tools.ToolCategory    { return tools.CategorySystem }
func (*ChannelConfigureTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*ChannelDisableTool) Category() tools.ToolCategory   { return tools.CategorySystem }
func (*ChannelListTool) Category() tools.ToolCategory      { return tools.CategorySystem }
func (*ChannelTestTool) Category() tools.ToolCategory      { return tools.CategorySystem }

// Config tools

func (*ConfigGetTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*ConfigSetTool) Category() tools.ToolCategory { return tools.CategorySystem }

// Diagnostics tools

func (*DoctorRunTool) Category() tools.ToolCategory    { return tools.CategorySystem }
func (*BackupCreateTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*CostQueryTool) Category() tools.ToolCategory    { return tools.CategorySystem }

// MCP tools

func (*MCPAddTool) Category() tools.ToolCategory    { return tools.CategorySystem }
func (*MCPRemoveTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*MCPListTool) Category() tools.ToolCategory   { return tools.CategorySystem }

// Navigate tool

func (*NavigateTool) Category() tools.ToolCategory { return tools.CategorySystem }

// Pin tools

func (*PinListTool) Category() tools.ToolCategory   { return tools.CategorySystem }
func (*PinCreateTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*PinDeleteTool) Category() tools.ToolCategory { return tools.CategorySystem }

// Project tools

func (*ProjectCreateTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*ProjectUpdateTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*ProjectDeleteTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*ProjectListTool) Category() tools.ToolCategory   { return tools.CategorySystem }

// Provider tools

func (*ProviderConfigureTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*ProviderListTool) Category() tools.ToolCategory      { return tools.CategorySystem }
func (*ProviderTestTool) Category() tools.ToolCategory      { return tools.CategorySystem }
func (*ModelsListTool) Category() tools.ToolCategory        { return tools.CategorySystem }

// Skill tools

func (*SkillInstallTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*SkillRemoveTool) Category() tools.ToolCategory  { return tools.CategorySystem }
func (*SkillSearchTool) Category() tools.ToolCategory  { return tools.CategorySystem }
func (*SkillListTool) Category() tools.ToolCategory    { return tools.CategorySystem }

// System task tools

func (*TaskCreateTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*TaskUpdateTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*TaskDeleteTool) Category() tools.ToolCategory { return tools.CategorySystem }
func (*TaskListTool) Category() tools.ToolCategory   { return tools.CategorySystem }
