// websocket_provider_refusal_test.go — an upstream provider refusal must
// reach the user.
//
// The live defect this pins: a provider returned HTTP 429 and the user saw an
// agent that produced no words at all — the turn opened, produced nothing, and
// closed REPORTING SUCCESS. No error frame, no warning, nothing to retry from.
// It reads as an agent that ignored you.
//
// Two independent mechanisms combined to produce that silence:
//
//  1. WSHandler.eventForwarder's `case agent.EventKindError` arm ended with an
//     unconditional `if code == agent.CodeRateLimited { continue }`, justified
//     by "the dedicated RateLimitFrame is authoritative for that class". That
//     justification does not hold for a PROVIDER refusal: EventKindRateLimit
//     has exactly one producer (AgentLoop.recordRateLimitDenial, pkg/agent/
//     loop.go), reachable only when Omnipus's OWN internal SEC-26 limiter is
//     configured and denies. A provider's 429 travels a completely different
//     path — runTurn's LLM-error block emits EventKindError with
//     Code: "rate_limited" — so the suppression dropped the only frame the
//     user would ever have seen, with nothing replacing it. The two mechanisms
//     share a code NAME but not a producer.
//
//  2. The `done` frame is emitted by wsStreamer.Finalize, reached via the
//     unconditional `defer ts.finalizeStreamer(ctx)` in runTurn. That defer
//     fires on EVERY return path including the LLM-error early return — and
//     markTurnFailed() was not called on that path, so the done frame went out
//     with DoneStats.TurnFailed absent, indistinguishable from success.
//
// These tests drive a scripted 429 through a REAL root webchat turn — real
// httptest server, real gorilla WebSocket client, real AgentLoop.Run goroutine,
// real wsStreamer, and the same outbound relay the channel Manager performs in
// production (gateway.go's RegisterChannel("webchat", wch) →
// Manager.dispatchOutbound → webchatChannel.Send) — and assert on THE FRAMES A
// CLIENT ACTUALLY RECEIVES. Nothing else in this codebase does that for an
// error turn.

package gateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// refusalTestModel is the model name the refusing provider reports and the
// config pins, so the agent loop resolves this provider for every call.
const refusalTestModel = "refusing-test-model"

// rateLimitedStreamingProvider implements providers.LLMProvider AND
// providers.StreamingProvider and refuses every call with a real provider-shaped
// HTTP 429 — the exact error value pkg/providers/common.HandleErrorResponse
// builds from an upstream 429 response, so agent.errorToProviderError extracts
// Status=429 from the wrapped chain and TranslateLLMError classifies it as
// CodeRateLimited without any substring guessing.
//
// It MUST implement StreamingProvider: only the streaming branch of the LLM
// call (pkg/agent/loop.go, the `sp.ChatStream(...)` block) calls
// bus.GetStreamer and then ts.setLastStreamer(streamer) — unconditionally,
// including on the error return. That assignment is what makes runTurn's
// deferred finalizeStreamer emit a done frame at all, and therefore what makes
// the "done frame claims success" half of this defect reachable. A
// non-streaming mock would leave ts.lastStreamer nil, emit no done frame, and
// silently test nothing.
type rateLimitedStreamingProvider struct {
	calls atomic.Int32
}

// refusal returns a fresh error value per call — the agent loop may wrap it,
// and sharing one instance across calls would alias whatever a wrapper mutates.
func (p *rateLimitedStreamingProvider) refusal() error {
	const body = `{"error":{"message":"Rate limit exceeded, please retry later.","type":"rate_limit_error"}}`
	return &common.ProviderError{
		Status:      429,
		Body:        body,
		BodyPreview: body,
		ContentType: "application/json",
	}
}

func (p *rateLimitedStreamingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return nil, p.refusal()
}

