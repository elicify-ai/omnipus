// Package systools — Category overrides for all system tools.
//
// Every tool in this package manages privileged platform infrastructure. The
// Category() method returns a domain-specific category rather than the legacy
// CategorySystem, so the tool registry and catalog can group tools by function.
//
// This file contains ONLY the Category overrides. All other Tool interface
// methods live in their respective source files.

package systools

import "github.com/elicify-ai/omnipus/pkg/tools"

// Agent tools

func (*AgentCreateTool) Category() tools.ToolCategory       { return tools.CategoryAgents }
func (*AgentUpdateTool) Category() tools.ToolCategory       { return tools.CategoryAgents }
func (*AgentDeleteTool) Category() tools.ToolCategory       { return tools.CategoryAgents }
func (*AgentReadMetadataTool) Category() tools.ToolCategory { return tools.CategoryAgents }

// Channel tools

func (*ChannelEnableTool) Category() tools.ToolCategory    { return tools.CategoryChannels }
func (*ChannelConfigureTool) Category() tools.ToolCategory { return tools.CategoryChannels }
func (*ChannelDisableTool) Category() tools.ToolCategory   { return tools.CategoryChannels }
func (*ChannelListTool) Category() tools.ToolCategory      { return tools.CategoryChannels }
func (*ChannelTestTool) Category() tools.ToolCategory      { return tools.CategoryChannels }

// Config tools

func (*ConfigGetTool) Category() tools.ToolCategory { return tools.CategoryPlatform }
func (*ConfigSetTool) Category() tools.ToolCategory { return tools.CategoryPlatform }

// Diagnostics tools

func (*DoctorRunTool) Category() tools.ToolCategory  { return tools.CategoryPlatform }
func (*UsageQueryTool) Category() tools.ToolCategory { return tools.CategoryPlatform }

// MCP tools

func (*MCPAddTool) Category() tools.ToolCategory    { return tools.CategoryMCP }
func (*MCPRemoveTool) Category() tools.ToolCategory { return tools.CategoryMCP }
func (*MCPListTool) Category() tools.ToolCategory   { return tools.CategoryMCP }

// Workspace tools

func (*WorkspaceCreateTool) Category() tools.ToolCategory { return tools.CategoryWorkspaces }
func (*WorkspaceUpdateTool) Category() tools.ToolCategory { return tools.CategoryWorkspaces }
func (*WorkspaceDeleteTool) Category() tools.ToolCategory { return tools.CategoryWorkspaces }
func (*WorkspaceListTool) Category() tools.ToolCategory   { return tools.CategoryWorkspaces }
func (*WorkspaceGetTool) Category() tools.ToolCategory    { return tools.CategoryWorkspaces }

// Provider tools

func (*ProviderConfigureTool) Category() tools.ToolCategory { return tools.CategoryProviders }
func (*ProviderListTool) Category() tools.ToolCategory      { return tools.CategoryProviders }
func (*ProviderTestTool) Category() tools.ToolCategory      { return tools.CategoryProviders }
func (*ModelsListTool) Category() tools.ToolCategory        { return tools.CategoryProviders }

// Skill tools

func (*SkillRemoveTool) Category() tools.ToolCategory { return tools.CategorySkills }
func (*SkillListTool) Category() tools.ToolCategory   { return tools.CategorySkills }
func (*SkillCreateTool) Category() tools.ToolCategory { return tools.CategorySkills }
func (*SkillEditTool) Category() tools.ToolCategory   { return tools.CategorySkills }

// System task tools

func (*TaskCreateTool) Category() tools.ToolCategory { return tools.CategoryTasks }
func (*TaskUpdateTool) Category() tools.ToolCategory { return tools.CategoryTasks }
func (*TaskDeleteTool) Category() tools.ToolCategory { return tools.CategoryTasks }
func (*TaskListTool) Category() tools.ToolCategory   { return tools.CategoryTasks }
