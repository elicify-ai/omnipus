//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — workspace-read endpoint (..,
// US-3).
//
// GET /api/v1/workspace/{agent_id}/{path...}
//
// Auth: RequireSessionCookieOrBearer + ownership check (AuthorizeAgentAccess).
// On ErrAgentOrphan the handler returns 503 per FR-093a.
//
// Path guard: validatePathWithAllowPaths via tools.ValidateWorkspacePath.
// Out-of-workspace → 403. Not-found → 404. Method other than GET → 405.
//
// Content-Type is derived from the file extension via an allow-list (FR-020a).
// Unknown extensions are served as application/octet-stream.
//
// Security headers :
// - Referrer-Policy: no-referrer
// - Content-Security-Policy: (locked-down policy, no framing)
// - X-Content-Type-Options: nosniff
//
// Streaming threshold :
// - ≤ 1,048,576 bytes → buffered read (os.ReadFile equivalent)
// - > 1,048,576 bytes → streamed (os.Open + io.Copy)

package gateway

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// workspaceStreamingThreshold is the file size boundary for buffered vs
// streamed delivery. Files above this size are streamed
// via io.Copy; files at or below this size are read into memory first.
const workspaceStreamingThreshold = 1 << 20 // 1,048,576 bytes

// buildWorkspaceCSP returns the Content-Security-Policy applied to all
// workspace-read and serve responses (FR-007 / FR-007c / FR-007e).
//
// Directives:
//   - default-src 'none' — deny everything by default
//   - script-src 'unsafe-inline' — permit inline scripts in HTML artifacts
//   - style-src 'unsafe-inline' — permit inline CSS
//   - img-src 'self' data: blob: — images from same origin + data URIs
//   - connect-src 'self' — hydrated SPA builds (Vite, Next.js exports)
//     can fetch their own /data.json; external network blocked. Changed
//     from 'none' in CR-01 / FR-007c.
//   - form-action 'self' — dev-iframe POSTs to its own origin; foreign-
//     origin POSTs blocked. Changed from 'none' in CR-01 to support
//     FR-023a (the dropped Origin middleware on /dev/).
//   - frame-ancestors '<mainOrigin>' — only the SPA's own origin may
//     embed served content. Falls back to '*' when mainOrigin is empty
//     (host=0.0.0.0/[::] without public_url set — see FR-007e). Defense
//     against T-04 (foreign embed of leaked-token URL).
//   - base-uri 'none' — forbid <base> tag hijacking
//   - object-src 'none' — no plugins
//
// mainOrigin is the SPA's browser-realistic origin (e.g.
// "http://1.2.3.4:5000"). Empty triggers the FR-007e fallback to '*'.
// The WARN about this fallback is emitted once at boot in gateway.Run
// (setupAndStartServices), not here — see F-8 fix.
func buildWorkspaceCSP(mainOrigin string) string {
	frameAncestors := "*"
	if mainOrigin != "" {
		frameAncestors = mainOrigin
	}
	return "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; " +
		"img-src 'self' data: blob:; connect-src 'self'; form-action 'self'; " +
		"frame-ancestors " + frameAncestors + "; base-uri 'none'; object-src 'none'"
}

// workspaceContentType maps lowercase file extensions to MIME types per
// FR-020a. Keys include the leading dot.
var workspaceContentType = map[string]string{
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".css":  "text/css",
	".js":   "application/javascript",
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".svg":  "image/svg+xml",
	".webp": "image/webp",
	".json": "application/json",
	".txt":  "text/plain; charset=utf-8",
	".md":   "text/plain; charset=utf-8",
	".pdf":  "application/pdf",
}

// contentTypeForPath resolves the Content-Type for the given file path
// based on its extension. Unknown extensions return application/octet-stream.
func contentTypeForPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ct, ok := workspaceContentType[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}

// setWorkspaceSecurityHeaders writes the security headers to w.
//
// mainOrigin is the SPA's browser-realistic origin used for the CSP
// `frame-ancestors` directive (FR-007 / FR-007c). Pass "" to opt into the
// FR-007e fallback (`frame-ancestors '*'`) — appropriate when the gateway
// is bound to 0.0.0.0/[::] and the operator has not configured
// gateway.public_url. The fallback emits a one-time WARN at boot.
func setWorkspaceSecurityHeaders(w http.ResponseWriter, mainOrigin string) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", buildWorkspaceCSP(mainOrigin))
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// resolveMainOrigin computes the SPA's browser-realistic origin for the
// CSP frame-ancestors directive on /serve/ and /dev/ responses
// (FR-007 / FR-007e). Resolution order:
//
//  1. cfg.Gateway.PublicURL — explicit reverse-proxy origin set by operator.
//  2. <scheme>://<host>:<port> — derived from cfg.Gateway.Host+Port.
//     Returns "" when host="0.0.0.0" or "[::]" (a non-browser-realistic
//     wildcard bind), triggering the FR-007e '*' fallback.
//
// Returns "" when no realistic origin can be derived. Callers MUST pass
// the result through to setWorkspaceSecurityHeaders / buildWorkspaceCSP
// without further validation — the helpers handle the empty-string
// fallback path.
func resolveMainOrigin(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if pu := strings.TrimSpace(cfg.Gateway.PublicURL); pu != "" {
		return strings.TrimRight(pu, "/")
	}
	host := strings.TrimSpace(cfg.Gateway.Host)
	if host == "" || host == "0.0.0.0" || host == "[::]" || host == "::" {
		// Wildcard bind has no browser-realistic origin — return empty so
		// buildWorkspaceCSP falls back to '*' with the WARN-once log.
		return ""
	}
	port := cfg.Gateway.Port
	scheme := "http"
	if port == 443 {
		scheme = "https"
	}
	if port > 0 && port != 80 && port != 443 {
		return fmt.Sprintf("%s://%s:%d", scheme, host, port)
	}
	return fmt.Sprintf("%s://%s", scheme, host)
}

