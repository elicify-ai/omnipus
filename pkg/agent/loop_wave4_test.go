// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers/promptcache"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/security"
)

// Wave 4 — SEC-26 rate-limiting wiring tests.

func makeRateLimitCfg(t *testing.T) (*config.Config, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			RateLimits: config.OmnipusRateLimitsConfig{
				MaxAgentLLMCallsPerHour:    100,
				MaxAgentToolCallsPerMinute: 20,
			},
		},
	}
	return cfg, bus.NewMessageBus()
}

// TestIsPrivilegedAgent verifies that IsPrivilegedAgent identifies privileged
// agent types. Privileges flow from agent type (FR-045), not from a hardcoded
// agent ID. This predicate gates the surviving SEC-26 sliding-window rate
// limits (LLM/hr, tool/min).
func TestIsPrivilegedAgent(t *testing.T) {
	cases := []struct {
		agentType string
		want      bool
	}{
		{"core", true},
		// ADR-049 D3 / CRIT-002: System Agents (the Judge) are NO LONGER
		// privileged — IsPrivilegedAgent is core-only, so type:system is
		// rate-limited like any non-core agent (SEC-26 sliding-window).
		{"system", false},
		{"custom", false},
		{"", false},
		{"CORE", false}, // must be case-sensitive
		{"core-extra", false},
	}
	for _, tc := range cases {
		got := security.IsPrivilegedAgent(tc.agentType)
		if got != tc.want {
			t.Errorf("IsPrivilegedAgent(%q) = %v, want %v", tc.agentType, got, tc.want)
		}
	}
}

// TestEstimateLLMCallCost is replaced by the catalog price. A model with no
// catalog input price is unpriced. It is not the old $3-per-million fallback
// (1,000 input tokens would have cost $0.003). Nil usage still costs nothing;
// TestAccountProviderCall_NilUsageAddsNoCost covers that on production accounting.
func TestEstimateLLMCallCost(t *testing.T) {
	got := promptcache.Price(promptcache.PriceInput{
		ProviderID: "openai",
		Usage: protocoltypes.UsageInfo{
			PromptTokens:     1000,
			CompletionTokens: 200,
		},
	})
	const oldFallback = 0.003
	if got.Cost == oldFallback {
		t.Fatalf("cost = %v, want unpriced rather than the old $3-per-million fallback", got.Cost)
	}
	if got.BillingClass != promptcache.BillingClassUnpriced {
		t.Fatalf("billing class = %q, want %q", got.BillingClass, promptcache.BillingClassUnpriced)
	}
	if got.UnpricedTokens == 0 {
		t.Fatal("unpriced tokens = 0, want the call's tokens kept")
	}
}
