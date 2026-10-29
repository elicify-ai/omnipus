// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

import (
	"log/slog"
	"net/http"

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
//   - no user (ctxkey.UserContextKey absent or empty).
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
			writeJSONErr(w, http.StatusForbidden, "this action requires a signed-in person")
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
	if u, _ := ctx.Value(ctxkey.UserContextKey{}).(*config.UserConfig); u == nil || u.Username == "" {
		return "no_user"
	}
	return ""
}
