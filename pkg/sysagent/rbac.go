// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package sysagent implements the Omnipus built-in system agent per BRD
// Appendix D. It uses the user's configured LLM provider and exposes the
// 41 system.* tools for managing agents, channels, providers, skills,
// config, diagnostics, and more. These tools are now ordinary builtins
// on the central registry, governed by per-agent policy (FR-045).
//
// RBAC (SEC-19): tool access is gated by the caller's role.
// Rate limiting: per-category sliding windows guard against runaway ops.
// Audit logging (SEC-15): every system tool invocation is logged.
package sysagent

import "fmt"

// PrincipalRole is the RBAC role of the caller invoking a system tool.
// This is distinct from config.UserRole (human user roles) — PrincipalRole
// models the agent/principal access level for system tool operations.
type PrincipalRole string

const (
	// RoleAdmin has full system tool access including destructive operations.
	RoleAdmin PrincipalRole = "admin"
	// RoleOperator can create/configure but not destroy or modify security settings.
	RoleOperator PrincipalRole = "operator"
	// RoleViewer has read-only system tool access.
	RoleViewer PrincipalRole = "viewer"
	// RoleAgent means the caller is a user agent — no system tool access.
	RoleAgent PrincipalRole = "agent"
	// RoleSingleUser is the default when RBAC is not configured. All ops allowed
	// (with UI confirmation for destructive ones).
	RoleSingleUser PrincipalRole = "single_user"
)

// ToolPermission describes the minimum RBAC role required to call a system tool.
type ToolPermission struct {
	// MinRole is the minimum role required. RoleSingleUser bypasses all RBAC checks.
	MinRole PrincipalRole
}

// toolPermissions maps each system tool name to its required minimum role.
// Derived from BRD Appendix D §D.5.3 and §D.5.4.
var toolPermissions = map[string]ToolPermission{
	// Agent management — destructive require admin.
	"system.agent.create":         {MinRole: RoleOperator},
	"system.agent.update":         {MinRole: RoleOperator},
	"system.agent.delete":         {MinRole: RoleAdmin},
	"system.agent.list":           {MinRole: RoleViewer},
	"system.agent.activate":       {MinRole: RoleOperator},
	"system.agent.deactivate":     {MinRole: RoleOperator},
	"system.agent.read_metadata":  {MinRole: RoleViewer},
	"system.agent.write_metadata": {MinRole: RoleOperator},

	// Workspace management.
	"system.workspace.create": {MinRole: RoleOperator},
	"system.workspace.update": {MinRole: RoleOperator},
	"system.workspace.delete": {MinRole: RoleAdmin},
	"system.workspace.list":   {MinRole: RoleViewer},
	"system.workspace.get":    {MinRole: RoleViewer},

	// Task management.
	"system.task.create": {MinRole: RoleOperator},
	"system.task.update": {MinRole: RoleOperator},
	"system.task.delete": {MinRole: RoleAdmin},
	"system.task.list":   {MinRole: RoleViewer},

	// Channel management.
	"system.channel.enable":    {MinRole: RoleOperator},
	"system.channel.configure": {MinRole: RoleOperator},
	"system.channel.disable":   {MinRole: RoleOperator},
	"system.channel.list":      {MinRole: RoleViewer},
	"system.channel.test":      {MinRole: RoleViewer},

	// Skill management.
	"system.skill.install": {MinRole: RoleOperator},
	"system.skill.remove":  {MinRole: RoleAdmin},
	"system.skill.search":  {MinRole: RoleViewer},
	"system.skill.list":    {MinRole: RoleViewer},
	// Skill authoring (FR-9.2): consent-gated writes that mutate the skills tree.
	"system.skill.create": {MinRole: RoleAdmin},
	"system.skill.edit":   {MinRole: RoleAdmin},

	// MCP server management.
	"system.mcp.add":    {MinRole: RoleOperator},
	"system.mcp.remove": {MinRole: RoleAdmin},
	"system.mcp.list":   {MinRole: RoleViewer},

	// Provider management.
	"system.provider.configure": {MinRole: RoleOperator},
	"system.provider.list":      {MinRole: RoleViewer},
	"system.provider.test":      {MinRole: RoleViewer},
	"system.models.list":        {MinRole: RoleViewer},

	// Config.
	"system.config.get": {MinRole: RoleViewer},
	"system.config.set": {MinRole: RoleOperator},

	// Diagnostics / utility.
	"system.doctor.run": {MinRole: RoleViewer},
	"system.cost.query": {MinRole: RoleViewer},
	"system.navigate":   {MinRole: RoleViewer},
}

// roleWeight returns a numeric weight for ordering; higher = more privileged.
func roleWeight(r PrincipalRole) int {
	switch r {
	case RoleAdmin:
		return 4
	case RoleOperator:
		return 3
	case RoleViewer:
		return 2
	case RoleAgent:
		return 1
	default:
		return 0
	}
}

// PermissionDeniedError is returned when the caller's role is insufficient.
type PermissionDeniedError struct {
	Tool     string
	Caller   PrincipalRole
	Required PrincipalRole
}

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf(
		"PERMISSION_DENIED: tool %q requires %s access, caller has %s",
		e.Tool, e.Required, e.Caller,
	)
}

// CheckRBAC returns nil when callerRole may call toolName, or PermissionDeniedError.
// Single-user mode (RoleSingleUser) bypasses role checks — RBAC only applies when
// SEC-19 is enabled.
func CheckRBAC(callerRole PrincipalRole, toolName string) error {
	// User agents never get system tool access.
	if callerRole == RoleAgent {
		return &PermissionDeniedError{Tool: toolName, Caller: callerRole, Required: RoleViewer}
	}
	// Single-user mode: no restrictions.
	if callerRole == RoleSingleUser {
		return nil
	}
	perm, known := toolPermissions[toolName]
	if !known {
		// Unknown tool: deny by default.
		return &PermissionDeniedError{Tool: toolName, Caller: callerRole, Required: RoleOperator}
	}
	if roleWeight(callerRole) < roleWeight(perm.MinRole) {
		return &PermissionDeniedError{Tool: toolName, Caller: callerRole, Required: perm.MinRole}
	}
	return nil
}

// FriendlyDenialMessage returns a conversational explanation of a denial.
func FriendlyDenialMessage(err *PermissionDeniedError) string {
	return fmt.Sprintf(
		"That operation requires %s access. You're connected as %s.",
		err.Required, err.Caller,
	)
}
