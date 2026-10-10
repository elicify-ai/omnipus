// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// stop_session.go is the ONE session Stop (founder decision 2026-10-05,
// coordination/LANE-A-ONE-STOP-DECISION-20261005.md): people (web Stop and
// Stop all, /stop, /cancel, channel commands, REST delete) and agents
// (delegate stop_all, the stop half of redirect) all call StopSession.
//
// The sequence is the same for every caller: the durable fence and note are
// stamped first (SteerCanceller.StopTurns, ADR-20260928 D2/D4), each reached
// running turn is asked to stop politely at once, and RequestCancel forces it
// 3 s later (cancelHardAbortDelay) — pinned to the exact selected execution,
// never a replacement. Only the owning execution lands `stopped`, after its
// running work has shut down. A stop never ends a goal.
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

// StopTurnsCanceller is the durable stamping half StopSession drives. The
// composition root's SteerCanceller satisfies it.
type StopTurnsCanceller interface {
	StopTurnsWithCause(ctx context.Context, sessionID string, by steer.Principal, subtree bool, cause session.StopCause, stopTurn GenerationCancelFunc) (steer.CancelReport, error)
	ContinueAcceptedStop(ctx context.Context, intent session.UnfinishedStopIntent, stopTurn GenerationCancelFunc) (steer.CancelReport, error)
}

// StopRequest names one Stop. By is the authenticated principal (a person or
// the stopping agent's session) recorded on the stop note. Tree selects Stop
// all (the session and every helper under it) instead of this session only.
type StopRequest struct {
	SessionID string
	By        steer.Principal
	Channel   string
	Tree      bool
	// Cause is the requested session's own stop cause; empty means
	// StopCauseStop. A plan Stop sweeping its members passes StopCauseCascade.
	Cause session.StopCause
	// Continue, when set, carries out that already-accepted stop control
	// (same control id and seq) for SessionID instead of accepting a new
	// one. Tree and Cause are ignored: the control fixed them at acceptance.
	Continue *session.UnfinishedStopIntent
	// HooksFor returns the transport hooks for one reached session. Nil uses
	// the default background-shell kill for every reached session.
	HooksFor func(sessionID string) CancelHooks
	// Canceller overrides the loop's own canceller (the gateway passes the
	// composition-root instance). Nil uses al.steerCanceller().
	Canceller StopTurnsCanceller
	// OnInputDiscarded, when set, is called synchronously for each queued
	// person's message this Stop discards (FR-024), BEFORE the turn is asked to
	// stop - so a live client learns of it before the turn's end can report the
	// message as working.
	OnInputDiscarded func(DiscardedInput)
}

// StopResult reports what one Stop reached.
type StopResult struct {
	// Report is the durable cascade's report; empty when the session has no
	// lifecycle record (Durable false).
	Report  steer.CancelReport
	Durable bool
	// Root is the requested session's own turn outcome; RootErr its error.
	Root    CancelOutcome
	RootErr error
	// Fired: a running turn was asked to stop or a queued/idle stop landed.
	// Armed: the Stop was latched for a turn that has not registered yet.
	Fired, Armed bool
	// BackgroundKilled/BackgroundFailed total the background-shell kills.
	BackgroundKilled, BackgroundFailed int
	// Selected holds each reached session's accepted execution/control pair.
	Selected map[string]session.StopSelection
	// Discarded lists the person's queued web/channel messages this Stop
	// discarded before delivery (FR-024); DiscardErr is a failure to label any
	// of them. A discarded message is never consumed either way.
	Discarded  []DiscardedInput
	DiscardErr error
}

