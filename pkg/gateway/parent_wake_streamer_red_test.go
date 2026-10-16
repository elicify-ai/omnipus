// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Test 29.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - BDD "A wake with no session is not attached to another chat"
//   - FR-003
//   - Planned test TestGetStreamer_MissingSessionDoesNotGuess
//
// An empty session id is refused. A different session that is open does not
// receive that turn's streamer.
//
// Step 4 items 2–3 are deferred to CHECK.
package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestGetStreamer_MissingSessionDoesNotGuess(t *testing.T) {
	h, _, al := newTestWSHandler(t)
	t.Cleanup(h.Wait)
	const otherChat = "chat-other-pw"
	otherSession, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("SETUP: other open session: %v", err)
	}
	conn := makeTestConn()
	bindTestConnToSession(h, otherChat, otherSession.ID, conn)

	// FR-003: test the instrument with a named, genuinely bound session.
	// A bare handler cannot reveal an erroneous fallback into an open chat.
	control, ok := h.GetStreamer(context.Background(), "webchat", otherChat, otherSession.ID)
	if !ok || control == nil {
		t.Fatal("SETUP: a named session must obtain its real streamer")
	}
	if err := control.Update(context.Background(), "named-session control"); err != nil {
		t.Fatalf("SETUP: named-session token: %v", err)
	}
	if frame := readTokenFrame(t, conn.sendCh); frame.Content != "named-session control" {
		t.Fatalf("SETUP: other chat token=%q, want the named-session control", frame.Content)
	}

	streamer, ok := h.GetStreamer(context.Background(), "webchat", otherChat, "")
	if ok || streamer != nil {
		t.Fatalf("GetStreamer(webchat, %q, empty session) = (%v, %v), want a refusal — a wake with no session id is not attached to whichever chat is open (FR-003 / Test 29)", otherChat, streamer, ok)
	}
	select {
	case raw := <-conn.sendCh:
		t.Fatalf("the other open session received a frame after a missing-session wake: %s", raw)
	default:
	}
	// DEL-25 already refuses; this is a regression pin, not new RED proof.
}
