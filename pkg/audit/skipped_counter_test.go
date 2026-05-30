// Package audit — tests for the IncSkipped / SnapshotSkipped counter pair.
//
// Covers (quick sweep 2):
//   - web_serve+allow tick
//   - web_serve+deny tick
//   - "other" sink for unknown decision label
//   - "other" sink for unknown tool label
//   - snapshot field summing (Total == WebServeAllow + WebServeDeny + Other)
//   - concurrent IncSkipped with atomic.Int64 verification
//
// NOTE: these tests share the process-wide auditSkippedCounters state and
// therefore run serially (no t.Parallel) to avoid cross-test interference.
// Each test calls ResetSkippedForTest() at the start so tests are independent
// when run in the default single-count sequential mode.
// ResetSkippedForTest is a test-only helper; production code must never call it.

package audit

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSkippedCounter_WebServeAllow verifies that IncSkipped("web_serve",
// DecisionAllow) increments only the web_serve_allow bucket.
func TestSkippedCounter_WebServeAllow(t *testing.T) {
	ResetSkippedForTest()

	IncSkipped("web_serve", DecisionAllow)
	IncSkipped("web_serve", DecisionAllow)

	snap := SnapshotSkipped()
	assert.Equal(t, int64(2), snap.WebServeAllow, "web_serve_allow must be 2")
	assert.Equal(t, int64(0), snap.WebServeDeny, "web_serve_deny must be 0")
	assert.Equal(t, int64(0), snap.Other, "other must be 0")
	assert.Equal(t, int64(2), snap.Total, "total must equal sum of buckets")
}

// TestSkippedCounter_WebServeDeny verifies that IncSkipped("web_serve",
// DecisionDeny) increments only the web_serve_deny bucket.
func TestSkippedCounter_WebServeDeny(t *testing.T) {
	ResetSkippedForTest()

	IncSkipped("web_serve", DecisionDeny)

	snap := SnapshotSkipped()
	assert.Equal(t, int64(0), snap.WebServeAllow, "web_serve_allow must be 0")
	assert.Equal(t, int64(1), snap.WebServeDeny, "web_serve_deny must be 1")
	assert.Equal(t, int64(0), snap.Other, "other must be 0")
	assert.Equal(t, int64(1), snap.Total, "total must equal 1")
}

// TestSkippedCounter_UnknownDecision verifies that an unknown decision string
// (not "allow" or "deny") falls through to the "other" bucket.
func TestSkippedCounter_UnknownDecision(t *testing.T) {
	ResetSkippedForTest()

	IncSkipped("web_serve", "mystery_decision")

	snap := SnapshotSkipped()
	assert.Equal(t, int64(0), snap.WebServeAllow)
	assert.Equal(t, int64(0), snap.WebServeDeny)
	assert.Equal(t, int64(1), snap.Other, "unknown decision must go to other bucket")
	assert.Equal(t, int64(1), snap.Total)
}

// TestSkippedCounter_UnknownTool verifies that an unknown tool name falls
// through to the "other" bucket.
func TestSkippedCounter_UnknownTool(t *testing.T) {
	ResetSkippedForTest()

	IncSkipped("unknown_tool_xyz", DecisionAllow)
	IncSkipped("another_mystery", DecisionDeny)

	snap := SnapshotSkipped()
	assert.Equal(t, int64(0), snap.WebServeAllow)
	assert.Equal(t, int64(0), snap.WebServeDeny)
	assert.Equal(t, int64(2), snap.Other, "unknown tools must both go to other bucket")
	assert.Equal(t, int64(2), snap.Total)
}

// TestSkippedCounter_SnapshotSum verifies that SnapshotSkipped.Total is always
// the arithmetic sum of the labeled buckets — no hidden state.
func TestSkippedCounter_SnapshotSum(t *testing.T) {
	ResetSkippedForTest()

	IncSkipped("web_serve", DecisionAllow)    // +1 web_serve_allow
	IncSkipped("web_serve", DecisionDeny)     // +1 web_serve_deny
	IncSkipped("unknown_tool", DecisionAllow) // +1 other
	IncSkipped("web_serve", "bad_decision")   // +1 other

	snap := SnapshotSkipped()
	require.Equal(t, snap.WebServeAllow+snap.WebServeDeny+snap.Other, snap.Total,
		"Total must equal WebServeAllow + WebServeDeny + Other")
	assert.Equal(t, int64(1), snap.WebServeAllow)
	assert.Equal(t, int64(1), snap.WebServeDeny)
	assert.Equal(t, int64(2), snap.Other)
	assert.Equal(t, int64(4), snap.Total)
}

// TestSkippedCounter_ConcurrentIncSkipped verifies that concurrent calls to
// IncSkipped do not lose increments — the underlying atomic.Int64 must be
// race-free.
func TestSkippedCounter_ConcurrentIncSkipped(t *testing.T) {
	ResetSkippedForTest()

	const goroutines = 100
	const perGoroutine = 50
	const expected = goroutines * perGoroutine

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				IncSkipped("web_serve", DecisionAllow)
			}
		}()
	}
	wg.Wait()

	snap := SnapshotSkipped()
	assert.Equal(t, int64(expected), snap.WebServeAllow,
		"no increments must be lost under concurrent access")
	assert.Equal(t, int64(expected), snap.Total)
}
