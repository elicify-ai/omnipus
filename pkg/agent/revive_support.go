// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 fix round for #890 (8-reviewer gate): execution of a revived
// ordinary-root inbound turn — the detached, published, shutdown-aware
// replacement for the synchronous processMessage call the gate's architect
// pass flagged (CC-1: the reply was discarded; F1: the call blocked Run's
// message pump for a whole turn; silent-failure-hunter #1: a turn that
// already ran could be queued a second time) — plus the revival-failure
// memory the D2 launch backstop consults so a refusal after a failed resume
// tells the truth (silent-failure-hunter #6).
package agent

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// revivedTurnHandoffBudget bounds how long a revived ordinary-root turn
// waits for the cancelled turn it replaces to unwind before starting. The
// revived turn reads the session's history while the dying turn may still be
// appending its final writes; letting them overlap would let the resumed
// turn read a half-written history. The wait is on the dying turn's
// finishedChan (closed in turn_exit.go::Finish), so it releases as soon as
// the turn exits — the budget only caps the pathological case (a wedged
// unwind), after which the resumed turn starts anyway rather than stalling
// the user's message behind a turn that may never end.
const revivedTurnHandoffBudget = 5 * time.Second

// inboundRunContext returns the context a revived inbound turn runs under.
// Run stores its own runCtx here at startup, so a resumed turn is cancelled
// by the same Stop that ends the dispatch loop and is drained by
// WaitForActiveRequests like every other in-flight request (architect CC-2).
// Before Run has stored one, the fallback is the loop-lifetime context
// (loopCtx — cancelled by Stop and Close), so a revival racing boot is still
// cancelled by the same Stop instead of running detached on
// context.Background(); the plain Background fallback remains only for
// zero-value loops built without NewAgentLoop.
func (al *AgentLoop) inboundRunContext() context.Context {
	if p := al.inboundCtx.Load(); p != nil {
		return *p
	}
	if al.loopCtx != nil {
		return al.loopCtx
	}
	return context.Background()
}

// revivalFailure is one recorded failed resume attempt: when it happened and
// what failed. Recorded on the AgentLoop (not the lifecycle record — the
// store write that recording needs is exactly what is failing) and cleared
// by the next successful revive of the same session.
type revivalFailure struct {
	at    time.Time
	cause error
}

// markRevivalFailure records that sessionID's most recent resume attempt
// failed with cause, so the D2 launch backstop can distinguish "stopped,
// and the next message would revive it" from "stopped, and the resume
// attempt itself is failing" (silent-failure-hunter #6: the second state
// must not tell the user to send another message).
func (al *AgentLoop) markRevivalFailure(sessionID string, cause error) {
	if al == nil || sessionID == "" || cause == nil {
		return
	}
	al.revivalFailures.Store(sessionID, revivalFailure{at: time.Now(), cause: cause})
}

// lastRevivalFailure reports the session's recorded failed resume attempt,
// if one is still recorded. Entries are cleared only by a successful revive
// (clearRevivalFailure); a stale entry for a session that later revived
// elsewhere is overwritten by the next failure and never read once the
// record is live again — the launch backstop consults it only for a
// terminal/stopped parent, where a stale entry would otherwise mislabel a
// genuine stop.
func (al *AgentLoop) lastRevivalFailure(sessionID string) (revivalFailure, bool) {
	if al == nil || sessionID == "" {
		return revivalFailure{}, false
	}
	v, ok := al.revivalFailures.Load(sessionID)
	if !ok {
		return revivalFailure{}, false
	}
	rf, isRF := v.(revivalFailure)
	if !isRF {
		return revivalFailure{}, false
	}
	return rf, true
}

// clearRevivalFailure drops the session's recorded resume failure after a
// successful revive, so a later, genuine stop-refusal is not mislabelled as
// a revival failure.
func (al *AgentLoop) clearRevivalFailure(sessionID string) {
	if al == nil || sessionID == "" {
		return
	}
	al.revivalFailures.Delete(sessionID)
}

