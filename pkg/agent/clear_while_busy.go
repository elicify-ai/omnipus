// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// clear_while_busy.go: /clear typed while a turn is running. Founder ruling: the
// /clear is shown in the chat as a queued message, it sends a Stop to stop the
// turn, and then the context and chat move. It is never refused for being busy.
//
// Before this, a /clear that reached a busy worker was steered into the live
// turn as ordinary text, so the model saw "/clear" and nothing was cleared.
// Now the worker holds the /clear back, runs the ONE session Stop
// (stop_session.go::StopSession, through RequestScopedCancelForSession), waits
// for the turn to settle, and only then queues the /clear as the next turn,
// where the normal command path (clearConversation) moves the window and
// appends the marker. If the Stop cannot be requested or the turn does not
// settle, the person is told and nothing is cleared - never half a clear.

package agent

import (
	"context"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// clearStopSettleTimeout bounds the wait for the stopped turn to leave the
// worker. The Stop itself forces the turn after cancelHardAbortDelay and
// abandons it a similar time later, so this is comfortably longer. A var so a
// test can shorten it.
var clearStopSettleTimeout = 30 * time.Second

const clearSettlePoll = 20 * time.Millisecond

// clearNotPerformedReply is the visible failure when the running turn could not
// be stopped. Nothing was cleared.
func clearNotPerformedReply(reason string) string {
	return "/clear was not performed: " + reason + " Nothing was cleared; send /clear again."
}

// isClearInbound reports whether msg is a person's /clear command.
func isClearInbound(msg bus.InboundMessage) bool {
	if !reviveInboundIsHumanTurn(msg) {
		return false
	}
	name, ok := commands.CommandName(msg.Content)
	return ok && name == "clear"
}

// deferClearUntilStopped takes over a /clear that arrives while this worker is
// inside a user turn. It returns true when it took the message (the Stop and the
// clear then run in the background); false leaves the message to the normal
// enqueue path (an idle worker, a system turn, any other message).
func (w *sessionWorker) deferClearUntilStopped(msg bus.InboundMessage) bool {
	if !isClearInbound(msg) {
		return false
	}
	w.steerMu.Lock()
	busy := w.inTurn.Load() && !w.systemTurn.Load()
	w.steerMu.Unlock()
	if !busy {
		return false
	}
	// Held from here until the stop settles and the clear runs. Reported BEFORE
	// the stop starts, so a later not-performed report can never precede it.
	w.parent.notifyClearHeld(msg)
	go w.stopThenClear(msg)
	return true
}

// stopThenClear stops the running turn through the one Stop, waits for it to
// settle, then queues the /clear as the next turn.
func (w *sessionWorker) stopThenClear(msg bus.InboundMessage) {
	if err := w.requestStopForClear(msg); err != nil {
		w.rejectClear(msg, "the running turn could not be stopped ("+err.Error()+").")
		return
	}
	if !w.waitForTurnToSettle(w.ctx, clearStopSettleTimeout) {
		w.rejectClear(msg, "the running turn did not stop in time.")
		return
	}
	if w.pushInbox(msg) {
		w.parent.notifyClearReleased(msg)
		return
	}
	// The worker is leaving (or its queue is full): hand the /clear to a live
	// worker for the scope, which spawns a fresh one if this one is gone.
	if !w.parent.dispatchSessionWorker(w.scope, msg) {
		w.rejectClear(msg, "the agent is too busy to queue it.")
		return
	}
	w.parent.notifyClearReleased(msg)
}

// requestStopForClear runs the one session Stop for the person who typed /clear.
// An unauthenticated sender is refused by the Stop itself ("Stop requires an
// authenticated sender"), and that error becomes the visible failure.
func (w *sessionWorker) requestStopForClear(msg bus.InboundMessage) error {
	userID := strings.TrimSpace(msg.Sender.CanonicalID)
	if userID == "" {
		userID = strings.TrimSpace(msg.GatewayUserID)
	}
	ctx, cancel := context.WithTimeout(w.ctx, clearStopSettleTimeout)
	defer cancel()
	if sessionID := strings.TrimSpace(msg.SessionID); sessionID != "" {
		_, _, err := w.parent.RequestScopedCancelForSession(ctx, sessionID, userID, msg.Channel, "session")
		return err
	}
	_, _, err := w.parent.RequestStopByChannelChat(ctx, msg.Channel, msg.ChatID, userID)
	return err
}

// waitForTurnToSettle reports whether the worker left its turn within timeout.
func (w *sessionWorker) waitForTurnToSettle(ctx context.Context, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(clearSettlePoll)
	defer tick.Stop()
	for {
		if !w.inTurn.Load() {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return !w.inTurn.Load()
		case <-tick.C:
		}
	}
}

// rejectClear tells the person the /clear did not happen. The person's /clear
// row stays in the transcript; nothing else was changed.
func (w *sessionWorker) rejectClear(msg bus.InboundMessage, reason string) {
	logger.WarnCF("agent.worker", "/clear while a turn was running was not performed",
		map[string]any{"scope": w.scope, "reason": reason})
	// The terminal fact for the person's /clear row; the reply below is only
	// the human-readable reason.
	w.parent.notifyClearNotPerformed(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := w.parent.bus.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: msg.Channel,
		ChatID:  msg.ChatID,
		Content: clearNotPerformedReply(reason),
	}); err != nil {
		logger.WarnCF("agent.worker", "Failed to send the /clear failure reply",
			map[string]any{"channel": msg.Channel, "error": err.Error()})
	}
}
