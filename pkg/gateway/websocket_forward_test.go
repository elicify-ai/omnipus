// websocket_forward_test.go: tests for forward agent events to the client as frames

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// --- moved from websocket_pump.go tests 2026-09-15 ---

// --- moved from websocket.go tests 2026-09-15 ---

// TestEventForwarder_SessionBasedFallback_ReachesReattachedConnection is the
// direct regression test: a SubTurnEndPayload carrying a STALE ChatID (as a
// background delegate dispatched before a reload would) must still reach
// this connection when its SessionID matches the session this connection
// has attached to — even though its ChatID does not match this connection's
// own chatID or taskChatIDs alias at all.
func TestEventForwarder_SessionBasedFallback_ReachesReattachedConnection(t *testing.T) {
	h := makeMinimalHandler()

	const newChatID = "webchat:new-connection-after-reload"
	const staleChatID = "webchat:stale-connection-before-reload"
	const durableSessionID = "session-durable-abc123"

	// Simulate handleAttachSession having reattached this (new) connection
	// to the persisted session.
	// #823 Lane A: driven through h.hubSyncTap. This kind is produced by the
	// session hub (sync tap), not the per-connection forwarder, so the old
	// runForwarder+bus.Emit form of this test could no longer observe it.
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, newChatID, durableSessionID, wc)

	// A background delegate's real completion event, carrying the STALE
	// pre-reload chatID (its turn was dispatched under the OLD connection)
	// but the SAME durable session_id.
	h.hubSyncTap(agent.Event{
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
	h := makeMinimalHandler()

	const thisConnChatID = "webchat:this-connection"
	// #823 Lane A: driven through h.hubSyncTap. This kind is produced by the
	// session hub (sync tap), not the per-connection forwarder, so the old
	// runForwarder+bus.Emit form of this test could no longer observe it.
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, thisConnChatID, "session-A", wc)

	h.hubSyncTap(agent.Event{
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

	assert.Empty(t, ch,
		"an event belonging to a different session must not be forwarded to this connection")
}

// TestEventForwarder_ErrorFrame_ReachesReattachedConnection pins the D5-class
// hole the EventKindError arm kept after every other live event moved to
// matchesEvent: a workspace refuse / provider 429 stamped the originating
// tab's chatID, so a second tab (or a reload) attached to the same session
// dropped the typed error. token+done still fanned out, and the error
// bubble never appeared.
// #823 catch-up redesign: migrated from runForwarder+bus.Emit to
// h.hubSyncTap directly — EventKindError now goes through the session hub
// (websocket_forward_hub.go's hubError, ported verbatim from the retired
// onError), exactly once per event, delivered to every connection
// resolveSessionConnsLocked finds for the payload's own SessionID — which is
// exactly the "reaches a reattached connection by session, not by stale
// chatID" guarantee this test pins, now enforced by the hub's delivery
// resolution instead of eventForwarder's per-connection matchesEvent.
func TestEventForwarder_ErrorFrame_ReachesReattachedConnection(t *testing.T) {
	h := makeMinimalHandler()

	const newChatID = "webchat:new-connection-after-reload"
	const staleChatID = "webchat:stale-connection-before-reload"
	const durableSessionID = "session-durable-error-abc"

	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, newChatID, durableSessionID, wc)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindError,
		Payload: agent.ErrorPayload{
			Stage:     "workspace",
			Code:      string(agent.CodeAgentNotConfigured),
			Message:   agent.UserMessageForCode(agent.CodeAgentNotConfigured),
			ChatID:    staleChatID,
			SessionID: durableSessionID,
		},
	})

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
	h := makeMinimalHandler()

	const thisConnChatID = "webchat:this-connection"
	// #823 Lane A: driven through h.hubSyncTap. This kind is produced by the
	// session hub (sync tap), not the per-connection forwarder, so the old
	// runForwarder+bus.Emit form of this test could no longer observe it.
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, thisConnChatID, "session-A", wc)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindError,
		Payload: agent.ErrorPayload{
			Stage:     "workspace",
			Code:      string(agent.CodeAgentNotConfigured),
			Message:   agent.UserMessageForCode(agent.CodeAgentNotConfigured),
			ChatID:    "webchat:unrelated-connection",
			SessionID: "session-B",
		},
	})

	assert.Empty(t, ch,
		"a typed error belonging to a different session must not be forwarded to this connection")
}

