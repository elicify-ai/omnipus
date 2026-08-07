// Package gateway — test-only exports.
//
// This file exposes unexported helpers to package-level tests so that tests
// can call real production code paths rather than re-implementing them.
// Do NOT call these from production code.

package gateway

import (
	"time"
)

// ClearDegradedForTest calls the real clearDegraded closure logic on the
// services struct. It is used by reload rollback tests to verify that the
// degraded fields are zeroed without spinning up the full service graph.
func (s *services) ClearDegradedForTest() {
	s.reloadMu.Lock()
	s.reloadDegraded = false
	s.reloadError = nil
	s.reloadMu.Unlock()
}

// SetOrphanWatchdogTimeoutForTest overrides orphanWatchdogTimeout for the duration
// of a test, restoring the original value at test cleanup.
// Used by TestSpawn_OrphanSubTurn_EmitsInterruptedAfter5s to avoid sleeping 5 seconds.
func SetOrphanWatchdogTimeoutForTest(d time.Duration) func() {
	orig := orphanWatchdogTimeout
	orphanWatchdogTimeout = d
	return func() { orphanWatchdogTimeout = orig }
}

// SetOrphanWatchdogMaxRechecksForTest overrides orphanWatchdogMaxRechecks for
// the duration of a test, restoring the original value at test cleanup. Used
// to prove the watchdog eventually force-fires a synthetic "interrupted"
// frame for a span whose IsSubTurnActiveForSpawnCall liveness check reports
// active forever (a genuinely wedged/deadlocked turn) without the test
// needing to wait for the real 15-reschedule default ceiling.
func SetOrphanWatchdogMaxRechecksForTest(n int) func() {
	orig := orphanWatchdogMaxRechecks
	orphanWatchdogMaxRechecks = n
	return func() { orphanWatchdogMaxRechecks = orig }
}
