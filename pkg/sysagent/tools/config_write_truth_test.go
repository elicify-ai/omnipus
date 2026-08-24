// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// This file pins ONE property of set_config: a success answer is evidence that
// the setting changed.
//
// It did not used to be. dotSet round-trips the whole config through JSON, and
// json.Unmarshal DROPS an object member that matches no struct field — so a key
// naming nothing at all cleared the deny list, cleared the allow list, wrote
// into the intermediate map, evaporated on the way back into config.Config, and
// was reported as {"key":…, "value":…, "previous_value":…}. An LLM reading that
// concludes the setting changed and acts on it; the operator then reads a
// config.json that disagrees with what the agent said it did.
//
// The tests below are grouped as: the drops that must now be refusals, the
// phantom creations that must now be refusals, and — the half that matters just
// as much — the legitimate writes that must still land untouched. A tool that
// refuses everything would pass the first two groups.

// ---- group 1: a write that cannot land must be refused, not reported ----

// TestConfigSet_PhantomSectionRejected pins the case that was live in shipped
// code: "heartbeat." is listed in knownConfigPrefixes but config.Config has no
// Heartbeat field, so every write under it was dropped and reported as a
// success. TestConfigSet_NoRestartForNonRestartKey used that very key as its
// fixture and passed, because it only ever asserted on requires_restart.
func TestConfigSet_PhantomSectionRejected(t *testing.T) {
	deps, cfg := newTestDeps()

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "heartbeat.enabled",
		"value": false,
	})
	if !result.IsError {
		t.Fatalf("set_config(heartbeat.enabled) reported success: %s — there is no Heartbeat "+
			"field on config.Config, so nothing was written and the answer is a lie",
			result.ForLLM)
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_KEY" {
		t.Errorf("code = %v, want INVALID_KEY", errBlock["code"])
	}
	// The refusal must name the missing thing, or the agent just retries spellings.
	if msg, _ := errBlock["message"].(string); !strings.Contains(msg, "heartbeat") {
		t.Errorf("message %q does not name the segment that does not exist", msg)
	}
	if _, leaked := mustConfigMap(t, cfg)["heartbeat"]; leaked {
		t.Error("a phantom \"heartbeat\" section was left behind in the config")
	}
}

// TestConfigSet_UnknownFieldInKnownSectionRejected covers the same drop one
// level down: the SECTION exists, the field in it does not.
func TestConfigSet_UnknownFieldInKnownSectionRejected(t *testing.T) {
	for _, key := range []string{
		"gateway.nonexistent_field_zzz",
		"agents.defaults.no_such_setting",
		"tools.read_file.no_such_setting",
		// json:"-" is the same thing by a different route: SaveConfig never
		// serializes these, so a write here could not persist even in memory.
		"agents.defaults.restrict_to_workspace",
	} {
		t.Run(key, func(t *testing.T) {
			deps, _ := newTestDeps()
			result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
				"key":   key,
				"value": "anything",
			})
			if !result.IsError {
				t.Fatalf("set_config(%q) reported success: %s — the field does not exist, so "+
					"json.Unmarshal dropped the write", key, result.ForLLM)
			}
			errBlock, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
			if errBlock["code"] != "INVALID_KEY" {
				t.Errorf("code = %v, want INVALID_KEY", errBlock["code"])
			}
		})
	}
}

