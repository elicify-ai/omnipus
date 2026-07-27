//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/routing"
)

// canonicalDMScopes is the closed set of accepted values for session.dm_scope,
// imported from pkg/routing/session_key.go constants (single source of truth).
var canonicalDMScopes = [4]routing.DMScope{
	routing.DMScopeMain,
	routing.DMScopePerPeer,
	routing.DMScopePerChannelPeer,
	routing.DMScopePerAccountChannelPeer,
}

// dmScopeInvalidMsg lists all four canonical values so callers can correct
// legacy "global" or case-variant inputs.
var dmScopeInvalidMsg = fmt.Sprintf(
	`dm_scope must be exactly one of: %q, %q, %q, %q`,
	string(routing.DMScopeMain),
	string(routing.DMScopePerPeer),
	string(routing.DMScopePerChannelPeer),
	string(routing.DMScopePerAccountChannelPeer),
)

// HandleSessionScope handles GET + PUT /api/v1/security/session-scope.
//
// GET returns the current dm_scope from config.
// PUT accepts {"dm_scope": string} and persists session.dm_scope via safeUpdateConfigJSON.
// Session routing is cached at boot so all changes require a restart (requires_restart=true).
// Admin-only for PUT; emits audit entry with resource="session.dm_scope".
func (a *restAPI) HandleSessionScope(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg := a.agentLoop.GetConfig()
		scope := cfg.Session.DMScope
		if scope == "" {
			scope = string(routing.DMScopePerChannelPeer)
		}
		jsonOK(w, gen.SessionScopeResponse{
			DmScope: gen.SessionScopeResponseDmScope(scope),
		})

	case http.MethodPut:
		a.putSessionScope(w, r)

	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// putSessionScope is the admin-only body of PUT /api/v1/security/session-scope.
func (a *restAPI) putSessionScope(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var body gen.SessionScopeRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SessionScopeRequest", &body, validateEnabled) {
		return
	}

	if string(body.DmScope) == "" {
		jsonErr(w, http.StatusBadRequest, dmScopeInvalidMsg)
		return
	}

	valid := false
	for _, v := range canonicalDMScopes {
		if string(body.DmScope) == string(v) {
			valid = true
			break
		}
	}
	if !valid {
		jsonErr(w, http.StatusBadRequest, dmScopeInvalidMsg)
		return
	}

	cfg := a.agentLoop.GetConfig()
	oldScope := cfg.Session.DMScope
	if oldScope == "" {
		oldScope = string(routing.DMScopePerChannelPeer)
	}

	if err := a.safeUpdateConfigJSON(func(m map[string]any) error {
		ensureMap(m, "session")["dm_scope"] = string(body.DmScope)
		return nil
	}); err != nil {
		slog.Error("rest: update session dm_scope", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not save config")
		return
	}

	confirmed, reloadErr := a.triggerReloadAndWaitOutcome()
	if reloadErr != nil {
		if a.agentLoop != nil {
			if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
				if err := audit.EmitSecuritySettingChange(
					r.Context(), auditLogger,
					"session.dm_scope", oldScope, string(body.DmScope),
				); err != nil {
					slog.Error("rest: audit emit session dm_scope change", "error", err)
				}
			}
		}
		slog.Info("rest: session dm_scope updated (restart required)", "dm_scope", string(body.DmScope))
		warnMsg := "config saved to disk but hot-reload failed; restart the gateway to apply"
		jsonOK(w, gen.SessionScopeUpdateResponse{
			Saved:           true,
			RequiresRestart: true,
			AppliedDmScope:  oldScope,
			Warning:         &warnMsg,
		})
		return
	}
	if !confirmed {
		// Session routing already requires a restart regardless (cached at
		// boot — see this handler's doc comment), but the in-memory config
		// itself may not yet reflect dm_scope for any other consumer that
		// reads it live. Name the setting so an operator grepping
		// gateway.log can tell this specific change is the one still
		// unconfirmed, distinct from an unrelated concurrent reload.
		slog.Warn("rest: hot-reload after session dm_scope update did not confirm within the poll window",
			"dm_scope", string(body.DmScope))
	}

	if a.agentLoop != nil {
		if auditLogger := a.agentLoop.AuditLogger(); auditLogger != nil {
			if err := audit.EmitSecuritySettingChange(
				r.Context(),
				auditLogger,
				"session.dm_scope",
				oldScope,
				string(body.DmScope),
			); err != nil {
				slog.Error("rest: audit emit session dm_scope change", "error", err)
			}
		}
	}

	slog.Info("rest: session dm_scope updated", "dm_scope", string(body.DmScope))

	jsonOK(w, gen.SessionScopeUpdateResponse{
		Saved:           true,
		RequiresRestart: true,
		AppliedDmScope:  oldScope,
	})
}
