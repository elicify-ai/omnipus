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
// route is registered BARE on the MAIN gateway listener (ADR-044 — there is no
// separate preview listener), without RequireSessionCookieOrBearer; it is
// CSRF- and Origin-check-exempt by the /preview/ path prefix.
//
// Static mode: path-traversal guard → MIME → buffered/streaming response.
// Dev mode: reverse proxy to loopback dev-server port with CSP injection.

package gateway

import (
	"crypto/subtle"
	"errors"
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

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
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

	// FR-011: a preview request carrying service-worker metadata is refused
	// before any serving — a registered service worker would outlive the
	// preview tab and keep serving from (or fetching through) the preview
	// origin after the registration is gone. This is the ONE code path for
	// both modes: dispatched Mode 1 label requests and Mode 2 /preview/ path
	// requests both enter here.
	if strings.EqualFold(r.Header.Get("Service-Worker"), "script") ||
		strings.EqualFold(r.Header.Get("Sec-Fetch-Dest"), "serviceworker") {
		a.auditServeFailure(r, "preview.serviceworker_refused", "deny", "", "", http.StatusForbidden, startedAt)
		writeDevProxyError(w, http.StatusForbidden, "service workers are not allowed for previews")
		return
	}

	// ADR-094 Mode 1: the request arrived dispatched under a preview label
	// Host — the preview-host mux (preview_host_dispatch.go) has already
	// classified, rate-limited and labelled it. Serve by label; the /preview/
	// path parsing below is the Mode 2 surface only.
	if label := previewHostLabelFromContext(r.Context()); label != "" {
		a.servePreviewByLabel(w, r, label, startedAt)
		return
	}

	if r.Method == http.MethodOptions {
		a.handleServePreviewPreflight(w, r)
		return
	}

	remainder := strings.TrimPrefix(r.URL.Path, middleware.PreviewPathPrefix)
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
			a.serveStaticFile(w, r, entry.AbsDir, remaining, agentID, token, token, startedAt)
			return
		}
	}

	a.auditServeFailure(r, "preview.token_invalid", "deny", agentID, token, http.StatusNotFound, startedAt)
	jsonErr(w, http.StatusNotFound, "preview registration not found or expired")
}

// handleServePreviewPreflight handles CORS OPTIONS for /preview/ (FR-007a +
// ADR-094 FR-012/DS-7 row 4): the preflight answers EXACTLY
// "GET, HEAD, OPTIONS" — no allow-headers wildcard, ever (a wildcard would
// permit cross-origin non-simple writes to the dev upstream) — for ANY
// origin; Access-Control-Allow-Origin itself is emitted only for the main
// origin (the SPA probe) so a foreign origin gets no read permission even
// though it learns the methods list. The foreign-origin 204 with no ACAO is
// the pre-existing FR-007a stealth-rejection posture, kept.
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
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

