// Omnipus — System Agent Tool Tests: the posting list is not agent-writable.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// T-20 — set_config refuses channels.*.outbound (channels-spec-r8, T-20;
// FR-024; scenario S-33; US-4 AS3). Same black-box shape as its sibling
// config_channel_ownership_test.go (FR-024 landed the deny entry in WP-0;
// these rows pin it): every spelling of "an agent edits who this connection
// may start messages to" is refused by the real ConfigSetTool and the config
// is byte-identical afterwards.
//
// S-33's two cases and their spellings:
//   - the leaf path, bare and namespaced, with subkeys and case variants —
//     refused by the outbound deny entry, whose reason must be named;
//   - a whole-instance object write that CARRIES outbound — refused (the
//     ancestor-merge rule), outbound unchanged.
//
// Positive control: an instance that HAS an outbound setting keeps its
// ordinary fields agent-writable (enabled) — the subtree block must not
// freeze the instance.

package systools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// seedOutboundChannel installs one instance that already carries an outbound
// setting, so "outbound unchanged" is visible as a preserved grant rather
// than an absent key.
func seedOutboundChannel(cfg *config.Config) {
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"discord": {
			Type:        "discord",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
			Outbound: &config.ChannelOutboundConfig{
				Policy:  "agent_may_start",
				Targets: []string{"discord:222"},
			},
		},
		"slack.eu": {
			Type:        "slack",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
			Outbound: &config.ChannelOutboundConfig{
				Policy:  "reply_only",
				Targets: []string{"slack:C1"},
			},
		},
	}
}

func TestT20_ConfigSet_OutboundRefused_S33(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		value     any
		wantWords []string // lower-cased substrings the refusal reason must name
	}{
		{"bare instance leaf", "channels.discord.outbound",
			map[string]any{"policy": "agent_may_start", "targets": []string{"discord:999"}},
			[]string{"start messages", "recipients"}},
		{"namespaced instance leaf", "channels.slack.eu.outbound",
			map[string]any{"policy": "agent_may_start"},
			[]string{"start messages"}},
		{"policy subkey", "channels.discord.outbound.policy", "agent_may_start",
			[]string{"start messages"}},
		{"targets subkey", "channels.discord.outbound.targets", []string{"discord:999"},
			[]string{"start messages"}},
		// dotSet matches struct fields case-insensitively, so case variants
		// reach the same field and must be refused the same way.
		{"case variant", "channels.discord.Outbound",
			map[string]any{"policy": "agent_may_start"}, []string{"start messages"}},
		{"case variant of a subkey", "channels.slack.eu.OUTBOUND.POLICY", "agent_may_start",
			[]string{"start messages"}},
		// S-33 case 2: a whole-instance object write that CARRIES outbound.
		// dotSet merges the object into the live struct, so the value would
		// reach the posting list.
		{"whole instance carrying outbound", "channels.discord",
			map[string]any{
				"enabled":  true,
				"outbound": map[string]any{"policy": "agent_may_start", "targets": []string{"discord:999"}},
			},
			nil},
		{"whole channel map carrying outbound", "channels",
			map[string]any{"discord": map[string]any{
				"outbound": map[string]any{"policy": "agent_may_start"},
			}},
			nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps(t)
			seedOutboundChannel(cfg)
			before := snapshotConfig(t, cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			if !result.IsError {
				t.Fatalf("set_config(%q) succeeded — an agent rewrote the posting list "+
					"(T-20/S-33, FR-024)\nbody: %s", tc.key, result.ForLLM)
			}
			errObj, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
			message, _ := errObj["message"].(string)
			if message == "" {
				t.Fatalf("set_config(%q) refusal carries no error.message: %s", tc.key, result.ForLLM)
			}
			for _, want := range tc.wantWords {
				if !strings.Contains(strings.ToLower(message), want) {
					t.Errorf("set_config(%q) refusal does not name the reason (want %q in it): %s",
						tc.key, want, message)
				}
			}
			if after := snapshotConfig(t, cfg); after != before {
				t.Errorf("set_config(%q) was refused but the config changed:\nbefore: %s\nafter:  %s",
					tc.key, before, after)
			}
		})
	}
}

func TestT20_ConfigSet_OutboundDenyDoesNotFreezeTheInstance(t *testing.T) {
	// The positive control: the subtree block is about the posting list, not
	// the instance — an ordinary field on an outbound-carrying instance stays
	// agent-writable, and the stored outbound survives the write untouched.
	deps, cfg := newTestDeps(t)
	seedOutboundChannel(cfg)

	result := systools.NewConfigSetTool(deps).Execute(context.Background(),
		map[string]any{"key": "channels.discord.enabled", "value": false})
	if result.IsError {
		t.Fatalf("set_config(channels.discord.enabled) refused — the outbound block must not "+
			"freeze ordinary instance fields: %s", result.ForLLM)
	}
	inst := cfg.Channels["discord"]
	if inst.Enabled {
		t.Fatalf("the enabled=false write did not land")
	}
	if inst.Outbound == nil || inst.Outbound.Policy != "agent_may_start" ||
		len(inst.Outbound.Targets) != 1 || inst.Outbound.Targets[0] != "discord:222" {
		t.Fatalf("the stored outbound changed through an ordinary write: %+v", inst.Outbound)
	}
}
