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

// WriteSteerRevivalState publishes the state Revive durably landed —
// running for a terminal follow-up's minted generation, queued for a
// same-generation resume (D2) — to the session that owns the durable
// steering edge.
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
		al.deliverSubagentState(rec.SteeredBy.SteeringSessionID, rec, string(rec.State), nil)
	}
	return nil
}

// reportSteeredSessionTerminalUpward lands sessionID at nextState for the
// two dead ends nothing else on the completion path ever visits:
//   - Finding 5: a Stop cascade reached a session that was only ever queued
//     or parked — it never ran a turn, so completeSteeredTurn's own
//     post-turn-exit call site never fires for it.
//   - Finding 6: a queued session's promotion inside drainSteerQueue's
//     dispatch goroutine failed outright (a non-cancellation error) before
//     a turn ever started.
//
// D2 CRIT-001 (ADR-20260928 sub-agent control plane) rewrote both halves:
//
//   - A STOPPED landing (the stop path) writes the durable state and
//     NOTHING upward: no inbox message, no frame, no wake, and never the
//     deterministic final id. The direct parent is told by the D6
//     stopped-child notice (cause/actor/time, dedup key
//     (parent, child, generation, stop_seq)) — a separate deliverable, not
//     wired by this function. The old "interrupted: the session was
//     cancelled" error event published here pre-ADR is exactly the losing
//     publication T11 pins, and is deleted with the deliver-first ordering.
//   - A genuine terminal FAILURE (Finding 6) goes through D2's one
//     outcome/publication commit (steer_completion_commit.go): terminal
//     state AND the protected outbox tuple in one mutation, then publish
//     from the committed bytes. A publish failure leaves the committed
//     outbox entry pending and retryable — the record no longer has to stay
//     non-terminal to stay recoverable, which was Defect 1's compromise.
//
// Refuses (logged, never fatal to the caller) when the record has moved
// past generation, is already terminal, or is already stopped — a Stop, a
// Revive or another completion racing this write is a legitimate outcome.
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
	if nextState == session.LifecycleStopped {
		al.landSteeredStopReport(ctx, sessionID, generation, outcome)
		return
	}
	// Genuine terminal failure: the one outcome/publication commit, then
	// publish from the committed outbox entry.
	res, commitErr := al.commitSteeredCompletion(lifecycle, rec, nextState, outcome, "", failureReason)
	if commitErr != nil {
		logger.WarnCF("agent", "steer: terminal report: outcome/outbox commit failed",
			map[string]any{"session_id": sessionID, "generation": generation, "error": commitErr.Error()})
		return
	}
	if res.kind == steeredCommitTerminal {
		if _, pubErr := al.publishCommittedFinal(ctx, rec, res); pubErr != nil {
			logCommittedFinalPublishFailure("steer: terminal report", sessionID, generation, res, pubErr)
		}
	}
}

