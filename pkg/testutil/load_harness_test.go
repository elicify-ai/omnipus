package testutil_test

// Tests for pkg/testutil/load_harness.go
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 (testutil half) + F19
//
// BDD scenarios:
//   Given: the Percentile function and LoadMetrics type
//   When: called with various inputs
//   Then: results match expected values; caller's slice is never mutated

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/testutil"
)

// TestPercentile_EmptySlice verifies that Percentile returns 0 for an empty input.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 — "empty slice (returns 0)"
func TestPercentile_EmptySlice(t *testing.T) {
	result := testutil.Percentile(nil, 0.95)
	assert.Equal(t, time.Duration(0), result, "Percentile of empty slice must return 0")

	result = testutil.Percentile([]time.Duration{}, 0.95)
	assert.Equal(t, time.Duration(0), result, "Percentile of [] must return 0")
}

// TestPercentile_ZeroOrNegativeP verifies that p <= 0 returns 0.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12
func TestPercentile_ZeroOrNegativeP(t *testing.T) {
	data := []time.Duration{1 * time.Millisecond, 2 * time.Millisecond}

	assert.Equal(t, time.Duration(0), testutil.Percentile(data, 0), "p=0 must return 0")
	assert.Equal(t, time.Duration(0), testutil.Percentile(data, -0.5), "p<0 must return 0")
}

// TestPercentile_SingleElement verifies that a single-element slice returns
// that element regardless of p.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 — "single element (returns element)"
func TestPercentile_SingleElement(t *testing.T) {
	elem := 42 * time.Millisecond
	data := []time.Duration{elem}

	tests := []struct {
		p    float64
		want time.Duration
	}{
		{0.5, elem},
		{0.95, elem},
		{0.99, elem},
		{1.0, elem},
	}

	for _, tc := range tests {
		got := testutil.Percentile(data, tc.p)
		assert.Equal(t, tc.want, got,
			"Percentile([%v], %v) must return %v, got %v", elem, tc.p, tc.want, got)
	}
}

// TestPercentile_100Elements verifies P50, P95, P99 on 100 ordered elements.
// Elements are 1ms, 2ms, …, 100ms (sorted ascending).
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 — "100 elements (P50=median, P95=95th, P99=99th)"
func TestPercentile_100Elements(t *testing.T) {
	data := make([]time.Duration, 100)
	for i := range data {
		data[i] = time.Duration(i+1) * time.Millisecond
	}

	// Nearest-rank formula: ceil(p*n) — using 1-indexed = floor(p*(n+1)) as per impl.
	// For n=100, p=0.50: floor(0.50*101)-1 = floor(50.5)-1 = 50-1 = 49 → data[49] = 50ms
	// For n=100, p=0.95: floor(0.95*101)-1 = floor(95.95)-1 = 95-1 = 94 → data[94] = 95ms
	// For n=100, p=0.99: floor(0.99*101)-1 = floor(99.99)-1 = 99-1 = 98 → data[98] = 99ms
	p50 := testutil.Percentile(data, 0.50)
	p95 := testutil.Percentile(data, 0.95)
	p99 := testutil.Percentile(data, 0.99)

	assert.Equal(t, 50*time.Millisecond, p50, "P50 of 1..100ms should be 50ms")
	assert.Equal(t, 95*time.Millisecond, p95, "P95 of 1..100ms should be 95ms")
	assert.Equal(t, 99*time.Millisecond, p99, "P99 of 1..100ms should be 99ms")
}

// TestPercentile_UnsortedInputPreservesCallerOrder verifies that calling
// Percentile does NOT sort the caller's original slice — the function must
// operate on a copy.
//
// This is the key regression test for F19's in-place sort bug.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12 — "unsorted input leaves caller's slice unsorted (proves copy)"
// Traces to: temporal-puzzling-melody.md §Plan 4 F19 — fix Percentile in-place mutation
func TestPercentile_UnsortedInputPreservesCallerOrder(t *testing.T) {
	// Deliberately unsorted so we can detect if sort.Slice touched the original.
	original := []time.Duration{
		50 * time.Millisecond,
		10 * time.Millisecond,
		90 * time.Millisecond,
		30 * time.Millisecond,
		70 * time.Millisecond,
	}

	// Capture the original order.
	before := make([]time.Duration, len(original))
	copy(before, original)

	// Call Percentile — this must not mutate original.
	result := testutil.Percentile(original, 0.90)

	// The result must be > 0 (proves the function computed something).
	require.Greater(t, int64(result), int64(0), "Percentile must return a non-zero result for non-empty input")

	// The original slice must have the same order as before.
	require.Equal(t, before, original,
		"Percentile must NOT sort the caller's slice in-place; original order must be preserved")
}

// TestPercentile_DuplicateValues verifies correct behavior when all values are equal.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12
func TestPercentile_DuplicateValues(t *testing.T) {
	v := 15 * time.Millisecond
	data := []time.Duration{v, v, v, v, v}

	for _, p := range []float64{0.1, 0.5, 0.95, 0.99} {
		got := testutil.Percentile(data, p)
		assert.Equal(t, v, got, "Percentile of all-%v at p=%v should be %v", v, p, v)
	}
}

