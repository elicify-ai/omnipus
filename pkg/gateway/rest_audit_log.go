//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
)

// HandleSandboxAuditLog handles PUT /api/v1/security/audit-log.
//
// PUT accepts {"enabled": bool} and persists to config.sandbox.audit_log via
// safeUpdateConfigJSON. Emits a security_setting_change audit entry before
// returning. Gated by adminWrap (withAuth → RequireNotBypass); dev_mode_bypass
// returns 503.
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
		// Pre-check: gen.AuditLogToggleRequest.Enabled is bool (not *bool), so
		// an empty {} body decodes as Enabled:false and would silently disable
		// audit logging. Guard against this by requiring the "enabled" key to be
		// present in the raw body before decode. This is safe because the body is
		// always small (< 100 bytes) and we reset it via bytes.NewReader below.
		rawBody, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "could not read request body")
			return
		}
		if !bytes.Contains(rawBody, []byte(`"enabled"`)) {
			jsonErr(w, http.StatusBadRequest, `request body must contain "enabled" field`)
			return
		}
		// Replace the consumed body so decodeAndValidate can re-read it.
		r.Body = io.NopCloser(bytes.NewReader(rawBody))

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
		// swap file handles. Do not call triggerReloadAndWait here; the requires_restart
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
