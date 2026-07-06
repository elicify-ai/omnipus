// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package policy_test

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/policy"
)

// TestConfigLoader_SecuritySection validates that a valid security config JSON is
// parsed correctly into policy structures at startup.
// Traces to: wave2-security-layer-spec.md line 805 (TestConfigLoader_SecuritySection)
// BDD: Scenario: Declarative JSON Policy Files (spec line 125)
func TestConfigLoader_SecuritySection(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 917 (Dataset: Policy File Examples — valid full policy)
	t.Run("full valid policy parses correctly", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// filepath.IsAbs rejects "/tmp" on Windows (no drive letter).
			// Tracked in #113.
			t.Skip("POSIX-only absolute path (see #113)")
		}
		rawJSON := []byte(`{
			"default_policy": "deny",
			"ssrf": {
				"enabled": true,
				"allow_internal": ["10.0.0.5", "10.0.0.6"]
			},
			"rate_limits": {
				"daily_cost_cap_usd": 50.0
			},
			"audit": {
				"output": "file",
				"redaction": true,
				"redaction_patterns": ["INTERNAL-[0-9]{6}"]
			},
			"policy": {
				"filesystem": {
					"allowed_paths": ["/tmp", "~/omnipus/agents/"]
				},
				"exec": {
					"allowed_binaries": ["git *", "npm *", "python3 *.py"]
				}
			}
		}`)

		cfg, err := policy.ParseSecurityConfig(rawJSON)
		require.NoError(t, err, "valid security config should parse without error")

		t.Run("default_policy is parsed", func(t *testing.T) {
			assert.Equal(t, policy.PolicyDeny, cfg.DefaultPolicy)
		})

		t.Run("SSRF allow_internal is parsed", func(t *testing.T) {
			assert.True(t, cfg.SSRF.IsEnabled())
			assert.Contains(t, cfg.SSRF.AllowInternal, "10.0.0.5")
			assert.Contains(t, cfg.SSRF.AllowInternal, "10.0.0.6")
		})

		t.Run("exec allowed_binaries are parsed", func(t *testing.T) {
			assert.Contains(t, cfg.Policy.Exec.AllowedBinaries, "git *")
			assert.Contains(t, cfg.Policy.Exec.AllowedBinaries, "npm *")
		})

		t.Run("audit config is parsed", func(t *testing.T) {
			assert.Equal(t, "file", cfg.Audit.Output)
			assert.True(t, cfg.Audit.IsRedactionEnabled())
			assert.Contains(t, cfg.Audit.RedactionPatterns, "INTERNAL-[0-9]{6}")
		})

		t.Run("daily cost cap is parsed", func(t *testing.T) {
			assert.Equal(t, 50.0, cfg.RateLimits.DailyCostCapUSD)
		})
	})

	t.Run("minimal valid policy: only default_policy", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 918 (valid minimal policy)
		cfg, err := policy.ParseSecurityConfig([]byte(`{"default_policy": "deny"}`))
		require.NoError(t, err)
		assert.Equal(t, policy.PolicyDeny, cfg.DefaultPolicy)
	})

	t.Run("missing default_policy defaults to deny via GetDefaultPolicy", func(t *testing.T) {
		// Deny-by-default per CLAUDE.md hard constraint #6
		cfg, err := policy.ParseSecurityConfig([]byte(`{}`))
		require.NoError(t, err)
		assert.Equal(t, policy.PolicyDeny, cfg.GetDefaultPolicy(),
			"absent default_policy must return 'deny' via GetDefaultPolicy (deny-by-default)")
	})
}

