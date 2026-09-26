package providers

// deleted_billing_curve_test_compat_test.go — TEST-ONLY compat copy.
//
// The C-7/D2 deletion (provider-messages spec §7.3) removed
// calculateBillingCooldown from production (pkg/providers/cooldown.go): the
// billing-specific 5h→24h lockout curve no longer exists and billing
// failures take the ONE standard curve. Two qa-lead-owned LEGACY tests in
// this package still compile against the deleted symbol:
//
//	cooldown_test.go::TestCooldown_BillingEscalation (asserts the deleted
//	5h lockout — goes assertion-RED as SUPERSEDED),
//	cooldown_test.go::TestCooldown_BillingCap (calls the deleted function
//	directly — a compile dependency).
//
// Test files are qa-lead's; the implementer does not edit them. This file
// therefore holds the deleted function VERBATIM, in a _test.go-only file,
// so the package's test binary keeps compiling until qa-lead deletes the
// two superseded tests. It is production code nowhere — the C-7 absence
// test scans non-test files only. FLAGGED FOR CHECK (test-integrity-audit).

import (
	"math"
	"time"
)

// calculateBillingCooldown — verbatim copy of the function deleted from
// cooldown.go by C-7/D2. Kept solely for the legacy tests above.
//
// Formula from OpenClaw: min(24h, 5h * 2^min(n-1, 10))
//
//	1 error  → 5 hours
//	2 errors → 10 hours
//	3 errors → 20 hours
//	4+ errors → 24 hours (cap)
func calculateBillingCooldown(billingErrorCount int) time.Duration {
	const baseMs = 5 * 60 * 60 * 1000 // 5 hours
	const maxMs = 24 * 60 * 60 * 1000 // 24 hours

	n := max(1, billingErrorCount)
	exp := min(n-1, 10)
	raw := float64(baseMs) * math.Pow(2, float64(exp))
	ms := int(math.Min(float64(maxMs), raw))
	return time.Duration(ms) * time.Millisecond
}
