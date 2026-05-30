//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the approval-registry map cleanup at terminal-state transitions.
//
// Background: prior to v0.1 ship-blocker A1.4 the registry never deleted
// entries from r.entries on terminal transitions; the map grew monotonically.
// The fix schedules a deferred delete (after r.terminalRetention) on each of
// the four terminal paths: resolve(), fireTimeout(), cancelBatchShortCircuit(),
// and cancelAllPendingForRestart(). Tests below force terminalRetention to 0
// so deletion is synchronous and assertable without time-based waits.

package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApprovalRegistry_ResolveDeletesEntry verifies that resolve()
// removes the entry from r.entries when the retention window is 0.
// BDD: Given a registry with terminalRetention=0,
// When an approval is requested and then resolved,
// Then r.entries is empty after the resolve.
func TestApprovalRegistry_ResolveDeletesEntry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0 // delete synchronously under the lock

	e, accepted := reg.requestApproval(
		"tc-resolve-cleanup", "read_file",
		map[string]any{"path": "/tmp/x"},
		"agent-A", "sess-A", "turn-A",
		false,
	)
	require.True(t, accepted, "approval must be accepted")

	// Drain outcome so resolve does not block on the buffered channel.
	doneCh := make(chan struct{})
	go func() {
		<-e.resultCh
		close(doneCh)
	}()

	ok, gone := reg.resolve(e.ApprovalID, ApprovalActionApprove)
	require.True(t, ok, "resolve must succeed on a pending entry")
	require.False(t, gone, "fresh resolve must not report gone=true")

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("resultCh never received outcome")
	}

	reg.mu.Lock()
	count := len(reg.entries)
	reg.mu.Unlock()
	assert.Equal(t, 0, count,
		"entries map must be empty after resolve when terminalRetention=0; got %d",
		count)
}

// TestApprovalRegistry_BatchShortCircuitDeletesEntry verifies that
// cancelBatchShortCircuit() removes the entry from r.entries when
// terminalRetention=0.
// BDD: Given a registry with terminalRetention=0,
// When an approval is requested and then canceled via batch short-circuit,
// Then r.entries is empty after the cancellation.
func TestApprovalRegistry_BatchShortCircuitDeletesEntry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0

	e, accepted := reg.requestApproval(
		"tc-bsc-cleanup", "web_search",
		map[string]any{"q": "x"},
		"agent-C", "sess-C", "turn-C",
		false,
	)
	require.True(t, accepted, "approval must be accepted")

	// Drain outcome so cancelBatchShortCircuit does not block.
	doneCh := make(chan struct{})
	go func() {
		<-e.resultCh
		close(doneCh)
	}()

	ok := reg.cancelBatchShortCircuit(e.ApprovalID)
	require.True(t, ok, "cancelBatchShortCircuit must return true for a pending entry")

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("resultCh never received outcome")
	}

	reg.mu.Lock()
	count := len(reg.entries)
	reg.mu.Unlock()
	assert.Equal(t, 0, count,
		"entries map must be empty after cancelBatchShortCircuit when terminalRetention=0; got %d",
		count)
}

// TestApprovalRegistry_RestartCancelDeletesEntry verifies that
// cancelAllPendingForRestart() removes entries from r.entries when
// terminalRetention=0.
// BDD: Given a registry with terminalRetention=0 and one pending entry,
// When cancelAllPendingForRestart is called,
// Then r.entries is empty after all outcomes are delivered.
func TestApprovalRegistry_RestartCancelDeletesEntry(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0

	e, accepted := reg.requestApproval(
		"tc-restart-cleanup", "read_file",
		map[string]any{"path": "/tmp/y"},
		"agent-D", "sess-D", "turn-D",
		false,
	)
	require.True(t, accepted, "approval must be accepted")

	// Drain outcome so cancelAllPendingForRestart does not block.
	doneCh := make(chan struct{})
	go func() {
		<-e.resultCh
		close(doneCh)
	}()

	canceled := reg.cancelAllPendingForRestart()
	require.Len(t, canceled, 1, "must have canceled exactly one entry")

	select {
	case <-doneCh:
	case <-time.After(2 * time.Second):
		t.Fatal("resultCh never received outcome")
	}

	reg.mu.Lock()
	count := len(reg.entries)
	reg.mu.Unlock()
	assert.Equal(t, 0, count,
		"entries map must be empty after cancelAllPendingForRestart when terminalRetention=0; got %d",
		count)
}

