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
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// SteerCanceller implements ADR-091's durable Stop cascade and revival.
type SteerCanceller struct {
	Lifecycle           *session.LifecycleStore
	cancelTurn          GenerationCancelFunc
	revivalStateWriter  RevivalStateWriter
	retainStopSelection func(session.StopSelection) error
	locks               sync.Map
}

var _ steer.Canceller = (*SteerCanceller)(nil)

// GenerationCancelResult is the live-turn registry's answer to a cancel
// carrying the generation stamped on the durable record.
type GenerationCancelResult struct {
	Found                  bool
	Cancelled              bool
	SkippedNewerGeneration bool
	// Superseded refines SkippedNewerGeneration: the carried selection no
	// longer matches the durable fence (it landed, or a Resume/completion
	// replaced it first), so the effect did nothing (D2/D5). Callers that
	// only need "may something still be running" keep reading
	// SkippedNewerGeneration.
	Superseded bool
}

// GenerationCancelFunc carries the cascade's acceptance-time StopSelection
// in ctx. Adapters must retain that pair through retries/timers and compare the
// selected execution at their effect boundaries, not reselect by generation.
type GenerationCancelFunc func(ctx context.Context, sessionID string, generation int) (GenerationCancelResult, error)

// RevivalStateWriter persists the parent's subagent_state(running) lifecycle
// event after Revive has durably published the new generation. WP-B supplies
// the transcript/frame implementation at the composition root.
type RevivalStateWriter func(ctx context.Context, sessionID string, generation int) error

// SteerGenerationCancel applies only the accepted execution/control selection
// carried in ctx. Missing targeting is a visible error, never a current-fence
// lookup that could select a same-generation replacement.
func (al *AgentLoop) SteerGenerationCancel(ctx context.Context, sessionID string, generation int) (GenerationCancelResult, error) {
	selected, current, err := al.stopSelectionForCallback(ctx, sessionID, generation)
	if err != nil {
		return GenerationCancelResult{}, err
	}
	if !current {
		al.removeQueuedStopEffects(sessionID, []session.StopEffect{selected.Effect})
		return GenerationCancelResult{SkippedNewerGeneration: true, Superseded: true}, nil
	}
	ctx = withStopSelection(ctx, selected)
	d, current, retainErr := al.retainSelectedStop(ctx, sessionID, generation, nil)
	if retainErr != nil {
		return GenerationCancelResult{}, retainErr
	}
	if !current {
		al.removeQueuedStopEffects(sessionID, []session.StopEffect{selected.Effect})
		return GenerationCancelResult{SkippedNewerGeneration: true, Superseded: true}, nil
	}
	if d == nil {
		return GenerationCancelResult{}, nil
	}
	ts, _ := al.activeTurnForCancel(sessionID, CancelScope{SessionID: sessionID, TurnOnly: true}).(*turnState)
	if ts != nil {
		if !liveTurnMatchesStop(ts, selected) {
			return GenerationCancelResult{Found: true, SkippedNewerGeneration: true}, nil
		}
		ts.claimCancel(true)
		ts.requestHardAbort()
		return GenerationCancelResult{Found: true, Cancelled: true}, nil
	}
	// runTurn may have cleared its handle while the selected owner's actual
	// disposal/output tail is still pending. That owner, not this callback,
	// performs the landing after retiring its own execution resources.
	return GenerationCancelResult{Found: true, Cancelled: true}, nil
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
// Refuses (nil, never fatal to the caller) when the record has moved
// past generation, is already terminal, or is already stopped — a Stop, a
// Revive or another completion racing this write is a legitimate outcome —
// and when the record itself is gone (ErrLifecycleNotFound). The
// error-bearing exits, each returned so the stop cascade surfaces it in
// report.Unreachable (the visible channel the stop caller reads): the
// STOPPED arm's landed-stop history append, a lifecycle read failure other
// than not-found, and the terminal-failure arm's outcome/outbox commit — a
// failed commit leaves no durable outbox entry to retry from, so nil here
// would strand the terminal failure with no writer and no receipt.
func (al *AgentLoop) reportSteeredSessionTerminalUpward(
	ctx context.Context, sessionID string, generation int,
	nextState session.LifecycleState, outcome steer.Outcome, failureReason string,
) error {
	if al == nil {
		return nil
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return nil // record gone: raced away, not ours to act (refusal arm)
		}
		return fmt.Errorf("steer: terminal report: load session %q: %w", sessionID, err)
	}
	if rec.Generation != generation || rec.Terminal() {
		return nil
	}
	if nextState == session.LifecycleStopped {
		return al.landSteeredStopReport(ctx, sessionID, generation, outcome)
	}
	// Synthetic terminal failure carries the selected admission's stamp.
	// It must never borrow a replacement's live handle.
	res, commitErr := al.commitSteeredCompletion(lifecycle, rec, nextState, outcome, "", failureReason, al.executionClaimFor(rec))
	if commitErr != nil {
		logger.WarnCF("agent", "steer: terminal report: outcome/outbox commit failed",
			map[string]any{"session_id": sessionID, "generation": generation, "error": commitErr.Error()})
		// A failed commit leaves NO durable outbox entry to retry from, so
		// this must surface: the stop cascade reports it via
		// report.Unreachable instead of a success with no receipt.
		return fmt.Errorf("steer: terminal report: outcome/outbox commit for session %q generation %d: %w",
			sessionID, generation, commitErr)
	}
	if res.kind == steeredCommitTerminal {
		if _, pubErr := al.publishCommittedFinal(ctx, rec, res); pubErr != nil {
			logCommittedFinalPublishFailure("steer: terminal report", sessionID, generation, res, pubErr)
		}
	}
	return nil
}

