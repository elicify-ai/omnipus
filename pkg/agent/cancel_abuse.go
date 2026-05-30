// Package agent — cancel_abuse.go
//
// cancelAbuseDetector tracks cancel-request rates per (user, channel) pair and
// emits a cancel.abuse_pattern audit event when a burst threshold is crossed
// within a sliding time window (FR-25a).
//
// Moved from pkg/gateway so all four cancel entry points (web, Tier A /cancel,
// Tier B text-parsing, CLI) share a single abuse detector on the AgentLoop.

package agent

import (
	"context"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// abuseKey uniquely identifies a (user, channel) pair as a map key.
// Using a struct avoids the key-collision bug that the string form
// user + "|" + channel has when user or channel contain the separator.
type abuseKey struct {
	User    string
	Channel string
}

// cancelAbuseDetector tracks cancel-request rates per (user, channel) pair and
// emits a cancel.abuse_pattern audit event when a burst threshold is crossed
// within a sliding time window (FR-25a).
type cancelAbuseDetector struct {
	mu      sync.Mutex
	windows map[abuseKey][]time.Time
	window  time.Duration // sliding window size (default 60s)
	burstAt int           // burst threshold (default 10)
	// evictEvery controls how often we scan for and delete stale entries.
	// On every Nth call to recordAttempt a full eviction pass is run.
	evictEvery int
	callCount  int
}

func newCancelAbuseDetector() *cancelAbuseDetector {
	return &cancelAbuseDetector{
		windows:    make(map[abuseKey][]time.Time),
		window:     60 * time.Second,
		burstAt:    10,
		evictEvery: 50, // evict stale entries every 50 calls
	}
}

// recordAttempt records a cancel attempt for the given (user, channel) pair.
// If the number of attempts within the sliding window reaches burstAt, a single
// cancel.abuse_pattern WARNING audit entry is emitted and the window entry is
// deleted (so the next burst generates a fresh event, not a flood).
//
// The mutex is released before calling audit.Emit to avoid holding a lock over
// a disk-backed write operation.
//
// auditLogger may be nil (audit disabled); the emit is a no-op in that case.
func (d *cancelAbuseDetector) recordAttempt(
	ctx context.Context,
	user, channel string,
	at time.Time,
	auditLogger *audit.Logger,
) {
	key := abuseKey{User: user, Channel: channel}
	cutoff := at.Add(-d.window)

	d.mu.Lock()

	// Prune timestamps older than the window.
	entries := d.windows[key]
	filtered := entries[:0]
	for _, t := range entries {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	filtered = append(filtered, at)

	var shouldEmit bool
	var count int
	var windowSecs float64

	if len(filtered) >= d.burstAt {
		// Capture burst data before releasing the lock.
		count = len(filtered)
		windowSecs = d.window.Seconds()
		shouldEmit = true
		// Delete the entry so the next burst generates a fresh event.
		delete(d.windows, key)
	} else {
		d.windows[key] = filtered
	}

	// Periodic eviction: remove keys whose newest timestamp is older than 2×
	// window (covers transient users who are no longer active).
	d.callCount++
	if d.callCount%d.evictEvery == 0 {
		d.evictStale(at)
	}

	d.mu.Unlock()

	// Emit outside the critical section — audit.Emit may do disk I/O.
	if shouldEmit {
		audit.Emit(ctx, auditLogger, audit.EventCancelAbusePattern, audit.SeverityWarn, map[string]any{
			"canceller_user":     user,
			"canceller_channel":  channel,
			"attempts_in_window": count,
			"window_seconds":     windowSecs,
		})
	}
}

// evictStale removes all map entries whose newest timestamp is older than
// 2× the sliding window duration. Must be called with d.mu held.
func (d *cancelAbuseDetector) evictStale(now time.Time) {
	staleBefore := now.Add(-2 * d.window)
	for k, ts := range d.windows {
		if len(ts) == 0 {
			delete(d.windows, k)
			continue
		}
		newest := ts[len(ts)-1]
		if newest.Before(staleBefore) {
			delete(d.windows, k)
		}
	}
}
