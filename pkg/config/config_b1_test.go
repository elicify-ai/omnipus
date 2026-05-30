// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — B1 unit tests for validators.
//
// Tests cover:
// - TestValidateRemovedKeys_* — boot rejection of removed keys
// - TestValidateAllowPaths_* — FR-002a AllowReadPaths/AllowWritePaths validation
// - TestValidateBoundsCheck_* — numeric field bounds
// - TestValidateAuthMismatchLogLevel_* — gateway field
// - TestResolveBool_* — *bool resolver
// - TestBootConfigRoundTrip — new fields survive SaveConfig/LoadConfig round-trip

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// : validateRemovedKeys
// ---------------------------------------------------------------------------

func TestValidateRemovedKeys_AbsentKeys_OK(t *testing.T) {
	data := []byte(`{"version":2,"agents":{"defaults":{"workspace":"/tmp"}}}`)
	if err := validateRemovedKeys(data); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestValidateRemovedKeys_RestrictToWorkspacePresent_Rejects(t *testing.T) {
	data := []byte(`{"version":2,"agents":{"defaults":{"restrict_to_workspace":false,"workspace":"/tmp"}}}`)
	err := validateRemovedKeys(data)
	if err == nil {
		t.Fatal("expected error for restrict_to_workspace key, got nil")
	}
	if err.Error() != fr001RemovedKeysMsg {
		t.Fatalf("expected exact FR-001 message, got: %q", err.Error())
	}
}

func TestValidateRemovedKeys_RestrictToWorkspaceTrue_Rejects(t *testing.T) {
	// Key presence (any value) must be rejected.
	data := []byte(`{"version":2,"agents":{"defaults":{"restrict_to_workspace":true}}}`)
	if err := validateRemovedKeys(data); err == nil {
		t.Fatal("expected error for restrict_to_workspace=true key (any value must be rejected)")
	}
}

func TestValidateRemovedKeys_AllowReadOutsideWorkspacePresent_Rejects(t *testing.T) {
	data := []byte(`{"version":2,"agents":{"defaults":{"allow_read_outside_workspace":true}}}`)
	err := validateRemovedKeys(data)
	if err == nil {
		t.Fatal("expected error for allow_read_outside_workspace key, got nil")
	}
	if err.Error() != fr001RemovedKeysMsg {
		t.Fatalf("expected exact FR-001 message, got: %q", err.Error())
	}
}

func TestValidateRemovedKeys_BothKeysPresent_Rejects(t *testing.T) {
	data := []byte(
		`{"version":2,"agents":{"defaults":{"restrict_to_workspace":false,"allow_read_outside_workspace":false}}}`,
	)
	if err := validateRemovedKeys(data); err == nil {
		t.Fatal("expected error when both removed keys are present")
	}
}

func TestValidateRemovedKeys_NoAgentsSection_OK(t *testing.T) {
	data := []byte(`{"version":2,"gateway":{"port":18790}}`)
	if err := validateRemovedKeys(data); err != nil {
		t.Fatalf("expected nil for config without agents section, got %v", err)
	}
}

func TestValidateRemovedKeys_MalformedJSON_OK(t *testing.T) {
	// Malformed JSON should not panic; error will be caught by the later unmarshal.
	data := []byte(`{not-valid-json}`)
	if err := validateRemovedKeys(data); err != nil {
		t.Fatalf("expected nil for malformed JSON (validation deferred), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// FR-002a,,, : validateAllowPaths
// ---------------------------------------------------------------------------

func TestValidateAllowPaths_ValidPatterns_OK(t *testing.T) {
	patterns := []string{
		`^/tmp/agent-workspace/`,
		`^/var/data/uploads/`,
	}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err != nil {
		t.Fatalf("expected nil for valid patterns, got %v", err)
	}
}

func TestValidateAllowPaths_EmptySlice_OK(t *testing.T) {
	if err := validateAllowPaths(nil, "cfg.Tools.AllowReadPaths"); err != nil {
		t.Fatalf("expected nil for empty slice, got %v", err)
	}
}

func TestValidateAllowPaths_MissingCaret_Rejects(t *testing.T) {
	patterns := []string{`/tmp/safe/`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for pattern without leading ^")
	}
}

func TestValidateAllowPaths_EmptyString_Rejects(t *testing.T) {
	patterns := []string{`^/safe/`, ``}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for empty-string pattern (no leading ^)")
	}
}

func TestValidateAllowPaths_InlineFlag_Rejects(t *testing.T) {
	// : inline flags like (?i) must be rejected.
	patterns := []string{`^(?i)/tmp/`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for inline flag (?i)")
	}
}

func TestValidateAllowPaths_InlineFlagS_Rejects(t *testing.T) {
	patterns := []string{`^(?s)foo`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowWritePaths"); err == nil {
		t.Fatal("expected error for inline flag (?s)")
	}
}

func TestValidateAllowPaths_NonASCII_Rejects(t *testing.T) {
	// : non-ASCII characters must be rejected.
	patterns := []string{"^/tmp/é"}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for non-ASCII character in pattern")
	}
}

func TestValidateAllowPaths_KnownBadPath_etc_passwd_Rejects(t *testing.T) {
	// : pattern that matches /etc/passwd must be rejected.
	//.* matches every path including /etc/passwd.
	patterns := []string{`^.*`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for ^.* (matches /etc/passwd)")
	}
}

func TestValidateAllowPaths_Alternation_Rejects(t *testing.T) {
	// test #26b: alternation that includes a known-bad path.
	patterns := []string{`^/etc/(passwd|shadow)`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for alternation matching known-bad path /etc/passwd")
	}
}

func TestValidateAllowPaths_WildcardGroup_Rejects(t *testing.T) {
	//.{1,} matches any non-empty string including known-bad paths.
	patterns := []string{`^.{1,}`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for ^.{1,} (matches /etc/passwd)")
	}
}

func TestValidateAllowPaths_WordCharStar_Rejects(t *testing.T) {
	// ^\w* matches strings like "" (the empty string in known-bad set).
	// "" is in the known-bad fixture, so this must fail.
	patterns := []string{`^\w*`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for ^\\w* (matches empty string in known-bad set)")
	}
}

func TestValidateAllowPaths_AllowWritePaths_AlsoValidated(t *testing.T) {
	// : AllowWritePaths is also validated (not just AllowReadPaths).
	patterns := []string{`/no-caret`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowWritePaths"); err == nil {
		t.Fatal("expected error for write path without leading ^")
	}
}

func TestValidateAllowPaths_InvalidRegexp_Rejects(t *testing.T) {
	// Pattern that does not compile.
	patterns := []string{`^[unclosed`}
	if err := validateAllowPaths(patterns, "cfg.Tools.AllowReadPaths"); err == nil {
		t.Fatal("expected error for pattern that does not compile as regexp")
	}
}

// ---------------------------------------------------------------------------
// : bounds checks via validateBootConfig
// ---------------------------------------------------------------------------

func minimalValidConfig() *Config {
	cfg := DefaultConfig()
	// DefaultConfig sets no Sandbox.MaxConcurrentDevServers (0); boot validator
	// applies default=2. Start with explicit valid values to test bounds rejection.
	return cfg
}

func TestValidateBoundsCheck_MaxConcurrentDevServers_Zero_AppliesDefault(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Sandbox.MaxConcurrentDevServers = 0 // unset → should get default 2
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for MaxConcurrentDevServers=0 (should apply default), got %v", err)
	}
	if cfg.Sandbox.MaxConcurrentDevServers != 2 {
		t.Fatalf("expected default 2, got %d", cfg.Sandbox.MaxConcurrentDevServers)
	}
}

func TestValidateBoundsCheck_MaxConcurrentDevServers_TooLow_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Sandbox.MaxConcurrentDevServers = -1
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for MaxConcurrentDevServers=-1")
	}
}