// TestPercentile_PEqualOne verifies that p=1.0 returns the maximum element.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F12
func TestPercentile_PEqualOne(t *testing.T) {
	data := []time.Duration{1 * time.Millisecond, 5 * time.Millisecond, 100 * time.Millisecond}
	got := testutil.Percentile(data, 1.0)
	assert.Equal(t, 100*time.Millisecond, got, "p=1.0 must return the maximum element")
}

// TestNewLoadMetrics_PrivateFields verifies that LoadMetrics is constructed
// correctly and getters return the expected initial values.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F19 — constructor + private fields
func TestNewLoadMetrics_PrivateFields(t *testing.T) {
	m := testutil.NewLoadMetrics(42)
	require.NotNil(t, m)

	assert.Equal(t, 42, m.GoroutinesBefore(), "GoroutinesBefore must match constructor arg")
	assert.Equal(t, 0, m.GoroutinesAfter(), "GoroutinesAfter must be 0 before Finalize")
	assert.Equal(t, 0, m.SessionsOpened(), "SessionsOpened must start at 0")
	assert.Equal(t, 0, m.MessagesRecv(), "MessagesRecv must start at 0")
	assert.Equal(t, 0, m.DroppedFrames(), "DroppedFrames must start at 0")
	assert.Equal(t, time.Duration(0), m.Duration(), "Duration must start at 0")
	assert.Equal(t, uint64(0), m.PeakRSSBytes(), "PeakRSSBytes must start at 0")
}

// TestLoadMetrics_RecordAndFinalize verifies that all mutating methods work
// correctly and Finalize sets the goroutine count and duration.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F19 — methods RecordFirstToken, RecordDoneFrame,
// IncSessionsOpened, IncMessagesRecv, IncDropped, Finalize
func TestLoadMetrics_RecordAndFinalize(t *testing.T) {
	m := testutil.NewLoadMetrics(10)

	m.IncSessionsOpened()
	m.IncSessionsOpened()
	m.IncMessagesRecv()
	m.IncMessagesRecv()
	m.IncMessagesRecv()
	m.IncDropped()

	m.RecordFirstToken(10 * time.Millisecond)
	m.RecordFirstToken(20 * time.Millisecond)
	m.RecordFirstToken(30 * time.Millisecond)

	m.RecordDoneFrame(50 * time.Millisecond)
	m.RecordDoneFrame(100 * time.Millisecond)

	m.Finalize(11, 5*time.Second)

	assert.Equal(t, 2, m.SessionsOpened())
	assert.Equal(t, 3, m.MessagesRecv())
	assert.Equal(t, 1, m.DroppedFrames())
	assert.Equal(t, 11, m.GoroutinesAfter())
	assert.Equal(t, 5*time.Second, m.Duration())
}

// TestLoadMetrics_PercentileAccessors verifies P95FirstToken, P95DoneFrame,
// P99DoneFrame return non-zero values after recordings and zero before.
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F19 — percentile accessors
func TestLoadMetrics_PercentileAccessors(t *testing.T) {
	m := testutil.NewLoadMetrics(0)

	// Before any recordings: all percentile accessors return 0.
	assert.Equal(t, time.Duration(0), m.P95FirstToken(), "P95FirstToken must be 0 before recordings")
	assert.Equal(t, time.Duration(0), m.P95DoneFrame(), "P95DoneFrame must be 0 before recordings")
	assert.Equal(t, time.Duration(0), m.P99DoneFrame(), "P99DoneFrame must be 0 before recordings")

	// Record 100 first-token latencies (1ms..100ms).
	for i := 1; i <= 100; i++ {
		m.RecordFirstToken(time.Duration(i) * time.Millisecond)
		m.RecordDoneFrame(time.Duration(i*2) * time.Millisecond)
	}

	// P95FirstToken: nearest-rank for 100 elements → 95ms.
	assert.Equal(t, 95*time.Millisecond, m.P95FirstToken(), "P95FirstToken mismatch")
	// P95DoneFrame: 100 elements 2ms..200ms, p95 → 190ms.
	assert.Equal(t, 190*time.Millisecond, m.P95DoneFrame(), "P95DoneFrame mismatch")
	// P99DoneFrame: 100 elements 2ms..200ms, p99 → 198ms.
	assert.Equal(t, 198*time.Millisecond, m.P99DoneFrame(), "P99DoneFrame mismatch")
}

// TestLoadMetrics_PercentileDoesNotMutateInternal verifies that reading
// P95FirstToken twice returns the same value and the internal slice is unchanged
// after the first read (proves the accessor sorts a copy, not the live slice).
//
// Traces to: temporal-puzzling-melody.md §Plan 4 F19 — copy semantics in percentile accessors
func TestLoadMetrics_PercentileDoesNotMutateInternal(t *testing.T) {
	m := testutil.NewLoadMetrics(0)

	// Record latencies in descending order to maximize the chance that an
	// in-place sort would corrupt subsequent reads.
	for i := 100; i >= 1; i-- {
		m.RecordFirstToken(time.Duration(i) * time.Millisecond)
	}

	first := m.P95FirstToken()
	second := m.P95FirstToken()

	assert.Equal(t, first, second,
		"P95FirstToken must return the same value on repeated calls; "+
			"first=%v second=%v — mismatch implies the accessor mutated the backing slice", first, second)
	assert.Equal(t, 95*time.Millisecond, first,
		"P95FirstToken of 100 elements must be 95ms regardless of insertion order")
}
