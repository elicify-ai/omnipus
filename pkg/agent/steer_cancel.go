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
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// SteerCanceller implements ADR-091's durable Stop cascade and revival.
type SteerCanceller struct {
	Lifecycle          *session.LifecycleStore
	cancelTurn         GenerationCancelFunc
	revivalStateWriter RevivalStateWriter
	locks              sync.Map
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

// RevivalStateWriter persists the parent's subagent_state(running) lifecycle
// event after Revive has durably published the new generation. WP-B supplies
// the transcript/frame implementation at the composition root.
type RevivalStateWriter func(ctx context.Context, sessionID string, generation int) error

// SteerGenerationCancel adapts the active-turn registry to the durable
// generation-aware cancellation contract used by SteerCanceller.
func (al *AgentLoop) SteerGenerationCancel(_ context.Context, sessionID string, generation int) (GenerationCancelResult, error) {
	ok, reason := al.requestCancelForGeneration(sessionID, generation)
	if ok {
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	}
	if strings.HasPrefix(reason, "stale generation:") {
		return GenerationCancelResult{Found: true, SkippedNewerGeneration: true}, nil
	}
	return GenerationCancelResult{}, nil
}

// WriteSteerRevivalState publishes the running state created by Revive to the
// session that owns the durable steering edge.
func (al *AgentLoop) WriteSteerRevivalState(_ context.Context, sessionID string, generation int) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return session.ErrLifecycleNotFound
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return err
	}
	if rec.Generation != generation {
		return steer.ErrStaleGeneration
	}
	if rec.SteeredBy != nil {
		al.deliverSubagentState(rec.SteeredBy.SteeringSessionID, rec, string(session.LifecycleRunning))
	}
	return nil
}

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

// SetRevivalStateWriter installs the I-4 lifecycle-event writer. It is a
// construction-time option and must not be changed after the canceller is
// published to concurrent callers.
func (c *SteerCanceller) SetRevivalStateWriter(writer RevivalStateWriter) *SteerCanceller {
	if c != nil {
		c.revivalStateWriter = writer
	}
	return c
}

// CancelSubtree implements I-6 as one operation under the stopped node's
// cascade lock: enumerate, stamp, generation-aware cancel, then enumerate once
// more for children published during the first pass.
func (c *SteerCanceller) CancelSubtree(ctx context.Context, sessionID string, by steer.Principal) (steer.CancelReport, error) {
	return c.cascade(ctx, sessionID, by, true)
}

// StopSubtree is CancelSubtree's DURABLE half on its own: the same cascade
// lock, the same two enumeration passes, the same Stop markers — but the live
// turns are left to stop themselves.
//
// It exists for delegate(action="cancel", hard=false), whose contract
// (ADR-053 R§Cancel/restart) promises the child a checkpoint-flush window
// before anything is torn out from under it. The marker still has to land
// immediately, because it is what stops admission ever promoting a queued
// session that has been cancelled (reserveDispatch, below) — and a marker is
// durable where a request to a live turn is not. The caller asks the reached
// turns to stop cooperatively and escalates after its own grace window;
// see steer_delegate_cancel.go::cancelDelegatedSubtree.
func (c *SteerCanceller) StopSubtree(ctx context.Context, sessionID string, by steer.Principal) (steer.CancelReport, error) {
	return c.cascade(ctx, sessionID, by, false)
}

func (c *SteerCanceller) cascade(
	ctx context.Context, sessionID string, by steer.Principal, cancelLiveTurns bool,
) (steer.CancelReport, error) {
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

	fireLiveCancels := func() {
		if cancelLiveTurns {
			c.cancelStamped(ctx, stamped, &report)
		}
	}

	first, walkErr := CollectDescendantSessionIDs(c.Lifecycle, sessionID)
	if walkErr != nil {
		report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: walkErr.Error()})
	}
	process(append([]string{sessionID}, first...))
	fireLiveCancels()

	second, secondErr := CollectDescendantSessionIDs(c.Lifecycle, sessionID)
	if secondErr != nil {
		report.Unreachable = appendUniqueUnreachable(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: secondErr.Error()})
	}
	lateStart := len(stamped)
	process(second)
	if len(stamped) > lateStart {
		fireLiveCancels()
	}
	return report, nil
}

