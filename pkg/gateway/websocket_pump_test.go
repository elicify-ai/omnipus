// websocket_pump_test.go: tests for write pump and event forwarding to the client.

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from websocket.go tests 2026-09-15 ---

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

func TestEventForwarder_ErrorFrame_DetailScrubsRegisteredCredential(t *testing.T) {
	const secret = "sk-live-WSDETAIL-9pQ2x7"
	const body = `{"error":{"message":"Incorrect API key provided: ` + secret + `"}}`

	// Registering a credential publishes the process-wide replacer; leave the
	// process as this test found it.
	t.Cleanup(logger.SetSensitiveValueReplacer(nil))
	cfg := &config.Config{}
	cfg.RegisterSensitiveValues([]string{secret})

	pe := &agent.ProviderError{Status: 503, Body: body}
	translated := agent.TranslateLLMError(pe, "LLM call failed")

	bus := agent.NewEventBus()
	defer bus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(4)
	done := runForwarder(h, wc, "chat-1", bus)

	bus.Emit(agent.Event{Kind: agent.EventKindError, Payload: agent.ErrorPayload{
		Stage:         "llm",
		ChatID:        "chat-1",
		Code:          string(translated.Code),
		Message:       translated.Message,
		ProviderError: pe,
	}})
	bus.Close()
	<-done

	require.Len(t, ch, 1)
	raw := <-ch
	assert.NotContains(t, string(raw), secret, "the error frame must not carry the registered credential anywhere")

	var frame generated.ErrorFrame
	require.NoError(t, json.Unmarshal(raw, &frame))
	require.NotNil(t, frame.Payload)
	require.NotNil(t, frame.Payload.LlmError.Detail)
	detail := *frame.Payload.LlmError.Detail
	assert.Contains(t, detail, "status=503", "the detail must still carry the provider's status for the operator")
	assert.Contains(t, detail, "Incorrect API key provided", "the detail must still carry the provider's own words")
	assert.Contains(t, detail, "[FILTERED]", "the detail must show where the credential was scrubbed")
	assert.Equal(t, agent.UserMessageForCode(agent.CodeNetwork), frame.Payload.LlmError.Message,
		"the frame's message is the plain message for the error's code")
}

func TestEventForwarder_ErrorFrame_PrefersPayloadCodeAndMessage(t *testing.T) {
	// A curated, caller-provided message that the classifier would, if
	// re-run against it, reclassify to CodeContentPolicy ("safety" is a
	// pinned contentPolicySubstrings hit) — proving the forwarder must use
	// p.Code/p.Message verbatim rather than recomputing.
	const curated = "hook aborted turn during before_tool: content flagged under our safety policy"

	cases := []struct {
		name        string
		payload     agent.ErrorPayload
		wantMessage string
		wantCode    string
	}{
		{
			name: "populated Code — curated message preserved verbatim",
			payload: agent.ErrorPayload{
				Stage:   "hook.before_tool",
				Code:    string(agent.CodeUnknown),
				Message: curated,
				ChatID:  "chat-1",
			},
			wantMessage: curated,
			wantCode:    string(agent.CodeUnknown),
		},
		{
			name: "empty Code falls back to the classifier (legacy/uncovered call site)",
			payload: agent.ErrorPayload{
				Stage:         "runTurn",
				Message:       "network hiccup",
				ProviderError: &agent.ProviderError{Status: 500, Body: "internal server error"},
				ChatID:        "chat-1",
			},
			wantMessage: agent.UserMessageForCode(agent.CodeNetwork),
			wantCode:    string(agent.CodeNetwork),
		},
		{
			// SUPERSEDED CASE — read before "restoring" it.
			//
			// This case previously asserted the OPPOSITE (wantFrame: false,
			// "Code=rate_limited is suppressed — RateLimitFrame is
			// authoritative") and checked only assert.Empty(t, ch): it pinned
			// the suppression as correct and asserted nothing about a
			// replacement frame ever arriving. That made it a test that
			// encoded the bug — a provider's HTTP 429 emits exactly this
			// payload from runTurn's LLM-error block, and the suppression
			// deleted the user's only signal, producing a turn that opened,
			// said nothing, and closed reporting success.
			//
			// The "RateLimitFrame is authoritative" premise is false for this
			// payload: EventKindRateLimit has exactly one producer
			// (recordRateLimitDenial), reachable only from Omnipus's own
			// internal SEC-26 limiter, which never runs for an upstream
			// refusal. See eventForwarder's EventKindError arm for the full
			// producer argument, and
			// TestEventForwarder_InternalRateLimitDenial_EmitsExactlyOneFrame
			// for the proof that the internal limiter still emits exactly one
			// frame.
			name: "Code=rate_limited is FORWARDED — an upstream refusal must reach the user",
			payload: agent.ErrorPayload{
				Stage:   "runTurn",
				Code:    string(agent.CodeRateLimited),
				Message: "rate limit: too many requests (retry after 5s)",
				ChatID:  "chat-1",
			},
			wantMessage: "rate limit: too many requests (retry after 5s)",
			wantCode:    string(agent.CodeRateLimited),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := agent.NewEventBus()
			defer bus.Close()
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			done := runForwarder(h, wc, "chat-1", bus)

			bus.Emit(agent.Event{Kind: agent.EventKindError, Payload: tc.payload})
			bus.Close()
			<-done

			require.Len(t, ch, 1)
			raw := <-ch
			var frame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			assert.Equal(t, tc.wantMessage, frame.Message)
			require.NotNil(t, frame.Payload)
			assert.Equal(t, tc.wantMessage, frame.Payload.LlmError.Message)
			assert.Equal(t, tc.wantCode, frame.Payload.LlmError.Code)
		})
	}
}

