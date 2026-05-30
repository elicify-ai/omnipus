// Package systools — RequiresAdminAsk overrides for all system tools.
//
// Every tool in this package manages privileged platform infrastructure (agents,
// channels, providers, MCP servers, etc.). Accordingly, every tool overrides
// RequiresAdminAsk to return true, which:
//
//  1. Triggers the admin-ask fence in FilterToolsByPolicy (FR-061): custom agents
//     that have "allow" for a system.* tool get the policy downgraded to "ask",
//     enforcing human-in-the-loop approval regardless of the configured policy.
//  2. Gates the approval endpoint: non-admin users cannot approve system.* tool
//     executions (FR-015).
//
// This file contains ONLY the RequiresAdminAsk overrides. All other Tool
// interface methods (Name, Description, Parameters, Execute, Scope) live in
// their respective source files.

package systools

// Agent tools

func (*AgentCreateTool) RequiresAdminAsk() bool     { return true }
func (*AgentUpdateTool) RequiresAdminAsk() bool     { return true }
func (*AgentDeleteTool) RequiresAdminAsk() bool     { return true }
func (*AgentListTool) RequiresAdminAsk() bool       { return true }
func (*AgentActivateTool) RequiresAdminAsk() bool   { return true }
func (*AgentDeactivateTool) RequiresAdminAsk() bool { return true }

// Channel tools

func (*ChannelEnableTool) RequiresAdminAsk() bool    { return true }
func (*ChannelConfigureTool) RequiresAdminAsk() bool { return true }
func (*ChannelDisableTool) RequiresAdminAsk() bool   { return true }
func (*ChannelListTool) RequiresAdminAsk() bool      { return true }
func (*ChannelTestTool) RequiresAdminAsk() bool      { return true }

// Config tools

func (*ConfigGetTool) RequiresAdminAsk() bool { return true }
func (*ConfigSetTool) RequiresAdminAsk() bool { return true }

// Diagnostics tools

func (*DoctorRunTool) RequiresAdminAsk() bool    { return true }
func (*BackupCreateTool) RequiresAdminAsk() bool { return true }
func (*CostQueryTool) RequiresAdminAsk() bool    { return true }

// MCP tools

func (*MCPAddTool) RequiresAdminAsk() bool    { return true }
func (*MCPRemoveTool) RequiresAdminAsk() bool { return true }
func (*MCPListTool) RequiresAdminAsk() bool   { return true }

// Navigate tool

func (*NavigateTool) RequiresAdminAsk() bool { return true }

// Pin tools

func (*PinListTool) RequiresAdminAsk() bool   { return true }
func (*PinCreateTool) RequiresAdminAsk() bool { return true }
func (*PinDeleteTool) RequiresAdminAsk() bool { return true }

// Project tools

func (*ProjectCreateTool) RequiresAdminAsk() bool { return true }
func (*ProjectUpdateTool) RequiresAdminAsk() bool { return true }
func (*ProjectDeleteTool) RequiresAdminAsk() bool { return true }
func (*ProjectListTool) RequiresAdminAsk() bool   { return true }

// Provider tools

func (*ProviderConfigureTool) RequiresAdminAsk() bool { return true }
func (*ProviderListTool) RequiresAdminAsk() bool      { return true }
func (*ProviderTestTool) RequiresAdminAsk() bool      { return true }
func (*ModelsListTool) RequiresAdminAsk() bool        { return true }

// Skill tools

func (*SkillInstallTool) RequiresAdminAsk() bool { return true }
func (*SkillRemoveTool) RequiresAdminAsk() bool  { return true }
func (*SkillSearchTool) RequiresAdminAsk() bool  { return true }
func (*SkillListTool) RequiresAdminAsk() bool    { return true }

// System task tools

func (*TaskCreateTool) RequiresAdminAsk() bool { return true }
func (*TaskUpdateTool) RequiresAdminAsk() bool { return true }
func (*TaskDeleteTool) RequiresAdminAsk() bool { return true }
func (*TaskListTool) RequiresAdminAsk() bool   { return true }