// TestConfigSet_NamespacedInstanceFieldsAreTruthfullyReported is the
// namespaced-instance counterpart of every other test in this file: whatever
// set_config reports must match what actually happened, in BOTH directions —
// a reported success must be a real write, and a refusal must be a real
// refusal, never the reverse.
//
// This test used to be TestConfigSet_NamespacedInstanceKeyIsRefusedNotReportedAsWritten,
// asserting only the refusal half, because at the time EVERY namespaced
// instance key was unreachable — ordinary and ownership fields alike — so
// there was only one behaviour worth pinning: a namespaced channel instance
// id contains a dot ("slack.eu"), so "channels.slack.eu.workspace_id" split
// into FOUR segments and the three-segment blocked key
// "channels.*.workspace_id" did not match it; ADR-065 FR-5 stayed safe only
// because dotSet split on "." too and the write landed as a field "eu" of an
// instance "slack" that json.Unmarshal then dropped — safe, but the tool
// answered {"key":"channels.slack.eu.workspace_id","value":"workspace-beta"}
// and an agent reading that believed it had rebound the channel.
//
// configKeySegments (config.go) now resolves a namespaced instance correctly,
// which SPLITS that one behaviour into two: an ordinary field now truthfully
// SUCCEEDS — the headline fix; multi-account channel settings were completely
// unconfigurable through this tool before this change — and an ownership
// field still truthfully REFUSES, via the deny list rather than by the old
// accident (see config_channel_ownership_test.go's
// TestConfigSet_NamespacedInstanceOwnershipRefused for the FR-5 side of that
// same refusal, proved with a fuller set of ownership-field spellings).
func TestConfigSet_NamespacedInstanceFieldsAreTruthfullyReported(t *testing.T) {
	const namespaced = "slack.eu"
	newSeededDeps := func() (*systools.Deps, *config.Config) {
		deps, cfg := newTestDeps()
		cfg.Channels = map[string]config.ChannelInstanceConfig{
			namespaced: {
				Type:        "slack",
				Enabled:     true,
				WorkspaceID: "workspace-alpha",
				Identity:    &config.ChannelIdentity{Kind: config.ChannelIdentityKindAgent, ID: "mia"},
			},
		}
		return deps, cfg
	}

	t.Run("ordinary field succeeds and truthfully lands", func(t *testing.T) {
		deps, cfg := newSeededDeps()
		ctx := context.Background()

		result := systools.NewConfigSetTool(deps).Execute(ctx, map[string]any{
			"key":   "channels.slack.eu.enabled",
			"value": false,
		})
		if result.IsError {
			t.Fatalf("set_config(channels.slack.eu.enabled) = error %s — an ordinary field on a "+
				"namespaced channel instance is legitimately writable; this is the multi-account "+
				"bug this change fixes", result.ForLLM)
		}
		if cfg.Channels[namespaced].Enabled {
			t.Fatalf("set_config reported success but Enabled is still true on %q", namespaced)
		}
		// get_config must be able to show what set_config just reported — the
		// same property TestConfigSet_EveryReportedSuccessIsReadableBack pins
		// for every other key in this file, now proved for a namespaced one.
		getResult := systools.NewConfigGetTool(deps).Execute(ctx,
			map[string]any{"key": "channels.slack.eu.enabled"})
		if getResult.IsError {
			t.Fatalf("set_config reported success but get_config cannot read it back: %s", getResult.ForLLM)
		}
		if value := parseSuccess(t, getResult.ForLLM)["value"]; value != false {
			t.Errorf("get_config(channels.slack.eu.enabled) = %v, want false", value)
		}
	})

	t.Run("ownership field is refused, not silently dropped", func(t *testing.T) {
		deps, cfg := newSeededDeps()

		result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key":   "channels.slack.eu.workspace_id",
			"value": "workspace-beta",
		})
		if !result.IsError {
			t.Fatalf("set_config(channels.slack.eu.workspace_id) reported success: %s — nothing "+
				"was written; the real %q record still reads %q",
				result.ForLLM, namespaced, cfg.Channels[namespaced].WorkspaceID)
		}
		// The safety property from FR-5 must survive the new refusal.
		if got := cfg.Channels[namespaced].WorkspaceID; got != "workspace-alpha" {
			t.Errorf("the %q ownership record moved to %q (ADR-065 FR-5)", namespaced, got)
		}
	})
}

// ---- group 2: a write that would CREATE config must be refused ----

// TestConfigSet_PhantomMapEntryRejected pins the DECISION recorded on
// validateConfigKeyLands: set_config updates settings, it does not create them.
//
// "channels.foo.type" used to conjure a whole channel instance that no operator
// ever configured. It was inert — the ownership record is blocked by FR-5 and
// token_ref by the sensitive-name filter, so the instance could never send
// anything — but it was still a real entry in the operator's config.json,
// indistinguishable from one they wrote themselves.
func TestConfigSet_PhantomMapEntryRejected(t *testing.T) {
	for _, tc := range []struct{ key string }{
		{"channels.foo.type"},
		{"channels.foo.enabled"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps()
			seedOwnedChannel(cfg) // a real, unrelated instance exists

			result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
				"key":   tc.key,
				"value": "slack",
			})
			if !result.IsError {
				t.Fatalf("set_config(%q) reported success: %s — it conjured a channel instance "+
					"the operator never configured", tc.key, result.ForLLM)
			}
			if _, created := cfg.Channels["foo"]; created {
				t.Errorf("a phantom channel instance \"foo\" was created: %+v", cfg.Channels["foo"])
			}
			errBlock, _ := parseError(t, result.ForLLM)["error"].(map[string]any)
			if errBlock["code"] != "INVALID_KEY" {
				t.Errorf("code = %v, want INVALID_KEY", errBlock["code"])
			}
			// A refusal that does not say where creation DOES live just makes the
			// agent retry the same key.
			if msg, _ := errBlock["message"].(string); !strings.Contains(msg, "configure_channel") {
				t.Errorf("message %q does not point at the real creation path", msg)
			}
		})
	}
}

// ---- group 3: the negative control — legitimate writes must still land ----

