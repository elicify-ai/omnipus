// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"fmt"
)

// SkillTrustLevel controls how skills without a verifiable SHA-256 hash are handled (SEC-09).
type SkillTrustLevel string

const (
	// SkillTrustBlockUnverified blocks installation when hash cannot be verified.
	SkillTrustBlockUnverified SkillTrustLevel = "block_unverified"
	// SkillTrustWarnUnverified warns but allows unverified installs (default).
	SkillTrustWarnUnverified SkillTrustLevel = "warn_unverified"
	// SkillTrustAllowAll skips all hash verification. omnipus doctor warns when set.
	SkillTrustAllowAll SkillTrustLevel = "allow_all"
)

// UnmarshalJSON validates and deserializes a SkillTrustLevel from JSON.
// Rejects unknown values at decode time so config.json with a typo fails fast
// at boot instead of silently resolving to the zero value.
func (l *SkillTrustLevel) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch SkillTrustLevel(s) {
	case SkillTrustBlockUnverified, SkillTrustWarnUnverified, SkillTrustAllowAll:
		*l = SkillTrustLevel(s)
		return nil
	case "":
		// empty string means field was omitted — keep zero value
		*l = ""
		return nil
	default:
		return fmt.Errorf("invalid skill_trust: %q (must be one of: block_unverified, warn_unverified, allow_all)", s)
	}
}

// SandboxMode controls how kernel-level sandboxing enforces policy at boot.
// Typed enum so a typo in config.json fails decoding rather than silently
// resolving to a permissive default.
type SandboxMode string

const (
	// SandboxModeEnforce activates kernel-level sandboxing (Landlock/seccomp/JobObjects).
	SandboxModeEnforce SandboxMode = "enforce"
	// SandboxModePermissive logs policy violations without blocking (audit-only).
	SandboxModePermissive SandboxMode = "permissive"
	// SandboxModeOff disables sandboxing — development only.
	SandboxModeOff SandboxMode = "off"
)

// UnmarshalJSON validates and deserializes a SandboxMode from JSON. Empty is
// accepted (the gateway boot path applies the "enforce on capable kernels"
// fresh-install default in that case). Unknown non-empty values are rejected
// so typos like "enfroce" fail at load time.
func (m *SandboxMode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch SandboxMode(s) {
	case SandboxModeEnforce, SandboxModePermissive, SandboxModeOff:
		*m = SandboxMode(s)
		return nil
	case "":
		*m = ""
		return nil
	default:
		return fmt.Errorf("invalid sandbox.mode: %q (must be one of: enforce, permissive, off)", s)
	}
}

// SandboxProfile is the per-agent kernel sandbox profile.
// It controls which sandbox limits are applied when an agent spawns child
// processes. Empty means "use the global default (DefaultProfile)".
type SandboxProfile string

const (
	// SandboxProfileWorkspace confines the agent to its workspace directory
	// (Landlock) with outbound network blocked (seccomp/egress-proxy deny-all).
	SandboxProfileWorkspace SandboxProfile = "workspace"
	// SandboxProfileWorkspaceNet confines the agent to its workspace directory
	// but permits outbound HTTP/HTTPS through the egress allow-list proxy.
	SandboxProfileWorkspaceNet SandboxProfile = "workspace+net"
	// SandboxProfileHost allows access to the full host filesystem and network
	// with only the most dangerous syscalls (mount, kexec, ptrace, …) blocked.
	SandboxProfileHost SandboxProfile = "host"
	// SandboxProfileOff disables all kernel-level sandboxing for this agent.
	// This is the "god mode" profile gated by three independent latches.
	SandboxProfileOff SandboxProfile = "off"
)

// IsValid reports whether p is one of the defined SandboxProfile constants.
// An empty string is considered valid (means "use global default").
func (p *SandboxProfile) IsValid() bool {
	switch *p {
	case SandboxProfileWorkspace, SandboxProfileWorkspaceNet,
		SandboxProfileHost, SandboxProfileOff, "":
		return true
	default:
		return false
	}
}