// landSteeredStopReport is the never-ran stop path's durable half: one
// Mutate that lands LifecycleStopped with the lasting note, spending a
// current-generation fence. It publishes no legacy upward event.
//
// Once the stopped state is DURABLE, the landing appends the D6 historical
// landed-stop projection to the control ledger for the exact control that
// ordered the stop (recorded at acceptance in StopEffect). The landed
// history — not the queued intent, not the final applied receipt — is what
// the W1 direct-parent notice publisher discovers. The final "applied"
// receipt cannot precede that notice's durability, so this landing never
// writes one. A history-append failure is visible and recoverable: the
// durable stop note plus the ledger intent are exactly what boot
// reconciliation retries from — and it is RETURNED to the caller, so the
// stop call surfaces it (report.Unreachable) instead of reporting success
// with zero history. Only then — landing and history both durable — does it
// publish the D6 direct-parent notices from that landed history
// (stopped_notice.go::deliverLandedStopNotices), the same publisher the
// cancelled turn's completion uses; a notice append failure leaves the
// landed history pending and retryable, visibly logged, and never gates the
// landing that already committed.
func (al *AgentLoop) landSteeredStopReport(ctx context.Context, sessionID string, generation int, outcome steer.Outcome) error {
	return al.landSteeredStopReportAtCut(ctx, sessionID, generation, outcome, nil)
}