// landSteeredStopReport is the never-ran stop path's durable half: one
// Mutate that lands LifecycleStopped with the lasting note, spending a
// current-generation fence. It publishes no legacy upward event.
//
// Once the stopped state is DURABLE, the landing appends the D6 historical
// landed-stop projection to the control ledger for the exact control that
// ordered the stop (recorded at acceptance in StopEffect) and — only after
// both are durable — publishes the D6 direct-parent notices from that landed
// history (stopped_notice.go::deliverLandedStopNotices), the same publisher
// the cancelled turn's completion uses. A history-append failure leaves the
// notice unwritten for now (the durable stop note plus the ledger intent are
// what boot reconciliation retries from); a notice append failure leaves the
// landed history pending and retryable, visibly logged, and never gates the
// landing that already committed.
func (al *AgentLoop) landSteeredStopReport(ctx context.Context, sessionID string, generation int, outcome steer.Outcome) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return
	}
	// The fence this landing carries out must still be current: an explicit
	// same-generation RESUME that cleared it (D2) supersedes the stop, and
	// this landing must not clobber the resumed record. (Full execution-
	// identity targeting is the D2 round-4 R4-MAJ-001 seam — a later unit.)
	fencedAtEntry := rec.Stop != nil && rec.Stop.Generation == generation
	var landed *session.LandedStop
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
		if cur.State == session.LifecycleStopped {
			return errTerminalReportAlreadyStopped
		}
		if fencedAtEntry && (cur.Stop == nil || cur.Stop.Generation != cur.Generation) {
			return errTerminalReportFenceSuperseded
		}
		if err := landSteeredStopLocked(cur, outcome); err != nil {
			return err
		}
		// Capture from the record AS PERSISTED: the note carries the
		// original stop instant, the effect names the control. A landing
		// without effect metadata (a fence-less stop disposition that
		// synthesized its note) has no control-ledger acceptance behind it
		// and writes no history.
		landed = landedStopFromRecord(cur)
		return nil
	})
	switch {
	case mutateErr == nil:
		// The stop landed. MAJ-003: this write ends the TURN, never the
		// child's session-owned goal — no goal step belongs in a stop path.
		if landed != nil {
			al.recordLandedStopLedger(sessionID, *landed)
		}
		// D6: only after the landing AND its ledger history are durable may
		// the parent learn — the notice is composed from the landed history,
		// never from the record's note. A publication failure here is visible
		// and stays pending for the boot replay; it never un-lands the stop.
		if fresh, loadErr := lifecycle.Load(sessionID); loadErr != nil || fresh == nil {
			logger.ErrorCF("agent", "steer: stop landing: landed-stop notice not published (record reload failed; boot replay retries)",
				map[string]any{"session_id": sessionID, "generation": generation, "error": errString(loadErr)})
		} else if _, pubErr := al.deliverLandedStopNotices(ctx, fresh); pubErr != nil {
			logger.ErrorCF("agent", "steer: stop landing: direct-parent notice not delivered — the landed history stays pending for boot retry",
				map[string]any{"session_id": sessionID, "generation": generation, "error": pubErr.Error()})
		}
	case errors.Is(mutateErr, errTerminalReportAlreadyStopped),
		errors.Is(mutateErr, errTerminalReportFenceSuperseded),
		errors.Is(mutateErr, errTerminalReportStaleGeneration),
		errors.Is(mutateErr, errTerminalReportAlreadyTerminal),
		errors.Is(mutateErr, session.ErrLifecycleTerminalImmutable):
		// A supersede or a race landing first: legitimate, not a failure.
		logger.InfoCF("agent", "steer: terminal report: stop landing refused (already stopped, superseded, or raced)",
			map[string]any{"session_id": sessionID, "generation": generation, "reason": mutateErr.Error()})
	default:
		logger.WarnCF("agent", "steer: terminal report: persist stopped state failed",
			map[string]any{"session_id": sessionID, "generation": generation, "error": mutateErr.Error()})
	}
}

// landedStopFromRecord builds the D6 landed-stop history payload from a
// record that just landed stopped: the retained note's who/why/when (the
// ORIGINAL stop instant — never a time.Now reconstruction), the acceptance's
// control id and target from StopEffect, and the original direct parent.
// nil when the landing carries no control-ledger acceptance (a synthesized
// fence-less note) — that stop has no accepted control to attach history to.
func landedStopFromRecord(cur *session.LifecycleRecord) *session.LandedStop {
	if cur == nil || cur.StopNote == nil || cur.StopEffect == nil {
		return nil
	}
	return &session.LandedStop{
		Seq:             int64(cur.StopNote.Seq),
		ControlID:       cur.StopEffect.ControlID,
		ParentSessionID: cur.SteeringSessionID(),
		Generation:      cur.Generation,
		Cause:           cur.StopNote.Cause,
		Actor:           cur.StopNote.By,
		At:              cur.StopNote.At,
	}
}

