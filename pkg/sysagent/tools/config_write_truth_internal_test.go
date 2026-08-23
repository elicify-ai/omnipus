// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// The end-to-end tests in config_write_truth_test.go can only ever exercise the
// FIRST truth gate, because validateConfigKeyLands refuses a dropped key before
// dotSet is reached. These tests drive the second gate directly, so its
// drop-detection is proved rather than assumed — otherwise observeConfigWrite
// would be a branch no test can enter.

// TestDotSet_DropsAKeyThatNamesNothingAndReportsNoError is the defect itself,
// isolated to two lines. It is not asserting desirable behaviour — it is
// pinning the fact everything else in this file exists to compensate for: the
// writer through which set_config makes every change reports success for a
// write that changed nothing.
func TestDotSet_DropsAKeyThatNamesNothingAndReportsNoError(t *testing.T) {
	cfg := config.DefaultConfig()

	if err := dotSet(cfg, "heartbeat.enabled", true); err != nil {
		t.Fatalf("dotSet returned %v — if it now REFUSES a key that names no field, "+
			"validateConfigKeyLands has become redundant and this file should shrink", err)
	}
	if _, err := dotGet(cfg, "heartbeat.enabled"); err == nil {
		t.Fatal("config.Config grew a heartbeat section — the fixture is stale; " +
			"pick another key that names nothing")
	}
}

// TestObserveConfigWrite_DetectsADroppedWrite is the gate catching exactly that.
func TestObserveConfigWrite_DetectsADroppedWrite(t *testing.T) {
	for _, tc := range []struct{ key string }{
		{"heartbeat.enabled"},             // no such section
		{"gateway.nonexistent_field_zzz"}, // no such field
		{"channels.x.eu.workspace_id"},    // a dotted instance id, split into "x" → "eu"
	} {
		t.Run(tc.key, func(t *testing.T) {
			cfg := config.DefaultConfig()
			if err := dotSet(cfg, tc.key, "a-non-zero-value"); err != nil {
				t.Fatalf("dotSet: %v", err)
			}
			_, normalised, err := observeConfigWrite(cfg, tc.key, "a-non-zero-value")
			if err == nil {
				t.Fatalf("observeConfigWrite(%q) = nil — the write was dropped and the tool "+
					"would report it as applied", tc.key)
			}
			if !strings.Contains(err.Error(), "did not take effect") {
				t.Errorf("error %q does not say the write did not take effect", err)
			}
			if len(normalised) != 0 {
				t.Errorf("normalised = %v, want none for a dropped write", normalised)
			}
		})
	}
}

// TestObserveConfigWrite_MergeIsNotDrift is the first false positive a naive
// read-back comparison produces: json.Unmarshal MERGES an object into the live
// struct, so the value read back at "agents.defaults" carries every sibling
// setting as well as the one member that was written.
func TestObserveConfigWrite_MergeIsNotDrift(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.MaxTokens = 4242

	want := map[string]any{"default_model": map[string]any{"model": "glm-4.7"}}
	if err := dotSet(cfg, "agents.defaults", want); err != nil {
		t.Fatalf("dotSet: %v", err)
	}
	_, normalised, err := observeConfigWrite(cfg, "agents.defaults", want)
	if err != nil {
		t.Fatalf("observeConfigWrite = %v — a partial object write is a merge, not a drop", err)
	}
	if len(normalised) != 0 {
		t.Errorf("normalised = %v — the untouched siblings are not drift", normalised)
	}
	if cfg.Agents.Defaults.MaxTokens != 4242 {
		t.Errorf("the merge itself lost a sibling: MaxTokens = %d", cfg.Agents.Defaults.MaxTokens)
	}
}

// TestObserveConfigWrite_OmitemptyZeroIsNotADrop is the second false positive:
// `omitempty` erases a zero value on the way back out, so a field that was
// genuinely set to "" is ABSENT afterwards and looks identical to a dropped
// write. validateConfigKeyLands has already proved the field exists, which is
// what makes it safe to read absence as zero here.
func TestObserveConfigWrite_OmitemptyZeroIsNotADrop(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true, BaseURL: "https://api.example.invalid"},
	}
	if err := dotSet(cfg, "channels.telegram.base_url", ""); err != nil {
		t.Fatalf("dotSet: %v", err)
	}
	if got := cfg.Channels["telegram"].BaseURL; got != "" {
		t.Fatalf("precondition: BaseURL = %q, want cleared", got)
	}
	if _, err := dotGet(cfg, "channels.telegram.base_url"); err == nil {
		t.Fatal("precondition: base_url is still present after being cleared — " +
			"the omitempty tag this test is about has gone")
	}
	_, _, err := observeConfigWrite(cfg, "channels.telegram.base_url", "")
	if err != nil {
		t.Fatalf("observeConfigWrite = %v — clearing an omitempty field is a real write", err)
	}
}

