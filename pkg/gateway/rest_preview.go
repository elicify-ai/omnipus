//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — unified /preview/{agent}/{token}/... handler.
//
// HandlePreview is the single HTTP entry point for the web_serve tool's URL
// surface. Both static-file registrations (ServedSubdirs) and dev-server
// registrations (DevServerRegistry) are reachable under the same /preview/
// prefix.  The handler looks up the token in DevServerRegistry first; on a
// miss it falls back to ServedSubdirs. Unknown or expired tokens → 404.
//
// Also provides back-compat HandleServeWorkspace (/serve/) and HandleDevProxy
// (/dev/) handlers for old registrations still in client transcripts.
//
// Auth model (FR-023): TOKEN-ONLY. The path token IS the credential. This
// route is registered on the PREVIEW listener without RequireSessionCookieOrBearer.
//
// Static mode: path-traversal guard → MIME → buffered/streaming response.
// Dev mode: reverse proxy to loopback dev-server port with CSP injection.
//
// Audit: same emitPreviewAuditEntry infrastructure as the former /serve/ and
// /dev/ handlers.

package gateway

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/validation"
)

// devProxyPathPrefix is the URL prefix served by HandleDevProxy.
const devProxyPathPrefix = "/dev/"

// HandlePreview serves GET /preview/{agent_id}/{token}/{file_path...} on the
// PREVIEW listener.  It routes to the dev proxy or static file handler by
// looking up the token in the appropriate registry.
func (a *restAPI) HandlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		a.handleServePreviewPreflight(w, r)
		return
	}
	// B1.3b: emit CORS headers on actual GET/HEAD responses so the SPA's
	// cors-mode HEAD probe can read the status code.
	a.addPreviewCORSHeaders(w, r)
	startedAt := time.Now()

	remainder := strings.TrimPrefix(r.URL.Path, "/preview/")
	if strings.HasPrefix(remainder, "/") {
		a.auditServeFailure(r, "preview.malformed_url", "error", "", "", http.StatusBadRequest, startedAt)
		jsonErr(w, http.StatusBadRequest, "malformed preview URL")
		return
	}
	parts := strings.SplitN(remainder, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		a.auditServeFailure(r, "preview.malformed_url", "error", "", "", http.StatusBadRequest, startedAt)
		jsonErr(w, http.StatusBadRequest, "malformed URL: expected /preview/{agent}/{token}/...")
		return
	}
	agentID := parts[0]
	token := parts[1]
	var remaining string
	if len(parts) == 3 {
		remaining = parts[2]
	}

	if err := validation.EntityID(agentID); err != nil {
		a.auditServeFailure(r, "preview.malformed_url", "error", agentID, token, http.StatusBadRequest, startedAt)
		jsonErr(w, http.StatusBadRequest, "invalid agent ID")
		return
	}

	// Dev-server registry (Tier 3) — try first.
	if a.devServers != nil {
		reg := a.devServers.Lookup(token)
		if reg != nil {
			if reg.AgentID != agentID {
				a.auditServeFailure(
					r,
					"preview.token_agent_mismatch",
					"deny",
					agentID,
					token,
					http.StatusForbidden,
					startedAt,
				)
				writeDevProxyError(w, http.StatusForbidden, "token does not match agent")
				return
			}
			a.proxyDevRequest(w, r, reg, remaining, agentID, token, startedAt)
			return
		}
	}

	// Static-file registry (Tier 1) — fallback.
	if a.servedSubdirs != nil {
		entry := a.servedSubdirs.Lookup(token)
		if entry != nil {
			if entry.AgentID != agentID {
				a.auditServeFailure(
					r,
					"preview.token_agent_mismatch",
					"deny",
					agentID,
					token,
					http.StatusForbidden,
					startedAt,
				)
				jsonErr(w, http.StatusForbidden, "token does not belong to this agent")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			a.serveStaticFile(w, r, entry.AbsDir, remaining, agentID, token, startedAt)
			return
		}
	}

	a.auditServeFailure(r, "preview.token_invalid", "deny", agentID, token, http.StatusNotFound, startedAt)
	jsonErr(w, http.StatusNotFound, "preview registration not found or expired")
}

