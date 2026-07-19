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

// Wave 4 — SEC-26 rate limiting and cost tracking wiring tests.
//
// These tests prove that:
//   - The RateLimiterRegistry is always initialized in NewAgentLoop.
//   - SetDailyCostCap is applied from config.
//   - CostTracker restores persisted daily cost from disk.
//   - IsSystemAgent returns true only for the system agent.
//   - estimateLLMCallCost returns non-negative values for various models.

func makeRateLimitCfg(t *testing.T) (*config.Config, *bus.MessageBus) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			RateLimits: config.OmnipusRateLimitsConfig{
				DailyCostCapUSD:            5.00,
				MaxAgentLLMCallsPerHour:    100,
				MaxAgentToolCallsPerMinute: 20,
			},
		},
	}
	return cfg, bus.NewMessageBus()
}

// TestRateLimiter_InitializedFromConfig verifies that NewAgentLoop constructs
// a non-nil RateLimiterRegistry and applies the configured daily cost cap.
func TestRateLimiter_InitializedFromConfig(t *testing.T) {
	cfg, msgBus := makeRateLimitCfg(t)
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	registry := al.RateLimiter()
	if registry == nil {
		t.Fatal("RateLimiter() must not be nil after NewAgentLoop")
	}

	// Fresh registry starts at zero cost.
	if got := registry.GetDailyCost(); got != 0.0 {
		t.Errorf("GetDailyCost() = %.4f, want 0.0 on fresh registry", got)
	}

	// Spend just under the $5.00 cap — must be allowed.
	result := registry.CheckGlobalCostCap(4.99, "some-agent")
	if !result.Allowed {
		t.Fatalf("spend below cap should be allowed, got denied: %s", result.PolicyRule)
	}
	// Accumulated is now 4.99; spending $0.02 more pushes over the $5.00 cap.
	result = registry.CheckGlobalCostCap(0.02, "some-agent")
	if result.Allowed {
		t.Fatal("spend over cap should be denied, got allowed")
	}
}

// TestRateLimiter_ZeroCapMeansNoCap verifies that DailyCostCapUSD=0 (default)
// means no cap is applied, regardless of how much is spent.
func TestRateLimiter_ZeroCapMeansNoCap(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:      tmpDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		// Sandbox.RateLimits left at zero — no limits.
	}
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	defer al.Close()

	registry := al.RateLimiter()
	if registry == nil {
		t.Fatal("RateLimiter() must not be nil after NewAgentLoop")
	}

	// Without a cap, any spend must be allowed.
	result := registry.CheckGlobalCostCap(999999.0, "some-agent")
	if !result.Allowed {
		t.Errorf("spend with no cap configured should always be allowed, got denied: %s", result.PolicyRule)
	}
}

// TestIsPrivilegedAgent verifies that IsPrivilegedAgent identifies privileged agent types.
// Privileges flow from agent type (FR-045), not from a hardcoded agent ID.
func TestIsPrivilegedAgent(t *testing.T) {
	cases := []struct {
		agentType string
		want      bool
	}{
		{"core", true},
		// ADR-049 D3 / CRIT-002: System Agents (the Judge) are NO LONGER
		// privileged — IsPrivilegedAgent is core-only, so type:system is
		// rate-limited + cost-capped like any non-core agent (SEC-26).
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

// TestCostTracker_RoundTrip verifies that CostTracker persists daily cost
// through SaveFromRegistry and restores it via LoadIntoRegistry.
func TestCostTracker_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	costPath := dir + "/cost.json"

	// Build a registry with a known accumulated cost.
	reg1 := security.NewRateLimiterRegistry()
	reg1.SetDailyCostCap(10.0)
	reg1.LoadDailyCost(2.71, nowUTCDate())

	// Persist it.
	tracker := security.NewCostTracker(costPath)
	if err := tracker.SaveFromRegistry(reg1); err != nil {
		t.Fatalf("SaveFromRegistry failed: %v", err)
	}

	// Load into a fresh registry.
	reg2 := security.NewRateLimiterRegistry()
	reg2.SetDailyCostCap(10.0)
	tracker2 := security.NewCostTracker(costPath)
	tracker2.LoadIntoRegistry(reg2)

	restored := reg2.GetDailyCost()
	if restored != 2.71 {
		t.Errorf("restored daily cost = %.4f, want 2.71", restored)
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

// TestRateLimiter_RecordSpend_AccumulatesEvenWhenOverCap verifies the Wave 4
// post-review fix: RecordSpend must unconditionally accumulate cost, even
// when it pushes the total past the cap. The previous implementation used
// CheckGlobalCostCap as the recorder, which only incremented on allow — so a
// $0.50 call with $4.99/$5.00 cap was allowed (pre-check passed) but the
// post-call record was denied and the accumulator stayed at $4.99 forever,
// letting every subsequent call sneak past the cap.
func TestRateLimiter_RecordSpend_AccumulatesEvenWhenOverCap(t *testing.T) {
	reg := security.NewRateLimiterRegistry()
	reg.SetDailyCostCap(5.0)
	reg.LoadDailyCost(4.99, security.TodayUTCDate())

	// Record a $0.50 call — this would have been dropped by CheckGlobalCostCap
	// because 4.99 + 0.50 > 5.00.
	reg.RecordSpend(0.50, "alice")

	if got := reg.GetDailyCost(); got != 5.49 {
		t.Errorf("after RecordSpend($0.50) on $4.99 accumulator, got $%.2f, want $5.49", got)
	}

	// The next CheckGlobalCostCap call (simulating the NEXT turn's pre-check)
	// must now deny because the recorded total exceeds the cap.
	result := reg.CheckGlobalCostCap(0.01, "alice")
	if result.Allowed {
		t.Errorf("next CheckGlobalCostCap should deny after accumulator exceeds cap, got allowed")
	}
}

// TestRateLimiter_RecordSpend_PrivilegedAgentExempt verifies that privileged agent
// types (core/system) spend is not counted in the daily accumulator (FR-045).
func TestRateLimiter_RecordSpend_PrivilegedAgentExempt(t *testing.T) {
	reg := security.NewRateLimiterRegistry()
	reg.SetDailyCostCap(5.0)

	reg.RecordSpend(100.0, "core")

	if got := reg.GetDailyCost(); got != 0 {
		t.Errorf("privileged agent spend should not be recorded, got $%.2f, want $0.00", got)
	}
}

// TestRateLimiter_RecordSpend_ZeroAndNegativeIgnored verifies that zero and
// negative costs are no-ops (defensive — negative cost would decrement).
func TestRateLimiter_RecordSpend_ZeroAndNegativeIgnored(t *testing.T) {
	reg := security.NewRateLimiterRegistry()
	reg.LoadDailyCost(1.0, security.TodayUTCDate())

	reg.RecordSpend(0, "alice")
	reg.RecordSpend(-5.0, "alice")

	if got := reg.GetDailyCost(); got != 1.0 {
		t.Errorf("zero/negative spend should be no-op, got $%.2f, want $1.00", got)
	}
}

// nowUTCDate returns today's date in "YYYY-MM-DD" UTC format.
// Used to seed test state with the current day so LoadIntoRegistry accepts it.
func nowUTCDate() string {
	return security.TodayUTCDate()
}
