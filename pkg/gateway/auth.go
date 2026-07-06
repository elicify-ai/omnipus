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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
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

// CLITokenContextKey is an alias for ctxkey.CLITokenContextKey kept for
// compatibility with existing code in this package that uses the
// gateway-local type name.
type CLITokenContextKey = ctxkey.CLITokenContextKey

// AuthResult holds the outcome of a bearer token check.
type AuthResult struct {
	Authenticated bool
	User          *config.UserConfig
	// ViaCLIToken is true only when User was authenticated against
	// Gateway.CLIToken (the synthetic "cli" identity), never when it came
	// from a real Gateway.Users row. Callers that inject User into the
	// request context under UserContextKey must also inject this under
	// CLITokenContextKey so downstream handlers (e.g. HandleLogout) can
	// tell the two apart — see CLITokenContextKey's doc for why that
	// distinction matters.
	ViaCLIToken bool
}

// checkBearerAuth validates the Authorization header.
// It loops every account in Gateway.Users (the single-user model normally
// holds exactly one, but a pre-single-user-model install may still carry
// leftover extra accounts from before the Users CRUD API was deleted —
// config.warnAboutExtraUsers flags that at load time as an advisory; every
// configured account still authenticates here regardless), then the CLI's
// dedicated Gateway.CLIToken, then falls back to the legacy
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

	// 1. The human account(s) in Gateway.Users. Single-user model: normally
	// holds exactly one entry, but a pre-single-user-model install could
	// still carry a leftover second (or third...) account from before the
	// Users CRUD API was deleted (config.warnAboutExtraUsers logs a WARN
	// for this at load time — deliberately not self-healed/truncated here,
	// since that would race a legitimate account's own in-flight login).
	// Looping keeps every configured account able to authenticate rather
	// than silently and permanently locking out everyone but index 0.
	// SEC-1 / UAT #399: the presented token's embedded ID indexes directly
	// to the right hash inside VerifyToken, with a scan fallback for legacy
	// tokens.
	for i := range cfg.Gateway.Users {
		if cfg.Gateway.Users[i].VerifyToken(rawToken) == nil {
			return AuthResult{Authenticated: true, User: &cfg.Gateway.Users[i]}
		}
	}

	// 2. The CLI's dedicated token — decoupled from the human account (see
	// GatewayConfig.CLIToken doc). Verified via the nil-safe helper that
	// wraps the same shared VerifyTokenAgainst logic UserConfig.VerifyToken
	// uses internally, with no legacy hash to check. ViaCLIToken:true tells
	// callers this identity is NOT backed by a Gateway.Users row (see
	// CLITokenContextKey's doc).
	if cfg.Gateway.VerifyCLIToken(rawToken) == nil {
		return AuthResult{Authenticated: true, User: &config.UserConfig{Username: "cli"}, ViaCLIToken: true}
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
