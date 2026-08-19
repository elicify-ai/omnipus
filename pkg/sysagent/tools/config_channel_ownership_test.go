// Omnipus — System Agent Tool Tests: channel ownership record is not agent-writable
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-065 / channel-agent-ownership-spec FR-5.
//
// ADR-065 makes a channel instance owned by exactly one (workspace, agent) pair
// and gates every outbound send on that pair. The pair lives in config.json as
// channels.<instance>.identity (kind + id) and channels.<instance>.workspace_id.
//
// set_config is `allow` in the global tool-policy ceiling and "channels." is a
// permitted section, so before this change an agent with shipped-default
// permissions could simply rewrite its own ownership record and then send
// through any instance it wanted. A constraint the constrained party can edit
// is not a constraint — these tests are the proof it can no longer.
//
// Deliberately BLACK-BOX (package systools_test): FR-5 is a property of the
// shipped tool, not of the table behind it. Everything here is asserted through
// the real ConfigSetTool / ConfigGetTool the agent actually calls. The
// table-walking invariants (well-formedness, ancestor coverage, case variants,
// read-policy-decided) already run over every new entry from
// config_blocklist_test.go and config_key_matching_test.go.
package systools_test

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// seedOwnedChannel installs two owned channel instances so a successful rewrite
// is visible as a CHANGED owner rather than as a key appearing out of nowhere.
// discord belongs to a different agent in a different workspace — that is the
// instance the attacking agent wants, and stealing it is the concrete harm
// ADR-065 FR-5 exists to prevent.
func seedOwnedChannel(cfg *config.Config) {
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
		},
		"discord": {
			Type:        "discord",
			Enabled:     true,
			WorkspaceID: "workspace-beta",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "ava"},
		},
	}
}

// TestConfigSet_ChannelOwnershipRecordRefused is the FR-5 enforcement proof
// (spec §4 row 11). Every spelling of "rewrite who owns this channel" must be
// refused by the real tool, and the config must be byte-identical afterwards.
func TestConfigSet_ChannelOwnershipRecordRefused(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{"steal the instance outright", "channels.telegram.identity.id", "attacker-agent"},
		{"flip the identity kind", "channels.telegram.identity.kind", "user"},
		{"replace the whole identity object", "channels.telegram.identity",
			map[string]any{"kind": "agent", "id": "attacker-agent"}},
		{"move the instance to another workspace", "channels.telegram.workspace_id", "workspace-beta"},
		{"unbind the instance", "channels.telegram.workspace_id", ""},
		// Ancestor writes: dotSet writes an object at a path and json.Unmarshal
		// MERGES it into the live struct, so one call at the instance — or at
		// the whole channels map — reaches the ownership fields underneath.
		{"rewrite the instance wholesale", "channels.telegram",
			map[string]any{"identity": map[string]any{"kind": "agent", "id": "attacker-agent"}}},
		{"rewrite the whole channel map", "channels",
			map[string]any{"telegram": map[string]any{"workspace_id": "workspace-beta"}}},
		// dotSet writes through json.Unmarshal, which matches struct fields
		// case-insensitively, so a case variant reaches the same field.
		{"case variant of the owner id", "channels.telegram.Identity.ID", "attacker-agent"},
		{"case variant of the workspace binding", "channels.telegram.Workspace_ID", "workspace-beta"},
		// A different instance key must be covered too — the rule is about the
		// shape of the path, not about one hard-coded instance name.
		{"another instance, same field", "channels.discord.workspace_id", "workspace-beta"},
		{"another instance, owner id", "channels.discord.identity.id", "attacker-agent"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps()
			seedOwnedChannel(cfg)
			before := snapshotConfig(t, cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			if !result.IsError {
				t.Fatalf("set_config(%q) succeeded — an agent can rewrite its own ownership record "+
					"(ADR-065 FR-5)\nbody: %s", tc.key, result.ForLLM)
			}
			errObj, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
			message, _ := errObj["message"].(string)
			if message == "" {
				t.Fatalf("set_config(%q) refusal carries no error.message: %s", tc.key, result.ForLLM)
			}
			// The refusal must NAME the reason. An LLM that is told only "no"
			// retries variations of the same key; one that is told the record is
			// the ownership binding stops.
			if !strings.Contains(strings.ToLower(message), "ownership") {
				t.Errorf("set_config(%q) refusal does not name the reason: %s", tc.key, message)
			}
			if after := snapshotConfig(t, cfg); after != before {
				t.Errorf("set_config(%q) was refused but the config changed:\nbefore: %s\nafter:  %s",
					tc.key, before, after)
			}
		})
	}
}