// HandleServeWorkspace serves GET /serve/{agent_id}/{token}/{file_path...}.
// Kept for back-compat with registrations produced before the /preview/ route
// landed.
// TODO(v0.2 cleanup, target 2026-08-01): delete /serve/ and /dev/ shims
func (a *restAPI) HandleServeWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		a.handleServePreviewPreflight(w, r)
		return
	}
	// B1.3b: emit CORS headers on actual GET/HEAD responses.
	a.addPreviewCORSHeaders(w, r)
	startedAt := time.Now()

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	remainder := strings.TrimPrefix(r.URL.Path, "/serve/")
	if strings.HasPrefix(remainder, "/") {
		a.auditServeFailure(r, "serve.malformed_url", "error", "", "", http.StatusBadRequest, startedAt)
		jsonErr(w, http.StatusBadRequest, "malformed serve URL")
		return
	}
	parts := strings.SplitN(remainder, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		a.auditServeFailure(r, "serve.malformed_url", "error", "", "", http.StatusBadRequest, startedAt)
		jsonErr(w, http.StatusBadRequest, "malformed URL: expected /serve/{agent}/{token}/...")
		return
	}
	agentID := parts[0]
	token := parts[1]
	var relPath string
	if len(parts) == 3 {
		relPath = parts[2]
	}

	if err := validation.EntityID(agentID); err != nil {
		a.auditServeFailure(r, "serve.malformed_url", "error", agentID, token, http.StatusBadRequest, startedAt)
		jsonErr(w, http.StatusBadRequest, "invalid agent ID")
		return
	}
	if a.servedSubdirs == nil {
		jsonErr(w, http.StatusInternalServerError, "serve registry not initialized")
		return
	}
	entry := a.servedSubdirs.Lookup(token)
	if entry == nil {
		a.auditServeFailure(r, "serve.token_invalid", "deny", agentID, token, http.StatusUnauthorized, startedAt)
		jsonErr(w, http.StatusUnauthorized, "token unknown or expired")
		return
	}
	if entry.AgentID != agentID {
		a.auditServeFailure(r, "serve.token_agent_mismatch", "deny", agentID, token, http.StatusForbidden, startedAt)
		jsonErr(w, http.StatusForbidden, "token does not belong to this agent")
		return
	}

	a.serveStaticFile(w, r, entry.AbsDir, relPath, agentID, token, startedAt)
}

// HandleDevProxy serves /dev/{agent_id}/{token}/... requests.
// Kept for back-compat with registrations produced before the /preview/ route.
// TODO(v0.2 cleanup, target 2026-08-01): delete /serve/ and /dev/ shims
func (a *restAPI) HandleDevProxy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		a.handleServePreviewPreflight(w, r)
		return
	}
	// B1.3b: emit CORS headers on actual GET/HEAD responses.
	a.addPreviewCORSHeaders(w, r)
	startedAt := time.Now()

	if runtime.GOOS != "linux" {
		writeDevProxyError(w, http.StatusServiceUnavailable, tools.Tier3UnsupportedMessage)
		return
	}
	if a.devServers == nil {
		writeDevProxyError(w, http.StatusServiceUnavailable, "dev-server registry not configured")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, devProxyPathPrefix)
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		a.auditDevFailure(r, "dev.malformed_url", "error", "", "", http.StatusBadRequest, startedAt)
		writeDevProxyError(w, http.StatusBadRequest, "invalid /dev path; expected /dev/<agent>/<token>/...")
		return
	}
	agentID := parts[0]
	token := parts[1]
	remaining := ""
	if len(parts) == 3 {
		remaining = parts[2]
	}

	if err := validation.EntityID(agentID); err != nil {
		a.auditDevFailure(r, "dev.path_invalid", "error", agentID, "", http.StatusBadRequest, startedAt)
		writeDevProxyError(w, http.StatusBadRequest, "invalid agent identifier")
		return
	}
	if remaining != "" {
		cleaned := path.Clean("/" + remaining)
		if strings.HasPrefix(cleaned, "/..") {
			a.auditDevFailure(r, "dev.path_traversal", "deny", agentID, token, http.StatusBadRequest, startedAt)
			writeDevProxyError(w, http.StatusBadRequest, "path traversal not allowed")
			return
		}
		remaining = strings.TrimPrefix(cleaned, "/")
	}

	reg := a.devServers.Lookup(token)
	if reg == nil {
		a.auditDevFailure(r, "dev.token_invalid", "deny", agentID, token, http.StatusServiceUnavailable, startedAt)
		writeDevProxyError(w, http.StatusServiceUnavailable, "dev-server registration not found or expired")
		return
	}
	if reg.AgentID != agentID {
		a.auditDevFailure(r, "dev.token_agent_mismatch", "deny", agentID, token, http.StatusForbidden, startedAt)
		writeDevProxyError(w, http.StatusForbidden, "token does not match agent")
		return
	}

	a.proxyDevRequest(w, r, reg, remaining, agentID, token, startedAt)
}

