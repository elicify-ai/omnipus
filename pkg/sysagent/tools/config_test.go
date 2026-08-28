// Omnipus — System Agent Tool Tests: Config
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"testing"

	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ---- get_config ----

// TestConfigGet_ValidKey verifies that get_config reads a known gateway key correctly.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigGet_ValidKey(t *testing.T) {
	deps, cfg := newTestDeps()
	// Set a known value in the in-memory config to verify the tool reads it.
	cfg.Gateway.Port = 4242
	tool := systools.NewConfigGetTool(deps)

	result := tool.Execute(context.Background(), map[string]any{"key": "gateway.port"})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	if m["key"] != "gateway.port" {
		t.Errorf("key = %v, want 'gateway.port'", m["key"])
	}
	// JSON numbers decode to float64.
	if m["value"] != float64(4242) {
		t.Errorf("value = %v (%T), want 4242", m["value"], m["value"])
	}
	if m["source"] != "config" {
		t.Errorf("source = %v, want 'config'", m["source"])
	}
}

// TestConfigGet_Differentiation verifies that two different keys produce different key names
// and values in the response. This proves the tool is not hardcoding responses.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigGet_Differentiation(t *testing.T) {
	deps, cfg := newTestDeps()
	// Use two gateway fields with distinct types — port (int) and host (string).
	cfg.Gateway.Port = 5000
	cfg.Gateway.Host = "127.0.0.1"
	tool := systools.NewConfigGetTool(deps)

	r1 := tool.Execute(context.Background(), map[string]any{"key": "gateway.port"})
	if r1.IsError {
		t.Fatalf("get gateway.port failed: %s", r1.ForLLM)
	}
	m1 := parseSuccess(t, r1.ForLLM)

	r2 := tool.Execute(context.Background(), map[string]any{"key": "gateway.host"})
	if r2.IsError {
		t.Fatalf("get gateway.host failed: %s", r2.ForLLM)
	}
	m2 := parseSuccess(t, r2.ForLLM)

	if m1["key"] == m2["key"] {
		t.Error("both keys returned the same key name — differentiation test failed")
	}
	// gateway.port is float64(5000) and gateway.host is "127.0.0.1" — these are always different.
	if m1["value"] == m2["value"] {
		t.Error("both keys returned the same value — differentiation test failed")
	}
}

// TestConfigGet_SensitiveKeyBlocked verifies that keys containing api_key, secret,
// token, or password are blocked with FORBIDDEN.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigGet_SensitiveKeyBlocked(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewConfigGetTool(deps)
	ctx := context.Background()

	sensitiveKeys := []string{
		"providers.api_key",
		"channels.discord.token",
		"channels.slack.secret",
		"gateaway.admin_password",
	}
	for _, key := range sensitiveKeys {
		t.Run(key, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{"key": key})
			if !result.IsError {
				t.Fatalf("expected FORBIDDEN for key %q, got success: %s", key, result.ForLLM)
			}
			m := parseError(t, result.ForLLM)
			errBlock, _ := m["error"].(map[string]any)
			if errBlock["code"] != "FORBIDDEN" {
				t.Errorf("key %q: code = %v, want FORBIDDEN", key, errBlock["code"])
			}
		})
	}
}

// TestConfigGet_UnknownKey verifies that an unknown/nonexistent key returns KEY_NOT_FOUND.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigGet_UnknownKey(t *testing.T) {
	deps, _ := newTestDeps()
	result := systools.NewConfigGetTool(deps).Execute(context.Background(), map[string]any{
		"key": "gateway.nonexistent_field_zzz",
	})
	if !result.IsError {
		t.Fatalf("expected error for unknown key, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "KEY_NOT_FOUND" {
		t.Errorf("code = %v, want KEY_NOT_FOUND", errBlock["code"])
	}
}

// TestConfigGet_EmptyKey verifies that an empty key is rejected with INVALID_INPUT.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigGet_EmptyKey(t *testing.T) {
	deps, _ := newTestDeps()
	result := systools.NewConfigGetTool(deps).Execute(context.Background(), map[string]any{"key": ""})
	if !result.IsError {
		t.Fatalf("expected error for empty key, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_INPUT" {
		t.Errorf("code = %v, want INVALID_INPUT", errBlock["code"])
	}
}

// ---- set_config ----

// TestConfigSet_NormalSet verifies set_config writes a value that is then readable by get_config.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigSet_NormalSet(t *testing.T) {
	deps, cfg := newTestDeps()
	ctx := context.Background()

	// gateway.port is a known int field.
	result := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
		"key":   "gateway.port",
		"value": float64(7777),
	})
	if result.IsError {
		t.Fatalf("set failed: %s", result.ForLLM)
	}

	m := parseSuccess(t, result.ForLLM)
	if m["key"] != "gateway.port" {
		t.Errorf("key = %v, want 'gateway.port'", m["key"])
	}
	if m["value"] != float64(7777) {
		t.Errorf("value = %v, want 7777", m["value"])
	}

	// Verify the in-memory config was updated.
	if cfg.Gateway.Port != 7777 {
		t.Errorf("cfg.Gateway.Port = %d, want 7777", cfg.Gateway.Port)
	}

	// Round-trip: read back via get_config.
	getResult := systools.NewConfigGetTool(deps).Execute(ctx, map[string]any{"key": "gateway.port"})
	if getResult.IsError {
		t.Fatalf("get after set failed: %s", getResult.ForLLM)
	}
	gm := parseSuccess(t, getResult.ForLLM)
	if gm["value"] != float64(7777) {
		t.Errorf("read-back value = %v, want 7777", gm["value"])
	}
}

