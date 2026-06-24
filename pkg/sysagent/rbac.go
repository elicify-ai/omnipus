// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package sysagent implements the Omnipus built-in system agent per BRD
// Appendix D. It uses the user's configured LLM provider and exposes the
// 37 management tools for managing agents, channels, providers, skills,
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
	"create_agent":         {MinRole: RoleOperator},
	"update_agent":         {MinRole: RoleOperator},
	"delete_agent":         {MinRole: RoleAdmin},
	"activate_agent":       {MinRole: RoleOperator},
	"deactivate_agent":     {MinRole: RoleOperator},
	"read_agent_metadata":  {MinRole: RoleViewer},
	"write_agent_metadata": {MinRole: RoleOperator},

	// Workspace management.
	"create_workspace": {MinRole: RoleOperator},
	"update_workspace": {MinRole: RoleOperator},
	"delete_workspace": {MinRole: RoleAdmin},
	"list_workspaces":  {MinRole: RoleViewer},
	"get_workspace":    {MinRole: RoleViewer},

	// Task management.
	"create_task_in_workspace": {MinRole: RoleOperator},
	"update_task_in_workspace": {MinRole: RoleOperator},
	"delete_task_in_workspace": {MinRole: RoleAdmin},
	"list_tasks_in_workspace":  {MinRole: RoleViewer},

	// Channel management.
	"enable_channel":    {MinRole: RoleOperator},
	"configure_channel": {MinRole: RoleOperator},
	"disable_channel":   {MinRole: RoleOperator},
	"list_channels":     {MinRole: RoleViewer},
	"test_channel":      {MinRole: RoleViewer},

	// Skill management.
	"remove_skill": {MinRole: RoleAdmin},
	"list_skills":  {MinRole: RoleViewer},
	// Skill authoring (FR-9.2): consent-gated writes that mutate the skills tree.
	"create_skill": {MinRole: RoleAdmin},
	"edit_skill":   {MinRole: RoleAdmin},

	// MCP server management.
	"add_mcp_server":    {MinRole: RoleOperator},
	"remove_mcp_server": {MinRole: RoleAdmin},
	"list_mcp_servers":  {MinRole: RoleViewer},

	// Provider management.
	"configure_provider": {MinRole: RoleOperator},
	"list_providers":     {MinRole: RoleViewer},
	"test_provider":      {MinRole: RoleViewer},
	"list_models":        {MinRole: RoleViewer},

	// Config.
	"get_config": {MinRole: RoleViewer},
	"set_config": {MinRole: RoleOperator},

	// Diagnostics / utility.
	"run_doctor": {MinRole: RoleViewer},
	"query_cost": {MinRole: RoleViewer},
	"navigate":   {MinRole: RoleViewer},
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