// StopSession runs the one Stop. Callers authenticate By before calling (the
// command surfaces refuse an unauthenticated sender). The returned error is
// reserved for a Stop that could not start (no session, an unreadable target,
// an unresolvable tree); per-session failures are in Report.Unreachable and
// RootErr, for the caller to surface visibly.
func (al *AgentLoop) StopSession(ctx context.Context, req StopRequest) (StopResult, error) {
	var res StopResult
	if al == nil || strings.TrimSpace(req.SessionID) == "" {
		return res, fmt.Errorf("Stop requires a session")
	}
	// N6: once this Stop has ended the session's episode, release any retained
	// external-CLI driver (its option/env snapshot). No-op while the run is still
	// winding down (releaseExternalRunIfIdle checks the running flag; the steered
	// exit path's own release covers that case) and while any revival still holds
	// the driver: a Stop retires only the holds of the admissions it actually
	// selected, at their settlement (removeQueuedStopEffects, NEW-7) — never a
	// session-wide clear, which a failed or delayed Stop would aim at someone
	// else's accepted revival.
	defer al.releaseExternalRunIfIdle(req.SessionID)
	hooksFor := req.HooksFor
	if hooksFor == nil {
		// Founder decision Q13: a plain Stop ends the session's current turn
		// and leaves its background shell processes running; only Stop all
		// (cancel, the whole tree) kills them. A caller passing its own hooks
		// (the scheduled-run deadline) decides for itself.
		hooksFor = func(string) CancelHooks {
			if !req.Tree {
				return CancelHooks{}
			}
			return CancelHooks{KillBackgroundSessions: killBackgroundSessionsForCancelSurface}
		}
	}
	canceller := CancelCanceller{UserID: req.By.ID, Channel: req.Channel}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		// No lifecycle store: no durable helper edges exist, so the session's
		// own turn is the whole tree.
		res.Discarded, res.DiscardErr = al.discardUndeliveredHumanInput(req.SessionID)
		notifyDiscarded(req.OnInputDiscarded, res.Discarded)
		res.Root, res.RootErr = al.RequestCancel(ctx, CancelScope{SessionID: req.SessionID, TurnOnly: true}, canceller, hooksFor(req.SessionID))
		return al.finishPlainStop(res), nil
	}
	var mu sync.Mutex
	res.Selected = make(map[string]session.StopSelection)
	swept := make(map[string]bool) // sessions whose stop already ran the shell hook
	stopTurn := func(effectCtx context.Context, id string, generation int) (GenerationCancelResult, error) {
		// D5: this Stop supersedes the session's older pending steers.
		if selected, carried := stopSelectionFromContext(effectCtx); carried {
			if err := al.supersedePendingSteers(id, selected.Effect.ControlID); err != nil {
				return GenerationCancelResult{}, err
			}
		}
		// FR-024: input admitted but not yet committed into the model input is
		// discarded by this Stop, and labelled.
		discarded, discardErr := al.discardUndeliveredHumanInput(id)
		notifyDiscarded(req.OnInputDiscarded, discarded)
		outcome, err := al.RequestCancel(effectCtx,
			CancelScope{SessionID: id, TurnOnly: true, Generation: generation}, canceller, hooksFor(id))
		mu.Lock()
		res.Discarded = append(res.Discarded, discarded...)
		res.DiscardErr = errors.Join(res.DiscardErr, discardErr)
		swept[id] = true
		if selected, carried := stopSelectionFromContext(effectCtx); carried {
			res.Selected[id] = selected
		}
		res.BackgroundKilled += outcome.BackgroundSessionsKilled
		res.BackgroundFailed += outcome.BackgroundSessionsFailed
		res.Fired = res.Fired || outcome.Fired
		if id == req.SessionID {
			res.Root, res.RootErr = outcome, err
			res.Armed = outcome.Armed
		}
		mu.Unlock()
		result := GenerationCancelResult{
			Found: outcome.Fired || outcome.Armed, Cancelled: outcome.Fired,
			SkippedNewerGeneration: outcome.SkippedNewerGeneration,
		}
		if err == nil && !result.Found && !result.SkippedNewerGeneration {
			// Stamped but never ran a turn (queued or idle): the selected
			// admission's own settlement lands it (SteerGenerationCancel).
			return al.SteerGenerationCancel(effectCtx, id, generation)
		}
		return result, err
	}
	stopper := req.Canceller
	if stopper == nil {
		stopper = al.steerCanceller()
	}
	if _, err := lifecycle.Load(req.SessionID); err != nil {
		if !errors.Is(err, session.ErrLifecycleNotFound) || req.Continue != nil {
			// An accepted control continues only on its own record.
			return res, fmt.Errorf("read Stop target: %w", err)
		}
		// An ordinary chat may have no lifecycle record of its own while its
		// helpers do (their durable edge names it). Stop its own turn, and
		// for Stop all every direct helper's whole tree through the same
		// cascade; a read failure of the tree is never hidden.
		var children []session.LifecycleRecord
		if req.Tree {
			var listErr error
			children, listErr = lifecycle.List(session.LifecycleFilter{SteeringSessionID: req.SessionID})
			if listErr != nil {
				return res, fmt.Errorf("resolve Stop-all tree: %w", listErr)
			}
		}
		res.Discarded, res.DiscardErr = al.discardUndeliveredHumanInput(req.SessionID)
		notifyDiscarded(req.OnInputDiscarded, res.Discarded)
		res.Root, res.RootErr = al.RequestCancel(ctx, CancelScope{SessionID: req.SessionID, TurnOnly: true}, canceller, hooksFor(req.SessionID))
		res = al.finishPlainStop(res)
		for _, child := range children {
			report, stopErr := stopper.StopTurnsWithCause(ctx, child.SessionID, req.By, true, session.StopCauseCascade, stopTurn)
			if stopErr != nil {
				report.Unreachable = append(report.Unreachable, steer.UnreachableSession{ID: child.SessionID, Reason: stopErr.Error()})
			}
			res.Report.Reached = append(res.Report.Reached, report.Reached...)
			res.Report.Unreachable = append(res.Report.Unreachable, report.Unreachable...)
			res.Report.SkippedNewerGeneration = append(res.Report.SkippedNewerGeneration, report.SkippedNewerGeneration...)
			res.Report.Superseded = append(res.Report.Superseded, report.Superseded...)
			res.Report.SkippedTerminal = append(res.Report.SkippedTerminal, report.SkippedTerminal...)
			res.Durable = true
		}
		if len(res.Report.Reached) != 0 {
			res.Fired = true
		}
		return res, nil
	}

	var report steer.CancelReport
	var err error
	if req.Continue != nil {
		report, err = stopper.ContinueAcceptedStop(ctx, *req.Continue, stopTurn)
	} else {
		cause := req.Cause
		if cause == "" {
			cause = session.StopCauseStop
		}
		report, err = stopper.StopTurnsWithCause(ctx, req.SessionID, req.By, req.Tree, cause, stopTurn)
	}
	if err != nil {
		return res, err
	}
	res.Report, res.Durable = report, true
	if req.Tree && req.Continue == nil {
		// Q13: Stop all kills the background shells of the whole tree, also
		// of sessions that have no turn left to stop (already stopped by an
		// earlier plain Stop, or idle), which the cascade does not reach.
		al.killTreeBackgroundShells(req.SessionID, hooksFor, swept, &res)
	}
	// A queued-only stop still did work: its durable stop landed without an
	// active turn. A repeated stop has no Reached entries and is a no-op.
	if !res.Armed {
		res.Fired = res.Fired || len(report.Reached) != 0
	}
	res.Armed = !res.Fired && res.Armed
	return res, nil
}