// serveStaticFile serves the file at absDir/relPath with path-traversal guards,
// symlink resolution, MIME detection, and buffered/streaming delivery.
// Used by HandlePreview's static-mode branch. dedupKey keys the serve.served
// audit dedup set; auditToken feeds the audit details — they are separate
// because the Mode 1 static branch dedups under a "label:" key but must keep
// the LABEL out of the audit details (FR-026 redaction), so it audits with an
// empty auditToken (token_prefix "<invalid>") while the Mode 2 path passes
// the registration token for both.
func (a *restAPI) serveStaticFile(
	w http.ResponseWriter,
	r *http.Request,
	absDir string,
	relPath string,
	agentID, dedupKey, auditToken string,
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
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, auditToken, http.StatusForbidden, startedAt)
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
				a.auditServeFailure(r, "serve.path_invalid", "error", agentID, auditToken, http.StatusForbidden, startedAt)
				jsonErr(w, http.StatusForbidden, "access denied: path is outside the registered directory")
				return
			}
			cleaned = resolved
		} else if !errors.Is(evalErr, os.ErrNotExist) {
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, auditToken, http.StatusForbidden, startedAt)
			jsonErr(w, http.StatusForbidden, "access denied: path could not be resolved")
			return
		}
		absPath = cleaned
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, auditToken, http.StatusNotFound, startedAt)
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
			a.auditServeFailure(r, "serve.path_invalid", "error", agentID, auditToken, http.StatusNotFound, startedAt)
			jsonErr(w, http.StatusNotFound, "no index.html in directory")
			return
		}
		absPath = indexPath
		info = indexInfo
	}

	emitFirstServed := a.markFirstServed(dedupKey)

	if info.Size() <= workspaceStreamingThreshold {
		data, readErr := os.ReadFile(absPath)
		if readErr != nil {
			slog.Error("rest: serveStaticFile: ReadFile failed", "path", absPath, "error", readErr)
			jsonErr(w, http.StatusInternalServerError, "could not read file")
			return
		}
		setPreviewStaticHeaders(w, r, mainOrigin)
		w.Header().Set("Content-Type", contentTypeForPath(absPath))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			if _, writeErr := w.Write(data); writeErr != nil {
				slog.Debug("rest: serveStaticFile: write failed", "error", writeErr)
			}
		}
		if emitFirstServed {
			a.auditServeSuccess(r, "serve.served", agentID, auditToken, http.StatusOK, startedAt, int64(len(data)))
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
	setPreviewStaticHeaders(w, r, mainOrigin)
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
		a.auditServeSuccess(r, "serve.served", agentID, auditToken, http.StatusOK, startedAt, bytesOut)
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

// filterReservedCookiePairs removes the gateway's own credential cookie PAIRS
// from a raw Cookie header value and reports whether anything was removed
// (ADR-094 FR-012b). Names are matched case-SENSITIVELY (cookie names are
// case-sensitive per RFC 6265; "CSRF=" is the app's own cookie), pairs are
// split on ";" and trimmed, and a pair with no "=" is dropped (it is not a
// valid cookie pair). An empty result deletes the header entirely.
func filterReservedCookiePairs(cookieHeader string) (filtered string, dropped bool) {
	if cookieHeader == "" {
		return "", false
	}
	pairs := strings.Split(cookieHeader, ";")
	kept := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		p := strings.TrimSpace(pair)
		if p == "" {
			dropped = true
			continue
		}
		name := p
		if i := strings.IndexByte(p, '='); i >= 0 {
			name = p[:i]
		} else {
			// A pair with no "=" is not a valid cookie pair — drop it.
			dropped = true
			continue
		}
		if _, reserved := reservedGatewayCookieNames[strings.TrimSpace(name)]; reserved {
			dropped = true
			continue
		}
		kept = append(kept, p)
	}
	if !dropped {
		// Common case: nothing reserved present. Leave the header byte-exact —
		// no re-serialization risk.
		return cookieHeader, false
	}
	if len(kept) == 0 {
		return "", true
	}
	return strings.Join(kept, "; "), true
}

// gatewayOwnsBearer reports whether the gateway's own validator would accept
// authz as a bearer credential (ADR-094 FR-012b): a user token or CLI token
// via resolveBearerIdentity, or the legacy OMNIPUS_BEARER_TOKEN env token via
// a constant-time compare (the same compare checkBearerAuth runs). A foreign
// bearer — the previewed app's own API token — returns false and reaches the
// app untouched.
func gatewayOwnsBearer(cfg *config.Config, authz string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		return false
	}
	raw := strings.TrimPrefix(authz, prefix)
	if _, _, matched := resolveBearerIdentity(cfg, raw); matched {
		return true
	}
	if required := os.Getenv("OMNIPUS_BEARER_TOKEN"); required != "" &&
		subtle.ConstantTimeCompare([]byte(raw), []byte(required)) == 1 {
		return true
	}
	return false
}