// TestConfigLoader_MalformedSecurity validates that malformed security config causes
// a parse error with descriptive messages.
// Traces to: wave2-security-layer-spec.md line 806 (TestConfigLoader_MalformedSecurity)
// BDD: Scenario: malformed config (spec line 137, 273)
func TestConfigLoader_MalformedSecurity(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 137 (User Story 8, Acceptance Scenario 3)
	t.Run("invalid default_policy value returns error", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 960 (invalid: "maybe")
		_, err := policy.ParseSecurityConfig([]byte(`{"default_policy": "maybe"}`))
		require.Error(t, err, "invalid default_policy value should return error")
		assert.Contains(t, err.Error(), "default_policy")
		assert.Contains(t, err.Error(), "maybe")
	})

	t.Run("completely invalid JSON returns parse error", func(t *testing.T) {
		_, err := policy.ParseSecurityConfig([]byte(`{invalid json`))
		require.Error(t, err, "malformed JSON must return parse error")
	})

	t.Run("invalid audit output value returns error", func(t *testing.T) {
		_, err := policy.ParseSecurityConfig([]byte(`{"audit": {"output": "database"}}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "audit.output")
	})

	t.Run("invalid exec approval value returns error", func(t *testing.T) {
		_, err := policy.ParseSecurityConfig([]byte(`{"policy": {"exec": {"approval": "prompt"}}}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "approval")
	})

	t.Run("non-absolute filesystem path returns error", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 273 (boundary: relative path in allowed_paths)
		_, err := policy.ParseSecurityConfig([]byte(`{"policy": {"filesystem": {"allowed_paths": ["relative/path"]}}}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "allowed_paths")
	})
}

// TestIsSystemAgent_PrivilegedAgentTypeDetection verifies privileges flow from
// agent type, not from a hardcoded ID (FR-045).
// Traces to: wave2-security-layer-spec.md line 183 (IsSystemAgent exemption)
func TestIsSystemAgent_PrivilegedAgentTypeDetection(t *testing.T) {
	assert.True(t, policy.IsSystemAgent("core"))
	assert.True(t, policy.IsSystemAgent("system"))
	assert.False(t, policy.IsSystemAgent("custom"))
	assert.False(t, policy.IsSystemAgent(""))
}

// TestDMSafetyChecker_OpenChannel validates detection of open DM channel configurations.
// Traces to: wave2-security-layer-spec.md line 803 (TestDMSafetyChecker_OpenChannel)
// BDD: Scenario: Open Telegram channel flagged (spec line 746)
func TestDMSafetyChecker_OpenChannel(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 746 (Scenario: Open Telegram channel flagged)
	t.Run("enabled channel with no allow_from produces a warning", func(t *testing.T) {
		channels := []policy.ChannelConfig{
			{Name: "telegram", Enabled: true, AllowFrom: nil},
		}
		warnings := policy.CheckDMSafety(channels)
		require.NotEmpty(t, warnings, "open Telegram channel should produce at least one warning")
		assert.Contains(t, warnings[0], "Telegram",
			"warning must name the channel")
		assert.Contains(t, warnings[0], "allow_from",
			"warning must mention the allow_from field")
	})

	t.Run("enabled channel with allow_from set produces no warning", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 754 (Scenario: Restricted channel — no warning)
		channels := []policy.ChannelConfig{
			{Name: "telegram", Enabled: true, AllowFrom: []string{"user123"}},
		}
		warnings := policy.CheckDMSafety(channels)
		assert.Empty(t, warnings,
			"channel with allow_from set should not produce a warning")
	})

	t.Run("disabled channel with no allow_from produces no warning", func(t *testing.T) {
		channels := []policy.ChannelConfig{
			{Name: "telegram", Enabled: false, AllowFrom: nil},
		}
		warnings := policy.CheckDMSafety(channels)
		assert.Empty(t, warnings,
			"disabled channel should not be flagged even if allow_from is empty")
	})

	t.Run("multiple open channels each produce a warning", func(t *testing.T) {
		channels := []policy.ChannelConfig{
			{Name: "telegram", Enabled: true, AllowFrom: nil},
			{Name: "slack", Enabled: true, AllowFrom: nil},
		}
		warnings := policy.CheckDMSafety(channels)
		assert.Len(t, warnings, 2,
			"two open channels should produce two warnings")
	})
}
