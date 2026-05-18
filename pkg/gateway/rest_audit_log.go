//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// HandleSandboxAuditLog handles PUT /api/v1/security/audit-log.
//
// PUT accepts {"enabled": bool} and persists to config.sandbox.audit_log via
// safeUpdateConfigJSON. Emits a security_setting_change audit entry before
// returning. Admin-only; non-admin requests receive 403.
//
// Response shape:
//
//	{
//	  "saved":            true,
//	  "requires_restart": true,
//	  "applied_enabled":  <bool — value before this save>
//	}
//
// GET returns the current flag value:
//
//	{"enabled": bool}
func (a *restAPI) HandleSandboxAuditLog(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		jsonOK(w, gen.AuditLogToggle{
			Enabled: cfg.Sandbox.AuditLog,
		})

	case http.MethodPut:
		var body gen.AuditLogToggleRequest
		validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
		if !decodeAndValidate(w, r, "AuditLogToggleRequest", &body, validateEnabled) {
			return
		}

		oldEnabled := a.agentLoop.GetConfig().Sandbox.AuditLog
		newEnabled := body.Enabled

		if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
			ensureMap(m, "sandbox")["audit_log"] = newEnabled
			return nil
		}); err != nil {
			slog.Error("rest: update sandbox audit_log", "error", err)
			jsonErr(w, http.StatusInternalServerError, "could not save config")
			return
		}

		if err := audit.EmitSecuritySettingChange(
			r.Context(),
			a.agentLoop.AuditLogger(),
			"sandbox.audit_log",
			oldEnabled,
			newEnabled,
		); err != nil {
			slog.Error("rest: audit emit audit_log change", "error", err)
		}

		// audit_log is in RestartGatedKeys — changing it requires a restart to
		// swap file handles. Do not call awaitReload here; the requires_restart
		// response field informs the admin.
		jsonOK(w, gen.AuditLogUpdateResponse{
			Saved:           true,
			RequiresRestart: true,
			AppliedEnabled:  oldEnabled,
		})

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
