//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package middleware

import (
	"log/slog"
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// RequireNotBypass gates admin-only user-management and security-setting
// endpoints. When gateway.dev_mode_bypass is true every request is auth'd
// as admin, so these endpoints would otherwise be anonymously reachable —
// a catastrophic elevation. Returning 503 disables the surface entirely in
// that mode.
//
// The config is read from the request context (written by
// configSnapshotMiddleware). If the snapshot is missing we fail closed with
// 503 rather than silently passing through: a handler chain that skipped the
// snapshot middleware cannot be trusted to have applied auth either.
//
// Forensic logging (CRIT-2 from #155 silent-failure review): every 503
// emits a structured slog.Warn with the request path, remote address, and
// whether the trip was due to bypass-on or missing-config. Operators
// triaging "why am I getting 503s on admin routes?" must be able to see
// the cause without enabling debug-level logging. The audit emit is wired
// at the route layer in pkg/gateway/rest.go because this package
// intentionally has no audit dep.
func RequireNotBypass(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, _ := r.Context().Value(ctxkey.ConfigContextKey{}).(*config.Config)
		cfgMissing := cfg == nil
		bypassOn := cfg != nil && cfg.Gateway.DevModeBypass
		if cfgMissing || bypassOn {
			slog.Warn("gateway.admin_route_blocked_by_bypass_gate",
				"path", r.URL.Path,
				"remote_addr", r.RemoteAddr,
				"reason", reasonForBypassBlock(cfgMissing, bypassOn))
			writeJSONErr(w, http.StatusServiceUnavailable, "user management disabled in dev-mode-bypass")
			return
		}
		next(w, r)
	}
}

func reasonForBypassBlock(cfgMissing, bypassOn bool) string {
	switch {
	case cfgMissing:
		return "config_snapshot_missing"
	case bypassOn:
		return "dev_mode_bypass_on"
	default:
		return "unknown"
	}
}
