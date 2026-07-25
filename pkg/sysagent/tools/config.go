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

// blockedConfigKeyPrefixes are keys within an otherwise-known section that
// must still be rejected by system.config.set. ADR-054 §11 checklist item 6:
// agents.list (the retired agent-roster entity blob) must be rejected
// specifically — NOT by removing the "agents." prefix from
// knownConfigPrefixes, which gates the whole agents.* namespace and would
// also break agents.defaults.* (including agents.defaults.default_agent_id,
// D6.4). Agent CRUD now goes exclusively through the agent store / dedicated
// create_agent, update_agent, delete_agent tools and the /api/v1/agents REST
// endpoints, never through generic config mutation.
var blockedConfigKeyPrefixes = []string{
	"agents.list",
}

// validateConfigKey returns an error if key does not start with a known config
// prefix, or if it falls under a key that is explicitly blocked even within a
// known section (see blockedConfigKeyPrefixes).
func validateConfigKey(key string) error {
	for _, blocked := range blockedConfigKeyPrefixes {
		if key == blocked || strings.HasPrefix(key, blocked+".") || strings.HasPrefix(key, blocked+"[") {
			return fmt.Errorf(
				"config key %q is managed by the agent store, not generic config — use create_agent/update_agent/delete_agent instead",
				key,
			)
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
