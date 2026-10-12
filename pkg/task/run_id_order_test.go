package task

import "testing"

// A run's ID is the tie-break in ListRuns when two runs share a StartedAt
// second (RFC 3339 has second resolution), so IDs minted one after another
// must sort in mint order even inside the same millisecond. A ULID with a
// fresh random suffix per call only orders across milliseconds; two runs
// opened in the same millisecond came back newest-last (PR 1260 CI,
// TestTaskRunsEndpoint).
func TestNewRunID_MintedInOrderSortInOrder(t *testing.T) {
	const n = 5000 // far more than one millisecond's worth of calls
	prev, err := newRunID()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < n; i++ {
		next, err := newRunID()
		if err != nil {
			t.Fatal(err)
		}
		if next <= prev {
			t.Fatalf("run id %d (%s) does not sort after the one minted before it (%s)", i, next, prev)
		}
		prev = next
	}
}
