// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_keeper_reconnect_test.go — ADR-082 T-13/S-14/FR-013: a keeper-style
// follow-up turn (idle steer, zero-output push, nudge, fallback compile —
// all dispatched via AsyncNotifier.Notify on the "system" channel, exactly
// like pkg/agent/goal_triggers.go's dispatchGoalAsyncFollowUp) must stream
// to whichever connection is CURRENTLY bound to its originating session,
// even when that is a DIFFERENT connection than the one active when the
// goal/turn started (E5: the keeper's captured chat id goes stale the
// moment the viewer reconnects on a new WS connection or a new tab).
//
// This test lives in pkg/gateway (not pkg/agent) because verifying "reaches
// the REBOUND connection" requires the real WSHandler connection-binding
// machinery (h.sessions/h.sessionIDs, GetStreamer, webchatChannel.Send) —
// pkg/agent's own keeper tests fake bus.Streamer/StreamDelegate and cannot
// observe this.
//
// It does not call the private dispatchGoalAsyncFollowUp/AsyncNotifier.Notify
// (unexported, package agent) directly — instead it publishes the exact
// bus.InboundMessage shape AsyncNotifier.Notify constructs (Channel:
// "system", AsyncOriginAgentID, AsyncTranscriptSessionID, Sender.CanonicalID)
// onto the SAME message bus a running AgentLoop.Run consumes, which routes
// it through processSystemMessage (pkg/agent/loop.go) — the identical
// dispatch path every keeper trigger uses.
//
// Log-line proof caveat: pkg/logger (zerolog) binds its ConsoleWriter to
// os.Stdout once at package-init time with no exported redirect hook, so
// this test cannot literally grep for an ABSENT "Send failed"/"no active
// connection" log line the way an equivalent log/slog-based test could
// (see e.g. cookie_auth_observability_test.go's slog.SetDefault pattern,
// which does not apply here — pkg/channels' "Send failed" log
// (manager.go:1357) goes through logger.ErrorCF, not slog). The behavioral
// proof below is logically equivalent and strictly stronger: pkg/channels'
// sendWithRetry logs "Send failed" if AND ONLY IF webchatChannel.Send
// returns a non-nil error; asserting B affirmatively RECEIVES the keeper
// follow-up's content proves Send returned nil for this call, which proves
// no "Send failed" fired for it.
package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
)

// TestKeeperFollowUp_ReachesRebindedConnection is T-13.
func TestKeeperFollowUp_ReachesRebindedConnection(t *testing.T) {
	provider := newControllableStreamProvider(
		providerRound{tokens: []string{"Hello", " there."}},
		providerRound{tokens: []string{"Keeper", " follow-up:", " goal check-in."}},
	)
	srv, _, msgBus := newControllableStreamTestServer(t, provider)

	// Connection A starts the session with an ordinary first turn.
	connA := dialTestWS(t, srv)
	sendWSAuthFrameDevMode(t, connA)

	msgFrame := wsClientFrameTestHelper{Type: "message", Content: "hello"}
	data, err := json.Marshal(msgFrame)
	require.NoError(t, err)
	require.NoError(t, connA.WriteMessage(websocket.TextMessage, data))

	started := readFrameOfType(t, connA, "session_started", 5*time.Second)
	sessionID := started.SessionID
	require.NotEmpty(t, sessionID)

	// A disconnects — exactly like the goal's originating tab closing.
	require.NoError(t, connA.Close())

	// staleChatID stands in for what a keeper's captured GoalRouteChatID
	// would be: A's real webchat chat id (server-internal, never echoed on
	// the wire, so not literally recoverable here) — any value that does
	// NOT resolve to any live h.sessions/h.sessionIDs entry demonstrates
	// FR-013 identically, since D6's fix is keyed on SESSION id, never on
	// this string resolving to anything.
	const staleChatID = "webchat:stale-disconnected-chat-id"

	// A DIFFERENT connection, B, attaches to the SAME session — the
	// "reconnected on a new tab/socket" scenario E5 describes.
	connB := dialTestWS(t, srv)
	t.Cleanup(func() { _ = connB.Close() })
	sendWSAuthFrameDevMode(t, connB)

	attachFrame := wsClientFrameTestHelper{Type: "attach_session", SessionID: sessionID}
	attachData, err := json.Marshal(attachFrame)
	require.NoError(t, err)
	require.NoError(t, connB.WriteMessage(websocket.TextMessage, attachData))

	// Wait for evidence B's bind completed (skip the connection-open
	// session_state one-shot, which precedes any attach processing — see
	// turn_survives_disconnect_test.go's identical wait for the same reason).
	waitForAttachToBind(t, connB)

	// Dispatch a keeper-style follow-up: same bus.InboundMessage shape
	// AsyncNotifier.Notify constructs for dispatchGoalAsyncFollowUp,
	// carrying the STALE chat id (A's, now dead) but the CORRECT,
	// still-valid transcript session id.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, msgBus.PublishInbound(ctx, bus.InboundMessage{
		Channel: "system",
		Sender:  bus.SenderInfo{CanonicalID: "system:goal_loop"},
		ChatID:  staleChatID,
		Content: "Keeper follow-up: goal check-in.",
		// AsyncOriginAgentID / AsyncTranscriptSessionID: same fields
		// AsyncNotifier.Notify stamps for a keeper dispatch (FIX 5d).
		AsyncOriginAgentID:       "mia",
		AsyncTranscriptSessionID: sessionID,
	}))

	// B must receive the keeper follow-up's content — via the streaming
	// path (GetStreamer succeeds for the session id) or the
	// webchatChannel.Send fallback, either way proving delivery reached
	// the REBOUND connection despite the stale chat id.
	deadline := time.Now().Add(10 * time.Second)
	var seen strings.Builder
	found := false
	for time.Now().Before(deadline) && !found {
		connB.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, rerr := connB.ReadMessage()
		if rerr != nil {
			break
		}
		var f struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		if f.Type == "token" {
			seen.WriteString(f.Content)
		}
		if strings.Contains(seen.String(), "Keeper follow-up") {
			found = true
		}
	}
	require.True(t, found,
		"the keeper follow-up's content must reach the REBOUND connection B — "+
			"got: %q", seen.String())
}

// waitForAttachToBind blocks until conn has received an attach-related
// frame (i.e. NOT the connection-open session_state one-shot, which always
// precedes any attach_session processing) — proving handleAttachSession's
// bind (its first action, before any frame is emitted) has completed.
func waitForAttachToBind(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, raw, rerr := conn.ReadMessage()
		require.NoError(t, rerr, "must receive at least one attach-related frame")
		var probe struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &probe) == nil && probe.Type == "session_state" {
			continue
		}
		return
	}
}