func TestValidateBoundsCheck_MaxConcurrentDevServers_TooHigh_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Sandbox.MaxConcurrentDevServers = 101
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for MaxConcurrentDevServers=101")
	}
}

func TestValidateBoundsCheck_MaxConcurrentBuilds_Zero_AppliesDefault(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Sandbox.MaxConcurrentBuilds = 0
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for MaxConcurrentBuilds=0 (should apply default), got %v", err)
	}
	if cfg.Sandbox.MaxConcurrentBuilds != 2 {
		t.Fatalf("expected default 2, got %d", cfg.Sandbox.MaxConcurrentBuilds)
	}
}

func TestValidateBoundsCheck_MaxConcurrentBuilds_TooHigh_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Sandbox.MaxConcurrentBuilds = 200
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for MaxConcurrentBuilds=200")
	}
}

func TestValidateBoundsCheck_BuildStaticTimeoutSeconds_Zero_AppliesDefault(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Tools.BuildStatic.TimeoutSeconds = 0
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for TimeoutSeconds=0 (should apply default), got %v", err)
	}
	if cfg.Tools.BuildStatic.TimeoutSeconds != 300 {
		t.Fatalf("expected default 300, got %d", cfg.Tools.BuildStatic.TimeoutSeconds)
	}
}

func TestValidateBoundsCheck_BuildStaticTimeoutSeconds_TooHigh_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Tools.BuildStatic.TimeoutSeconds = 3601
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for TimeoutSeconds=3601")
	}
}

