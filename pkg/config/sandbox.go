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

// FilesystemModel mirrors sandbox.FilesystemModel as a config-side type, the
// same way SandboxMode mirrors sandbox.Mode. The duplication is deliberate and
// matches the existing convention: pkg/config must not import pkg/sandbox, and
// the wire/config vocabulary is allowed to be stated where the config is
// defined. pkg/sandbox.ParseFilesystemModel is the single validator.
type FilesystemModel string

const (
	// FilesystemModelConfined enumerates readable and executable paths. The
	// behaviour of every release before ADR-062.
	FilesystemModelConfined FilesystemModel = "confined"

	// FilesystemModelOpen leaves reads and execution unrestricted apart from
	// the secret set, and confines writes exactly as confined does.
	FilesystemModelOpen FilesystemModel = "open"
)

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
	// (web_serve dev mode only). bash's background-session mode ("bash" —
	// ADR-036 unified the retired "exec"/"workspace_shell"/"workspace_shell_bg"
	// tools into it) has no port-exposure capability — that capability was
	// dropped, not ported, when workspace_shell_bg was merged (ADR-036 §3.1).
	// Default [18000, 18999] applied by the boot validator when the field is
	// zero.
	DevServerPortRange PortRange `json:"dev_server_port_range,omitempty"`

	// EgressAllowList is the operator-controlled host allow-list for the
	// egress proxy used by Tier 2 (build_static) and Tier 3 (web_serve dev
	// mode) child processes, plus "bash" (ADR-036 unified the retired
	// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it) for its
	// background-session mode's proxied egress. Entries may be exact
	// hostnames or "*.x" wildcard patterns. Default: ["registry.npmjs.org",
	// "github.com", "raw.githubusercontent.com"] applied by the boot
	// validator when empty.
	EgressAllowList []string `json:"egress_allow_list,omitempty"`

	// Tier3Commands extends the baseline Tier 3 dev-server command allow-list
	// with operator-defined commands (e.g. "remix dev"). Each entry is a full
	// "binary subcommand" string. Comparison is case-sensitive exact-prefix.
	Tier3Commands []string `json:"tier3_commands,omitempty"`

	// PathGuardAuditFailClosed controls behavior when the audit logger
	// fails during a Tier 2 (build_static) or Tier 3 (web_serve dev mode)
	// invocation, or a "bash" (ADR-036 unified the retired
	// "exec"/"workspace_shell"/"workspace_shell_bg" tools into it) invocation
	// — foreground or background alike; the audit gate (emitAuditOrDeny) runs
	// before the foreground/background branch, so both are covered by the
	// same check. When nil or true (default via ResolveBool), the tool
	// refuses to run without a guaranteed compliance trail. When explicitly
	// set to false, the audit failure is logged at Error and execution
	// proceeds (operator opt-out).
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
	// Documented gap: a compiled binary spawned via "bash" (ADR-036 unified
	// the retired "exec"/"workspace_shell"/"workspace_shell_bg" tools into
	// it) can still dial RFC1918 IPs on allowed ports (e.g.
	// https://192.168.1.1/) because kernel enforcement is port-only.
	// CIDR-level enforcement for compiled children would require eBPF cgroup
	// CGROUP_INET4_CONNECT, deferred to a later release. Operators concerned
	// about this gap should keep bash's tool-policy set to "deny" or "ask"
	// (CLAUDE.md hard constraint 6 — bash has no feature-flag gate, only an
	// explicit per-agent tool-policy entry) on agents that handle untrusted
	// content.
	//
	// Empty list (the default) means strict-block of the default-deny set
	// for code paths the gateway controls.
	EgressAllowCIDRs []string `json:"egress_allow_cidrs,omitempty"`

	// AllowedPaths lists additional filesystem paths the sandbox may read.
	// Paths outside this list (and the agent workspace) are inaccessible.
	AllowedPaths []string `json:"allowed_paths,omitempty"`

	// AllowedExecPaths lists directories the sandbox may READ and EXECUTE
	// from, and never write to. It exists because AllowedPaths cannot express
	// "run the tools installed here": that list grants read+write and never
	// the execute bit, so before this field an operator had no way at all to
	// let an agent run a toolchain outside the handful of system binary
	// directories baked into the kernel policy.
	//
	// That gap made agents unable to run node, npm, python or anything else
	// installed by Homebrew (/usr/local, /opt/homebrew) or a version manager
	// (fnm, nvm, volta, asdf, pyenv, rbenv, cargo), which is where these tools
	// live on a normal developer machine.
	//
	// Entries are read+execute ONLY — never writable. That separation is what
	// keeps this safe: a directory the agent can execute from but cannot write
	// to does not let it drop a binary and run it. The kernel enforces this
	// regardless of Unix ownership, so it holds even for Homebrew directories
	// owned by the console user.
	//
	// A leading ~ is expanded to the current user's home directory. Entries
	// that do not exist produce a rule that simply matches nothing.
	//
	// An entry overlapping a WRITABLE path — allowed_paths, or the built-in
	// $OMNIPUS_HOME / /tmp / $TMPDIR grants — is DROPPED with a warning when
	// the policy is built, not rejected at validation: the write grant wins and
	// the execute grant is discarded, so the union can never become a writable
	// AND executable directory. The tag deliberately omits `omitempty`: this
	// field is seeded non-empty, and omitting an operator's explicit empty list
	// on save would silently re-seed the defaults on the next boot.
	AllowedExecPaths []string `json:"allowed_exec_paths"`

	// FilesystemModel selects how the sandbox treats READS and PROGRAM
	// EXECUTION. Writes are confined identically under both values — this key
	// never widens what an agent can modify. ADR-062.
	//
	//   "confined" — reads and execution are allowed only on enumerated paths.
	//   "open"     — reads and execution are unrestricted, except for the
	//                secret set (master.key, credentials.json, config.json,
	//                cli.token, entities/), which stays unreachable.
	//
	// "confined" was the only behaviour before ADR-062 and it does not work in
	// practice: the set of paths a working toolchain reads cannot be listed in
	// advance, so every tool an operator installs breaks silently until someone
	// diagnoses a bare "operation not permitted" and edits allowed_exec_paths.
	// The open model removes that class of failure and is what Claude Code and
	// Codex both ship.
	//
	// Platform honesty: macOS enforces the secret set with real Seatbelt denies.
	// Linux enforces it by never granting those paths (Landlock has no deny
	// primitive). Windows has no filesystem sandbox backend at all, so neither
	// value changes anything there — see the boot WARN.
	//
	// The tag omits `omitempty` for the same reason allowed_exec_paths does:
	// the field is seeded non-empty, and dropping it on save would silently
	// re-seed the default on the next boot rather than persist the operator's
	// choice.
	FilesystemModel string `json:"filesystem_model"`

	// WorkspacePathGuard is the operator-facing control for the IN-PROCESS
	// path guard — the third file-access rule layer identified by ADR-068.
	// It is NOT the kernel sandbox, and it is deliberately named so that no
	// one can confuse the two:
	//
	//   - `sandbox.mode` (above) selects how the KERNEL enforces policy
	//     (Landlock on Linux, Seatbelt on macOS) on a child process that has
	//     already been spawned.
	//   - `sandbox.workspace_path_guard` (this field) decides, in Go and
	//     before any child exists, whether an agent's file tools and the
	//     text of a `bash` command may reference paths outside the agent's
	//     own home directory and its approved workspace mounts.
	//
	// The two are independent. Turning the kernel sandbox off does NOT turn
	// this guard off — which is precisely the operator-experience defect
	// ADR-068 §6 records: the Security page showed one switch, the operator
	// turned it off, and commands kept being refused by a different rule
	// that had no switch at all.
	//
	// What it actually governs today (be exact — a control whose label
	// over-promises is the ADR-037 anti-pattern this project bans). This
	// value resolves into AgentDefaults.RestrictToWorkspace, which is handed
	// to: write_file, edit_file, append_file, bash, send_file and
	// browser_screenshot as their write scope; and, combined with
	// AllowReadOutsideWorkspace, to read_file, list_directory, library_list
	// and library_read as their read scope (pkg/agent/instance.go). So it is
	// a PATH guard, not yet a pure write guard. ADR-068 step 2 narrows the
	// bash side of it to writes only; the file-tool read side keeps its own
	// separate control (allow_read_outside_workspace).
	//
	// *bool, not bool, and the reason is not cosmetic: a plain bool cannot
	// tell "the operator chose false" apart from "nobody has ever set this",
	// and SaveConfig re-marshals the whole struct on every save. nil means
	// unset and resolves to the shipped default of TRUE (guard on) via
	// ResolveBool — the same nil-means-unset pattern as
	// PathGuardAuditFailClosed above. `omitempty` keeps an unset field out
	// of config.json entirely, so an untouched install never grows the key.
	//
	// Precedence, applied by applyWorkspacePathGuard (validator.go):
	// OMNIPUS_AGENTS_DEFAULTS_RESTRICT_TO_WORKSPACE (the FR-001 env hatch,
	// still wins) > this key > the shipped default (true).
	//
	// Why it lives under `sandbox.` rather than under `agents.defaults.`
	// next to the field it drives: everything under `sandbox.` is blocked
	// from the generic PUT /api/v1/config endpoint (pkg/gateway/
	// blocked_paths.go) and from the sysagent's own `system.config.set`
	// tool (pkg/sysagent/tools/config.go), so it can only be changed through
	// the dedicated /api/v1/security/sandbox-config endpoint, which is
	// admin-gated, re-auth-gated and audited. `agents.defaults.*` is
	// writable through both of those unaudited surfaces — including by an
	// agent itself, which would let an agent switch off its own cage.
	WorkspacePathGuard *bool `json:"workspace_path_guard,omitempty"`

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

	// RateLimits configures per-agent LLM/tool call limits (SEC-26). All
	// fields default to 0 (no limit). The global daily USD cost cap that used
	// to live here (DailyCostCapUSD) was retired by ADR-053 D12 — the only
	// app-level spend brake is now pkg/agent.TokenBudget (PlanningConfig.TokenBudget).
	RateLimits OmnipusRateLimitsConfig `json:"rate_limits,omitempty"`

	// ToolPolicies holds global per-tool access policies. Keys are tool names;
	// values are "allow", "ask", or "deny". Takes precedence over agent-level
	// policies when stricter (deny > ask > allow).
	//
	// There is no global default-policy fallback (CLAUDE.md hard constraint
	// 6): every static builtin tool must resolve from an explicit, literal
	// entry here and/or in an agent's tools.builtin.policies map. Coverage
	// gaps are a hard validation failure (config.ValidateToolPolicyCoverage),
	// never a runtime default.
	ToolPolicies map[string]string `json:"tool_policies,omitempty"`

	// SSRF configures outbound-HTTP SSRF protection (SEC-24).
	// When Enabled is true, all tool HTTP clients (web_search, skills installer,
	// browser, exec proxy) route through the SSRFChecker which blocks
	// connections to private/internal IP ranges and cloud metadata endpoints.
	// AllowInternal lists hosts, IPs, or CIDRs that are exempted from SSRF
	// blocking (e.g. ["localhost", "10.0.0.0/8"] to allow an internal search
	// service while still blocking all other private ranges).
	SSRF OmnipusSSRFConfig `json:"ssrf,omitempty"`

	// GodMode is the runtime global "bypass-permissions" switch (O14). It is
	// DISTINCT from the --allow-god-mode boot flag: the boot flag (and the
	// nogodmode build tag) gate AVAILABILITY; this field is the live ON/OFF
	// state. When true (and god mode is available), the override engine:
	//   - floors every agent's effective tool policy at "allow" (no prompts);
	//   - forces the kernel sandbox off (full host fs + syscalls), network
	//     egress open, and shell guard / deny-patterns off, regardless of the
	//     fixed "bash" (ADR-036 unified the retired
	//     "exec"/"workspace_shell"/"workspace_shell_bg" tools into it) limits.
	// Audit logging, the prompt-injection guard, and rate limiting are NOT
	// disabled — those defend against external threats, not agent freedom.
	//
	// The override is non-destructive: tool policies are NOT mutated on disk.
	// The override is applied purely at resolution time (agentToolsCfgToPolicy
	// / the loop's god-mode wiring), so switching GodMode off restores the
	// prior behavior exactly.
	//
	// Toggled at runtime via POST /api/v1/gateway/god-mode (password step-up)
	// or set at boot for headless runs. Has no effect when god mode is not
	// available (nogodmode build, or neither --allow-god-mode nor
	// GodModeAllowed grants availability — see GodModeAllowed below).
	GodMode bool `json:"god_mode,omitempty"`

	// GodModeAllowed is the PERSISTED operator authorization for god mode
	// (Constraint #6: deny-by-default for security, opt-in for features). It is
	// DISTINCT from both GodMode (the live on/off switch above) and the
	// --allow-god-mode CLI flag:
	//   - GodMode is the runtime state; it does nothing without availability.
	//   - --allow-god-mode is a per-process boot flag (headless/CI opt-in),
	//     never persisted.
	//   - GodModeAllowed is a config-persisted grant, set by POST
	//     /api/v1/gateway/god-mode enabled=true from the Settings UI. It
	//     survives restarts so the UI-driven "flip switch -> restart to
	//     activate" flow does not require re-passing a CLI flag.
	//
	// AVAILABILITY (whether god mode CAN be turned on at all) is resolved
	// ONCE at boot as (--allow-god-mode OR GodModeAllowed) AND
	// sandbox.GodModeAvailable (build support), then frozen for the life of
	// the process (agent.SetAllowGodMode / the godModeAvailable atomic). This
	// is why enabling god mode from a boot where it was not yet available
	// only takes effect after a gateway restart: this flag can grant
	// authorization in config, but the frozen boot decision does not
	// re-evaluate until the next boot reads it.
	//
	// Disabling god mode (GodMode=false) does NOT clear this flag — once an
	// operator has authorized god mode, availability persists so future
	// enable/disable toggles apply live without another restart. Revoking
	// authorization entirely requires editing config.json directly.
	GodModeAllowed bool `json:"god_mode_allowed,omitempty"`

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
	// DevicePairingEnabled gates the device-pairing feature (admin approval of
	// companion devices — pkg/pairing, GET /api/v1/devices, the
	// device_pairing_response WS frame, and the Settings → Devices tab). The
	// pairing/approval scaffolding exists but the device-side request entry
	// point and persistence are not yet implemented — kept behind this flag,
	// default false, until that lands.
	DevicePairingEnabled bool `json:"device_pairing_enabled,omitempty"`
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
//
// ADR-053 D12 ("no money caps") retires the SEC-26 global daily USD cost
// cap that previously lived here as DailyCostCapUSD. The only app-level
// spend brake is now pkg/agent.TokenBudget (the operator-set token ceiling
// in PlanningConfig.TokenBudgetTokens). Per-agent sliding-window rate
// limits (LLM/hr, tool/min) remain.
type OmnipusRateLimitsConfig struct {
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
