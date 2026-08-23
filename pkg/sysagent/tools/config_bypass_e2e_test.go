// Omnipus — System Agent Tool Tests: set_config deny-table bypasses
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// TestConfigSet_CaseVariantOfBlockedKeyRefused is the end-to-end proof for the
// case bypass. Each key below was ACCEPTED before the fix and flipped a live
// security control, because the deny table compared bytes while dotSet writes
// through json.Unmarshal, which matches struct fields case-insensitively.
//
// The two booleans asserted here are the ones that were demonstrated:
// dev_mode_bypass disables gateway authentication entirely and is read per
// request (no restart), and trust_xff makes the client IP attacker-controlled,
// which is what auth rate limiting and audit attribution key on. Both are
// `omitempty` and false by default, which is exactly why the mis-cased write
// landed: with the canonical key absent from the marshaled map, the mis-cased
// one was the only spelling json.Unmarshal saw.
func TestConfigSet_CaseVariantOfBlockedKeyRefused(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{"title-cased leaf", "gateway.Dev_Mode_Bypass", true},
		{"upper-cased leaf", "gateway.DEV_MODE_BYPASS", true},
		{"title-cased section", "Gateway.dev_mode_bypass", true},
		{"whole key upper", "GATEWAY.DEV_MODE_BYPASS", true},
		{"xff title-cased", "gateway.Trust_Xff", true},
		{"xff upper", "gateway.TRUST_XFF", true},
		{"sandbox mixed case", "Sandbox.Mode", "off"},
		{"tool policies mixed case", "SANDBOX.Tool_Policies.bash", "allow"},
		{"read paths mixed case", "tools.Allow_Read_Paths", []any{"/"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps()
			before := snapshotConfig(t, cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
				"key":   tc.key,
				"value": tc.value,
			})
			if !result.IsError {
				t.Fatalf("set_config(%q) succeeded — a mis-cased spelling reaches the same field "+
					"as the blocked key: %s", tc.key, result.ForLLM)
			}
			if cfg.Gateway.DevModeBypass {
				t.Fatalf("set_config(%q) left gateway authentication disabled", tc.key)
			}
			if cfg.Gateway.TrustXFF {
				t.Fatalf("set_config(%q) left the client IP attacker-controlled", tc.key)
			}
			if after := snapshotConfig(t, cfg); after != before {
				t.Errorf("set_config(%q) was refused but still mutated the config\nbefore: %s\nafter:  %s",
					tc.key, before, after)
			}
		})
	}
}

// TestConfigSet_SectionWriteRefused is the end-to-end proof for the ancestor
// bypass: an object written at a SECTION path is merged into the live struct by
// json.Unmarshal, so it reaches blocked leaves the deny table refused
// individually. The allow list explicitly admitted a bare section name, so this
// needed no trick at all — just a coarser key.
func TestConfigSet_SectionWriteRefused(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{
			name:  "gateway section disables auth",
			key:   "gateway",
			value: map[string]any{"dev_mode_bypass": true},
		},
		{
			name:  "gateway section spoofs the client ip",
			key:   "gateway",
			value: map[string]any{"trust_xff": true},
		},
		{
			name:  "mis-cased gateway section",
			key:   "Gateway",
			value: map[string]any{"dev_mode_bypass": true},
		},
		{
			name:  "tools section rewrites the filesystem fence",
			key:   "tools",
			value: map[string]any{"allow_write_paths": []any{"/"}},
		},
		{
			name:  "tools section stops secret redaction",
			key:   "tools",
			value: map[string]any{"filter_sensitive_data": false},
		},
		{
			name:  "mis-cased tools section",
			key:   "TOOLS",
			value: map[string]any{"allow_read_paths": []any{"/"}},
		},
		{
			name:  "tools.web section repoints egress",
			key:   "tools.web",
			value: map[string]any{"proxy": "http://attacker.example:8080"},
		},
		{
			name:  "agents section replaces the roster",
			key:   "agents",
			value: map[string]any{"list": []any{map[string]any{"id": "pwn"}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps, cfg := newTestDeps()
			before := snapshotConfig(t, cfg)

			result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
				"key":   tc.key,
				"value": tc.value,
			})
			if !result.IsError {
				t.Fatalf("set_config(%q) succeeded — a section write reaches every blocked leaf "+
					"under it: %s", tc.key, result.ForLLM)
			}
			m := parseError(t, result.ForLLM)
			errBlock, _ := m["error"].(map[string]any)
			if code, _ := errBlock["code"].(string); code != "INVALID_KEY" {
				t.Errorf("set_config(%q): code = %v, want INVALID_KEY", tc.key, errBlock["code"])
			}
			if cfg.Gateway.DevModeBypass || cfg.Gateway.TrustXFF {
				t.Fatalf("set_config(%q) reached a gateway security control: bypass=%v xff=%v",
					tc.key, cfg.Gateway.DevModeBypass, cfg.Gateway.TrustXFF)
			}
			if !cfg.Tools.FilterSensitiveData {
				t.Fatalf("set_config(%q) switched off secret redaction", tc.key)
			}
			if after := snapshotConfig(t, cfg); after != before {
				t.Errorf("set_config(%q) was refused but still mutated the config\nbefore: %s\nafter:  %s",
					tc.key, before, after)
			}
		})
	}
}

