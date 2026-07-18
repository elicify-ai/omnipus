// Package sandbox — BuildDevURL: helper for constructing absolute
// /dev/<agentID>/<token>/ preview URLs for dev-mode registrations.
//
// Originally lifted from the now-removed pkg/tools/run_in_workspace.go, the
// predecessor of the former workspace_shell_bg tool. When workspace_shell_bg
// was merged into "bash" (ADR-036 unified the retired
// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it), its
// port-exposure/preview-URL capability was dropped, not ported (ADR-036
// §3.1) — bash's background-session mode has no preview URL and does not
// use this helper.
//
// BuildDevURL currently has no production caller: web_serve.go builds its
// "/preview/..." URL independently via middleware.CanonicalGatewayOrigin(cfg)
// (preview-on-main-listener v5, ADR-044 D2) — the SAME canonical origin used
// for CORS/CSP/WS CheckOrigin, derived from the single main gateway listener.
// There is no separate preview listener / preview port / preview origin
// config anymore (those keys were deleted, no back-compat): /preview/ is
// served on cfg.Gateway.Port alongside the SPA and the REST API. BuildDevURL
// is kept only as a documented reference implementation of the legacy "/dev/"
// URL shape and scheme-coercion rule below, exercised only by
// dev_url_test.go — it does not model the current /preview/ URL contract and
// must not be reintroduced as a production call site.
//
// Scheme coercion rule: if gatewayHost does not contain "://" it is treated
// as a bare host[:port] and "https://" is prepended. Callers of this
// reference helper wanting a plain-HTTP result must supply the full URL form
// (e.g. "http://192.168.1.10:5000") to prevent the coercion from producing a
// mixed-content URL. (Production code does not hit this: it always uses the
// scheme middleware.CanonicalGatewayOrigin(cfg) itself derives — see above.)

package sandbox

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// devURLSchemeWarnOnce guards the one-time scheme-coercion WARN.
var devURLSchemeWarnOnce sync.Once

// BuildDevURL returns the absolute /dev/<agentID>/<token>/ URL for a preview,
// using gatewayHost as the origin. When gatewayHost is empty, returns just
// the path (test wiring).
//
// Note: unused in production (see the package doc comment above) — web_serve
// dev mode builds its own "/preview/..." URL independently, from
// middleware.CanonicalGatewayOrigin(cfg) (the main gateway listener's
// origin), rather than calling this function. bash's background-session mode
// ("bash" — ADR-036 unified the retired
// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it) also does not
// call this helper: the equivalent preview-URL capability was dropped, not
// ported, when workspace_shell_bg was merged (ADR-036 §3.1).
//
// gatewayHost examples accepted (raw /dev/ form shown; this is NOT the
// /preview/ form web_serve dev mode actually returns to callers):
//   - ""                       → "/dev/agent/token/"
//   - "127.0.0.1:5001"         → "https://127.0.0.1:5001/dev/agent/token/"
//   - "https://example.com"    → "https://example.com/dev/agent/token/"
//   - "http://192.168.1.1:5001"→ "http://192.168.1.1:5001/dev/agent/token/"
//   - "https://example.com/"   → "https://example.com/dev/agent/token/" (trailing slash stripped)
func BuildDevURL(agentID, token, gatewayHost string) string {
	path := fmt.Sprintf("/dev/%s/%s/", agentID, token)
	if gatewayHost == "" {
		return path
	}
	host := strings.TrimSuffix(gatewayHost, "/")
	if !strings.Contains(host, "://") {
		devURLSchemeWarnOnce.Do(func() {
			slog.Warn("legacy BuildDevURL: gatewayHost lacks a scheme; coercing to https — "+
				"this helper is unused in production (see package doc); real /preview/ URLs "+
				"are built from middleware.CanonicalGatewayOrigin(cfg), which derives its "+
				"scheme from cfg.Gateway.PublicURL/Host/Port",
				"gateway_host", gatewayHost)
		})
		// Bracket bare IPv6 addresses so the URL is valid (RFC 2732).
		// Heuristic: if the bare host contains ':' but no '.' (IPv4 addresses
		// have dots) and no '[' (already bracketed), treat it as an IPv6 literal.
		// Examples: "::1" → "[::1]", "2001:db8::1" → "[2001:db8::1]".
		// "127.0.0.1:5001" has dots so it is not rewritten.
		// "[::1]:5173" already has '[' so it passes through unchanged.
		if strings.Contains(host, ":") && !strings.Contains(host, ".") && !strings.HasPrefix(host, "[") {
			host = "[" + host + "]"
		}
		host = "https://" + host
	}
	return host + path
}