// TestConfigSet_NamespacedInstanceOwnershipRefused is FR-5 for a NAMESPACED
// channel instance (ADR-029) — the namespaced counterpart of
// TestConfigSet_ChannelOwnershipRecordRefused above.
//
// This test used to be TestConfigSet_NamespacedInstanceOwnershipIsUnreachable,
// asserting a narrower, ACCIDENTAL property: instance ids may be namespaced
// and therefore contain a dot ("slack.eu"), so "channels.slack.eu.workspace_id"
// split into four raw segments and the three-segment blocked key
// "channels.*.workspace_id" did not cover it positionally. The key was
// harmless anyway, for a reason that lived in the WRITER rather than in the
// policy table: dotSet split on every ".", so the value could only ever land
// under a SEPARATE instance called "slack" whose "eu" field does not exist on
// ChannelInstanceConfig — and json.Unmarshal silently dropped it. The real
// "slack.eu" record was never addressable by that writer at all. That test's
// own comment named exactly this moment: "if dotSet ever learns to address
// dotted map keys, this test fails and blockedConfigKeys must grow a matching
// rule — instead of the hole opening silently."
//
// configKeySegments (config.go) is that change: it now resolves "slack"+"eu"
// into the real "slack.eu" map entry ON PURPOSE, for every ORDINARY field —
// that is the whole point of the multi-account fix (see
// config_multi_account_test.go). Left alone, that would open exactly the hole
// the old comment warned about: an agent could rewrite a namespaced
// instance's ownership record the same way it could once rewrite a bare
// one's, before FR-5 existed. blockedConfigKeys' existing
// "channels.*.identity" / "channels.*.workspace_id" entries close it WITHOUT
// growing — configKeySegments coalesces "slack"+"eu" into the single token
// "slack.eu" before the wildcard match ever runs, so the SAME single-segment
// wildcard that has always covered a bare instance covers a namespaced one
// too (see the "single-segment wildcard is enough for BOTH shapes" comment
// above those entries in config.go). The write is REFUSED, and refused for
// the reason FR-5 names — not by an accident of the old writer, and the
// assertion below says so directly instead of staying verdict-free.
func TestConfigSet_NamespacedInstanceOwnershipRefused(t *testing.T) {
	const instance = "slack.eu"
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{"steal the instance outright", "channels.slack.eu.identity.id", "attacker-agent"},
		{"flip the identity kind", "channels.slack.eu.identity.kind", "user"},
		{"replace the whole identity object", "channels.slack.eu.identity",
			map[string]any{"kind": "agent", "id": "attacker-agent"}},
		{"move the instance to another workspace", "channels.slack.eu.workspace_id", "workspace-beta"},
		{"unbind the instance", "channels.slack.eu.workspace_id", ""},
		// dotSet writes through json.Unmarshal, which matches struct fields
		// case-insensitively (and configSegmentMatches folds the same way),
		// so a case variant reaches the same field.
		{"case variant of the owner id", "channels.slack.eu.Identity.ID", "attacker-agent"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps()
			cfg.Channels = map[string]config.ChannelInstanceConfig{
				instance: {
					Type:        "slack",
					Enabled:     true,
					WorkspaceID: "workspace-alpha",
					Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
				},
			}
			before := snapshotConfig(t, cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})

			if !result.IsError {
				t.Fatalf("set_config(%q) succeeded — an agent can rewrite a NAMESPACED instance's "+
					"own ownership record (ADR-065 FR-5)\nbody: %s", tc.key, result.ForLLM)
			}
			errObj, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
			message, _ := errObj["message"].(string)
			if message == "" {
				t.Fatalf("set_config(%q) refusal carries no error.message: %s", tc.key, result.ForLLM)
			}
			// The refusal must NAME the reason, same as the bare-instance
			// case above — an LLM told only "no" retries variations of the
			// same key.
			if !strings.Contains(strings.ToLower(message), "ownership") {
				t.Errorf("set_config(%q) refusal does not name the reason: %s", tc.key, message)
			}
			if after := snapshotConfig(t, cfg); after != before {
				t.Errorf("set_config(%q) was refused but the config changed:\nbefore: %s\nafter:  %s",
					tc.key, before, after)
			}
		})
	}
}