// recordLandedStopLedger appends the landed-stop history line after a
// durable landing. A failure stays visible: the stop itself is already
// durable, and the stop note plus the ledger intent are exactly what boot
// reconciliation retries from — the append is idempotent by seq, so the
// retry verifies the tuple instead of duplicating history.
func (al *AgentLoop) recordLandedStopLedger(sessionID string, landed session.LandedStop) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	if err := lifecycle.RecordLandedStop(sessionID, landed); err != nil {
		logger.WarnCF("agent", "steer: stop landing: landed-stop history not recorded (recoverable from the durable stop note and ledger intent)",
			map[string]any{
				"session_id": sessionID,
				"seq":        landed.Seq,
				"control_id": landed.ControlID,
				"generation": landed.Generation,
				"error":      err.Error(),
			})
	}
}

// errTerminalReport* are the stop-landing's Mutate-refusal sentinels — the
// counterparts of steer_completion.go's errComplete* family.
var (
	errTerminalReportVanished        = errors.New("steer: terminal report: record vanished during landing")
	errTerminalReportStaleGeneration = errors.New("steer: terminal report: generation changed during landing")
	errTerminalReportAlreadyTerminal = errors.New("steer: terminal report: record became terminal during landing")
	errTerminalReportAlreadyStopped  = errors.New("steer: terminal report: record already landed stopped")
	errTerminalReportFenceSuperseded = errors.New("steer: terminal report: the stop fence was cleared (superseded by an explicit resume)")
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
		session.LifecycleStopped, steer.OutcomeInterrupted, "interrupted: the session was cancelled")
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
	var cancelTurn GenerationCancelFunc
	if c != nil {
		cancelTurn = c.cancelTurn
	}
	return c.cascade(ctx, sessionID, by, true, cancelTurn)
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
	return c.cascade(ctx, sessionID, by, true, nil)
}

// StopTurns stamps the requested session (and, for stop-all, its durable
// subtree) and calls the supplied non-terminal Stop adapter with each stamped
// generation. It shares the existing cascade lock and late-child pass, but
// never invokes the administrative cancellation/goal-ending adapter.
func (c *SteerCanceller) StopTurns(ctx context.Context, sessionID string, by steer.Principal, subtree bool, stopTurn GenerationCancelFunc) (steer.CancelReport, error) {
	return c.cascade(ctx, sessionID, by, subtree, stopTurn)
}

func (c *SteerCanceller) cascade(
	ctx context.Context, sessionID string, by steer.Principal, subtree bool, cancelTurn GenerationCancelFunc,
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
	// process stamps each id with ONE cause (D2/D6's closed vocabulary):
	// StopCauseStop for the cascade's own direct target (sessionID — the
	// session named in the CancelSubtree/StopSubtree call), StopCauseCascade
	// for every OTHER id, which is reachable only because it is sessionID's
	// descendant. Callers below split the combined root+first-pass-
	// descendants slice the pre-stop_note code processed as one list into
	// two calls purely to attach the right cause per id; seen/ordering are
	// otherwise unchanged from before this split.
	process := func(ids []string, cause session.StopCause) {
		for _, id := range ids {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			generation, outcome, err := c.stampStop(id, at, by, cause)
			switch {
			case errors.Is(err, errCascadeTerminal):
				report.SkippedTerminal = append(report.SkippedTerminal, id)
			case err != nil:
				report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: id, Reason: err.Error()})
			case outcome == stopStamped || outcome == stopAlreadyStamped:
				stamped[id] = generation
				report.Reached = append(report.Reached, id)
			case outcome == stopAlreadyLanded:
				// D2 stop table / MIN-007: the child is already landed
				// stopped — "already stopped", nothing written, nothing to
				// fire. Not Reached (no live effect belongs to this repeat),
				// not Unreachable (nothing failed), not SkippedTerminal
				// (stopped is not terminal — D2 keeps it resumable).
			}
		}
	}

	fireLiveCancels := func() {
		c.cancelStamped(ctx, stamped, &report, cancelTurn)
	}

	var first []string
	if subtree {
		var walkErr error
		first, walkErr = CollectDescendantSessionIDs(c.Lifecycle, sessionID)
		if walkErr != nil {
			report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: walkErr.Error()})
		}
	}
	process([]string{sessionID}, session.StopCauseStop)
	process(first, session.StopCauseCascade)
	fireLiveCancels()
	if !subtree {
		return report, nil
	}

	second, secondErr := CollectDescendantSessionIDs(c.Lifecycle, sessionID)
	if secondErr != nil {
		report.Unreachable = appendUniqueUnreachable(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: secondErr.Error()})
	}
	lateStart := len(stamped)
	process(second, session.StopCauseCascade)
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

