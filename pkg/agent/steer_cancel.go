// steer_cancel.go implements ADR-091 I-6: SteerCanceller.CancelSubtree/
// StopSubtree walk the durable steering edge, stamp a Stop marker on every
// reachable non-terminal descendant under the stopped node's own cascade
// lock, and cancel each live turn with the stamped generation;
// SteerCanceller.Revive increments a stopped or terminal session's
// generation under the same record lock so a newer instruction — the ONLY
// thing that may, per the founder's decision (Q17/D8) — can bring it back.
// See pkg/agent/CLAUDE.md's "Delegation" section for this file's place among
// the other three ADR-091 implementation files. Owned by ADR-091 fix lane 2.

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

	"github.com/elicify-ai/omnipus/pkg/logger"
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
func (al *AgentLoop) SteerGenerationCancel(ctx context.Context, sessionID string, generation int) (GenerationCancelResult, error) {
	// [Finding 4, ADR-091 fix lane 2] This is the ONE cancelTurn callback the
	// cascade invokes for every reached (stamped) session — a human's Stop
	// (websocket_cancel.go/rest_sessions.go, always via CancelSubtree) and
	// the agent's own hard delegate(cancel) alike. admission.go::
	// removeQueuedSession exists so a cancelled worker stops counting toward
	// queue positions reported to a model and shown in the side panel; before
	// this it was called only from the agent's own soft/hard cancel
	// (steer_delegate_cancel.go), never from a human's Stop. Draining here —
	// inside the cascade's per-node step, not one caller — covers both
	// without a second call site, and is a harmless no-op for a session that
	// was never queued.
	al.steerAdmission().removeQueuedSession(sessionID)

	ok, reason := al.requestCancelForGeneration(sessionID, generation)
	if ok {
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	}
	if strings.HasPrefix(reason, "stale generation:") {
		return GenerationCancelResult{Found: true, SkippedNewerGeneration: true}, nil
	}
	// [Finding 5, ADR-091 fix lane 2] No live turn was found for this
	// stamped session — it was only ever queued or parked and never ran a
	// turn, so nothing else will ever terminalise it or report it upward.
	al.terminaliseNeverRanStop(ctx, sessionID, generation)
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
		al.deliverSubagentState(rec.SteeredBy.SteeringSessionID, rec, string(session.LifecycleRunning), nil)
	}
	return nil
}

