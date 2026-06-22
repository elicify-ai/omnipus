//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// godModeAvailable reports whether god mode CAN be enabled in this gateway: the
// build supports it (not the nogodmode tag, so sandbox.GodModeAvailable is true)
// AND --allow-god-mode was passed at boot (a.allowGodMode). This is the single
// availability predicate consulted by both the GET and POST handlers and is the
// REST-layer mirror of agent.GodModeActive's availability half.
func (a *restAPI) godModeAvailable() bool {
	return sandbox.GodModeAvailable && a.allowGodMode
}

// HandleGodMode dispatches GET (read state) and POST (toggle) for
// /api/v1/gateway/god-mode (O14). Registered with adminWrap (withAuth →
// RequireAdmin → RequireNotBypass), so dev_mode_bypass returns 503 before any
// handler logic runs. The POST additionally requires a single-use password
// re-auth consent token (requireReAuth) — god mode is the highest-blast-radius
// runtime switch in the product and must never be flippable without step-up.
func (a *restAPI) HandleGodMode(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.getGodMode(w, r)
	case http.MethodPost:
		a.setGodMode(w, r)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// getGodMode returns the current runtime god-mode state and availability.
func (a *restAPI) getGodMode(w http.ResponseWriter, _ *http.Request) {
	if a.agentLoop == nil {
		jsonErr(w, http.StatusServiceUnavailable, "god-mode: agent loop not initialized")
		return
	}
	available := a.godModeAvailable()
	// enabled is the persisted runtime switch, but it is only meaningful (and
	// only has any effect) when god mode is available. Report false when
	// unavailable so the UI never shows "on" for an inert switch.
	enabled := available && a.agentLoop.GetConfig().Sandbox.GodMode
	jsonOK(w, gen.GodModeStatus{
		Enabled:   enabled,
		Available: available,
	})
}

// setGodMode flips the global god-mode switch. Gated by step-up (requireReAuth)
// on top of adminWrap. Persists sandbox.god_mode, audit-logs the toggle with the
// acting user, and triggers a config reload so the override engine applies (or
// reverts) live without a restart.
func (a *restAPI) setGodMode(w http.ResponseWriter, r *http.Request) {
	if a.agentLoop == nil {
		jsonErr(w, http.StatusServiceUnavailable, "god-mode: agent loop not initialized")
		return
	}

	user, ok := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if !ok || user == nil {
		jsonErr(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Step-up consent gate (FR-12.2). MUST come before any state change — god
	// mode cannot be flipped without re-typing the password. requireReAuth
	// writes the 403 itself on a missing/invalid token.
	if !a.requireReAuth(w, r, user.Username) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var body gen.GodModeUpdateRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "GodModeUpdateRequest", &body, validateEnabled) {
		return
	}

	available := a.godModeAvailable()
	// Enabling god mode requires availability. Turning it OFF is always allowed
	// (fail-safe: an operator can always reach the more-restrictive state, even
	// in an unavailable build, to clear a stale persisted true).
	if body.Enabled && !available {
		if !sandbox.GodModeAvailable {
			jsonErr(w, http.StatusForbidden,
				"god mode is not available in this build (compiled with nogodmode)")
			return
		}
		jsonErr(w, http.StatusForbidden,
			"god mode requires --allow-god-mode at gateway boot")
		return
	}

	// Snapshot the old value inside the atomic config write so the audit diff is
	// consistent with the persisted state, not a pre-lock race copy.
	var oldGodMode bool
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		sb := ensureMap(m, "sandbox")
		if b, ok := sb["god_mode"].(bool); ok {
			oldGodMode = b
		}
		if body.Enabled {
			sb["god_mode"] = true
		} else {
			// Persist false explicitly is unnecessary (omitempty); delete keeps
			// the on-disk shape minimal and matches the default.
			delete(sb, "god_mode")
		}
		return nil
	}); err != nil {
		slog.Error("rest: update god_mode config", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	// Audit the toggle with the acting user BEFORE the reload so the trail is
	// written even if the reload below fails. Audit logging is never disabled by
	// god mode itself.
	if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
		if err := audit.EmitSecuritySettingChange(
			r.Context(), auditLogger,
			"gateway.god_mode",
			map[string]any{"enabled": oldGodMode, "username": user.Username},
			map[string]any{"enabled": body.Enabled, "username": user.Username},
		); err != nil {
			slog.Error("rest: audit emit god_mode change", "error", err)
		}
	}
	slog.Warn("god-mode toggled",
		"event", "god_mode_toggle",
		"enabled", body.Enabled,
		"actor", user.Username)

	// Apply (or revert) the override live: TriggerReload rebuilds every agent
	// instance from the just-written config, so agentToolsCfgToPolicy and the
	// loop's sandbox-profile resolution pick up the new sandbox.god_mode value.
	//
	// Reload-failure semantics mirror putToolPolicies: the config IS persisted
	// (safeUpdateConfigJSON already refreshed the in-memory config), so we never
	// 500 — that would wrongly signal the write failed. ErrReloadNotConfigured is
	// the no-reload-loop case (tests / minimal embeddings) and is benign. A
	// genuine reload error is logged at Error: running agents may keep the prior
	// override state until restart, which the operator must see in the logs.
	if err := a.agentLoop.TriggerReload(); err != nil {
		if errors.Is(err, agent.ErrReloadNotConfigured) {
			slog.Debug("rest: god_mode persisted; live reload not configured on this loop", "error", err)
		} else {
			// Persisted, but live agents may keep the previous override until restart.
			slog.Error("rest: reload after god_mode toggle failed", "error", err)
		}
	}

	enabled := available && a.agentLoop.GetConfig().Sandbox.GodMode
	jsonOK(w, gen.GodModeStatus{
		Enabled:   enabled,
		Available: available,
	})
}
