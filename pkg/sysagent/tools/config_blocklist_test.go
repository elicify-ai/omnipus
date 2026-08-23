// Omnipus — System Agent Tool Tests: set_config security block list
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// This file is deliberately an INTERNAL test (package systools, not
// systools_test): it walks the blockedConfigKeys table itself so the policy is
// verified as data. A black-box test can only ever check the handful of keys
// someone remembered to type out; this one fails the moment an entry is added
// without a reason, or a match form (child key, array index) stops being
// covered.
package systools

import (
	"strings"
	"testing"
)

// TestBlockedConfigKeys_TableIsWellFormed pins the shape of the policy table:
// every entry must carry a key and a human-readable reason, and no entry may be
// shadowed by an earlier, broader entry (which would make its reason dead text
// the caller never sees).
func TestBlockedConfigKeys_TableIsWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(blockedConfigKeys))
	for i, b := range blockedConfigKeys {
		if strings.TrimSpace(b.Key) == "" {
			t.Errorf("blockedConfigKeys[%d]: empty Key", i)
			continue
		}
		if strings.TrimSpace(b.Reason) == "" {
			t.Errorf("blockedConfigKeys[%d] (%q): empty Reason — the rejection must tell the "+
				"caller what it hit and where the setting really lives", i, b.Key)
		}
		if seen[b.Key] {
			t.Errorf("blockedConfigKeys[%d]: duplicate Key %q", i, b.Key)
		}
		seen[b.Key] = true

		// Shadowing check: an earlier entry that is a prefix-parent of this one
		// means this entry's reason can never be reached.
		for j := 0; j < i; j++ {
			if strings.HasPrefix(b.Key, blockedConfigKeys[j].Key+".") {
				t.Errorf("blockedConfigKeys[%d] (%q) is shadowed by the broader entry [%d] (%q) — "+
					"its Reason is unreachable", i, b.Key, j, blockedConfigKeys[j].Key)
			}
		}
	}
}

// TestValidateConfigKey_EveryBlockedKeyIsRefused walks the table and asserts
// that each entry is refused in all three match forms: the bare key, a child
// key, and an array index. This is what makes a subtree block (e.g. "sandbox")
// meaningful — the danger is almost always in the children.
func TestValidateConfigKey_EveryBlockedKeyIsRefused(t *testing.T) {
	for _, b := range blockedConfigKeys {
		for _, candidate := range []string{
			b.Key,
			b.Key + ".some_child",
			b.Key + ".some_child.grandchild",
			b.Key + "[0]",
		} {
			err := validateConfigKey(nil, candidate)
			if err == nil {
				t.Errorf("validateConfigKey(%q) = nil, want refusal (blocked by %q)", candidate, b.Key)
				continue
			}
			if !strings.Contains(err.Error(), b.Reason) {
				t.Errorf("validateConfigKey(%q) error %q does not carry the block reason %q",
					candidate, err.Error(), b.Reason)
			}
		}
	}
}