// Revive resumes a stopped session on the SAME generation (ADR-20260928 D2
// CRIT-001, round-4 R4-MAJ-001: an explicit RESUME atomically clears the
// stop_note, any current-generation marker and the stop-effect metadata, and
// changes the same-generation state to queued) — or mints G+1 for a terminal
// follow-up (the one remaining reason a generation moves on revival;
// ADR-093 D4 keeps done/failed children resumable). LifecycleStore.Mutate
// holds the record's write lock across the read, decision, and append, so a
// concurrent Stop is applied in arrival order.
//
// The old generation-bumping revive is superseded: only done/failed mints a
// next generation. The generation-bump-on-live-fence behaviour it had lives
// on only in the superseded oracles
// TestRevive_NewGeneration_OldMarkerInert / TestStopRevive_OrderUnderLock,
// which qa-lead owns migrating.
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
		if rec.Terminal() {
			// done/failed continuation still mints G+1 (unchanged).
			rec.Generation++
			rec.ResumedFrom = rec.SessionID
			rec.State = session.LifecycleRunning
			rec.FailedReason = ""
			rec.NeedsInput = nil
			// The prior generation's committed final outbox is never carried
			// forward: explicit RESUME of a committed done/failed G creating
			// G+1 "neither copies G's outbox into G+1 nor hides, supersedes,
			// acknowledges or retires it" (D2). persistLocked would reject the
			// carried tuple anyway — its generation no longer matches.
			rec.FinalDelivery = nil
			// The prior generation's stop targeting metadata never rides
			// into the minted one (same rule as the outbox tuple above).
			rec.StopEffect = nil
			generation = rec.Generation
			revived = true
			return nil
		}
		// rec.Stopped() covers BOTH the live current-generation fence and a
		// record that has already LANDED session.LifecycleStopped with its
		// fence cleared — landed state OR the current fence, the two
		// durably-stopped shapes an explicit RESUME releases on the SAME
		// generation. A live fence means the stop is still in flight; the
		// newer RESUME supersedes it (D5) and the cancelled turn's later
		// completion lands on a cleared record.
		if !rec.Stopped() {
			return nil
		}
		// D2 CRIT-001 clears the note and the stop-effect metadata — for a
		// LANDED stopped record those two fields are the last reconstructable
		// landed-history tuple (original at/seq/control_id). If that history
		// is not yet on the control ledger — the landing half's append failed
		// or raced — it is persisted HERE, in this same mutation and lock
		// hold, before the clear; and the resume is refused VISIBLY if the
		// ledger refuses the append. Destroying the tuple without recording
		// its history is a loss, never a success. The refusal is retryable:
		// re-issuing the resume retries the idempotent append.
		//
		// The landed-state guard matters: an IN-FLIGHT fence shape (running
		// or queued record carrying a current fence) has not landed — its
		// stop has no history, and writing one would fabricate a landed stop
		// from a bare intent.
		if rec.State == session.LifecycleStopped {
			if landed := landedStopFromRecord(rec); landed != nil {
				if err := c.Lifecycle.RecordLandedStopLocked(sessionID, *landed); err != nil {
					return fmt.Errorf("steer: resume: landed-stop history for %q seq %d (control %q) is not yet recorded and could not be written: %w — the resume is refused so the durable stop note and stop effect keep the tuple recoverable",
						sessionID, landed.Seq, landed.ControlID, err)
				}
			}
		}
		rec.State = session.LifecycleQueued
		rec.Stop = nil
		rec.StopNote = nil
		// D2 CRIT-001: the RESUME clears the stop-effect metadata atomically
		// with the note and the fence — the superseded stop's targeting must
		// not bind the replacement this resume admits.
		rec.StopEffect = nil
		rec.NeedsInput = nil
		rec.FailedReason = ""
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
	// stopAlreadyLanded is the explicit D2 stop-table / MIN-007 idempotent
	// shape: the record has already LANDED LifecycleStopped (fence spent,
	// note retained). The repeat stop answers "already stopped": no ledger
	// line, no sequence, the retained note untouched, no new fence, and —
	// unlike stopAlreadyStamped — no live effect either (the stop it would
	// carry out already happened).
	stopAlreadyLanded
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

