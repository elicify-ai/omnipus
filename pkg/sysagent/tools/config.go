// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---- system.config.get ----

type ConfigGetTool struct{ deps *Deps }

func NewConfigGetTool(d *Deps) *ConfigGetTool   { return &ConfigGetTool{deps: d} }
func (t *ConfigGetTool) Name() string           { return "get_config" }
func (t *ConfigGetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ConfigGetTool) Description() string {
	return "Read a configuration value by dot-notation key.\nParameters: key (required, e.g. 'gateway.port')."
}

func (t *ConfigGetTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"key": map[string]any{"type": "string"}},
		"required":   []string{"key"},
	}
}

func (t *ConfigGetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	key, _ := args["key"].(string)
	if key == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "key is required", ""))
	}
	// Block reading sensitive keys by name.
	if isSensitiveConfigName(key) {
		return tools.ErrorResult(errorJSON("FORBIDDEN",
			"Credentials cannot be read via config.get — they are write-only",
			"Use list_providers to see configured providers (without keys)",
		))
	}
	// Block reading the keys the policy table marks undisclosable. get_config
	// used to have no deny list at all: every key set_config refuses was
	// readable, so the tool handed out the account hashes (gateway.users), the
	// auth-bypass flag and the whole enforcement configuration — the map an
	// attacker wants BEFORE using any of the write-side findings.
	if err := validateConfigReadKey(key); err != nil {
		return tools.ErrorResult(errorJSON("FORBIDDEN", err.Error(),
			"Operators inspect these in Settings → Security or config.json"))
	}
	value, err := dotGet(t.deps.GetCfg(), key)
	if err != nil {
		return tools.ErrorResult(errorJSON("KEY_NOT_FOUND",
			fmt.Sprintf("Config key %q not found: %v", key, err),
			"Check the key name and try again",
		))
	}
	// A section read must not return what the direct read of its children
	// refuses; blocked and credential-bearing descendants come back redacted.
	return tools.NewToolResult(successJSON(map[string]any{
		"key":    key,
		"value":  redactConfigValue(key, value),
		"source": "config",
	}))
}

// ---- system.config.set ----

type ConfigSetTool struct{ deps *Deps }

func NewConfigSetTool(d *Deps) *ConfigSetTool   { return &ConfigSetTool{deps: d} }
func (t *ConfigSetTool) Name() string           { return "set_config" }
func (t *ConfigSetTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ConfigSetTool) Description() string {
	return "Update a configuration value.\nParameters: key (required), value (required)."
}

func (t *ConfigSetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":   map[string]any{"type": "string"},
			"value": map[string]any{"description": "New config value (any JSON-compatible type)"},
		},
		"required": []string{"key", "value"},
	}
}

func (t *ConfigSetTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	key, _ := args["key"].(string)
	if key == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "key is required", ""))
	}
	value, hasValue := args["value"]
	if !hasValue {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "value is required", ""))
	}
	if isSensitiveConfigName(key) {
		return tools.ErrorResult(errorJSON("FORBIDDEN",
			"Use configure_provider to set API keys",
			"Credentials are stored encrypted in credentials.json, not config.json",
		))
	}
	// Validate that the key refers to a known config path to prevent arbitrary injection.
	if err := validateConfigKey(key); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_KEY", err.Error(),
			"Use get_config to inspect available config keys"))
	}
	requiresRestart := isRestartRequired(key)

	// Capture prevValue before mutation (under the same lock as the mutation).
	var prevValue any
	var setErr error
	err := t.deps.WithConfig(func(cfg *config.Config) error {
		prevValue, _ = dotGet(cfg, key)
		if err := dotSet(cfg, key, value); err != nil {
			setErr = err
			return fmt.Errorf("SET_FAILED: %w", err)
		}
		return nil
	})
	if setErr != nil {
		return tools.ErrorResult(errorJSON("SET_FAILED", setErr.Error(),
			"Check that the key is a valid config path"))
	}
	if err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"key":              key,
		"value":            value,
		"previous_value":   prevValue,
		"requires_restart": requiresRestart,
	}))
}

// knownConfigPrefixes are the top-level config sections that can be set at runtime.
// Keys outside this set are rejected by system.config.set to avoid corrupting the config.
var knownConfigPrefixes = []string{
	"gateway.", "agents.", "sandbox.", "security.", "channels.", "tools.",
	"heartbeat.", "devices.", "providers", "workspace_path",
}

