// Allowlist tests for filterChildEnv (v0.2 #155 item 3).
//
// Verifies the explicit-allowlist semantics introduced when sensitiveEnvKeys
// (3-key denylist) was replaced with allowedChildEnvKeys (closed set + prefix
// allowlist + OMNIPUS_CHILD_* opt-in). The allowlist model fails closed: any
// new sensitive env var added by a future contributor or third-party
// dependency is automatically stripped without anyone having to remember to
// add it to a list.
//
// The boundary tested here is isAllowedChildEnvKey (the predicate). The
// integration test in proc_environ_test.go and hardened_exec_env_test.go
// covers the end-to-end child-environment shape via subprocess re-exec.

package sandbox

import (
	"strings"
	"testing"
)

// TestAllowlist_CoreKeys verifies that the canonical allowlist members all
// pass the predicate. These are the keys a generic build/run child needs
// to operate (PATH, HOME, LANG, ...).
func TestAllowlist_CoreKeys(t *testing.T) {
	allowed := []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TZ", "LANG", "TMPDIR", "TERM"}
	for _, k := range allowed {
		if !isAllowedChildEnvKey(k) {
			t.Errorf("allowlist: %q must be allowed for child child processes", k)
		}
	}
}

// TestAllowlist_LCFamily verifies that the LC_* family is granted as a
// prefix match. Locale variables come in many forms (LC_ALL, LC_CTYPE,
// LC_NUMERIC, LC_TIME, LC_COLLATE, LC_MONETARY, LC_MESSAGES, LC_PAPER,
// LC_NAME, LC_ADDRESS, LC_TELEPHONE, LC_MEASUREMENT, LC_IDENTIFICATION).
// All must pass.
func TestAllowlist_LCFamily(t *testing.T) {
	cases := []string{
		"LC_ALL", "LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE",
		"LC_MONETARY", "LC_MESSAGES", "LC_PAPER", "LC_NAME",
		"LC_ADDRESS", "LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION",
	}
	for _, k := range cases {
		if !isAllowedChildEnvKey(k) {
			t.Errorf("allowlist: %q (LC_*) must be allowed", k)
		}
	}
}

// TestAllowlist_XDGFamily verifies the XDG Base Directory family passes.
func TestAllowlist_XDGFamily(t *testing.T) {
	cases := []string{
		"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
		"XDG_RUNTIME_DIR", "XDG_DATA_DIRS", "XDG_CONFIG_DIRS",
	}
	for _, k := range cases {
		if !isAllowedChildEnvKey(k) {
			t.Errorf("allowlist: %q (XDG_*) must be allowed", k)
		}
	}
}

// TestAllowlist_ChildOptIn verifies the operator escape hatch. Any key with
// the OMNIPUS_CHILD_ prefix is intentionally pass-through; the rename makes
// the trust boundary explicit and grep-able.
func TestAllowlist_ChildOptIn(t *testing.T) {
	cases := []string{
		"OMNIPUS_CHILD_NPM_TOKEN", // pretend npm token forwarded by operator
		"OMNIPUS_CHILD_FOO",       // generic
		"OMNIPUS_CHILD_",          // empty suffix is still on the prefix
	}
	for _, k := range cases {
		if !isAllowedChildEnvKey(k) {
			t.Errorf("allowlist: %q (OMNIPUS_CHILD_*) must be allowed", k)
		}
	}
}

// TestAllowlist_DeniesPreviouslySensitive ensures the three keys that were
// the entire denylist before #155 item 3 are still stripped — but now via
// the allowlist's default-deny rather than explicit naming.
func TestAllowlist_DeniesPreviouslySensitive(t *testing.T) {
	denied := []string{"OMNIPUS_MASTER_KEY", "OMNIPUS_BEARER_TOKEN", "OMNIPUS_KEY_FILE"}
	for _, k := range denied {
		if isAllowedChildEnvKey(k) {
			t.Errorf("allowlist: previously-denylisted key %q must still be stripped", k)
		}
	}
}

// TestAllowlist_DeniesNewSensitive verifies the threat model: a new
// hypothetical sensitive env var must be stripped by default. This is the
// fail-closed property the allowlist exists to guarantee.
func TestAllowlist_DeniesNewSensitive(t *testing.T) {
	// Cases an attacker-or-bug might leak.
	denied := []string{
		"OMNIPUS_SESSION_HMAC",        // hypothetical future internal secret
		"OMNIPUS_CONFIG_PATH",         // even non-secret OMNIPUS_* must not leak
		"AWS_ACCESS_KEY_ID",           // common upstream cred
		"GITHUB_TOKEN",                // CI runner cred
		"OPENROUTER_API_KEY",          // LLM provider cred
		"DATABASE_URL",                // DSN
		"OMNIPUS_BEARER_TOKEN_BACKUP", // fat-fingered duplicate
		"OMNIPUS_KEY_FILE_OLD",        // post-rotation residue
		"PATHX",                       // PATH typo / spoofing attempt
		"PATH_INFO",                   // CGI-leak — not the same as PATH
		"HOMEX",                       // HOME typo / spoofing attempt
	}
	for _, k := range denied {
		if isAllowedChildEnvKey(k) {
			t.Errorf("allowlist: %q must be denied (default-deny under #155 item 3)", k)
		}
	}
}

// TestAllowlist_FilterChildEnvIntegration drives the public ScrubGatewayEnv
// surface end-to-end. It seeds a known-allowlisted key, a known-denied
// previously-sensitive key, and a generic third-party key; after the filter,
// only the allowlisted entries must survive.
func TestAllowlist_FilterChildEnvIntegration(t *testing.T) {
	t.Setenv("OMNIPUS_MASTER_KEY", "must-be-stripped-1")
	t.Setenv("OMNIPUS_KEY_FILE", "must-be-stripped-2")
	t.Setenv("OMNIPUS_BEARER_TOKEN", "must-be-stripped-3")
	t.Setenv("AWS_ACCESS_KEY_ID", "must-be-stripped-4")
	t.Setenv("OMNIPUS_CHILD_NPM_TOKEN", "may-pass-through")
	t.Setenv("OMNIPUS_OTHER_INTERNAL", "must-be-stripped-5")

	out := ScrubGatewayEnv()

	denied := []string{
		"OMNIPUS_MASTER_KEY=",
		"OMNIPUS_KEY_FILE=",
		"OMNIPUS_BEARER_TOKEN=",
		"AWS_ACCESS_KEY_ID=",
		"OMNIPUS_OTHER_INTERNAL=",
	}
	for _, prefix := range denied {
		for _, kv := range out {
			if strings.HasPrefix(kv, prefix) {
				t.Errorf("filterChildEnv: denied key %q present in output: %q", prefix, kv)
			}
		}
	}

	var optInPresent bool
	for _, kv := range out {
		if strings.HasPrefix(kv, "OMNIPUS_CHILD_NPM_TOKEN=") {
			optInPresent = true
		}
	}
	if !optInPresent {
		t.Error("filterChildEnv: opt-in key OMNIPUS_CHILD_NPM_TOKEN must pass through")
	}
}
