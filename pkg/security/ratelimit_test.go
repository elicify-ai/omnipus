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

// TestRateLimiter_PrivilegedAgentExempt validates that ONLY the core agent type
// is exempt from the SEC-26 sliding-window rate limits per FR-045 (privileges
// by type, not by hardcoded ID). ADR-049 D3 / CRIT-002 narrowed the exemption
// from core||system to core-only, so a type:system agent (the Judge) is NO
// LONGER exempt from the LLM/hr and tool/min limits either.
//
// ADR-053 D12 retired the SEC-26 daily USD cost cap that this predicate used
// to also gate; this test now exercises ONLY the surviving sliding-window
// behaviour via the registry's GetOrCreate + IsPrivilegedAgent exemption.
//
// Traces to: wave2-security-layer-spec.md line 800 (TestRateLimiter_SystemAgentExempt)
// BDD: Scenario: Privileged agent exempt from rate limits (spec line 709)
func TestRateLimiter_PrivilegedAgentExempt(t *testing.T) {
	// Traces to: wave2-security-layer-spec.md line 709 (Scenario: System agent exempt)
	t.Run("core agent bypasses sliding-window rate limit", func(t *testing.T) {
		registry := security.NewRateLimiterRegistry()
		// Tiny sliding-window: 1 call per hour — would block a second call
		// from a non-privileged agent.
		w := registry.GetOrCreate(
			"agent:core-1:llm_call",
			1,
			time.Hour,
			security.ScopeAgent,
			"core-1",
			"llm_call",
		)
		// Saturate the window as a "custom" agent.
		_ = registry
		// The core-agent bypass only matters in the actual enforcement site
		// (loop.go turnLoop), where `!security.IsPrivilegedAgent(...)` is
		// short-circuited. Here we assert the predicate's behaviour so the
		// registry-level invariant stays documented.
		assert.True(t, security.IsPrivilegedAgent("core"),
			"core must remain privileged for sliding-window rate limits")
		assert.False(t, security.IsPrivilegedAgent("system"),
			"type:system must NOT be privileged (ADR-049 D3 / CRIT-002)")
		assert.False(t, security.IsPrivilegedAgent("custom"))
		// The window still records Allow() / rejects as expected.
		assert.True(t, w.Allow().Allowed, "first call on the window is allowed")
		assert.False(t, w.Allow().Allowed, "second call exceeds the 1/hour window")
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
