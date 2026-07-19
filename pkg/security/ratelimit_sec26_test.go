// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// SEC-26 narrowing (ADR-049 D3 / CRIT-002): System Agents are no longer
// privileged. IsPrivilegedAgent is core-only, so a type:system agent (the Judge)
// is rate-limited and cost-capped like any non-core agent.

package security_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/security"
)

// TestSEC26_IsPrivilegedAgent_CoreOnly verifies that IsPrivilegedAgent is
// core-only after ADR-049 D3 — in particular type:system is NOT privileged.
func TestSEC26_IsPrivilegedAgent_CoreOnly(t *testing.T) {
	assert.True(t, security.IsPrivilegedAgent("core"), "core must remain privileged")
	assert.False(t, security.IsPrivilegedAgent("system"),
		"type:system must NOT be privileged (ADR-049 D3 / CRIT-002)")
	assert.False(t, security.IsPrivilegedAgent("custom"))
	assert.False(t, security.IsPrivilegedAgent("worker"))
	assert.False(t, security.IsPrivilegedAgent(""))
	assert.False(t, security.IsPrivilegedAgent("SYSTEM"), "must be case-sensitive")
}

// TestSEC26_TypeSystemAgent_IsRateLimited proves that a type:system agent is
// subject to the daily cost cap while a core agent is exempt (SEC-26 via the
// narrowed IsPrivilegedAgent inside CheckGlobalCostCap / RecordSpend).
func TestSEC26_TypeSystemAgent_IsRateLimited(t *testing.T) {
	reg := security.NewRateLimiterRegistry()
	reg.SetDailyCostCap(1.00)

	// Core is exempt — a spend far above the cap is allowed and NOT recorded.
	core := reg.CheckGlobalCostCap(5.00, "core")
	assert.True(t, core.Allowed, "core must be exempt from the daily cost cap")
	assert.Equal(t, 0.0, reg.GetDailyCost(), "privileged (core) spend must not accumulate")

	// System is NOT exempt — a spend above the cap is DENIED with a cooldown.
	sys := reg.CheckGlobalCostCap(5.00, "system")
	assert.False(t, sys.Allowed, "type:system must be subject to the daily cost cap (SEC-26)")
	assert.Contains(t, sys.PolicyRule, "cost cap", "denied result must explain the cap")
	assert.Greater(t, sys.RetryAfterSeconds, 0.0, "denied system call must carry a retry_after cooldown")

	// A system spend UNDER the cap is allowed and DOES accumulate (metered).
	reg2 := security.NewRateLimiterRegistry()
	reg2.SetDailyCostCap(10.00)
	ok := reg2.CheckGlobalCostCap(3.00, "system")
	assert.True(t, ok.Allowed, "an under-cap system spend is allowed")
	assert.InDelta(t, 3.00, reg2.GetDailyCost(), 1e-9, "type:system spend must be metered, not exempt")
}

// TestSEC26_TypeSystemAgent_RecordSpendCounted proves RecordSpend counts a
// type:system agent's spend (privileged agents' spend is not counted).
func TestSEC26_TypeSystemAgent_RecordSpendCounted(t *testing.T) {
	reg := security.NewRateLimiterRegistry()
	reg.RecordSpend(2.50, "core")
	assert.Equal(t, 0.0, reg.GetDailyCost(), "core spend must not be recorded (privileged)")

	reg.RecordSpend(2.50, "system")
	assert.InDelta(t, 2.50, reg.GetDailyCost(), 1e-9, "type:system spend must be recorded (non-privileged)")
}