// String implements fmt.Stringer.
func (p *SandboxProfile) String() string { return string(*p) }

// MarshalJSON serializes the profile as a JSON string.
func (p *SandboxProfile) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(*p))
}

// UnmarshalJSON validates and deserialises a SandboxProfile from JSON.
// Empty string is accepted (field omitted in config.json; callers default-fill).
// Unknown non-empty values are rejected so typos fail at load time rather than
// silently behaving as the zero value.
func (p *SandboxProfile) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch SandboxProfile(s) {
	case SandboxProfileWorkspace, SandboxProfileWorkspaceNet,
		SandboxProfileHost, SandboxProfileOff:
		*p = SandboxProfile(s)
		return nil
	case "":
		*p = ""
		return nil
	default:
		return fmt.Errorf("invalid sandbox_profile: %q (must be one of: workspace, workspace+net, host, off)", s)
	}
}

// PromptInjectionLevel controls prompt guard aggressiveness (SEC-25).
type PromptInjectionLevel string

const (
	// PromptInjectionLow applies minimal prompt sanitization.
	PromptInjectionLow PromptInjectionLevel = "low"
	// PromptInjectionMedium applies moderate prompt sanitization (default).
	PromptInjectionMedium PromptInjectionLevel = "medium"
	// PromptInjectionHigh applies aggressive prompt sanitization.
	PromptInjectionHigh PromptInjectionLevel = "high"
)

// UnmarshalJSON validates and deserializes a PromptInjectionLevel from JSON.
// Empty string is accepted (config may legitimately omit it — the handler
// defaults to "medium"). Only genuinely unknown non-empty values are rejected.
func (l *PromptInjectionLevel) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	switch PromptInjectionLevel(s) {
	case PromptInjectionLow, PromptInjectionMedium, PromptInjectionHigh:
		*l = PromptInjectionLevel(s)
		return nil
	case "":
		// empty string means field was omitted — keep zero value
		*l = ""
		return nil
	default:
		return fmt.Errorf("invalid prompt_injection_level: %q (must be one of: low, medium, high)", s)
	}
}

// PortRange is a pair of int32 values representing [min, max] port bounds
// (inclusive on both ends). Used for DevServerPortRange in OmnipusSandboxConfig.
type PortRange [2]int32

// IsZero reports whether the PortRange is the zero value (both fields zero).
// Used by the boot validator to detect "not configured" so it can apply the
// default [18000, 18999] range without overwriting an explicit [0, 0] (which
// is not a valid range and will be rejected by the sandbox layer anyway).
func (r PortRange) IsZero() bool {
	return r[0] == 0 && r[1] == 0
}

// Min returns the lower bound (inclusive). Named accessor for callsite clarity
// since PortRange is an array type.
func (r PortRange) Min() int32 { return r[0] }

// Max returns the upper bound (inclusive). Named accessor.
func (r PortRange) Max() int32 { return r[1] }

// Validate checks that the PortRange has Min in [1, 65535], Max in [1, 65535],
// and Min <= Max. Returns nil for the zero-value range (the boot validator
// applies a default before invoking this). Callers that expect a configured
// range MUST check IsZero() first.
func (r PortRange) Validate() error {
	if r.IsZero() {
		return nil
	}
	if r[0] < 1 || r[0] > 65535 {
		return fmt.Errorf("dev_server_port_range min %d out of [1,65535]", r[0])
	}
	if r[1] < 1 || r[1] > 65535 {
		return fmt.Errorf("dev_server_port_range max %d out of [1,65535]", r[1])
	}
	if r[0] > r[1] {
		return fmt.Errorf("dev_server_port_range min %d > max %d", r[0], r[1])
	}
	return nil
}

