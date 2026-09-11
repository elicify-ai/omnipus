// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package goal

import (
	"fmt"
	"log/slog"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// Sweep applies GOAL-FR-043/FR-044's retention rule to every goal record in
// store (wave S3 of the joint ADR-084/ADR-085/ADR-086 delivery, US-10/EC-10,
// S-40/S-41). Two independent passes over the same List() snapshot:
//
//  1. FR-044/EC-10 — a goal whose ActiveSessionID no longer resolves to a
//     real session (because the session was already removed, by
//     pkg/session/retention_sweep.go's own sweep pass or otherwise) MUST
//     NOT be left dangling. It is terminal-expired right here, at this
//     sweep pass, via Terminate(generated.GoalStateExpired, ...). Because
//     Terminate stamps LastActivityAt to now, the just-expired record is
//     NOT also deleted in this same pass — it becomes eligible for
//     the age-based removal below only once it, in turn, ages past the
//     retention window, which is exactly what "terminal-expired at the
//     next sweep" (EC-10's wording) means observed across repeated calls:
//     this call notices the dangling reference and terminates it; a LATER
//     call, once the record itself is old enough, removes it for good.
//     This is only the EC-10 half of FR-044 — the OTHER half ("deleting a
//     task MUST transition and remove its goal") is a direct call from
//     pkg/task/store.go's own deletion path (wave E5, outside this
//     package's write-set) and is not implemented here.
//
//  2. FR-043 — a TERMINAL record (IsTerminalState) whose LastActivityAt is
//     older than retentionDays is removed outright, mirroring exactly the
//     rule pkg/session/retention_sweep.go::RetentionSweep applies to
//     session transcripts (same cutoff computation, same units). A
//     defining/active record is never removed by age alone: D14's whole
//     point is that a terminal record is now RETAINED (never
//     field-zero-erased, FR-027/FR-028) and therefore needs its own sweep
//     to avoid piling up forever (US-10's title) — an active goal is not
//     "piling up", it is doing its job, and reaches terminal through
//     normal operation (the Judge, the budget, the keeper's idle-expiry
//     sweep, D-A) or through pass 1 above.
//
// sessionExists is a pure predicate with no locking semantics of its own as
// far as THIS package is concerned: "does a session with this id currently
// exist". This package deliberately does not import pkg/session (mirrors
// doc.go's "this package does not itself import pkg/session" and
// lock_test.go's TestGoalSessionLockOrder comment) — the caller that DOES
// hold a *session.UnifiedStore (pkg/session/retention_sweep.go's
// goalRetentionSweepFn hook) builds this predicate from it and passes it
// in, which is what keeps this package free of that dependency and avoids
// a pkg/goal <-> pkg/session import cycle (pkg/session must be able to
// call INTO this sweep as a hook without pkg/goal calling back into
// pkg/session).
//
// Lock order (R-33, doc.go, binding across the whole delivery):
//
//	goalLock -> taskFileLock -> sessionLock -> cacheMu
//
// Sweep only ever takes GOAL locks (via store.Update/store.Delete, which
// internally acquire and fully release the pkg/entity striped-mutex +
// sidecar-flock pair before returning). It never itself acquires a session
// lock — sessionExists is expected to be a plain predicate function, not a
// closure that reaches back into a session store while a goal lock from
// THIS call is still held; pkg/session/retention_sweep_test.go's
// TestRetentionSweep_GoalHookRunsAfterShardsReleased proves the converse
// half of that invariant: this hook is only ever invoked once
// pkg/session's own ADR-057 session-shard lock (lockAllSessionShards) has
// already been fully released, never nested inside it.
//
// Windows posture (restated, mirrors doc.go): fileutil.WithFlock is a
// documented no-op on Windows, so store.Update/store.Delete's
// cross-process mutual-exclusion guarantee is POSIX-only there — on
// Windows only the in-process striped mutex protects concurrent goal-file
// writes, including this sweep's own.
//
// When retentionDays <= 0, Sweep is a no-op and returns (0, 0, nil) —
// mirroring pkg/session/retention_sweep.go::RetentionSweep's own
// retentionDays <= 0 contract, so the two stay behaviourally identical on
// "retention disabled".
func Sweep(store *Store, retentionDays int, sessionExists func(sessionID string) bool, now time.Time) (removed, expired int, err error) {
	if retentionDays <= 0 {
		return 0, 0, nil
	}
	if store == nil {
		return 0, 0, fmt.Errorf("goal: sweep: nil store")
	}
	if sessionExists == nil {
		return 0, 0, fmt.Errorf("goal: sweep: nil sessionExists predicate")
	}

	goals, skipped, err := store.List()
	if err != nil {
		return 0, 0, fmt.Errorf("goal: sweep: list: %w", err)
	}
	if len(skipped) > 0 {
		slog.Warn("goal: sweep: skipping unparseable records",
			"count", len(skipped), "ids", skipped)
	}

	cutoff := now.Add(-time.Duration(retentionDays) * 24 * time.Hour)

	for i := range goals {
		g := goals[i]

		if IsActiveState(g.State) && g.ActiveSessionID != "" && !sessionExists(g.ActiveSessionID) {
			id := g.GoalID
			if _, uerr := store.Update(id, func(gg *Goal) error {
				if !IsActiveState(gg.State) {
					// Raced with another mutation between List() and this
					// Update (e.g. it was already terminated by the engine
					// concurrently) — nothing left to expire.
					return nil
				}
				return gg.Terminate(generated.GoalStateExpired,
					"active session no longer exists (retention sweep, GOAL-FR-044/EC-10)", now)
			}); uerr != nil {
				slog.Warn("goal: sweep: terminal-expire failed",
					"goal_id", id, "error", uerr)
			} else {
				expired++
			}
			continue
		}

		if IsTerminalState(g.State) && g.LastActivityAt.Before(cutoff) {
			if derr := store.Delete(g.GoalID); derr != nil {
				slog.Warn("goal: sweep: delete failed",
					"goal_id", g.GoalID, "error", derr)
				continue
			}
			removed++
		}
	}

	return removed, expired, nil
}