// TestObserveConfigWrite_NormalisationIsReportedNotFailed is the third shape.
// config.FlexibleStringSlice stores the bare string "alice" as ["alice"]: the
// write landed, in a different form. That must surface, and must not fail.
func TestObserveConfigWrite_NormalisationIsReportedNotFailed(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram": {Type: "telegram", Enabled: true},
	}
	if err := dotSet(cfg, "channels.telegram.allow_from", "alice"); err != nil {
		t.Fatalf("dotSet: %v", err)
	}
	stored, normalised, err := observeConfigWrite(cfg, "channels.telegram.allow_from", "alice")
	if err != nil {
		t.Fatalf("observeConfigWrite = %v — normalisation is legitimate and must not fail the write", err)
	}
	if len(normalised) != 1 || normalised[0] != "channels.telegram.allow_from" {
		t.Fatalf("normalised = %v, want the written path", normalised)
	}
	list, ok := stored.([]any)
	if !ok || len(list) != 1 || list[0] != "alice" {
		t.Fatalf("stored = %#v, want [\"alice\"]", stored)
	}
}

// TestConfigWriteDrift_SeparatesMissingFromDifferent pins the distinction the
// whole design rests on: an ABSENT leaf is a dropped write (an error), a
// PRESENT but unequal leaf is normalisation (reported, not fatal). Collapsing
// the two either breaks legitimate writes or lets a drop through.
func TestConfigWriteDrift_SeparatesMissingFromDifferent(t *testing.T) {
	got := map[string]any{
		"kept":       "same",
		"normalised": []any{"alice"},
		"nested":     map[string]any{"there": float64(1)},
	}
	want := map[string]any{
		"kept":       "same",
		"normalised": "alice",
		"nested":     map[string]any{"there": float64(1), "gone": "value"},
		"dropped":    "value",
		"zero":       "", // absent from got, but zero — omitempty, not a drop
	}
	missing, differing := configWriteDrift("k", got, want)

	wantMissing := []string{"k.dropped", "k.nested.gone"}
	if strings.Join(missing, ",") != strings.Join(wantMissing, ",") {
		t.Errorf("missing = %v, want %v", missing, wantMissing)
	}
	wantDiffering := []string{"k.normalised"}
	if strings.Join(differing, ",") != strings.Join(wantDiffering, ",") {
		t.Errorf("differing = %v, want %v", differing, wantDiffering)
	}
}