func (p *rateLimitedStreamingProvider) ChatStream(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
	_ func(accumulated string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	// No onChunk call: the provider refused before emitting a single token.
	// This is what makes the turn produce "no words at all".
	return nil, p.refusal()
}

func (p *rateLimitedStreamingProvider) GetDefaultModel() string { return refusalTestModel }

// newRefusingTestWSHandler mirrors newStreamingTestWSHandler
// (websocket_multisession_test.go) — same config shape, same Run goroutine,
// same SetStreamDelegate — with two deliberate differences:
//
//   - the provider refuses every call with a 429, and
//   - the webchatChannel is wired up AND an outbound relay goroutine is started,
//     reproducing what production's channel Manager does
//     (gateway.go: RegisterChannel("webchat", wch); manager.go:
//     dispatchOutbound routes bus.OutboundChan() by msg.Channel to that
//     channel's Send). Without the relay, the second designed path — runTurn's
//     error → sessionWorker.processTurn's TranslateTurnError(err).Message →
//     publishResponseIfNeeded → PublishOutbound → webchatChannel.Send → token +
//     done — would be silently absent from the test, and the test could not
//     answer whether the user ever gets an assistant bubble.
func newRefusingTestWSHandler(t *testing.T) (*WSHandler, *rateLimitedStreamingProvider) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")

	tmpDir := t.TempDir()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Model: refusalTestModel},
				MaxTokens:    4096,
			},
			// A real chat-target agent: the "main" sentinel is gone (ADR-064),
			// so handleChatMessage rejects a message frame with no agent_id
			// when no default can be resolved.
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &rateLimitedStreamingProvider{}
	al := mustAgentLoop(t, cfg, msgBus, provider)

	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		if err := al.Run(ctx); err != nil && err != context.Canceled {
			t.Logf("agent loop Run exited: %v", err)
		}
	}()

	handler := newWSHandler(msgBus, al, "")
	msgBus.SetStreamDelegate(handler)

	// Production parity: the webchatChannel and the wsHandler share a
	// reference so streaming can suppress a duplicate Send (gateway.go).
	wch := newWebchatChannel(handler)
	handler.webchatCh = wch

	// Production parity: stand in for Manager.dispatchOutbound.
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-msgBus.OutboundChan():
				if !ok {
					return
				}
				if msg.Channel != "webchat" {
					continue
				}
				if err := wch.Send(ctx, msg); err != nil {
					t.Logf("webchat relay Send: %v", err)
				}
			}
		}
	}()

	t.Cleanup(func() {
		cancel()
		for _, ch := range []chan struct{}{runDone, relayDone} {
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
				t.Logf("background goroutine did not exit within 2s of cancel")
			}
		}
	})

	// Give Run time to start reading from the bus before any publish.
	time.Sleep(20 * time.Millisecond)

	return handler, provider
}

// collectedFrame is one server→client frame captured off the real socket,
// retaining the raw bytes so a per-type generated decoder can be applied.
type collectedFrame struct {
	Type string
	Raw  []byte
}

// collectWSFrames reads every frame the server sends until the socket has been
// quiet for quietFor, or until the hard deadline elapses. Unlike
// drainUntilSessionDone it does NOT stop at the first done/error — the whole
// point of these tests is the COMPLETE frame list a client observes, including
// anything that arrives after the first terminal-looking frame.
func collectWSFrames(
	t *testing.T,
	conn *websocket.Conn,
	quietFor time.Duration,
	hardDeadline time.Duration,
) []collectedFrame {
	t.Helper()
	var frames []collectedFrame
	stopAt := time.Now().Add(hardDeadline)
	for {
		readBy := time.Now().Add(quietFor)
		if readBy.After(stopAt) {
			readBy = stopAt
		}
		if !readBy.After(time.Now()) {
			return frames
		}
		if err := conn.SetReadDeadline(readBy); err != nil {
			t.Fatalf("SetReadDeadline: %v", err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// Read deadline hit (socket quiet) or the peer closed: done.
			return frames
		}
		var probe struct {
			Type string `json:"type"`
		}
		if jsonErr := json.Unmarshal(raw, &probe); jsonErr != nil {
			t.Fatalf("server sent a frame that is not valid JSON: %v (%s)", jsonErr, string(raw))
		}
		cp := make([]byte, len(raw))
		copy(cp, raw)
		frames = append(frames, collectedFrame{Type: probe.Type, Raw: cp})
	}
}

// frameTypes renders the observed frame sequence for failure messages — the
// single most useful diagnostic when one of these assertions trips.
func collectedFrameTypes(frames []collectedFrame) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.Type)
	}
	return out
}

