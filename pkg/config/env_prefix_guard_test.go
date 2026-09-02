// env_prefix_guard_test.go — regression coverage for the B2b double-prefix
// bug (fix/uat-v0.1.1-defects).
//
// Background: caarlos0/env (v11) accumulates `envPrefix` tags across nested
// struct levels: Key = (sum of every enclosing envPrefix) + the field's own
// `env` tag (env.go's optionsWithEnvPrefix/parseFieldParams). If a field
// already carries a FULLY-QUALIFIED env tag (e.g.
// `env:"OMNIPUS_TOOLS_BROWSER_HEADLESS"`) AND an enclosing struct level also
// declares an `envPrefix` that duplicates the same prefix, the accumulated
// Key doubles the prefix (e.g.
// "OMNIPUS_TOOLS_BROWSER_OMNIPUS_TOOLS_BROWSER_HEADLESS") — a name nobody
// sets, so the override silently never fires. This shipped ~20 dead env vars
// (BrowserToolConfig's 15 fields + AgentDefaults.SubTurn's 5 fields) with
// zero test coverage to catch it.
//
// TestConfig_EnvKeys_NoDoublePrefix is the general guardrail: it walks every
// env-tagged field on Config via env.GetFieldParams and asserts that any
// field whose OWN tag is already fully-qualified (starts with "OMNIPUS_")
// resolves to a Key identical to its own tag — i.e. no enclosing envPrefix
// contributed anything. That is the precise invariant the double-prefix bug
// violates: a self-qualified tag should never receive additional prefixing.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"os"
	"strings"
	"testing"

	"github.com/caarlos0/env/v11"
)

// TestConfig_EnvKeys_NoDoublePrefix walks every env-tagged field reachable
// from Config and fails if any field's own tag is already a fully-qualified
// "OMNIPUS_..." key but resolves to a DIFFERENT (prefix-augmented) Key. This
// is the guardrail requested by the B2b RCA: it would have failed on the
// original code for all 15 BrowserToolConfig fields, all 5
// AgentDefaults.SubTurn fields, and BrowserToolConfig's embedded
// ToolConfig.Enabled (via the different failure mode covered by
// TestBrowserToolConfig_EmbeddedToolConfig_RequiresEnvPrefix below).
func TestConfig_EnvKeys_NoDoublePrefix(t *testing.T) {
	fps, err := env.GetFieldParams(&Config{})
	if err != nil {
		t.Fatalf("env.GetFieldParams(&Config{}) error: %v", err)
	}
	if len(fps) == 0 {
		t.Fatal("env.GetFieldParams(&Config{}) returned zero fields — env tag walk is broken")
	}

	var failures []string
	for _, fp := range fps {
		if fp.OwnKey == "" || fp.OwnKey == "-" {
			continue
		}
		// A field whose own tag is already namespaced under OMNIPUS_ must
		// resolve to itself: no enclosing envPrefix should have added
		// anything in front of it. If Key != OwnKey here, some ancestor
		// struct level applied an envPrefix on top of an already-qualified
		// tag — exactly the double-prefix bug.
		if strings.HasPrefix(fp.OwnKey, "OMNIPUS_") && fp.Key != fp.OwnKey {
			failures = append(failures, fp.OwnKey+" -> resolved Key="+fp.Key)
		}
		// Belt-and-braces: also reject any Key that contains the same
		// non-trivial ("OMNIPUS_...") prefix substring twice outright,
		// regardless of whether OwnKey itself was qualified. This catches
		// the case where a *relative* own tag (e.g. "ENABLED") sits behind
		// TWO stacked envPrefix declarations that duplicate each other
		// (the historical BrowserToolConfig.ToolConfig.Enabled failure mode
		// before the outer ToolsConfig.Browser envPrefix was removed).
		if dup := findDuplicatedOmnipusPrefix(fp.Key); dup != "" {
			failures = append(failures, fp.OwnKey+" -> resolved Key="+fp.Key+" (prefix segment "+dup+" repeated)")
		}
	}

	if len(failures) > 0 {
		t.Fatalf("found %d env key(s) with a duplicated prefix (double-envPrefix bug):\n  %s",
			len(failures), strings.Join(failures, "\n  "))
	}
}

// findDuplicatedOmnipusPrefix returns the longest "OMNIPUS_..._" prefix
// segment of key that appears a second time immediately following its own
// first occurrence (i.e. key == p + p + rest), or "" if none does. This
// mirrors exactly how caarlos0/env accumulates opts.Prefix across nested
// envPrefix tags: a duplicated prefix always shows up as an exact
// self-concatenation at the start of the string.
func findDuplicatedOmnipusPrefix(key string) string {
	if !strings.HasPrefix(key, "OMNIPUS_") {
		return ""
	}
	// Try every underscore-delimited split point as a candidate prefix
	// length, longest first, so we report the most specific duplicated
	// segment.
	for i := len(key) - 1; i > 0; i-- {
		if key[i] != '_' {
			continue
		}
		prefix := key[:i+1]
		if len(prefix) == 0 || len(prefix)*2 > len(key) {
			continue
		}
		if key[:len(prefix)*2] == prefix+prefix {
			return prefix
		}
	}
	return ""
}

