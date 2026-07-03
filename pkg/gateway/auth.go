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

// UserContextKey is an alias for ctxkey.UserContextKey kept for compatibility
// with existing code in this package that uses the gateway-local type name.
type UserContextKey = ctxkey.UserContextKey

// AuthResult holds the outcome of a bearer token check.
type AuthResult struct {
	Authenticated bool
	User          *config.UserConfig
}

// checkBearerAuth validates the Authorization header.
// It checks the single human account (Gateway.Users[0], single-account model),
// then the CLI's dedicated Gateway.CLIToken, then falls back to the legacy
// OMNIPUS_BEARER_TOKEN env var for backward compatibility.
// Returns AuthResult so callers can distinguish authenticated from anonymous.
func checkBearerAuth(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg *config.Config) AuthResult {
	auth := r.Header.Get("Authorization")
	prefix := "Bearer "

	// dev_mode_bypass short-circuit: when auth isn't configured AND bypass is
	// enabled, allow the request (even with no Authorization header at all).
	// This makes the SPA reviewable on a fresh install without onboarding
	// first. The account and OMNIPUS_BEARER_TOKEN paths below still run for
	// the non-bypass case.
	if !strings.HasPrefix(auth, prefix) && cfg.Gateway.DevModeBypass {
		slog.Warn(
			"AUTH-BYPASS",
			"users_count",
			len(cfg.Gateway.Users),
			"bypass_flag",
			cfg.Gateway.DevModeBypass,
			"has_bearer",
			strings.HasPrefix(auth, prefix),
		)
		warnUnauthOnce.Do(func() {
			slog.Warn("DEV MODE: API has no authentication. Set gateway.dev_mode_bypass=false for production.")
		})
		return AuthResult{Authenticated: true, User: &devBypassUser}
	}

	if !strings.HasPrefix(auth, prefix) {
		// No Bearer prefix — treat as unauthenticated.
		http.Error(w, "unauthorized: missing Bearer token", http.StatusUnauthorized)
		return AuthResult{Authenticated: false}
	}
	rawToken := strings.TrimPrefix(auth, prefix)

	// 1. The single human account (Gateway.Users[0], if configured).
	// Single-user model: Gateway.Users holds at most one entry now, so this is
	// a direct check rather than a loop. SEC-1 / UAT #399: the presented
	// token's embedded ID indexes directly to the right hash inside
	// VerifyToken, with a scan fallback for legacy tokens.
	if len(cfg.Gateway.Users) > 0 {
		if cfg.Gateway.Users[0].VerifyToken(rawToken) == nil {
			return AuthResult{Authenticated: true, User: &cfg.Gateway.Users[0]}
		}
	}

	// 2. The CLI's dedicated token — decoupled from the human account (see
	// GatewayConfig.CLIToken doc). Verified via the same shared helper that
	// UserConfig.VerifyToken uses internally, with no legacy hash to check.
	if cfg.Gateway.CLIToken != nil {
		if config.VerifyTokenAgainst([]config.TokenEntry{*cfg.Gateway.CLIToken}, "", rawToken) == nil {
			return AuthResult{Authenticated: true, User: &config.UserConfig{Username: "cli"}}
		}
	}

	// Auth is configured (a human account and/or a CLI token exist) but the
	// presented token matched neither — reject without falling through to the
	// legacy env-token path below. This preserves prior behavior: once any
	// account-based auth is configured, an unmatched token is rejected
	// immediately rather than being checked against OMNIPUS_BEARER_TOKEN.
	if len(cfg.Gateway.Users) > 0 || cfg.Gateway.CLIToken != nil {
		http.Error(w, "unauthorized: invalid Bearer token", http.StatusUnauthorized)
		return AuthResult{Authenticated: false}
	}

	// 3. Fallback: legacy OMNIPUS_BEARER_TOKEN env var.
	required := os.Getenv("OMNIPUS_BEARER_TOKEN")
	if required == "" {
		if cfg.Gateway.DevModeBypass {
			// Auth not configured — development mode. Warn once at startup.
			warnUnauthOnce.Do(func() {
				slog.Warn("DEV MODE: API has no authentication. Set gateway.dev_mode_bypass=false for production.")
			})
			// Allow all requests in dev mode. Provide a synthetic User so
			// handlers that read *UserConfig from context (e.g.
			// /auth/validate) see an identity instead of nil.
			return AuthResult{Authenticated: true, User: &devBypassUser}
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
	return AuthResult{Authenticated: true, User: &envTokenUser}
}

// devBypassUser is the synthetic identity returned when the request passes
// via gateway.dev_mode_bypass. Handlers that read *UserConfig from context
// (HandleValidateToken etc.) see "_dev_bypass" rather than nil.
var devBypassUser = config.UserConfig{Username: "_dev_bypass"}

// envTokenUser is the synthetic identity returned when the request passes
// via the legacy OMNIPUS_BEARER_TOKEN environment fallback.
var envTokenUser = config.UserConfig{Username: "_env_token"}

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
