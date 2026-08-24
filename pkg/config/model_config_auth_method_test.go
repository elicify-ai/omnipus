package config

import (
	"strings"
	"testing"
)

// TestModelConfigValidate_AuthMethodClosedSet — ADR-068 FR-003 (X-25):
// ModelConfig.AuthMethod is the closed set `api_key | sign_in` (empty means
// api_key). The retired store-OAuth values `oauth` and `token` are rejected
// by validation, never silently accepted.
func TestModelConfigValidate_AuthMethodClosedSet(t *testing.T) {
	for _, tc := range []struct {
		method  string
		wantErr bool
	}{
		{"", false},
		{"api_key", false},
		{"sign_in", false},
		{"oauth", true},
		{"token", true},
		{"OAuth", true},
		{"device_code", true},
	} {
		t.Run("auth_method="+tc.method, func(t *testing.T) {
			mc := &ModelConfig{Provider: "openai", Model: "gpt-4o", AuthMethod: tc.method}
			err := mc.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("Validate() with auth_method=%q: want error, got nil", tc.method)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() with auth_method=%q: unexpected error %v", tc.method, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "auth_method") {
				t.Errorf("error %q should name auth_method", err)
			}
		})
	}
}

// TestValidateProviders_RejectsRetiredAuthMethod — the same rule reaches the
// config-load path (LoadConfig → ValidateProviders), indexed by provider row.
func TestValidateProviders_RejectsRetiredAuthMethod(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Providers = []*ModelConfig{
		{Provider: "openai", Model: "gpt-4o"},
		{Provider: "anthropic", Model: "claude-sonnet-4-5", AuthMethod: "oauth"},
	}
	err := cfg.ValidateProviders()
	if err == nil {
		t.Fatal("ValidateProviders() = nil, want error for auth_method=oauth")
	}
	if !strings.Contains(err.Error(), "providers[1]") {
		t.Errorf("error %q should index the offending row", err)
	}
}

// TestDefaultConfig_ProvidersPassAuthMethodValidation — the seeded defaults
// must themselves satisfy the closed set, or a fresh install fails to boot.
func TestDefaultConfig_ProvidersPassAuthMethodValidation(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.ValidateProviders(); err != nil {
		t.Fatalf("DefaultConfig().ValidateProviders() = %v", err)
	}
}