// TestEventForwarder_ErrorFrame_DetailFollowsCuratedRule is the FIX 2
// (re-review) regression: when p.Code != "" the forwarder previously left
// `detail` pinned to `translated.Detail` — the Detail computed by the
// UNCONDITIONAL, top-of-block agent.TranslateLLMError(p.ProviderError,
// p.Message) call — instead of recomputing it alongside code/message/
// retryable inside the curated-override branch. Every curated call site
// today passes ProviderError: nil, so this was harmless by coincidence
// (agent.BuildDetail(nil, msg) echoes msg, same as translated.Detail in that
// case — the first case below pins that this still holds). The second case
// pins the forward-looking contract: a curated Code+Message paired with a
// non-nil ProviderError must produce Detail from
// agent.BuildDetail(p.ProviderError, message) — the status/body diagnostic —
// not silently diverge from whatever a future refactor of the unconditional
// fresh-classification call happens to produce.
func TestEventForwarder_ErrorFrame_DetailFollowsCuratedRule(t *testing.T) {
	cases := []struct {
		name       string
		payload    agent.ErrorPayload
		wantDetail string
	}{
		{
			name: "curated Code+Message, nil ProviderError — Detail echoes the curated message",
			payload: agent.ErrorPayload{
				Stage:   "hook.before_tool",
				Code:    string(agent.CodeUnknown),
				Message: "hook aborted turn: policy violation",
				ChatID:  "chat-1",
			},
			wantDetail: "hook aborted turn: policy violation",
		},
		{
			name: "curated Code+Message, non-nil ProviderError — Detail carries status/body, not the message",
			payload: agent.ErrorPayload{
				Stage:         "hook.before_tool",
				Code:          string(agent.CodeUnknown),
				Message:       "hook aborted turn: policy violation",
				ProviderError: &agent.ProviderError{Status: 503, Body: "upstream unavailable"},
				ChatID:        "chat-1",
			},
			wantDetail: "status=503 body=upstream unavailable",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := agent.NewEventBus()
			defer bus.Close()
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			done := runForwarder(h, wc, "chat-1", bus)

			bus.Emit(agent.Event{Kind: agent.EventKindError, Payload: tc.payload})
			bus.Close()
			<-done

			require.Len(t, ch, 1)
			raw := <-ch
			var frame generated.ErrorFrame
			require.NoError(t, json.Unmarshal(raw, &frame))
			require.NotNil(t, frame.Payload)
			require.NotNil(t, frame.Payload.LlmError.Detail)
			assert.Equal(t, tc.wantDetail, *frame.Payload.LlmError.Detail,
				"Detail must follow the same curated-preferred rule as Code/Message/Retryable")
		})
	}
}

// ---------------------------------------------------------------------------
// M4-2: Exponential-backoff send / droppedFrames counter (sendConnGenFrame)
// ---------------------------------------------------------------------------

// TestSendConnGenFrame_ResetOnSuccess verifies that droppedFrames is reset to 0
// after a successful non-critical send.
// BDD: Given a wsConn with a pre-set droppedFrames=5 and a drained sendCh,
// When sendConnGenFrame delivers a non-critical "token" frame successfully,
// Then wc.droppedFrames is 0.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes default branch droppedFrames reset
func TestSendConnGenFrame_ResetOnSuccess(t *testing.T) {
	wc := makeTestConn()
	// Pre-populate so we can confirm the reset.
	wc.droppedFrames.Store(5)

	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "hello",
		SessionId: "sess-test",
	})

	assert.Equal(t, int32(0), wc.droppedFrames.Load(),
		"droppedFrames must be reset to 0 after a successful non-critical send")
}

// TestSendConnGenFrame_IncrementsDroppedFramesOnFullChannel verifies that droppedFrames
// increments when all three backoff attempts are exhausted.
// BDD: Given a wsConn with a zero-capacity send channel (always full),
// When sendConnGenFrame is called with a non-critical "token" frame,
// Then wc.droppedFrames increments by 1.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes backoff exhaustion counter
func TestSendConnGenFrame_IncrementsDroppedFramesOnFullChannel(t *testing.T) {
	// Zero-capacity channel: every send attempt fails immediately.
	wc := &wsConn{
		sendCh: make(chan []byte),
		doneCh: make(chan struct{}),
	}

	before := wc.droppedFrames.Load()
	// sendConnGenFrame spends up to ~60 ms on backoff attempts — acceptable in a unit test.
	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "overflow",
		SessionId: "sess-test",
	})

	assert.Equal(t, before+1, wc.droppedFrames.Load(),
		"droppedFrames must increment by 1 after all three backoff attempts fail")
}