func TestValidateBoundsCheck_BuildStaticMemoryLimitBytes_Zero_AppliesDefault(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Tools.BuildStatic.MemoryLimitBytes = 0
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for MemoryLimitBytes=0 (should apply default), got %v", err)
	}
	if cfg.Tools.BuildStatic.MemoryLimitBytes != 536870912 {
		t.Fatalf("expected default 536870912, got %d", cfg.Tools.BuildStatic.MemoryLimitBytes)
	}
}

func TestValidateBoundsCheck_BuildStaticMemoryLimitBytes_TooLow_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Tools.BuildStatic.MemoryLimitBytes = 1024 // below 64 MiB
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for MemoryLimitBytes below 64 MiB")
	}
}

// ---------------------------------------------------------------------------
// : AuthMismatchLogLevel validator
// ---------------------------------------------------------------------------

func TestValidateAuthMismatchLogLevel_Empty_AppliesDefault(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Gateway.AuthMismatchLogLevel = ""
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for empty AuthMismatchLogLevel (should apply default warn), got %v", err)
	}
	if cfg.Gateway.AuthMismatchLogLevel != "warn" {
		t.Fatalf("expected default 'warn', got %q", cfg.Gateway.AuthMismatchLogLevel)
	}
}

func TestValidateAuthMismatchLogLevel_Debug_OK(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Gateway.AuthMismatchLogLevel = "debug"
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for AuthMismatchLogLevel=debug, got %v", err)
	}
}

func TestValidateAuthMismatchLogLevel_Info_OK(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Gateway.AuthMismatchLogLevel = "info"
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("expected nil for AuthMismatchLogLevel=info, got %v", err)
	}
}

func TestValidateAuthMismatchLogLevel_Invalid_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Gateway.AuthMismatchLogLevel = "error"
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for AuthMismatchLogLevel=error (not in allowed set)")
	}
}

func TestValidateAuthMismatchLogLevel_Uppercase_Rejects(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Gateway.AuthMismatchLogLevel = "WARN"
	if err := validateBootConfig(cfg); err == nil {
		t.Fatal("expected error for AuthMismatchLogLevel=WARN (must be lowercase)")
	}
}

// ---------------------------------------------------------------------------
// : ResolveBool helper
// ---------------------------------------------------------------------------

func TestResolveBool_NilPointer_ReturnsDefault(t *testing.T) {
	if ResolveBool(nil, true) != true {
		t.Fatal("nil *bool with default=true should return true")
	}
	if ResolveBool(nil, false) != false {
		t.Fatal("nil *bool with default=false should return false")
	}
}

func TestResolveBool_TruePointer_ReturnsTrue(t *testing.T) {
	v := true
	if ResolveBool(&v, false) != true {
		t.Fatal("*bool=true should return true regardless of default")
	}
}

func TestResolveBool_FalsePointer_ReturnsFalse(t *testing.T) {
	v := false
	if ResolveBool(&v, true) != false {
		t.Fatal("*bool=false should return false regardless of default")
	}
}

