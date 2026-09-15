// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// newTestRestAPIWithClawHub builds a restAPI whose config enables (or disables)
// the ClawHub skill marketplace and optionally sets a GitHub registry token ref.
// Used to exercise the marketplace-gating paths (GET /skills/marketplace plus the
// 409 refusal on search/install when no marketplace is enabled).
func newTestRestAPIWithClawHub(t *testing.T, clawhubEnabled bool, githubTokenRef string) *restAPI {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "") // disable auth in tests

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	cfg.Tools.Skills.Marketplaces = []config.MarketplaceConfig{
		{Name: "clawhub", Type: "clawhub", Enabled: clawhubEnabled, BaseURL: "https://clawhub.ai"},
	}
	if githubTokenRef != "" {
		cfg.Tools.Skills.Marketplaces = append(cfg.Tools.Skills.Marketplaces, config.MarketplaceConfig{
			Name: "github", Type: "github", Enabled: true, TokenRef: githubTokenRef,
		})
	}
	seedTestAgents(cfg)

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})

	return &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		homePath:      tmpDir,
	}
}
