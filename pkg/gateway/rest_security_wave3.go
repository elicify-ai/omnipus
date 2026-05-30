//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// rest_security_wave3.go — Wave 3 operator-facing REST endpoints.
//
// This file wires one endpoint:
//   - GET /api/v1/security/exec-proxy-status
//
// HandlePromptGuard was originally in this file but was superseded by the
// implementation in rest_prompt_guard.go, which adds hot-reload support
// and uses EmitSecuritySettingChange for audit logging.

// HandleExecProxyStatus handles GET /api/v1/security/exec-proxy-status.
//
// Returns the runtime state of the exec SSRF proxy (SEC-28). Operators need
// this to distinguish "proxy disabled by config" from "proxy failed to bind"
// from "proxy running normally", which directly affects the threat model the
// environment is actually enforcing.
//
// Response shape:
//
//	{
//	  "enabled": bool,   // cfg.Tools.Exec.EnableProxy
//	  "running": bool,   // listener currently bound
//	  "address": string, // "127.0.0.1:PORT" when running, omitted otherwise
//	}
func (a *restAPI) HandleExecProxyStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cfg := a.agentLoop.GetConfig()
	enabled := cfg.Tools.Exec.EnableProxy

	proxy := a.agentLoop.ExecProxy()
	if proxy == nil {
		// Proxy disabled OR failed to bind at startup. The enabled flag
		// distinguishes the two cases for the UI.
		jsonOK(w, gen.ExecProxyStatus{
			Enabled: enabled,
			Running: false,
		})
		return
	}

	addr := proxy.Addr()
	jsonOK(w, gen.ExecProxyStatus{
		Enabled: enabled,
		Running: true,
		Address: &addr,
	})
}