func TestResolveBool_PathGuardAuditFailClosed_NilMeansTrue(t *testing.T) {
	// : PathGuardAuditFailClosed nil → true (safe default).
	var cfg OmnipusSandboxConfig
	if ResolveBool(cfg.PathGuardAuditFailClosed, true) != true {
		t.Fatal("unset PathGuardAuditFailClosed (nil) should resolve to true (fail-closed)")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: new fields survive SaveConfig / LoadConfig
// ---------------------------------------------------------------------------

func TestBootConfigRoundTrip_NewFields(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	// Set new fields explicitly.
	cfg.Sandbox.BrowserEvaluateEnabled = true
	failClosed := false
	cfg.Sandbox.PathGuardAuditFailClosed = &failClosed
	cfg.Sandbox.MaxConcurrentDevServers = 5
	cfg.Sandbox.MaxConcurrentBuilds = 3
	cfg.Sandbox.Tier3Commands = []string{"node server", "python3 -m"}
	cfg.Sandbox.EgressAllowList = []string{"registry.npmjs.org"}
	cfg.Sandbox.DevServerPortRange = PortRange{19000, 19999}
	cfg.Tools.BuildStatic.TimeoutSeconds = 600
	cfg.Tools.BuildStatic.MemoryLimitBytes = 1073741824 // 1 GiB
	cfg.Tools.ServeWorkspace.MaxDurationSeconds = 3600
	cfg.Tools.ServeWorkspace.MinDurationSeconds = 120
	cfg.Gateway.AuthMismatchLogLevel = "info"
	cfg.Gateway.Users = []UserConfig{
		{Username: "alice", Role: UserRoleAdmin, SessionTokenHash: "bcrypthash"},
	}
	// Add an agent with OwnerUsername.
	cfg.Agents.List = []AgentConfig{
		{ID: "agent-1", Name: "My Agent", OwnerUsername: "alice"},
	}

	if err := SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// LoadConfig applies boot validator and returns defaults; verify round-trip.
	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if !loaded.Sandbox.BrowserEvaluateEnabled {
		t.Error("BrowserEvaluateEnabled did not survive round-trip")
	}
	if loaded.Sandbox.PathGuardAuditFailClosed == nil || *loaded.Sandbox.PathGuardAuditFailClosed != false {
		t.Error("PathGuardAuditFailClosed=false did not survive round-trip")
	}
	if loaded.Sandbox.MaxConcurrentDevServers != 5 {
		t.Errorf("MaxConcurrentDevServers: got %d, want 5", loaded.Sandbox.MaxConcurrentDevServers)
	}
	if loaded.Sandbox.MaxConcurrentBuilds != 3 {
		t.Errorf("MaxConcurrentBuilds: got %d, want 3", loaded.Sandbox.MaxConcurrentBuilds)
	}
	if len(loaded.Sandbox.Tier3Commands) != 2 || loaded.Sandbox.Tier3Commands[0] != "node server" {
		t.Errorf("Tier3Commands did not survive round-trip: %v", loaded.Sandbox.Tier3Commands)
	}
	if len(loaded.Sandbox.EgressAllowList) != 1 || loaded.Sandbox.EgressAllowList[0] != "registry.npmjs.org" {
		t.Errorf("EgressAllowList did not survive round-trip: %v", loaded.Sandbox.EgressAllowList)
	}
	if loaded.Sandbox.DevServerPortRange != (PortRange{19000, 19999}) {
		t.Errorf("DevServerPortRange did not survive round-trip: %v", loaded.Sandbox.DevServerPortRange)
	}
	if loaded.Tools.BuildStatic.TimeoutSeconds != 600 {
		t.Errorf("BuildStatic.TimeoutSeconds: got %d, want 600", loaded.Tools.BuildStatic.TimeoutSeconds)
	}
	if loaded.Tools.BuildStatic.MemoryLimitBytes != 1073741824 {
		t.Errorf("BuildStatic.MemoryLimitBytes: got %d, want 1073741824", loaded.Tools.BuildStatic.MemoryLimitBytes)
	}
	if loaded.Tools.ServeWorkspace.MaxDurationSeconds != 3600 {
		t.Errorf("ServeWorkspace.MaxDurationSeconds: got %d, want 3600", loaded.Tools.ServeWorkspace.MaxDurationSeconds)
	}
	if loaded.Tools.ServeWorkspace.MinDurationSeconds != 120 {
		t.Errorf("ServeWorkspace.MinDurationSeconds: got %d, want 120", loaded.Tools.ServeWorkspace.MinDurationSeconds)
	}
	if loaded.Gateway.AuthMismatchLogLevel != "info" {
		t.Errorf("AuthMismatchLogLevel: got %q, want 'info'", loaded.Gateway.AuthMismatchLogLevel)
	}
	if len(loaded.Gateway.Users) < 1 || loaded.Gateway.Users[0].SessionTokenHash != "bcrypthash" {
		t.Errorf("SessionTokenHash did not survive round-trip: %v", loaded.Gateway.Users)
	}
	if len(loaded.Agents.List) < 1 || loaded.Agents.List[0].OwnerUsername != "alice" {
		t.Errorf("OwnerUsername did not survive round-trip: %v", loaded.Agents.List)
	}
}

func TestBootConfigRoundTrip_RemovedKeys_Rejected(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Write raw JSON that contains the removed keys.
	raw := map[string]any{
		"version": CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{
				"restrict_to_workspace": false,
				"workspace":             "/tmp",
			},
		},
	}
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	_, err := LoadConfig(cfgPath)
	if err == nil {
		t.Fatal("expected LoadConfig to fail for config with restrict_to_workspace key")
	}
	if err.Error() != fr001RemovedKeysMsg {
		t.Fatalf("expected exact FR-001 error message, got: %q", err.Error())
	}
}

func TestBootConfigRoundTrip_DefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Write a minimal valid config — no new fields set.
	raw := map[string]any{
		"version": CurrentVersion,
		"agents": map[string]any{
			"defaults": map[string]any{
				"workspace": "/tmp",
			},
		},
	}
	data, _ := json.MarshalIndent(raw, "", "  ")
	if err := os.WriteFile(cfgPath, data, 0o600); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	loaded, err := LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Boot validator must have applied defaults.
	if loaded.Sandbox.MaxConcurrentDevServers != 2 {
		t.Errorf("expected default MaxConcurrentDevServers=2, got %d", loaded.Sandbox.MaxConcurrentDevServers)
	}
	if loaded.Sandbox.MaxConcurrentBuilds != 2 {
		t.Errorf("expected default MaxConcurrentBuilds=2, got %d", loaded.Sandbox.MaxConcurrentBuilds)
	}
	if loaded.Tools.BuildStatic.TimeoutSeconds != 300 {
		t.Errorf("expected default BuildStatic.TimeoutSeconds=300, got %d", loaded.Tools.BuildStatic.TimeoutSeconds)
	}
	if loaded.Tools.BuildStatic.MemoryLimitBytes != 536870912 {
		t.Errorf("expected default MemoryLimitBytes=536870912, got %d", loaded.Tools.BuildStatic.MemoryLimitBytes)
	}
	if loaded.Sandbox.DevServerPortRange != (PortRange{18000, 18999}) {
		t.Errorf("expected default DevServerPortRange [18000,18999], got %v", loaded.Sandbox.DevServerPortRange)
	}
	if loaded.Gateway.AuthMismatchLogLevel != "warn" {
		t.Errorf("expected default AuthMismatchLogLevel=warn, got %q", loaded.Gateway.AuthMismatchLogLevel)
	}
	// PR 5: default egress allow-list expanded to 7 entries for npm + GitHub.
	if len(loaded.Sandbox.EgressAllowList) != 7 {
		t.Errorf(
			"expected default EgressAllowList len=7, got %d: %v",
			len(loaded.Sandbox.EgressAllowList),
			loaded.Sandbox.EgressAllowList,
		)
	}
	// PathGuardAuditFailClosed nil → true via ResolveBool.
	if ResolveBool(loaded.Sandbox.PathGuardAuditFailClosed, true) != true {
		t.Error("expected PathGuardAuditFailClosed nil → true via ResolveBool")
	}
}
