// Omnipus — Channel Tool Implementation Tests (§6)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests verify the OUTCOME of the channel enable/disable/test tools
// implemented in §6 (previously NOT_IMPLEMENTED stubs). They assert the actual
// config mutation / status report, not just that the tool returns success.

package systools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
)

// parseToolJSON extracts the JSON object from a tool's ForLLM result.
func parseToolJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("tool result is not JSON: %q (%v)", s, err)
	}
	return m
}

func TestChannelEnable_MutatesConfigToEnabled(t *testing.T) {
	deps, cfg := newTestDeps()
	if cfg.Channels == nil {
		cfg.Channels = map[string]config.ChannelInstanceConfig{}
	}
	tool := systools.NewChannelEnableTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"id": "telegram"})
	if res.IsError {
		t.Fatalf("enable returned error: %s", res.ForLLM)
	}
	out := parseToolJSON(t, res.ForLLM)
	if out["enabled"] != true {
		t.Errorf("result enabled = %v; want true", out["enabled"])
	}
	// OUTCOME: the config must actually reflect enabled=true.
	ch, ok := cfg.Channels["telegram"]
	if !ok {
		t.Fatalf("telegram channel not in config after enable")
	}
	if !ch.Enabled {
		t.Errorf("config telegram.Enabled = false; want true (the mutation did not persist)")
	}
	if ch.Type != "telegram" {
		t.Errorf("config telegram.Type = %q; want telegram", ch.Type)
	}
}

func TestChannelDisable_MutatesConfigToDisabled(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true},
	}
	tool := systools.NewChannelDisableTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"id": "telegram"})
	if res.IsError {
		t.Fatalf("disable returned error: %s", res.ForLLM)
	}
	out := parseToolJSON(t, res.ForLLM)
	if out["enabled"] != false {
		t.Errorf("result enabled = %v; want false", out["enabled"])
	}
	// OUTCOME: the config must actually reflect enabled=false.
	if cfg.Channels["telegram"].Enabled {
		t.Errorf("config telegram.Enabled = true; want false (disable did not persist)")
	}
}

func TestChannelDisable_RejectsUnconfigured(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{}
	tool := systools.NewChannelDisableTool(deps)

	// telegram is a KNOWN channel but NOT configured → disable should error.
	res := tool.Execute(context.Background(), map[string]any{"id": "telegram"})
	if !res.IsError {
		t.Errorf("disable of an unconfigured channel should error; got success: %s", res.ForLLM)
	}
}

func TestChannelEnable_RejectsUnknownChannel(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewChannelEnableTool(deps)
	res := tool.Execute(context.Background(), map[string]any{"id": "nonexistent-channel"})
	if !res.IsError {
		t.Errorf("enable of an unknown channel should error; got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "CHANNEL_NOT_FOUND") {
		t.Errorf("expected CHANNEL_NOT_FOUND; got %s", res.ForLLM)
	}
}

func TestChannelEnable_RejectsMissingID(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewChannelEnableTool(deps)
	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Errorf("enable without id should error; got: %s", res.ForLLM)
	}
}

func TestChannelTest_ReportsNotConfigured(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{}
	tool := systools.NewChannelTestTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"id": "telegram"})
	if res.IsError {
		t.Fatalf("test returned a hard error: %s", res.ForLLM)
	}
	out := parseToolJSON(t, res.ForLLM)
	// OUTCOME: an unconfigured channel reports success=false.
	if out["success"] != false {
		t.Errorf("test of unconfigured channel: success = %v; want false", out["success"])
	}
	if !strings.Contains(out["message"].(string), "not configured") {
		t.Errorf("message should say 'not configured'; got %q", out["message"])
	}
}

func TestChannelTest_ReportsNoCredentials(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true}, // no TokenRef, no Identity
	}
	tool := systools.NewChannelTestTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"id": "telegram"})
	out := parseToolJSON(t, res.ForLLM)
	// OUTCOME: configured-but-no-credentials reports success=false with a credentials hint.
	if out["success"] != false {
		t.Errorf("test of credential-less channel: success = %v; want false", out["success"])
	}
	if !strings.Contains(out["message"].(string), "credentials") {
		t.Errorf("message should mention credentials; got %q", out["message"])
	}
}

func TestChannelTest_ReportsConfigured(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true, TokenRef: "channel.telegram.token"},
	}
	tool := systools.NewChannelTestTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"id": "telegram"})
	out := parseToolJSON(t, res.ForLLM)
	// OUTCOME: a configured channel with credentials reports success=true.
	if out["success"] != true {
		t.Errorf("test of configured channel: success = %v; want true (msg: %v)", out["success"], out["message"])
	}
	if out["enabled"] != true {
		t.Errorf("test should report enabled=true; got %v", out["enabled"])
	}
}

// TestChannelEnableDisableRoundTrip verifies enable→disable→enable cycles persist correctly.
func TestChannelEnableDisableRoundTrip(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{}
	enable := systools.NewChannelEnableTool(deps)
	disable := systools.NewChannelDisableTool(deps)
	ctx := context.Background()

	enable.Execute(ctx, map[string]any{"id": "discord"})
	if !cfg.Channels["discord"].Enabled {
		t.Fatalf("after enable: discord not enabled")
	}
	disable.Execute(ctx, map[string]any{"id": "discord"})
	if cfg.Channels["discord"].Enabled {
		t.Fatalf("after disable: discord still enabled")
	}
	enable.Execute(ctx, map[string]any{"id": "discord"})
	if !cfg.Channels["discord"].Enabled {
		t.Fatalf("after re-enable: discord not enabled")
	}
}
