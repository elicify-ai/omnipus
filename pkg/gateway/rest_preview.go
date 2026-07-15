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
// Auth model (FR-023): TOKEN-ONLY. The path token IS the credential. This
// route is registered on the PREVIEW listener without RequireSessionCookieOrBearer.
//
// Static mode: path-traversal guard → MIME → buffered/streaming response.
// Dev mode: reverse proxy to loopback dev-server port with CSP injection.

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
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/validation"
)

// HandlePreview serves GET /preview/{agent_id}/{token}/{file_path...} on the
// MAIN gateway listener (ADR-044 — there is no separate preview listener
// anymore). It routes to the dev proxy or static file handler by looking up
// the token in the appropriate registry.
//
// FR-006: gateway.preview_enabled is read LIVE on every request (via the
// request-scoped config snapshot injected by configSnapshotMiddleware, same
// as every other handler on the main mux) — disabling it 404s every /preview/
// request immediately, no restart required, and does NOT tear down any
// already-running dev server (those idle-TTL out via DevServerRegistry).
func (a *restAPI) HandlePreview(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()

	cfg := configFromContext(r.Context())
	if cfg == nil {
		cfg = a.agentLoop.GetConfig()
	}
	if cfg == nil || !cfg.IsPreviewEnabled() {
		// Distinguish this 404 from an unknown-token 404 (FR-021) — the
		// event name lets operators tell "preview turned off" apart from
		// "someone is probing for stale tokens" in the audit trail.
		a.auditServeFailure(r, "preview.disabled", "deny", "", "", http.StatusNotFound, startedAt)
		jsonErr(w, http.StatusNotFound, "preview disabled")
		return
	}

	if r.Method == http.MethodOptions {
		a.handleServePreviewPreflight(w, r)
		return
	}
	// B1.3b: emit CORS headers on actual GET/HEAD responses so the SPA's
	// cors-mode HEAD probe can read the status code.
	a.addPreviewCORSHeaders(w, r)

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

// handleServePreviewPreflight handles CORS OPTIONS for /preview/ (FR-007a).
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
// Used by HandlePreview's static-mode branch.
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

// reservedGatewayCookieNames are the gateway's own auth/CSRF cookie names.
// FR-013 (ADR-044, anti-fixation): a previewed dev server's response MUST
// NOT be able to plant or overwrite these on the shared browser origin — a
// malicious agent-generated app could otherwise ride Set-Cookie to fixate
// the operator's session or CSRF token. See middleware/session_cookie.go
// (SessionCookieName) and middleware/csrf.go (the csrf / __Host-csrf names).
var reservedGatewayCookieNames = map[string]struct{}{
	"omnipus-session": {},
	"csrf":            {},
	"__Host-csrf":     {},
}

// proxyDevRequest forwards the request to the dev-server's loopback port.
// Strips the /preview/<agent>/<token> (or /dev/<agent>/<token>) prefix so the
// embedded app sees its own root paths.
//
// FR-007d: ModifyResponse strips upstream CSP/XFO so the gateway-injected
// policy is authoritative.
//
// FR-013 (ADR-044): the Director strips EVERY request Cookie header (not just
// Authorization) before forwarding — the dev server has no legitimate need for
// the operator's origin cookies, and forwarding them would let a compromised
// dev dependency read the session/CSRF cookie values. ModifyResponse then
// neutralizes any upstream Set-Cookie for a reserved gateway cookie name
// (anti-fixation) while leaving the previewed app's own cookies untouched.
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
		// FR-013: strip the ENTIRE Cookie header (not just Authorization) —
		// the previewed dev server never legitimately needs the operator's
		// origin cookies (omnipus-session, csrf, or anything else scoped to
		// the main gateway origin). This closes the read-vector half of the
		// session-riding threat via the proxy path (the accepted residual for
		// a directly-opened preview tab is documented in the spec's
		// Non-Behaviors section).
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Forwarded-Proto", schemeFromRequest(r))
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")
		resp.Header.Del("X-Frame-Options")
		neutralizeReservedSetCookies(resp)
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

// neutralizeReservedSetCookies drops any upstream Set-Cookie header whose
// cookie name matches one of the gateway's reserved auth/CSRF cookie names
// (FR-013, anti-fixation) before the response reaches the browser. All other
// Set-Cookie values — the previewed app's own cookies — pass through
// unmodified, including their original attribute string (via Cookie.Raw), so
// this never rewrites/breaks a legitimate previewed-app cookie.
//
// Uses resp.Cookies() (net/http's own Set-Cookie parser) rather than
// hand-rolled string splitting so cookie names are extracted per RFC 6265
// exactly the way the standard library — and the browser — would.
func neutralizeReservedSetCookies(resp *http.Response) {
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return
	}
	hasReserved := false
	for _, c := range cookies {
		if _, reserved := reservedGatewayCookieNames[c.Name]; reserved {
			hasReserved = true
			break
		}
	}
	if !hasReserved {
		// Common case: no reserved cookie present. Leave the header(s)
		// exactly as the upstream sent them — no re-serialization risk.
		return
	}
	resp.Header.Del("Set-Cookie")
	for _, c := range cookies {
		if _, reserved := reservedGatewayCookieNames[c.Name]; reserved {
			slog.Warn("preview: dropped upstream Set-Cookie for a reserved gateway cookie name (anti-fixation)",
				"cookie_name", c.Name)
			continue
		}
		if c.Raw != "" {
			resp.Header.Add("Set-Cookie", c.Raw)
		} else {
			resp.Header.Add("Set-Cookie", c.String())
		}
	}
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