// handleServePreviewPreflight handles CORS OPTIONS for /preview/, /serve/ and
// /dev/ (FR-007a).
func (a *restAPI) handleServePreviewPreflight(w http.ResponseWriter, r *http.Request) {
	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}
	mainOrigin := resolveMainOrigin(cfg)
	requestOrigin := r.Header.Get("Origin")
	w.Header().Set("Vary", "Origin")
	if mainOrigin != "" && requestOrigin != "" && strings.EqualFold(
		strings.TrimRight(requestOrigin, "/"),
		strings.TrimRight(mainOrigin, "/"),
	) {
		w.Header().Set("Access-Control-Allow-Origin", mainOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Max-Age", "86400")
	}
	w.WriteHeader(http.StatusNoContent)
}

// addPreviewCORSHeaders adds Access-Control-Allow-Origin to GET/HEAD
// responses on the preview listener so the SPA's cors-mode HEAD probe
// (B1.3b) can read the HTTP status code and detect 5xx errors.
// Only emitted when the request carries an Origin header that matches the
// configured main origin (same same-origin check as the preflight handler).
func (a *restAPI) addPreviewCORSHeaders(w http.ResponseWriter, r *http.Request) {
	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}
	mainOrigin := resolveMainOrigin(cfg)
	requestOrigin := r.Header.Get("Origin")
	if mainOrigin != "" && requestOrigin != "" && strings.EqualFold(
		strings.TrimRight(requestOrigin, "/"),
		strings.TrimRight(mainOrigin, "/"),
	) {
		w.Header().Set("Access-Control-Allow-Origin", mainOrigin)
		w.Header().Set("Vary", "Origin")
	}
}