// steerCancellerRegistry maps *AgentLoop -> the ONE I-6 Canceller its Stop
// surfaces share. A package-level side table rather than an AgentLoop field
// for the reason admission.go::steerAdmissionRegistry gives: loop.go is
// line-count-pinned (scripts/budgets/files.txt) and cannot grow by a field
// declaration. Entries are never removed, on the same bounded-leak reasoning.
//
// Sharing one instance is load-bearing, not tidiness: CancelSubtree
// serialises a subtree on the stopped node's own cascade lock, and two
// cancellers would take two different locks for the same session — so a
// human's Stop and an agent's delegate(action="cancel") could interleave
// mid-cascade.
var (
	steerCancellerRegistry   = map[*AgentLoop]*SteerCanceller{}
	steerCancellerRegistryMu sync.Mutex
)

// SetSteerCanceller publishes the composition root's Canceller (gateway
// boot's wireSteerDeps) as the one al's own Stop paths use.
func (al *AgentLoop) SetSteerCanceller(canceller *SteerCanceller) {
	if al == nil || canceller == nil {
		return
	}
	steerCancellerRegistryMu.Lock()
	defer steerCancellerRegistryMu.Unlock()
	steerCancellerRegistry[al] = canceller
}

// steerCanceller returns al's Canceller, constructing one on first use wired
// to al's own generation-aware live-turn adapter (SteerGenerationCancel) so a
// lane or focused test that never reached gateway boot still cascades for
// real rather than stamping markers nothing acts on.
func (al *AgentLoop) steerCanceller() *SteerCanceller {
	steerCancellerRegistryMu.Lock()
	defer steerCancellerRegistryMu.Unlock()
	if c, ok := steerCancellerRegistry[al]; ok {
		return c
	}
	c := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
	steerCancellerRegistry[al] = c
	return c
}

// Revive mints a generation for a stopped session or terminal follow-up.
// LifecycleStore.Mutate holds the record's write lock across the read,
// decision, and append, so a concurrent Stop is applied in arrival order.
// The old Stop marker is retained as inert history: reserveDispatch compares
// its generation with the newly incremented record generation.
func (c *SteerCanceller) Revive(ctx context.Context, sessionID string, _ steer.Principal) (int, error) {
	if c == nil || c.Lifecycle == nil {
		return 0, session.ErrLifecycleNotFound
	}
	var generation int
	var revived bool
	err := c.Lifecycle.Mutate(sessionID, func(rec *session.LifecycleRecord) error {
		if rec == nil {
			return session.ErrLifecycleNotFound
		}
		generation = rec.Generation
		stopped := rec.Stop != nil && rec.Stop.Generation == rec.Generation
		if !stopped && !rec.Terminal() {
			return nil
		}

		rec.Generation++
		rec.ResumedFrom = rec.SessionID
		rec.State = session.LifecycleRunning
		rec.FailedReason = ""
		rec.NeedsInput = nil
		generation = rec.Generation
		revived = true
		return nil
	})
	if err != nil {
		return 0, err
	}
	if revived && c.revivalStateWriter != nil {
		if err := c.revivalStateWriter(ctx, sessionID, generation); err != nil {
			return generation, fmt.Errorf("steer: write revival state for %q generation %d: %w", sessionID, generation, err)
		}
	}
	return generation, nil
}

// reserveDispatch is I-6's package-internal reservation primitive
// (landing order I-6: "internal to pkg/agent, called by Dispatch"). The
// live guard refuses a Stop marker for the record's current generation
// (ErrDispatchCancelled), a stale generation (ErrStaleGeneration), or a
// terminal record with no follow-up (ErrTerminal). SteerLauncher.Dispatch
// calls it before admitting or queueing a turn.
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
