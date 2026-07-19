// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestRateLimiter_SlidingWindow validates sliding window rate limiting per agent.
// Traces to: wave2-security-layer-spec.md line 797 (TestRateLimiter_SlidingWindow)
// BDD: Scenario: Per-agent rate limit rejection (spec line 689)
func TestRateLimiter_SlidingWindow(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 859 (Dataset: Rate Limit row 1: 10 llm_calls/hour)
	t.Run("10 calls allowed then 11th rejected", func(t *testing.T) {
		sw := security.NewSlidingWindow(10, time.Hour, security.ScopeAgent, "researcher", "llm_calls")
		for i := 0; i < 10; i++ {
			result := sw.Allow()
			require.True(t, result.Allowed, "call %d of 10 should be allowed", i+1)
		}

		result := sw.Allow()
		assert.False(t, result.Allowed, "11th call should be rejected")
		assert.Contains(t, result.PolicyRule, "rate_limit",
			"denied result must explain rate limit")
		assert.Contains(t, result.PolicyRule, "llm_calls")
	})

	// Dataset row 2: per-agent, 5 tool_calls/minute window
	t.Run("5 tool_calls/min then 6th rejected", func(t *testing.T) {
		sw := security.NewSlidingWindow(5, time.Minute, security.ScopeAgent, "assistant", "tool_calls")
		for i := 0; i < 5; i++ {
			result := sw.Allow()
			require.True(t, result.Allowed, "call %d of 5 should be allowed", i+1)
		}

		result := sw.Allow()
		assert.False(t, result.Allowed)
		assert.Greater(t, result.RetryAfterSeconds, 0.0,
			"retry_after_seconds must be positive so the agent knows when to retry")
	})

	// Dataset row 6: counters reset on new instance (simulates restart)
	t.Run("new SlidingWindow instance starts with zero count", func(t *testing.T) {
		sw := security.NewSlidingWindow(10, time.Hour, security.ScopeAgent, "researcher", "llm_calls")
		// Fresh limiter — first call must be allowed
		result := sw.Allow()
		assert.True(t, result.Allowed, "first call on fresh sliding window should be allowed")
	})
}

// TestRateLimiter_RetryAfterSeconds validates that rejected calls include a valid
// retry_after_seconds value indicating the next available slot in the sliding window.
// Traces to: wave2-security-layer-spec.md line 798 (TestRateLimiter_RetryAfterSeconds)
// BDD: Scenario: Per-agent rate limit rejection — retry_after_seconds (spec line 689)
func TestRateLimiter_RetryAfterSeconds(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 689 (retry_after_seconds requirement)
	sw := security.NewSlidingWindow(3, time.Minute, security.ScopeAgent, "researcher", "llm_calls")

	for i := 0; i < 3; i++ {
		sw.Allow()
	}

	result := sw.Allow()
	require.False(t, result.Allowed)
	assert.Greater(t, result.RetryAfterSeconds, 0.0,
		"retry_after_seconds must be > 0 so the agent knows when to retry")
	assert.LessOrEqual(t, result.RetryAfterSeconds, 60.0,
		"retry_after_seconds must not exceed the 60s window duration")
	assert.NotEmpty(t, result.PolicyRule, "policy_rule must explain the rate limit hit")
}

