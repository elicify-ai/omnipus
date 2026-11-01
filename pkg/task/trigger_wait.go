// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

// trigger_wait.go — the scheduling-wait invariant (tool-parity spec r17 §7.2
// TK-30). A task whose time trigger has not yet released it must not be moved
// to `next` by any automatic promotion; only the scheduler's own fire at the
// occurrence releases it. The rule is computed from persisted state only (the
// trigger, its next occurrence and the scheduler-delivery marker); no new
// status exists.
//
//   - manual or no trigger: never held.
//   - once: released only when the delivery marker records exactly the
//     trigger's at_ms (the scheduler delivered this occurrence). A future
//     at_ms is awaiting; a past one without that marker was missed and is
//     skipped, never replayed.
//   - recurring: held while its next occurrence is strictly in the future
//     (the next boundary governs; an exhausted series no longer holds).

import "time"

// TriggerNextOccurrence returns the next occurrence of tr strictly after
// nowMs. ok is false for a manual or nil trigger, a once trigger whose instant
// is not after nowMs, an exhausted recurring series, or a recurring trigger
// whose rule cannot be expanded.
func TriggerNextOccurrence(tr *Trigger, nowMs int64) (next int64, ok bool) {
	if tr == nil {
		return 0, false
	}
	switch tr.Type {
	case TriggerOnce:
		if tr.Config.AtMs == nil || *tr.Config.AtMs <= nowMs {
			return 0, false
		}
		return *tr.Config.AtMs, true
	case TriggerRecurring:
		cfg := tr.Config
		if cfg.Rrule == nil || *cfg.Rrule == "" || cfg.DtstartMs == nil || cfg.Tz == nil || *cfg.Tz == "" {
			return 0, false
		}
		n, found, err := NextOccurrenceAfter(*cfg.Rrule, *cfg.DtstartMs, *cfg.Tz, nowMs)
		if err != nil || !found {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

// TriggerAwaitingOccurrence reports whether t carries a once or recurring
// trigger whose next occurrence is strictly after now (scheduling state 1).
func TriggerAwaitingOccurrence(t *Task, now time.Time) bool {
	if t == nil {
		return false
	}
	_, ok := TriggerNextOccurrence(t.Trigger, now.UnixMilli())
	return ok
}

// OnceDelivered reports whether t's once trigger was delivered by the
// scheduler: the marker records exactly the current trigger's at_ms.
func OnceDelivered(t *Task) bool {
	if t == nil || t.Trigger == nil || t.Trigger.Type != TriggerOnce || t.Trigger.Config.AtMs == nil {
		return false
	}
	return t.LastDeliveredAtMs != nil && *t.LastDeliveredAtMs == *t.Trigger.Config.AtMs
}

// TriggerHoldsPromotion reports whether t's trigger forbids an automatic
// promotion to `next` at now: the task is still awaiting its occurrence, or
// (for once) its occurrence passed without a recorded delivery.
func TriggerHoldsPromotion(t *Task, now time.Time) bool {
	if t == nil || t.Trigger == nil {
		return false
	}
	switch t.Trigger.Type {
	case TriggerOnce:
		return !OnceDelivered(t)
	case TriggerRecurring:
		return TriggerAwaitingOccurrence(t, now)
	default:
		return false
	}
}
