// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPlatformAuthKeys_ProvisionedFromEnv_OnFreshInstall is the test
// code-review finding 8 asks for directly: a fresh instance — no
// config.json on disk at all, the actual state of a brand-new desktop or
// hosted install before first boot — can have its trust anchor provisioned
// through OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS alone, with no hand-edited
// config.json and no other config-write surface involved (both of which
// refuse the security.* subtree by design — see blocked_paths.go and
// sysagent/tools/config.go).
//
// This also exercises the config.go fix alongside it: loadConfigInternal's
// os.IsNotExist(err) branch did not call env.Parse at all before that fix,
// so every env-tagged field — not just this one — was silently inert on a
// genuinely fresh instance. That bug would have made this route (the fix
// for finding 8) fail on exactly the install it was built for.
func TestPlatformAuthKeys_ProvisionedFromEnv_OnFreshInstall(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("test setup: %s must not exist yet", configPath)
	}

	t.Setenv("OMNIPUS_SECURITY_PLATFORM_AUTH_ISSUER", "https://auth.omnipus.ai")
	t.Setenv("OMNIPUS_SECURITY_PLATFORM_AUTH_CLIENT_ID", "omnipus-desktop")
	t.Setenv("OMNIPUS_SECURITY_PLATFORM_AUTH_INSTANCE_ID", "inst_abc123")
	t.Setenv("OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS",
		"prod-2026-09:EdDSA:MCowBQYDK2VwAyEAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa,"+
			"prod-2026-10:EdDSA:MCowBQYDK2VwAyEAbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig on a fresh install failed: %v", err)
	}

	pa := cfg.Security.PlatformAuth
	if pa.Issuer != "https://auth.omnipus.ai" {
		t.Errorf("Issuer = %q, want https://auth.omnipus.ai", pa.Issuer)
	}
	if pa.ClientID != "omnipus-desktop" {
		t.Errorf("ClientID = %q, want omnipus-desktop", pa.ClientID)
	}
	if pa.InstanceID != "inst_abc123" {
		t.Errorf("InstanceID = %q, want inst_abc123", pa.InstanceID)
	}
	if len(pa.Keys) != 2 {
		t.Fatalf("len(Keys) = %d, want 2", len(pa.Keys))
	}
	want := []PlatformAuthKey{
		{KID: "prod-2026-09", Alg: "EdDSA", PublicKey: "MCowBQYDK2VwAyEAaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{KID: "prod-2026-10", Alg: "EdDSA", PublicKey: "MCowBQYDK2VwAyEAbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}
	for i, w := range want {
		if pa.Keys[i] != w {
			t.Errorf("Keys[%d] = %+v, want %+v", i, pa.Keys[i], w)
		}
	}

	// Confirm the anchor this env var built is one the verifier would
	// actually accept a `kid` lookup against — i.e. it round-tripped as real
	// data, not as an empty/zero-value entry that merely didn't error.
	for _, k := range pa.Keys {
		if k.KID == "" || k.Alg == "" || k.PublicKey == "" {
			t.Errorf("a provisioned key has an empty field: %+v", k)
		}
	}
}

// TestPlatformAuthKey_UnmarshalText covers the format the doc comment on
// PlatformAuthConfig.Keys promises, both the happy path and the refusals —
// a malformed OMNIPUS_SECURITY_PLATFORM_AUTH_KEYS entry must fail the whole
// config load rather than silently produce a half-built or empty key.
func TestPlatformAuthKey_UnmarshalText(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    PlatformAuthKey
		wantErr bool
	}{
		{
			name: "well-formed triple",
			in:   "kid1:EdDSA:cHVibGljS2V5",
			want: PlatformAuthKey{KID: "kid1", Alg: "EdDSA", PublicKey: "cHVibGljS2V5"},
		},
		{
			name: "surrounding whitespace is trimmed per field",
			in:   " kid1 : EdDSA : cHVibGljS2V5 ",
			want: PlatformAuthKey{KID: "kid1", Alg: "EdDSA", PublicKey: "cHVibGljS2V5"},
		},
		{
			name:    "too few fields",
			in:      "kid1:EdDSA",
			wantErr: true,
		},
		{
			name:    "empty kid",
			in:      ":EdDSA:cHVibGljS2V5",
			wantErr: true,
		},
		{
			name:    "empty alg",
			in:      "kid1::cHVibGljS2V5",
			wantErr: true,
		},
		{
			name:    "empty public key",
			in:      "kid1:EdDSA:",
			wantErr: true,
		},
		{
			name:    "empty string",
			in:      "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got PlatformAuthKey
			err := got.UnmarshalText([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("UnmarshalText(%q) = %+v, nil; want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalText(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("UnmarshalText(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// TestPlatformAuthKeys_RoundTripThroughConfigJSON pins the shape config.json
// stores and the gateway reads back. It exists because adding
// PlatformAuthKey.UnmarshalText for the env provisioning route silently broke
// exactly this: encoding/json refuses a JSON OBJECT the moment the
// destination type implements encoding.TextUnmarshaler, so a configured
// instance could no longer load its own trust anchor. The symptom was two
// steps away from the cause — "Omnipus could not record the sign-in on this
// machine" at the end of an otherwise successful callback, because the
// post-write in-memory config refresh failed.
//
// Marshal-then-unmarshal, not a hand-written literal, so the test cannot
// drift from what the writer actually produces.
func TestPlatformAuthKeys_RoundTripThroughConfigJSON(t *testing.T) {
	want := []PlatformAuthKey{
		{KID: "prod-2026-09", Alg: "EdDSA", PublicKey: "MCowBQYDK2VwAyEAcHVibGljS2V5"},
		{KID: "prod-2026-10", Alg: "EdDSA", PublicKey: "MCowBQYDK2VwAyEAc2Vjb25kS2V5"},
	}

	encoded, err := json.Marshal(PlatformAuthConfig{Keys: want})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The object form IS the contract. A regression to the "kid:alg:key"
	// string form would still round-trip, so assert the stored shape too.
	if !strings.Contains(string(encoded), `"kid":"prod-2026-09"`) {
		t.Fatalf("keys are not stored as objects: %s", encoded)
	}

	var got PlatformAuthConfig
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", encoded, err)
	}
	if len(got.Keys) != len(want) {
		t.Fatalf("got %d keys, want %d (%+v)", len(got.Keys), len(want), got.Keys)
	}
	for i := range want {
		if got.Keys[i] != want[i] {
			t.Errorf("key %d = %+v, want %+v", i, got.Keys[i], want[i])
		}
	}
}
