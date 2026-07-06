// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// --- Wave 5b: System Agent Rate Limit Tests ---
//
// These tests cover the rate limiting behavior required by the wave5b spec:
//   - System agent is exempt from the global cost-cap (already in ratelimit.go)
//   - System tool categories have the correct per-category thresholds
//
// The SlidingWindow infrastructure is shared — the system tool handler will
// create one SlidingWindow per category using the exact thresholds below.

// TestSystemAgentExemptFromCostCapRateLimit verifies that privileged agents (core/system
// type) are always allowed through the global cost cap regardless of accumulated spend.
//
// Traces to: wave5b-system-agent-spec.md — System tool rate limiting (US-11)
// BDD: "Privileged agent exempt from rate limits" (FR-045: privileges by type, not ID)
func TestSystemAgentExemptFromCostCapRateLimit(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 196 (User Story 11)

	r := security.NewRateLimiterRegistry()
	r.SetDailyCostCap(1.00) // $1.00 cap

	// Exhaust the cap for a normal agent.
	result := r.CheckGlobalCostCap(0.99, "custom")
	require.True(t, result.Allowed, "custom agent: first call within cap should be allowed")

	// Now the remaining budget is ~$0.01.
	result = r.CheckGlobalCostCap(0.05, "custom")
	assert.False(t, result.Allowed, "custom agent: call that exceeds cap should be denied")

	// Privileged (core) agents must still be allowed even though cap is exceeded.
	result = r.CheckGlobalCostCap(1000.00, "core")
	assert.True(t, result.Allowed,
		"core agent must be exempt from the global cost cap (FR-045)")
	assert.Equal(t, "privileged agent exempt from cost cap", result.PolicyRule,
		"exempt policy rule must identify reason")
}

// TestRateLimiterCreate verifies that the create-category sliding window
// enforces the 30-per-minute threshold specified in the wave5b spec.
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Rate limit hit on create operations
// BDD: "Given 30 system.agent.create calls in 60s, When 31st call, Then RATE_LIMITED"
func TestRateLimiterCreate(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 762 (US-11 AC1)
	// Dataset row 3: Create, 30/min, 60s, system.agent.create, over limit → RATE_LIMITED

	const createLimit = 30
	const createWindow = 60 * time.Second

	sw := security.NewSlidingWindow(
		createLimit,
		createWindow,
		security.ScopeAgent,
		"omnipus-system",
		"system_tool.create",
	)

	// First 30 calls must all succeed.
	for i := 0; i < createLimit; i++ {
		result := sw.Allow()
		require.True(t, result.Allowed,
			"create call %d/%d should be allowed", i+1, createLimit)
	}

	// The 31st call must be rate-limited.
	result := sw.Allow()
	assert.False(t, result.Allowed,
		"31st create call must be RATE_LIMITED per wave5b spec")
	assert.Greater(t, result.RetryAfterSeconds, 0.0,
		"RATE_LIMITED response must include retry_after_seconds")
	assert.Contains(t, result.PolicyRule, "rate_limit",
		"policy rule must identify rate limit hit")
}

// TestRateLimiterRecovery verifies that after the 60-second window expires,
// a new create call is allowed (rate limit resets).
//
// Traces to: wave5b-system-agent-spec.md — Scenario: Rate limit recovery (US-11 AC3)
// BDD: "Given rate limit window expired, When new request, Then it succeeds"
func TestRateLimiterRecovery(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 776 (US-11 AC3)

	// Use a very short window (1ms) so we can test expiry without real sleeps.
	sw := security.NewSlidingWindow(
		2,
		1*time.Millisecond,
		security.ScopeAgent,
		"omnipus-system",
		"system_tool.create",
	)

	// Fill the window.
	require.True(t, sw.Allow().Allowed, "first call should be allowed")
	require.True(t, sw.Allow().Allowed, "second call should be allowed")
	assert.False(t, sw.Allow().Allowed, "third call should be rate-limited (window full)")

	// Wait for window to expire.
	time.Sleep(2 * time.Millisecond)

	// After window expiry, a new call must be allowed.
	result := sw.Allow()
	assert.True(t, result.Allowed,
		"after window expiry, rate limit must reset and allow new calls")
}

// TestRateLimiterCategories verifies that all 6 system tool rate-limit categories
// have the correct thresholds as defined in the wave5b spec.
//
// Traces to: wave5b-system-agent-spec.md Dataset: Rate Limit Categories (rows 1-9)
// BDD: All category limits enforced at their spec-defined thresholds.
func TestRateLimiterCategories(t *testing.T) {
	// Traces to: wave5b-system-agent-spec.md line 843 (Dataset: Rate Limit Categories)

	tests := []struct {
		name      string
		limit     int
		window    time.Duration
		resource  string
		overLimit int // calls that exceed the limit
	}{
		// Dataset row 3: Create — 30/min
		{
			name:      "create: 30/min",
			limit:     30,
			window:    60 * time.Second,
			resource:  "system_tool.create",
			overLimit: 31,
		},
		// Dataset row 5: Delete — 10/min
		{
			name:      "delete: 10/min",
			limit:     10,
			window:    60 * time.Second,
			resource:  "system_tool.delete",
			overLimit: 11,
		},
		// Dataset row 6: Config — 10/min
		{
			name:      "config: 10/min",
			limit:     10,
			window:    60 * time.Second,
			resource:  "system_tool.config",
			overLimit: 11,
		},
		// Dataset row 7: List/query — 60/min
		{
			name:      "list_query: 60/min",
			limit:     60,
			window:    60 * time.Second,
			resource:  "system_tool.list",
			overLimit: 61,
		},
		// Dataset row 8: Channel — 5/min
		{
			name:      "channel: 5/min",
			limit:     5,
			window:    60 * time.Second,
			resource:  "system_tool.channel",
			overLimit: 6,
		},
		// Dataset row 9: Backup — 1/5min
		{
			name:      "backup: 1/5min",
			limit:     1,
			window:    5 * time.Minute,
			resource:  "system_tool.backup",
			overLimit: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sw := security.NewSlidingWindow(
				tc.limit,
				tc.window,
				security.ScopeAgent,
				"omnipus-system",
				tc.resource,
			)

			// All calls up to and including the limit must succeed.
			for i := 0; i < tc.limit; i++ {
				result := sw.Allow()
				assert.True(t, result.Allowed,
					"call %d/%d should be within the %s limit", i+1, tc.limit, tc.name)
			}

			// The (limit+1)-th call must be rejected.
			result := sw.Allow()
			assert.False(t, result.Allowed,
				"call %d should exceed the %s limit and be RATE_LIMITED",
				tc.overLimit, tc.name)
			assert.Greater(t, result.RetryAfterSeconds, 0.0,
				"RATE_LIMITED must include retry_after_seconds for %s", tc.name)
		})
	}
}
