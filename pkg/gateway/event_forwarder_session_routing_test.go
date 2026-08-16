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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
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

// TestEventForwarder_ErrorFrame_ReachesReattachedConnection pins the D5-class
// hole the EventKindError arm kept after every other live event moved to
// matchesEvent: a workspace refuse / provider 429 stamped the originating
// tab's chatID, so a second tab (or a reload) attached to the same session
// dropped the typed error. token+done still fanned out, and the error
// bubble never appeared.
func TestEventForwarder_ErrorFrame_ReachesReattachedConnection(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()

	const newChatID = "webchat:new-connection-after-reload"
	const staleChatID = "webchat:stale-connection-before-reload"
	const durableSessionID = "session-durable-error-abc"

	h.mu.Lock()
	h.sessionIDs[newChatID] = durableSessionID
	h.mu.Unlock()

	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, newChatID, bus)

	bus.Emit(agent.Event{
		Kind: agent.EventKindError,
		Payload: agent.ErrorPayload{
			Stage:     "workspace",
			Code:      string(agent.CodeAgentNotConfigured),
			Message:   agent.UserMessageForCode(agent.CodeAgentNotConfigured),
			ChatID:    staleChatID,
			SessionID: durableSessionID,
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1,
		"a typed error whose ChatID names a stale connection must still reach a NEW connection attached to the SAME session")
	raw := <-ch
	var frame generated.ErrorFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.NotNil(t, frame.Payload)
	assert.Equal(t, string(agent.CodeAgentNotConfigured), frame.Payload.LlmError.Code)
	assert.Equal(t, agent.UserMessageForCode(agent.CodeAgentNotConfigured), frame.Message)
	require.NotNil(t, frame.SessionId)
	assert.Equal(t, durableSessionID, *frame.SessionId)
}

// TestEventForwarder_ErrorFrame_DoesNotLeakAcrossDifferentSessions is the
// negative twin: SessionID matching is not a broadcast.
func TestEventForwarder_ErrorFrame_DoesNotLeakAcrossDifferentSessions(t *testing.T) {
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
		Kind: agent.EventKindError,
		Payload: agent.ErrorPayload{
			Stage:     "workspace",
			Code:      string(agent.CodeAgentNotConfigured),
			Message:   agent.UserMessageForCode(agent.CodeAgentNotConfigured),
			ChatID:    "webchat:unrelated-connection",
			SessionID: "session-B",
		},
	})

	bus.Close()
	<-done

	assert.Empty(t, ch,
		"a typed error belonging to a different session must not be forwarded to this connection")
}

// TestEventForwarder_RateLimitFrame_ReachesReattachedConnection is the
// internal-limiter twin of TestEventForwarder_ErrorFrame_ReachesReattachedConnection.
// EventKindRateLimit used to match on ChatID only, so a second tab (or a
// reload) attached to the same session never saw Omnipus's own SEC-26 denial.
func TestEventForwarder_RateLimitFrame_ReachesReattachedConnection(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()

	const newChatID = "webchat:new-connection-after-reload"
	const staleChatID = "webchat:stale-connection-before-reload"
	const durableSessionID = "session-durable-ratelimit-abc"

	h.mu.Lock()
	h.sessionIDs[newChatID] = durableSessionID
	h.mu.Unlock()

	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, newChatID, bus)

	bus.Emit(agent.Event{
		Kind: agent.EventKindRateLimit,
		Payload: agent.RateLimitPayload{
			Scope:             "agent",
			Resource:          "llm_call",
			PolicyRule:        "max_agent_llm_calls_per_hour",
			RetryAfterSeconds: 12,
			AgentID:           "mia",
			ChatID:            staleChatID,
			SessionID:         durableSessionID,
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1,
		"an internal rate-limit whose ChatID names a stale connection must still reach a NEW connection attached to the SAME session")
	raw := <-ch
	var frame generated.RateLimitFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	assert.Equal(t, string(generated.WsFrameTypeRateLimit), frame.Type)
	assert.Equal(t, durableSessionID, frame.SessionId)
	assert.Equal(t, "max_agent_llm_calls_per_hour", frame.PolicyRule)
}

// TestEventForwarder_RateLimitFrame_DoesNotLeakAcrossDifferentSessions is
// the negative twin: SessionID matching is not a broadcast.
func TestEventForwarder_RateLimitFrame_DoesNotLeakAcrossDifferentSessions(t *testing.T) {
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
		Kind: agent.EventKindRateLimit,
		Payload: agent.RateLimitPayload{
			Scope:      "agent",
			Resource:   "llm_call",
			PolicyRule: "max_agent_llm_calls_per_hour",
			ChatID:     "webchat:unrelated-connection",
			SessionID:  "session-B",
		},
	})

	bus.Close()
	<-done

	assert.Empty(t, ch,
		"an internal rate-limit belonging to a different session must not be forwarded to this connection")
}
