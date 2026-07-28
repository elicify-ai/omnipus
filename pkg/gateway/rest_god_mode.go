// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// godModeAvailable reports whether god mode is ACTIVE-CAPABLE in this boot:
// the build supports it (not the nogodmode tag, so sandbox.GodModeAvailable is
// true) AND this process was authorized at boot (a.allowGodMode). a.allowGodMode
// is itself the OR of the legacy --allow-god-mode CLI flag and the
// config-persisted sandbox.god_mode_allowed grant — see
// pkg/gateway/gateway.go's resolveAllowGodMode, the single point where those
// two sources combine. Both a.allowGodMode and sandbox.GodModeAvailable are
// frozen at boot, so this predicate's answer never changes for the life of
// the process — a config-only grant (UI enable) requires a restart before
// this flips true. This is the single availability predicate consulted by
// both the GET and POST handlers and is the REST-layer mirror of
// agent.GodModeActive's availability half.
//
// godModeSupported, by contrast, answers a DIFFERENT question — whether this
// build has god mode compiled in at all — and does not depend on any boot
// authorization. Enabling god mode (see setGodMode) is gated on
// godModeSupported, not on godModeAvailable: the whole point of the UI-driven
// flow is that enabling GRANTS availability (for the next boot), it does not
// require it up front.
func (a *restAPI) godModeAvailable() bool {
	return sandbox.GodModeAvailable && a.allowGodMode
}

// godModeSupported reports whether this build has god mode compiled in at
// all (false only under the nogodmode build tag). See godModeAvailable's doc
// comment for how this differs from availability.
func (a *restAPI) godModeSupported() bool {
	return sandbox.GodModeAvailable
}

// HandleGodMode dispatches GET (read state) and POST (toggle) for
// /api/v1/gateway/god-mode (O14). Registered with adminWrap (withAuth →
// RequireNotBypass), so dev_mode_bypass returns 503 before any handler logic
// runs. The POST additionally requires a single-use password re-auth consent
// token (requireReAuth) — god mode is the highest-blast-radius runtime switch
// in the product and must never be flippable without step-up.
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

// getGodMode returns the current runtime god-mode state, availability, and
// build support.
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
		Supported: a.godModeSupported(),
	})
}

// setGodMode flips the global god-mode switch (O14). Gated by step-up
// (requireReAuth) on top of adminWrap.
//
// ENABLING is permitted whenever this build SUPPORTS god mode
// (godModeSupported), regardless of whether it is already AVAILABLE
// (godModeAvailable) in this boot — that asymmetry is the whole point of the
// UI-driven enablement flow: flipping the switch is how an unauthorized boot
// GETS authorized. When the boot was not already available, this call
// persists BOTH sandbox.god_mode_allowed=true (the authorization grant) and
// sandbox.god_mode=true (the runtime switch) in the same atomic write, but
// the override has no live effect until the gateway restarts and
// re-evaluates availability — the response's restart_required flag says so
// explicitly. When the boot was already available (legacy --allow-god-mode,
// or a previous UI-enable that has since been restarted), enabling applies
// live via TriggerReload exactly as before, and restart_required is false.
//
// DISABLING is always permitted (fail-safe: an operator can always reach the
// more-restrictive state, even on an unavailable/unsupported build, to clear
// a stale persisted true) and always applies live — restart_required is
// always false for a disable. Disabling clears sandbox.god_mode but
// deliberately leaves sandbox.god_mode_allowed untouched: authorization, once
// granted, persists so future enable/disable toggles do not require another
// restart.
//
// Every toggle is audit-logged with the acting user BEFORE the reload, so the
// trail is written even if the reload fails.
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

	// available reflects THIS boot's frozen authorization decision — captured
	// once, before any config mutation below, so restartRequired (computed
	// from it) describes whether the toggle we are about to persist has any
	// live effect in the CURRENT process.
	available := a.godModeAvailable()
	// Enabling requires only build SUPPORT, not prior availability (see the
	// function doc comment). Disabling is unconditional. sandbox.GodModeAvailable
	// (not godModeSupported()'s receiver form) is used directly here so the
	// 403 branch cannot be confused with the availability predicate above.
	if body.Enabled && !sandbox.GodModeAvailable {
		jsonErr(w, http.StatusForbidden,
			"god mode is not available in this build (compiled with nogodmode)")
		return
	}
	// restartRequired: enabling while this boot was not already available
	// changes config authorization only — the boot-frozen availability atomic
	// does not re-evaluate until the next restart. Disabling (or enabling when
	// already available) always applies live, so this is false in every other
	// case.
	restartRequired := body.Enabled && !available

	// Snapshot the old values inside the atomic config write so the audit diff
	// is consistent with the persisted state, not a pre-lock race copy.
	var oldGodMode, oldGodModeAllowed bool
	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		sb := ensureMap(m, "sandbox")
		if b, ok := sb["god_mode"].(bool); ok {
			oldGodMode = b
		}
		if b, ok := sb["god_mode_allowed"].(bool); ok {
			oldGodModeAllowed = b
		}
		if body.Enabled {
			sb["god_mode"] = true
			// Persist the authorization grant alongside the runtime switch.
			// This is what makes the UI-driven flow durable across restarts:
			// on the next boot, resolveAllowGodMode reads this key and ORs it
			// with --allow-god-mode to recompute availability.
			sb["god_mode_allowed"] = true
		} else {
			// Persist false explicitly is unnecessary (omitempty); delete keeps
			// the on-disk shape minimal and matches the default. Deliberately
			// do NOT touch god_mode_allowed here — disabling must not revoke
			// authorization (see function doc comment).
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
	// god mode itself. Include god_mode_allowed in the diff so the audit trail
	// captures the full authorization change, not just the runtime switch —
	// this is the highest-blast-radius control in the product (SEC-17).
	if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
		newGodModeAllowed := oldGodModeAllowed || body.Enabled
		if err := audit.EmitSecuritySettingChange(
			r.Context(),
			auditLogger,
			"gateway.god_mode",
			map[string]any{"enabled": oldGodMode, "god_mode_allowed": oldGodModeAllowed, "username": user.Username},
			map[string]any{
				"enabled":          body.Enabled,
				"god_mode_allowed": newGodModeAllowed,
				"username":         user.Username,
				"restart_required": restartRequired,
			},
		); err != nil {
			slog.Error("rest: audit emit god_mode change", "error", err)
		}
	}
	slog.Warn("god-mode toggled",
		"event", "god_mode_toggle",
		"enabled", body.Enabled,
		"restart_required", restartRequired,
		"actor", user.Username)

	// Apply (or revert) the override live when this boot is already available:
	// TriggerReload rebuilds every agent instance from the just-written config,
	// so agentToolsCfgToPolicy and the loop's sandbox-profile resolution pick up
	// the new sandbox.god_mode value. When restartRequired is true this call is
	// still harmless (and still needed for the disable-only-changed-god_mode
	// case within the same request lifecycle), but the override will not
	// actually take effect until the boot-frozen availability atomic is
	// recomputed on the next restart.
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
			// Persisted, but live agents may keep the previous override state.
			slog.Error("rest: reload after god_mode toggle failed", "error", err)
		}
	}

	jsonOK(w, gen.GodModeUpdateResponse{
		Enabled:         body.Enabled,
		RestartRequired: restartRequired,
	})
}