// TestValidateConfigKeyLands_Table walks the schema gate over the shapes it has
// to get right. The accepted half is the load-bearing half: a gate that refuses
// everything would satisfy every rejection case below.
func TestValidateConfigKeyLands_Table(t *testing.T) {
	newCfg := func() *config.Config {
		cfg := config.DefaultConfig()
		cfg.Channels = map[string]config.ChannelInstanceConfig{
			"telegram": {Type: "telegram", Enabled: true},
			"slack.eu": {Type: "slack", Enabled: true},
		}
		return cfg
	}

	accepted := map[string]string{
		"gateway.port":                        "a plain nested field",
		"gateway":                             "a bare section — writing a whole object is legal",
		"agents.defaults.default_model.model": "three levels down, inside a nested struct",
		"tools.read_file.max_read_file_size":  "three levels down",
		"devices.enabled":                     "a bool",
		"tools.web.enabled":                   "PROMOTED from the untagged embedded ToolConfig",
		"tools.web.brave.enabled":             "a named nested struct beside the embedded one",
		"channels.telegram.enabled":           "a field of an EXISTING map entry",
		"channels.telegram.base_url":          "an omitempty field of an existing map entry",
		"providers":                           "a slice field, written wholesale",
		// The multi-account fix: "slack.eu" is a NAMESPACED instance id
		// (ADR-029) that itself contains a dot, so these two keys split into
		// FOUR raw segments. configKeySegments now coalesces "slack"+"eu"
		// back into the single map key "slack.eu" — exactly the
		// createChannelInstance map key (pkg/gateway/rest.go) — before
		// walking the schema, so an ordinary field on a namespaced instance
		// lands exactly like one on a bare instance always has.
		"channels.slack.eu.enabled": "an ordinary field of an EXISTING namespaced map entry",
		// workspace_id is ALSO an ownership field (ADR-065 FR-5), and it DOES
		// land here: this function only walks the schema, it has no notion of
		// which fields are security-sensitive. Landing here is necessary but
		// not sufficient for set_config to actually apply the write — the
		// refusal for this specific key comes from a DIFFERENT, EARLIER
		// layer, validateConfigKey / blockedConfigKeys, which ConfigSetTool
		// always runs first. See
		// TestConfigSet_NamespacedInstanceOwnershipRefused
		// (config_channel_ownership_test.go) for that refusal proved through
		// the real tool.
		"channels.slack.eu.workspace_id": "an ownership field — lands at the schema level; blocked one layer up",
	}
	for key, why := range accepted {
		t.Run("accept/"+key, func(t *testing.T) {
			if err := validateConfigKeyLands(newCfg(), key); err != nil {
				t.Errorf("validateConfigKeyLands(%q) = %v — %s, and the gate must not freeze it",
					key, err, why)
			}
		})
	}

	refused := map[string]string{
		"heartbeat.enabled":                     "no Heartbeat field exists on config.Config",
		"gateway.nonexistent_field_zzz":         "no such field in an existing section",
		"agents.defaults.restrict_to_workspace": "json:\"-\" — it can never be written through JSON",
		"channels.foo.enabled":                  "\"foo\" is a NEW map entry, i.e. creation",
		"gateway.port.sub":                      "there is nothing inside an int",
		"providers.0.model_name":                "a slice is not addressable by segment",
	}
	for key, why := range refused {
		t.Run("refuse/"+key, func(t *testing.T) {
			err := validateConfigKeyLands(newCfg(), key)
			if err == nil {
				t.Errorf("validateConfigKeyLands(%q) = nil — %s, so the write would be "+
					"reported without landing", key, why)
				return
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error %q does not name the key the caller sent", err)
			}
		})
	}

	// The namespaced instance again, this time with its dotted PREFIX ALSO
	// present as a real, separate instance: both "slack" (bare) and
	// "slack.eu" (namespaced) exist at once — a real coexistence scenario (a
	// legacy bare-keyed instance alongside a newer namespaced one, same
	// type). "eu" is not a field of ChannelInstanceConfig, so the bare
	// reading is impossible regardless of the fact that "slack" happens to
	// exist; configKeySegments must fall through to the namespaced reading
	// rather than getting stuck on a same-typed bare sibling. See
	// configKeySegments' comment on why lookupJSONField — not mere
	// existence — decides which reading wins.
	t.Run("accept/channels.slack.eu.workspace_id with a coexisting bare slack instance", func(t *testing.T) {
		cfg := newCfg()
		cfg.Channels["slack"] = config.ChannelInstanceConfig{Type: "slack"}
		if err := validateConfigKeyLands(cfg, "channels.slack.eu.workspace_id"); err != nil {
			t.Errorf("validateConfigKeyLands = %v — \"eu\" cannot be a field of the BARE \"slack\" "+
				"instance, so the namespaced \"slack.eu\" instance must be tried instead, and it has "+
				"a real workspace_id field (the write is still refused one layer up, by "+
				"validateConfigKey/blockedConfigKeys — ADR-065 FR-5 — not by failing to land here)", err)
		}
	})
}

// TestJSONAddressableFields_MatchesEncodingJSONPromotion pins the one reflection
// rule that is easy to get subtly wrong. encoding/json promotes the fields of an
// anonymous struct ONLY when the embedded field carries no json name;
// reflect.VisibleFields promotes them either way. pkg/config depends on the
// stricter rule (WebToolsConfig embeds ToolConfig untagged), and getting it
// wrong in the loose direction would let the gate accept keys that do not exist.
func TestJSONAddressableFields_MatchesEncodingJSONPromotion(t *testing.T) {
	type Inner struct {
		Promoted string `json:"promoted"`
	}
	type NamedEmbed struct {
		Buried string `json:"buried"`
	}
	type Named struct {
		Hidden string `json:"hidden"`
	}
	type Outer struct {
		Inner                           // untagged embed  → Promoted IS promoted
		NamedEmbed `json:"named_embed"` // named embed     → Buried is NOT promoted
		Named      Named                `json:"named"`
		Skip       string               `json:"-"`
		Direct     string               `json:"direct"`
	}

	names := map[string]bool{}
	for _, f := range jsonAddressableFields(reflect.TypeOf(Outer{})) {
		names[f.Name] = true
	}
	for _, want := range []string{"promoted", "named", "direct", "named_embed"} {
		if !names[want] {
			t.Errorf("%q is not addressable but encoding/json can decode it there: %v", want, names)
		}
	}
	for _, unwanted := range []string{"Skip", "-", "buried", "Inner", "Promoted"} {
		if names[unwanted] {
			t.Errorf("%q is addressable but encoding/json cannot decode it there: %v", unwanted, names)
		}
	}
}