// TestBrowserToolConfig_EnvKeys_NoDoublePrefix is the narrow, fast-failing
// regression test for the exact bug reported in B2b: every BrowserToolConfig
// field must resolve to its own literal env tag with no extra prefix, when
// parsed as part of the full Config tree (mirrors real LoadConfig usage,
// not an isolated struct).
func TestBrowserToolConfig_EnvKeys_NoDoublePrefix(t *testing.T) {
	fps, err := env.GetFieldParams(&Config{})
	if err != nil {
		t.Fatalf("env.GetFieldParams error: %v", err)
	}

	want := map[string]string{
		"OMNIPUS_TOOLS_BROWSER_HEADLESS":             "",
		"OMNIPUS_TOOLS_BROWSER_CDP_URL":              "",
		"OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT":         "",
		"OMNIPUS_TOOLS_BROWSER_MAX_TABS":             "",
		"OMNIPUS_TOOLS_BROWSER_PERSIST_SESSION":      "",
		"OMNIPUS_TOOLS_BROWSER_PROFILE_DIR":          "",
		"OMNIPUS_TOOLS_BROWSER_EXEC_PATH":            "",
		"OMNIPUS_TOOLS_BROWSER_MAX_TOTAL_TABS":       "",
		"OMNIPUS_TOOLS_BROWSER_LIVE_VIEW_ENABLED":    "",
		"OMNIPUS_TOOLS_BROWSER_TAKE_CONTROL_ENABLED": "",
		"OMNIPUS_TOOLS_BROWSER_WEBRTC_ENABLED":       "",
		"OMNIPUS_TOOLS_BROWSER_PREFER_PACKAGED":      "",
		"OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME":    "",
		"OMNIPUS_TOOLS_BROWSER_WEBRTC_STUN_SERVER":   "",
	}

	seen := map[string]bool{}
	for _, fp := range fps {
		if _, ok := want[fp.OwnKey]; ok {
			seen[fp.OwnKey] = true
			if fp.Key != fp.OwnKey {
				t.Errorf("BrowserToolConfig field env:%q resolved to Key=%q, want %q (double-prefix regression)",
					fp.OwnKey, fp.Key, fp.OwnKey)
			}
		}
	}
	for k := range want {
		if !seen[k] {
			t.Errorf("expected env tag %q not found via env.GetFieldParams(&Config{}) — field renamed or removed?", k)
		}
	}
}

// TestBrowserToolConfig_EmbeddedToolConfig_RequiresEnvPrefix locks in the
// one deliberate exception documented on BrowserToolConfig: the embedded
// ToolConfig.Enabled field (relative env:"ENABLED" tag, shared by every
// other ToolConfig embedder) must still resolve to
// "OMNIPUS_TOOLS_BROWSER_ENABLED" — which requires KEEPING the envPrefix
// tag on BrowserToolConfig's embedded ToolConfig field. A well-intentioned
// future cleanup that removes that "redundant-looking" envPrefix (it looks
// identical to the one already removed from the outer ToolsConfig.Browser
// field) would silently break OMNIPUS_TOOLS_BROWSER_ENABLED. This test
// exists specifically to catch that regression.
func TestBrowserToolConfig_EmbeddedToolConfig_RequiresEnvPrefix(t *testing.T) {
	fps, err := env.GetFieldParams(&Config{})
	if err != nil {
		t.Fatalf("env.GetFieldParams error: %v", err)
	}

	found := false
	for _, fp := range fps {
		if fp.OwnKey != "ENABLED" {
			continue
		}
		if fp.Key == "OMNIPUS_TOOLS_BROWSER_ENABLED" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no ToolConfig.Enabled field resolved to OMNIPUS_TOOLS_BROWSER_ENABLED — " +
			"BrowserToolConfig's embedded ToolConfig lost its envPrefix tag (regression)")
	}

	// Functional round-trip: setting the env var must actually flip the
	// field via a real LoadConfig-style env.Parse call.
	t.Setenv("OMNIPUS_TOOLS_BROWSER_ENABLED", "true")
	cfg := DefaultConfig()
	cfg.Tools.Browser.Enabled = false
	if err := env.Parse(cfg); err != nil {
		t.Fatalf("env.Parse error: %v", err)
	}
	if !cfg.Tools.Browser.Enabled {
		t.Error("Tools.Browser.Enabled did not flip to true under OMNIPUS_TOOLS_BROWSER_ENABLED=true")
	}
}