// TestConfigSet_LegitimateWritesStillLand is the control that stops the two
// groups above from being satisfied by a tool that refuses everything. Every
// key here is a real, unblocked setting, and the assertion is not merely
// "no error" — it reads the typed field back off config.Config, so a refusal
// AND a silent no-op both fail it.
func TestConfigSet_LegitimateWritesStillLand(t *testing.T) {
	cases := []struct {
		key   string
		value any
		check func(*config.Config) bool
		what  string
	}{
		{"gateway.port", float64(7777),
			func(c *config.Config) bool { return c.Gateway.Port == 7777 }, "an int field"},
		{"gateway.host", "127.0.0.5",
			func(c *config.Config) bool { return c.Gateway.Host == "127.0.0.5" }, "a string field"},
		{"agents.defaults.default_model.model", "glm-4.7",
			func(c *config.Config) bool { return c.Agents.Defaults.DefaultModel.Model == "glm-4.7" }, "a nested field"},
		{"tools.read_file.max_read_file_size", float64(123456),
			func(c *config.Config) bool { return c.Tools.ReadFile.MaxReadFileSize == 123456 },
			"a doubly-nested field"},
		{"devices.enabled", true,
			func(c *config.Config) bool { return c.Devices.Enabled }, "a bool field"},
		// The embedded-struct rule: WebToolsConfig embeds ToolConfig with no
		// json name, so encoding/json PROMOTES Enabled and "tools.web.enabled"
		// must resolve to it. A schema walk using reflect.VisibleFields' looser
		// rule, or one that ignored embedding, would refuse this.
		{"tools.web.enabled", true,
			func(c *config.Config) bool { return c.Tools.Web.Enabled }, "a promoted embedded field"},
		// An EXISTING map entry stays fully writable — only creation is refused.
		{"channels.telegram.enabled", false,
			func(c *config.Config) bool { return !c.Channels["telegram"].Enabled },
			"a field of an existing map entry"},
		{"channels.telegram.max_message_length", float64(4096),
			func(c *config.Config) bool { return c.Channels["telegram"].MaxMessageLength == 4096 },
			"another field of an existing map entry"},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			deps, cfg := newTestDeps()
			seedOwnedChannel(cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
				"key": tc.key, "value": tc.value,
			})
			if result.IsError {
				t.Fatalf("set_config(%q) = error %s — %s is legitimately writable and the "+
					"truth gates must not freeze it", tc.key, result.ForLLM, tc.what)
			}
			if !tc.check(cfg) {
				t.Fatalf("set_config(%q) reported success but the value did not land", tc.key)
			}
		})
	}
}

// TestConfigSet_ObjectWriteMergesWithoutFalseAlarm is the specific false
// positive a naive "read it back and compare" check would produce.
//
// json.Unmarshal MERGES an object into the live struct, so writing
// {"default_model":{…}} at "agents.defaults" legitimately leaves every sibling
// setting standing — the value read back is much larger than the value written.
// The verification must judge that a success, member by member, not a mismatch.
func TestConfigSet_ObjectWriteMergesWithoutFalseAlarm(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Agents.Defaults.MaxTokens = 4242
	cfg.Agents.Defaults.DefaultModel.Provider = "openrouter"

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "agents.defaults",
		"value": map[string]any{"default_model": map[string]any{"model": "glm-4.7"}},
	})
	if result.IsError {
		t.Fatalf("set_config(agents.defaults, partial object) = error %s — a partial object "+
			"write is an ordinary merge, not a failed write", result.ForLLM)
	}
	if cfg.Agents.Defaults.DefaultModel.Model != "glm-4.7" {
		t.Fatalf("Name = %q, want glm-4.7", cfg.Agents.Defaults.DefaultModel.Model)
	}
	if cfg.Agents.Defaults.MaxTokens != 4242 || cfg.Agents.Defaults.DefaultModel.Provider != "openrouter" {
		t.Fatalf("the merge dropped siblings: MaxTokens=%d Provider=%q",
			cfg.Agents.Defaults.MaxTokens, cfg.Agents.Defaults.DefaultModel.Provider)
	}
}

// TestConfigSet_ZeroValueOnOmitemptyFieldStillSucceeds is the second false
// positive a naive read-back would produce. `omitempty` erases a zero on the
// way back out, so a field set to "" is ABSENT from the re-marshalled config —
// which looks exactly like a dropped write. It is not one, and clearing a
// setting must keep working.
func TestConfigSet_ZeroValueOnOmitemptyFieldStillSucceeds(t *testing.T) {
	deps, cfg := newTestDeps()
	seedOwnedChannel(cfg)
	instance := cfg.Channels["telegram"]
	instance.BaseURL = "https://api.example.invalid"
	cfg.Channels["telegram"] = instance

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "channels.telegram.base_url", // json:"base_url,omitempty"
		"value": "",
	})
	if result.IsError {
		t.Fatalf("set_config(channels.telegram.base_url, \"\") = error %s — clearing an "+
			"omitempty field is a real write, not a dropped one", result.ForLLM)
	}
	if got := cfg.Channels["telegram"].BaseURL; got != "" {
		t.Fatalf("BaseURL = %q, want cleared", got)
	}
}

