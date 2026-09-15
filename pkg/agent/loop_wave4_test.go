// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"reflect"
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

// TestRateLimiter_InitializedFromConfig verifies that NewAgentLoop constructs
// a non-nil RateLimiterRegistry and exposes it via RateLimiter().
func TestRateLimiter_InitializedFromConfig(t *testing.T) {
	cfg, msgBus := makeRateLimitCfg(t)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	registry := al.RateLimiter()
	if registry == nil {
		t.Fatal("RateLimiter() must not be nil after NewAgentLoop")
	}
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

// TestRateLimiterRegistry_USDCapPathRemoved is the structural regression that
// fails closed if the SEC-26 USD cap sneaks back in. The cap methods ADR-053
// D12 removed must NOT exist on security.RateLimiterRegistry (#540 / S5
// anti-drift).
func TestRateLimiterRegistry_USDCapPathRemoved(t *testing.T) {
	// Sanity: the registry still constructs and exposes GetOrCreate for the
	// surviving sliding-window rate limits.
	reg := security.NewRateLimiterRegistry()
	if reg == nil {
		t.Fatal("RateLimiterRegistry must still construct (sliding-window limits remain)")
	}
	window := reg.GetOrCreate(
		"agent:test:llm_call",
		10,
		0,
		security.ScopeAgent,
		"test",
		"llm_call",
	)
	if window == nil {
		t.Fatal("sliding-window GetOrCreate must still work after D12")
	}

	// The USD cap methods are intentionally absent — see pkg/security/ratelimit.go
	// header doc-comment. If anyone re-introduces them, this reflection guard
	// fails AND a new ADR must justify bringing the USD cap back (S5 anti-drift).
	regType := reflect.TypeOf((*security.RateLimiterRegistry)(nil))
	banned := []string{
		"CheckGlobalCostCap", // the pre-turn gate
		"RecordSpend",        // the post-call recorder
		"SetDailyCostCap",    // the boot wiring
		"GetDailyCost",       // the GET /rate-limits + observability read
		"LoadDailyCost",      // the restore-from-disk path
	}
	for _, name := range banned {
		if _, ok := regType.MethodByName(name); ok {
			t.Errorf("security.RateLimiterRegistry must not have method %q — "+
				"ADR-053 D12 removed the SEC-26 USD cap (S5 anti-drift, #540).", name)
		}
	}
}
