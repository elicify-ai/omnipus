//go:build !cgo

// Regression coverage for the reload/replay "never self-updates" bug found
// by live UAT re-verification (2026-07): after a browser reload, a
// background delegate's live completion event (or any other live event
// tied to the turn it was dispatched under) could never reach the NEW
// connection.
//
// Root cause: ServeHTTP mints a brand-new chatID ("webchat:" + uuid.New())
// for EVERY WebSocket connection — there is no client-supplied continuity
// across a reload. A turn's own ChatID (threaded onto every event payload)
// is stamped ONCE at turn-dispatch time from whichever connection sent the
// message, and never changes even after that connection closes. The
// forwarder's original matchesChatID matched only by that per-connection
// chatID (or the taskChatIDs alias, which maps THIS connection's chatID to
// the session_id — not to the stale chatID), so a turn dispatched before a
// reload could never have its live events reach the reattached connection.
//
// The fix (matchesEvent, pkg/gateway/websocket.go) adds a session-based
// fallback: when the event's own SessionID matches the session THIS
// connection currently has open (h.sessionIDs[chatID], set by
// handleAttachSession/handleChatMessage), the event is forwarded even
// though its ChatID names a different, stale connection.

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestEventForwarder_SessionBasedFallback_ReachesReattachedConnection is the
// direct regression test: a SubTurnEndPayload carrying a STALE ChatID (as a
// background delegate dispatched before a reload would) must still reach
// this connection when its SessionID matches the session this connection
// has attached to — even though its ChatID does not match this connection's
// own chatID or taskChatIDs alias at all.
func TestEventForwarder_SessionBasedFallback_ReachesReattachedConnection(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()

	const newChatID = "webchat:new-connection-after-reload"
	const staleChatID = "webchat:stale-connection-before-reload"
	const durableSessionID = "session-durable-abc123"

	// Simulate handleAttachSession having reattached this (new) connection
	// to the persisted session.
	h.mu.Lock()
	h.sessionIDs[newChatID] = durableSessionID
	h.mu.Unlock()

	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, newChatID, bus)

	// A background delegate's real completion event, carrying the STALE
	// pre-reload chatID (its turn was dispatched under the OLD connection)
	// but the SAME durable session_id.
	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnEnd,
		Payload: agent.SubTurnEndPayload{
			AgentID:           "ray",
			Status:            agent.SubTurnStatusSuccess,
			SpanID:            "span_c1",
			ParentSpawnCallID: session.ToolCallID("c1"),
			DurationMS:        45000,
			ChatID:            staleChatID,
			SessionID:         durableSessionID,
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1,
		"BUG REGRESSION: a live event whose ChatID names a stale, pre-reload connection must still "+
			"reach a NEW connection attached to the SAME session via its SessionID")
	frame := drainFrame(t, ch)
	assert.Equal(t, "subagent_end", frame.Type)
	assert.Equal(t, "span_c1", frame.SpanID)
	assert.Equal(t, "success", frame.Status)
}

// TestEventForwarder_SessionBasedFallback_DoesNotLeakAcrossDifferentSessions
// is the negative-case guard: an event whose SessionID does NOT match this
// connection's currently-attached session (and whose ChatID also doesn't
// match) must NOT be forwarded — the session-based fallback must not turn
// into a broadcast-to-everyone leak.
func TestEventForwarder_SessionBasedFallback_DoesNotLeakAcrossDifferentSessions(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()

	const thisConnChatID = "webchat:this-connection"
	h.mu.Lock()
	h.sessionIDs[thisConnChatID] = "session-A"
	h.mu.Unlock()

	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, thisConnChatID, bus)

	bus.Emit(agent.Event{
		Kind: agent.EventKindSubTurnEnd,
		Payload: agent.SubTurnEndPayload{
			AgentID:           "ray",
			Status:            agent.SubTurnStatusSuccess,
			SpanID:            "span_other",
			ParentSpawnCallID: session.ToolCallID("c-other"),
			ChatID:            "webchat:unrelated-connection",
			SessionID:         "session-B", // a DIFFERENT session
		},
	})

	bus.Close()
	<-done

	assert.Empty(t, ch,
		"an event belonging to a different session must not be forwarded to this connection")
}