// serveStaticFile serves the file at absDir/relPath with path-traversal guards,
// symlink resolution, MIME detection, and buffered/streaming delivery.
// Shared between HandlePreview (static mode) and HandleServeWorkspace.
func (a *restAPI) serveStaticFile(
	w http.ResponseWriter,
	r *http.Request,
	absDir string,
	relPath string,
	agentID, token string,
	startedAt time.Time,
) {
	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}
	mainOrigin := resolveMainOrigin(cfg)

	var absPath string
	if relPath == "" || relPath == "." {
		absPath = absDir
	} else {
		candidate := filepath.Join(absDir, filepath.FromSlash(relPath))
		dirWithSep := absDir
		if !strings.HasSuffix(dirWithSep, string(filepath.Separator)) {
			dirWithSep += string(filepath.Separator)
		}
		cleaned := filepath.Clean(candidate)
		if cleaned != absDir && !strings.HasPrefix(cleaned, dirWithSep) {
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, token, http.StatusForbidden, startedAt)
			jsonErr(w, http.StatusForbidden, "access denied: path is outside the registered directory")
			return
		}
		if resolved, evalErr := filepath.EvalSymlinks(cleaned); evalErr == nil {
			// Resolve the registered base too — on macOS, /var/folders/... resolves
			// to /private/var/folders/..., and on Linux/Termux /tmp can be a symlink
			// to a real path under /private or /data. Without resolving the base,
			// a symlink-equivalent ancestor (not an attacker's symlink-escape)
			// causes the safety check to mis-fire and reject legitimate requests.
			resolvedBase := absDir
			resolvedBaseWithSep := dirWithSep
			if rb, baseErr := filepath.EvalSymlinks(absDir); baseErr == nil {
				resolvedBase = rb
				resolvedBaseWithSep = rb
				if !strings.HasSuffix(resolvedBaseWithSep, string(filepath.Separator)) {
					resolvedBaseWithSep += string(filepath.Separator)
				}
			}
			if resolved != absDir && resolved != resolvedBase &&
				!strings.HasPrefix(resolved, dirWithSep) &&
				!strings.HasPrefix(resolved, resolvedBaseWithSep) {
				a.auditServeFailure(r, "serve.path_invalid", "error", agentID, token, http.StatusForbidden, startedAt)
				jsonErr(w, http.StatusForbidden, "access denied: path is outside the registered directory")
				return
			}
			cleaned = resolved
		} else if !os.IsNotExist(evalErr) {
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, token, http.StatusForbidden, startedAt)
			jsonErr(w, http.StatusForbidden, "access denied: path could not be resolved")
			return
		}
		absPath = cleaned
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, token, http.StatusNotFound, startedAt)
			jsonErr(w, http.StatusNotFound, "file not found")
			return
		}
		slog.Error("rest: serveStaticFile: stat failed", "path", absPath, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not stat path")
		return
	}
	if info.IsDir() {
		indexPath := filepath.Join(absPath, "index.html")
		indexInfo, indexErr := os.Stat(indexPath)
		if indexErr != nil || indexInfo.IsDir() {
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, token, http.StatusNotFound, startedAt)
			jsonErr(w, http.StatusNotFound, "no index.html in directory")
			return
		}
		absPath = indexPath
		info = indexInfo
	}

	emitFirstServed := a.markFirstServed(token)

	if info.Size() <= workspaceStreamingThreshold {
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			slog.Error("rest: serveStaticFile: ReadFile failed", "path", absPath, "error", readErr)
			jsonErr(w, http.StatusInternalServerError, "could not read file")
			return
		}
		setWorkspaceSecurityHeaders(w, mainOrigin)
		w.Header().Set("Content-Type", contentTypeForPath(absPath))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			if _, writeErr := w.Write(data); writeErr != nil {
				slog.Debug("rest: serveStaticFile: write failed", "error", writeErr)
			}
		}
		if emitFirstServed {
			a.auditServeSuccess(r, "serve.served", agentID, token, http.StatusOK, startedAt, int64(len(data)))
		}
		return
	}

	f, openErr := os.Open(absPath)
	if openErr != nil {
		slog.Error("rest: serveStaticFile: Open failed", "path", absPath, "error", openErr)
		jsonErr(w, http.StatusInternalServerError, "could not open file")
		return
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			slog.Debug("rest: serveStaticFile: file close error", "error", closeErr)
		}
	}()
	setWorkspaceSecurityHeaders(w, mainOrigin)
	w.Header().Set("Content-Type", contentTypeForPath(absPath))
	w.WriteHeader(http.StatusOK)
	var bytesOut int64
	if r.Method != http.MethodHead {
		var copyErr error
		bytesOut, copyErr = io.Copy(w, f)
		if copyErr != nil {
			slog.Debug("rest: serveStaticFile: io.Copy failed", "error", copyErr)
		}
	}
	if emitFirstServed {
		a.auditServeSuccess(r, "serve.served", agentID, token, http.StatusOK, startedAt, bytesOut)
	}
}

