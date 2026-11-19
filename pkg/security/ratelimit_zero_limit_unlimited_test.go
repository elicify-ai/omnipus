// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ratelimit_zero_limit_unlimited_test.go — #1882 follow-up P12 (RED; gate
// finding cr-limiter #1): a limit of 0 or less arriving at the limiter must
// mean "no limit", not deny-all, and must not panic.
//
// The user-facing rule is docs/settings.md (Security rate limits): "Leave
// either blank, or enter 0, for no limit." The wire contracts spell the same
// rule (contracts/components/schemas/RateLimitConfig.yaml and
// RateLimitsUpdateRequest.yaml: "0 = unlimited", minimum 0). The Settings
// screen saves 0 for a cleared field, so a 0 in the configured limit is a
// real, user-reachable value, and a limiter that receives it must not turn
// it into a permanent deny-all (or panic on a negative one).
//
// RED on d9d89b9145: today a non-positive maxCount denies everything
// (allowAt's `len(events) < maxCount` is never true) and NewSlidingWindow
// panics for a limit below -1 (`make([]time.Time, 0, limit+1)`). These tests
// are expected to FAIL until the production fix lands; they are not a
// characterisation of the current behaviour.

package security_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// unlimitedProbeCalls is far above any positive limit used below (the
// largest is 3), so "all calls allowed" can only hold when no limit is being
// enforced at all.
const unlimitedProbeCalls = 50

// zeroLimitWindow keeps every recorded event inside the window for the whole
// test (no real-clock call can age out of an hour), so the assertions are
// deterministic without sleeps.
const zeroLimitWindow = time.Hour

func TestRateLimiter_ZeroOrNegativeLimitMeansUnlimited(t *testing.T) {
	t.Run("limit_zero_allows_every_call", func(t *testing.T) {
		sw := security.NewSlidingWindow(0, zeroLimitWindow, security.ScopeAgent, "agent-zero-limit", "llm_call")
		for i := 0; i < unlimitedProbeCalls; i++ {
			result := sw.Allow()
			require.True(t, result.Allowed,
				"P12: call %d of %d must be allowed — a limit of 0 means no limit (docs/settings.md: \"enter 0, for no limit\"; RateLimitConfig.yaml: \"0 = unlimited\"), but the window denied it with %+v",
				i+1, unlimitedProbeCalls, result)
		}
	})

	t.Run("limit_below_minus_one_does_not_panic_and_allows_every_call", func(t *testing.T) {
		// A negative limit is not a configured state anywhere (the contracts
		// set minimum 0), so the contract for it is the robustness property:
		// constructing the window must not panic, and it must not deny.
		sw := security.NewSlidingWindow(-5, zeroLimitWindow, security.ScopeAgent, "agent-negative-limit", "llm_call")
		for i := 0; i < unlimitedProbeCalls; i++ {
			result := sw.Allow()
			require.True(t, result.Allowed,
				"P12: call %d of %d on a window built with limit -5 must be allowed (no configured limit) — denied with %+v",
				i+1, unlimitedProbeCalls, result)
		}
	})

	t.Run("registry_limit_zero_on_new_key_allows_every_call", func(t *testing.T) {
		reg := security.NewRateLimiterRegistry()
		sw := reg.GetOrCreate("agent:zero-registry:llm_call", 0, zeroLimitWindow, security.ScopeAgent, "agent-zero-registry", "llm_call")
		for i := 0; i < unlimitedProbeCalls; i++ {
			result := sw.Allow()
			require.True(t, result.Allowed,
				"P12: call %d of %d through GetOrCreate with limit 0 must be allowed — a 0 limit reaching the registry means no limit (docs/settings.md), denied with %+v",
				i+1, unlimitedProbeCalls, result)
		}
	})

	t.Run("lowered_existing_window_to_zero_is_unlimited_then_positive_limit_enforces_again", func(t *testing.T) {
		reg := security.NewRateLimiterRegistry()
		key := "agent:lowered-to-zero:llm_call"

		// Start from a real configured limit and record one event, so the
		// window is live and holds state before the lowering.
		sw := reg.GetOrCreate(key, 3, zeroLimitWindow, security.ScopeAgent, "agent-lowered-to-zero", "llm_call")
		require.True(t, sw.Allow().Allowed,
			"P12 setup: the first call under the original limit 3 must be allowed")

		// Lower the live window to 0 through the registry — the same path a
		// Settings save takes to reach a running limiter (#1882).
		lowered := reg.GetOrCreate(key, 0, zeroLimitWindow, security.ScopeAgent, "agent-lowered-to-zero", "llm_call")
		require.Same(t, sw, lowered,
			"P12 setup: GetOrCreate must return the live window already in use for the key — otherwise the assertions below would pass on a fresh window instead of the lowered one")

		for i := 0; i < unlimitedProbeCalls; i++ {
			result := lowered.Allow()
			require.True(t, result.Allowed,
				"P12: call %d of %d after lowering the live window to 0 must be allowed — 0 means no limit (docs/settings.md: \"enter 0, for no limit\"), denied with %+v",
				i+1, unlimitedProbeCalls, result)
		}

		// Raising it back to a positive limit enforces that limit again: the
		// window now holds unlimitedProbeCalls+1 events inside the hour, far
		// above 3, so the next call must be refused with a well-formed
		// denial.
		raised := reg.GetOrCreate(key, 3, zeroLimitWindow, security.ScopeAgent, "agent-lowered-to-zero", "llm_call")
		require.Same(t, sw, raised,
			"P12 setup: raising must apply to the same live window, not replace it")
		result := raised.Allow()
		require.False(t, result.Allowed,
			"P12: after raising the live window back to 3, the next call must be refused — the window holds %d recorded events inside the window; got %+v",
			unlimitedProbeCalls+1, result)
		assert.Greater(t, result.RetryAfterSeconds, 0.0,
			"P12: the refusal after raising must carry a positive retry_after_seconds so the agent knows when to retry")
		assert.NotEmpty(t, result.PolicyRule,
			"P12: the refusal after raising must name the policy rule that was hit")
	})
}