// runRefusedWebchatTurn drives one real webchat turn that the provider refuses
// with a 429 and returns every frame the client received.
func runRefusedWebchatTurn(t *testing.T) []collectedFrame {
	t.Helper()

	handler, provider := newRefusingTestWSHandler(t)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	t.Cleanup(handler.Wait)

	conn := dialTestWS(t, srv)
	t.Cleanup(func() { _ = conn.Close() })
	sendWSAuthFrameDevMode(t, conn)

	msg := wsClientFrameTestHelper{Type: "message", Content: "what is the weather today?"}
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))

	frames := collectWSFrames(t, conn, 2*time.Second, 25*time.Second)

	require.Positive(t, provider.calls.Load(),
		"fixture check: the provider was never called, so no refusal was ever produced — "+
			"the turn did not reach the LLM call at all (routing/workspace refusal?)")
	require.NotEmpty(t, frames, "fixture check: the client received no frames at all")
	t.Logf("frames received by the client: %v", collectedFrameTypes(frames))
	// Dump the raw frames too: the ORDER and the session_id tagging of this
	// exact sequence are what distinguish "the backend dropped the user's only
	// signal" from "the backend delivered it and the SPA mishandled it". Both
	// have been live hypotheses for this defect; a type-only list cannot tell
	// them apart, and reconstructing it by hand from logs is exactly the step
	// that got skipped when this was first investigated.
	for i, f := range frames {
		t.Logf("  frame[%d] %s: %s", i, f.Type, string(f.Raw))
	}

	return frames
}