// TestSendConnGenFrame_CriticalFrameBypassesBackoff verifies that "error" frames use
// the blocking critical path and do not increment droppedFrames.
// This is the differentiation test: critical vs non-critical frame types must produce
// different channel-send behavior.
// BDD: Given a wsConn with a drained sendCh,
// When sendConnGenFrame is called with a critical "error" frame,
// Then the frame is enqueued and droppedFrames remains 0.
// BDD: Given a wsConn with a drained sendCh,
// When sendConnGenFrame is called with a non-critical "token" frame with different content,
// Then the frame is enqueued and its content differs from the "error" frame.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes critical vs non-critical paths
func TestSendConnGenFrame_CriticalFrameBypassesBackoff(t *testing.T) {
	// Critical "error" frame.
	wcCrit := makeTestConn()
	sendConnGenFrame(wcCrit, string(generated.WsFrameTypeError), generated.ErrorFrame{
		Type:    string(generated.WsFrameTypeError),
		Message: "critical-message-A",
	})

	select {
	case raw := <-wcCrit.sendCh:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f), "critical frame must be valid JSON")
		assert.Equal(t, "error", f.Type)
		assert.Equal(t, "critical-message-A", f.Message,
			"critical frame content must match exactly — not hardcoded")
	default:
		t.Fatal("critical 'error' frame was not enqueued on sendCh")
	}
	assert.Equal(t, int32(0), wcCrit.droppedFrames.Load(), "critical frame must not increment droppedFrames")

	// Non-critical "token" frame — different type, different content.
	wcNonCrit := makeTestConn()
	sendConnGenFrame(wcNonCrit, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "stream-content-B",
		SessionId: "sess-test",
	})

	select {
	case raw := <-wcNonCrit.sendCh:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f), "non-critical frame must be valid JSON")
		assert.Equal(t, "token", f.Type,
			"non-critical frame type must be 'token' — different from critical path")
		assert.Equal(t, "stream-content-B", f.Content,
			"non-critical frame content must match exactly — not hardcoded")
	default:
		t.Fatal("non-critical 'token' frame was not enqueued on sendCh")
	}
}

// TestSendConnGenFrame_DegradedWarningAfterThreshold verifies that when droppedFrames
// reaches droppedFramesWarnThreshold (20), a "connection degraded" error frame is
// injected into the send channel.
//
// BDD: Given a wsConn with droppedFrames=19 and a full send channel,
// When the 20th non-critical frame is dropped,
// Then an ErrorFrame{type:"error", message contains "degraded"} is sent.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes degraded warning injection
func TestSendConnGenFrame_DegradedWarningAfterThreshold(t *testing.T) {
	wc := &wsConn{
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	wc.droppedFrames.Store(int32(droppedFramesWarnThreshold - 1)) // 19 already dropped

	// Fill the single slot so all three backoff attempts fail.
	wc.sendCh <- []byte(`{"type":"dummy"}`)

	// After ~150ms (after the ~60ms backoff exhausts and the warning send blocks),
	// drain the channel so the blocking degraded warning can land.
	receivedFrames := make(chan []byte, 4)
	go func() {
		time.Sleep(150 * time.Millisecond)
		for {
			select {
			case data, ok := <-wc.sendCh:
				if !ok {
					return
				}
				receivedFrames <- data
			case <-time.After(2 * time.Second):
				return
			}
		}
	}()

	// Trigger the 20th drop — blocks for ~60ms backoff + warning send.
	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "trigger-degraded",
		SessionId: "sess-test",
	})

	// Give the goroutine time to receive and forward the degraded frame.
	deadline := time.After(3 * time.Second)
	var degradedFound bool
outer:
	for {
		select {
		case raw := <-receivedFrames:
			var f replayFrameDecoder
			if json.Unmarshal(raw, &f) != nil {
				continue
			}
			if f.Type == "error" && strings.Contains(f.Message, "degraded") {
				degradedFound = true
				break outer
			}
		case <-deadline:
			break outer
		}
	}

	assert.True(t, degradedFound,
		"a 'connection degraded' error frame must be injected after %d consecutive drops",
		droppedFramesWarnThreshold)
}

// TestSendConnGenFrame_DroppedFramesResetAfterDegradedWarning verifies that after the
// degraded warning fires, droppedFrames is reset to 0.
// BDD: Given a wsConn at threshold-1 drops,
// When the 20th drop fires the degraded warning,
// Then wc.droppedFrames is reset to 0.
// Traces to: pkg/gateway/websocket.go — sendRawFrameBytes wc.droppedFrames = 0 after warning
func TestSendConnGenFrame_DroppedFramesResetAfterDegradedWarning(t *testing.T) {
	wc := &wsConn{
		sendCh: make(chan []byte, 1),
		doneCh: make(chan struct{}),
	}
	wc.droppedFrames.Store(int32(droppedFramesWarnThreshold - 1)) // 19
	wc.sendCh <- []byte(`{"type":"dummy"}`)

	// Drain so the degraded frame can land and the blocking select unblocks.
	// Closes sendCh on cleanup so the drainer goroutine exits with the test.
	go func() {
		for range wc.sendCh {
		}
	}()
	t.Cleanup(func() {
		// Brief wait so any in-flight sendConnGenFrame finishes before close.
		time.Sleep(50 * time.Millisecond)
		close(wc.sendCh)
	})

	sendConnGenFrame(wc, string(generated.WsFrameTypeToken), generated.TokenFrame{
		Type:      string(generated.WsFrameTypeToken),
		Content:   "trigger-reset",
		SessionId: "sess-test",
	})

	// Give the degraded warning send time to complete.
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, int32(0), wc.droppedFrames.Load(),
		"droppedFrames must be reset to 0 after the degraded warning fires")
}

