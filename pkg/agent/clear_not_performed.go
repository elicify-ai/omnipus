// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// clear_not_performed.go: the terminal "this /clear did not happen" fact.
// A /clear that is refused or cannot be carried out is reported to the gateway
// for the person's own /clear row (its transcript entry), so the web client
// reads the outcome from a message_status "failed" frame instead of parsing
// the explanatory reply or waiting on a timer. A /clear that DID happen is
// signalled by its marker row and reports nothing here.

package agent

import (
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// ClearNotPerformedFunc receives the session and the transcript entry id of the
// person's /clear message that was refused or not performed.
type ClearNotPerformedFunc func(sessionID, transcriptEntryID string)

// SetClearNotPerformedHook installs the single receiver (the gateway's WS
// handler, at construction). Nil removes it.
func (al *AgentLoop) SetClearNotPerformedHook(fn ClearNotPerformedFunc) {
	if al == nil {
		return
	}
	if fn == nil {
		al.clearNotPerformedHook.Store(nil)
		return
	}
	al.clearNotPerformedHook.Store(&fn)
}

// notifyClearNotPerformed reports msg's /clear row as not performed. A message
// from another channel has no web row to mark and is skipped quietly. A WEB
// message that cannot be reported (no receiver, or no session or entry id) is
// never silent: with the client's timer gone, "no failed frame" reads as "held",
// so the only trace left is this Warn.
func (al *AgentLoop) notifyClearNotPerformed(msg bus.InboundMessage) {
	if al == nil {
		return
	}
	if msg.Channel != "" && msg.Channel != "webchat" {
		return
	}
	hook := al.clearNotPerformedHook.Load()
	sessionID := strings.TrimSpace(msg.SessionID)
	entryID := strings.TrimSpace(msg.TranscriptEntryID)
	if hook == nil || sessionID == "" || entryID == "" {
		slog.Warn("agent: a refused /clear could not be reported to its chat row; the row will look held",
			"session_id", sessionID, "entry_id", entryID, "receiver_installed", hook != nil)
		return
	}
	(*hook)(sessionID, entryID)
}

// NotifyClearNotPerformedForTest drives the receiver the way the worker does,
// for the gateway's wiring test.
func (al *AgentLoop) NotifyClearNotPerformedForTest(sessionID, transcriptEntryID string) {
	al.notifyClearNotPerformed(bus.InboundMessage{SessionID: sessionID, TranscriptEntryID: transcriptEntryID})
}