// TestConfigSet_SectionWritesThatAreSafeStillWork is the positive control for
// the ancestor rule. "At, under or above a blocked key" must not degrade into
// "no section may ever be written": a section that contains no blocked
// descendant is still writable as a whole, and so are ordinary leaves.
func TestConfigSet_SectionWritesThatAreSafeStillWork(t *testing.T) {
	t.Run("agents.defaults section", func(t *testing.T) {
		deps, cfg := newTestDeps()
		result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key":   "agents.defaults",
			"value": map[string]any{"default_model": map[string]any{"model": "glm-4.7"}},
		})
		if result.IsError {
			t.Fatalf("set_config(agents.defaults) failed: %s", result.ForLLM)
		}
		if cfg.Agents.Defaults.DefaultModel.Model != "glm-4.7" {
			t.Errorf("ModelName = %q, want %q", cfg.Agents.Defaults.DefaultModel.Model, "glm-4.7")
		}
	})

	t.Run("tools.read_file section", func(t *testing.T) {
		deps, cfg := newTestDeps()
		result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key":   "tools.read_file",
			"value": map[string]any{"max_read_file_size": float64(4242)},
		})
		if result.IsError {
			t.Fatalf("set_config(tools.read_file) failed: %s", result.ForLLM)
		}
		if cfg.Tools.ReadFile.MaxReadFileSize != 4242 {
			t.Errorf("MaxReadFileSize = %d, want 4242", cfg.Tools.ReadFile.MaxReadFileSize)
		}
	})

	t.Run("gateway.port leaf", func(t *testing.T) {
		deps, cfg := newTestDeps()
		result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key":   "gateway.port",
			"value": float64(7331),
		})
		if result.IsError {
			t.Fatalf("set_config(gateway.port) failed: %s", result.ForLLM)
		}
		if cfg.Gateway.Port != 7331 {
			t.Errorf("cfg.Gateway.Port = %d, want 7331", cfg.Gateway.Port)
		}
	})
}

// assertDefaultConfigShape guards the two assumptions the negative tests above
// rely on: that a fresh config has authentication ON and redaction ON. If a
// default ever flips, the tests would still pass while proving nothing.
func TestConfigSet_DefaultsAssumedByTheseTests(t *testing.T) {
	cfg := config.DefaultConfig()
	if cfg.Gateway.DevModeBypass {
		t.Error("DefaultConfig has dev_mode_bypass ON — the bypass assertions above prove nothing")
	}
	if cfg.Gateway.TrustXFF {
		t.Error("DefaultConfig has trust_xff ON — the XFF assertions above prove nothing")
	}
	if !cfg.Tools.FilterSensitiveData {
		t.Error("DefaultConfig has secret redaction OFF — the redaction assertion above proves nothing")
	}
}