// TestEventForwarder_RateLimitFrame_RoutingAndFieldMapping covers the
// EventKindRateLimit arm, which had no test of any kind despite being cited as
// the justification for suppressing an entire class of error frame.
//
// It pins three behaviours the arm promises in its own comment: agent-scoped
// denials reach only the connection whose chatID matches; global-scope denials
// (the daily cost cap, which is not tied to any chatID) broadcast to every
// connection regardless; and every RateLimitPayload field the SPA renders
// survives the hop to generated.RateLimitFrame.
func TestEventForwarder_RateLimitFrame_RoutingAndFieldMapping(t *testing.T) {
	basePayload := agent.RateLimitPayload{
		Scope:             "agent",
		Resource:          "llm_call",
		PolicyRule:        "max_agent_llm_calls_per_hour",
		RetryAfterSeconds: 42,
		AgentID:           "mia",
		Tool:              "bash",
		ChatID:            "chat-1",
	}

	cases := []struct {
		name      string
		payload   agent.RateLimitPayload
		wantFrame bool
		reason    string
	}{
		{
			name:      "agent-scoped denial for this chat is delivered",
			payload:   basePayload,
			wantFrame: true,
			reason:    "the denial belongs to this connection's chat",
		},
		{
			name: "agent-scoped denial for a different chat is not delivered",
			payload: func() agent.RateLimitPayload {
				p := basePayload
				p.ChatID = "chat-someone-else"
				return p
			}(),
			wantFrame: false,
			reason:    "a denial is meaningless — and a small information leak — outside its own chat",
		},
		{
			name: "global-scope denial broadcasts even to a non-matching chat",
			payload: func() agent.RateLimitPayload {
				p := basePayload
				p.Scope = "global"
				p.Resource = "daily_cost"
				p.ChatID = "chat-someone-else"
				return p
			}(),
			wantFrame: true,
			reason:    "the daily cost cap is not tied to a chatID; every connection must learn about it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evBus := agent.NewEventBus()
			defer evBus.Close()
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			done := runForwarder(h, wc, "chat-1", evBus)

			evBus.Emit(agent.Event{Kind: agent.EventKindRateLimit, Payload: tc.payload})
			evBus.Close()
			<-done

			if !tc.wantFrame {
				assert.Empty(t, ch, "no rate_limit frame expected: %s", tc.reason)
				return
			}

			require.Len(t, ch, 1, "exactly one rate_limit frame expected: %s", tc.reason)
			var frame generated.RateLimitFrame
			require.NoError(t, json.Unmarshal(<-ch, &frame))

			assert.Equal(t, string(generated.WsFrameTypeRateLimit), frame.Type)
			assert.Equal(t, tc.payload.Scope, frame.Scope)
			assert.Equal(t, tc.payload.Resource, frame.Resource)
			assert.Equal(t, tc.payload.PolicyRule, frame.PolicyRule,
				"policy_rule names WHICH limit fired — without it the user cannot tell "+
					"an LLM-call cap from a tool-call cap from the daily cost cap")
			assert.InDelta(t, tc.payload.RetryAfterSeconds, frame.RetryAfterSeconds, 0.001,
				"retry_after_seconds is the only actionable field in the frame")
			require.NotNil(t, frame.AgentId)
			assert.Equal(t, tc.payload.AgentID, *frame.AgentId)
			require.NotNil(t, frame.Tool)
			assert.Equal(t, tc.payload.Tool, *frame.Tool)
		})
	}
}

// TestEventForwarder_RateLimitFrame_IgnoresMistypedPayload pins the arm's
// payload type assertion. The historical dual-emit this whole area is about
// sent an EventKindError carrying a RateLimitPayload; the mirror-image mistake
// (an EventKindRateLimit carrying an ErrorPayload) must be dropped rather than
// panicking the forwarder goroutine, which serves a live WebSocket connection.
func TestEventForwarder_RateLimitFrame_IgnoresMistypedPayload(t *testing.T) {
	evBus := agent.NewEventBus()
	defer evBus.Close()
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(4)
	done := runForwarder(h, wc, "chat-1", evBus)

	evBus.Emit(agent.Event{
		Kind:    agent.EventKindRateLimit,
		Payload: agent.ErrorPayload{Code: string(agent.CodeRateLimited), Message: "wrong type", ChatID: "chat-1"},
	})
	evBus.Close()
	<-done

	assert.Empty(t, ch, "a mistyped payload must be dropped, not rendered and not panicked on")
}

