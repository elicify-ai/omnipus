// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// CreateProvider creates a provider based on the configuration.
// It uses the model_list configuration (new format) to create providers.
// The old providers config is automatically converted to model_list during config loading.
// Returns the provider, the model ID to use, and any error.
func CreateProvider(cfg *config.Config) (LLMProvider, string, error) {
	pair := cfg.Agents.Defaults.DefaultModel
	model := pair.String()

	// Must have model_list at this point
	if len(cfg.Providers) == 0 {
		return nil, "", fmt.Errorf("no providers configured. Please add entries to model_list in your config")
	}

	// Resolve the default (provider, model) pair (ADR-068 D14.1): a row
	// whose own Model field equals the pair exactly, OR a row that legitimately
	// SERVES it via its Models[] list, the X-13/X-17/X-22 custom/local
	// bypass, or the served catalog (ResolveDefaultModelRow — see its doc
	// for why config.GetModelConfig alone is not enough here). Uses the
	// process-installed catalog: the same document the gateway's own
	// default-model PUT validates against (providers.SetCatalog installs it
	// at boot from the identical variable gateway.go hands to restAPI), so a
	// pair that PUT accepted is guaranteed to resolve here too.
	modelCfg, ok := ResolveDefaultModelRow(cfg, ProviderCatalog(), pair.Provider, pair.Model)
	if !ok {
		return nil, "", fmt.Errorf("default model %q not found in providers", model)
	}

	// Inject global workspace if not set in model config
	if modelCfg.Home == "" {
		modelCfg.Home = cfg.AgentHomeBasePath()
	}

	// Use factory to create provider
	provider, modelID, err := CreateProviderFromConfig(modelCfg)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create provider for model %q: %w", model, err)
	}

	return provider, modelID, nil
}
