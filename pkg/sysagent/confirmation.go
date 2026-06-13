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
	"system.agent.create":       ConfirmationNone,
	"system.agent.update":       ConfirmationNone,
	"system.agent.list":         ConfirmationNone,
	"system.agent.activate":     ConfirmationNone,
	"system.agent.deactivate":   ConfirmationNone,
	"system.workspace.create":   ConfirmationNone,
	"system.workspace.update":   ConfirmationNone,
	"system.workspace.list":     ConfirmationNone,
	"system.task.create":        ConfirmationNone,
	"system.task.update":        ConfirmationNone,
	"system.task.list":          ConfirmationNone,
	"system.channel.enable":     ConfirmationNone,
	"system.channel.configure":  ConfirmationNone,
	"system.channel.list":       ConfirmationNone,
	"system.channel.test":       ConfirmationNone,
	"system.skill.install":      ConfirmationNone,
	"system.skill.search":       ConfirmationNone,
	"system.skill.list":         ConfirmationNone,
	"system.mcp.add":            ConfirmationNone,
	"system.mcp.list":           ConfirmationNone,
	"system.provider.configure": ConfirmationNone,
	"system.provider.list":      ConfirmationNone,
	"system.provider.test":      ConfirmationNone,
	"system.models.list":        ConfirmationNone,
	"system.pin.list":           ConfirmationNone,
	"system.pin.create":         ConfirmationNone,
	"system.config.get":         ConfirmationNone,
	"system.doctor.run":         ConfirmationNone,
	"system.cost.query":         ConfirmationNone,
	"system.navigate":           ConfirmationNone,
	"system.backup.create":      ConfirmationNone,

	// UI confirmation required — destructive operations.
	"system.agent.delete":     ConfirmationUI,
	"system.workspace.delete": ConfirmationUI,
	"system.task.delete":      ConfirmationUI,
	"system.channel.disable":  ConfirmationUI,
	"system.skill.remove":     ConfirmationUI,
	"system.mcp.remove":       ConfirmationUI,
	"system.pin.delete":       ConfirmationUI,
	"system.config.set":       ConfirmationNone, // set is safe; security.* keys get UI confirmation at tool level
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