// blockedConfigKey names one config key — or one subtree root — that generic
// config mutation must never write, together with the reason it is off limits.
//
// It is a table rather than a run of `if` statements so the policy is data:
// one place to read, one place to extend, and directly enumerable by a test
// (see TestValidateConfigKey_BlockedKeys, which asserts every entry here is
// refused and that a representative legitimate key in the same section is
// still accepted).
type blockedConfigKey struct {
	// Key is a config key or the root of a subtree. A candidate key matches
	// when it is AT, UNDER or ABOVE Key — see configKeyCovers and
	// configKeyIsAncestorOf — compared case-insensitively.
	Key string
	// Reason is surfaced verbatim in the rejection so the caller — usually an
	// LLM — is told what it hit and where the setting really lives, instead of
	// retrying variations of the same key.
	Reason string

	// ReadReason, when non-empty, additionally makes the key unreadable via
	// get_config, and is surfaced verbatim the way Reason is. It is a separate
	// sentence from Reason because the two answer different questions: Reason
	// says why WRITING widens the agent's cage; ReadReason says why the VALUE
	// must not be disclosed.
	//
	// Exactly one of ReadReason / ReadOKReason must be set on every entry —
	// pinned by TestBlockedConfigKeys_ReadPolicyIsDecidedForEveryEntry. A new
	// entry therefore cannot be added without someone deciding, in writing,
	// whether it is readable; the alternative (an empty field meaning
	// "readable") would make every future addition silently fail open on the
	// read side.
	ReadReason string
	// ReadOKReason, when non-empty, records why this key is deliberately
	// READABLE even though it is not writable, and is never shown to the
	// caller. It exists so the carve-out is justified in the table rather than
	// inferred from the absence of a ReadReason.
	ReadOKReason string
}

