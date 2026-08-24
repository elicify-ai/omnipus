package providers

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestCreateProviderReturnsHTTPProviderForOpenRouter(t *testing.T) {
	cfg := config.DefaultConfig()
	// The default is the exact (provider, model) pair of the row below
	// (ADR-068 D14.1) — a CATALOG provider id and a BARE model id.
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
		Provider: "openrouter", Model: "~anthropic/claude-sonnet-latest",
	}
	const keyRef = "FACTORY_TEST_OPENROUTER_KEY"
	t.Setenv(keyRef, "sk-or-test")
	modelCfg := &config.ModelConfig{
		Provider:  "openrouter",
		Model:     "~anthropic/claude-sonnet-latest",
		APIKeyRef: keyRef,
	}
	cfg.Providers = []*config.ModelConfig{modelCfg}

	provider, _, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := provider.(*HTTPProvider); !ok {
		t.Fatalf("provider type = %T, want *HTTPProvider", provider)
	}
}

func TestCreateProviderReturnsCodexCliProviderForCodexCode(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{
		Provider: "codex-cli", Model: "gpt-5.4-codex",
	}
	cfg.Providers = []*config.ModelConfig{
		{
			Provider: "codex-cli",
			Model:    "gpt-5.4-codex",
			Home:     "/tmp/workspace",
		},
	}

	provider, _, err := CreateProvider(cfg)
	if err != nil {
		t.Fatalf("CreateProvider() error = %v", err)
	}

	if _, ok := provider.(*CodexCliProvider); !ok {
		t.Fatalf("provider type = %T, want *CodexCliProvider", provider)
	}
}
