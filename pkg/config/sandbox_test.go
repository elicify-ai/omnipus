// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestOmnipusSandboxConfig_ResolvedMode_Precedence verifies that the Mode
// field is the sole source of truth for sandbox state. An empty Mode
// resolves to "off" — the gateway boot path applies the
// "enforce on capable kernels" fresh-install default at a higher layer.
func TestOmnipusSandboxConfig_ResolvedMode_Precedence(t *testing.T) {
	cases := []struct {
		name string
		cfg  OmnipusSandboxConfig
		want string
	}{
		{"explicit enforce", OmnipusSandboxConfig{Mode: "enforce"}, "enforce"},
		{"explicit permissive", OmnipusSandboxConfig{Mode: "permissive"}, "permissive"},
		{"explicit off", OmnipusSandboxConfig{Mode: "off"}, "off"},
		{"zero value resolves off", OmnipusSandboxConfig{}, "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ResolvedMode()
			if got != tc.want {
				t.Errorf("ResolvedMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestOmnipusSandboxConfig_EgressAllowCIDRs_RoundTrip pins the v0.2
// (#155 item 4) field shape: cfg.Sandbox.EgressAllowCIDRs is a []string
// of CIDR ranges marshaled under "egress_allow_cidrs". The field is the
// operator's escape hatch for the default-deny outbound posture — entries
// here are merged into the SSRFChecker's allow-list at boot so internal
// services on RFC1918 addresses can be reached by the gateway-controlled
// HTTP clients.
//
// We assert two things:
//  1. The JSON tag matches what operators write in config.json.
//  2. Empty/missing values round-trip to a nil slice (not a non-nil
//     empty slice), so the strict-block default is preserved.
func TestOmnipusSandboxConfig_EgressAllowCIDRs_RoundTrip(t *testing.T) {
	// JSON tag check — operators write "egress_allow_cidrs" in config.json.
	rt := reflect.TypeOf((*OmnipusSandboxConfig)(nil)).Elem()
	field, ok := rt.FieldByName("EgressAllowCIDRs")
	if !ok {
		t.Fatalf("OmnipusSandboxConfig.EgressAllowCIDRs field missing — v0.2 #155 regression")
	}
	if got := field.Type.String(); got != "[]string" {
		t.Fatalf("EgressAllowCIDRs type = %q, want %q", got, "[]string")
	}
	if got, want := field.Tag.Get("json"), "egress_allow_cidrs,omitempty"; got != want {
		t.Fatalf("EgressAllowCIDRs json tag = %q, want %q", got, want)
	}

	// Round-trip a populated config.
	original := OmnipusSandboxConfig{
		EgressAllowCIDRs: []string{"10.0.0.0/8", "192.168.1.0/24"},
	}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"egress_allow_cidrs":["10.0.0.0/8","192.168.1.0/24"]`) {
		t.Errorf("Marshal output missing egress_allow_cidrs: %s", encoded)
	}

	var decoded OmnipusSandboxConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, want := len(decoded.EgressAllowCIDRs), 2; got != want {
		t.Fatalf("Decoded EgressAllowCIDRs len = %d, want %d", got, want)
	}
	if got, want := decoded.EgressAllowCIDRs[0], "10.0.0.0/8"; got != want {
		t.Errorf("Decoded[0] = %q, want %q", got, want)
	}

	// Empty config defaults: omitempty + nil slice = no key in JSON, and
	// decoding back gives a nil slice (strict-block posture preserved).
	empty := OmnipusSandboxConfig{}
	encodedEmpty, _ := json.Marshal(empty)
	if strings.Contains(string(encodedEmpty), "egress_allow_cidrs") {
		t.Errorf("Empty config should omit egress_allow_cidrs key; got: %s", encodedEmpty)
	}
}

// TestSSRFConfig_AllowInternalRemainsStringList pins the invariant that
// OmnipusSSRFConfig.AllowInternal must stay []string (heterogeneous:
// hostnames, IPs, CIDRs all in one list). Any refactor that introduces
// a bool or a separate allow_internal_cidrs field flips the answer to
// this test and fails loudly.
func TestSSRFConfig_AllowInternalRemainsStringList(t *testing.T) {
	rt := reflect.TypeOf((*OmnipusSSRFConfig)(nil)).Elem()
	field, ok := rt.FieldByName("AllowInternal")
	if !ok {
		t.Fatalf("OmnipusSSRFConfig.AllowInternal field missing — regression")
	}
	if got := field.Type.String(); got != "[]string" {
		t.Fatalf("OmnipusSSRFConfig.AllowInternal type = %q, want %q — regression",
			got, "[]string")
	}
	// Defensively assert no neighboring field was introduced that would
	// compete with AllowInternal. "AllowInternalCIDRs" was the shape the
	// spec's earlier draft proposed and the revision rejected; guard
	// against a future re-introduction.
	if _, leaked := rt.FieldByName("AllowInternalCIDRs"); leaked {
		t.Fatalf("OmnipusSSRFConfig.AllowInternalCIDRs must not exist — regression")
	}
}

// --- SkillTrustLevel UnmarshalJSON ---

// TestSkillTrustLevel_UnmarshalJSON_ValidValues confirms all three canonical
// strings round-trip through UnmarshalJSON without error.
func TestSkillTrustLevel_UnmarshalJSON_ValidValues(t *testing.T) {
	cases := []struct {
		raw  string
		want SkillTrustLevel
	}{
		{`"block_unverified"`, SkillTrustBlockUnverified},
		{`"warn_unverified"`, SkillTrustWarnUnverified},
		{`"allow_all"`, SkillTrustAllowAll},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			var got SkillTrustLevel
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSkillTrustLevel_UnmarshalJSON_UppercaseRejected confirms that
// uppercased variants ("BLOCK_UNVERIFIED") are rejected at decode time.
func TestSkillTrustLevel_UnmarshalJSON_UppercaseRejected(t *testing.T) {
	var got SkillTrustLevel
	if err := json.Unmarshal([]byte(`"BLOCK_UNVERIFIED"`), &got); err == nil {
		t.Fatal("expected error for BLOCK_UNVERIFIED, got nil")
	}
}

// TestSkillTrustLevel_UnmarshalJSON_UnknownRejected confirms that arbitrary
// unknown strings are rejected at decode time.
func TestSkillTrustLevel_UnmarshalJSON_UnknownRejected(t *testing.T) {
	var got SkillTrustLevel
	if err := json.Unmarshal([]byte(`"ridiculous"`), &got); err == nil {
		t.Fatal("expected error for unknown value, got nil")
	}
}

// TestSkillTrustLevel_UnmarshalJSON_EmptyAccepted confirms that the empty
// string is accepted (omitted field in config.json).
func TestSkillTrustLevel_UnmarshalJSON_EmptyAccepted(t *testing.T) {
	var got SkillTrustLevel
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("unexpected error for empty string: %v", err)
	}
}

// --- PromptInjectionLevel UnmarshalJSON ---

// TestPromptInjectionLevel_UnmarshalJSON_ValidValues confirms all three
// canonical strings round-trip through UnmarshalJSON without error.
func TestPromptInjectionLevel_UnmarshalJSON_ValidValues(t *testing.T) {
	cases := []struct {
		raw  string
		want PromptInjectionLevel
	}{
		{`"low"`, PromptInjectionLow},
		{`"medium"`, PromptInjectionMedium},
		{`"high"`, PromptInjectionHigh},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			var got PromptInjectionLevel
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestPromptInjectionLevel_UnmarshalJSON_UppercaseRejected confirms that
// "MEDIUM" is rejected at decode time.
func TestPromptInjectionLevel_UnmarshalJSON_UppercaseRejected(t *testing.T) {
	var got PromptInjectionLevel
	if err := json.Unmarshal([]byte(`"MEDIUM"`), &got); err == nil {
		t.Fatal("expected error for MEDIUM, got nil")
	}
}

// TestPromptInjectionLevel_UnmarshalJSON_UnknownRejected confirms that
// arbitrary unknown strings are rejected at decode time.
func TestPromptInjectionLevel_UnmarshalJSON_UnknownRejected(t *testing.T) {
	var got PromptInjectionLevel
	if err := json.Unmarshal([]byte(`"ridiculous"`), &got); err == nil {
		t.Fatal("expected error for unknown value, got nil")
	}
}

// TestPromptInjectionLevel_UnmarshalJSON_EmptyAccepted confirms that the
// empty string is accepted (config may legitimately omit this field).
func TestPromptInjectionLevel_UnmarshalJSON_EmptyAccepted(t *testing.T) {
	var got PromptInjectionLevel
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("unexpected error for empty string: %v", err)
	}
}

// --- SandboxProfile UnmarshalJSON ---

// TestSandboxProfile_UnmarshalJSON_ValidValues confirms the canonical profile
// strings round-trip through UnmarshalJSON without error.
func TestSandboxProfile_UnmarshalJSON_ValidValues(t *testing.T) {
	cases := []struct {
		raw  string
		want SandboxProfile
	}{
		{`"workspace"`, SandboxProfileWorkspace},
		{`"workspace+net"`, SandboxProfileWorkspaceNet},
		{`"host"`, SandboxProfileHost},
		{`"off"`, SandboxProfileOff},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			var got SandboxProfile
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSandboxProfile_UnmarshalJSON_InvalidValueRejected confirms that unknown
// non-empty values are rejected at decode time so typos fail fast.
func TestSandboxProfile_UnmarshalJSON_InvalidValueRejected(t *testing.T) {
	var got SandboxProfile
	if err := json.Unmarshal([]byte(`"sandboxed"`), &got); err == nil {
		t.Fatal("expected error for unknown sandbox_profile value, got nil")
	}
}

// TestSandboxProfile_UnmarshalJSON_NoneRejected confirms that "none" is no
// longer a valid profile now that SandboxProfileNone has been removed.
func TestSandboxProfile_UnmarshalJSON_NoneRejected(t *testing.T) {
	var got SandboxProfile
	if err := json.Unmarshal([]byte(`"none"`), &got); err == nil {
		t.Fatal("expected error for removed 'none' sandbox_profile, got nil")
	}
}

// TestSandboxProfile_UnmarshalJSON_EmptyAccepted confirms that the empty
// string is accepted (config may legitimately omit this field).
func TestSandboxProfile_UnmarshalJSON_EmptyAccepted(t *testing.T) {
	var got SandboxProfile
	if err := json.Unmarshal([]byte(`""`), &got); err != nil {
		t.Fatalf("unexpected error for empty string: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestSandboxProfile_MarshalJSON_RoundTrip confirms that MarshalJSON produces
// the expected string representation and that Unmarshal recovers the value.
func TestSandboxProfile_MarshalJSON_RoundTrip(t *testing.T) {
	profiles := []SandboxProfile{
		SandboxProfileWorkspace,
		SandboxProfileWorkspaceNet,
		SandboxProfileHost,
		SandboxProfileOff,
	}
	for _, p := range profiles {
		t.Run(string(p), func(t *testing.T) {
			b, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("MarshalJSON(%q) error: %v", p, err)
			}
			var got SandboxProfile
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("UnmarshalJSON(%s) error: %v", b, err)
			}
			if got != p {
				t.Errorf("round-trip: got %q, want %q", got, p)
			}
		})
	}
}

// TestSandboxProfile_IsValid confirms the IsValid predicate for known and
// unknown values including the empty string.
func TestSandboxProfile_IsValid(t *testing.T) {
	cases := []struct {
		p     SandboxProfile
		valid bool
	}{
		{SandboxProfileWorkspace, true},
		{SandboxProfileWorkspaceNet, true},
		{SandboxProfileHost, true},
		{SandboxProfileOff, true},
		{"", true},
		{"none", false}, // "none" was removed — must be invalid
		{"unknown", false},
		{"WORKSPACE", false},
	}
	for _, tc := range cases {
		t.Run(string(tc.p), func(t *testing.T) {
			if got := tc.p.IsValid(); got != tc.valid {
				t.Errorf("IsValid(%q) = %v, want %v", tc.p, got, tc.valid)
			}
		})
	}
}

// TestAllowedPaths_ReadOnlySemanticDocumented pins the invariant that
// AllowedPaths field's doc comment must describe READ access only,
// never write. A drive-by edit that swaps "may read" for "may write"
// (or drops the phrase) would silently broaden the sandbox grant; this
// test makes that regression loud.
func TestAllowedPaths_ReadOnlySemanticDocumented(t *testing.T) {
	data, err := os.ReadFile("sandbox.go")
	if err != nil {
		t.Fatalf("read sandbox.go: %v", err)
	}
	src := string(data)
	idx := strings.Index(src, "AllowedPaths []string")
	if idx < 0 {
		t.Fatal("AllowedPaths field declaration not found in sandbox.go — regression")
	}
	// Walk back up to the preceding blank line and collect the doc-comment block.
	commentBlock := src[:idx]
	if nl := strings.LastIndex(commentBlock, "\n\n"); nl >= 0 {
		commentBlock = commentBlock[nl:]
	}
	if !strings.Contains(commentBlock, "may read") {
		t.Fatalf("AllowedPaths doc comment must contain \"may read\" to pin read-only semantics; got:\n%s",
			commentBlock)
	}
	if strings.Contains(commentBlock, "may write") {
		t.Fatalf("AllowedPaths doc comment must NOT say \"may write\" — regression:\n%s",
			commentBlock)
	}
}

// TestTier3Commands_LoadFromJSON verifies that OmnipusSandboxConfig.Tier3Commands
// round-trips through JSON serialization so operators can extend the baseline
// Tier 3 allow-list via config.json under "sandbox.tier3_commands".
func TestTier3Commands_LoadFromJSON(t *testing.T) {
	raw := `{
		"tier3_commands": ["remix dev", "hugo server"]
	}`
	var cfg OmnipusSandboxConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(cfg.Tier3Commands) != 2 {
		t.Fatalf("expected 2 Tier3Commands, got %d: %v", len(cfg.Tier3Commands), cfg.Tier3Commands)
	}
	if cfg.Tier3Commands[0] != "remix dev" {
		t.Errorf("Tier3Commands[0] = %q, want %q", cfg.Tier3Commands[0], "remix dev")
	}
	if cfg.Tier3Commands[1] != "hugo server" {
		t.Errorf("Tier3Commands[1] = %q, want %q", cfg.Tier3Commands[1], "hugo server")
	}
}

// TestTier3Commands_OmittedFieldYieldsNilSlice verifies that when
// tier3_commands is absent from config.json, the field is nil (not an empty
// slice). This ensures callers that range over it behave correctly.
func TestTier3Commands_OmittedFieldYieldsNilSlice(t *testing.T) {
	raw := `{}`
	var cfg OmnipusSandboxConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if cfg.Tier3Commands != nil {
		t.Errorf("expected Tier3Commands nil when omitted, got %v", cfg.Tier3Commands)
	}
}

// TestTier3Commands_MarshalRoundTrip verifies that the field marshals back to
// the correct JSON key ("tier3_commands") and the values survive a round-trip.
func TestTier3Commands_MarshalRoundTrip(t *testing.T) {
	cfg := OmnipusSandboxConfig{
		Tier3Commands: []string{"remix dev", "hugo server"},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	jsonStr := string(b)
	if !strings.Contains(jsonStr, `"tier3_commands"`) {
		t.Errorf("marshaled JSON missing tier3_commands key: %s", jsonStr)
	}

	var cfg2 OmnipusSandboxConfig
	if err := json.Unmarshal(b, &cfg2); err != nil {
		t.Fatalf("Unmarshal round-trip: %v", err)
	}
	if len(cfg2.Tier3Commands) != 2 {
		t.Fatalf("round-trip: expected 2 commands, got %d", len(cfg2.Tier3Commands))
	}
	if cfg2.Tier3Commands[0] != "remix dev" || cfg2.Tier3Commands[1] != "hugo server" {
		t.Errorf("round-trip values mismatch: %v", cfg2.Tier3Commands)
	}
}

// TestTier3Commands_SingleTokenRejectedAtBoot verifies that validateBootConfig
// rejects Tier3Commands entries that have fewer than two tokens. A single-token
// entry like "bash" or "node" would enable arbitrary execution of that binary
// and is never the operator's intent.
func TestTier3Commands_SingleTokenRejectedAtBoot(t *testing.T) {
	cases := []struct {
		name    string
		entries []string
	}{
		{
			name:    "bare binary bash",
			entries: []string{"bash"},
		},
		{
			name:    "trailing space single token (next )",
			entries: []string{"next "},
		},
		{
			name:    "empty string entry",
			entries: []string{"", "next dev"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Sandbox.Tier3Commands = tc.entries
			if err := validateBootConfig(cfg); err == nil {
				t.Fatalf("validateBootConfig must error for Tier3Commands=%v, got nil", tc.entries)
			}
		})
	}
}

// TestTier3Commands_TwoTokenLoadsFine verifies that a well-formed two-token
// entry passes validateBootConfig without error.
func TestTier3Commands_TwoTokenLoadsFine(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Tier3Commands = []string{"hugo server"}
	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig must accept 'hugo server'; got: %v", err)
	}
}

// TestWorkspaceShellEnabled_DefaultFalse verifies deny-by-default: a nil pointer
// is filled with false by validateBootConfig, matching hard constraint #6.
func TestWorkspaceShellEnabled_DefaultFalse(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sandbox.Experimental.WorkspaceShellEnabled = nil

	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}

	if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
		t.Fatal("expected WorkspaceShellEnabled to be non-nil after validateBootConfig")
	}
	if *cfg.Sandbox.Experimental.WorkspaceShellEnabled {
		t.Error("nil WorkspaceShellEnabled must default to false (deny-by-default), got true")
	}
}

// TestWorkspaceShellEnabled_ExplicitTrue verifies that an explicit true is preserved.
func TestWorkspaceShellEnabled_ExplicitTrue(t *testing.T) {
	cfg := DefaultConfig()
	v := true
	cfg.Sandbox.Experimental.WorkspaceShellEnabled = &v

	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}

	if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil || !*cfg.Sandbox.Experimental.WorkspaceShellEnabled {
		t.Error("explicit true must remain true after validateBootConfig")
	}
}

// TestWorkspaceShellEnabled_ExplicitFalse verifies that an explicit false is preserved.
func TestWorkspaceShellEnabled_ExplicitFalse(t *testing.T) {
	cfg := DefaultConfig()
	v := false
	cfg.Sandbox.Experimental.WorkspaceShellEnabled = &v

	if err := validateBootConfig(cfg); err != nil {
		t.Fatalf("validateBootConfig: %v", err)
	}

	if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil || *cfg.Sandbox.Experimental.WorkspaceShellEnabled {
		t.Error("explicit false must remain false after validateBootConfig")
	}
}