// TestWS_ProviderRateLimitRefusal_ReachesTheUser is the primary regression for
// half 1: the forwarder must deliver a provider refusal as a typed error frame.
//
// BDD:
//
//	Given a real webchat session whose provider refuses with HTTP 429,
//	When the user sends a message,
//	Then the client receives an `error` frame carrying code "rate_limited",
//	And that error frame carries a non-empty user-facing message.
//
// Before the fix this failed: eventForwarder's
// `if code == agent.CodeRateLimited { continue }` dropped the frame, and no
// rate_limit frame replaced it (EventKindRateLimit's only producer,
// recordRateLimitDenial, never runs for a provider refusal).
func TestWS_ProviderRateLimitRefusal_ReachesTheUser(t *testing.T) {
	frames := runRefusedWebchatTurn(t)

	var errorFrames []generated.ErrorFrame
	for _, f := range frames {
		if f.Type != string(generated.WsFrameTypeError) {
			continue
		}
		var ef generated.ErrorFrame
		require.NoError(t, json.Unmarshal(f.Raw, &ef), "error frame must decode as generated.ErrorFrame")
		errorFrames = append(errorFrames, ef)
	}

	require.NotEmpty(t, errorFrames,
		"a provider refusal must reach the user as an error frame — got frames %v. "+
			"eventForwarder suppressed code==rate_limited on the assumption that the dedicated "+
			"RateLimitFrame covers it, but EventKindRateLimit's only producer is the INTERNAL "+
			"SEC-26 limiter (recordRateLimitDenial); an upstream 429 never produces one, so the "+
			"suppression drops the user's only signal",
		collectedFrameTypes(frames))

	var sawRateLimited bool
	for _, ef := range errorFrames {
		if ef.Payload == nil {
			continue
		}
		if ef.Payload.LlmError.Code == string(agent.CodeRateLimited) {
			sawRateLimited = true
			assert.NotEmpty(t, ef.Payload.LlmError.Message,
				"the rate-limit error frame must carry a user-facing message, not an empty string")
			// Not a nicety — a delivery precondition. The SPA validates every
			// inbound frame against the strict generated Zod schema at the WS
			// edge (src/lib/ws.ts) BEFORE the reducer sees it, and ErrorFrame
			// declares top-level `message` as required with min length 1
			// (contracts/asyncapi.yaml → src/lib/api/generated/schemas.ts).
			// An ErrorFrame carrying only `payload.llm_error` and an empty
			// `message` is dropped silently at that boundary — which would
			// reproduce this very defect through a different door: the fix
			// would emit a frame, and the user would still see nothing.
			assert.NotEmpty(t, ef.Message,
				"the error frame's top-level message must be non-empty or the SPA's strict "+
					"schema validation drops the frame at the WS edge before rendering it")
			assert.True(t, ef.Payload.LlmError.Retryable,
				"a 429 is retryable — the client needs that to offer a retry affordance")
		}
	}
	assert.True(t, sawRateLimited,
		"the delivered error frame must carry code %q so the SPA renders the rate-limit "+
			"treatment rather than a generic failure; got frames %v",
		agent.CodeRateLimited, collectedFrameTypes(frames))

	// No rate_limit frame is expected here: this refusal came from the
	// PROVIDER, not from Omnipus's own SEC-26 limiter. Pinning its absence is
	// what proves the two mechanisms are genuinely distinct producers, and
	// therefore that suppressing the error frame left nothing behind.
	for _, f := range frames {
		assert.NotEqual(t, string(generated.WsFrameTypeRateLimit), f.Type,
			"a provider refusal must NOT synthesize the internal limiter's dedicated "+
				"rate_limit frame — that frame means 'Omnipus denied this', which is a "+
				"different fact with a different remedy")
	}
}

// TestWS_ProviderRateLimitRefusal_DoneFrameDoesNotClaimSuccess is the primary
// regression for half 2: an unexplained silence must not read as success.
//
// BDD:
//
//	Given a real webchat session whose provider refuses with HTTP 429,
//	When the user sends a message and the turn ends,
//	Then the done frame emitted by the streamer carries stats.turn_failed = true.
//
// Before the fix this failed: runTurn's LLM-error early return never called
// markTurnFailed(), while the unconditional `defer ts.finalizeStreamer(ctx)`
// still fired — so DoneStats.TurnFailed was absent, which is exactly what a
// successful turn looks like on the wire.
func TestWS_ProviderRateLimitRefusal_DoneFrameDoesNotClaimSuccess(t *testing.T) {
	frames := runRefusedWebchatTurn(t)

	var firstDone *generated.DoneFrame
	for _, f := range frames {
		if f.Type != string(generated.WsFrameTypeDone) {
			continue
		}
		var df generated.DoneFrame
		require.NoError(t, json.Unmarshal(f.Raw, &df), "done frame must decode as generated.DoneFrame")
		firstDone = &df
		break
	}

	require.NotNil(t, firstDone,
		"the turn must produce a done frame (the streamer's finalize) — got frames %v",
		collectedFrameTypes(frames))
	require.NotNil(t, firstDone.Stats,
		"the streamer's done frame must carry stats — got frames %v", collectedFrameTypes(frames))
	require.NotNil(t, firstDone.Stats.TurnFailed,
		"a turn that ended in a provider refusal must NOT emit a done frame with the failure "+
			"flag absent — absent is indistinguishable from success, which is precisely how a "+
			"429 came to read as 'the agent ignored you'. Frames: %v",
		collectedFrameTypes(frames))
	assert.True(t, *firstDone.Stats.TurnFailed,
		"stats.turn_failed must be true for a turn that ended in error")
}