// TestBrowserToolConfig_EnvOverride_ActuallyOverridesJSON is the REVERT-PROOF
// test for B2b: unlike the pre-existing TestLoadConfig_BrowserPreferPackaged /
// TestLoadConfig_BrowserTrustPathChrome tests (which set env and JSON to the
// SAME value and therefore pass even when the env override is completely
// dead), this test sets JSON to one value and the env var to the OPPOSITE
// value, then asserts the env var wins. This is exactly the scenario that
// was silently broken before the B2b fix: on the unfixed code, LoadConfig
// would return TrustPathChrome=true (the JSON value) even with
// OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME=false set, because the env lookup
// key was double-prefixed and never matched.
func TestBrowserToolConfig_EnvOverride_ActuallyOverridesJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"
	// JSON says true; env var (below) says false. Env must win per
	// config.go's documented precedence (env.Parse runs after JSON load,
	// SetDefaultsForZeroValuesOnly is not set so env unconditionally
	// overwrites).
	configJSON := `{"version":1,"tools":{"browser":{"trust_path_chrome":true,"headless":true,"max_tabs":9,"webrtc_enabled":true}}}`
	if err := writeTestConfigFile(t, configPath, configJSON); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME", "false")
	t.Setenv("OMNIPUS_TOOLS_BROWSER_HEADLESS", "false")
	t.Setenv("OMNIPUS_TOOLS_BROWSER_MAX_TABS", "42")
	t.Setenv("OMNIPUS_TOOLS_BROWSER_WEBRTC_ENABLED", "false")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.Tools.Browser.TrustPathChrome {
		t.Error(
			"TrustPathChrome=true: env var OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME=false did not override JSON true (B2b regression)",
		)
	}
	if cfg.Tools.Browser.Headless {
		t.Error(
			"Headless=true: env var OMNIPUS_TOOLS_BROWSER_HEADLESS=false did not override JSON true (B2b regression)",
		)
	}
	if cfg.Tools.Browser.MaxTabs != 42 {
		t.Errorf(
			"MaxTabs=%d: env var OMNIPUS_TOOLS_BROWSER_MAX_TABS=42 did not override JSON 9 (B2b regression)",
			cfg.Tools.Browser.MaxTabs,
		)
	}
	if cfg.Tools.Browser.WebRTCEnabled {
		t.Error(
			"WebRTCEnabled=true: env var OMNIPUS_TOOLS_BROWSER_WEBRTC_ENABLED=false did not override JSON true (B2b regression)",
		)
	}
}

// TestAgentDefaultsSubTurn_EnvOverride_ActuallyOverridesJSON is the
// REVERT-PROOF test for the AgentDefaults.SubTurn half of B2b.
func TestAgentDefaultsSubTurn_EnvOverride_ActuallyOverridesJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"
	configJSON := `{"version":1,"agents":{"defaults":{"subturn":{"max_depth":3,"max_concurrent":5}}}}`
	if err := writeTestConfigFile(t, configPath, configJSON); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH", "9")
	t.Setenv("OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_CONCURRENT", "11")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.Agents.Defaults.SubTurn.MaxDepth != 9 {
		t.Errorf(
			"SubTurn.MaxDepth=%d: env var OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_DEPTH=9 did not override JSON 3 (B2b regression)",
			cfg.Agents.Defaults.SubTurn.MaxDepth,
		)
	}
	if cfg.Agents.Defaults.SubTurn.MaxConcurrent != 11 {
		t.Errorf(
			"SubTurn.MaxConcurrent=%d: env var OMNIPUS_AGENTS_DEFAULTS_SUBTURN_MAX_CONCURRENT=11 did not override JSON 5 (B2b regression)",
			cfg.Agents.Defaults.SubTurn.MaxConcurrent,
		)
	}
}

// TestReadFileToolConfig_EnvTagsWired closes the "separate lesser defect"
// noted in the B2b RCA: ReadFileToolConfig had no env tags at all, so the
// envPrefix on the outer ToolsConfig.ReadFile field was pure dead weight.
func TestReadFileToolConfig_EnvTagsWired(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.json"
	configJSON := `{"version":1,"tools":{"read_file":{"enabled":true,"max_read_file_size":1000}}}`
	if err := writeTestConfigFile(t, configPath, configJSON); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("OMNIPUS_TOOLS_READ_FILE_ENABLED", "false")
	t.Setenv("OMNIPUS_TOOLS_READ_FILE_MAX_READ_FILE_SIZE", "5000")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}

	if cfg.Tools.ReadFile.Enabled {
		t.Error("ReadFile.Enabled=true: env var OMNIPUS_TOOLS_READ_FILE_ENABLED=false did not override JSON true")
	}
	if cfg.Tools.ReadFile.MaxReadFileSize != 5000 {
		t.Errorf(
			"ReadFile.MaxReadFileSize=%d: env var OMNIPUS_TOOLS_READ_FILE_MAX_READ_FILE_SIZE=5000 did not override JSON 1000",
			cfg.Tools.ReadFile.MaxReadFileSize,
		)
	}
}

// writeTestConfigFile is a tiny local helper (kept file-local to avoid
// perturbing any shared test helper naming elsewhere in the package).
func writeTestConfigFile(t *testing.T, path, contents string) error {
	t.Helper()
	return os.WriteFile(path, []byte(contents), 0o600)
}