func (al *AgentLoop) landSteeredStopReportAtCut(ctx context.Context, sessionID string, generation int, outcome steer.Outcome, afterLanding func()) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		if errors.Is(err, session.ErrLifecycleNotFound) {
			return nil // record gone: raced away, not ours to act
		}
		return fmt.Errorf("steer: landed stop report: load session %q: %w", sessionID, err)
	}
	// The fence this landing carries out must still be current: an explicit
	// same-generation RESUME that cleared it (D2) supersedes the stop, and
	// this landing must not clobber the resumed record. The carried execution
	// and control identity are checked again inside the owning mutation.
	fencedAtEntry := rec.Stop != nil && rec.Stop.Generation == generation
	selected, carried := stopSelectionFromContext(ctx)
	if fencedAtEntry && !carried {
		return fmt.Errorf("steer: stop landing %q: accepted execution/control selection is missing", sessionID)
	}
	claim := al.executionClaimFor(rec)
	var landed *session.LandedStop
	var landedRecord *session.LifecycleRecord
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
		// Finalization owns only the original accepted pair, checked under
		// this mutation's lock even after a same-generation resume/new Stop.
		if carried && !stopSelectionMatchesRecord(selected, cur) {
			return errTerminalReportFenceSuperseded
		}
		if !carried && ((cur.ExecutionID != nil || claim.RunID != "") && !claim.matches(cur)) {
			return errTerminalReportFenceSuperseded
		}
		if fencedAtEntry && (cur.Stop == nil || cur.Stop.Generation != cur.Generation) {
			return errTerminalReportFenceSuperseded
		}
		if err := landSteeredStopLocked(lifecycle, cur, outcome); err != nil {
			return err
		}
		// Capture from the record AS PERSISTED: the note carries the
		// original stop instant, the effect names the control. A landing
		// without effect metadata (a synthesized fence-less note) recorded
		// its own ledger history inside landSteeredStopLocked — Correction
		// C3 — so there is nothing left for the post-mutation write below.
		landed = landedStopFromRecord(cur)
		landedRecord = cur
		return nil
	})
	if afterLanding != nil {
		afterLanding()
	}
	switch {
	case mutateErr == nil:
		if stateErr := al.publishCurrentStoppedState(landedRecord); stateErr != nil {
			return stateErr
		}
		// The stop landed. MAJ-003: this write ends the TURN, never the
		// child's session-owned goal — no goal step belongs in a stop path.
		// A failed history append keeps the landed state, note and effect
		// exactly as written (the retry anchor) and returns the failure so
		// the stop call's caller hears it instead of success-with-no-history.
		if landed != nil {
			if histErr := al.recordLandedStopLedger(sessionID, *landed); histErr != nil {
				// Full cause (including the ledger's absolute path) stays in
				// the server-side log only; the returned sentinel is the
				// path-free public reason the cascade publishes.
				logger.WarnCF("agent", "steer: stop landing: landed-stop history not recorded (recoverable from the durable stop note and ledger intent)",
					map[string]any{
						"session_id": sessionID,
						"seq":        landed.Seq,
						"control_id": landed.ControlID,
						"generation": generation,
						"error":      histErr.Error(),
					})
				return fmt.Errorf("%w (control %s, seq %d)", errLandedStopHistoryNotRecorded, landed.ControlID, landed.Seq)
			}
		}
		// D6: only after the landing AND its ledger history are durable may
		// the parent learn — the notice is composed from the landed history,
		// never from the record's note. A publication failure here is visible
		// and stays pending for the boot replay; it never un-lands the stop.
		if fresh, loadErr := lifecycle.Load(sessionID); loadErr != nil || fresh == nil {
			logger.ErrorCF("agent", "steer: stop landing: landed-stop notice not published (record reload failed; boot replay retries)",
				map[string]any{"session_id": sessionID, "generation": generation, "error": errString(loadErr)})
			return fmt.Errorf("Stop: landed history remains pending because the saved session could not be read")
		} else if _, pubErr := al.deliverLandedStopNotices(ctx, fresh); pubErr != nil {
			logger.ErrorCF("agent", "steer: stop landing: direct-parent notice not delivered — the landed history stays pending for boot retry",
				map[string]any{"session_id": sessionID, "generation": generation, "error": pubErr.Error()})
			return fmt.Errorf("Stop: landed history remains pending because its direct-parent notice could not be published")
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
		return fmt.Errorf("Stop: selected stopped state could not be saved; retry after storage is repaired")
	}
	return nil
}

// landedStopFromRecord builds the D6 landed-stop history payload from a
// record that just landed stopped: the retained note's who/why/when (the
// ORIGINAL stop instant — never a time.Now reconstruction), the acceptance's
// control id and target from StopEffect, and the original direct parent.
// nil when the landing carries no control-ledger acceptance (a synthesized
// fence-less note) — that stop has no accepted control to attach history
// to, and since Correction C3 it wrote its own ledger line inside the
// landing instead (landSteeredStopLocked).
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
// durable landing. The returned error is the CALLER's to surface: the stop
// itself is already durable, and the stop note plus the ledger intent are
// exactly what boot reconciliation retries from — the append is idempotent
// by seq, so the retry verifies the tuple instead of duplicating history.
// A failure confined to a log line while the stop call reports success is
// the silent loss the W2a history-failure witnesses forbid (D2 round-3
// MAJ-001: a persistence failure is surfaced, never claimed as delivery).
func (al *AgentLoop) recordLandedStopLedger(sessionID string, landed session.LandedStop) error {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return nil
	}
	return lifecycle.RecordLandedStop(sessionID, landed)
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

