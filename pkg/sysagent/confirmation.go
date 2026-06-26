// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sysagent

// ConfirmationLevel describes how a system tool call must be confirmed.
// Per BRD Appendix D §D.5.3.
type ConfirmationLevel int

const (
	// ConfirmationNone — additive/safe operation, no confirmation required.
	ConfirmationNone ConfirmationLevel = iota
	// ConfirmationUI — destructive operation; requires explicit UI button click.
	// LLM-generated text ("yes, I confirm") is NOT accepted.
	ConfirmationUI
)

// toolConfirmation maps each system tool to its confirmation requirement.
var toolConfirmation = map[string]ConfirmationLevel{
	// No confirmation required — safe/additive operations.
	"create_agent":             ConfirmationNone,
	"update_agent":             ConfirmationNone,
	"read_agent_metadata":      ConfirmationNone,
	"write_agent_metadata":     ConfirmationNone,
	"create_workspace":         ConfirmationNone,
	"update_workspace":         ConfirmationNone,
	"list_workspaces":          ConfirmationNone,
	"get_workspace":            ConfirmationNone,
	"create_task_in_workspace": ConfirmationNone,
	"update_task_in_workspace": ConfirmationNone,
	"list_tasks_in_workspace":  ConfirmationNone,
	"enable_channel":           ConfirmationNone,
	"configure_channel":        ConfirmationNone,
	"list_channels":            ConfirmationNone,
	"test_channel":             ConfirmationNone,
	"list_skills":              ConfirmationNone,
	"add_mcp_server":           ConfirmationNone,
	"list_mcp_servers":         ConfirmationNone,
	"configure_provider":       ConfirmationNone,
	"list_providers":           ConfirmationNone,
	"test_provider":            ConfirmationNone,
	"list_models":              ConfirmationNone,
	"get_config":               ConfirmationNone,
	"run_doctor":               ConfirmationNone,
	"get_usage":                ConfirmationNone,
	"navigate":                 ConfirmationNone,

	// UI confirmation required — destructive operations and consent-gated writes.
	"delete_agent":             ConfirmationUI,
	"delete_workspace":         ConfirmationUI,
	"delete_task_in_workspace": ConfirmationUI,
	"disable_channel":          ConfirmationUI,
	"remove_skill":             ConfirmationUI,
	"remove_mcp_server":        ConfirmationUI,
	// Skill authoring mutates the skills tree — explicit consent gate (FR-9.2).
	"create_skill": ConfirmationUI,
	"edit_skill":   ConfirmationUI,
	"set_config":   ConfirmationNone, // set is safe; security.* keys get UI confirmation at tool level
}

// RequiresConfirmation returns the confirmation level for a named tool.
// Falls back to ConfirmationUI for unknown tool names (deny-by-default).
func RequiresConfirmation(toolName string) ConfirmationLevel {
	level, ok := toolConfirmation[toolName]
	if !ok {
		return ConfirmationUI
	}
	return level
}