// Contains reports whether p falls within the inclusive [Min, Max] range.
// Returns false for the zero range to avoid accepting port 0 as valid.
func (r PortRange) Contains(p int32) bool {
	if r.IsZero() {
		return false
	}
	return p >= r[0] && p <= r[1]
}

// OmnipusSandboxConfig holds Wave 2 kernel-level sandboxing configuration per
// BRD SEC-01 through SEC-20 (Landlock, seccomp, Job Objects, RBAC, audit log)
// and Sprint-J sandbox-apply wiring (FR-J-001..016).
//
// All fields default to the most restrictive safe value when omitted.
// Populated from config.json under the "sandbox" key.
type OmnipusSandboxConfig struct {
	// MaxConcurrentDevServers caps the number of web_serve dev-mode servers
	// (Tier 3) that can be running concurrently across all agents. Default 4
	// (applied by the boot validator).
	MaxConcurrentDevServers int32 `json:"max_concurrent_dev_servers,omitempty"`

	// MaxConcurrentBuilds caps the number of build_static (Tier 2) processes
	// running concurrently. Default 2 (applied by the boot validator).
	MaxConcurrentBuilds int32 `json:"max_concurrent_builds,omitempty"`

	// DevServerPortRange is the [min, max] inclusive port range for Tier 3
	// (web_serve dev mode and workspace.shell_bg). Default [18000, 18999]
	// applied by the boot validator when the field is zero.
	DevServerPortRange PortRange `json:"dev_server_port_range,omitempty"`

	// EgressAllowList is the operator-controlled host allow-list for the
	// egress proxy used by Tier 2 (build_static) and Tier 3 (web_serve dev
	// mode and workspace.shell_bg) child processes. Entries may be exact
	// hostnames or "*.x" wildcard patterns. Default: ["registry.npmjs.org",
	// "github.com", "raw.githubusercontent.com"] applied by the boot
	// validator when empty.
	EgressAllowList []string `json:"egress_allow_list,omitempty"`

	// Tier3Commands extends the baseline Tier 3 dev-server command allow-list
	// with operator-defined commands (e.g. "remix dev"). Each entry is a full
	// "binary subcommand" string. Comparison is case-sensitive exact-prefix.
	Tier3Commands []string `json:"tier3_commands,omitempty"`

	// PathGuardAuditFailClosed controls behavior when the audit logger
	// fails during a Tier 2 (build_static) or Tier 3 (web_serve / workspace
	// shell) invocation. When nil or true (default via ResolveBool), the
	// tool refuses to run without a guaranteed compliance trail. When
	// explicitly set to false, the audit failure is logged at Error and
	// execution proceeds (operator opt-out).
	PathGuardAuditFailClosed *bool `json:"path_guard_audit_fail_closed,omitempty"`

	// BrowserEvaluateEnabled gates browser.evaluate (arbitrary JS execution).
	// Defaults to false (deny-by-default per SEC-04/SEC-06). Must be explicitly
	// opted in by the operator. Mirrors Tools.Browser.EvaluateEnabled but
	// lives here so it can be managed alongside other sandbox-level controls
	// without touching the Tools subtree.
	BrowserEvaluateEnabled bool `json:"browser_evaluate_enabled,omitempty"`

	// Mode selects how the sandbox enforces policy at boot.
	// Valid values: SandboxModeEnforce (default on capable kernels),
	// SandboxModePermissive (audit-only), SandboxModeOff (development only).
	// Unknown values are rejected at config-load time by SandboxMode's
	// UnmarshalJSON. An empty Mode on a fresh config is treated as
	// "enforce on capable kernels" by the gateway boot path.
	Mode SandboxMode `json:"mode,omitempty" env:"OMNIPUS_SANDBOX_MODE"`

	// AllowNetworkOutbound permits sandboxed processes to make outbound TCP
	// connections. When false (default), outbound connections are blocked
	// at the Landlock/seccomp layer. Has effect only when Mode is enforce
	// or permissive.
	AllowNetworkOutbound bool `json:"allow_network_outbound,omitempty"`

	// EgressAllowCIDRs is the operator-supplied list of CIDR ranges that are
	// explicitly permitted for outbound connections from sandboxed children
	// (v0.2 #155 item 4). The default-deny set covers RFC1918 (10/8,
	// 172.16/12, 192.168/16), link-local (169.254/16 — including the cloud
	// metadata endpoint), loopback (127/8, ::1/128), and IPv6 unique-local
	// + link-local (fc00::/7, fe80::/10). Operators with a legitimate
	// internal-service requirement add the CIDR here to bypass the deny.
	//
	// What is enforced where:
	//   - Kernel layer (Landlock NET_CONNECT_TCP, ABI v4+): port-level
	//     allow-list only — Landlock cannot filter by destination IP. The
	//     gateway installs a port allow-list of {53, 80, 443} plus the
	//     dev-server port range; everything else is blocked at connect(2).
	//   - Go-side layer (pkg/security/SSRFChecker): the CIDR-level filter
	//     applies to gateway-controlled HTTP clients (web_search, MCP fetches,
	//     skills installer). Entries here are merged into the SSRFChecker's
	//     allow-list at boot.
	//
	// Documented gap: a compiled binary spawned via workspace.shell can still
	// dial RFC1918 IPs on allowed ports (e.g. https://192.168.1.1/) because
	// kernel enforcement is port-only. CIDR-level enforcement for compiled
	// children would require eBPF cgroup CGROUP_INET4_CONNECT, deferred to a
	// later release. Operators concerned about this gap should keep
	// experimental.workspace_shell_enabled=false on agents that handle
	// untrusted content.
	//
	// Empty list (the default) means strict-block of the default-deny set
	// for code paths the gateway controls.
	EgressAllowCIDRs []string `json:"egress_allow_cidrs,omitempty"`

	// AllowedPaths lists additional filesystem paths the sandbox may read.
	// Paths outside this list (and the agent workspace) are inaccessible.
	AllowedPaths []string `json:"allowed_paths,omitempty"`

	// AuditLog enables the structured security audit log per SEC-17.
	// Written to ~/.omnipus/system/audit.jsonl.
	AuditLog bool `json:"audit_log,omitempty"`

	// SkillTrust controls how skills without a verifiable SHA-256 hash are handled (SEC-09).
	// Valid values: SkillTrustBlockUnverified, SkillTrustWarnUnverified (default), SkillTrustAllowAll.
	// SkillTrustAllowAll disables hash verification and triggers an omnipus doctor warning.
	SkillTrust SkillTrustLevel `json:"skill_trust,omitempty"`

	// PromptInjectionLevel controls how aggressively the prompt guard
	// sanitizes untrusted tool results (SEC-25). Valid: "low", "medium"
	// (default), "high". Affects web_search, web_fetch, browser_*, read_file
	// results before they enter the LLM's context.
	PromptInjectionLevel PromptInjectionLevel `json:"prompt_injection_level,omitempty"`

	// RateLimits configures per-agent LLM/tool call limits and the global
	// daily cost cap (SEC-26). All fields default to 0 (no limit).
	RateLimits OmnipusRateLimitsConfig `json:"rate_limits,omitempty"`

	// ToolPolicies holds global per-tool access policies. Keys are tool names;
	// values are "allow", "ask", or "deny". Takes precedence over agent-level
	// policies when stricter (deny > ask > allow).
	ToolPolicies map[string]string `json:"tool_policies,omitempty"`

	// DefaultToolPolicy is the fallback global policy for tools not listed in
	// ToolPolicies. Valid values: "allow" (default), "ask", "deny".
	DefaultToolPolicy string `json:"default_tool_policy,omitempty"`

	// SSRF configures outbound-HTTP SSRF protection (SEC-24).
	// When Enabled is true, all tool HTTP clients (web_search, skills installer,
	// browser, exec proxy) route through the SSRFChecker which blocks
	// connections to private/internal IP ranges and cloud metadata endpoints.
	// AllowInternal lists hosts, IPs, or CIDRs that are exempted from SSRF
	// blocking (e.g. ["localhost", "10.0.0.0/8"] to allow an internal search
	// service while still blocking all other private ranges).
	SSRF OmnipusSSRFConfig `json:"ssrf,omitempty"`

	// DefaultProfile is the global fallback sandbox profile applied to agents
	// that do not specify their own SandboxProfile. When empty, defaults to
	// SandboxProfileWorkspace at enforcement time.
	DefaultProfile SandboxProfile `json:"default_profile,omitempty"`

	// ShellDenyPatterns is the global operator-controlled list of shell command
	// deny patterns (regular expressions). Per-agent AgentShellPolicy.CustomDenyPatterns
	// are merged with this list at enforcement time. Patterns that fail to compile
	// are logged at Warn and skipped.
	ShellDenyPatterns []string `json:"shell_deny_patterns,omitempty"`

	// Experimental holds feature flags for dark-launched capabilities.
	// All flags default to false (deny-by-default per SEC design).
	Experimental ExperimentalConfig `json:"experimental,omitempty"`
}

