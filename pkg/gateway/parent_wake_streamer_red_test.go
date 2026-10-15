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
)

func TestGetStreamer_MissingSessionDoesNotGuess(t *testing.T) {
	h := &WSHandler{}
	const otherSession = "sess-other-pw"
	const otherChat = "chat-other-pw"

	streamer, ok := h.GetStreamer(context.Background(), "webchat", otherChat, "")
	if ok || streamer != nil {
		t.Fatalf("GetStreamer(webchat, %q, empty session) = (%v, %v), want a refusal — a wake with no session id is not attached to whichever chat is open (FR-003 / Test 29)", otherChat, streamer, ok)
	}
	// A named session on this bare handler is not called: resolveSessionStore
	// needs a loop. The refusal above is the whole observable. DEL-25 already
	// returns false, so this test is a regression pin and can be green.
	_ = otherSession
}
