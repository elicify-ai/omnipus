// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// TestEstimateLLMCallCost verifies that estimateLLMCallCost produces
// strictly positive costs for known and unknown models.
func TestEstimateLLMCallCost(t *testing.T) {
	cases := []struct {
		model      string
		promptToks int
		outputToks int
	}{
		{"claude-3-5-sonnet-20241022", 1000, 200},
		{"claude-opus-4-5", 500, 100},
		{"gpt-4o", 800, 300},
		{"gemini-1.5-flash", 1000, 500},
		{"unknown-model-xyz", 1000, 200}, // must use conservative default
	}
	for _, tc := range cases {
		u := &providers.UsageInfo{
			PromptTokens:     tc.promptToks,
			CompletionTokens: tc.outputToks,
			TotalTokens:      tc.promptToks + tc.outputToks,
		}
		cost := estimateLLMCallCost(tc.model, u)
		if cost <= 0 {
			t.Errorf("estimateLLMCallCost(%q, %d+%d) = %.6f, want > 0",
				tc.model, tc.promptToks, tc.outputToks, cost)
		}
	}
}

// TestEstimateLLMCallCost_NilUsage verifies nil usage returns 0 without panic.
func TestEstimateLLMCallCost_NilUsage(t *testing.T) {
	cost := estimateLLMCallCost("claude-3-5-sonnet", nil)
	if cost != 0 {
		t.Errorf("estimateLLMCallCost with nil usage = %.6f, want 0", cost)
	}
}
