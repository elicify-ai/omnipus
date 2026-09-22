// Owner: WP-D (landing order §7: "the WP-A lane writes the compiled no-op
// bodies for the interfaces WP-B and WP-D later implement, in files named
// for their owners ... ownership of those files passes to WP-B and WP-D at
// CP-0"). WP-A (this lane) writes this file's CP-0 stub bodies only; WP-D
// replaces them with the real I-6 cascade, reservation and revival at CP-3.
// Do not add production logic here after CP-0 — that is WP-D's.

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// SteerCanceller is the CP-0 compiled stub for steer.Canceller, owned by
// WP-D from CP-0 onward. CancelSubtree reaches nothing; Revive returns the
// current generation unchanged — placeholders until WP-D's I-6 cascade
// lands at CP-3.
type SteerCanceller struct {
	Lifecycle  *session.LifecycleStore
	cancelTurn GenerationCancelFunc
	locks      sync.Map
}

var _ steer.Canceller = (*SteerCanceller)(nil)

// GenerationCancelResult is the live-turn registry's answer to a cancel
// carrying the generation stamped on the durable record.
type GenerationCancelResult struct {
	Found                  bool
	Cancelled              bool
	SkippedNewerGeneration bool
}

// GenerationCancelFunc cancels a live turn only when its registered
// generation equals generation. WP-A wires the turn registry implementation;
// the indirection keeps the durable cascade independently testable.
type GenerationCancelFunc func(ctx context.Context, sessionID string, generation int) (GenerationCancelResult, error)

// NewSteerCanceller builds the I-6 Canceller. cancelTurn is optional only so
// CP-0 wiring continues to compile until WP-A's generation-aware turn registry
// lands; production wiring must supply it before ADR-091 is reachable.
func NewSteerCanceller(lifecycle *session.LifecycleStore, cancelTurn ...GenerationCancelFunc) *SteerCanceller {
	c := &SteerCanceller{Lifecycle: lifecycle}
	if len(cancelTurn) > 0 {
		c.cancelTurn = cancelTurn[0]
	}
	return c
}

// CancelSubtree implements I-6 as one operation under the stopped node's
// cascade lock: enumerate, stamp, generation-aware cancel, then enumerate once
// more for children published during the first pass.
func (c *SteerCanceller) CancelSubtree(ctx context.Context, sessionID string, by steer.Principal) (steer.CancelReport, error) {
	var report steer.CancelReport
	if c == nil || c.Lifecycle == nil {
		report.Unreachable = append(report.Unreachable, steer.UnreachableSession{
			ID: sessionID, Reason: "lifecycle store is not configured",
		})
		return report, nil
	}
	if sessionID == "" {
		return report, fmt.Errorf("steer: cancel subtree: session id is required")
	}

	lock := c.cascadeLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	stamped := make(map[string]int)
	seen := make(map[string]struct{})
	at := time.Now().UTC()
	process := func(ids []string) {
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			generation, outcome, err := c.stampStop(id, at, by)
			switch {
			case errors.Is(err, errCascadeTerminal):
				report.SkippedTerminal = append(report.SkippedTerminal, id)
			case err != nil:
				report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: id, Reason: err.Error()})
			case outcome == stopStamped || outcome == stopAlreadyStamped:
				stamped[id] = generation
				report.Reached = append(report.Reached, id)
			}
		}
	}

	first, walkErr := CollectDescendantSessionIDs(c.Lifecycle, sessionID)
	if walkErr != nil {
		report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: walkErr.Error()})
	}
	process(append([]string{sessionID}, first...))
	c.cancelStamped(ctx, stamped, &report)

	second, secondErr := CollectDescendantSessionIDs(c.Lifecycle, sessionID)
	if secondErr != nil {
		report.Unreachable = appendUniqueUnreachable(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: secondErr.Error()})
	}
	lateStart := len(stamped)
	process(second)
	if len(stamped) > lateStart {
		c.cancelStamped(ctx, stamped, &report)
	}
	return report, nil
}

// Revive implements steer.Canceller. CP-0 stub: returns the record's
// current generation unchanged (no revival takes place).
func (c *SteerCanceller) Revive(_ context.Context, sessionID string, _ steer.Principal) (int, error) {
	if c.Lifecycle == nil {
		return 0, nil
	}
	rec, err := c.Lifecycle.Load(sessionID)
	if err != nil {
		return 0, err
	}
	return rec.Generation, nil
}

// reserveDispatch is I-6's package-internal reservation primitive
// (landing order I-6: "internal to pkg/agent, called by Dispatch"). The
// CP-0 stub admits everything — WP-D's phase-3 body refuses a Stop marker
// for the record's current generation (ErrDispatchCancelled), a stale
// generation (ErrStaleGeneration), or a terminal record with no follow-up
// (ErrTerminal). Nothing calls this yet: SteerLauncher.Dispatch is itself a
// CP-0 stub (steer_launcher.go).
func reserveDispatch(rec *session.LifecycleRecord, gen int) (ok bool, reason string) {
	if rec == nil {
		return false, steer.ErrInvalidEdge.Error()
	}
	if gen < rec.Generation {
		return false, steer.ErrStaleGeneration.Error()
	}
	if rec.Terminal() {
		return false, steer.ErrTerminal.Error()
	}
	if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
		return false, steer.ErrDispatchCancelled.Error()
	}
	return true, ""
}

type stopStampOutcome uint8

const (
	stopStamped stopStampOutcome = iota + 1
	stopAlreadyStamped
)

var errCascadeTerminal = errors.New("steer: cancel subtree: terminal record")

func (c *SteerCanceller) cascadeLock(sessionID string) *sync.Mutex {
	lock, _ := c.locks.LoadOrStore(sessionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func (c *SteerCanceller) stampStop(sessionID string, at time.Time, by steer.Principal) (int, stopStampOutcome, error) {
	var generation int
	var outcome stopStampOutcome
	err := c.Lifecycle.Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		if rec.Terminal() {
			return errCascadeTerminal
		}
		generation = rec.Generation
		if rec.Stop != nil && rec.Stop.Generation == rec.Generation {
			outcome = stopAlreadyStamped
			return errStopAlreadyStamped
		}
		rec.Stop = &session.Stop{At: at, Generation: rec.Generation, By: by}
		outcome = stopStamped
		return nil
	})
	if errors.Is(err, errStopAlreadyStamped) {
		return generation, outcome, nil
	}
	return generation, outcome, err
}

var errStopAlreadyStamped = errors.New("steer: cancel subtree: current generation already stamped")

func (c *SteerCanceller) cancelStamped(ctx context.Context, stamped map[string]int, report *steer.CancelReport) {
	if c.cancelTurn == nil {
		return
	}
	for _, id := range report.Reached {
		generation, ok := stamped[id]
		if !ok {
			continue
		}
		delete(stamped, id)
		result, err := c.cancelTurn(ctx, id, generation)
		if err != nil {
			report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: id, Reason: err.Error()})
			continue
		}
		if result.SkippedNewerGeneration {
			report.SkippedNewerGeneration = append(report.SkippedNewerGeneration, id)
		}
	}
}

func appendUniqueUnreachable(items []steer.UnreachableSession, item steer.UnreachableSession) []steer.UnreachableSession {
	for _, existing := range items {
		if existing.ID == item.ID && existing.Reason == item.Reason {
			return items
		}
	}
	return append(items, item)
}
