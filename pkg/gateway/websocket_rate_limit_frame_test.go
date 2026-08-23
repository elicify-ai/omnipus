// websocket_rate_limit_frame_test.go — coverage for eventForwarder's
// `case agent.EventKindRateLimit` arm, and for the invariant that makes the
// sibling EventKindError arm safe to forward unconditionally.
//
// Context. The forwarder used to drop every EventKindError carrying
// code=="rate_limited", on the stated grounds that the dedicated RateLimitFrame
// emitted by THIS arm was "authoritative for that class". That reasoning
// conflated two mechanisms that share a code name but not a producer, and the
// arm doing the supposed covering had no test at all:
//
//   - EventKindRateLimit (this arm) is produced ONLY by
//     AgentLoop.recordRateLimitDenial (pkg/agent/loop.go), from two call sites
//     both gated on Omnipus's own SEC-26 limiter being configured. It means
//     "Omnipus denied this".
//   - An upstream provider 429 never reaches that function. It surfaces as
//     EventKindError from runTurn's LLM-error block. It means "the provider
//     denied this".
//
// So the suppression removed the provider signal and replaced it with nothing.
// It is gone (see websocket.go's EventKindError arm). These tests pin both
// halves of the resulting contract: this arm still renders the internal
// limiter's dedicated frame correctly, and an internal denial still produces
// EXACTLY ONE live frame — the dual-emit that recordRateLimitDenial's doc
// comment records as removed bus pollution must not creep back in through the
// unsuppressed error arm.

package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

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