// TestEventForwarder_RateLimitFrame_ReachesReattachedConnection is the
// internal-limiter twin of TestEventForwarder_ErrorFrame_ReachesReattachedConnection.
// EventKindRateLimit used to match on ChatID only, so a second tab (or a
// reload) attached to the same session never saw Omnipus's own SEC-26 denial.
// #823 catch-up redesign: migrated to h.hubSyncTap directly —
// EventKindRateLimit (SESSION scope) now goes through the session hub
// (websocket_forward_hub.go's hubRateLimit), see
// TestEventForwarder_ErrorFrame_ReachesReattachedConnection's comment above
// for the full rationale.
func TestEventForwarder_RateLimitFrame_ReachesReattachedConnection(t *testing.T) {
	h := makeMinimalHandler()

	const newChatID = "webchat:new-connection-after-reload"
	const staleChatID = "webchat:stale-connection-before-reload"
	const durableSessionID = "session-durable-ratelimit-abc"

	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, newChatID, durableSessionID, wc)

	h.hubSyncTap(agent.Event{
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
	h := makeMinimalHandler()

	const thisConnChatID = "webchat:this-connection"
	// #823 Lane A: driven through h.hubSyncTap. This kind is produced by the
	// session hub (sync tap), not the per-connection forwarder, so the old
	// runForwarder+bus.Emit form of this test could no longer observe it.
	wc, ch := makeForwarderTestConn(64)
	bindTestConnToSession(h, thisConnChatID, "session-A", wc)

	h.hubSyncTap(agent.Event{
		Kind: agent.EventKindRateLimit,
		Payload: agent.RateLimitPayload{
			Scope:      "agent",
			Resource:   "llm_call",
			PolicyRule: "max_agent_llm_calls_per_hour",
			ChatID:     "webchat:unrelated-connection",
			SessionID:  "session-B",
		},
	})

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

	// #823 catch-up redesign: migrated to h.hubSyncTap directly (see
	// TestEventForwarder_ErrorFrame_ReachesReattachedConnection's comment).
	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(4)
	bindTestConnToSession(h, "chat-1", "chat-1", wc)

	h.hubSyncTap(agent.Event{Kind: agent.EventKindError, Payload: agent.ErrorPayload{
		Stage:         "llm",
		ChatID:        "chat-1",
		Code:          string(translated.Code),
		Message:       translated.Message,
		ProviderError: pe,
	}})

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

	// #823 catch-up redesign: migrated to h.hubSyncTap directly (see
	// TestEventForwarder_ErrorFrame_ReachesReattachedConnection's comment).
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			bindTestConnToSession(h, "chat-1", "chat-1", wc)

			h.hubSyncTap(agent.Event{Kind: agent.EventKindError, Payload: tc.payload})

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

	// #823 catch-up redesign: migrated to h.hubSyncTap directly (see
	// TestEventForwarder_ErrorFrame_ReachesReattachedConnection's comment).
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			bindTestConnToSession(h, "chat-1", "chat-1", wc)

			h.hubSyncTap(agent.Event{Kind: agent.EventKindError, Payload: tc.payload})

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

	// #823 catch-up redesign: this test spans BOTH delivery paths — an
	// agent-scoped (session) denial now goes through the hub tap
	// (hubRateLimit), while a global-scope denial stays on the legacy
	// per-connection forwarder (onRateLimit, modified to handle only
	// global scope). A real *WSHandler built via newWSHandler gets both
	// automatically (EventBus.Emit calls the sync tap before its
	// subscriber fan-out); this test drives a bare EventBus directly, so
	// it must call h.hubSyncTap itself, in the same before-fan-out order,
	// to reproduce that.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evBus := agent.NewEventBus()
			defer evBus.Close()
			h := makeMinimalHandler()
			wc, ch := makeForwarderTestConn(4)
			bindTestConnToSession(h, "chat-1", "chat-1", wc)
			done := runForwarder(h, wc, "chat-1", evBus)

			h.hubSyncTap(agent.Event{Kind: agent.EventKindRateLimit, Payload: tc.payload})
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
	h := makeMinimalHandler()
	h.agentLoop = al
	wc, ch := makeForwarderTestConn(256)
	// #823 catch-up redesign: install the hub sync tap on this AgentLoop
	// like newWSHandler would in production. A real routingSessionID is an
	// internally-generated id (turn.go's ts.routingSessionID =
	// session.RoutingSessionID(ts.transcriptSessionID)), not the literal
	// "rl-frame-session-2" sessionKey passed to ProcessDirectWithChannel
	// below — rather than hardcode a guess at that internal id, wrap the
	// tap to bind this connection to whatever SessionID a RateLimitPayload
	// ACTUALLY carries, the instant it arrives and before hubSyncTap
	// resolves delivery targets. This is the same "bind by session" contract
	// production uses (a real WSHandler binds via handleAttachSession/
	// handleChatMessage as sessions are minted) — this test just does not
	// have a full webchat handshake to drive that binding itself.
	// Also bind chatID to itself as a fallback: hubRateLimit
	// (websocket_forward_hub.go) falls back to hubResolveSessionIDForChat
	// when p.SessionID is empty, and ProcessDirectWithChannel's dispatch
	// path may leave routingSessionID empty for this non-webchat entry
	// point — this covers that case too, so the test is correct regardless
	// of which one the real payload actually carries.
	bindTestConnToSession(h, chatID, chatID, wc)
	al.SetEventSyncTap(func(evt agent.Event) {
		if p, ok := evt.Payload.(agent.RateLimitPayload); ok && p.SessionID != "" {
			bindTestConnToSession(h, chatID, p.SessionID, wc)
		}
		h.hubSyncTap(evt)
	})
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

// --- Suite 5: eventForwarder unit tests ---

// TestEventForwarder_ForwardsToolExecStart verifies that a ToolExecStartPayload event
// with a matching chatID is forwarded to the wsConn's sendCh as a "tool_call_start" frame.
// BDD: Given an eventForwarder goroutine subscribed to an EventBus with chatID "chat-1",
// When a ToolExecStartPayload event for chatID "chat-1" is emitted,
// Then a replayFrameDecoder with type "tool_call_start" appears on sendCh.
// Traces to: pkg/gateway/websocket.go — WSHandler.eventForwarder
// #823 catch-up redesign: migrated to handler.hubSyncTap directly —
// EventKindToolExecStart now goes through the session hub
// (websocket_forward_hub.go's hubToolExecStart), exactly once per event.
// The standalone `eb := agent.NewEventBus()` this test used to drive is a
// DIFFERENT EventBus instance than handler.agentLoop's own internal one
// (the one newTestWSHandler's newWSHandler call installs the tap on via
// SetEventSyncTap), so emitting on it never reached the tap either way —
// calling hubSyncTap directly is the correct migration, not a workaround.
func TestEventForwarder_ForwardsToolExecStart(t *testing.T) {
	handler, _, _ := newTestWSHandler(t)
	t.Cleanup(handler.Wait)

	wc := makeTestConn()
	chatID := "chat-1"
	bindTestConnToSession(handler, chatID, chatID, wc)

	handler.hubSyncTap(agent.Event{
		Kind: agent.EventKindToolExecStart,
		Payload: agent.ToolExecStartPayload{
			ToolCallID: "call-xyz",
			ChatID:     chatID,
			SessionID:  chatID,
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
		t.Fatal("no frame received on sendCh within 2s — hubSyncTap did not forward the event")
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