// TestConfigSet_NormalisedValueIsReportedAsStored covers the third shape: the
// write lands, but not in the form it was sent.
//
// config.FlexibleStringSlice accepts a bare string and stores ["alice"]. That
// is legitimate — failing the call would break a real write — but reporting
// only {"value":"alice"} leaves the agent believing the stored value is a
// string. The tool must hand back what the config actually holds.
func TestConfigSet_NormalisedValueIsReportedAsStored(t *testing.T) {
	deps, cfg := newTestDeps()
	seedOwnedChannel(cfg)

	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key":   "channels.telegram.allow_from",
		"value": "alice",
	})
	if result.IsError {
		t.Fatalf("set_config(channels.telegram.allow_from, \"alice\") = error %s — a "+
			"FlexibleStringSlice legitimately accepts a bare string", result.ForLLM)
	}
	if got := cfg.Channels["telegram"].AllowFrom; len(got) != 1 || got[0] != "alice" {
		t.Fatalf("AllowFrom = %#v, want [alice]", got)
	}
	m := parseSuccess(t, result.ForLLM)
	stored, ok := m["stored_value"].([]any)
	if !ok {
		t.Fatalf("no stored_value in %v — the response says value:\"alice\" while the config "+
			"holds [\"alice\"], and nothing tells the agent they differ", m)
	}
	if len(stored) != 1 || stored[0] != "alice" {
		t.Errorf("stored_value = %#v, want [\"alice\"]", stored)
	}
	if note, _ := m["note"].(string); !strings.Contains(note, "stored_value") {
		t.Errorf("note = %q — it must point the reader at stored_value", note)
	}
}

// TestConfigSet_ExactWriteReportsNoDrift is the counterpart: when the value
// lands exactly as sent, the response must NOT be cluttered with stored_value
// and a normalisation note. Otherwise "normalised" would mean nothing.
func TestConfigSet_ExactWriteReportsNoDrift(t *testing.T) {
	deps, _ := newTestDeps()
	result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
		"key": "gateway.port", "value": float64(7777),
	})
	if result.IsError {
		t.Fatalf("set_config(gateway.port) = error %s", result.ForLLM)
	}
	m := parseSuccess(t, result.ForLLM)
	if _, present := m["stored_value"]; present {
		t.Errorf("stored_value present for an exact write: %v", m)
	}
	if _, present := m["note"]; present {
		t.Errorf("note present for an exact write: %v", m)
	}
}

// TestConfigSet_EveryReportedSuccessIsReadableBack is the property behind all
// of the above, asserted directly: whatever set_config reports as written,
// get_config must be able to show. Any key that reports success while
// get_config cannot find it is, by definition, the defect this file exists for.
func TestConfigSet_EveryReportedSuccessIsReadableBack(t *testing.T) {
	writes := []struct {
		key   string
		value any
	}{
		{"gateway.port", float64(7777)},
		{"gateway.host", "127.0.0.5"},
		{"agents.defaults.default_model.model", "glm-4.7"},
		{"tools.read_file.max_read_file_size", float64(123456)},
		{"devices.enabled", true},
		{"tools.web.enabled", true},
		{"channels.telegram.enabled", true},
		// The keys that used to report success and vanish.
		{"heartbeat.enabled", false},
		{"gateway.nonexistent_field_zzz", "x"},
		{"channels.slack.eu.workspace_id", "workspace-beta"},
		{"channels.foo.type", "slack"},
	}
	for _, w := range writes {
		t.Run(w.key, func(t *testing.T) {
			deps, cfg := newTestDeps()
			seedOwnedChannel(cfg)
			ctx := context.Background()

			setResult := systools.NewConfigSetTool(deps).Execute(ctx,
				map[string]any{"key": w.key, "value": w.value})
			if setResult.IsError {
				return // a refusal claims nothing, so there is nothing to contradict
			}
			getResult := systools.NewConfigGetTool(deps).Execute(ctx, map[string]any{"key": w.key})
			if getResult.IsError {
				t.Fatalf("set_config(%q) reported success but get_config cannot read it back: %s\n"+
					"set said: %s", w.key, getResult.ForLLM, setResult.ForLLM)
			}
		})
	}
}

// mustConfigMap marshals cfg through the same JSON path set_config writes
// through, so a test can assert on what the config actually contains.
func mustConfigMap(t *testing.T, cfg *config.Config) map[string]any {
	t.Helper()
	data, err := cfg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return m
}
