// Package perf — shared latency math helpers.
// Every file in this package compiles under both CGO_ENABLED=0 and CGO_ENABLED=1.

package perf

import "math"

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
