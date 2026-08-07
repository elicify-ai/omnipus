// logLatencyDistribution lives in a !cgo-tagged file because its only caller
// (benchmark_per_turn_test.go) is !cgo-tagged. Keeping it here means it is
// compiled — and therefore used — only in the build where it is referenced, so
// neither the `unused` linter nor `nolintlint` flags it under either CGO mode.
// (computePercentile stays in percentile_test.go with no build tag, since it is
// also called from the untagged benchmark_media_test.go.)

package perf

import "testing"

// logLatencyDistribution prints min/p50/p95/p99/max to the test log.
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
