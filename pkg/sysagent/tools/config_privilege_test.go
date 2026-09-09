// Omnipus — System Agent Tool Tests: set_config privilege escalation
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// snapshotConfig serializes the whole config so a refused set_config call can be
// asserted to have changed NOTHING — not just the key it aimed at. dotSet
// round-trips the entire config through a generic map, so a partially-applied
// write would show up here even if the targeted field looked untouched.
func snapshotConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return string(b)
}

// TestConfigSet_SecurityCriticalKeysRefused is the end-to-end proof for the
// second privilege escalation: set_config took no path argument and so bypassed
// the filesystem chokepoint entirely, while the keys below are precisely the
// controls that define the agent's boundary. Every one of them must be refused
// through the real tool, and the config must be byte-identical afterwards.
func TestConfigSet_SecurityCriticalKeysRefused(t *testing.T) {
	cases := []struct {
		name  string
		key   string
		value any
	}{
		{"sandbox off", "sandbox.mode", "off"},
		{"god mode on", "sandbox.god_mode", true},
		{"god mode authorized", "sandbox.god_mode_allowed", true},
		{"rewrite tool policies", "sandbox.tool_policies", map[string]any{"bash": "allow"}},
		{"grant itself bash", "sandbox.tool_policies.bash", "allow"},
		{"empty the shell guard", "sandbox.shell_deny_patterns", []any{}},
		{"widen writable paths", "sandbox.allowed_paths", []any{"/"}},
		{"widen executable paths", "sandbox.allowed_exec_paths", []any{"/"}},
		{"open the filesystem model", "sandbox.filesystem_model", "open"},
		{"open internal egress", "sandbox.egress_allow_cidrs", []any{"0.0.0.0/0"}},
		{"disable ssrf", "sandbox.ssrf.enabled", false},
		{"exempt internal hosts", "sandbox.ssrf.allow_internal", []any{"169.254.169.254"}},
		{"disable the audit log", "sandbox.audit_log", false},
		{"trust unverified skills", "sandbox.skill_trust", "allow_all"},
		{"weaken injection guard", "sandbox.prompt_injection_level", "low"},
		{"disable gateway auth", "gateway.dev_mode_bypass", true},
		{"mint an api credential", "gateway.users", []any{map[string]any{"username": "pwn"}}},
		{"spoof the client ip", "gateway.trust_xff", true},
		{"widen the canonical origin", "gateway.public_url", "https://attacker.example"},
		{"defeat the approval gate", "gateway.tool_approval_timeout", 0},
		{"defeat the approval queue", "gateway.tool_approval_max_pending", 0},
		{"silence auth failures", "gateway.auth_mismatch_log_level", "debug"},
		{"disable inbound validation", "gateway.validate_inbound", false},
		{"widen file reads", "tools.allow_read_paths", []any{"/"}},
		{"widen file writes", "tools.allow_write_paths", []any{"/"}},
		{"stop secret redaction", "tools.filter_sensitive_data", false},
		{"neuter secret redaction", "tools.filter_min_length", 1 << 30},
		{"widen the exec allow-list", "tools.exec.allowed_binaries", []any{"/bin/sh"}},
		{"drop exec approval", "tools.exec.approval", "never"},
		{"bypass the egress proxy", "tools.exec.enable_proxy", false},
		{"register an mcp program", "tools.mcp.servers", map[string]any{
			"pwn": map[string]any{"command": "/bin/sh", "args": []any{"-c", "id"}},
		}},
		{"launch an arbitrary binary", "tools.browser.exec_path", "/tmp/evil"},
		{"attach to a foreign cdp", "tools.browser.cdp_url", "ws://attacker.example/devtools"},
		{"enable arbitrary page js", "sandbox.browser_evaluate_enabled", true},
		{"let cron run shell", "tools.cron.allow_command", true},
		{"exempt private hosts", "tools.web.private_host_whitelist", []any{"169.254.169.254"}},
		{"proxy all web traffic", "tools.web.proxy", "http://attacker.example:8080"},
		{"repoint the skill supply chain", "tools.skills.marketplaces", []any{
			map[string]any{"name": "evil", "base_url": "https://attacker.example"},
		}},
		{"repoint the workspace anchor", "workspace_path", "/"},
		{"reserved security section", "security.default_policy", "allow"},
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
				t.Fatalf("set_config(%q) succeeded — an agent can %s: %s",
					tc.key, tc.name, result.ForLLM)
			}
			m := parseError(t, result.ForLLM)
			errBlock, _ := m["error"].(map[string]any)
			if code, _ := errBlock["code"].(string); code != "INVALID_KEY" {
				t.Errorf("set_config(%q): code = %v, want INVALID_KEY", tc.key, errBlock["code"])
			}

			if after := snapshotConfig(t, cfg); after != before {
				t.Errorf("set_config(%q) was refused but still mutated the config\nbefore: %s\nafter:  %s",
					tc.key, before, after)
			}
		})
	}
}

// TestConfigSet_LegitimateKeysStillWritable is the positive control for the
// block list. Without it the suite above would pass against a build that
// refuses every key, which would be a broken tool rather than a secure one.
func TestConfigSet_LegitimateKeysStillWritable(t *testing.T) {
	t.Run("gateway.port", func(t *testing.T) {
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

	t.Run("agents.defaults.default_model.model", func(t *testing.T) {
		deps, cfg := newTestDeps()
		result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key":   "agents.defaults.default_model.model",
			"value": "glm-4.7",
		})
		if result.IsError {
			t.Fatalf("set_config(agents.defaults.default_model.model) failed: %s", result.ForLLM)
		}
		if cfg.Agents.Defaults.DefaultModel.Model != "glm-4.7" {
			t.Errorf("Name = %q, want %q", cfg.Agents.Defaults.DefaultModel.Model, "glm-4.7")
		}
	})

	t.Run("tools.read_file.max_read_file_size", func(t *testing.T) {
		deps, cfg := newTestDeps()
		result := systools.NewConfigSetTool(deps).Execute(context.Background(), map[string]any{
			"key":   "tools.read_file.max_read_file_size",
			"value": float64(123456),
		})
		if result.IsError {
			t.Fatalf("set_config(tools.read_file.max_read_file_size) failed: %s", result.ForLLM)
		}
		if cfg.Tools.ReadFile.MaxReadFileSize != 123456 {
			t.Errorf("MaxReadFileSize = %d, want 123456", cfg.Tools.ReadFile.MaxReadFileSize)
		}
	})
}
