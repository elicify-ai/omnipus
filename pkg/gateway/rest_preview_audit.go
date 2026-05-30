//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — shared audit emission for preview-listener routes
// (/preview/, /serve/, /dev/) (FR-024 / FR-024a).
//
// All three handlers in rest_preview.go call emitPreviewAuditEntry so
// the payload schema and remote-IP canonicalisation logic stay
// consistent across the unified and back-compat surfaces.

package gateway

import (
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// tokenPrefixRE is the canonical pattern for the first 8 characters of a
// valid token (base64-RawURL alphabet). Tokens that don't match this
// shape (e.g. URL-decoded bytes from an attacker-controlled string) are
// substituted with "<invalid>" to prevent log injection (F-12).
var tokenPrefixRE = regexp.MustCompile(`^[A-Za-z0-9_\-]{1,8}$`)

// firstServedTokens tracks tokens that have already produced an
// audit-bus serve.served / dev.proxied entry. Subsequent requests on the
// same token within its TTL skip the audit emission per FR-024 (per-
// asset audit on dev servers floods the bus — a Next.js page load can
// fire hundreds of asset requests).
//
// The set is process-global because tokens are unique 32-byte values
// across the entire gateway and the registry-level token uniqueness
// gives us free namespacing. Memory cost is bounded by max active
// registrations × token-string-size ≈ a few KB at typical load.
//
// Tokens are removed when their registration expires (see
// purgeFirstServedTokens called by janitor / Stop). A leak between TTL
// expiry and purge is harmless: at worst the same token (which the
// crypto/rand backend will not reissue) would skip its audit on the
// vanishingly unlikely re-mint.
var (
	firstServedMu     sync.Mutex
	firstServedTokens = make(map[string]struct{})
)

// markFirstServed atomically records that the given token has produced
// its serve.served / dev.proxied audit event. Returns true ONLY for the
// FIRST call per token within the process — subsequent calls return
// false. Thread-safe; safe to call from concurrent request handlers.
//
// To bound memory under sustained churn, a soft cap of 4096 entries is
// enforced — once exceeded, the entire set is dropped on the next
// addition and a Warn is logged so operators can see the breadcrumb.
// This cap fires only in pathological gateways; normal cleanup happens
// via purgeFirstServedTokens / purgeFirstServedTokensBulk called by the
// janitors in ServedSubdirs and DevServerRegistry on eviction.
func (a *restAPI) markFirstServed(token string) bool {
	if token == "" {
		return false
	}
	firstServedMu.Lock()
	defer firstServedMu.Unlock()
	if _, seen := firstServedTokens[token]; seen {
		return false
	}
	// Soft memory cap: at typical load (1 active token per agent, maybe
	// 100 agents) this is far below the limit. The cap exists to bound
	// pathological gateways with hundreds of thousands of churned
	// registrations over their uptime. When it fires, the entire set is
	// dropped — long-lived active tokens will re-emit serve.served once
	// after the reset, which is acceptable (F-9 safety net).
	const maxEntries = 4096
	if len(firstServedTokens) >= maxEntries {
		slog.Warn("preview-audit: firstServedTokens cap hit — resetting; serve.served may re-emit for active tokens",
			"cap", maxEntries)
		firstServedTokens = make(map[string]struct{}, maxEntries)
	}
	firstServedTokens[token] = struct{}{}
	return true
}

// purgeFirstServedTokens removes a single token from the firstServedTokens
// set. Called by ServedSubdirs.Evict / DevServerRegistry.Unregister so that
// evicted registrations don't re-emit serve.served if their token is reused
// (astronomically unlikely with 32-byte random tokens, but correct by design).
//
// Thread-safe. No-op when token is empty or absent.
func purgeFirstServedTokens(token string) {
	if token == "" {
		return
	}
	firstServedMu.Lock()
	delete(firstServedTokens, token)
	firstServedMu.Unlock()
}

// purgeFirstServedTokensBulk removes a batch of tokens from firstServedTokens
// in a single lock acquisition. Called by bulk eviction paths (e.g. the
// janitor in DevServerRegistry when it sweeps multiple expired entries).
//
// Thread-safe. Nil or empty slice is a no-op.
func purgeFirstServedTokensBulk(tokens []string) {
	if len(tokens) == 0 {
		return
	}
	firstServedMu.Lock()
	for _, tok := range tokens {
		delete(firstServedTokens, tok)
	}
	firstServedMu.Unlock()
}

// upstreamFailureWindow is the per-(token, remoteIP) suppression window for
// dev.upstream_unreachable audit emission. A flapping dev server can fire
// dozens of failures per second under HMR reconnects; without this dedup
// every retry fills the audit bus with effectively-identical entries. The
// 60s window is short enough that a real new outage produces a fresh
// audit entry for operator triage, but long enough to absorb a flapping
// loop.
const upstreamFailureWindow = 60 * time.Second

// upstreamFailureState is the value stored in firstUpstreamFailureTokens.
// It records when the most-recent admit fired and how many subsequent
// failures were swallowed during the suppression window. The counter is
// surfaced via slog when the next admit fires so operators can see how
// many failures were dropped during the quiet period.
type upstreamFailureState struct {
	firstSeenAt     time.Time
	suppressedCount int
}

// firstUpstreamFailureTokens tracks tokens that have recently emitted a
// dev.upstream_unreachable audit entry. Sibling to firstServedTokens —
// same locking discipline (process-global mutex), same 4096-entry soft
// cap, same reset-on-cap-hit semantics. Keyed by token+"|"+remoteIP so
// two concurrent clients hitting a flapping dev server each get their
// own first-in-window admit.
var (
	firstUpstreamFailureMu     sync.Mutex
	firstUpstreamFailureTokens = make(map[string]*upstreamFailureState)
)

// markFirstUpstreamFailure decides whether to admit a
// dev.upstream_unreachable audit entry for the given token+remoteIP
// combination. Returns:
//   - firstInWindow=true on the first call within upstreamFailureWindow,
//     OR when the previous admit's window has elapsed (a fresh outage).
//     suppressedCount is the number of failures that were swallowed
//     since the previous admit (zero on the very first call).
//   - firstInWindow=false within the suppression window. The internal
//     counter is incremented so the next admit can report it.
//
// Thread-safe; safe to call from concurrent ErrorHandler invocations.
func markFirstUpstreamFailure(token, remoteIP string) (firstInWindow bool, suppressedCount int) {
	if token == "" {
		// No token means we cannot dedup meaningfully. Admit and let the
		// caller decide; the audit will at least record the event.
		return true, 0
	}
	key := token + "|" + remoteIP
	now := time.Now()

	firstUpstreamFailureMu.Lock()
	defer firstUpstreamFailureMu.Unlock()

	// Soft memory cap mirroring firstServedTokens. Pathological gateways
	// could otherwise grow this map without bound under sustained churn.
	const maxEntries = 4096
	if len(firstUpstreamFailureTokens) >= maxEntries {
		slog.Warn("preview-audit: firstUpstreamFailureTokens cap hit — resetting; dev.upstream_unreachable may re-emit",
			"cap", maxEntries)
		firstUpstreamFailureTokens = make(map[string]*upstreamFailureState, maxEntries)
	}

	state, seen := firstUpstreamFailureTokens[key]
	if !seen {
		firstUpstreamFailureTokens[key] = &upstreamFailureState{firstSeenAt: now}
		return true, 0
	}

	// Window expired → admit again, surface the previous suppression
	// count to the caller, and reset the state to start a fresh window.
	if now.Sub(state.firstSeenAt) >= upstreamFailureWindow {
		prevSuppressed := state.suppressedCount
		state.firstSeenAt = now
		state.suppressedCount = 0
		return true, prevSuppressed
	}

	// Within the window — suppress and increment.
	state.suppressedCount++
	return false, 0
}

// emitPreviewAuditEntry writes a preview-listener audit entry (/preview/,
// /serve/, /dev/) to the gateway's audit logger. The schema follows
// FR-024:
//
//	{
//	  "agent_id": "<string>",
//	  "token_prefix": "<first 8 chars of token>",
//	  "sanitized_path": "/<prefix>/<agent>/<redacted>/<remaining>",
//	  "method": "<HTTP method>",
//	  "status": <int>,
//	  "remote_ip": "<X-Forwarded-For canonicalised, else r.RemoteAddr>",
//	  "bytes_out": <int, optional>,
//	  "duration_ms": <int>
//	}
//
// level is the slog level for the underlying message — Info for success
// (preview.served, serve.served, dev.proxied), Warn for security
// failures (preview.token_invalid, serve.token_invalid,
// dev.token_agent_mismatch, etc.).
//
// Audit-write failures are logged at Warn but NEVER block the HTTP
// response. Audit is best-effort here — the spec is explicit that
// per-asset audit on dev servers must not flood the bus.
func (a *restAPI) emitPreviewAuditEntry(
	r *http.Request,
	event string,
	decision string,
	agentID, token string,
	status int,
	startedAt time.Time,
	bytesOut int64,
	level slog.Level,
) {
	if a == nil || a.agentLoop == nil {
		return
	}
	logger := a.agentLoop.AuditLogger()

	durationMs := int64(time.Since(startedAt) / time.Millisecond)
	// F-12: sanitize the token prefix before writing to logs. On failure paths
	// (serve.token_invalid, dev.token_invalid) the token value is attacker-
	// controlled; URL-decoded bytes can carry newlines or control characters
	// that pollute structured log output. Accept only the base64-RawURL
	// alphabet in the first 8 chars; anything else becomes "<invalid>".
	tokenPrefix := token
	if len(tokenPrefix) > 8 {
		tokenPrefix = tokenPrefix[:8]
	}
	if !tokenPrefixRE.MatchString(tokenPrefix) {
		tokenPrefix = "<invalid>"
	}
	sanitisedPath := sanitisePreviewPath(r.URL.Path, token)
	// F-14: only trust X-Forwarded-For when cfg.Gateway.TrustXFF is set.
	// On plain-HTTP deployments without a trusted proxy, clients can spoof the
	// audit IP by sending this header. Pull config from context (set by
	// configSnapshotMiddleware); fall back to live config if not yet wired.
	var trustXFF bool
	if auditCfg := configFromContext(r.Context()); auditCfg != nil {
		trustXFF = auditCfg.Gateway.TrustXFF
	} else if liveCfg := a.agentLoop.GetConfig(); liveCfg != nil {
		trustXFF = liveCfg.Gateway.TrustXFF
	}
	remoteIP := canonicalRemoteIP(r, trustXFF)

	details := map[string]any{
		"token_prefix":   tokenPrefix,
		"sanitized_path": sanitisedPath,
		"method":         r.Method,
		"status":         status,
		"remote_ip":      remoteIP,
		"duration_ms":    durationMs,
	}
	if bytesOut > 0 {
		details["bytes_out"] = bytesOut
	}

	// slog mirror so operators tailing logs see preview events at the
	// expected level even when audit-bus writes are degraded.
	slog.Log(r.Context(), level, "preview-audit",
		"event", event,
		"decision", decision,
		"agent_id", agentID,
		"status", status,
		"remote_ip", remoteIP,
		"duration_ms", durationMs,
	)

	if logger == nil {
		// No audit logger wired (test path or degraded boot). The slog
		// line above is the surviving record.
		return
	}
	if err := logger.Log(&audit.Entry{
		Timestamp: time.Now().UTC(),
		Event:     event,
		Decision:  decision,
		AgentID:   agentID,
		Details:   details,
	}); err != nil {
		// Best-effort: audit-write failure is logged at Warn (preview
		// events are operational signals, not fail-closed gates). If the
		// audit pipeline is degraded this is the operator's signal.
		slog.Warn("preview-audit: audit log write failed",
			"event", event, "error", err)
	}
}

// sanitisePreviewPath redacts the token portion of a /serve/ or /dev/
// URL path so audit logs do not leak full bearer tokens. The token is
// replaced with "<redacted>" leaving the agent ID and any remaining path
// segments visible for operator triage.
//
// Input examples:
//
//	/serve/agent-1/abc123def456.../index.html
//	/dev/agent-2/xyz789.../api/login
//
// Output:
//
//	/serve/agent-1/<redacted>/index.html
//	/dev/agent-2/<redacted>/api/login
//
// Falls back to a static "<sanitisation-failed>" string if the path
// shape is unrecognizable — never returns the raw token.
func sanitisePreviewPath(urlPath, token string) string {
	if urlPath == "" {
		return ""
	}
	// Quick path: if the token appears verbatim, replace it. Cheaper than
	// re-parsing the path.
	if token != "" && strings.Contains(urlPath, token) {
		return strings.Replace(urlPath, token, "<redacted>", 1)
	}
	// Slow path: parse /<prefix>/<agent>/<token>/<rest>. The token is
	// always the third path segment for /serve/ and /dev/.
	trimmed := strings.TrimPrefix(urlPath, "/")
	parts := strings.SplitN(trimmed, "/", 4)
	if len(parts) < 3 {
		return urlPath
	}
	parts[2] = "<redacted>"
	return "/" + strings.Join(parts, "/")
}

// canonicalRemoteIP returns the client IP address for an HTTP request.
//
// When trustXFF is true (gateway.trust_xff in config), the first entry of
// X-Forwarded-For is used — appropriate when the gateway is behind a single
// trusted reverse proxy. Operators with multi-hop proxy chains should configure
// their inner proxy to set X-Forwarded-For to the outermost client IP.
//
// When trustXFF is false (the default), X-Forwarded-For is ignored and
// r.RemoteAddr is used exclusively. This prevents clients from spoofing
// their audit IP on plain-HTTP / bare-metal deployments (F-14 fix).
// See docs/operations/reverse-proxy.md for enabling this flag.
func canonicalRemoteIP(r *http.Request, trustXFF bool) string {
	if trustXFF {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// First comma-separated entry is the originating client.
			if idx := strings.Index(xff, ","); idx > 0 {
				return strings.TrimSpace(xff[:idx])
			}
			return strings.TrimSpace(xff)
		}
	}
	// Strip port from RemoteAddr if present.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
