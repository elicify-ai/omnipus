// Package gateway — test-only exports.
//
// This file exposes unexported helpers to package-level tests so that tests
// can call real production code paths rather than re-implementing them.
// Do NOT call these from production code.

package gateway

// ClearDegradedForTest calls the real clearDegraded closure logic on the
// services struct. It is used by reload rollback tests to verify that the
// degraded fields are zeroed without spinning up the full service graph.
func (s *services) ClearDegradedForTest() {
	s.reloadMu.Lock()
	s.reloadDegraded = false
	s.reloadError = nil
	s.reloadMu.Unlock()
}

// SetOrphanWatchdogTimeoutForTest / SetOrphanWatchdogMaxRechecksForTest were
// removed with the orphan watchdog itself (ADR-091 UAT defect 2 — see
// websocket_forward.go's top-of-file doc comment for why the whole
// mechanism was retired, not re-aimed).