// blockedConfigKeys is the security-critical surface that set_config refuses.
//
// The threat this closes: set_config takes no path argument and therefore
// bypasses the filesystem chokepoint entirely, yet the keys below are the very
// controls that define an agent's boundary. An agent able to write them can
// widen its own cage — turn the sandbox off, mint itself an API credential,
// point exec/browser/MCP at an arbitrary binary, or switch off the redaction
// that keeps secrets out of its own context. That is exactly the exposure
// ADR-060's secret set exists to prevent for config.json on disk; this tool
// edits the same settings through the front door.
//
// This is a DENY list layered on top of the knownConfigPrefixes ALLOW list, not
// a replacement for it: a key must both start with a known section AND avoid
// every entry here. Entries are matched before the allow list.
//
// The same table governs get_config, minus the entries that carry a
// ReadOKReason. Reads and writes are two different threats over one overlapping
// set of keys — writing widens the cage, reading discloses — so the policy is
// ONE table with a per-entry read decision rather than two tables that drift.
// Defaulting reads to the write list is what makes a future addition
// fail-closed on both axes.
//
// Nothing here is a tool-policy fallback (CLAUDE.md hard constraint 6). Tool
// policy stays explicit, seeded data; this list only decides which config keys
// one particular tool may write. sandbox.tool_policies remains fully
// operator-editable — via Settings, the REST API, or config.json — it just is
// not editable by the agent whose policies it governs.
var blockedConfigKeys = []blockedConfigKey{
	// ADR-054 §11 checklist item 6: agents.list (the retired agent-roster
	// entity blob) must be rejected specifically — NOT by removing the
	// "agents." prefix from knownConfigPrefixes, which gates the whole agents.*
	// namespace and would also break agents.defaults.* (including
	// agents.defaults.default_agent_id, D6.4).
	{
		Key: "agents.list",
		Reason: "the agent roster is managed by the agent store, not generic config — " +
			"use create_agent/update_agent/delete_agent instead",
		ReadOKReason: "the roster is neither credential material nor an enforcement control — " +
			"it is write-blocked because the agent store owns it, and list_agents already " +
			"returns the same data",
	},

	// ---- the sandbox enforcement surface (entire subtree) ----
	//
	// Every field under sandbox.* is a security control: mode, god_mode,
	// god_mode_allowed, tool_policies, shell_deny_patterns, allowed_paths,
	// allowed_exec_paths, filesystem_model, egress_allow_list,
	// egress_allow_cidrs, allow_network_outbound, ssrf.*, audit_log,
	// skill_trust, prompt_injection_level, browser_evaluate_enabled. There is
	// no non-security key in the namespace worth carving an exception for, and
	// a subtree block is the only form that stays correct when a field is added.
	{
		Key: "sandbox",
		Reason: "the whole sandbox namespace is the enforcement boundary itself (mode, god_mode, " +
			"tool_policies, shell_deny_patterns, allowed_paths, allowed_exec_paths, filesystem_model, " +
			"egress and SSRF allow-lists, audit_log, skill_trust, prompt_injection_level) — " +
			"change it in Settings → Security or config.json, never from an agent",
		ReadOKReason: "the agent's own enforcement configuration. OPERATOR DECISION 2026-08-12: an agent granted the config tool may READ its settings — that is what the tool is for, and it is how an agent can explain WHY it cannot do something instead of just failing. Writing this namespace stays blocked: reading the cage is diagnosis, opening it is escape",
	},

	// ---- gateway authentication, origin and approval controls ----
	{
		Key:          "gateway.dev_mode_bypass",
		Reason:       "it disables gateway authentication entirely, granting unauthenticated admin access to the whole API",
		ReadOKReason: "an agent that can see the front door is unauthenticated can say so; it still cannot change it",
	},
	{
		Key:    "gateway.users",
		Reason: "it is the bearer-token account list — writing it mints an API credential",
		ReadReason: "it holds the bcrypt password and session-token hashes of every account — " +
			"offline-cracking material, not configuration",
	},
	{
		// Also caught by the credential substring filter above; listed so this
		// predicate is correct standalone if that filter is ever narrowed.
		Key:        "gateway.token",
		Reason:     "it is the gateway bearer token — credentials live in the encrypted store",
		ReadReason: "it is the gateway bearer token in plaintext",
	},
	{
		Key:        "gateway.cli_token",
		Reason:     "it is the CLI bearer token — credentials live in the encrypted store",
		ReadReason: "it is the CLI token entry — its hash and issuance metadata",
	},
	{
		Key: "gateway.public_url",
		Reason: "it defines the canonical origin that CSP, CORS and the WebSocket CheckOrigin " +
			"all validate against (ADR-044 D2)",
		ReadOKReason: "it is the origin a browser types — public by construction — and agents build " +
			"web_serve preview links from it (ADR-044)",
	},
	{
		Key: "gateway.trust_xff",
		Reason: "it makes the client IP attacker-controlled, which is what auth rate limiting " +
			"and audit attribution key on",
		ReadOKReason: "enforcement configuration, readable for the same reason as sandbox; the write stays blocked",
	},
	{
		Key:          "gateway.validate_inbound",
		Reason:       "it is the inbound contract validator (Constraint #8)",
		ReadOKReason: "enforcement configuration, readable; the write stays blocked",
	},
	{
		Key:          "gateway.auth_mismatch_log_level",
		Reason:       "it controls whether failed authentication attempts are logged at all",
		ReadOKReason: "a log level. Knowing what is recorded is not an advantage worth withholding from a tool the operator granted",
	},
	{
		Key:    "gateway.tool_approval_timeout",
		Reason: "it governs the human approval gate that every ask-policy tool call passes through",
		ReadOKReason: "the timeout is user-visible in the approval prompt itself — an agent may " +
			"legitimately say how long a pending approval lives, and knowing it widens nothing",
	},
	{
		Key:    "gateway.tool_approval_max_pending",
		Reason: "it governs the human approval gate that every ask-policy tool call passes through",
		ReadOKReason: "a queue depth is operational, not secret, and knowing it does not let an " +
			"agent approve anything",
	},

	// ---- tool-surface boundary controls ----
	{
		Key:          "tools.allow_read_paths",
		Reason:       "it is the filesystem read boundary for every file tool",
		ReadOKReason: "the read fence's location. Post-ADR-060 reads are open anyway, so this discloses nothing an agent cannot already determine by reading",
	},
	{
		Key:          "tools.allow_write_paths",
		Reason:       "it is the filesystem write boundary for every file tool",
		ReadOKReason: "the write fence's location — precisely what an agent needs in order to explain a refused write rather than retrying blindly",
	},
	{
		Key: "tools.filter_sensitive_data",
		Reason: "it is the filter that strips API keys, tokens and secrets out of tool results " +
			"before they reach the model",
		ReadOKReason: "whether results are scrubbed. Readable; the write stays blocked",
	},
	{
		Key: "tools.filter_min_length",
		Reason: "it tunes that same secret-redaction filter — a large value switches redaction off " +
			"without appearing to disable anything",
		ReadOKReason: "readable with the write blocked. The chunk-a-secret concern it was denied for is not addressed by hiding a threshold that can be measured in a few calls",
	},
	{
		Key: "tools.exec",
		Reason: "it holds the exec binary allow-list, the exec approval mode and the egress proxy " +
			"toggle for spawned processes",
		ReadOKReason: "which binaries may be run and whether approval is required — the answer to 'why was my command refused'",
	},
	{
		Key: "tools.mcp",
		Reason: "an MCP server entry names a program the gateway launches — writing it is arbitrary " +
			"code execution, and it would bypass whatever policy governs add_mcp_server",
		ReadOKReason: "the configured servers. Embedded credentials are caught by redactConfigValue's field-name redaction at any depth, so the list is readable while its secrets are not",
	},
	{
		Key: "tools.browser",
		Reason: "exec_path and cdp_url make the gateway launch or attach to a chosen binary, " +
			"profile_dir points the browser profile anywhere, and evaluate_enabled turns on " +
			"arbitrary in-page JavaScript",
		ReadOKReason: "browser paths and endpoints. Reads are open post-ADR-060, so the filesystem layout it discloses is already visible",
	},
	{
		Key:          "tools.cron.allow_command",
		Reason:       "it lets scheduled jobs run shell commands",
		ReadOKReason: "readable; the write stays blocked",
	},
	{
		Key:          "tools.web.private_host_whitelist",
		Reason:       "it is the SSRF exemption list for private/internal hosts (SEC-24)",
		ReadOKReason: "the SSRF exemption list. Readable so an agent can explain a refused fetch; the write stays blocked",
	},
	{
		Key:          "tools.web.proxy",
		Reason:       "it routes every outbound web request through a chosen proxy",
		ReadOKReason: "the egress path. Readable; the write stays blocked",
	},
	{
		Key: "tools.skills.marketplaces",
		Reason: "it is the skill supply chain — a marketplace entry decides where installable " +
			"instructions are fetched from",
		ReadOKReason: "install_skill REQUIRES a registry name as an argument, so an agent that cannot " +
			"see which registries exist cannot install anything; find_skills already surfaces the " +
			"same list, and the credential-ref redaction below covers auth_token_ref/token_ref",
	},

	// ---- reserved: no such config section exists TODAY ----
	//
	// "security" and "workspace_path" are listed in knownConfigPrefixes but
	// there is no matching field on config.Config, so a write under them is
	// currently a silent no-op that reports success (dotSet creates the key in
	// the generic map; the unmarshal back into *config.Config drops it).
	// Blocking them is behaviour-neutral today and fail-closed tomorrow: if a
	// `security` section is ever introduced, or `workspace_path` — the anchor
	// the filesystem confinement is computed from — is reintroduced, it does
	// not silently become agent-writable the moment the field lands.
	{
		Key: "security",
		Reason: "reserved — no such config section exists today, and a security section must " +
			"never be agent-writable by default if one is introduced",
		ReadOKReason: "reserved and empty. Nothing to disclose; the write stays blocked",
	},
	{
		Key: "workspace_path",
		Reason: "reserved — it names the workspace root that filesystem confinement is " +
			"anchored to, and must never be agent-writable if reintroduced",
		ReadOKReason: "the confinement root, which an agent already knows — it is its own working directory",
	},
}