// errLandedStopHistoryNotRecorded is the typed classification of a failed
// landed-stop history append. Its text is the PUBLIC reason: it says the
// stop itself LANDED (the failure is the history projection, never the
// stop), names the repair, and deliberately carries NO underlying detail —
// the raw append error (which contains the control-ledger's absolute path)
// stays in the server-side WARN log, never in a CancelReport.Unreachable
// reason that the gateway publishes to clients. Consumers classify with
// errors.Is; the durable note+effect tuple is the retry anchor either way.
var errLandedStopHistoryNotRecorded = errors.New("steer: stop landing: the stop landed, but its landed-stop history could not be recorded; retry after storage is repaired")

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
	return c.cascade(ctx, sessionID, by, true, cancelTurn, nil)
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
	return c.cascade(ctx, sessionID, by, true, nil, nil)
}

// StopTurns stamps the requested session (and, for stop-all, its durable
// subtree) and calls the supplied non-terminal Stop adapter with each stamped
// generation. It shares the existing cascade lock and late-child pass, but
// never invokes the administrative cancellation/goal-ending adapter.
func (c *SteerCanceller) StopTurns(ctx context.Context, sessionID string, by steer.Principal, subtree bool, stopTurn GenerationCancelFunc) (steer.CancelReport, error) {
	return c.cascade(ctx, sessionID, by, subtree, stopTurn, nil)
}

// ReapplySelectedStops is the delegate tool's grace backstop: it carries the
// effect of Stops an EARLIER cooperative acceptance already selected, under
// the same cascade lock, and accepts nothing new. A selection whose fence is no
// longer current (landed, resumed, replaced by a newer Stop) is dropped, never
// re-targeted at the session's current execution.
func (c *SteerCanceller) ReapplySelectedStops(
	ctx context.Context, sessionID string, selections []session.StopSelection, cancelTurn GenerationCancelFunc,
) (steer.CancelReport, error) {
	if len(selections) == 0 {
		return steer.CancelReport{}, nil
	}
	return c.cascade(ctx, sessionID, steer.Principal{}, false, cancelTurn, selections)
}

// currentReappliedSelections keeps, under the cascade lock, only the carried
// selections whose durable fence is still current. A superseded selection is
// dropped; it is never re-targeted at the session's current execution.
func (c *SteerCanceller) currentReappliedSelections(reapply []session.StopSelection, report *steer.CancelReport) map[string]session.StopSelection {
	current := make(map[string]session.StopSelection)
	for _, selected := range reapply {
		if _, dup := current[selected.SessionID]; dup {
			continue
		}
		rec, err := c.Lifecycle.Load(selected.SessionID)
		if err != nil {
			if !errors.Is(err, session.ErrLifecycleNotFound) {
				report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: selected.SessionID, Reason: err.Error()})
			}
			continue
		}
		if !stopSelectionMatchesRecord(selected, rec) {
			continue
		}
		current[selected.SessionID] = selected
		report.Reached = append(report.Reached, selected.SessionID)
	}
	return current
}

