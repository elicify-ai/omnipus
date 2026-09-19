// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// authedAdminUser is the username the settings-mutation tests authenticate as.
const authedAdminUser = "settings-admin"

// withAuthedAdmin attaches an authenticated *config.UserConfig under
// UserContextKey{}. Tests that drive a settings-mutation handler directly
// (bypassing the withAuth middleware) use this so the handler's own
// "not authenticated" 401 does not fire.
//
// Single-user model (operator directive, 2026-07): there is no admin role left
// to inject — these handlers only read the caller's identity, never a role.
//
// This replaces the old withReAuthAdmin helper, which additionally minted a
// single-use password consent token. ADR-0008 ruling 6 removed that gate: the
// six high-blast-radius controls are confirmed in the SPA, not re-authenticated
// on the wire, so there is no token left to mint.
func withAuthedAdmin(r *http.Request) *http.Request {
	user := &config.UserConfig{Username: authedAdminUser}
	return r.WithContext(context.WithValue(r.Context(), UserContextKey{}, user))
}