// reportSteeredSessionTerminalUpward lands sessionID terminal at nextState
// and, if it has a steering parent, delivers ONE upward event carrying
// outcome and failureReason first — mirroring completeSteeredTurn's own
// ordering (steer_completion.go: "Delivery is deliberately first; boot
// recovery can repair a delivered-but-not-terminal record, while
// terminal-first could lose the only copy of the child's result").
//
// Shared by two dead ends nothing else on the ADR-091 completion path ever
// visits, because both describe a session with no turnResult to build a
// normal completion from:
//   - Finding 5: a Stop cascade reached a session that was only ever queued
//     or parked — it never ran a turn, so completeSteeredTurn's own
//     post-turn-exit call site never fires for it.
//   - Finding 6: a queued session's promotion inside drainSteerQueue's
//     dispatch goroutine failed outright (a non-cancellation error) before
//     a turn ever started.
//
// Refuses (silently, logging at WARN on a real failure) whenever the record
// has already moved past generation, is already terminal, or the upward
// delivery itself fails — never overwrites state it cannot also report.
func (al *AgentLoop) reportSteeredSessionTerminalUpward(
	ctx context.Context, sessionID string, generation int,
	nextState session.LifecycleState, outcome steer.Outcome, failureReason string,
) {
	if al == nil {
		return
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return
	}
	if rec.Generation != generation || rec.Terminal() {
		return
	}
	// [Defect 1, ADR-091 fix lane RX-DELIVERY, CRITICAL] This early return
	// is the whole of the doc comment's "or the upward delivery itself
	// fails — never overwrites state it cannot also report" promise, which
	// the code did not keep: all three undelivered branches (message build
	// failed, no deliverer wired, Deliver returned an error) logged a WARN
	// and then FELL THROUGH to the terminal write below. The child landed
	// terminal with NO inbox entry — nothing left to recover from in
	// process, and the parent never told a descendant had gone away, so
	// hasRunningOrQueuedDescendant (steer_completion.go) kept the parent
	// waiting for ever on a worker that was already dead. Leaving the
	// record NON-terminal is strictly better: it stays visible to boot
	// recovery (boot_sweep.go::SteerBootRecovery) and to the operator,
	// which a terminal-but-unreported record is not.
	if rec.SteeredBy != nil && !al.deliverTerminalReport(ctx, rec, generation, outcome, failureReason) {
		return
	}
	// [Defect 2, ADR-091 fix lane RX-DELIVERY, CRITICAL] This used to be a
	// raw Load (above) -> Deliver (real I/O, just above) -> mutate the
	// PRE-Deliver in-memory snapshot -> Persist: the exact stale
	// read-then-write shape fix lane 1 replaced with LifecycleStore.Mutate
	// in steer_completion.go::completeSteeredTurn (Finding D) and
	// steer_launcher.go::commitSteeredDispatchState. Deliver's window is
	// wide — an inbox append, transcript writes and a parent wake — and a
	// Stop or a Revive landing inside it was silently ERASED, because the
	// snapshot's own (nil) Stop was written straight back over the marker.
	// pkg/session/lifecycle.go's rule is explicit: a caller doing
	// read-then-decide-then-write MUST use Mutate. Generation AND the Stop
	// marker are re-checked inside the SAME lock the write happens under,
	// and every field is set on `cur` — the record as it is NOW — so a Stop
	// stamped during delivery survives even on the paths that do write.
	//
	// Mutate is NOT reentrant: nothing inside this closure may call
	// Load/Persist/Mutate.
	stopBeforeDelivery := rec.Stop
	mutateErr := lifecycle.Mutate(sessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return errTerminalReportVanished
		}
		if cur.Generation != generation {
			return errTerminalReportStaleGeneration
		}
		if cur.Terminal() {
			return errTerminalReportAlreadyTerminal
		}
		if stopLandedDuringDelivery(stopBeforeDelivery, cur) {
			return errTerminalReportStoppedDuringDelivery
		}
		cur.State = nextState
		cur.NeedsInput = nil
		// A Stop marker is an INSTRUCTION ("do not run this generation"),
		// and landing the terminal state here is that instruction being
		// carried out — so it is spent and must be cleared. persistLocked
		// rejects a terminal record that still carries a current-generation
		// marker, and it is right to: the pair says "finished" and "still
		// waiting to be stopped" at once. Before that validation existed
		// this closure wrote exactly that shape to disk silently, which is
		// why a queued child that never ran stayed `queued` for ever after
		// a Stop instead of reaching `cancelled` (founder decision,
		// 2026-09-24: clear the marker; who stopped it and when live in the
		// event log, not on the record). An OLDER marker
		// (Stop.Generation < Generation) is inert history a revival
		// deliberately retains — Stopped()'s doc comment covers all four
		// shapes — so only the current generation's marker is cleared.
		if cur.Stop != nil && cur.Stop.Generation == cur.Generation {
			cur.Stop = nil
		}
		if nextState == session.LifecycleFailed {
			cur.FailedReason = failureReason
		}
		return nil
	})
	switch {
	case mutateErr == nil:
	case errors.Is(mutateErr, errTerminalReportStaleGeneration),
		errors.Is(mutateErr, errTerminalReportAlreadyTerminal),
		errors.Is(mutateErr, errTerminalReportStoppedDuringDelivery),
		errors.Is(mutateErr, session.ErrLifecycleTerminalImmutable):
		// The report is already durably delivered (Deliver ran above); a
		// Stop, a Revive or another completion racing this write is a
		// legitimate outcome, not a failure — mirrors completeSteeredTurn's
		// own refusal switch.
		logger.InfoCF("agent", "steer: terminal report: delivered; terminal write refused (a Stop, Revive or another completion landed during delivery)",
			map[string]any{"session_id": sessionID, "generation": generation, "reason": mutateErr.Error()})
	default:
		logger.WarnCF("agent", "steer: terminal report: persist terminal state failed",
			map[string]any{"session_id": sessionID, "generation": generation, "error": mutateErr.Error()})
	}
	// No second upward path here on purpose. The Deliver call above is the
	// only one: fix lane 1 deleted completeWaitingAncestors because it
	// completed the ancestor from the ancestor's OWN stale last answer
	// instead of re-entering it, so a nested child's real outcome never
	// arrived. Deliver's wake is the re-entry (steer_completion.go,
	// Finding A) and it carries this cancelled child's "interrupted:"
	// outcome up exactly like a normal completion does.
}

// deliverTerminalReport builds and delivers the ONE upward event a terminal
// report carries. It returns true ONLY when the parent's inbox entry is
// durably written — the precondition reportSteeredSessionTerminalUpward's
// terminal write now depends on (Defect 1).
//
// [Defect 3, ADR-091 fix lane RX-DELIVERY, HIGH] It is also the first caller
// anywhere in the codebase to READ steer.Delivery.Outcome. That field carries
// the one fact separating "the parent knows" (DeliveryWoke /
// DeliveryQueuedIntoLiveTurn) from "the parent will never know"
// (DeliveryStoredNotWoken), and every call site discarded it with
// `_, err := deliverer.Deliver(...)`. On a TERMINAL report a stored-not-woken
// outcome means the child is gone and nothing will re-enter the parent until
// boot recovery — an indefinite stall that was completely invisible. It is
// logged at ERROR, naming child, parent, message id and generation, and
// deliberately does NOT fail the report: the entry IS durable, so refusing
// the terminal write would only add a second inconsistency on top.
func (al *AgentLoop) deliverTerminalReport(
	ctx context.Context, rec *session.LifecycleRecord, generation int,
	outcome steer.Outcome, failureReason string,
) bool {
	message, merr := al.completionMessage(rec, outcome, "", failureReason)
	if merr != nil {
		logger.WarnCF("agent", "steer: terminal report: build upward message failed — terminal state NOT written",
			map[string]any{"session_id": rec.SessionID, "generation": generation, "error": merr.Error()})
		return false
	}
	deliverer := al.getUpwardDeliverer()
	if deliverer == nil {
		logger.WarnCF("agent", "steer: terminal report: no upward deliverer wired — terminal state NOT written",
			map[string]any{"session_id": rec.SessionID, "generation": generation})
		return false
	}
	delivery, derr := deliverer.Deliver(ctx, steer.UpwardEvent{
		ChildSessionID: rec.SessionID, Outcome: outcome, Message: message,
	})
	if derr != nil {
		logger.WarnCF("agent", "steer: terminal report: deliver upward event failed — terminal state NOT written",
			map[string]any{"session_id": rec.SessionID, "generation": generation, "error": derr.Error()})
		return false
	}
	if delivery.Outcome == steer.DeliveryStoredNotWoken {
		logger.ErrorCF("agent", "steer: terminal report: stored but the parent was NOT woken — it will wait until boot recovery re-nudges it",
			map[string]any{
				"session_id":        rec.SessionID,
				"parent_session_id": rec.SteeredBy.SteeringSessionID,
				"message_id":        delivery.MessageID,
				"generation":        generation,
				"outcome":           string(outcome),
				"delivery_outcome":  string(delivery.Outcome),
			})
	}
	return true
}