// cascade runs one Stop cascade under sessionID's cascade lock. A non-nil
// reapply carries already-accepted selections: nothing is stamped or
// enumerated, and only a selection still matching its durable fence is fired.
func (c *SteerCanceller) cascade(
	ctx context.Context, sessionID string, by steer.Principal, subtree bool, cancelTurn GenerationCancelFunc,
	reapply []session.StopSelection,
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
	held := true
	unlock := func() {
		if held {
			lock.Unlock()
			held = false
		}
	}
	defer unlock()

	stamped := make(map[string]session.StopSelection)
	if reapply != nil {
		stamped = c.currentReappliedSelections(reapply, &report)
		unlock()
		c.cancelStamped(ctx, stamped, &report, cancelTurn)
		return report, nil
	}
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
			accepted, outcome, err := c.stampStop(id, at, by, cause)
			switch {
			case errors.Is(err, errCascadeTerminal):
				report.SkippedTerminal = append(report.SkippedTerminal, id)
			case err != nil:
				report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: id, Reason: err.Error()})
			case outcome == stopStamped || outcome == stopAlreadyStamped:
				if accepted.Selection == nil {
					report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: id, Reason: "stamped Stop has no accepted execution/control selection"})
					continue
				}
				stamped[id] = *accepted.Selection
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
	unlock()
	fireLiveCancels()
	if !subtree {
		return report, nil
	}
	lock.Lock()
	held = true

	second, secondErr := CollectDescendantSessionIDs(c.Lifecycle, sessionID)
	if secondErr != nil {
		report.Unreachable = appendUniqueUnreachable(report.Unreachable, steer.UnreachableSession{ID: sessionID, Reason: secondErr.Error()})
	}
	lateStart := len(stamped)
	process(second, session.StopCauseCascade)
	unlock()
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
	canceller.retainStopSelection = al.retainAcceptedStop
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
	c.retainStopSelection = al.retainAcceptedStop
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
			// The prior execution's identity is not carried either: G+1 has
			// no current execution until its own admission stamps one
			// (execution_identity.go) — a new admission never reuses the old
			// run's identity slot. The prior stop-effect metadata does not
			// ride into the minted generation either.
			rec.ExecutionID = nil
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
		// Budget credit or a timeout reset reads the note before it is
		// cleared, in this same mutation, before dispatch. No lock is held
		// across that dispatch. A missing note does not invent a boundary.
		session.ApplyExplicitResumeBudget(rec, time.Now().UTC())
		rec.State = session.LifecycleQueued
		rec.Stop = nil
		rec.StopNote = nil
		// D2 CRIT-001: the RESUME clears the stop-effect metadata atomically
		// with the note and the fence — the superseded stop's targeting must
		// not bind the replacement this resume admits.
		rec.StopEffect = nil
		rec.NeedsInput = nil
		rec.FailedReason = ""
		// Same-generation resume: the stopped-out run's identity is history.
		// The resuming admission stamps its own before dispatching.
		rec.ExecutionID = nil
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
	if gen != rec.Generation {
		return false, steer.ErrStaleGeneration.Error()
	}
	if rec.Terminal() {
		return false, steer.ErrTerminal.Error()
	}
	if rec.Stopped() {
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
func (c *SteerCanceller) stampStop(sessionID string, at time.Time, by steer.Principal, cause session.StopCause) (session.StopAcceptance, stopStampOutcome, error) {
	accepted, err := c.Lifecycle.AcceptStopControlSelection(sessionID, session.StopControlIntent{
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
	if err != nil {
		// Durable acceptance may exist without a persisted fence; do not fire
		// an effect, and retain the store error for the caller/boot repair.
		return accepted, 0, err
	}
	// Retain the exact accepted pair before an effect adapter can block. This
	// is only short owner-map bookkeeping: no interrupt, model call or join.
	if accepted.Selection != nil && c.retainStopSelection != nil {
		if err := c.retainStopSelection(*accepted.Selection); err != nil {
			return accepted, 0, err
		}
	}
	switch accepted.Outcome {
	case session.StopAcceptMissing:
		return accepted, 0, session.ErrLifecycleNotFound
	case session.StopAcceptTerminal:
		return accepted, 0, errCascadeTerminal
	case session.StopAcceptAlreadyStamped:
		return accepted, stopAlreadyStamped, nil
	case session.StopAcceptAlreadyLanded:
		return accepted, stopAlreadyLanded, nil
	case session.StopAcceptGranted:
		return accepted, stopStamped, nil
	default:
		return accepted, 0, fmt.Errorf("steer: stop %q: unknown acceptance outcome %d", sessionID, accepted.Outcome)
	}
}

func (c *SteerCanceller) cancelStamped(ctx context.Context, stamped map[string]session.StopSelection, report *steer.CancelReport, cancelTurn GenerationCancelFunc) {
	if cancelTurn == nil {
		return
	}
	for _, id := range report.Reached {
		selected, ok := stamped[id]
		if !ok {
			continue
		}
		delete(stamped, id)
		result, err := cancelTurn(withStopSelection(ctx, selected), id, selected.Effect.Target.Generation)
		if err != nil {
			report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: id, Reason: err.Error()})
			continue
		}
		if result.Superseded {
			report.Superseded = append(report.Superseded, id)
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
