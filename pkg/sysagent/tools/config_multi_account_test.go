// Omnipus — System Agent Tool Tests: multi-account channel settings reachability
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A namespaced channel instance key ("telegram.one") is created by
// pkg/gateway/rest.go's createChannelInstance as chType + "." + slug
// (ADR-029). Before this change, the settings tools' path walker
// (configKeySegments, dotGet, dotSet) split every dot in a key blindly, so
// "channels.telegram.one.enabled" was parsed as channel type "telegram",
// field "one" — which does not exist on config.ChannelInstanceConfig — and
// refused. EVERY multi-account channel instance the product can create was
// therefore completely unconfigurable through get_config/set_config:
// enable/disable, base_url, allow_from, mention_only, every ordinary
// per-instance setting. Multi-account is exactly what ADR-065 exists to
// support.
//
// Creation (pkg/gateway's REST tests) and settings-reachability were never
// tested across the same seam before this file: this suite builds an
// instance the way the real creation path does, then exercises it entirely
// through the settings tools an agent actually calls.
package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// newNamespacedChannelInstance builds the config.ChannelInstanceConfig entry
// and map key exactly the way pkg/gateway/rest.go's createChannelInstance
// does: instanceKey := chType + "." + slug, validated by
// config.ValidateInstanceKey, initially {type, enabled:false}. Building it
// here rather than through the real REST handler keeps this test inside
// pkg/sysagent/tools (pkg/gateway is contested territory this run) while
// still proving the settings tools resolve the SAME grammar the creation path
// writes — verified by running the identical validation createChannelInstance
// runs before it ever writes the entry.
func newNamespacedChannelInstance(t *testing.T, cfg *config.Config, chType, slug string) string {
	t.Helper()
	instanceKey := chType + "." + slug
	if err := config.ValidateInstanceKey(instanceKey); err != nil {
		t.Fatalf("newNamespacedChannelInstance(%q, %q): %v", chType, slug, err)
	}
	if cfg.Channels == nil {
		cfg.Channels = map[string]config.ChannelInstanceConfig{}
	}
	cfg.Channels[instanceKey] = config.ChannelInstanceConfig{Type: chType, Enabled: false}
	return instanceKey
}

// TestConfigSet_NamespacedInstanceEndToEnd crosses the seam directly: create
// a channel instance the way createChannelInstance does, then reach it
// entirely through get_config/set_config — the two were tested in completely
// disjoint suites before this change (creation in pkg/gateway's REST tests,
// settings reachability nowhere at all for the namespaced shape, because it
// did not work).
func TestConfigSet_NamespacedInstanceEndToEnd(t *testing.T) {
	deps, cfg := newTestDeps()
	instance := newNamespacedChannelInstance(t, cfg, "telegram", "one")
	ctx := context.Background()

	// The instance starts disabled (createChannelInstance's documented
	// default). Enable it and set a second ordinary field through the
	// namespaced key — the exact operation the Channels screen's Configure
	// panel performs via a settings write.
	for _, tc := range []struct {
		key   string
		value any
	}{
		{"channels.telegram.one.enabled", true},
		{"channels.telegram.one.base_url", "https://api.example.invalid"},
	} {
		result := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
			"key": tc.key, "value": tc.value,
		})
		if result.IsError {
			t.Fatalf("set_config(%q) = error %s — a namespaced instance's ordinary settings must "+
				"be reachable through the settings tools", tc.key, result.ForLLM)
		}

		getResult := systools.NewConfigGetTool(deps).Execute(ctx, map[string]any{"key": tc.key})
		if getResult.IsError {
			t.Fatalf("set_config(%q) reported success but get_config cannot read it back: %s",
				tc.key, getResult.ForLLM)
		}
		if got := parseSuccess(t, getResult.ForLLM)["value"]; got != tc.value {
			t.Errorf("get_config(%q) = %v, want %v", tc.key, got, tc.value)
		}
	}

	// The change actually persisted on the real, typed config — not just in
	// the tool's own echo of what it was told.
	got := cfg.Channels[instance]
	if !got.Enabled {
		t.Error("cfg.Channels[\"telegram.one\"].Enabled = false, want true")
	}
	if got.BaseURL != "https://api.example.invalid" {
		t.Errorf("cfg.Channels[\"telegram.one\"].BaseURL = %q, want https://api.example.invalid", got.BaseURL)
	}
}