func (al *AgentLoop) finishPlainStop(res StopResult) StopResult {
	res.Fired, res.Armed = res.Root.Fired, res.Root.Armed
	res.BackgroundKilled = res.Root.BackgroundSessionsKilled
	res.BackgroundFailed = res.Root.BackgroundSessionsFailed
	return res
}

// StillRunning lists the reached sessions that may still be running past
// this Stop: unreachable ones and ones a newer generation took over. A
// selection superseded by a newer explicit action (D2/D5) is not counted.
func (r StopResult) StillRunning() []string {
	var out []string
	for _, item := range r.Report.Unreachable {
		out = append(out, item.ID)
	}
	for _, id := range r.Report.SkippedNewerGeneration {
		superseded := false
		for _, s := range r.Report.Superseded {
			if s == id {
				superseded = true
				break
			}
		}
		if !superseded {
			out = append(out, id)
		}
	}
	return out
}

// StopDelegatedTree is the delegate tool's stop_all: the one Stop, tree
// scope, recorded as the calling agent. It returns the reached session ids;
// any unreachable or still-running session is a visible error.
func (al *AgentLoop) StopDelegatedTree(sessionID string, by steer.Principal) ([]string, error) {
	res, err := al.StopSession(context.Background(), StopRequest{
		SessionID: sessionID, By: by, Channel: "agent", Tree: true,
		// The delegate tool already killed the subtree's background shells.
		HooksFor: func(string) CancelHooks { return CancelHooks{} },
	})
	if err != nil {
		return nil, fmt.Errorf("steer: stop %q: %w", sessionID, err)
	}
	if res.RootErr != nil {
		return res.Report.Reached, fmt.Errorf("steer: stop %q: %w", sessionID, res.RootErr)
	}
	report := res.Report
	if len(report.Reached) == 0 && len(report.Unreachable) > 0 {
		return nil, fmt.Errorf("steer: stop %q: %s", report.Unreachable[0].ID, report.Unreachable[0].Reason)
	}
	stillRunning := res.StillRunning()
	if len(stillRunning) > 0 {
		var reasons []string
		for _, item := range report.Unreachable {
			reasons = append(reasons, fmt.Sprintf("%s: %s", item.ID, item.Reason))
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "none")
		}
		return report.Reached, fmt.Errorf(
			"steer: stop %q: partial cascade — reached %d; unreachable %d (%s); still running: %s",
			sessionID, len(report.Reached), len(report.Unreachable), strings.Join(reasons, "; "),
			strings.Join(stillRunning, ", "))
	}
	return report.Reached, nil
}