// runRevivedOrdinaryTurn runs a revived ordinary-root inbound turn DETACHED:
// it owns the message from here on, starts exactly one processMessage run,
// publishes the reply through the same publishResponseIfNeeded path every
// other inbound caller uses (architect CC-1), and never reports failure
// back as "enqueue rejected" — a failure after the message was accepted is
// logged at error level and delivered to the user as the turn's error reply
// (silent-failure-hunter #1: no second run, no Debug-level loss).
//
// Mirrors loop.go's unroutable-message goroutine shape: registered with
// activeRequests so shutdown drains it (#265), an outer recover that logs
// and ends the goroutine (this is the only supervising recover — re-panicking
// would crash the process), and an inner C8 recover that synthesizes a
// terminal error frame and force-publishes it before re-panicking so the
// outer recover still records the original panic.
func (al *AgentLoop) runRevivedOrdinaryTurn(msg bus.InboundMessage, sessionKey string) {
	// Tracked in activeRequests so shutdown drains it (#265), like the
	// unroutable path.
	al.activeRequests.Add(1)
	go func() {
		defer al.activeRequests.Done()

		var (
			response  string
			ag        *AgentInstance
			published bool
		)

		// Outer recover — the only supervising recover for this detached
		// goroutine; it must swallow the panic (log-and-exit-goroutine), not
		// re-panic: there is no layer above this goroutine.
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent", "Panic in revived ordinary-root turn goroutine",
					map[string]any{
						"panic":       r,
						"session_key": sessionKey,
						"channel":     msg.Channel,
						"chat_id":     msg.ChatID,
						"stack":       string(debug.Stack()),
					})
			}
		}()

		// C8 (chat-stream-hang): guarantee a terminal frame on EVERY exit
		// path, including panic-recover. Registered AFTER the outer recover
		// so — defers being LIFO — this recover runs FIRST during unwinding:
		// it force-publishes an error frame on a fresh bounded-timeout
		// context (turnCtx may be cancelled mid-panic) and re-panics so the
		// outer recover logs the original panic with its stack.
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorCF("agent", "Panic in processMessage — emitting terminal error frame for a revived ordinary-root turn",
					map[string]any{
						"panic":       r,
						"session_key": sessionKey,
						"channel":     msg.Channel,
						"chat_id":     msg.ChatID,
						"stack":       string(debug.Stack()),
					})
				if response == "" {
					response = "Error processing message: the agent turn failed unexpectedly. Please try again."
				}
				if !published {
					func() {
						defer func() {
							if pr := recover(); pr != nil {
								logger.ErrorCF("agent", "Panic while force-publishing terminal frame for a revived ordinary-root turn",
									map[string]any{"panic": pr, "session_key": sessionKey, "channel": msg.Channel, "chat_id": msg.ChatID})
							}
						}()
						termCtx, termCancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer termCancel()
						al.publishResponseIfNeeded(termCtx, ag, msg.Channel, msg.ChatID, response)
					}()
					published = true
				}
				panic(r)
			}
		}()

		// ADR-093 D4 (architect CC-2): the cancelled turn this message
		// revives past may still be unwinding and appending its final
		// history writes. Wait for its finishedChan (bounded — see
		// revivedTurnHandoffBudget) so the resumed turn starts only after
		// the turn it replaces has exited; proceed anyway on a wedged unwind
		// rather than stalling the user's message indefinitely.
		if dying := al.getActiveTurnState(sessionKey); dying != nil {
			select {
			case <-dying.Finished():
			case <-time.After(revivedTurnHandoffBudget):
				logger.WarnCF("agent", "adr093: the replaced turn did not unwind within the handoff budget; the resumed turn starts anyway",
					map[string]any{"session_key": sessionKey})
			}
		}

		turnCtx := al.inboundRunContext()
		var err error
		response, ag, err = al.processMessage(turnCtx, msg)
		if err != nil {
			// The message was already accepted; this failure must surface as
			// an error-level log and the turn's error reply — never as an
			// "enqueue rejected" error that would queue the same message for
			// a second run.
			logger.ErrorCF("agent", "adr093: revived ordinary-root turn failed",
				map[string]any{
					"session_key": sessionKey,
					"channel":     msg.Channel,
					"chat_id":     msg.ChatID,
					"error":       err.Error(),
				})
			if response == "" {
				// ADR-051 §RD5: never surface raw err text in the reply —
				// TranslateTurnError keeps known-refusal copy and replaces
				// provider-originated text with typed copy.
				response = TranslateTurnError(err).Message
			}
		}
		if response != "" {
			al.publishResponseIfNeeded(turnCtx, ag, msg.Channel, msg.ChatID, response)
			published = true
		}
	}()
}
