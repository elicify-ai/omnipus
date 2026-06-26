// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sysagent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// ConfirmationFunc is called by the handler before executing a destructive tool.
// It must return (true, nil) only when the user has clicked the UI confirmation button.
// Returns (false, nil) when the user canceled, or (false, err) on error.
type ConfirmationFunc func(ctx context.Context, toolName string, args map[string]any) (confirmed bool, err error)

// SystemToolHandler wraps the system tool registry with RBAC, rate limiting,
// and audit logging. It is the single entry point for all system tool invocations.
type SystemToolHandler struct {
	registry    *tools.ToolRegistry
	rateLimiter *SystemRateLimiter
	audit       *audit.Logger
	confirm     ConfirmationFunc
}

// HandlerConfig groups the dependencies for a SystemToolHandler.
type HandlerConfig struct {
	// Registry holds the 35 registered system tools.
	Registry *tools.ToolRegistry
	// Audit is the audit logger (SEC-15).
	Audit *audit.Logger
	// Confirm is called for destructive operations to obtain UI-level confirmation.
	// If nil, destructive ops are denied in non-single-user mode.
	Confirm ConfirmationFunc
}

// NewSystemToolHandler creates a handler with the given config.
func NewSystemToolHandler(cfg HandlerConfig) *SystemToolHandler {
	return &SystemToolHandler{
		registry:    cfg.Registry,
		rateLimiter: NewSystemRateLimiter(),
		audit:       cfg.Audit,
		confirm:     cfg.Confirm,
	}
}

// Handle executes a system tool call after RBAC, rate limit, confirmation, and
// audit checks. It is the authoritative execution path for all system.* tools.
//
// callerRole is the RBAC role of the calling device/session.
// deviceID identifies the calling device for audit purposes.
func (h *SystemToolHandler) Handle(
	ctx context.Context,
	callerRole PrincipalRole,
	deviceID string,
	toolName string,
	args map[string]any,
) *tools.ToolResult {
	start := time.Now()

	// 1. RBAC check (SEC-19).
	if err := CheckRBAC(callerRole, toolName); err != nil {
		slog.Warn("System tool RBAC denied",
			"tool", toolName,
			"caller_role", callerRole,
			"device_id", deviceID,
		)
		h.logAuditWithSource(toolName, deviceID, string(callerRole), args, "denied", "not_required", start)
		if denied, ok := err.(*PermissionDeniedError); ok {
			return tools.ErrorResult(FriendlyDenialMessage(denied))
		}
		return tools.ErrorResult(err.Error())
	}

	// 2. Rate limit check.
	if err := h.rateLimiter.Check(toolName); err != nil {
		slog.Warn("System tool rate-limited", "tool", toolName)
		h.logAuditWithSource(toolName, deviceID, string(callerRole), args, "rate_limited", "not_required", start)
		if rlErr, ok := err.(*RateLimitedError); ok {
			return tools.ErrorResult(fmt.Sprintf(
				"RATE_LIMITED: too many %s operations — please wait %.0f seconds before trying again.",
				rlErr.Category, rlErr.RetryAfterSeconds,
			))
		}
		return tools.ErrorResult(err.Error())
	}

	// 3. UI confirmation for destructive operations.
	// confirmationSource records how confirmation was obtained for the audit trail.
	confirmationSource := "not_required"
	if RequiresConfirmation(toolName) == ConfirmationUI {
		confirmed := false
		confirmationSource = "ui_button" // default; overridden below for single-user bypass

		// Single-user mode: if the LLM passes confirm:true in the tool arguments,
		// treat it as pre-approved. This unblocks destructive tools in open-source
		// single-user mode while preserving deny-by-default for multi-user deployments
		// where a real confirm func must be wired regardless of the arg.
		if callerRole == RoleSingleUser {
			if c, ok := args["confirm"].(bool); ok && c {
				confirmed = true
				confirmationSource = "llm_arg_single_user"
			}
		}

		if !confirmed {
			if h.confirm == nil {
				h.logAuditWithSource(
					toolName, deviceID, string(callerRole), args,
					"no_confirm_handler", confirmationSource, start,
				)
				return tools.ErrorResult(
					"CONFIRMATION_REQUIRED: this operation requires explicit confirmation but no " +
						"confirmation handler is configured (headless mode). " +
						"Operation denied.",
				)
			}
			ok, err := h.confirm(ctx, toolName, args)
			if err != nil {
				h.logAuditWithSource(
					toolName, deviceID, string(callerRole), args,
					"confirm_error", confirmationSource, start,
				)
				return tools.ErrorResult(fmt.Sprintf(
					"CONFIRMATION_ERROR: failed to obtain confirmation: %v", err))
			}
			if !ok {
				h.logAuditWithSource(
					toolName, deviceID, string(callerRole), args,
					"canceled", confirmationSource, start,
				)
				return tools.NewToolResult(
					`{"success":false,"status":"CONFIRMATION_REQUIRED","message":"Operation canceled by user."}`,
				)
			}
			// confirmed via real UI button — source stays "ui_button"
		}
	}

	// 4. Execute via registry.
	result := h.registry.ExecuteWithContext(ctx, toolName, args, "", "", nil)

	// 5. Audit log after execution (SEC-15).
	decision := "allowed"
	if result != nil && result.IsError {
		decision = "error"
	}
	h.logAuditWithSource(toolName, deviceID, string(callerRole), args, decision, confirmationSource, start)

	return result
}

// logAuditWithSource writes a system tool invocation to the audit trail
// (SEC-15), including a confirmation_source discriminator that records whether
// confirmation came from an LLM tool argument ("llm_arg_single_user"), a real
// UI button click ("ui_button"), or was not required ("not_required").
func (h *SystemToolHandler) logAuditWithSource(
	toolName, deviceID, callerRole string,
	args map[string]any,
	decision string,
	confirmationSource string,
	start time.Time,
) {
	if h.audit == nil {
		return
	}
	entry := &audit.Entry{
		Timestamp:  start,
		Event:      audit.EventToolCall,
		AgentID:    SystemAgentName,
		Tool:       toolName,
		Decision:   decision,
		Parameters: args,
		Details: map[string]any{
			"device_id":           deviceID,
			"caller_role":         callerRole,
			"confirmation_source": confirmationSource,
		},
	}
	if err := h.audit.Log(entry); err != nil {
		slog.Error("System tool audit log failed",
			"tool", toolName,
			"device_id", deviceID,
			"error", err,
		)
	}
}
