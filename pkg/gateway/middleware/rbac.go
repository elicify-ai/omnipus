//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package middleware provides HTTP middleware for the Omnipus gateway.
package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

// writeJSONErr writes {"error": msg} with the given HTTP status. A post-header
// encode failure is unactionable (the status is already on the wire) and is
// logged at debug level; the caller still sees the correct status.
func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Debug("writeJSONErr: encode failed", "error", err)
	}
}

// RequireAdmin returns a middleware that enforces admin-only access.
//
// Response matrix:
//   - No role in context (auth middleware skipped or token unrecognized) → 401
//   - Authenticated user with role != admin → 403 + {"error":"admin required"}
//   - Authenticated admin → delegates to next handler unchanged
//
// This middleware must sit after withAuth, which is the layer that verifies the
// bearer token and writes the role into the request context via RoleContextKey.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := r.Context().Value(ctxkey.RoleContextKey{}).(config.UserRole)
		if !ok || role == "" {
			// No role in context means the auth middleware did not run or did
			// not recognize the token — treat as unauthenticated.
			writeJSONErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if role != config.UserRoleAdmin {
			writeJSONErr(w, http.StatusForbidden, "admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
