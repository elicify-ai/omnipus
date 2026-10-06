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
	// redirectWaitDeadline bounds how long the replacement waits for the
	// stop to land before reporting the undelivered instruction visibly
	// (never a silent drop). Generous beyond the one Stop's 3 s forced stop
	// and its 3 s detach safety net.
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

	bg := context.Background()

	// Stop half — the one Stop (stop_session.go::StopSession), this session
	// only: the fence and note are stamped durably first, the live turn is
	// asked to stop at once and forced 3 s later on the selected execution.
	// No descendants are walked, no goal is touched (locked decision 1's
	// stop shape, which a redirect's stop half shares).
	res, serr := al.StopSession(bg, StopRequest{
		SessionID: sessionID, By: by, Channel: "agent",
		HooksFor: func(string) CancelHooks { return CancelHooks{} },
	})
	if serr != nil {
		return fmt.Errorf("steer: redirect %q: stop: %w", sessionID, serr)
	}
	if res.RootErr != nil {
		return fmt.Errorf("steer: redirect %q: stop: %w", sessionID, res.RootErr)
	}
	report := res.Report
	if len(report.Unreachable) > 0 {
		return fmt.Errorf("steer: redirect %q: %s", sessionID, report.Unreachable[0].Reason)
	}
	if len(report.SkippedNewerGeneration) > 0 {
		return fmt.Errorf("steer: redirect %q: the selected stop was superseded", sessionID)
	}
	if _, selected := res.Selected[sessionID]; len(report.Reached) == 0 || !selected {
		return fmt.Errorf("steer: redirect %q: the stop stamped nothing (already stopped or terminal)", sessionID)
	}

	// Resume half — deliver the replacement once the stop has landed. The
	// instruction is appended only AFTER the stop is durable (never raced
	// into the dying turn's steering queue), so the dying turn can never
	// consume it mid-flight; ReviveStoppedSession appends it as the newest
	// instruction and re-validates atomically under the record lock. rec is
	// handed to the waiter as the launch-time parent-discovery fallback: it
	// is the record this call successfully loaded before the stop was
	// stamped, so when the record can no longer be read mid-wait the parent
	// is still known from it — never invented.
	//
	// The waiter is owned by the loop: it counts as an active request (the
	// shutdown drains join it) and runs on the loop-lifetime context, so a
	// shutdown ends the wait and reports the undelivered instruction
	// instead of leaving a detached goroutine behind.
	if !al.beginActiveRequest() {
		return fmt.Errorf("steer: redirect %q: the agent is shutting down; the session was stopped but the replacement instruction was not delivered", sessionID)
	}
	waitCtx := al.inboundRunContext()
	go func() {
		defer al.endActiveRequest()
		al.awaitStoppedAndRevive(waitCtx, rec, by, instruction)
	}()
	return nil
}

// awaitStoppedAndRevive polls the record until the stop has LANDED
// (state LifecycleStopped) or the session reached a terminal state (the
// final answer won the race), then revives it with the replacement
// instruction — stopped: same conversation, same generation; terminal: next
// round. On the deadline it reports the undelivered instruction to the
// parent visibly.
//
// The landed state is the qualifier — never LifecycleRecord.Stopped() alone:
// Stopped() is also true while a stop fence is still IN FLIGHT (stamped for
// the current generation, state still running/queued). Reviving on that
// shape re-queues a generation the old, still-registered turn then refuses,
// stranding the record queued with nobody running it. Only
// TransitionSession's landing (fence cleared, state stopped) or a terminal
// outcome means the old turn is really gone.
//
// Delivery is never silent: every exit that leaves the replacement
// unapplied reports the undelivered instruction to the parent through
// reportUndeliveredRedirect — a revive refusal (both the error return and
// the (false, nil) decline), a record that cannot be read mid-wait, and a
// record that no longer exists all tell the parent, never a log line
// alone. initial is the record RedirectSteeredSession loaded before the
// stop was stamped; it is the parent-discovery fallback for the exits
// where the record itself can no longer be read or no longer exists. The
// launch-time edge is the only parent ever reported to — when it is
// unknown there is nobody to tell, and none is invented.
func (al *AgentLoop) awaitStoppedAndRevive(ctx context.Context, initial *session.LifecycleRecord, by steer.Principal, instruction string) {
	lifecycle := al.GetSessionLifecycleStore()
	if lifecycle == nil {
		return
	}
	sessionID := initial.SessionID
	deadline := time.Now().Add(redirectWaitDeadline)
	poll := time.NewTicker(redirectPollInterval)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.ErrorCF("agent", "steer: redirect: the agent shut down while waiting for the stop to land — the replacement instruction was NOT delivered",
				map[string]any{"session_id": sessionID})
			al.reportUndeliveredRedirect(sessionID, initial,
				fmt.Errorf("the agent shut down before the stop landed: %w", ctx.Err()))
			return
		case <-poll.C:
		}
		rec, err := lifecycle.Load(sessionID)
		if err != nil {
			if errors.Is(err, session.ErrLifecycleNotFound) {
				logger.ErrorCF("agent", "steer: redirect: the session no longer exists — the replacement instruction was NOT delivered",
					map[string]any{"session_id": sessionID})
				al.reportUndeliveredRedirect(sessionID, initial,
					errors.New("the session no longer exists; the replacement instruction was never applied"))
				return
			}
			logger.ErrorCF("agent", "steer: redirect: cannot read the record while waiting for the stop to land",
				map[string]any{"session_id": sessionID, "error": err.Error()})
			al.reportUndeliveredRedirect(sessionID, initial,
				fmt.Errorf("the session record could not be read while waiting for the stop to land: %w", err))
			return
		}
		// Landed-or-terminal only — an in-flight fence (Stopped() true, state
		// still running/queued) is not enough; see this function's doc
		// comment for the stranded-queued shape an early revive produces.
		if rec.State == session.LifecycleStopped || rec.Terminal() {
			revived, rerr := al.ReviveStoppedSession(ctx, sessionID, by, instruction)
			if rerr != nil {
				logger.ErrorCF("agent", "steer: redirect: the replacement instruction could not be delivered",
					map[string]any{"session_id": sessionID, "error": rerr.Error()})
				al.reportUndeliveredRedirect(sessionID, rec, rerr)
				return
			}
			if !revived {
				// (false, nil): Revive re-read the record under its own lock
				// and found it live again — resumed or revived elsewhere in
				// the window since this poll observed stopped/terminal. The
				// replacement was NOT applied; the parent must know.
				logger.ErrorCF("agent", "steer: redirect: the replacement instruction was not applied — the session was already live again",
					map[string]any{"session_id": sessionID})
				al.reportUndeliveredRedirect(sessionID, rec,
					errors.New("the session was resumed or revived elsewhere before the replacement could be applied"))
			}
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