// TestEventForwarder_InternalRateLimitDenial_EmitsExactlyOneFrame is the
// guard-rail for the suppression removal.
//
// The removed `if code == agent.CodeRateLimited { continue }` was defended as
// de-duplication. It was not de-duplicating anything — but the concern behind
// it is real and has a real owner: recordRateLimitDenial's doc comment records
// that an earlier "EventKindError + RateLimitPayload + EventKindRateLimit"
// dual-emit was removed as bus pollution. That removal must hold on its own
// merits, at the PRODUCER, rather than being propped up by a blanket drop in
// the forwarder that also destroyed unrelated provider refusals.
//
// So this drives a REAL internal SEC-26 denial — a genuine AgentLoop with
// MaxAgentLLMCallsPerHour=1, a non-privileged agent, two turns — through the
// REAL forwarder, and counts the live frames a client would see.
//
// BDD:
//
//	Given an agent whose internal LLM-call budget is 1/hour,
//	When a second turn is denied by that limiter,
//	Then the client receives exactly ONE rate_limit frame,
//	And ZERO error frames for that denial.
func TestEventForwarder_InternalRateLimitDenial_EmitsExactlyOneFrame(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// The denied (second) turn never reaches the provider — the limiter
	// rejects before the LLM call — so only the first turn consumes a script
	// entry. The second entry exists purely so an accidental provider call
	// would produce a distinguishable success rather than a confusing
	// "scenario exhausted" error.
	provider := testutil.NewScenario().
		WithText("first response — the budget slot is consumed").
		WithText("second response — must never be reached")

	// Must not be a core agent id: core agents are exempt from SEC-26 via
	// security.IsPrivilegedAgent, so a core agent would never be denied and
	// this test would pass vacuously.
	const customAgentID = "rate-frame-test-agent"
	const chatID = "chat-internal-rl"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{
				ID:   customAgentID,
				Name: "Rate Frame Test Agent",
				Type: config.AgentTypeCustom,
			}},
		},
		Sandbox: config.OmnipusSandboxConfig{
			RateLimits: config.OmnipusRateLimitsConfig{
				MaxAgentLLMCallsPerHour: 1,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, provider)

	sub := al.SubscribeEvents(256)
	h := makeMinimalHandlerWithAgentLoop(al)
	wc, ch := makeForwarderTestConn(256)
	done := make(chan struct{})
	go h.eventForwarder(wc, chatID, sub, done)

	ctx := context.Background()

	_, err1 := al.ProcessDirectWithChannel(ctx, "first message", "rl-frame-session-1", "webchat", chatID)
	require.NoError(t, err1, "SETUP: the first turn must succeed so the single budget slot is genuinely consumed")

	_, err2 := al.ProcessDirectWithChannel(ctx, "second message", "rl-frame-session-2", "webchat", chatID)
	require.Error(t, err2, "SETUP: the second turn must be denied by the internal limiter")
	require.Contains(t, err2.Error(), "rate limit",
		"SETUP: the denial must come from the SEC-26 limiter, not some unrelated failure")

	// Closing the subscription terminates the forwarder deterministically, so
	// the frame count below is complete rather than a timing snapshot.
	al.UnsubscribeEvents(sub.ID)
	<-done

	var rateLimitFrames, errorFrames int
	for len(ch) > 0 {
		var probe struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(<-ch, &probe))
		switch probe.Type {
		case string(generated.WsFrameTypeRateLimit):
			rateLimitFrames++
		case string(generated.WsFrameTypeError):
			errorFrames++
		}
	}

	assert.Equal(t, 1, rateLimitFrames,
		"an internal SEC-26 denial must produce EXACTLY ONE rate_limit frame")
	assert.Equal(t, 0, errorFrames,
		"an internal SEC-26 denial must produce NO error frame — recordRateLimitDenial emits a "+
			"single EventKindRateLimit and nothing re-emits the returned error as EventKindError. "+
			"A count above zero means the retired dual-emit is back, and removing the forwarder's "+
			"blanket rate_limited drop now lets it reach the user as a duplicate bubble")
}

// TestClaimStreamOwnership_StaleClaimIsForceReclaimed exercises the
// defense-in-depth backstop (streamOwnershipStaleAfter): an unreleased claim
// older than the staleness threshold degrades to "reclaimable by a new
// turn" rather than shadowing a chatID forever, protecting against any
// FUTURE bug in this family (not just the abandoned-turn path this wave
// fixed directly).
func TestClaimStreamOwnership_StaleClaimIsForceReclaimed(t *testing.T) {
	var owners sync.Map
	owners.Store("chat-stale", streamOwnerClaim{
		turnID:    "turn-old-leaked",
		claimedAt: time.Now().Add(-streamOwnershipStaleAfter - time.Minute),
	})

	ok := claimStreamOwnership(&owners, "chat-stale", "turn-new")
	assert.True(t, ok, "a claim older than streamOwnershipStaleAfter must be force-reclaimable by a new turn")

	actual, loaded := owners.Load("chat-stale")
	require.True(t, loaded)
	claim, ok := actual.(streamOwnerClaim)
	require.True(t, ok)
	assert.Equal(t, "turn-new", claim.turnID, "the stored claim must now belong to the reclaiming turn")
}