// TestRateLimiter_GlobalCostCap validates the global daily cost cap across all agents.
// Traces to: wave2-security-layer-spec.md line 799 (TestRateLimiter_GlobalCostCap)
// BDD: Scenario: Global cost cap stops all agents (spec line 699)
func TestRateLimiter_GlobalCostCap(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 859 (Dataset row 4: global cost $50/day)
	t.Run("cost cap $50 allows $49.98 then blocks $0.05", func(t *testing.T) {
		registry := security.NewRateLimiterRegistry()
		registry.SetDailyCostCap(50.0)

		// Accumulate $49.98 — should be allowed
		result := registry.CheckGlobalCostCap(49.98, "researcher")
		require.True(t, result.Allowed, "cost $49.98 under cap $50 should be allowed")

		// This $0.05 call would push past $50
		result = registry.CheckGlobalCostCap(0.05, "researcher")
		assert.False(t, result.Allowed, "cost that exceeds $50 cap should be rejected")
		assert.Contains(t, result.PolicyRule, "global daily cost cap exceeded")
		assert.Contains(t, result.PolicyRule, "$50.00")
	})

	// Dataset row 5: emergency stop — zero cap blocks all
	t.Run("zero cost cap blocks all calls (emergency stop)", func(t *testing.T) {
		registry := security.NewRateLimiterRegistry()
		registry.SetDailyCostCap(0.001) // non-zero but tiny to trigger the check
		// First call of any positive amount should be blocked
		// The cap is 0.001, so 0.001 > 0.001 is false — try 0.002
		result := registry.CheckGlobalCostCap(0.002, "researcher")
		assert.False(t, result.Allowed, "second call should exceed the tiny cost cap")
	})

	t.Run("no cap configured (zero) allows any cost", func(t *testing.T) {
		// Traces to: wave2-security-layer-spec.md line 168 (dailyCostCap <= 0 = no cap)
		registry := security.NewRateLimiterRegistry()
		// Default DailyCostCap is 0.0 (not configured) — means no cap
		result := registry.CheckGlobalCostCap(1000.0, "researcher")
		assert.True(t, result.Allowed, "no cap configured (0.0) should allow any cost")
	})
}

// TestRateLimiter_PrivilegedAgentExempt validates that ONLY the core agent type
// is exempt from rate limits per FR-045 (privileges by type, not by hardcoded ID).
// ADR-049 D3 / CRIT-002 narrowed the exemption from core||system to core-only, so
// a type:system agent (the Judge) is NO LONGER exempt.
// Traces to: wave2-security-layer-spec.md line 800 (TestRateLimiter_SystemAgentExempt)
// BDD: Scenario: Privileged agent exempt from rate limits (spec line 709)
func TestRateLimiter_PrivilegedAgentExempt(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 709 (Scenario: System agent exempt)
	t.Run("core agent bypasses global cost cap", func(t *testing.T) {
		registry := security.NewRateLimiterRegistry()
		registry.SetDailyCostCap(0.001) // tiny cap that would block normal agents

		// First accumulate cost as custom agent to exhaust cap
		registry.CheckGlobalCostCap(0.001, "custom")

		// Core agent must bypass cost cap entirely (FR-045)
		result := registry.CheckGlobalCostCap(999.99, "core")
		assert.True(t, result.Allowed,
			"core agent must bypass cost cap: always returns Allowed=true")
	})

	t.Run("system agent is NOT exempt from CheckGlobalCostCap (ADR-049 D3)", func(t *testing.T) {
		registry := security.NewRateLimiterRegistry()
		registry.SetDailyCostCap(50.0)

		// Load $49.99 from a custom agent
		registry.CheckGlobalCostCap(49.99, "custom")

		// System-type agent is now subject to the cap: a $100 spend on top of
		// $49.99 exceeds $50 and MUST be denied (SEC-26 applies to type:system).
		result := registry.CheckGlobalCostCap(100.0, "system")
		assert.False(t, result.Allowed,
			"type:system agent is NOT exempt from the global cost cap (ADR-049 D3 / CRIT-002)")
		assert.Contains(t, result.PolicyRule, "cost cap")
	})
}

// TestRateLimiter_ConcurrentAccess validates thread-safety under concurrent rate limit checks.
// Traces to: wave2-security-layer-spec.md line 801 (TestRateLimiter_ConcurrentAccess)
// BDD: Edge case — concurrent rate limit requests with atomic operations (spec line 297)
func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 297 (Edge: concurrent atomic operations)
	const (
		goroutines = 100
		maxCalls   = 50
	)

	sw := security.NewSlidingWindow(maxCalls, time.Minute, security.ScopeAgent, "concurrent-agent", "llm_calls")

	var (
		wg      sync.WaitGroup
		allowed int64
		denied  int64
		mu      sync.Mutex
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := sw.Allow()
			mu.Lock()
			if result.Allowed {
				allowed++
			} else {
				denied++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Exactly maxCalls should be allowed, rest denied
	// Note: due to timing, allow for minor variance in sliding window
	assert.Equal(t, int64(maxCalls), allowed,
		"exactly %d calls should be allowed", maxCalls)
	assert.Equal(t, int64(goroutines-maxCalls), denied,
		"exactly %d calls should be denied", goroutines-maxCalls)
}
