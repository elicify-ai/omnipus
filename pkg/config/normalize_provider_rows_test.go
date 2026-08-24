// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// T28 (config half; ADR-067 FR-036, A-19) —
// TestConfig_ProviderID_TrimNotFold pins normalizeProviderRows to a
// TRIM-ONLY normalisation. Case is preserved and therefore significant:
// `" ZAI "` becomes `"ZAI"`, an id distinct from the catalog's `"zai"` —
// never silently case-folded into it. Case folding at this boundary would
// be the last surviving alias mechanism ADR-067 removes; the agent-side
// exact-comparison half of FR-036 (findModelConfigForProvider,
// resolveAgentPrimaryProvider) is T067-09's to test.
func TestConfig_ProviderID_TrimNotFold(t *testing.T) {
	cfg := &Config{
		Providers: []*ModelConfig{
			{
				Provider: "  ZAI  ",
				Model:    "\tglm-5.2\n",
				Protocol: " anthropic ",
				APIBase:  " https://example.invalid/v1 ",
			},
			nil, // must not panic
		},
	}

	normalizeProviderRows(cfg)

	got := cfg.Providers[0]
	if got.Provider != "ZAI" {
		t.Errorf("Provider = %q, want %q (trim-only, case preserved)", got.Provider, "ZAI")
	}
	if got.Model != "glm-5.2" {
		t.Errorf("Model = %q, want %q", got.Model, "glm-5.2")
	}
	if got.Protocol != "anthropic" {
		t.Errorf("Protocol = %q, want %q", got.Protocol, "anthropic")
	}
	if got.APIBase != "https://example.invalid/v1" {
		t.Errorf("APIBase = %q, want %q", got.APIBase, "https://example.invalid/v1")
	}

	// A trimmed-but-wrong-case id must NOT collapse to its lowercase form —
	// that would be case folding wearing a trim-only disguise.
	if got.Provider == "zai" {
		t.Errorf("Provider was case-folded to %q; normalizeProviderRows must never fold case", got.Provider)
	}
}

// TestConfig_ProviderID_TrimNotFold_NilConfig guards the nil-cfg short
// circuit — a load path calling this before a config exists must not panic.
func TestConfig_ProviderID_TrimNotFold_NilConfig(t *testing.T) {
	normalizeProviderRows(nil)
}
