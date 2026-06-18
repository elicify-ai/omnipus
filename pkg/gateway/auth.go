//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/gateway/ctxkey"
)

var warnUnauthOnce sync.Once

// configFromContext retrieves the config snapshot stored by configSnapshotMiddleware.
// Returns nil if no snapshot was stored (caller should fall back to GetConfig()).
func configFromContext(ctx context.Context) *config.Config {
	cfg, _ := ctx.Value(ctxkey.ConfigContextKey{}).(*config.Config)
	return cfg
}

// configContextKey is an unexported alias for ctxkey.ConfigContextKey.
// It is kept for internal use and test compatibility. External packages must
// use ctxkey.ConfigContextKey directly.
type configContextKey = ctxkey.ConfigContextKey

// RoleContextKey is an alias for ctxkey.RoleContextKey kept for compatibility
// with existing code in this package that uses the gateway-local type name.
type RoleContextKey = ctxkey.RoleContextKey

// UserContextKey is an alias for ctxkey.UserContextKey kept for compatibility
// with existing code in this package that uses the gateway-local type name.
type UserContextKey = ctxkey.UserContextKey

// AuthResult holds the outcome of a bearer token check.
type AuthResult struct {
	Authenticated bool
	Role          config.UserRole
	User          *config.UserConfig
}

// checkBearerAuth validates the Authorization header.
// It first checks config.Users (per-user RBAC), then falls back to the legacy
// OMNIPUS_BEARER_TOKEN env var (treated as admin role) for backward compatibility.
// Returns AuthResult so callers can distinguish no-auth from no-role.
func checkBearerAuth(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg *config.Config) AuthResult {
	auth := r.Header.Get("Authorization")
	prefix := "Bearer "

	// dev_mode_bypass short-circuit: when auth isn't configured AND bypass is
	// enabled, allow the request as admin (even with no Authorization header
	// at all). This makes the SPA reviewable on a fresh install without
	// onboarding first. Per-user RBAC and OMNIPUS_BEARER_TOKEN paths below
	// still run for the non-bypass case.
	if !strings.HasPrefix(auth, prefix) && cfg.Gateway.DevModeBypass {
		slog.Warn("AUTH-BYPASS", "users_count", len(cfg.Gateway.Users), "bypass_flag", cfg.Gateway.DevModeBypass, "has_bearer", strings.HasPrefix(auth, prefix))
		warnUnauthOnce.Do(func() {
			slog.Warn("DEV MODE: API has no authentication. Set gateway.dev_mode_bypass=false for production.")
		})
		return AuthResult{Authenticated: true, Role: config.UserRoleAdmin, User: &devBypassUser}
	}

	if !strings.HasPrefix(auth, prefix) {
		// No Bearer prefix — treat as unauthenticated.
		http.Error(w, "unauthorized: missing Bearer token", http.StatusUnauthorized)
		return AuthResult{Authenticated: false}
	}
	rawToken := strings.TrimPrefix(auth, prefix)

	// 1. Check per-user list (RBAC — bearer-token SET lookup with bcrypt).
	// SEC-1 / UAT #399: each user may hold several concurrent tokens; the
	// presented token's embedded ID indexes directly to the right hash, with a
	// scan fallback for legacy tokens (see UserConfig.VerifyToken).
	if len(cfg.Gateway.Users) > 0 {
		for i := range cfg.Gateway.Users {
			user := cfg.Gateway.Users[i]
			if user.VerifyToken(rawToken) == nil {
				// Token matches this user.
				return AuthResult{Authenticated: true, Role: user.Role, User: &user}
			}
		}
		// Token not found in user list — reject.
		http.Error(w, "unauthorized: invalid Bearer token", http.StatusUnauthorized)
		return AuthResult{Authenticated: false}
	}

	// 2. Fallback: legacy OMNIPUS_BEARER_TOKEN env var (treated as admin role).
	required := os.Getenv("OMNIPUS_BEARER_TOKEN")
	if required == "" {
		if cfg.Gateway.DevModeBypass {
			// Auth not configured — development mode. Warn once at startup.
			warnUnauthOnce.Do(func() {
				slog.Warn("DEV MODE: API has no authentication. Set gateway.dev_mode_bypass=false for production.")
			})
			// Allow all requests in dev mode, treated as admin. Provide a
			// synthetic User so handlers that read *UserConfig from context
			// (e.g. /auth/validate) see an admin identity instead of nil.
			return AuthResult{Authenticated: true, Role: config.UserRoleAdmin, User: &devBypassUser}
		}
		// No auth configured — deny by default (fail closed).
		http.Error(w, "unauthorized: no users configured, complete onboarding first", http.StatusUnauthorized)
		return AuthResult{Authenticated: false}
	}
	if subtle.ConstantTimeCompare([]byte(rawToken), []byte(required)) != 1 {
		http.Error(w, "unauthorized: invalid Bearer token", http.StatusUnauthorized)
		return AuthResult{Authenticated: false}
	}
	// Synthetic User for the env-token path: handlers reading *UserConfig
	// from context need a non-nil identity even when no per-user entry exists.
	return AuthResult{Authenticated: true, Role: config.UserRoleAdmin, User: &envTokenUser}
}

// devBypassUser is the synthetic admin identity returned when the request
// passes via gateway.dev_mode_bypass. Handlers that read *UserConfig from
// context (HandleValidateToken etc.) see "_dev_bypass" rather than nil.
var devBypassUser = config.UserConfig{Username: "_dev_bypass", Role: config.UserRoleAdmin}

// envTokenUser is the synthetic admin identity returned when the request
// passes via the legacy OMNIPUS_BEARER_TOKEN environment fallback.
var envTokenUser = config.UserConfig{Username: "_env_token", Role: config.UserRoleAdmin}

// MapUserRoleToPrincipal converts a config.UserRole (human user) to the equivalent
// RBAC principal role string for agent operations. Fails closed: unknown roles
// get minimal "user" permissions.
func MapUserRoleToPrincipal(ur config.UserRole) string {
	switch ur {
	case config.UserRoleAdmin:
		return "admin"
	case config.UserRoleUser:
		return "user"
	default:
		return "user"
	}
}

// configSnapshotMiddleware snapshots the current config into the request context
// so all handlers in the same request see a consistent config. This prevents a
// race condition during hot-reload where the config pointer can be replaced
// mid-iteration, causing auth failures under concurrent load.
func (a *restAPI) configSnapshotMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := a.agentLoop.GetConfig()
		ctx := context.WithValue(r.Context(), ctxkey.ConfigContextKey{}, cfg)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