// TestConfigSet_RequiresRestartFlag verifies that keys known to require restart set the flag.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigSet_RequiresRestartFlag(t *testing.T) {
	deps, _ := newTestDeps()

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "gateway.port",
		"value": float64(9999),
	})
	if result.IsError {
		t.Fatalf("set failed: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	if m["requires_restart"] != true {
		t.Errorf("requires_restart = %v, want true for gateway.port", m["requires_restart"])
	}
}

// TestConfigSet_NoRestartForNonRestartKey verifies that non-restart keys do not
// set requires_restart=true.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
// The key under test used to be "heartbeat.enabled". It was a bad choice, and
// the reason is the defect this file now pins elsewhere: there is no Heartbeat
// field on config.Config at all — "heartbeat." is listed in
// knownConfigPrefixes but names no section — so that write was dropped by
// json.Unmarshal and set_config reported success anyway. The test passed
// because it only ever looked at requires_restart. devices.enabled is a real
// bool field (config.DevicesConfig.Enabled) that is likewise not restart-gated,
// so it tests requires_restart against a write that actually happens.
// See TestConfigSet_PhantomSectionRejected for the heartbeat case itself.
func TestConfigSet_NoRestartForNonRestartKey(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Devices.Enabled = false

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "devices.enabled",
		"value": true,
	})
	if result.IsError {
		t.Fatalf("set devices.enabled failed: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	if m["requires_restart"] == true {
		t.Errorf("requires_restart = true for devices.enabled, expected false")
	}
	if !cfg.Devices.Enabled {
		t.Errorf("cfg.Devices.Enabled = false — the write this test reports on did not happen")
	}
}

// TestConfigSet_SensitiveKeyRejected verifies that credential-like keys are blocked.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigSet_SensitiveKeyRejected(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewConfigSetTool(deps)
	ctx := context.Background()

	sensitiveKeys := []string{
		"providers.api_key",
		"channels.discord.token",
		"channels.slack.secret",
		"gateway.admin_password",
	}
	for _, key := range sensitiveKeys {
		t.Run(key, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]any{"key": key, "value": "should-not-work"})
			if !result.IsError {
				t.Fatalf("expected FORBIDDEN for key %q, got success: %s", key, result.ForLLM)
			}
			m := parseError(t, result.ForLLM)
			errBlock, _ := m["error"].(map[string]any)
			if errBlock["code"] != "FORBIDDEN" {
				t.Errorf("key %q: code = %v, want FORBIDDEN", key, errBlock["code"])
			}
		})
	}
}

// TestConfigSet_UnknownKeyRejected verifies that keys outside the known sections
// are rejected with INVALID_KEY.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigSet_UnknownKeyRejected(t *testing.T) {
	deps, _ := newTestDeps()
	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "totally_unknown_section.field",
		"value": "anything",
	})
	if !result.IsError {
		t.Fatalf("expected error for unknown key, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_KEY" {
		t.Errorf("code = %v, want INVALID_KEY", errBlock["code"])
	}
}

// TestConfigSet_AgentsListRejected pins ADR-054 §11 checklist item 6:
// agents.list is the retired agent-roster entity blob and must be rejected by
// set_config even though "agents." is a known prefix — agent CRUD now goes
// exclusively through create_agent/update_agent/delete_agent.
//
// Traces to: ADR-054 §11 checklist item 6.
func TestConfigSet_AgentsListRejected(t *testing.T) {
	deps, _ := newTestDeps()
	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "agents.list",
		"value": []any{},
	})
	if !result.IsError {
		t.Fatalf("expected error for agents.list, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_KEY" {
		t.Errorf("code = %v, want INVALID_KEY", errBlock["code"])
	}
}

// TestConfigSet_AgentsDefaultsStillWorks is the regression a reviewer caught
// in ADR-054 v2: rejecting agents.list must NOT collaterally reject
// agents.defaults.* — agents.defaults is a SETTING (D1) and stays fully
// writable via set_config.
//
// Traces to: ADR-054 §11 checklist item 6 / v2 review finding C-3.
func TestConfigSet_AgentsDefaultsStillWorks(t *testing.T) {
	deps, cfg := newTestDeps()
	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "agents.defaults.default_model.model",
		"value": "glm-4.7",
	})
	if result.IsError {
		t.Fatalf("set agents.defaults.default_model.model failed: %s", result.ForLLM)
	}
	if cfg.Agents.Defaults.DefaultModel.Model != "glm-4.7" {
		t.Errorf("cfg.Agents.Defaults.DefaultModel.Model = %q, want %q", cfg.Agents.Defaults.DefaultModel.Model, "glm-4.7")
	}
}

// TestConfigSet_PreviousValueReturned verifies that set_config returns the old value
// in previous_value.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigSet_PreviousValueReturned(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Gateway.Port = 1234

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "gateway.port",
		"value": float64(5678),
	})
	if result.IsError {
		t.Fatalf("set failed: %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	// previous_value should be the old port.
	if m["previous_value"] != float64(1234) {
		t.Errorf("previous_value = %v, want 1234", m["previous_value"])
	}
}

// TestConfigSet_EmptyKeyRejected verifies empty key is rejected with INVALID_INPUT.
//
// Traces to: docs/internal/specs/tool-test-plan-2026-06.md §3.13
func TestConfigSet_EmptyKeyRejected(t *testing.T) {
	deps, _ := newTestDeps()
	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "",
		"value": "x",
	})
	if !result.IsError {
		t.Fatalf("expected error for empty key, got success: %s", result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_INPUT" {
		t.Errorf("code = %v, want INVALID_INPUT", errBlock["code"])
	}
}
