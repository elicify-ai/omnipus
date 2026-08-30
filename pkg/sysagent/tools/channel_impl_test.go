// Omnipus — Channel Tool Implementation Tests (§6)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These tests verify the OUTCOME of the channel enable/disable/test tools
// implemented in §6 (previously NOT_IMPLEMENTED stubs). They assert the actual
// config mutation / status report, not just that the tool returns success.

package systools_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// newTestDepsWithCredStore creates a Deps backed by a real (unlocked) credential
// store in t.TempDir(). Use this for configure_channel tests that write secrets.
func newTestDepsWithCredStore(t *testing.T) (*systools.Deps, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	credPath := filepath.Join(dir, "credentials.json")
	store := credentials.NewStore(credPath)
	// Use a deterministic 32-byte key so the test is hermetic and fast (no
	// Argon2id derivation overhead).
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("newTestDepsWithCredStore: rand.Read: %v", err)
	}
	if err := store.UnlockWithKey(key); err != nil {
		t.Fatalf("newTestDepsWithCredStore: UnlockWithKey: %v", err)
	}

	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:             dir,
		ConfigPath:       filepath.Join(dir, "config.json"),
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        store,
	}
	return deps, cfg
}

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
	message, ok := out["message"].(string)
	if !ok {
		t.Fatalf("out[\"message\"] has unexpected type %T, want string", out["message"])
	}
	if !strings.Contains(message, "not configured") {
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
	message, ok := out["message"].(string)
	if !ok {
		t.Fatalf("out[\"message\"] has unexpected type %T, want string", out["message"])
	}
	if !strings.Contains(message, "credentials") {
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

// TestConfigure_SetsTokenRef is the end-to-end test that was missing and masked
// the bug: configure_channel must (a) store the secret in the credential store
// under the canonical key and (b) write the token_ref field on the channel's
// ChannelInstanceConfig so that subsequent test_channel returns success=true
// (i.e. hasCreds = ch.TokenRef != "").
//
// Before the fix, configure_channel stored the credential under the wrong key
// ("channel.telegram.token") and never wrote TokenRef, so test_channel always
// returned success=false with "no credentials" despite configure_channel
// returning {"status":"connected"}.
func TestConfigure_SetsTokenRef(t *testing.T) {
	deps, cfg := newTestDepsWithCredStore(t)
	if cfg.Channels == nil {
		cfg.Channels = map[string]config.ChannelInstanceConfig{}
	}
	// First enable the channel (configure_channel doesn't require it, but
	// test_channel reports enabled status — keep the flow realistic).
	enableRes := systools.NewChannelEnableTool(deps).Execute(
		context.Background(), map[string]any{"id": "telegram"},
	)
	if enableRes.IsError {
		t.Fatalf("enable_channel: unexpected error: %s", enableRes.ForLLM)
	}

	// Call configure_channel with a token secret.
	configureRes := systools.NewChannelConfigureTool(deps).Execute(
		context.Background(), map[string]any{
			"id":    "telegram",
			"token": "bot123:AABBCCDD",
		},
	)
	if configureRes.IsError {
		t.Fatalf("configure_channel: unexpected error: %s", configureRes.ForLLM)
	}

	// (a) The config's TokenRef must be set to the canonical credential key.
	// The expected key mirrors the gateway's channelCredKey: "channel_<id>_<field>".
	const wantRef = "channel_telegram_token"
	ch, ok := cfg.Channels["telegram"]
	if !ok {
		t.Fatalf("telegram channel missing from config after configure_channel")
	}
	if ch.TokenRef != wantRef {
		t.Errorf("TokenRef = %q; want %q (configure_channel did not write the ref)", ch.TokenRef, wantRef)
	}

	// (b) A subsequent test_channel must return success=true (hasCreds satisfied
	// by the TokenRef we just wrote). This is the consumer that was broken.
	testRes := systools.NewChannelTestTool(deps).Execute(
		context.Background(), map[string]any{"id": "telegram"},
	)
	if testRes.IsError {
		t.Fatalf("test_channel: unexpected hard error: %s", testRes.ForLLM)
	}
	out := parseToolJSON(t, testRes.ForLLM)
	if out["success"] != true {
		t.Errorf("test_channel after configure: success = %v; want true (msg: %v)",
			out["success"], out["message"])
	}
}

// TestConfigure_AppSecretRef verifies the app_secret → AppSecretRef path (feishu/qq).
func TestConfigure_AppSecretRef(t *testing.T) {
	deps, cfg := newTestDepsWithCredStore(t)
	if cfg.Channels == nil {
		cfg.Channels = map[string]config.ChannelInstanceConfig{}
	}
	// feishu uses app_secret (→ AppSecretRef) not token.
	res := systools.NewChannelConfigureTool(deps).Execute(
		context.Background(), map[string]any{
			"id":         "telegram", // tool accepts app_secret for any id
			"app_secret": "supersecret",
		},
	)
	if res.IsError {
		t.Fatalf("configure_channel: unexpected error: %s", res.ForLLM)
	}
	ch := cfg.Channels["telegram"]
	const wantRef = "channel_telegram_app_secret"
	if ch.AppSecretRef != wantRef {
		t.Errorf("AppSecretRef = %q; want %q", ch.AppSecretRef, wantRef)
	}
}

// TestConfigure_PlainFields verifies non-secret fields (bot_id, app_id, mode)
// are written directly into the ChannelInstanceConfig without going through the
// credential store.
func TestConfigure_PlainFields(t *testing.T) {
	deps, cfg := newTestDepsWithCredStore(t)
	if cfg.Channels == nil {
		cfg.Channels = map[string]config.ChannelInstanceConfig{}
	}
	res := systools.NewChannelConfigureTool(deps).Execute(
		context.Background(), map[string]any{
			"id":     "telegram",
			"bot_id": "my-bot",
			"app_id": "12345",
			"mode":   "webhook",
		},
	)
	if res.IsError {
		t.Fatalf("configure_channel: unexpected error: %s", res.ForLLM)
	}
	ch := cfg.Channels["telegram"]
	if ch.BotID != "my-bot" {
		t.Errorf("BotID = %q; want \"my-bot\"", ch.BotID)
	}
	if ch.AppID != "12345" {
		t.Errorf("AppID = %q; want \"12345\"", ch.AppID)
	}
	if ch.Mode != "webhook" {
		t.Errorf("Mode = %q; want \"webhook\"", ch.Mode)
	}
}

// TestChannelTest_AppSecretRefCountsAsCredentials is the regression test:
// hasCreds in ChannelTestTool.Execute used to check only TokenRef and
// Identity, missing AppSecretRef entirely. Feishu, WeCom, DingTalk and Weixin
// authenticate via app_id + app_secret (AppSecretRef), not a token, so a
// channel configured that way was reported as "has no credentials" even
// though configure_channel had successfully stored and referenced the
// app_secret. This test configures a channel with only AppSecretRef set (no
// TokenRef, no Identity) and asserts test_channel now reports success=true.
func TestChannelTest_AppSecretRefCountsAsCredentials(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"feishu": {Type: "feishu", Enabled: true, AppSecretRef: "channel_feishu_app_secret"},
	}
	tool := systools.NewChannelTestTool(deps)

	res := tool.Execute(context.Background(), map[string]any{"id": "feishu"})
	if res.IsError {
		t.Fatalf("test_channel returned a hard error: %s", res.ForLLM)
	}
	out := parseToolJSON(t, res.ForLLM)
	if out["success"] != true {
		t.Errorf("test_channel with only AppSecretRef set: success = %v; want true (msg: %v)",
			out["success"], out["message"])
	}
}

// TestChannelList_ReportsLiveState is the regression test:
// ChannelListTool.Execute used to return the package-level knownChannels
// slice verbatim, so every channel always reported enabled=false and
// status="" regardless of what was actually in config.json — including a
// channel the caller had just enabled and configured. This test enables and
// configures one channel, leaves another with credentials but disabled, and
// leaves a third entirely untouched, then asserts list_channels reports the
// live state for each.
func TestChannelList_ReportsLiveState(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true, TokenRef: "channel_telegram_token"},
		"discord":  {Type: "discord", Enabled: false},
		// "slack" intentionally absent from cfg.Channels entirely.
	}
	tool := systools.NewChannelListTool(deps)

	res := tool.Execute(context.Background(), nil)
	if res.IsError {
		t.Fatalf("list_channels returned an error: %s", res.ForLLM)
	}
	out := parseToolJSON(t, res.ForLLM)
	rawChannels, ok := out["channels"].([]any)
	if !ok {
		t.Fatalf("list_channels response missing \"channels\" array: %s", res.ForLLM)
	}
	byID := make(map[string]map[string]any, len(rawChannels))
	for _, rc := range rawChannels {
		ch, ok := rc.(map[string]any)
		if !ok {
			t.Fatalf("list_channels entry has unexpected shape: %#v", rc)
		}
		id, _ := ch["id"].(string)
		byID[id] = ch
	}

	telegram, ok := byID["telegram"]
	if !ok {
		t.Fatalf("telegram missing from list_channels output")
	}
	if telegram["enabled"] != true {
		t.Errorf("telegram enabled = %v; want true (configured and enabled in cfg.Channels)", telegram["enabled"])
	}
	if telegram["status"] != "configured" {
		t.Errorf("telegram status = %v; want \"configured\"", telegram["status"])
	}

	discord, ok := byID["discord"]
	if !ok {
		t.Fatalf("discord missing from list_channels output")
	}
	if discord["enabled"] != false {
		t.Errorf("discord enabled = %v; want false", discord["enabled"])
	}
	if discord["status"] != "needs_credentials" {
		t.Errorf("discord status = %v; want \"needs_credentials\" (no TokenRef/AppSecretRef/Identity)", discord["status"])
	}

	slack, ok := byID["slack"]
	if !ok {
		t.Fatalf("slack missing from list_channels output")
	}
	if slack["enabled"] != false {
		t.Errorf("slack enabled = %v; want false (never configured)", slack["enabled"])
	}
	if slack["status"] != "not_configured" {
		t.Errorf("slack status = %v; want \"not_configured\"", slack["status"])
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