// configKeySegments splits a dot-notation config key into its segments. It does
// NOT trim: " sandbox.mode" keeps its leading space and therefore matches
// nothing, which is the correct outcome — that key names no real field, so it
// must be refused by the allow list rather than quietly normalised into one
// that does.
func configKeySegments(key string) []string { return strings.Split(key, ".") }

// configSegmentMatches reports whether one key segment names the same field as
// one blocked-key segment.
//
// The comparison is case-INSENSITIVE, and that is the whole point of this
// function. The writer under this policy — dotSet, which round-trips through
// json.Unmarshal — matches struct fields case-insensitively, so a deny list
// that compares bytes is enforcing a different key space than the one that gets
// written. "gateway.Dev_Mode_Bypass" missed a byte-exact deny entry, passed the
// allow list, and landed on Gateway.DevModeBypass: unauthenticated admin access
// to the whole API, no restart needed. A comparison must match the semantics of
// the consumer it protects, not the spelling of the table it reads from.
//
// The folding is deliberately BROADER than the writer's (strings.EqualFold does
// full Unicode simple folding; encoding/json folds ASCII). That asymmetry is
// safe in this direction and only in this direction: over-matching here can
// only DENY a key that would otherwise have been allowed. The allow list below
// stays byte-exact for the mirror-image reason — under-matching there can only
// REFUSE a key, never admit one. This is the same asymmetric-folding rule the
// filesystem carve-out on this branch already follows.
//
// A trailing array index binds to the segment it indexes, so "sandbox[0]"
// matches the blocked key "sandbox".
func configSegmentMatches(seg, blockedSeg string) bool {
	if strings.EqualFold(seg, blockedSeg) {
		return true
	}
	if len(seg) > len(blockedSeg) && seg[len(blockedSeg)] == '[' &&
		strings.EqualFold(seg[:len(blockedSeg)], blockedSeg) {
		return true
	}
	return false
}