// TestClaimStreamOwnership_FreshClaimIsNotReclaimed proves the staleness
// backstop does not weaken the normal, fast-path ownership gate: a claim
// well within streamOwnershipStaleAfter held by a different turn must still
// deny a concurrent claimant, exactly like before the staleness feature was
// added.
func TestClaimStreamOwnership_FreshClaimIsNotReclaimed(t *testing.T) {
	var owners sync.Map
	owners.Store("chat-fresh", streamOwnerClaim{
		turnID:    "turn-current-owner",
		claimedAt: time.Now(),
	})

	ok := claimStreamOwnership(&owners, "chat-fresh", "turn-other")
	assert.False(t, ok, "a fresh (non-stale) claim held by a different turn must not be reclaimed")

	actual, loaded := owners.Load("chat-fresh")
	require.True(t, loaded)
	claim, claimOk := actual.(streamOwnerClaim)
	require.True(t, claimOk, "stored owner must be a streamOwnerClaim")
	assert.Equal(t, "turn-current-owner", claim.turnID, "the original owner's claim must be untouched")
}

// --- Suite 5: eventForwarder unit tests ---

// TestEventForwarder_ForwardsToolExecStart verifies that a ToolExecStartPayload event
// with a matching chatID is forwarded to the wsConn's sendCh as a "tool_call_start" frame.
// BDD: Given an eventForwarder goroutine subscribed to an EventBus with chatID "chat-1",
// When a ToolExecStartPayload event for chatID "chat-1" is emitted,
// Then a replayFrameDecoder with type "tool_call_start" appears on sendCh.
// Traces to: pkg/gateway/websocket.go — WSHandler.eventForwarder
func TestEventForwarder_ForwardsToolExecStart(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc := makeTestConn()
	chatID := "chat-1"

	eb := agent.NewEventBus()
	t.Cleanup(eb.Close)

	sub := eb.Subscribe(16)
	eventDone := make(chan struct{})

	go handler.eventForwarder(wc, chatID, sub, eventDone)

	eb.Emit(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: "call-xyz",
			ChatID:     chatID,
			Tool:       "read_file",
			Arguments:  map[string]any{"path": "/tmp/test.txt"},
		},
	})

	select {
	case raw := <-wc.sendCh:
		var f replayFrameDecoder
		require.NoError(t, json.Unmarshal(raw, &f), "sendCh frame must be valid JSON")
		assert.Equal(t, "tool_call_start", f.Type, "frame type must be tool_call_start")
		assert.Equal(t, "call-xyz", f.CallID, "CallID must match ToolCallID")
		assert.Equal(t, "read_file", f.Tool, "Tool must match payload Tool")
	case <-time.After(2 * time.Second):
		t.Fatal("no frame received on sendCh within 2s — eventForwarder did not forward the event")
	}

	// Unsubscribe to drain the goroutine cleanly.
	eb.Unsubscribe(sub.ID)
	select {
	case <-eventDone:
	case <-time.After(1 * time.Second):
		t.Fatal("eventForwarder goroutine did not exit after subscription closed")
	}
}

// TestEventForwarder_FiltersByChatID verifies that a ToolExecStartPayload event for a
// different chatID is NOT forwarded to the wsConn's sendCh.
// BDD: Given an eventForwarder subscribed with chatID "chat-1",
// When a ToolExecStartPayload event for chatID "chat-other" is emitted,
// Then no frame arrives on sendCh within the timeout.
// Traces to: pkg/gateway/websocket.go — WSHandler.eventForwarder chatID filter
func TestEventForwarder_FiltersByChatID(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc := makeTestConn()
	chatID := "chat-1"

	eb := agent.NewEventBus()
	t.Cleanup(eb.Close)

	sub := eb.Subscribe(16)
	eventDone := make(chan struct{})

	go handler.eventForwarder(wc, chatID, sub, eventDone)

	// Emit an event for a different chatID — must NOT be forwarded.
	eb.Emit(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: "call-other",
			ChatID:     "chat-other", // non-matching
			Tool:       "exec",
			Arguments:  map[string]any{"command": "ls"},
		},
	})

	select {
	case raw := <-wc.sendCh:
		t.Fatalf("unexpected frame on sendCh — eventForwarder must filter by chatID, got: %s", string(raw))
	case <-time.After(150 * time.Millisecond):
		// Correct — no frame should arrive for a non-matching chatID.
	}

	eb.Unsubscribe(sub.ID)
	select {
	case <-eventDone:
	case <-time.After(1 * time.Second):
		t.Fatal("eventForwarder goroutine did not exit after subscription closed")
	}
}

