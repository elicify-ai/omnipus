//go:build !cgo

// cancel_handler_test.go — tests for the approval registry cancel path.
//
// Spec refs: FR-12, FR-13a

package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// cancelAllPendingForSession tests (FR-12, FR-13a)
// ---------------------------------------------------------------------------

// TestCancelAllPendingForSession_DeniesOnlyMatchingSession verifies that
// cancelAllPendingForSession only transitions approvals for the requested
// session and leaves approvals for other sessions untouched.
func TestCancelAllPendingForSession_DeniesOnlyMatchingSession(t *testing.T) {
	t.Parallel()

	reg := newApprovalRegistryV2(64, 30*time.Second)
	// Use immediate terminal deletion so cleanup doesn't race.
	reg.terminalRetention = 0
	t.Cleanup(func() { reg.cancelAllPendingForRestart() })

	// Register two pending approvals for "sess-A" and one for "sess-B"
	// by inserting directly into the map (test-only).
	a1 := makeTestApprovalEntry("approval-a1", "sess-A")
	a2 := makeTestApprovalEntry("approval-a2", "sess-A")
	b1 := makeTestApprovalEntry("approval-b1", "sess-B")

	reg.mu.Lock()
	reg.entries["approval-a1"] = a1
	reg.entries["approval-a2"] = a2
	reg.entries["approval-b1"] = b1
	reg.pendingCount.Store(3)
	reg.mu.Unlock()

	// Run cancelAllPendingForSession in a goroutine so we can drain resultCh.
	done := make(chan int, 1)
	go func() {
		n := reg.cancelAllPendingForSession("sess-A", "session canceled")
		done <- n
	}()

	// Drain resultCh for sess-A approvals (otherwise cancelAll blocks).
	<-a1.resultCh
	<-a2.resultCh

	n := <-done
	assert.Equal(t, 2, n, "must cancel both sess-A approvals")

	// sess-B must still be pending.
	reg.mu.Lock()
	b1state := reg.entries["approval-b1"].state
	reg.mu.Unlock()
	assert.Equal(t, ApprovalStatePending, b1state, "sess-B approval must remain pending")

	// sess-A approvals must be denied_cancel.
	reg.mu.Lock()
	a1state := reg.entries["approval-a1"]
	a2state := reg.entries["approval-a2"]
	reg.mu.Unlock()
	// After terminalRetention=0, entries may have been deleted already.
	// Accept either deleted or denied_cancel.
	if a1state != nil {
		assert.Equal(t, ApprovalStateDeniedCancel, a1state.state)
	}
	if a2state != nil {
		assert.Equal(t, ApprovalStateDeniedCancel, a2state.state)
	}
}

// ---------------------------------------------------------------------------
// makeTestApprovalEntry — helper for approvalRegistryV2 tests
// ---------------------------------------------------------------------------

func makeTestApprovalEntry(approvalID, sessionID string) *approvalEntry {
	return &approvalEntry{
		ApprovalID: approvalID,
		SessionID:  sessionID,
		ToolName:   "test_tool",
		CreatedAt:  time.Now(),
		ExpiresAt:  time.Now().Add(30 * time.Second),
		state:      ApprovalStatePending,
		resultCh:   make(chan ApprovalOutcome, 1),
	}
}
