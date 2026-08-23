package providers

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestCreateProviderReturnsHTTPProviderForOpenRouter(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.ModelName = "test-openrouter"
	const keyRef = "FACTORY_TEST_OPENROUTER_KEY"
	t.Setenv(keyRef, "sk-or-test")
	modelCfg := &config.ModelConfig{
		ModelName: "test-openrouter",
		Model:     "openrouter/auto",
		APIBase:   "https://openrouter.ai/api/v1",
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
	cfg.Agents.Defaults.ModelName = "test-codex"
	cfg.Providers = []*config.ModelConfig{
		{
			ModelName: "test-codex",
			Model:     "codex-cli/codex-model",
			Home:      "/tmp/workspace",
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
