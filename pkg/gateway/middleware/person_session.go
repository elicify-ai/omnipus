// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/gateway/pathredact"
)

// RequirePersonSession admits only a signed-in person (T11 spec §3.2): a
// request carrying a Gateway.Users identity, on a gateway that is not in
// development-bypass mode, that did not authenticate with the CLI token.
// Every other request is answered 403 BEFORE the handler runs, so the body is
// never read and nothing the handler would start is started.
//
// Refused, in this order:
//   - development bypass (gateway.dev_mode_bypass) — even when a user is
//     attached, because bypass means no one was actually authenticated;
//   - a missing config snapshot — fail closed, as RequireNotBypass does, since
//     bypass state cannot be known;
//   - the CLI token (ctxkey.CLITokenContextKey) — the token's synthetic user
//     is not a person;
//   - no user (ctxkey.UserContextKey absent or empty);
//   - a user that is not one of the request snapshot's Gateway.Users accounts
//     — for example the synthetic identity the legacy OMNIPUS_BEARER_TOKEN
//     fallback attaches. Authentication that succeeded without a configured
//     person account is not a signed-in person.
//
// Used as a.withAuth(middleware.RequirePersonSession(h)), so it runs after
// bearer authentication has attached the user and the CLI-token marker.
func RequirePersonSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if reason := personSessionRefusal(r); reason != "" {
			slog.Warn("gateway.person_session_required",
				"path", pathredact.RequestPath(r.URL.Path),
				"remote_addr", r.RemoteAddr,
				"reason", reason)
			writePersonSessionRefusal(w)
			return
		}
		next(w, r)
	}
}

// personSessionRefusal returns the log reason for refusing r, or "" when r
// comes from a signed-in person.
func personSessionRefusal(r *http.Request) string {
	ctx := r.Context()
	cfg, _ := ctx.Value(ctxkey.ConfigContextKey{}).(*config.Config)
	switch {
	case cfg == nil:
		return "config_snapshot_missing"
	case cfg.Gateway.DevModeBypass:
		return "dev_mode_bypass_on"
	}
	if viaCLI, _ := ctx.Value(ctxkey.CLITokenContextKey{}).(bool); viaCLI {
		return "cli_token"
	}
	u, _ := ctx.Value(ctxkey.UserContextKey{}).(*config.UserConfig)
	if u == nil || u.Username == "" {
		return "no_user"
	}
	// Membership by username in the same config snapshot the request was
	// authenticated against. Pointer identity is not used: cookie
	// authentication attaches a copy of the account.
	for i := range cfg.Gateway.Users {
		if cfg.Gateway.Users[i].Username == u.Username {
			return ""
		}
	}
	return "not_a_configured_person"
}

// PersonSessionRequiredCode is the ErrorResponse code of the person-gate 403,
// so a client can tell this refusal apart from other 403s (CSRF, origin) and
// label only this one "needs a signed-in person" (T11 SF-4).
const PersonSessionRequiredCode = "person_session_required"

// writePersonSessionRefusal writes the 403 ErrorResponse with its code.
func writePersonSessionRefusal(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	code := PersonSessionRequiredCode
	if err := json.NewEncoder(w).Encode(gen.ErrorResponse{
		Error: "this action requires a signed-in person",
		Code:  &code,
	}); err != nil {
		slog.Debug("writePersonSessionRefusal: encode failed", "error", err)
	}
}