// TestApprovalRegistry_TimeoutDeletesEntry verifies that fireTimeout()
// removes the entry from r.entries after the timeout fires.
// BDD: Given a registry with a short timeout and terminalRetention=0,
// When the approval times out,
// Then r.entries is empty.
func TestApprovalRegistry_TimeoutDeletesEntry(t *testing.T) {
	// Short timeout so the timer fires quickly; retention=0 deletes inline.
	reg := newApprovalRegistryV2(64, 50*time.Millisecond)
	reg.terminalRetention = 0

	e, accepted := reg.requestApproval(
		"tc-timeout-cleanup", "web_search",
		map[string]any{"q": "x"},
		"agent-B", "sess-B", "turn-B",
		false,
	)
	require.True(t, accepted, "approval must be accepted")

	// Block until the timeout outcome is delivered to the buffered channel.
	select {
	case outcome := <-e.resultCh:
		assert.False(t, outcome.Approved)
		assert.Equal(t, "timeout", outcome.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout transition did not fire within 2 s")
	}

	// fireTimeout schedules the delete inline (retention=0). Confirm the map
	// is empty now.
	reg.mu.Lock()
	count := len(reg.entries)
	reg.mu.Unlock()
	assert.Equal(t, 0, count,
		"entries map must be empty after fireTimeout when terminalRetention=0; got %d",
		count)
}

// TestPendingApproval_AutoDeniedOnCancel is T19.
//
// BDD:
//
//	Given a pending tool approval awaiting user decision (ask policy)
//	When cancelAllPendingForSession is called for that session with reason "session canceled"
//	Then the resultCh receives an outcome with Approved=false and Reason="session canceled"
//	And the registry pending count drops to zero
//	And the entry is scheduled for deletion (no map leak)
//	And approvals for a different session are unaffected
//
// Refs: spec FR-7, EC-8, T19
func TestPendingApproval_AutoDeniedOnCancel(t *testing.T) {
	reg := newApprovalRegistryV2(64, 300*time.Second)
	reg.terminalRetention = 0 // synchronous deletion for assertability

	const targetSession = "sess-cancel-T19"
	const otherSession = "sess-other-T19"

	// Register two pending approvals — one for the target session, one for another.
	targetEntry, accepted := reg.requestApproval(
		"tc-cancel-T19", "read_file",
		map[string]any{"path": "/tmp/secret"},
		"agent-T19", targetSession, "turn-T19",
		false,
	)
	require.True(t, accepted, "target approval must be accepted")

	otherEntry, accepted := reg.requestApproval(
		"tc-other-T19", "web_search",
		map[string]any{"q": "hello"},
		"agent-T19", otherSession, "turn-other-T19",
		false,
	)
	require.True(t, accepted, "other-session approval must be accepted")

	// Guard: both start pending.
	assert.Equal(t, int64(2), reg.pendingCount.Load(), "must start with 2 pending")

	// Fire cancel on the target session.
	n := reg.cancelAllPendingForSession(targetSession, "session canceled")
	assert.Equal(t, 1, n, "must auto-deny exactly 1 approval")

	// --- Assert T19: target resultCh delivers denied outcome ---
	select {
	case outcome := <-targetEntry.resultCh:
		assert.False(t, outcome.Approved, "target approval must be denied")
		assert.Equal(t, "session canceled", outcome.Reason, "reason must be 'session cancelled'")
	case <-time.After(2 * time.Second):
		t.Fatal("T19: resultCh never received auto-deny outcome within 2 s")
	}

	// --- Assert T19: pending count dropped by 1 (other session still pending) ---
	assert.Equal(t, int64(1), reg.pendingCount.Load(),
		"pending count must be 1 after auto-denying one of two entries")

	// --- Assert T19: target entry transitioned to denied_cancel ---
	reg.mu.Lock()
	te, ok := reg.entries[targetEntry.ApprovalID]
	reg.mu.Unlock()
	// terminalRetention=0 → entry deleted synchronously; ok must be false.
	if ok {
		t.Errorf(
			"T19: target entry still in map after cancelAllPendingForSession with terminalRetention=0; state=%s",
			te.state,
		)
	}

	// --- Assert T19: other session entry is still pending and unaffected ---
	reg.mu.Lock()
	oe, stillPresent := reg.entries[otherEntry.ApprovalID]
	reg.mu.Unlock()
	require.True(t, stillPresent, "T19: other-session approval must still be in map")
	assert.Equal(t, ApprovalStatePending, oe.state,
		"T19: other-session approval must remain pending")

	// Drain the other entry to avoid goroutine leaks in test cleanup (timer fires).
	oe.timer.Stop()
	drainCh := make(chan struct{})
	go func() {
		reg.cancelAllPendingForSession(otherSession, "test cleanup")
		close(drainCh)
	}()
	select {
	case <-otherEntry.resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("T19 cleanup: other-session resultCh never drained")
	}
	<-drainCh
}
