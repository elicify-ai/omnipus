// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_redirect.go — the redirect CONTROL's agent half: stop a steered
// session's current turn (single session, never a cascade) and start the
// replacement turn with the new instruction. Called from the delegate tool
// (pkg/tools/delegate_redirect.go, action="redirect") through the steering
// sink's capability interface — no tools -> agent import.
//
// ADR-20260928 D2's full sequence-fence redirect (ledger intent, fenced
// resume half) is a later unit on the control ledger; this file composes
// the SAME user-visible behavior from the existing, tested primitives —
// SteerCanceller.StopTurns (the single-Stop stamp a human's Stop uses),
// the cooperative interrupt, and ReviveStoppedSession (the atomic
// note-clear/queued mutation Correction C1's stopped row names).
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const (
	// redirectEscalationDelay bounds how long the cooperative (soft) stop
	// may take before the generation-aware hard abort fires — the same
	// two-phase escalation executeStopAll's grace backstop uses, agent-side.
	redirectEscalationDelay = 5 * time.Second
	// redirectWaitDeadline bounds how long the replacement waits for the
	// stop to land before reporting the undelivered instruction visibly
	// (never a silent drop). Generous beyond the escalation: a hard abort
	// plus its own 3s/5s internal timers must fit inside it.
	redirectWaitDeadline = 30 * time.Second
	// redirectPollInterval is the waiter's record-poll cadence.
	redirectPollInterval = 200 * time.Millisecond
)

// RedirectSteeredSession replaces sessionID's current turn with
// instruction: the live turn is stopped (THIS session only — descendants
// keep going; a redirect is never a cascade), and once the stop has landed
// the session is resumed on the same conversation with the instruction as
// its newest input (Correction C1's stopped row — the same way the Resume
// command delivers a message).
//
// The caller has already verified steering authority and refused the
// terminal (done/failed: "already finished — use resume") and 3P
// (not_steerable) shapes; a terminal record that lands mid-flight — the
// final answer winning the race against the stop (F0929-race) — still gets
// the instruction: the waiter revives the finished session into its next
// round with it (Correction C1's done/failed row).
//
// Delivery is never silent: if the replacement instruction cannot be
// applied within redirectWaitDeadline, the parent receives a visible
// subagent_message error and the instruction is reported undelivered — the
// caller re-issues redirect or resume; nothing is dropped with only a log
// line.
func (al *AgentLoop) RedirectSteeredSession(ctx context.Context, sessionID string, by steer.Principal, instruction string) error {
	if al == nil {
		return fmt.Errorf("steer: redirect %q: no AgentLoop wired", sessionID)
	}
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return fmt.Errorf("steer: redirect %q: lifecycle store is not configured", sessionID)
	}
	rec, err := lifecycle.Load(sessionID)
	if err != nil {
		return fmt.Errorf("steer: redirect %q: %w", sessionID, err)
	}
	if rec.Terminal() || rec.Stopped() {
		return fmt.Errorf("steer: redirect %q: not callable for a %s session (the caller routes those)", sessionID, rec.State)
	}

	canceller := al.steerCanceller()
	bg := context.Background()

	// Stop half — single session, cooperative first: stamp the Stop fence
	// (durable; nothing queued can start and the stop survives a restart),
	// then ask the live turn to stop at its next tool boundary. subtree=false
	// means exactly this session: cause stop, no descendants walked, no
	// administrative cancellation, no goal touched (locked decision 1's stop
	// shape, which a redirect's stop half shares).
	report, serr := canceller.StopTurns(bg, sessionID, by, false, nil)
	if serr != nil {
		return fmt.Errorf("steer: redirect %q: stop: %w", sessionID, serr)
	}
	if len(report.Reached) == 0 {
		if len(report.Unreachable) > 0 {
			return fmt.Errorf("steer: redirect %q: %s", sessionID, report.Unreachable[0].Reason)
		}
		return fmt.Errorf("steer: redirect %q: the stop stamped nothing (already stopped or terminal)", sessionID)
	}
	// The same queue-removal + cooperative interrupt pair
	// cancelDelegatedSubtree's soft branch runs for each reached id — here
	// that id is exactly sessionID.
	for _, id := range report.Reached {
		stampedRec, loadErr := lifecycle.Load(id)
		if loadErr != nil {
			return fmt.Errorf("steer: redirect %q: selected stop effect: %w", id, loadErr)
		}
		effects, effectErr := al.stopEffectsForCallback(id, stampedRec.Generation)
		if effectErr != nil {
			return fmt.Errorf("steer: redirect %q: selected stop effect: %w", id, effectErr)
		}
		al.removeQueuedStopEffects(id, effects)
		if _, interruptErr := al.Interrupt(id, ScopeSelfOnly, "delegate redirect"); interruptErr != nil {
			logger.WarnCF("agent", "steer: redirect: cooperative interrupt failed (the Stop marker is durable)",
				map[string]any{"session_id": id, "error": interruptErr.Error()})
		}
	}

	// Escalation backstop: if the cooperative stop has not landed within
	// redirectEscalationDelay, fire the generation-aware hard abort for this
	// ONE session — the same adapter the human Stop's fallback uses, which
	// also lands the never-ran stop for a queued-only target.
	generation := rec.Generation
	time.AfterFunc(redirectEscalationDelay, func() {
		cur, err := lifecycle.Load(sessionID)
		if err != nil || cur.Terminal() || cur.Stopped() {
			return
		}
		if _, err := al.SteerGenerationCancel(bg, sessionID, generation); err != nil {
			logger.ErrorCF("agent", "steer: redirect: hard-stop escalation failed",
				map[string]any{"session_id": sessionID, "generation": generation, "error": err.Error()})
		}
	})

	// Resume half — deliver the replacement once the stop has landed. The
	// instruction is appended only AFTER the stop is durable (never raced
	// into the dying turn's steering queue), so the dying turn can never
	// consume it mid-flight; ReviveStoppedSession appends it as the newest
	// instruction and re-validates atomically under the record lock.
	go al.awaitStoppedAndRevive(bg, sessionID, by, instruction)
	return nil
}

