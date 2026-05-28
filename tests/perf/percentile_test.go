// Package perf — shared latency math helpers.
// No build tag: these pure-math functions compile under both CGO_ENABLED=0 and CGO_ENABLED=1.
// Files that import pkg/gateway (testmain_test.go, helpers_test.go, and the gateway-dependent
// benchmark/SLO files) must retain //go:build !cgo because pkg/gateway itself carries that tag.

package perf

import (
	"math"
	"testing"
)

// computePercentile returns the p-th percentile (0–100) of a sorted float64 slice.
// The slice must be sorted ascending before calling.
func computePercentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (p / 100.0) * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// logLatencyDistribution prints min/p50/p95/p99/max to the test log.
//

func logLatencyDistribution(t testing.TB, label string, sorted []float64) {
	t.Helper()
	if len(sorted) == 0 {
		t.Logf("%s distribution: empty", label)
		return
	}
	t.Logf("%s distribution (n=%d): min=%.2f ms  p50=%.2f ms  p95=%.2f ms  p99=%.2f ms  max=%.2f ms",
		label,
		len(sorted),
		sorted[0],
		computePercentile(sorted, 50),
		computePercentile(sorted, 95),
		computePercentile(sorted, 99),
		sorted[len(sorted)-1],
	)
}