// proxyDevRequest forwards the request to the dev-server's loopback port.
// Strips the /preview/<agent>/<token> (or /dev/<agent>/<token>) prefix so the
// embedded app sees its own root paths.
//
// FR-007d: ModifyResponse strips upstream CSP/XFO so the gateway-injected
// policy is authoritative.
func (a *restAPI) proxyDevRequest(
	w http.ResponseWriter,
	r *http.Request,
	reg *sandbox.DevServerRegistration,
	remaining string,
	agentID, token string,
	startedAt time.Time,
) {
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", reg.Port),
	}
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.ErrorLog = slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn)

	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}
	mainOrigin := resolveMainOrigin(cfg)
	emitFirstServed := a.markFirstServed(token)

	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)
		req.URL.Path = "/" + remaining
		req.URL.RawPath = ""
		req.Header.Del("Authorization")
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Forwarded-Proto", schemeFromRequest(r))
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")
		resp.Header.Del("X-Frame-Options")
		setWorkspaceSecurityHeaders(responseHeaderWriter{resp.Header}, mainOrigin)
		if emitFirstServed {
			a.auditDevSuccess(r, "dev.proxied", agentID, token, resp.StatusCode, startedAt, -1)
		}
		return nil
	}

	rp.ErrorHandler = func(rw http.ResponseWriter, errReq *http.Request, err error) {
		remoteIP := r.RemoteAddr
		if host, _, splitErr := net.SplitHostPort(r.RemoteAddr); splitErr == nil {
			remoteIP = host
		}
		admit, suppressedCount := markFirstUpstreamFailure(token, remoteIP)
		if admit {
			if suppressedCount > 0 {
				prefix := token
				if len(prefix) > 8 {
					prefix = prefix[:8]
				}
				if !tokenPrefixRE.MatchString(prefix) {
					prefix = "<invalid>"
				}
				slog.Warn("dev.upstream_unreachable: window reset; previous failures were suppressed",
					"token_prefix", prefix,
					"remote_ip", remoteIP,
					"suppressed_count", suppressedCount,
				)
			}
			a.auditDevFailure(r, "dev.upstream_unreachable", "error", agentID, token, http.StatusBadGateway, startedAt)
		}
		// Do NOT echo the upstream-error message back to the (anonymous,
		// auth-less) preview client: it can leak the loopback port number,
		// dial/TLS error details, and other implementation specifics that an
		// agent's child process should not see. Operators retain the full
		// detail via the slog.Warn line below and the audit-emitted event.
		// Mirrors the pattern in pkg/sandbox/egress_proxy.go::handleHTTP.
		slog.Warn("preview: dev upstream unreachable",
			"agent_id", agentID, "port", reg.Port, "error", err)
		writeDevProxyError(rw, http.StatusBadGateway, "dev server unreachable")
	}

	rp.ServeHTTP(w, r)
}

// schemeFromRequest derives "https" or "http" for the X-Forwarded-Proto header.
func schemeFromRequest(r *http.Request) string {
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		return proto
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

// writeDevProxyError writes a JSON error response.
func writeDevProxyError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q}`, msg)
}

// auditServeSuccess emits a serve.served (or dev.proxied) event at Info level.
func (a *restAPI) auditServeSuccess(
	r *http.Request,
	event string,
	agentID, token string,
	status int,
	startedAt time.Time,
	bytesOut int64,
) {
	a.emitPreviewAuditEntry(r, event, "allow", agentID, token, status, startedAt, bytesOut, slog.LevelInfo)
}

// auditServeFailure emits a serve.* failure event at Warn level.
func (a *restAPI) auditServeFailure(
	r *http.Request,
	event string,
	decision string,
	agentID, token string,
	status int,
	startedAt time.Time,
) {
	a.emitPreviewAuditEntry(r, event, decision, agentID, token, status, startedAt, 0, slog.LevelWarn)
}

// auditDevSuccess emits a dev.proxied event at Info level.
func (a *restAPI) auditDevSuccess(
	r *http.Request,
	event string,
	agentID, token string,
	status int,
	startedAt time.Time,
	bytesOut int64,
) {
	a.emitPreviewAuditEntry(r, event, "allow", agentID, token, status, startedAt, bytesOut, slog.LevelInfo)
}

// auditDevFailure emits a dev.* failure event at Warn level.
func (a *restAPI) auditDevFailure(
	r *http.Request,
	event string,
	decision string,
	agentID, token string,
	status int,
	startedAt time.Time,
) {
	a.emitPreviewAuditEntry(r, event, decision, agentID, token, status, startedAt, 0, slog.LevelWarn)
}

// responseHeaderWriter adapts http.Header to the http.ResponseWriter interface
// for setWorkspaceSecurityHeaders, which only needs Header().
type responseHeaderWriter struct {
	h http.Header
}

func (rhw responseHeaderWriter) Header() http.Header       { return rhw.h }
func (rhw responseHeaderWriter) Write([]byte) (int, error) { return 0, nil }
func (rhw responseHeaderWriter) WriteHeader(int)           {}