// TestEventForwarder_ForwardsTaskRunStatus verifies that an
// agent.EventKindTaskRunStatus event (ADR-050 §3.8, task-run-history-spec.md
// §3.8) is forwarded as an exact task_run_status frame, carrying
// occurrence_ms as a correctly-typed, non-truncated int64 value. This is the
// WS-side regression guard for the AsyncAPI int64 codegen drift fix
// (scripts/gen-asyncapi-go/main.go — `format: int64` now maps to Go int64,
// not int — see pkg/api/generated/asyncapi_types.gen.go's
// TaskRunStatusFrame.OccurrenceMs and pkg/gateway/websocket.go's
// eventForwarder, case agent.EventKindTaskRunStatus, which now assigns
// *p.OccurrenceMs directly with no narrowing cast).
//
// Unlike ToolExecStart (chatID-scoped), EventKindTaskRunStatus is broadcast
// unconditionally to every connection — mirroring EventKindTaskStatusChanged
// immediately above it in eventForwarder's switch — so this test does not
// need a matching chatID (the frame arrives regardless).
//
// BDD: Given an eventForwarder goroutine subscribed to an EventBus,
// When a TaskRunStatusPayload event carrying a ms-epoch OccurrenceMs
// (already > math.MaxInt32 for any date after 1970-01-25) is emitted,
// Then a task_run_status frame with the exact field values — including
// occurrence_ms round-tripping with no precision loss — appears on sendCh.
// Traces to: pkg/gateway/websocket.go — WSHandler.eventForwarder,
// case agent.EventKindTaskRunStatus.
func TestEventForwarder_ForwardsTaskRunStatus(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc := makeTestConn()
	chatID := "chat-1"

	eb := agent.NewEventBus()
	t.Cleanup(eb.Close)

	sub := eb.Subscribe(16)
	eventDone := make(chan struct{})

	go handler.eventForwarder(wc, chatID, sub, eventDone)

	// Deliberately > math.MaxInt32 (2147483647) — any real epoch-ms value for
	// a post-1970-01-25 date already is. Before the codegen fix, this would
	// have silently wrapped when narrowed into the (bugged) *int field on a
	// 32-bit target, or been truncated by the hand-written
	// `int(*p.OccurrenceMs)` cast this test also guards the removal of.
	occMs := int64(1_784_620_800_000)
	eb.Emit(agent.Event{
		Kind: agent.EventKindTaskRunStatus,
		Payload: agent.TaskRunStatusPayload{
			TaskID:       "task-abc",
			RunID:        "run-xyz",
			OccurrenceMs: &occMs,
			Status:       "done",
		},
	})

	select {
	case raw := <-wc.sendCh:
		// Assert the exact frame shape via a generic map first — proves the
		// wire bytes themselves, not just Go-side decoding, carry the right
		// values (and nothing extra).
		var frame map[string]any
		require.NoError(t, json.Unmarshal(raw, &frame), "sendCh frame must be valid JSON")
		assert.Equal(t, "task_run_status", frame["type"])
		assert.Equal(t, "task-abc", frame["task_id"])
		assert.Equal(t, "run-xyz", frame["run_id"])
		assert.Equal(t, "done", frame["status"])
		require.Contains(t, frame, "occurrence_ms")
		// encoding/json decodes a JSON number into map[string]any as float64;
		// float64 has 53 bits of mantissa, comfortably exact for a ms-epoch
		// value, so an exact match here still catches a truncation bug (which
		// would produce a small/wrapped/negative value) while confirming the
		// real wire value round-trips correctly.
		occFloat, ok := frame["occurrence_ms"].(float64)
		require.True(t, ok, "occurrence_ms must be a JSON number, not null/string")
		assert.Equal(t, float64(occMs), occFloat,
			"occurrence_ms must equal the emitted value exactly — no truncation")

		// Decode into the generated wire type directly: this is the type-level
		// regression guard — TaskRunStatusFrame.OccurrenceMs is *int64 (see
		// TestContract_TaskRunStatusFrame_OccurrenceMsIsInt64Type in
		// pkg/api/generated/contract_test.go for the reflect-based pin of the
		// same fact), so this line would fail to compile if the codegen ever
		// regressed back to *int.
		var typed generated.TaskRunStatusFrame
		require.NoError(t, json.Unmarshal(raw, &typed))
		require.NotNil(t, typed.OccurrenceMs, "OccurrenceMs must not be nil")
		assert.Equal(t, occMs, *typed.OccurrenceMs,
			"occurrence_ms must round-trip exactly through *int64 — no 32-bit truncation")
	case <-time.After(2 * time.Second):
		t.Fatal("no frame received on sendCh within 2s — eventForwarder did not forward the task_run_status event")
	}

	eb.Unsubscribe(sub.ID)
	select {
	case <-eventDone:
	case <-time.After(1 * time.Second):
		t.Fatal("eventForwarder goroutine did not exit after subscription closed")
	}
}

// TestWritePumpEnforcesWriteDeadline_TextMessage proves that writePump's
// TextMessage write call (wc.conn.WriteMessage(websocket.TextMessage, msg))
// is bounded by wsWriteWait rather than blocking forever when the client
// stops reading.
//
// BDD:
//
//	Given a WS server connection whose client never reads again,
//	When writePump attempts its first write over the (unbuffered, nobody-
//	  reading) net.Pipe() transport,
//	Then the blocked WriteMessage call returns a deadline error within
//	  wsWriteWait (+ scheduling slack), and writePump's goroutine exits —
//	  it does not hang indefinitely.
//
// Traces to: pkg/gateway/websocket.go writePump (TextMessage branch).
func TestWritePumpEnforcesWriteDeadline_TextMessage(t *testing.T) {
	wc, wpDone := setupBackpressureWS(t)

	payload := make([]byte, 256*1024) // 256 KiB text frame, reused every send

	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			select {
			case wc.sendCh <- payload:
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		elapsed := time.Since(start)
		assert.LessOrEqualf(t, elapsed, 15*time.Second,
			"writePump must return within wsWriteWait(%s)+slack once the client stops reading, took %s — "+
				"a write deadline that isn't firing means the single writer goroutine can stall forever",
			wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, 5*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the test "+
				"isn't exercising real backpressure (the pipe write never actually blocked) rather than confirming the fix",
			elapsed, wsWriteWait)
	case <-time.After(25 * time.Second):
		t.Fatal("writePump did not return within 25s of a stalled client on the TextMessage path — " +
			"the write deadline is not being enforced (regression: missing SetWriteDeadline before " +
			"wc.conn.WriteMessage(websocket.TextMessage, msg) in writePump)")
	}
	<-feederDone
}