// stopLandedDuringDelivery reports whether cur carries a Stop marker for its
// CURRENT generation that was NOT already on the snapshot this report was
// built from — i.e. a Stop pressed while Deliver was doing its I/O.
//
// A marker that was already on the snapshot is the ordinary case, not a
// race: terminaliseNeverRanStop (below) reaches this function precisely
// BECAUSE the cascade has just stamped one, and refusing on it would leave
// Finding 5's never-ran child stranded for ever — the opposite of the bug.
// Generation alone identifies the marker: stampStop refuses to re-stamp a
// generation that already carries one (errStopAlreadyStamped), so two
// distinct markers can never share a generation.
func stopLandedDuringDelivery(before *session.Stop, cur *session.LifecycleRecord) bool {
	if cur.Stop == nil || cur.Stop.Generation != cur.Generation {
		return false
	}
	return before == nil || before.Generation != cur.Stop.Generation
}

// errTerminalReport* are reportSteeredSessionTerminalUpward's Mutate-refusal
// sentinels (Defect 2) — the terminal-report counterparts of
// steer_completion.go's errComplete* family.
var (
	errTerminalReportVanished              = errors.New("steer: terminal report: record vanished during delivery")
	errTerminalReportStaleGeneration       = errors.New("steer: terminal report: generation changed during delivery")
	errTerminalReportAlreadyTerminal       = errors.New("steer: terminal report: record became terminal during delivery")
	errTerminalReportStoppedDuringDelivery = errors.New("steer: terminal report: a Stop landed during delivery")
)

// terminaliseNeverRanStop closes Finding 5's gap: SteerGenerationCancel found
// no live turn to cancel for a session this cascade just stamped Stop for —
// it was only ever queued or parked and never ran a turn, so nothing will
// ever produce the upward "interrupted:" event a RUNNING child's own
// cancelled turn delivers via completeSteeredTurn. Without this, the
// session's own parent (hasRunningOrQueuedDescendant, steer_completion.go)
// keeps seeing a queued/needs_input descendant forever and never completes.
func (al *AgentLoop) terminaliseNeverRanStop(ctx context.Context, sessionID string, generation int) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return
	}
	// Only a session THIS Stop actually stamped, still sitting at the
	// generation it was stamped for — a concurrent Revive landing in the
	// meantime must not be clobbered.
	if rec.Generation != generation || rec.Terminal() || rec.Stop == nil || rec.Stop.Generation != generation {
		return
	}
	// ADR-093 MAJ-001 (test-plan row "Web Stop on a chat root that has
	// delegated"): a session with NO steering edge — a standing chat root —
	// is never terminalised by its own Stop cascade. Finding 5's reason for
	// terminalising a never-ran session is that its own PARENT would
	// otherwise wait forever on it (hasRunningOrQueuedDescendant,
	// steer_completion.go); a chat root has no parent to strand, and the
	// MAJ-001 contract is that the record stays `running` carrying the
	// current-generation Stop marker the cascade has just stamped — the
	// exact state ADR-093 D4's revival continues from. Writing `cancelled`
	// here would also CLEAR that marker (reportSteeredSessionTerminalUpward
	// spends it in its Mutate), un-delivering the Stop the user pressed.
	if rec.SteeredBy == nil {
		return
	}
	al.reportSteeredSessionTerminalUpward(ctx, sessionID, generation,
		session.LifecycleCancelled, steer.OutcomeInterrupted, "interrupted: the session was cancelled")
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
	m, ok := lock.(*sync.Mutex)
	if !ok {
		// c.locks is private to this file and every value ever stored under
		// any key is a *sync.Mutex (the LoadOrStore above is the map's only
		// writer) — unreachable in practice. Guard rather than panic on a
		// hypothetically corrupt map instead of trusting that invariant blindly.
		return &sync.Mutex{}
	}
	return m
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