// configKeyCovers reports whether key is AT or UNDER blocked — the key itself,
// a child, a grandchild, or an index into it.
func configKeyCovers(key, blocked string) bool {
	ks, bs := configKeySegments(key), configKeySegments(blocked)
	if len(ks) < len(bs) {
		return false
	}
	for i, b := range bs {
		if !configSegmentMatches(ks[i], b) {
			return false
		}
	}
	return true
}

// configKeyIsAncestorOf reports whether key sits strictly ABOVE blocked — i.e.
// writing key rewrites the blocked subtree wholesale.
//
// This is the granularity the deny loop used to miss entirely, and it made most
// of the table decorative. dotSet writes a whole object at a path, and
// json.Unmarshal MERGES that object into the live struct, so one call with
// a bare section name and an object value reaches every blocked leaf in that
// section at once. key="gateway" with {"dev_mode_bypass": true} is the worst
// case and needs no second step: that flag is read per request, so gateway
// authentication is off for the entire API immediately, with no restart. The
// allow list even made this the documented shape — it explicitly accepts a bare
// section name (key == "gateway").
//
// The fix is structural rather than three more root entries in the table —
// adding roots by hand is how the list became inconsistent (sandbox and
// security had them; gateway, tools and agents did not). "At, under, or above"
// is a property of the key space, so a leaf entry added later cannot
// reintroduce the hole.
func configKeyIsAncestorOf(key, blocked string) bool {
	ks, bs := configKeySegments(key), configKeySegments(blocked)
	if len(ks) >= len(bs) {
		return false
	}
	for i, k := range ks {
		if !configSegmentMatches(k, bs[i]) {
			return false
		}
	}
	return true
}

// validateConfigKey returns an error if key falls at, under or above a
// security-critical or otherwise blocked key (blockedConfigKeys), or if it does
// not start with a known config prefix (knownConfigPrefixes). Deny is evaluated
// first, so an entry in blockedConfigKeys always wins over its enclosing
// allowed section.
func validateConfigKey(key string) error {
	for _, blocked := range blockedConfigKeys {
		if configKeyCovers(key, blocked.Key) {
			return fmt.Errorf("config key %q is not writable via this tool: %s", key, blocked.Reason)
		}
		if configKeyIsAncestorOf(key, blocked.Key) {
			return fmt.Errorf(
				"config key %q is not writable via this tool: writing it replaces %q, which is blocked because %s — set a specific key below %q instead",
				key, blocked.Key, blocked.Reason, key)
		}
	}
	for _, prefix := range knownConfigPrefixes {
		if strings.HasPrefix(key, prefix) || key == strings.TrimSuffix(prefix, ".") {
			return nil
		}
	}
	return fmt.Errorf("unknown config key %q — only known sections may be set via this tool", key)
}

// validateConfigReadKey returns an error if key is AT or UNDER a key the table
// marks unreadable (ReadReason). An ANCESTOR read is not refused here — it is
// served with the blocked subtrees redacted (redactConfigValue), because
// refusing it outright would make whole sections unreadable over one leaf.
func validateConfigReadKey(key string) error {
	for _, blocked := range blockedConfigKeys {
		if blocked.ReadReason == "" {
			continue
		}
		if configKeyCovers(key, blocked.Key) {
			return fmt.Errorf("config key %q is not readable via this tool: %s", key, blocked.ReadReason)
		}
	}
	return nil
}

// configReadIsBlocked reports whether a fully-qualified config path is one
// get_config must never disclose.
func configReadIsBlocked(path string) bool {
	for _, blocked := range blockedConfigKeys {
		if blocked.ReadReason != "" && configKeyCovers(path, blocked.Key) {
			return true
		}
	}
	return false
}

