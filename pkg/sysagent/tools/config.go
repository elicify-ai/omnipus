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
	// Block reading sensitive keys.
	lower := strings.ToLower(key)
	if strings.Contains(lower, "api_key") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "password") {
		return tools.ErrorResult(errorJSON("FORBIDDEN",
			"Credentials cannot be read via config.get — they are write-only",
			"Use list_providers to see configured providers (without keys)",
		))
	}
	value, err := dotGet(t.deps.GetCfg(), key)
	if err != nil {
		return tools.ErrorResult(errorJSON("KEY_NOT_FOUND",
			fmt.Sprintf("Config key %q not found: %v", key, err),
			"Check the key name and try again",
		))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"key":    key,
		"value":  value,
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
	lower := strings.ToLower(key)
	if strings.Contains(lower, "api_key") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "password") {
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
	// when it equals Key, or begins with Key+"." (a child) or Key+"[" (an
	// index into an array-valued key).
	Key string
	// Reason is surfaced verbatim in the rejection so the caller — usually an
	// LLM — is told what it hit and where the setting really lives, instead of
	// retrying variations of the same key.
	Reason string
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
	},

	// ---- gateway authentication, origin and approval controls ----
	{
		Key:    "gateway.dev_mode_bypass",
		Reason: "it disables gateway authentication entirely, granting unauthenticated admin access to the whole API",
	},
	{
		Key:    "gateway.users",
		Reason: "it is the bearer-token account list — writing it mints an API credential",
	},
	{
		// Also caught by the credential substring filter above; listed so this
		// predicate is correct standalone if that filter is ever narrowed.
		Key:    "gateway.token",
		Reason: "it is the gateway bearer token — credentials live in the encrypted store",
	},
	{
		Key:    "gateway.cli_token",
		Reason: "it is the CLI bearer token — credentials live in the encrypted store",
	},
	{
		Key: "gateway.public_url",
		Reason: "it defines the canonical origin that CSP, CORS and the WebSocket CheckOrigin " +
			"all validate against (ADR-044 D2)",
	},
	{
		Key: "gateway.trust_xff",
		Reason: "it makes the client IP attacker-controlled, which is what auth rate limiting " +
			"and audit attribution key on",
	},
	{
		Key:    "gateway.validate_inbound",
		Reason: "it is the inbound contract validator (Constraint #8)",
	},
	{
		Key:    "gateway.auth_mismatch_log_level",
		Reason: "it controls whether failed authentication attempts are logged at all",
	},
	{
		Key:    "gateway.tool_approval_timeout",
		Reason: "it governs the human approval gate that every ask-policy tool call passes through",
	},
	{
		Key:    "gateway.tool_approval_max_pending",
		Reason: "it governs the human approval gate that every ask-policy tool call passes through",
	},

	// ---- tool-surface boundary controls ----
	{
		Key:    "tools.allow_read_paths",
		Reason: "it is the filesystem read boundary for every file tool",
	},
	{
		Key:    "tools.allow_write_paths",
		Reason: "it is the filesystem write boundary for every file tool",
	},
	{
		Key: "tools.filter_sensitive_data",
		Reason: "it is the filter that strips API keys, tokens and secrets out of tool results " +
			"before they reach the model",
	},
	{
		Key: "tools.filter_min_length",
		Reason: "it tunes that same secret-redaction filter — a large value switches redaction off " +
			"without appearing to disable anything",
	},
	{
		Key: "tools.exec",
		Reason: "it holds the exec binary allow-list, the exec approval mode and the egress proxy " +
			"toggle for spawned processes",
	},
	{
		Key: "tools.mcp",
		Reason: "an MCP server entry names a program the gateway launches — writing it is arbitrary " +
			"code execution, and it would bypass whatever policy governs add_mcp_server",
	},
	{
		Key: "tools.browser",
		Reason: "exec_path and cdp_url make the gateway launch or attach to a chosen binary, " +
			"profile_dir points the browser profile anywhere, and evaluate_enabled turns on " +
			"arbitrary in-page JavaScript",
	},
	{
		Key:    "tools.cron.allow_command",
		Reason: "it lets scheduled jobs run shell commands",
	},
	{
		Key:    "tools.web.private_host_whitelist",
		Reason: "it is the SSRF exemption list for private/internal hosts (SEC-24)",
	},
	{
		Key:    "tools.web.proxy",
		Reason: "it routes every outbound web request through a chosen proxy",
	},
	{
		Key: "tools.skills.marketplaces",
		Reason: "it is the skill supply chain — a marketplace entry decides where installable " +
			"instructions are fetched from",
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
	},
	{
		Key: "workspace_path",
		Reason: "reserved — it names the workspace root that filesystem confinement is " +
			"anchored to, and must never be agent-writable if reintroduced",
	},
}

// validateConfigKey returns an error if key falls under a security-critical or
// otherwise blocked key (blockedConfigKeys), or if it does not start with a
// known config prefix (knownConfigPrefixes). Deny is evaluated first, so an
// entry in blockedConfigKeys always wins over its enclosing allowed section.
func validateConfigKey(key string) error {
	for _, blocked := range blockedConfigKeys {
		if key == blocked.Key ||
			strings.HasPrefix(key, blocked.Key+".") ||
			strings.HasPrefix(key, blocked.Key+"[") {
			return fmt.Errorf("config key %q is not writable via this tool: %s", key, blocked.Reason)
		}
	}
	for _, prefix := range knownConfigPrefixes {
		if strings.HasPrefix(key, prefix) || key == strings.TrimSuffix(prefix, ".") {
			return nil
		}
	}
	return fmt.Errorf("unknown config key %q — only known sections may be set via this tool", key)
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
