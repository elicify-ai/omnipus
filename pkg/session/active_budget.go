// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Real-activity bookkeeping for a steered session (ADR-20260928 D8.7).
//
// This file used to also hold the active-time budget (ActiveBudgetDeadline,
// ApplyExplicitResumeBudget) that force-stopped a delegated session after
// its timeout. That budget is gone: delegated work has no time limit and
// ends only when it finishes or is explicitly stopped (issue #1306).
//
// not-wire-format: LastActivityAt is an internal disk field on
// LifecycleRecord, not a Session or StopNote wire field.
package session

import "time"

// NoteRealActivity records at as the latest moment this session was actually
// working. A zero time is ignored. A restart landing must not call this:
// moving the clock forward to boot time would hide the real last activity.
func (rec *LifecycleRecord) NoteRealActivity(at time.Time) {
	if rec == nil || at.IsZero() {
		return
	}
	at = at.UTC()
	if rec.LastActivityAt.IsZero() || at.After(rec.LastActivityAt) {
		rec.LastActivityAt = at
	}
}
