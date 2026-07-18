// Package envcontext provides the "new-office onboarding" preamble that sits
// above an agent's identity in the system prompt (Fix A, spec v7). Each agent
// — core, custom, subagent — sees the same paths, sandbox mode, network
// policy, and active warnings derived from live runtime state. The preamble
// is rebuilt only when the outer system-prompt cache invalidates (FR-053);
// operator config changes trigger that invalidation via
// ContextBuilderRegistry.InvalidateAllContextBuilders (FR-061).
package envcontext

// Platform captures the host OS identity the agent is running on. Populated
// from runtime.GOOS / runtime.GOARCH + /proc/version on Linux. Kernel is "" on
// non-Linux hosts. See FR-043.
type Platform struct {
	GOOS   string
	GOARCH string
	Kernel string
}

// NetworkPolicy reports whether outbound network is permitted. Host-level
// allow-list is not yet wired into the sandbox config, so the preamble only
// exposes the boolean today — a future addition can extend this struct
// without a behavioral change for existing consumers. FR-045.
type NetworkPolicy struct {
	OutboundAllowed bool
}

// Provider is the read-only view of runtime environment state rendered into
// the preamble. Each method is invoked at most once per cache-rebuild cycle.
//
// Methods that can fail return (value, error) so the renderer can downgrade
// the field to "<unknown>" without aborting the entire preamble (FR-054,
// CRIT-005). Methods that can never fail return bare values.
type Provider interface {
	Platform() (Platform, error)
	SandboxMode() (string, error)
	NetworkPolicy() NetworkPolicy
	WorkspacePath() string
	OmnipusHome() string
	ActiveWarnings() []string
	// PublicURL returns the externally-reachable base origin for the main
	// gateway listener (config.Gateway.PublicURL, resolved the same way
	// canonicalGatewayOrigin does for CORS/CSP/WS-origin and for the
	// preview/serve_web link base) so the agent knows where pages it serves
	// (e.g. via serve_web) are actually reachable from outside the sandbox.
	// Returns "" when no reachable origin can be derived (e.g. a wildcard
	// bind with no gateway.public_url set) — the renderer omits the line
	// entirely in that case rather than showing a misleading value.
	PublicURL() string
}

// Render is the canonical preamble renderer. Returns "" when p is nil, which
// is how ContextBuilder.BuildSystemPrompt detects "no env wired" and skips
// the prelude.
func Render(p Provider, workspaceOverride string) string {
	if p == nil {
		return ""
	}
	return render(p, workspaceOverride)
}
