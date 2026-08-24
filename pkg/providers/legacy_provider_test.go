// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestLegacyProvider_ConstructsViaProtocolDispatch — ADR-067 §7 regression
// requirement #4. CreateProvider is the boot-time entry point that turns
// `agents.defaults.default_model` into a transport; the impact table lists it
// as a d=1 caller of CreateProviderFromConfig with no coverage of its own.
func TestLegacyProvider_ConstructsViaProtocolDispatch(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "LEGACY_PROVIDER_TEST_KEY")

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "zai", Model: "glm-5.2"}
	cfg.Providers = []*config.ModelConfig{
		{Provider: "zai", Model: "glm-5.2", APIKeyRef: ref},
	}

	provider, modelID, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}
	got, ok := provider.(*HTTPProvider)
	if !ok {
		t.Fatalf("provider = %T, want *HTTPProvider", provider)
	}
	if want := "https://api.z.ai/api/paas/v4"; got.APIBase() != want {
		t.Errorf("base URL = %q, want the catalog row's %q", got.APIBase(), want)
	}
	if modelID != "glm-5.2" {
		t.Errorf("modelID = %q, want glm-5.2", modelID)
	}
}

// TestLegacyProvider_UnknownDefaultProviderFails — the default model naming a
// retired spelling must fail loudly at boot, not resolve to something else.
func TestLegacyProvider_UnknownDefaultProviderFails(t *testing.T) {
	withFixtureCatalog(t)
	ref := keyRef(t, "LEGACY_PROVIDER_UNKNOWN_KEY")

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "z-ai", Model: "glm-5.2"}
	cfg.Providers = []*config.ModelConfig{
		{Provider: "z-ai", Model: "glm-5.2", APIKeyRef: ref},
	}

	_, _, err := CreateProvider(cfg)
	if err == nil {
		t.Fatal("CreateProvider() = nil error, want an unknown-provider failure")
	}
	if strings.Contains(strings.ReplaceAll(err.Error(), "z-ai", ""), "zai") {
		t.Errorf("error %q offers the canonical id — no rename hint is allowed", err)
	}
}
