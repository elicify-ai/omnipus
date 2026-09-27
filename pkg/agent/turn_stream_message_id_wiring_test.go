// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// turn_stream_message_id_wiring_test.go — #823 catch-up-redesign end-to-end
// wiring coverage for the message-id mechanism at its ONE real call site,
// pkg/agent/loop_run_turn.go::callProviderOnce's streaming branch (the
// `rt.ts.nextRoundMessageID(); rt.ts.stampStreamerMessageID(streamer)` pair
// added immediately after the existing stampStreamerProducerAgentID /
// stampStreamerTurnID / stampStreamerParentSpawnCallID calls).
//
// turn_stream_message_id_test.go already proves the underlying
// nextRoundMessageID/stampStreamerMessageID mechanism in isolation. This
// file proves the call site is actually REACHED by a real streaming turn —
// mirroring turn_stream_identity_stamp_wiring_test.go's own rationale
// exactly: nine passing unit tests against a bare *turnState prove nothing
// about whether loop_run_turn.go actually calls them.
//
// Build: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestRunTurn_StreamingMessageID' -p 1 ./pkg/agent/

package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestRunTurn_StreamingMessageID_ReachesRealStreamer drives one real
// streaming turn (single round, no tool calls) through
// AgentLoop.processMessage with a live streamer registered via
// bus.SetStreamDelegate, and asserts the streamer observes exactly one
// SetMessageID call, with a non-empty value, landing BEFORE the first token
// Update — mirroring
// TestRunTurn_StreamingIdentityStamps_ReachRealStreamer's own assertions
// exactly (turn_stream_identity_stamp_wiring_test.go), reusing its
// identityStampRecordingStreamer/identityStampStreamDelegate helpers since
// producerAgentIDMockStreamer (which identityStampRecordingStreamer embeds)
// already implements SetMessageID.
func TestRunTurn_StreamingMessageID_ReachesRealStreamer(t *testing.T) {
	provider := &asyncResultStreamingProvider{content: "hello from the message-id stamped turn"}
	al, msgBus := newProgressWiringTestLoop(t, provider)

	streamer := &identityStampRecordingStreamer{}
	msgBus.SetStreamDelegate(&identityStampStreamDelegate{streamer: streamer})

	_, agent, err := al.processMessage(context.Background(), bus.InboundMessage{
		Channel: "webchat",
		Sender:  bus.SenderInfo{CanonicalID: "user:1", DisplayName: "Tester"},
		ChatID:  "direct",
		Content: "hi",
	})
	require.NoError(t, err)
	require.NotNil(t, agent, "test setup: processMessage must resolve a real agent")

	require.NotEmpty(t, streamer.updates, "test setup invariant: the streaming path must have "+
		"engaged — otherwise this test proves nothing about the streaming call site")

	require.Len(t, streamer.setMessageIDCalls, 1,
		"SetMessageID must be called exactly once before any token flows")
	assert.NotEmpty(t, streamer.setMessageIDCalls[0],
		"the stamped message id must be non-empty — nextRoundMessageID always mints a real uuid "+
			"for the turn's first round")
}

// msgIDScriptStep is one scripted streaming response for
// msgIDScriptedStreamProvider — content/finishReason/toolCalls, enough to
// drive both the ordinary tool-calling case and the ADR-087 D6 truncation-
// continuation case through the real streaming call site.
type msgIDScriptStep struct {
	content      string
	finishReason string
	toolCalls    []providers.ToolCall
}

// msgIDScriptedStreamProvider plays back a fixed sequence of streamed
// responses, one per round, failing loudly on any call past the scripted
// end (mirrors truncation_outcome_test.go's truncationScriptedProvider,
// which only implements the non-streaming Chat and so cannot drive this
// package's streaming call site at all).
type msgIDScriptedStreamProvider struct {
	mu    sync.Mutex
	steps []msgIDScriptStep
	calls int
}

func (p *msgIDScriptedStreamProvider) next() (msgIDScriptStep, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := p.calls
	p.calls++
	if idx >= len(p.steps) {
		return msgIDScriptStep{}, fmt.Errorf("msgIDScriptedStreamProvider: unscripted call %d (only %d scripted)", idx+1, len(p.steps))
	}
	return p.steps[idx], nil
}

func (p *msgIDScriptedStreamProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	step, err := p.next()
	if err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: step.content, FinishReason: step.finishReason, ToolCalls: step.toolCalls}, nil
}

func (p *msgIDScriptedStreamProvider) GetDefaultModel() string { return "msg-id-scripted-model" }

