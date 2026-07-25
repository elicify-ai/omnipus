//go:build !cgo

// websocket_error_frame_test.go — FIX 2 regression: the EventKindError
// forwarder in eventForwarder (websocket.go) always recomputed
// agent.TranslateLLMError(p.ProviderError, p.Message) from scratch, discarding
// the already-computed ErrorPayload.Code and any caller-curated Message. For
// trusted-internal-stage messages (hook aborts, model-switch failures,
// session save/restore, synthetic-error-floor, external-CLI sanitized text)
// this meant a curated message that happened to contain a pinned classifier
// substring (e.g. "safety") was silently replaced with generic boilerplate
// live — even though appendErrorTranscript (pkg/agent/turn.go) preserved the
// SAME message verbatim in the persisted transcript, so the error read
// correctly only after a page reload.
//
// The fix: prefer p.Code/p.Message when Code is populated, falling back to a
// fresh TranslateLLMError only when a call site left Code empty.

package gateway

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestEventForwarder_ErrorFrame_PrefersPayloadCodeAndMessage(t *testing.T) {
	// A curated, caller-provided message that the classifier would, if
	// re-run against it, reclassify to CodeContentPolicy ("safety" is a
	// pinned contentPolicySubstrings hit) — proving the forwarder must use
	// p.Code/p.Message verbatim rather than recomputing.
	const curated = "hook aborted turn during before_tool: content flagged under our safety policy"

	cases := []struct {
		name        string
		payload     agent.ErrorPayload
		wantFrame   bool
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
			wantFrame:   true,
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
			wantFrame:   true,
			wantMessage: agent.UserMessageForCode(agent.CodeNetwork),
			wantCode:    string(agent.CodeNetwork),
		},
		{
			name: "Code=rate_limited is suppressed — RateLimitFrame is authoritative",
			payload: agent.ErrorPayload{
				Stage:   "runTurn",
				Code:    string(agent.CodeRateLimited),
				Message: "rate limit: too many requests (retry after 5s)",
				ChatID:  "chat-1",
			},
			wantFrame: false,
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

			if !tc.wantFrame {
				assert.Empty(t, ch, "rate_limited errors must not emit a duplicate ErrorFrame")
				return
			}

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