// TestWritePumpEnforcesWriteDeadline_Ping proves that writePump's
// PingMessage write call (wc.conn.WriteMessage(websocket.PingMessage, nil),
// triggered by the nil sentinel on wc.sendCh) is bounded by wsWriteWait
// rather than blocking forever when the client stops reading.
//
// This is the call site most directly implicated in the production bug:
// the keepalive ping is what has to keep firing every wsPingPeriod to beat
// the reverse proxy's idle timeout, and it is exactly the frame that got
// silently starved when an earlier write on the same single writer
// goroutine blocked forever.
//
// setupBackpressureWS's net.Pipe() transport has no OS buffer to overflow,
// so the FIRST ping write attempt blocks immediately — the elapsed time is
// governed purely by wsWriteWait's own deadline, not by how many 2-byte
// ping frames it takes to organically overflow a platform-specific,
// best-effort-shrunk OS buffer (the previous, flakier mechanism — see the
// package doc comment above for the full root-cause account). The feeder
// below still enqueues PURE ping sentinels (no payload mixed in), so the
// write that ultimately blocks and times out is still, specifically,
// writePump's PingMessage branch.
//
// Traces to: pkg/gateway/websocket.go writePump (PingMessage branch).
func TestWritePumpEnforcesWriteDeadline_Ping(t *testing.T) {
	wc, wpDone := setupBackpressureWS(t)

	start := time.Now()
	feederDone := make(chan struct{})
	go func() {
		defer close(feederDone)
		for {
			select {
			case wc.sendCh <- wsPingMsg: // nil sentinel -> PingMessage write in writePump
			case <-wpDone:
				return
			}
		}
	}()

	select {
	case <-wpDone:
		elapsed := time.Since(start)
		assert.LessOrEqualf(t, elapsed, wsWriteWait+15*time.Second,
			"writePump must return within wsWriteWait(%s)+slack once the unbuffered pipe makes the "+
				"first ping write block, took %s", wsWriteWait, elapsed)
		assert.GreaterOrEqualf(t, elapsed, wsWriteWait-3*time.Second,
			"writePump returned after only %s — expected it to actually block until close to "+
				"wsWriteWait(%s) before the deadline fires; a near-instant return suggests the pipe "+
				"write never actually blocked before the feeder started", elapsed, wsWriteWait)
	case <-time.After(wsWriteWait + 30*time.Second):
		t.Fatal("writePump did not return within wsWriteWait+30s while flooded with ping frames against " +
			"a non-reading client over an unbuffered pipe — the ping write deadline is not being enforced " +
			"(regression: missing SetWriteDeadline before wc.conn.WriteMessage(websocket.PingMessage, " +
			"nil) in writePump)")
	}
	<-feederDone
}

// TestGetStreamer_WebchatAlwaysNonNil proves FR-003/S-05: a webchat
// GetStreamer call with a real session id but ZERO bound connections still
// returns a non-nil streamer and ok=true.
func TestGetStreamer_WebchatAlwaysNonNil(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	// Deliberately do NOT bind any connection to meta.ID — this is the
	// "only viewer disconnected mid-turn" / "keeper follow-up, no viewer at
	// all" scenario.
	streamer, ok := handler.GetStreamer(context.Background(), "webchat", "chat-no-conn", meta.ID)
	require.True(t, ok, "GetStreamer must return true for webchat even with zero bound connections")
	require.NotNil(t, streamer, "GetStreamer must return a non-nil streamer")
}

// TestGetStreamer_NonWebchatChannelReturnsFalse proves the channel != "webchat"
// early-return is unchanged.
func TestGetStreamer_NonWebchatChannelReturnsFalse(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	streamer, ok := handler.GetStreamer(context.Background(), "telegram", "chat-x", "session-x")
	assert.False(t, ok)
	assert.Nil(t, streamer)
}

// TestGetStreamer_EmptySessionIDAndNoBinding_ReturnsFalse proves that a
// caller supplying neither a session id nor a chatID with an existing
// binding still gets a clean "no streamer" result rather than a panic or a
// streamer with no usable session.
func TestGetStreamer_EmptySessionIDAndNoBinding_ReturnsFalse(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	streamer, ok := handler.GetStreamer(context.Background(), "webchat", "chat-unbound", "")
	assert.False(t, ok)
	assert.Nil(t, streamer)
}

// TestGetStreamer_RegistersLiveStreamer proves GetStreamer registers the
// streamer in h.liveStreamers keyed by session id (ADR-082 D3/D4's
// prerequisite for catch-up snapshot and active_turn reporting).
func TestGetStreamer_RegistersLiveStreamer(t *testing.T) {
	handler, _, al := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	streamer, ok := handler.GetStreamer(context.Background(), "webchat", "chat-reg", meta.ID)
	require.True(t, ok)

	handler.mu.Lock()
	registered, exists := handler.liveStreamers[meta.ID]
	handler.mu.Unlock()
	require.True(t, exists, "GetStreamer must register the streamer in liveStreamers")
	assert.Same(t, streamer, registered)
}
