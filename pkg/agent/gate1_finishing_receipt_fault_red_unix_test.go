//go:build linux || darwin

package agent

import (
	"errors"
	"os"
	"testing"
)

// F3/A6: ADR-20260928 D-D/D4 and the agreed first-delivered-append oracle.
// Both controls are accepted AFTER the actual terminal/outbox commit. Deny
// only the concrete controls file's append seam after those acceptances;
// lifecycle, transcript and provider remain healthy. The first delivered
// refinement must fail, and every accepted item must remain retryable.
func TestGate1Finishing_FirstReceiptFailureRetainsEntireBatch(t *testing.T) {
	f := gate1CommittedFinishing(t)
	first := f.accept(t, "First post-commit input whose delivered append is refused.", "gate1-receipt-fault-first")
	second := f.accept(t, "Second post-commit input must not be stranded by the first failure.", "gate1-receipt-fault-second")
	path := qaReceiptLedgerPath(f.al, f.child.SessionID)
	restore := qaReceiptDenyAppend(t, path)
	f.publication.open()
	err := f.completionError(t)
	var pathErr *os.PathError
	if !errors.Is(err, os.ErrPermission) || !errors.As(err, &pathErr) || pathErr.Path != path {
		t.Errorf("F3: real completion returned %v, want concrete permission/path error from first delivered-receipt append to %s", err, path)
	} else {
		t.Logf("fault reached actual completion return: permission denial at exact controls path; error=%v", err)
	}
	gate1RequireQueuedItems(t, f.al, f.child.SessionID, first, second)

	// Restore before opening the paid-provider edge and joining all owners.
	// Observation above is the fault oracle; no production state is repaired
	// or receipt fabricated in the test to hide the original failure.
	restore()
	f.provider.openAll()
	joinGoalFixtureRuns(t, f.al)
}