// TestValidateConfigKey_NamedEscalationsRefused is the explicit, human-readable
// list of the privilege escalations this block list exists to close. It
// duplicates coverage from the table walk on purpose: if someone deletes an
// entry from blockedConfigKeys, the table walk still passes (it walks whatever
// is left) and only this test fails.
func TestValidateConfigKey_NamedEscalationsRefused(t *testing.T) {
	escalations := []struct {
		key  string
		what string
	}{
		{"sandbox.mode", "turn the kernel sandbox off"},
		{"sandbox.god_mode", "floor every tool policy at allow"},
		{"sandbox.god_mode_allowed", "persist god-mode authorization"},
		{"sandbox.tool_policies", "rewrite its own tool policies"},
		{"sandbox.tool_policies.bash", "grant itself shell access"},
		{"sandbox.shell_deny_patterns", "empty the shell guard"},
		{"sandbox.allowed_paths", "widen the filesystem write boundary"},
		{"sandbox.allowed_exec_paths", "widen what it may execute"},
		{"sandbox.filesystem_model", "switch reads/exec from confined to open"},
		{"sandbox.egress_allow_cidrs", "widen the internal-network egress deny"},
		{"sandbox.ssrf.enabled", "switch SSRF protection off"},
		{"sandbox.ssrf.allow_internal", "exempt internal hosts from SSRF"},
		{"sandbox.audit_log", "switch the audit log off"},
		{"sandbox.skill_trust", "disable skill hash verification"},
		{"sandbox.prompt_injection_level", "weaken the prompt-injection guard"},
		{"gateway.dev_mode_bypass", "disable gateway authentication"},
		{"gateway.users", "mint itself an API bearer credential"},
		{"gateway.trust_xff", "spoof the client IP auth rate limiting keys on"},
		{"gateway.public_url", "widen the CSP/CORS/WS origin"},
		{"gateway.tool_approval_timeout", "defeat the human approval gate"},
		{"gateway.tool_approval_max_pending", "defeat the human approval gate"},
		{"gateway.auth_mismatch_log_level", "silence failed-auth logging"},
		{"gateway.validate_inbound", "disable inbound contract validation"},
		{"tools.allow_read_paths", "widen the file-tool read boundary"},
		{"tools.allow_write_paths", "widen the file-tool write boundary"},
		{"tools.filter_sensitive_data", "stop secrets being redacted from tool results"},
		{"tools.filter_min_length", "neuter the same redaction filter"},
		{"tools.exec.allowed_binaries", "widen the exec allow-list"},
		{"tools.exec.approval", "drop the exec approval requirement"},
		{"tools.exec.enable_proxy", "bypass the egress proxy"},
		{"tools.mcp.servers", "register an arbitrary program as an MCP server"},
		{"tools.browser.exec_path", "make the gateway launch an arbitrary binary"},
		{"tools.browser.cdp_url", "attach the browser to an attacker-controlled endpoint"},
		{"tools.browser.evaluate_enabled", "enable arbitrary in-page JavaScript"},
		{"tools.cron.allow_command", "let scheduled jobs run shell commands"},
		{"tools.web.private_host_whitelist", "exempt private hosts from SSRF"},
		{"tools.web.proxy", "route all outbound web traffic through a chosen proxy"},
		{"tools.skills.marketplaces", "repoint the skill supply chain"},
		{"agents.list", "rewrite the agent roster"},
		{"security.anything", "reserved security section"},
		{"workspace_path", "repoint the workspace confinement anchor"},
	}
	for _, e := range escalations {
		t.Run(e.key, func(t *testing.T) {
			if err := validateConfigKey(nil, e.key); err == nil {
				t.Fatalf("validateConfigKey(%q) = nil — an agent could %s", e.key, e.what)
			}
		})
	}
}

// TestValidateConfigKey_LegitimateKeysStillAccepted is the positive control.
// Without it, a block list that refuses literally every key would pass every
// test above.
func TestValidateConfigKey_LegitimateKeysStillAccepted(t *testing.T) {
	allowed := []string{
		"gateway.port",
		"gateway.host",
		"gateway.log_level",
		"gateway.hot_reload",
		"gateway.preview_enabled",
		"agents.defaults.default_model.model",
		"agents.defaults.default_agent_id",
		"tools.read_file.max_read_file_size",
		"tools.web.brave.max_results",
		"tools.manifest.compressed",
		"tools.delegate.require_parent_agent_id",
		"channels.discord.enabled",
		"devices.enabled",
	}
	for _, key := range allowed {
		t.Run(key, func(t *testing.T) {
			if err := validateConfigKey(nil, key); err != nil {
				t.Fatalf("validateConfigKey(%q) = %v, want nil — the block list must not "+
					"collaterally freeze ordinary settings", key, err)
			}
		})
	}
}

// TestValidateConfigKey_BlockedSectionsKeepAWritableSibling guards the specific
// failure mode of a subtree block: freezing a whole section that still has
// legitimate, non-security settings in it. sandbox is deliberately absent —
// that subtree is blocked in full and has no non-security key.
func TestValidateConfigKey_BlockedSectionsKeepAWritableSibling(t *testing.T) {
	siblings := map[string]string{
		"gateway": "gateway.port",
		"agents":  "agents.defaults.default_model.model",
		"tools":   "tools.read_file.max_read_file_size",
	}
	for section, sibling := range siblings {
		if err := validateConfigKey(nil, sibling); err != nil {
			t.Errorf("section %q has no writable sibling: validateConfigKey(%q) = %v",
				section, sibling, err)
		}
	}
}