// TestConfigSet_ChannelOwnershipBlockDoesNotOverBlock is the positive control
// for FR-5. A subtree block on "channels" would pass every assertion above and
// would also freeze every ordinary channel setting — a broken tool, not a
// secure one. Each key here is a real field on config.ChannelInstanceConfig and
// carries no ownership meaning.
func TestConfigSet_ChannelOwnershipBlockDoesNotOverBlock(t *testing.T) {
	cases := []struct {
		key   string
		value any
		check func(config.ChannelInstanceConfig) bool
	}{
		{"channels.telegram.enabled", false, func(c config.ChannelInstanceConfig) bool { return !c.Enabled }},
		{"channels.telegram.mention_only", true, func(c config.ChannelInstanceConfig) bool { return c.MentionOnly }},
		{"channels.telegram.base_url", "https://api.example.invalid",
			func(c config.ChannelInstanceConfig) bool { return c.BaseURL == "https://api.example.invalid" }},
		{"channels.telegram.use_markdown_v2", true,
			func(c config.ChannelInstanceConfig) bool { return c.UseMarkdownV2 }},
		{"channels.telegram.max_message_length", 4096,
			func(c config.ChannelInstanceConfig) bool { return c.MaxMessageLength == 4096 }},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps()
			seedOwnedChannel(cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(),
				map[string]any{"key": tc.key, "value": tc.value})
			if result.IsError {
				t.Fatalf("set_config(%q) = error %s — the ownership block must not collaterally "+
					"freeze ordinary channel settings", tc.key, result.ForLLM)
			}
			instance := cfg.Channels["telegram"]
			if !tc.check(instance) {
				t.Fatalf("set_config(%q) reported success but the value did not land: %+v",
					tc.key, instance)
			}
			// The write must not have disturbed the ownership record it sits next to.
			if instance.WorkspaceID != "workspace-alpha" || instance.Identity == nil || instance.Identity.ID != "mia" {
				t.Fatalf("set_config(%q) altered the ownership record: workspace=%q identity=%+v",
					tc.key, instance.WorkspaceID, instance.Identity)
			}
		})
	}
}

// TestConfigGet_ChannelOwnershipRecordIsReadable pins the READ half of the FR-5
// decision, which is deliberately the opposite of the write half.
//
// DECISION: the ownership record is write-blocked and READABLE. It is
// enforcement configuration, not credential material, and it follows the
// operator's 2026-08-12 ruling already recorded on the sandbox entry — an agent
// granted the config tool may read its own cage, because that is how it can
// explain WHY it cannot do something instead of failing opaquely. Under ADR-065
// an agent that names a channel it does not own gets a refusal; without the read
// it cannot discover which instance it DOES own, and the refusal is unactionable.
//
// The disclosure this concedes is small and largely unavoidable: the instance
// KEYS under "channels" are map keys and stay visible whatever the leaf policy
// is, and enforcement now lives at the send tool and at dispatch (FR-4, FR-6),
// so knowing an owner buys an attacker nothing it can act on.
func TestConfigGet_ChannelOwnershipRecordIsReadable(t *testing.T) {
	deps, cfg := newTestDeps()
	seedOwnedChannel(cfg)
	get := systools.NewConfigGetTool(deps)

	t.Run("direct read of the owner id", func(t *testing.T) {
		result := get.Execute(context.Background(), map[string]any{"key": "channels.telegram.identity.id"})
		if result.IsError {
			t.Fatalf("get_config(channels.telegram.identity.id) = error %s — the ownership record "+
				"is deliberately readable", result.ForLLM)
		}
		if value := parseSuccess(t, result.ForLLM)["value"]; value != "mia" {
			t.Errorf("value = %v, want \"mia\"", value)
		}
	})

	t.Run("direct read of the workspace binding", func(t *testing.T) {
		result := get.Execute(context.Background(), map[string]any{"key": "channels.telegram.workspace_id"})
		if result.IsError {
			t.Fatalf("get_config(channels.telegram.workspace_id) = error %s", result.ForLLM)
		}
		if value := parseSuccess(t, result.ForLLM)["value"]; value != "workspace-alpha" {
			t.Errorf("value = %v, want \"workspace-alpha\"", value)
		}
	})

	t.Run("section read leaves the record in the clear", func(t *testing.T) {
		result := get.Execute(context.Background(), map[string]any{"key": "channels"})
		if result.IsError {
			t.Fatalf("get_config(channels) = error %s", result.ForLLM)
		}
		body := parseSuccess(t, result.ForLLM)
		section, _ := body["value"].(map[string]any)
		instance, _ := section["telegram"].(map[string]any)
		if instance == nil {
			t.Fatalf("get_config(channels) returned no telegram instance: %s", result.ForLLM)
		}
		if instance["workspace_id"] != "workspace-alpha" {
			t.Errorf("workspace_id = %v, want \"workspace-alpha\" (unredacted by decision)",
				instance["workspace_id"])
		}
		identity, _ := instance["identity"].(map[string]any)
		if identity == nil || identity["id"] != "mia" {
			t.Errorf("identity = %v, want {kind:agent id:mia} (unredacted by decision)", instance["identity"])
		}
	})
}