func (p *msgIDScriptedStreamProvider) ChatStream(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
	onChunk func(accumulated string),
	_ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	step, err := p.next()
	if err != nil {
		return nil, err
	}
	if step.content != "" {
		onChunk(step.content)
	}
	return &providers.LLMResponse{Content: step.content, FinishReason: step.finishReason, ToolCalls: step.toolCalls}, nil
}

var _ providers.StreamingProvider = (*msgIDScriptedStreamProvider)(nil)

// msgIDStreamDelegate always hands back the SAME streamer, regardless of
// channel/chatID/sessionID or how many rounds have already run — enough to
// force the streaming branch every round and let one recording streamer
// observe every SetMessageID call across the whole turn.
type msgIDStreamDelegate struct {
	streamer bus.Streamer
}

func (d *msgIDStreamDelegate) GetStreamer(_ context.Context, _, _, _ string) (bus.Streamer, bool) {
	return d.streamer, true
}

var _ bus.StreamDelegate = (*msgIDStreamDelegate)(nil)

// TestRunTurn_StreamingMessageID_NewPerRoundAfterToolCall drives a real
// two-round turn (round 1: narration + a tool call; round 2: the final
// answer) through the streaming path and proves the two rounds get
// DIFFERENT message ids — the ordinary "each round is its own message"
// case, exercised through the real call site rather than a bare turnState.
func TestRunTurn_StreamingMessageID_NewPerRoundAfterToolCall(t *testing.T) {
	provider := &msgIDScriptedStreamProvider{steps: []msgIDScriptStep{
		{
			content: "Let me check that for you.",
			toolCalls: []providers.ToolCall{{
				ID:        "call-1",
				Name:      "mock_custom",
				Arguments: map[string]any{},
			}},
		},
		{content: "Here is the answer."},
	}}
	al, msgBus := newProgressWiringTestLoop(t, provider)
	al.RegisterTool(&mockCustomTool{})
	defaultAgent := al.registry.GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	defaultAgent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"mock_custom": "allow"},
	})

	streamer := &producerAgentIDMockStreamer{}
	msgBus.SetStreamDelegate(&msgIDStreamDelegate{streamer: streamer})

	resp, err := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey:      "msgid-session-1",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "do the thing",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err)
	assert.Equal(t, "Here is the answer.", resp)

	require.Len(t, streamer.setMessageIDCalls, 2,
		"SetMessageID must be called once per LLM call round (2 rounds: tool-calling + final)")
	assert.NotEmpty(t, streamer.setMessageIDCalls[0])
	assert.NotEmpty(t, streamer.setMessageIDCalls[1])
	assert.NotEqual(t, streamer.setMessageIDCalls[0], streamer.setMessageIDCalls[1],
		"round 2 (after the tool call resolved) must get its OWN message id, not reuse round 1's — "+
			"this is a genuinely new answer bubble, not a continuation")
}

// TestRunTurn_StreamingMessageID_ReusedAcrossD6Continuation drives a real
// two-round ADR-087 D6 auto-continue turn (round 1: truncated at the
// output-token limit, eligible to continue; round 2: completes normally)
// through the streaming path and proves BOTH rounds' SetMessageID calls
// carry the SAME value — the exact [U] assumption BE-DESIGN.md §11 item 2
// flags for verification before implementing, now exercised through the
// real call site (loop_run_turn.go -> loop_truncation.go's
// evaluateTruncatedSuccess -> markContinuationDispatched), not just the
// isolated turnState method.
func TestRunTurn_StreamingMessageID_ReusedAcrossD6Continuation(t *testing.T) {
	provider := &msgIDScriptedStreamProvider{steps: []msgIDScriptStep{
		{content: "The answer starts here but gets cut", finishReason: "length"},
		{content: " off, and now it finishes cleanly."},
	}}
	al, msgBus := newProgressWiringTestLoop(t, provider)
	defaultAgent := al.registry.GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	streamer := &producerAgentIDMockStreamer{}
	msgBus.SetStreamDelegate(&msgIDStreamDelegate{streamer: streamer})

	resp, err := al.runAgentLoop(context.Background(), defaultAgent, processOptions{
		SessionKey:      "msgid-session-continuation",
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "tell me something long",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err)
	assert.Contains(t, resp, "finishes cleanly",
		"test setup invariant: the D6 auto-continue must actually have run and produced the "+
			"stitched-together final answer")

	require.Len(t, streamer.setMessageIDCalls, 2,
		"SetMessageID must be called once per round (the truncated round, then its continuation)")
	assert.NotEmpty(t, streamer.setMessageIDCalls[0])
	assert.Equal(t, streamer.setMessageIDCalls[0], streamer.setMessageIDCalls[1],
		"the continuation round must REUSE the truncated round's message id — this is one logical "+
			"answer, finished across two provider calls, not two separate messages")
}