// stopSettleMargin is how long, beyond the 3 s forced stop, a caller that must
// not act on a still-running session (REST delete) waits for the forced
// turns to exit.
const stopSettleMargin = 2 * time.Second

// AwaitStoppedTurns waits until none of ids has a live turn, bounded by the
// forced stop (cancelHardAbortDelay) plus stopSettleMargin. It returns the ids
// still running at that point; nothing is held locked while waiting.
func (al *AgentLoop) AwaitStoppedTurns(ctx context.Context, ids []string) []string {
	deadline := time.Now().Add(cancelHardAbortDelay + stopSettleMargin)
	for {
		var live []string
		for _, id := range ids {
			if al.activeTurnForCancel(id, CancelScope{SessionID: id, TurnOnly: true}) != nil {
				live = append(live, id)
			}
		}
		if len(live) == 0 || !time.Now().Before(deadline) {
			return live
		}
		select {
		case <-ctx.Done():
			return live
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// killTreeBackgroundShells runs the background-shell kill hook for
// sessionID and every durable descendant the Stop's cascade did not
// already handle, adding the totals to res. A descendant walk that fails
// is reported as unreachable, never hidden.
func (al *AgentLoop) killTreeBackgroundShells(sessionID string, hooksFor func(string) CancelHooks, swept map[string]bool, res *StopResult) {
	descendants, err := CollectDescendantSessionIDs(al.GetSessionLifecycleStore(), sessionID)
	if err != nil {
		res.Report.Unreachable = append(res.Report.Unreachable, steer.UnreachableSession{
			ID: sessionID, Reason: "background shells of the helper tree could not be listed: " + err.Error(),
			HelperTreeUnlisted: true,
		})
	}
	ids := make([]string, 0, 1+len(descendants))
	ids = append(ids, sessionID)
	ids = append(ids, descendants...)
	for _, id := range ids {
		if swept[id] {
			continue
		}
		kill := hooksFor(id).KillBackgroundSessions
		if kill == nil {
			continue
		}
		killed, failed := kill(id)
		res.BackgroundKilled += killed
		res.BackgroundFailed += failed
	}
}

// notifyDiscarded hands each discarded input to the Stop caller's hook.
func notifyDiscarded(hook func(DiscardedInput), discarded []DiscardedInput) {
	if hook == nil {
		return
	}
	for _, d := range discarded {
		hook(d)
	}
}