// stampStop is the stop path's acceptance: D4's ledger-first control
// acceptance (lifecycle_control_ledger_writer.go::AcceptStopControl)
// followed, in the SAME lock hold, by the D2 fence+note stamp. The control
// ledger line is durable BEFORE the fence exists, so a crash between the
// two leaves a queued intent for boot reconciliation — never a stop
// pretending it was accepted after the fact. StopNote.Seq is the REAL
// per-child control-ledger sequence (no longer the generation stand-in),
// and the note's identity rides the record's StopEffect so the landing half
// can write the landed-stop history for the exact control that ordered it.
//
// The D2/MIN-007 idempotent shapes answer without touching the ledger: a
// live current-generation fence is stopAlreadyStamped (the pending effect
// still fires, as before), an already-LANDED stopped record is
// stopAlreadyLanded ("already stopped" — nothing written, nothing fired),
// and a terminal record is errCascadeTerminal as before.
func (c *SteerCanceller) stampStop(sessionID string, at time.Time, by steer.Principal, cause session.StopCause) (int, stopStampOutcome, error) {
	outcome, _, generation, err := c.Lifecycle.AcceptStopControl(sessionID, session.StopControlIntent{
		Cause:      cause,
		Actor:      session.StopActorFromPrincipal(by),
		AcceptedAt: at,
	}, func(rec *session.LifecycleRecord, grant session.ControlGrant, target session.StopEffectTarget) error {
		rec.Stop = &session.Stop{At: at, Generation: rec.Generation, By: by}
		// D2: the stop action stamps the in-flight fence, the lasting
		// stop_note (its Seq is the control-ledger sequence, NOT the
		// generation) and the stop_effect metadata in the SAME mutation.
		// Landing (steer_completion.go::deliverSteeredCompletion, this
		// file's reportSteeredSessionTerminalUpward) later clears rec.Stop
		// but retains the note and the effect untouched.
		rec.StopNote = &session.StopNote{
			At: at, By: session.StopActorFromPrincipal(by),
			Seq: uint64(grant.Seq), Cause: cause,
		}
		rec.StopEffect = &session.StopEffect{ControlID: grant.ControlID, Target: target}
		return nil
	})
	switch outcome {
	case session.StopAcceptMissing:
		return 0, 0, session.ErrLifecycleNotFound
	case session.StopAcceptTerminal:
		return generation, 0, errCascadeTerminal
	case session.StopAcceptAlreadyStamped:
		return generation, stopAlreadyStamped, nil
	case session.StopAcceptAlreadyLanded:
		return generation, stopAlreadyLanded, nil
	case session.StopAcceptGranted:
		if err != nil {
			// The acceptance intent is durable but the fence is not (D4's
			// queued-intent crash state, returned visibly). The cascade
			// reports the node unreachable; boot reconciliation finishes
			// the intent.
			return generation, 0, err
		}
		return generation, stopStamped, nil
	default:
		return generation, 0, err
	}
}

func (c *SteerCanceller) cancelStamped(ctx context.Context, stamped map[string]int, report *steer.CancelReport, cancelTurn GenerationCancelFunc) {
	if cancelTurn == nil {
		return
	}
	for _, id := range report.Reached {
		generation, ok := stamped[id]
		if !ok {
			continue
		}
		delete(stamped, id)
		result, err := cancelTurn(ctx, id, generation)
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
