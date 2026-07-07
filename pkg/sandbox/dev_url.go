// Package sandbox — BuildDevURL: shared helper for constructing absolute
// /preview/<agentID>/<token>/ preview URLs for dev-mode registrations.
//
// Originally lifted from the now-removed pkg/tools/run_in_workspace.go, the
// predecessor of the former workspace_shell_bg tool. When workspace_shell_bg
// was merged into "bash" (ADR-036 unified the retired
// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it), its
// port-exposure/preview-URL capability was dropped, not ported (ADR-036
// §3.1) — bash's background-session mode has no preview URL and does not
// use this helper. web_serve dev mode is the only mode that uses this URL
// construction logic.
//
// Scheme coercion rule: if gatewayHost does not contain "://" it is treated
// as a bare host[:port] and "https://" is prepended. Operators running a
// plain-HTTP preview listener must supply the full URL form (e.g.
// "http://192.168.1.10:5001") to prevent the coercion from producing a
// mixed-content URL.

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
// Note: web_serve dev mode is the only consumer, and it overrides the
// result to emit /preview/ URLs instead of the raw /dev/ form. bash's
// background-session mode ("bash" — ADR-036 unified the retired
// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it) does not call
// this helper: the equivalent preview-URL capability was dropped, not
// ported, when workspace_shell_bg was merged (ADR-036 §3.1).
//
// gatewayHost examples accepted (raw /dev/ form shown; web_serve dev mode
// substitutes /preview/ before returning the URL to the caller):
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
			slog.Warn("preview origin lacks scheme; coercing to https — set gateway.preview_origin to a full URL",
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