// redactedConfigValue is what a blocked or credential-bearing value is replaced
// with in a get_config result. It matches the audit redaction marker (SEC-16)
// so one string means one thing across the product.
const redactedConfigValue = "[REDACTED]"

// sensitiveConfigNameFragments are the field-name fragments that mark a value
// as credential material wherever it appears. They are the same four fragments
// get_config already refuses on a directly-requested key; applying them at every
// depth is what stops the ancestor read (get_config "providers", "channels")
// from returning what the direct read refuses.
var sensitiveConfigNameFragments = []string{"api_key", "secret", "token", "password"}

func isSensitiveConfigName(name string) bool {
	lower := strings.ToLower(name)
	for _, frag := range sensitiveConfigNameFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

// redactConfigValue returns value with every unreadable descendant replaced by
// redactedConfigValue. path is the fully-qualified config path value was read
// from ("" for the config root).
//
// Without this, the deny list only ever protected an exact read: get_config
// "gateway" returned the users array — bcrypt password and session-token hashes
// — along with the bearer token and dev_mode_bypass, none of which the direct
// reads of those same keys will hand over. Redacting rather than refusing keeps
// section reads useful: get_config "gateway" still reports the port and the
// host, it just cannot be used as a credential dump.
func redactConfigValue(path string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for name, sub := range typed {
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			if isSensitiveConfigName(name) || configReadIsBlocked(childPath) {
				out[name] = redactedConfigValue
				continue
			}
			out[name] = redactConfigValue(childPath, sub)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, sub := range typed {
			// Array elements share their parent's config path: the policy table
			// addresses fields, not indices.
			out[i] = redactConfigValue(path, sub)
		}
		return out
	default:
		return value
	}
}

// isRestartRequired returns true for config keys that require a restart.
//
// This list must stay consistent with the gateway's RestartGatedKeys
// (pkg/gateway/rest_pending_restart.go), which is the authoritative set the
// REST layer honors. It is duplicated here (rather than imported) because
// pkg/gateway depends on this package, so importing RestartGatedKeys back would
// be a cycle. gateway.public_url is restart-gated (ADR-044 D2 / FR-007): it
// feeds the boot-frozen CanonicalGatewayOrigin that serve_web's "no desync"
// guarantee and CSP/CORS/WS CheckOrigin all depend on, so an agent that sets it
// must be told requires_restart:true — omitting it let serve_web/CSP silently
// desync until a manual restart.
func isRestartRequired(key string) bool {
	for _, prefix := range []string{"gateway.port", "gateway.host", "gateway.bind", "gateway.public_url", "sandbox.", "security."} {
		if strings.HasPrefix(key, prefix) || key == strings.TrimSuffix(prefix, ".") {
			return true
		}
	}
	return false
}

// dotGet reads a dot-notation key from cfg by marshaling to a generic map.
func dotGet(cfg *config.Config, key string) (any, error) {
	m, err := configToMap(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return walkDot(m, strings.Split(key, "."))
}

// dotSet writes a dot-notation key into cfg by round-tripping through JSON.
// This is a safe, reflection-free approach for dynamic config writes.
func dotSet(cfg *config.Config, key string, value any) error {
	m, err := configToMap(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if setErr := setDot(m, strings.Split(key, "."), value); setErr != nil {
		return setErr
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("re-marshal config: %w", err)
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("unmarshal into config: %w", err)
	}
	return nil
}

// configToMap marshals cfg to a generic map[string]any via JSON round-trip.
func configToMap(cfg *config.Config) (map[string]any, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// walkDot traverses a nested map by path segments.
func walkDot(m map[string]any, path []string) (any, error) {
	if len(path) == 0 {
		return m, nil
	}
	v, ok := m[path[0]]
	if !ok {
		return nil, fmt.Errorf("key %q not found", path[0])
	}
	if len(path) == 1 {
		return v, nil
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("key %q is not an object", path[0])
	}
	return walkDot(sub, path[1:])
}

// setDot sets a value at the given dot-path in the nested map.
func setDot(m map[string]any, path []string, value any) error {
	if len(path) == 0 {
		return fmt.Errorf("empty path")
	}
	if len(path) == 1 {
		m[path[0]] = value
		return nil
	}
	sub, ok := m[path[0]]
	if !ok {
		// Create intermediate map.
		sub = map[string]any{}
		m[path[0]] = sub
	}
	subMap, ok := sub.(map[string]any)
	if !ok {
		return fmt.Errorf("key %q is not an object, cannot set nested key", path[0])
	}
	return setDot(subMap, path[1:], value)
}