// HandleWorkspace serves GET /api/v1/workspace/{agent_id}/{path...}.
//
// Route prefix: "/api/v1/workspace/". The handler strips the prefix and
// extracts the first path segment as agent_id; the remainder is the file
// path within the agent's workspace.
//
// This handler is registered via:
//
//	middleware.RequireSessionCookieOrBearer(getCfg)(http.HandlerFunc(a.HandleWorkspace))
//
// so the authenticated user is available via UserContextKey.
func (a *restAPI) HandleWorkspace(w http.ResponseWriter, r *http.Request) {
	// Method gate: workspace reads are GET only.
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Parse agent_id and file path from the URL.
	// URL shape: /api/v1/workspace/{agent_id}/{file_path...}
	remainder := strings.TrimPrefix(r.URL.Path, "/api/v1/workspace/")
	remainder = strings.TrimPrefix(remainder, "/")
	if remainder == "" {
		jsonErr(w, http.StatusBadRequest, "agent_id required")
		return
	}

	slashIdx := strings.Index(remainder, "/")
	var agentID, filePath string
	if slashIdx == -1 {
		agentID = remainder
		filePath = ""
	} else {
		agentID = remainder[:slashIdx]
		filePath = remainder[slashIdx+1:]
	}

	if err := validateEntityID(agentID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid agent ID")
		return
	}

	// Resolve authenticated user from context (set by RequireSessionCookieOrBearer).
	user, _ := r.Context().Value(UserContextKey{}).(*config.UserConfig)
	if user == nil {
		jsonErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Resolve current config.
	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}

	// Find the agent in config.
	var agentCfg *config.AgentConfig
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == agentID {
			ac := cfg.Agents.List[i]
			agentCfg = &ac
			break
		}
	}
	if agentCfg == nil {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf("agent %q not found", agentID))
		return
	}

	// Ownership check — returns ErrAgentOrphan if the agent has no owner.
	if err := config.AuthorizeAgentAccess(user, agentCfg); err != nil {
		if errors.Is(err, config.ErrAgentOrphan) {
			jsonErr(w, http.StatusServiceUnavailable, "agent has no owner; admin must reassign ownership")
			return
		}
		jsonErr(w, http.StatusForbidden, "forbidden")
		return
	}

	// Resolve workspace path for this agent.
	agentWorkspace, err := agentWorkspacePath(cfg, agentCfg.ID, agentCfg.Workspace, a.homePath)
	if err != nil {
		slog.Error("rest: HandleWorkspace: agentWorkspacePath failed",
			"agent_id", agentID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not resolve agent workspace")
		return
	}

	if filePath == "" {
		jsonErr(w, http.StatusBadRequest, "file path required")
		return
	}

	// Canonicalise and guard the requested path against the agent's workspace.
	// restrict=true, no allow-list patterns (workspace only).
	absPath, err := tools.ValidateWorkspacePath(filePath, agentWorkspace, true, nil)
	if err != nil {
		slog.Info("rest: HandleWorkspace: path rejected",
			"agent_id", agentID, "path", filePath, "error", err)
		if strings.Contains(err.Error(), "outside the workspace") ||
			strings.Contains(err.Error(), "symlink resolves outside") ||
			strings.Contains(err.Error(), "another agent's workspace") {
			jsonErr(w, http.StatusForbidden, fmt.Sprintf("access denied: %v", err))
		} else {
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid path: %v", err))
		}
		return
	}

	// Stat the file to determine streaming strategy.
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			jsonErr(w, http.StatusNotFound, "file not found")
			return
		}
		slog.Error("rest: HandleWorkspace: stat failed", "path", absPath, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not stat file")
		return
	}
	if info.IsDir() {
		jsonErr(w, http.StatusBadRequest, "path is a directory; specify a file path")
		return
	}

	if info.Size() <= workspaceStreamingThreshold {
		// Buffered path: read into memory before setting any headers so that
		// read errors can still return a proper HTTP error status.
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			slog.Error("rest: HandleWorkspace: ReadFile failed", "path", absPath, "error", readErr)
			jsonErr(w, http.StatusInternalServerError, "could not read file")
			return
		}
		// Headers set only after successful read — status code is still settable.
		setWorkspaceSecurityHeaders(w, resolveMainOrigin(cfg))
		w.Header().Set("Content-Type", contentTypeForPath(absPath))
		w.WriteHeader(http.StatusOK)
		if _, writeErr := w.Write(data); writeErr != nil {
			slog.Debug("rest: HandleWorkspace: write failed", "error", writeErr)
		}
		return
	}

	// Streaming path: open file BEFORE setting any response headers so that an
	// open failure can still return a proper HTTP error status (not a silent 200
	// with an empty body). Once WriteHeader is called the status code is frozen.
	f, openErr := os.Open(absPath)
	if openErr != nil {
		slog.Error("rest: HandleWorkspace: Open failed", "path", absPath, "error", openErr)
		jsonErr(w, http.StatusInternalServerError, "could not open file")
		return
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			slog.Debug("rest: HandleWorkspace: file close error", "error", closeErr)
		}
	}()
	// File is open — no more error paths before the response. Set headers now.
	setWorkspaceSecurityHeaders(w, resolveMainOrigin(cfg))
	w.Header().Set("Content-Type", contentTypeForPath(absPath))
	w.WriteHeader(http.StatusOK)
	if _, copyErr := io.Copy(w, f); copyErr != nil {
		slog.Debug("rest: HandleWorkspace: io.Copy failed", "error", copyErr)
	}
}