// TestConfigSet_NamespacedInstanceOrdinaryVsOwnershipFields is the positive
// control alongside the security proof: ordinary fields on a namespaced
// instance must be writable and ownership fields must be blocked, on the SAME
// instance, in the same test — proving the fix does not over-block (freezing
// every field would also make the ownership-refusal half pass) and does not
// under-block (a fully permissive walk would also make the reachability half
// pass).
func TestConfigSet_NamespacedInstanceOrdinaryVsOwnershipFields(t *testing.T) {
	deps, cfg := newTestDeps()
	const instance = "telegram.one"
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		instance: {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
		},
	}
	ctx := context.Background()

	t.Run("ordinary fields are writable", func(t *testing.T) {
		for _, tc := range []struct {
			key   string
			value any
		}{
			{"channels.telegram.one.enabled", false},
			{"channels.telegram.one.mention_only", true},
			{"channels.telegram.one.base_url", "https://api.example.invalid"},
		} {
			result := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
				"key": tc.key, "value": tc.value,
			})
			if result.IsError {
				t.Errorf("set_config(%q) = error %s — ordinary fields must not be collaterally "+
					"blocked by the ownership guard", tc.key, result.ForLLM)
			}
		}
	})

	t.Run("ownership fields are blocked", func(t *testing.T) {
		for _, tc := range []struct {
			key   string
			value any
		}{
			{"channels.telegram.one.identity.id", "attacker-agent"},
			{"channels.telegram.one.workspace_id", "workspace-beta"},
		} {
			result := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
				"key": tc.key, "value": tc.value,
			})
			if !result.IsError {
				t.Errorf("set_config(%q) succeeded — the ownership guard must cover the namespaced "+
					"shape exactly like the bare one (ADR-065 FR-5)", tc.key)
			}
		}
	})

	// Neither sub-test disturbed the record the other one exercises.
	owned := cfg.Channels[instance]
	if owned.WorkspaceID != "workspace-alpha" {
		t.Errorf("WorkspaceID = %q, want workspace-alpha (unchanged)", owned.WorkspaceID)
	}
	if owned.Identity == nil || owned.Identity.ID != "mia" {
		t.Errorf("Identity = %+v, want {agent mia} (unchanged)", owned.Identity)
	}
}

// TestConfigSet_LegacyBareInstanceUnaffectedByMultiAccountFix is the
// regression guard for the fix itself: configKeySegments now has an extra
// branch (coalesce a namespaced instance key) that a BARE key must never
// enter. A bare instance's ordinary fields, and its ownership fields, must
// resolve exactly as they did before this change — the multi-account fix
// must not, as a side effect, change behaviour for the shape that already
// worked.
func TestConfigSet_LegacyBareInstanceUnaffectedByMultiAccountFix(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {
			Type:        "telegram",
			Enabled:     true,
			WorkspaceID: "workspace-alpha",
			Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
		},
	}
	ctx := context.Background()

	ordinary := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
		"key": "channels.telegram.enabled", "value": false,
	})
	if ordinary.IsError {
		t.Fatalf("set_config(channels.telegram.enabled) = error %s — a bare instance's ordinary "+
			"field must still be writable", ordinary.ForLLM)
	}
	if cfg.Channels["telegram"].Enabled {
		t.Fatal("channels.telegram.enabled did not land")
	}

	ownership := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
		"key": "channels.telegram.workspace_id", "value": "workspace-beta",
	})
	if !ownership.IsError {
		t.Fatal("set_config(channels.telegram.workspace_id) succeeded — the bare-instance ownership " +
			"block must survive the multi-account fix unchanged (ADR-065 FR-5)")
	}
	if got := cfg.Channels["telegram"].WorkspaceID; got != "workspace-alpha" {
		t.Errorf("WorkspaceID = %q, want workspace-alpha (unchanged)", got)
	}
}
