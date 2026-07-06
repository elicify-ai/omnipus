//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
)

// HandlePromptGuard handles GET/PUT /api/v1/security/prompt-guard.
//
// GET returns the current prompt-injection level:
//
//	{"level": "low"|"medium"|"high", "requires_restart": false}
//
// PUT accepts {"level": "low"|"medium"|"high"} (case-sensitive), persists to
// config.sandbox.prompt_injection_level via safeUpdateConfigJSON, triggers a
// hot-reload via triggerReloadAndWait, and emits a security_setting_change audit entry.
// Changes take effect immediately — requires_restart is false on successful reload.
// PUT is gated by adminWrap (withAuth → RequireNotBypass); dev_mode_bypass returns 503.
func (a *restAPI) HandlePromptGuard(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		level := cfg.Sandbox.PromptInjectionLevel
		if level == "" {
			level = "medium"
		}
		jsonOK(w, gen.PromptGuardResponse{
			Level:           gen.PromptGuardResponseLevel(level),
			RequiresRestart: false,
		})

	case http.MethodPut:
		a.putPromptGuard(w, r)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// putPromptGuard is the handler body for PUT /api/v1/security/prompt-guard.
// Admin enforcement is handled by adminWrap at route registration in rest.go.
func (a *restAPI) putPromptGuard(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var body gen.PromptGuardUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "PromptGuardUpdateRequest", &body, validateEnabled) {
		return
	}
	switch string(body.Level) {
	case "low", "medium", "high":
	default:
		jsonErr(w, http.StatusBadRequest, `level must be one of: "low", "medium", "high"`)
		return
	}

	oldLevel := a.agentLoop.GetConfig().Sandbox.PromptInjectionLevel
	if oldLevel == "" {
		oldLevel = "medium"
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		ensureMap(m, "sandbox")["prompt_injection_level"] = string(body.Level)
		return nil
	}); err != nil {
		slog.Error("rest: update prompt_injection_level", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	if auditErr := audit.EmitSecuritySettingChange(r.Context(), a.agentLoop.AuditLogger(),
		"sandbox.prompt_injection_level", oldLevel, string(body.Level)); auditErr != nil {
		slog.Error("rest: audit emit prompt guard change", "error", auditErr)
	}

	if reloadErr := a.triggerReloadAndWait(); reloadErr != nil {
		slog.Info("rest: prompt guard level updated (restart required)", "level", string(body.Level))
		warnMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		jsonOK(w, gen.PromptGuardUpdateResponse{
			Saved:           true,
			RequiresRestart: true,
			AppliedLevel:    gen.PromptGuardUpdateResponseAppliedLevel(body.Level),
			Warning:         &warnMsg,
		})
		return
	}

	slog.Info("rest: prompt guard level updated", "level", string(body.Level))

	jsonOK(w, gen.PromptGuardUpdateResponse{
		Saved:           true,
		RequiresRestart: false,
		AppliedLevel:    gen.PromptGuardUpdateResponseAppliedLevel(body.Level),
	})
}