// awaitStoppedAndRevive polls the record until the stop has landed
// (stopped) or the session reached a terminal state (the final answer won
// the race), then revives it with the replacement instruction — stopped:
// same conversation, same generation; terminal: next round. On the
// deadline it reports the undelivered instruction to the parent visibly.
func (al *AgentLoop) awaitStoppedAndRevive(ctx context.Context, sessionID string, by steer.Principal, instruction string) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	deadline := time.Now().Add(redirectWaitDeadline)
	for {
		time.Sleep(redirectPollInterval)
		rec, err := lifecycle.Load(sessionID)
		if err != nil {
			if errors.Is(err, session.ErrLifecycleNotFound) {
				return // the session was reaped; nothing to revive
			}
			logger.ErrorCF("agent", "steer: redirect: cannot read the record while waiting for the stop to land",
				map[string]any{"session_id": sessionID, "error": err.Error()})
			return
		}
		if rec.Stopped() || rec.Terminal() {
			revived, rerr := al.ReviveStoppedSession(ctx, sessionID, by, instruction)
			if rerr != nil {
				logger.ErrorCF("agent", "steer: redirect: the replacement instruction could not be delivered",
					map[string]any{"session_id": sessionID, "error": rerr.Error()})
				al.reportUndeliveredRedirect(sessionID, rec, rerr)
			}
			_ = revived
			return
		}
		if time.Now().After(deadline) {
			logger.ErrorCF("agent", "steer: redirect: the stop did not land within the deadline — the replacement instruction was NOT delivered",
				map[string]any{"session_id": sessionID, "deadline_seconds": int(redirectWaitDeadline.Seconds())})
			al.reportUndeliveredRedirect(sessionID, rec, fmt.Errorf("the current turn did not stop within %s", redirectWaitDeadline))
			return
		}
	}
}

// reportUndeliveredRedirect tells the redirecting parent, through the same
// visible subagent_message channel the queued-steering abandonment uses,
// that the replacement instruction did not land — never a silent drop.
func (al *AgentLoop) reportUndeliveredRedirect(sessionID string, rec *session.LifecycleRecord, cause error) {
	parentID := steerParentSessionID(rec)
	if strings.TrimSpace(parentID) == "" {
		return
	}
	al.deliverSubagentMessage(parentID, rec, "error",
		fmt.Sprintf("Redirect for session %s was not applied: %v. Re-issue delegate redirect, or use delegate resume.",
			sessionID, cause), nil)
}
