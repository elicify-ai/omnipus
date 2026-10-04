// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Active-time budget for a steered session (ADR-20260928 D6 / D8.7).
//
// The lifetime limit is remaining active time, not wall clock from CreatedAt.
// Time stopped, and the gap from the last real activity through a restart,
// does not consume it. A voluntary resume spends whatever is left. A timeout
// stop has already spent the budget, so an explicit resume starts a fresh
// Limits.TimeoutSeconds at the resume instant.
//
// not-wire-format: LastActivityAt, ActiveBudgetAnchor and StoppedForSeconds
// are internal disk fields on LifecycleRecord. They are not Session or
// StopNote wire fields. StopNote.Seq remains the generation stand-in until
// a typed control ledger supplies a real per-child sequence; this file does
// not mint control ids or stop sequences.
//
// Not done here: no production boot writer lands cause "restart" with
// by "restart" while leaving LastActivityAt at the pre-crash persist.
// failInterrupted still rewrites a non-terminal record to failed. Until that
// writer exists, restart downtime is credited only when a resume actually
// observes a restart note whose At is later than LastActivityAt.
package session

import "time"

// NoteRealActivity records at as the latest moment this session was actually
// working. A zero time is ignored. A restart landing must not call this:
// moving the clock forward to boot time would hide the downtime D8.7 credits.
func (rec *LifecycleRecord) NoteRealActivity(at time.Time) {
	if rec == nil || at.IsZero() {
		return
	}
	at = at.UTC()
	if rec.LastActivityAt.IsZero() || at.After(rec.LastActivityAt) {
		rec.LastActivityAt = at
	}
}

// ActiveBudgetDeadline is the absolute deadline for one execution of rec.
// ok is false when no positive timeout is configured. With no anchor and no
// stopped credit this is CreatedAt + TimeoutSeconds, or now + TimeoutSeconds
// when CreatedAt was never set — the same bound steered turns already used.
func (rec *LifecycleRecord) ActiveBudgetDeadline(now time.Time) (deadline time.Time, ok bool) {
	if rec == nil || rec.SteeredBy == nil || rec.SteeredBy.Limits.TimeoutSeconds <= 0 {
		return time.Time{}, false
	}
	anchor := rec.ActiveBudgetAnchor
	if anchor.IsZero() {
		anchor = rec.CreatedAt
	}
	if anchor.IsZero() {
		anchor = now
	}
	credit := time.Duration(0)
	if rec.StoppedForSeconds > 0 {
		credit = time.Duration(rec.StoppedForSeconds) * time.Second
	}
	limit := time.Duration(rec.SteeredBy.Limits.TimeoutSeconds) * time.Second
	return anchor.Add(limit + credit), true
}

// ApplyExplicitResumeBudget persists the budget consequence of an explicit
// resume of a stopped session. Call it inside the lifecycle mutation that
// clears the stop note, before the note is cleared and before any dispatch.
// A missing note is not a boundary: no credit and no reset are invented.
//
// cause timeout replaces the anchor with now and clears stopped credit, so
// the next run gets a full TimeoutSeconds instead of expiring immediately.
// Any other cause adds the stopped gap (whole seconds) to StoppedForSeconds.
// A restart note uses LastActivityAt when that timestamp is earlier than the
// note, so time since the last real persist is credit rather than budget.
func ApplyExplicitResumeBudget(rec *LifecycleRecord, now time.Time) {
	if rec == nil || rec.StopNote == nil || now.IsZero() {
		return
	}
	now = now.UTC()
	note := rec.StopNote
	if note.Cause == StopCauseTimeout {
		rec.ActiveBudgetAnchor = now
		rec.StoppedForSeconds = 0
		return
	}
	boundary := note.At
	if note.Cause == StopCauseRestart && !rec.LastActivityAt.IsZero() && rec.LastActivityAt.Before(boundary) {
		boundary = rec.LastActivityAt
	}
	if boundary.IsZero() || !now.After(boundary) {
		return
	}
	delta := int64(now.Sub(boundary) / time.Second)
	if delta > 0 {
		rec.StoppedForSeconds += delta
	}
}
