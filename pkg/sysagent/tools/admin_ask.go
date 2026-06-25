// Package systools — RequiresAdminAsk stubs for all system tools.
//
// RETIRED: the role-based admin-ask fence is disabled — there is no "admin" role
// anymore, so a gate that "asks the admin" has no one to ask. Every tool now
// returns false here; access control is the per-agent tool POLICY (allow / ask /
// deny) plus the destructive-op confirmation (RequiresConfirmation → ConfirmationUI).
// The policy-level "ask" still routes through approval, so consent isn't gone —
// it's policy-driven, not role-driven. A real approval model may be reintroduced
// later (then these flip back, or move to a dedicated gate).
//
// These overrides are kept (returning false) only because the system-tool structs
// don't embed BaseTool; deleting them would break the Tool interface. All other
// Tool methods (Name, Description, Parameters, Execute, Scope) live in their
// respective source files.

package systools

// Agent tools

func (*AgentCreateTool) RequiresAdminAsk() bool        { return false }
func (*AgentUpdateTool) RequiresAdminAsk() bool        { return false }
func (*AgentDeleteTool) RequiresAdminAsk() bool        { return false }
func (*AgentActivateTool) RequiresAdminAsk() bool      { return false }
func (*AgentDeactivateTool) RequiresAdminAsk() bool    { return false }
func (*AgentReadMetadataTool) RequiresAdminAsk() bool  { return false }
func (*AgentWriteMetadataTool) RequiresAdminAsk() bool { return false }

// Channel tools

func (*ChannelEnableTool) RequiresAdminAsk() bool    { return false }
func (*ChannelConfigureTool) RequiresAdminAsk() bool { return false }
func (*ChannelDisableTool) RequiresAdminAsk() bool   { return false }
func (*ChannelListTool) RequiresAdminAsk() bool      { return false }
func (*ChannelTestTool) RequiresAdminAsk() bool      { return false }

// Config tools

func (*ConfigGetTool) RequiresAdminAsk() bool { return false }
func (*ConfigSetTool) RequiresAdminAsk() bool { return false }

// Diagnostics tools

func (*DoctorRunTool) RequiresAdminAsk() bool { return false }
func (*CostQueryTool) RequiresAdminAsk() bool { return false }

// MCP tools

func (*MCPAddTool) RequiresAdminAsk() bool    { return false }
func (*MCPRemoveTool) RequiresAdminAsk() bool { return false }
func (*MCPListTool) RequiresAdminAsk() bool   { return false }

// Navigate tool

func (*NavigateTool) RequiresAdminAsk() bool { return false }

// Workspace tools

func (*WorkspaceCreateTool) RequiresAdminAsk() bool { return false }
func (*WorkspaceUpdateTool) RequiresAdminAsk() bool { return false }
func (*WorkspaceDeleteTool) RequiresAdminAsk() bool { return false }
func (*WorkspaceListTool) RequiresAdminAsk() bool   { return false }
func (*WorkspaceGetTool) RequiresAdminAsk() bool    { return false }

// Provider tools

func (*ProviderConfigureTool) RequiresAdminAsk() bool { return false }
func (*ProviderListTool) RequiresAdminAsk() bool      { return false }
func (*ProviderTestTool) RequiresAdminAsk() bool      { return false }
func (*ModelsListTool) RequiresAdminAsk() bool        { return false }

// Skill tools

func (*SkillRemoveTool) RequiresAdminAsk() bool { return false }
func (*SkillListTool) RequiresAdminAsk() bool   { return false }

// Skill authoring writes mutate the skills tree — they always require admin
// approval when policy is "ask" (consent gate, FR-9.2).

func (*SkillCreateTool) RequiresAdminAsk() bool { return false }
func (*SkillEditTool) RequiresAdminAsk() bool   { return false }

// System task tools

func (*TaskCreateTool) RequiresAdminAsk() bool { return false }
func (*TaskUpdateTool) RequiresAdminAsk() bool { return false }
func (*TaskDeleteTool) RequiresAdminAsk() bool { return false }
func (*TaskListTool) RequiresAdminAsk() bool   { return false }