// ExperimentalConfig holds feature flags for dark-launched tools and capabilities.
// All flags default to false (deny-by-default per SEC hard constraint #6).
type ExperimentalConfig struct {
	// WorkspaceShellEnabled gates the workspace.shell and workspace.shell_bg
	// builtin tools. Defaults to false (deny-by-default). Operators must
	// explicitly opt in by writing:
	//   {"experimental": {"workspace_shell_enabled": true}}
	//
	// The validator fills a nil pointer with false. Jim (the general-purpose
	// core agent) has this flag flipped to true during SeedConfig.
	WorkspaceShellEnabled *bool `json:"workspace_shell_enabled,omitempty"`
}

// OmnipusSSRFConfig holds SSRF protection settings for outbound HTTP clients (SEC-24).
type OmnipusSSRFConfig struct {
	// Enabled activates SSRF protection for all outbound HTTP tool clients.
	// Default: false (not enabled). Set to true to block private-IP connections.
	Enabled bool `json:"enabled,omitempty"`

	// AllowInternal lists hostnames, exact IPs, or CIDR ranges that are exempt
	// from SSRF blocking even when Enabled is true. Entries may be:
	//   - Exact IPv4/IPv6:  "127.0.0.1", "::1"
	//   - CIDR range:       "10.0.0.0/8", "192.168.0.0/16"
	//   - Hostname:         "localhost", "internal.corp"
	AllowInternal []string `json:"allow_internal,omitempty"`
}

// OmnipusRateLimitsConfig holds Wave 4 rate limit configuration (SEC-26).
// All fields default to 0, meaning no limit is enforced.
type OmnipusRateLimitsConfig struct {
	// DailyCostCapUSD is the global daily cost cap in USD. 0 = no cap.
	DailyCostCapUSD float64 `json:"daily_cost_cap_usd,omitempty"`
	// MaxAgentLLMCallsPerHour limits LLM calls per agent per hour. 0 = no limit.
	MaxAgentLLMCallsPerHour int `json:"max_agent_llm_calls_per_hour,omitempty"`
	// MaxAgentToolCallsPerMinute limits tool calls per agent per minute. 0 = no limit.
	MaxAgentToolCallsPerMinute int `json:"max_agent_tool_calls_per_minute,omitempty"`
}

// ResolvedMode returns the effective sandbox mode string. An empty Mode
// resolves to "off" — callers that want the "enforce on capable kernels"
// default for fresh installs apply it at a higher layer (e.g. the gateway
// boot path), so this helper only reports what the config file says.
func (s OmnipusSandboxConfig) ResolvedMode() string {
	if s.Mode != "" {
		return string(s.Mode)
	}
	return string(SandboxModeOff)
}