// proxyDevRequest forwards the request to the dev-server's loopback port.
// Strips the /preview/<agent>/<token> prefix so the embedded app sees its own
// root paths.
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

	// ADR-094 mode discrimination: a dispatched label request (Mode 1) gets
	// the minimal CSP, no redirect rule and the same CORS policy; the /preview/
	// path surface (Mode 2) gets the byte-stable CSP template and the DS-2
	// redirect rule. The prefix is percent-encoded per DS-7's reserved-char
	// rows; the client document URL is what the browser resolves a relative
	// Location against.
	mode1 := previewHostLabelFromContext(r.Context()) != ""
	prefix := middleware.PreviewPathPrefix + url.PathEscape(agentID) + "/" + url.PathEscape(token)
	clientOrigin := &url.URL{Scheme: schemeFromRequest(r), Host: r.Host}
	clientBase := &url.URL{
		Scheme: clientOrigin.Scheme,
		Host:   r.Host,
		Path:   prefix + "/" + remaining,
	}

	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)
		req.URL.Path = "/" + remaining
		req.URL.RawPath = ""
		// ADR-094 FR-012b: strip ONLY the gateway's own credential cookies —
		// the previewed dev server never legitimately needs them, while the
		// previewed app's OWN cookies (any other name, including case-variant
		// look-alikes like "CSRF") must survive the hop so a cookie-sessioned
		// app keeps working. Pair-level filtering, not a name filter: the
		// browser sends `name=value; name2=value2` on ONE header line, so
		// removing the reserved NAMES means removing their PAIRS.
		filteredCookie, dropped := filterReservedCookiePairs(req.Header.Get("Cookie"))
		if dropped {
			if filteredCookie == "" {
				req.Header.Del("Cookie")
			} else {
				req.Header.Set("Cookie", filteredCookie)
			}
		}
		// ADR-094 FR-012b: strip Authorization only when the gateway's own
		// validator would ACCEPT it (user token, CLI token, legacy env token)
		// — a foreign bearer (the previewed app's own API token) must reach
		// the app untouched.
		if gatewayOwnsBearer(cfg, req.Header.Get("Authorization")) {
			req.Header.Del("Authorization")
		}
		req.Header.Set("X-Forwarded-Host", r.Host)
		req.Header.Set("X-Forwarded-Proto", schemeFromRequest(r))
	}

	rp.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Content-Security-Policy-Report-Only")
		resp.Header.Del("X-Frame-Options")
		neutralizeReservedSetCookies(resp)
		applyPreviewResponseCORS(resp.Header)
		setWorkspaceSecurityHeaders(responseHeaderWriter{resp.Header}, mainOrigin)
		if mode1 {
			resp.Header.Set("Content-Security-Policy", previewMode1CSP)
		} else {
			resp.Header.Set("Content-Security-Policy",
				buildPreviewCSP(mainOrigin, wsOriginFor(mainOrigin), prefix))
		}
		if !mode1 {
			applyPreviewRedirectRule(resp, clientOrigin, clientBase, prefix)
		}
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

// cookieNameFromSetCookieLine leniently extracts the cookie NAME from a raw
// Set-Cookie header line, WITHOUT the strict RFC 6265 value validation that
// net/http's Response.Cookies() applies. It strips the attributes (everything
// from the first ';'), then takes the substring before the first '=', trimmed
// of surrounding whitespace. This parses more permissively than the stdlib on
// purpose: it must recognize a reserved cookie by name even when its VALUE
// contains a byte net/http would reject. Returns "" when the line has no '='.
func cookieNameFromSetCookieLine(line string) string {
	if i := strings.IndexByte(line, ';'); i >= 0 {
		line = line[:i]
	}
	name, _, found := strings.Cut(line, "=")
	if !found {
		return ""
	}
	return strings.TrimSpace(name)
}

// neutralizeReservedSetCookies drops any upstream Set-Cookie header whose
// cookie name matches one of the gateway's reserved auth/CSRF cookie names
// (FR-013, anti-fixation) before the response reaches the browser. Every other
// Set-Cookie line — the previewed app's own cookies — passes through
// byte-for-byte unmodified, so this never rewrites/breaks a legitimate
// previewed-app cookie.
//
// It scans the RAW resp.Header["Set-Cookie"] lines by name rather than using
// resp.Cookies(), because net/http's Response.Cookies() silently DROPS any
// Set-Cookie line whose value fails its strict RFC 6265 grammar (any byte
// outside 0x20-0x7E minus '"', ';', '\'). A malicious previewed app could
// therefore emit `Set-Cookie: omnipus-session=<one malformed byte>` that
// Cookies() discards — making the reserved name invisible to a parse-based
// filter — while real browsers, materially more lenient, would still store it,
// clobbering the operator's real session cookie. Matching on the leniently
// extracted name closes that fail-open gap (silent-failure-hunter CRITICAL).
func neutralizeReservedSetCookies(resp *http.Response) {
	raw := resp.Header["Set-Cookie"]
	if len(raw) == 0 {
		return
	}
	kept := make([]string, 0, len(raw))
	dropped := false
	for _, line := range raw {
		name := cookieNameFromSetCookieLine(line)
		if _, reserved := reservedGatewayCookieNames[name]; reserved {
			slog.Warn("preview: dropped upstream Set-Cookie for a reserved gateway cookie name (anti-fixation)",
				"cookie_name", name)
			dropped = true
			continue
		}
		kept = append(kept, line)
	}
	if !dropped {
		// Common case: no reserved cookie present. Leave the header(s)
		// exactly as the upstream sent them — no re-serialization risk.
		return
	}
	if len(kept) == 0 {
		resp.Header.Del("Set-Cookie")
		return
	}
	resp.Header["Set-Cookie"] = kept
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
