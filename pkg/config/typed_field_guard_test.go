// typed_field_guard_test.go — compile-time-style guard against re-introducing
// typed channel fields on the Config struct (Spec-2 SC-4 / TDD #8).
//
// Background: the channel configuration was migrated from a typed struct
// (`channelsConfigV0` with fields `Telegram telegramConfigV0`, `Discord ...`,
// etc., now used only for v0 migration in config_old.go) to a single
// `Channels map[string]ChannelInstanceConfig` map on Config. This test enforces
// that no typed channel field (a field named after a channel type and/or of a
// per-channel typed-config struct type) is re-introduced on the current Config
// struct — which would silently bypass the map-based channel registry and the
// credential-store routing that depends on it.
//
// We use reflection rather than a go/types AST scan so the guard runs as a
// normal `go test` without pulling in golang.org/x/tools as a direct dep. The
// reflect scan is over the Config struct type, which is exactly what the
// gateway loads and the SPA reads — if a typed field sneaks back onto Config,
// this test fails.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"reflect"
	"testing"
)

// channelTypeNames is the set of channel-type identifiers that, under the old
// typed schema, each had their own field on the channels struct. None of these
// may appear as a field name on the current Config struct.
var channelTypeNames = []string{
	"Telegram",
	"Discord",
	"Slack",
	"WhatsApp",
	"Matrix",
	"IRC",
	"Feishu",
	"Weixin",
	"QQ",
	"DingTalk",
	"LINE",
	"WeCom",
	"Email",
	"GoogleChat",
	"Signal",
}

// perChannelTypedConfigTypes lists the per-channel typed-config struct types
// that exist as standalone converters (InstanceToTelegram etc.). None of these
// may be the type of a field on the Config struct — that would mean a typed
// channel field was re-added.
func perChannelTypedConfigTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(TelegramConfig{}),
		reflect.TypeOf(DiscordConfig{}),
		reflect.TypeOf(SlackConfig{}),
		reflect.TypeOf(WhatsAppConfig{}),
		reflect.TypeOf(FeishuConfig{}),
		reflect.TypeOf(EmailConfig{}),
	}
}

// TestConfig_ChannelsFieldIsMap asserts that the Config struct's Channels field
// is exactly `map[string]ChannelInstanceConfig` — the map-based channel
// registry. A regression that changes it back to a typed struct would fail
// here.
func TestConfig_ChannelsFieldIsMap(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	field, ok := cfgType.FieldByName("Channels")
	if !ok {
		t.Fatal("Config struct must have a field named Channels — it was removed (regression)")
	}

	expected := reflect.TypeOf(map[string]ChannelInstanceConfig{})
	if field.Type != expected {
		t.Fatalf("Config.Channels must be %s, got %s (typed channel struct re-introduced?)",
			expected, field.Type)
	}
}

// TestConfig_HasNoTypedChannelFields asserts that no field on the Config struct
// is named after a channel type (Telegram, Discord, Slack, …) — the hallmarks
// of the old typed schema. This prevents a silent re-introduction of typed
// channel config alongside the map.
func TestConfig_HasNoTypedChannelFields(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	names := make(map[string]struct{}, cfgType.NumField())
	for i := 0; i < cfgType.NumField(); i++ {
		names[cfgType.Field(i).Name] = struct{}{}
	}

	for _, ch := range channelTypeNames {
		if _, found := names[ch]; found {
			t.Errorf(
				"Config must not have a typed channel field %q — channels are map-keyed via Config.Channels (SC-4)",
				ch,
			)
		}
	}
}

// TestConfig_HasNoPerChannelTypedConfigFieldType asserts that no field on the
// Config struct has a type of any per-channel typed-config struct
// (TelegramConfig, DiscordConfig, …). Even if a field were renamed, its type
// would still betray a typed channel field.
func TestConfig_HasNoPerChannelTypedConfigFieldType(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	forbidden := make(map[reflect.Type]string, len(perChannelTypedConfigTypes()))
	for _, ty := range perChannelTypedConfigTypes() {
		forbidden[ty] = ty.Name()
	}

	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		if name, bad := forbidden[f.Type]; bad {
			t.Errorf(
				"Config field %q has per-channel typed-config type %s — channels must be map-keyed (SC-4)",
				f.Name,
				name,
			)
		}
	}
}

// TestConfig_HasNoChannelsConfigV0Field asserts that no field on the Config
// struct has the v0 typed channels struct type (channelsConfigV0). That struct
// is allowed to live in config_old.go for migration only — it must never appear
// as a field on the current Config.
func TestConfig_HasNoChannelsConfigV0Field(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	v0Type := reflect.TypeOf(channelsConfigV0{})

	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		if f.Type == v0Type {
			t.Errorf(
				"Config field %q is of type channelsConfigV0 — the v0 typed channels struct must not be re-introduced on Config (SC-4)",
				f.Name,
			)
		}
	}
}

// TestChannelsMapIsOnlyChannelRegistry is a meta-assertion: the Config struct
// has exactly one field whose type is a map with ChannelInstanceConfig value
// type — the Channels registry. If a second such map appeared, it would
// indicate a parallel typed-keyed registry being smuggled in.
func TestChannelsMapIsOnlyChannelRegistry(t *testing.T) {
	cfgType := reflect.TypeOf(Config{})
	instanceType := reflect.TypeOf(ChannelInstanceConfig{})
	count := 0
	for i := 0; i < cfgType.NumField(); i++ {
		f := cfgType.Field(i)
		if f.Type.Kind() == reflect.Map &&
			f.Type.Elem() == instanceType {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("Config must have exactly one map[string]...ChannelInstanceConfig field (Channels); found %d", count)
	}
}
