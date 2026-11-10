// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// clear_held.go: the "this /clear is held" fact. A /clear is held only while the
// server is stopping the running reply before it clears (deferClearUntilStopped):
// it is accepted but not yet applied. The gateway turns the report into the
// existing message_status state=held for the person's /clear row. A /clear to an
// idle or stopped agent is applied at once (a person's message continues a
// landed stop) and is never reported held.

package agent

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// ClearHeldFunc receives the session and the transcript entry id of the
// person's /clear message the server has accepted but not yet applied.
type ClearHeldFunc func(sessionID, transcriptEntryID string)

// SetClearHeldHook installs the single receiver (the gateway's WS handler, at
// construction). Nil removes it.
func (al *AgentLoop) SetClearHeldHook(fn ClearHeldFunc) {
	if al == nil {
		return
	}
	if fn == nil {
		al.clearHeldHook.Store(nil)
		return
	}
	al.clearHeldHook.Store(&fn)
}

// notifyClearHeld reports msg's /clear row as held. A message from another
// channel has no web row to mark and is skipped quietly. A web message that
// cannot be reported is logged: its row then keeps the ordinary look, which is
// only a missing hint, not a wrong state.
func (al *AgentLoop) notifyClearHeld(msg bus.InboundMessage) {
	if al == nil {
		return
	}
	if msg.Channel != "" && msg.Channel != "webchat" {
		return
	}
	hook := al.clearHeldHook.Load()
	sessionID := strings.TrimSpace(msg.SessionID)
	entryID := strings.TrimSpace(msg.TranscriptEntryID)
	if hook == nil || sessionID == "" || entryID == "" {
		slog.Warn("agent: a held /clear could not be reported to its chat row",
			"session_id", sessionID, "entry_id", entryID, "receiver_installed", hook != nil)
		return
	}
	(*hook)(sessionID, entryID)
}

// ClearReleasedFunc receives the session and the transcript entry id of a /clear
// the server was holding and has now handed on: the running reply has stopped
// and the /clear is queued as the next turn. The hold is over; whether the clear
// then succeeds is reported by its marker row, and a failure by the not-performed
// report.
type ClearReleasedFunc func(sessionID, transcriptEntryID string)

// SetClearReleasedHook installs the single receiver (the gateway's WS handler, at
// construction). Nil removes it.
func (al *AgentLoop) SetClearReleasedHook(fn ClearReleasedFunc) {
	if al == nil {
		return
	}
	if fn == nil {
		al.clearReleasedHook.Store(nil)
		return
	}
	al.clearReleasedHook.Store(&fn)
}

// notifyClearReleased reports that msg's held /clear is no longer held, so its
// recorded state does not stay "held" for a client that retries the same id. A
// message from another channel has no web row and is skipped quietly; a web
// message that cannot be reported is logged.
func (al *AgentLoop) notifyClearReleased(msg bus.InboundMessage) {
	if al == nil {
		return
	}
	if msg.Channel != "" && msg.Channel != "webchat" {
		return
	}
	hook := al.clearReleasedHook.Load()
	sessionID := strings.TrimSpace(msg.SessionID)
	entryID := strings.TrimSpace(msg.TranscriptEntryID)
	if hook == nil || sessionID == "" || entryID == "" {
		slog.Warn("agent: a released /clear could not be reported to its chat row; a retry of it would still read as held",
			"session_id", sessionID, "entry_id", entryID, "receiver_installed", hook != nil)
		return
	}
	(*hook)(sessionID, entryID)
}

// NotifyClearReleasedForTest drives the receiver the way the worker does, for the
// gateway's wiring test.
func (al *AgentLoop) NotifyClearReleasedForTest(sessionID, transcriptEntryID string) {
	al.notifyClearReleased(bus.InboundMessage{SessionID: sessionID, TranscriptEntryID: transcriptEntryID})
}

// NotifyClearHeldForTest drives the receiver the way the worker does, for the
// gateway's wiring test.
func (al *AgentLoop) NotifyClearHeldForTest(sessionID, transcriptEntryID string) {
	al.notifyClearHeld(bus.InboundMessage{SessionID: sessionID, TranscriptEntryID: transcriptEntryID})
}
