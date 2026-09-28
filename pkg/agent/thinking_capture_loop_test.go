// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Loop-level RED tests for the WP-C capture pipeline (spec §8.1 storage unit,
// §16 items 26/35; FR-030). The production capture consumer replaces the `nil`
// onReasoning placeholder at pkg/agent/loop_run_turn.go's ChatStream call (the
// placeholder comment names WP-C). Expected values derive from the spec.
//
// thinkingStreamProvider is a test-owned streaming provider driving the WP-B
// landed ChatStream signature (onReasoning receives the ACCUMULATED text so
// far). It mocks at the provider API edge only — the loop, store, capture and
// redaction are real.

const thinkingSentinel = "sk-live-abcd1234EFGH" // recognised credential shape (audit set)

type thinkingRound struct {
	reasoning       string
	answer          string
	toolName        string
	toolArgs        string
	thinkingTokens  int
	providerSummary bool
}

// thinkingStreamProvider scripts rounds; each ChatStream call consumes the
// next round.
type thinkingStreamProvider struct {
	mu       sync.Mutex
	rounds   []thinkingRound
	idx      int
	requests [][]providers.Message // every provider-bound request, in order

	// cancel-test support: emit a reasoning prefix, then block until the
	// caller cancels the turn context.
	blockAfterEmit   bool
	holdbackAfter    int
	reasoningEmitted chan struct{}
	once             sync.Once
}

func (p *thinkingStreamProvider) GetDefaultModel() string { return "test-model" }

func (p *thinkingStreamProvider) Chat(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
) (*providers.LLMResponse, error) {
	// The streaming path is the one under test; Chat exists to satisfy
	// providers.LLMProvider and records the request too.
	p.mu.Lock()
	defer p.mu.Unlock()
	reqCopy := make([]providers.Message, len(messages))
	copy(reqCopy, messages)
	p.requests = append(p.requests, reqCopy)
	round := p.rounds[min(p.idx, len(p.rounds)-1)]
	p.idx++
	return &providers.LLMResponse{
		Content:      round.answer,
		Reasoning:    round.reasoning,
		FinishReason: "stop",
	}, nil
}

func (p *thinkingStreamProvider) ChatStream(
	ctx context.Context,
	messages []providers.Message,
	tools []providers.ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(accumulated string),
	onProgress providers.OnToolCallProgress,
	onReasoning func(accumulated string),
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	round := p.rounds[min(p.idx, len(p.rounds)-1)]
	p.idx++
	reqCopy := make([]providers.Message, len(messages))
	copy(reqCopy, messages)
	p.requests = append(p.requests, reqCopy)
	p.mu.Unlock()

	if onReasoning != nil && round.reasoning != "" {
		if p.blockAfterEmit {
			// Emit the reasoning prefix, then block until cancel.
			onReasoning(round.reasoning[:p.holdbackAfter])
			p.once.Do(func() { close(p.reasoningEmitted) })
			<-ctx.Done()
			return nil, ctx.Err()
		}
		for i := 1; i <= len(round.reasoning); i++ {
			onReasoning(round.reasoning[:i])
		}
	}
	if onProgress != nil {
		onProgress(protocoltypes.ToolCallProgress{ReasoningBytes: len(round.reasoning)})
	}

	if round.toolName != "" {
		if onChunk != nil {
			onChunk("")
		}
		return &providers.LLMResponse{
			Content:   "",
			Reasoning: round.reasoning, // D15: raw rides the context file
			ToolCalls: []protocoltypes.ToolCall{{
				ID:   "call_" + round.toolName,
				Type: "function",
				Function: &protocoltypes.FunctionCall{
					Name:      round.toolName,
					Arguments: round.toolArgs,
				},
			}},
			FinishReason: "tool_calls",
		}, nil
	}

	if onChunk != nil {
		onChunk(round.answer)
	}
	return &providers.LLMResponse{
		Content:      round.answer,
		Reasoning:    round.reasoning,
		Usage:        &protocoltypes.UsageInfo{ThinkingTokens: round.thinkingTokens},
		FinishReason: "stop",
	}, nil
}

// newThinkingTestLoop builds a real AgentLoop with the scripted provider.
func newThinkingTestLoop(t *testing.T, provider *thinkingStreamProvider) *AgentLoop {
	t.Helper()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
	}
	return mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
}

// transcriptEntries walks every session in the loop's store and returns all
// transcript entries (the tests avoid pinning the transcript-session-id
// convention by walking the store).
func transcriptEntries(t *testing.T, al *AgentLoop) []session.TranscriptEntry {
	t.Helper()
	var out []session.TranscriptEntry
	sessions, err := al.sharedSessionStore.ListSessions()
	require.NoError(t, err)
	for _, m := range sessions {
		entries, err := al.sharedSessionStore.ReadTranscript(m.ID)
		require.NoError(t, err)
		out = append(out, entries...)
	}
	return out
}

// filterThinkingEntries keeps only type:"thinking" entries.
func filterThinkingEntries(entries []session.TranscriptEntry) []session.TranscriptEntry {
	var out []session.TranscriptEntry
	for _, e := range entries {
		if e.Type == session.EntryTypeThinking {
			out = append(out, e)
		}
	}
	return out
}

func TestThinkingLoop_ToolOnlyRound_StoresOneThinkingEntryPerRound(t *testing.T) {
	al := newThinkingTestLoop(t, &thinkingStreamProvider{rounds: []thinkingRound{
		{
			reasoning: "Need to look at the deploy log for " + thinkingSentinel + " evidence.",
			toolName:  "no_such_tool_wpc",
			toolArgs:  `{"path":"x"}`,
		},
		{
			reasoning: "The tool is unavailable; answering from what I know.",
			answer:    "The deploy log is unavailable.",
		},
	}})
	w := newSessionWorker("wpc-toolonly", al, func() {})
	w.processTurn(context.Background(), bus.InboundMessage{
		Channel: "test",
		Sender:  bus.SenderInfo{CanonicalID: "user-a"},
		ChatID:  "chat-a",
		Content: "check the deploy",
		Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-a"},
	})

	entries := transcriptEntries(t, al)
	think := filterThinkingEntries(entries)
	require.Len(t, think, 2,
		"a two-round turn keeps ONE thinking row per round, in round order (spec §8.1); got %+v", think)

	// Round 1 (tool-only): the thinking row exists on its OWN entry — never
	// folded into the tool_call entry (spec §8.1).
	assert.Equal(t, "Need to look at the deploy log for [REDACTED] evidence.", think[0].ThinkingText,
		"round 1's redacted reasoning, on its own entry (split secret masked whole-string)")
	assert.NotContains(t, think[0].ThinkingText, thinkingSentinel)
	assert.Empty(t, think[0].Content, "display copy never rides content (C3)")
	assert.Empty(t, think[0].Role, "no role on a thinking entry (C3)")

	// Round 2's row is a DIFFERENT entry with a distinct ID.
	assert.Equal(t, "The tool is unavailable; answering from what I know.", think[1].ThinkingText)
	assert.NotEqual(t, think[0].ID, think[1].ID, "one entry, one ID per round")

	// turn_id binds each row to its round.
	assert.NotEqual(t, think[0].TurnID, think[1].TurnID,
		"different rounds of one turn bind different turn_ids — per-round rows")
	assert.Equal(t, session.EntryTypeThinking, think[0].Type)
}

func TestThinkingLoop_CancelMidThinking_ExactlyOneEntryWithTextSoFar(t *testing.T) {
	// Spec §8.1: "A turn canceled mid-thinking produces exactly one thinking
	// entry carrying the text so far" (§16 item 35).
	prov := &thinkingStreamProvider{
		rounds: []thinkingRound{
			{reasoning: "Begin analyzing the request. The key is " + thinkingSentinel + " held"},
		},
		blockAfterEmit:   true,
		holdbackAfter:    len("Begin analyzing the request. The key is "),
		reasoningEmitted: make(chan struct{}),
	}
	al := newThinkingTestLoop(t, prov)
	w := newSessionWorker("wpc-cancel", al, func() {})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.processTurn(ctx, bus.InboundMessage{
			Channel: "test",
			Sender:  bus.SenderInfo{CanonicalID: "user-a"},
			ChatID:  "chat-a",
			Content: "slow reasoning turn",
			Peer:    bus.Peer{Kind: bus.PeerDirect, ID: "user-a"},
		})
	}()

	// Wait until the provider emitted the reasoning prefix, then cancel.
	select {
	case <-prov.reasoningEmitted:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never emitted the reasoning prefix; the turn never reached the capture")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("processTurn did not return after cancel")
	}

	entries := transcriptEntries(t, al)
	think := filterThinkingEntries(entries)
	require.Len(t, think, 1,
		"a cancel mid-thinking produces EXACTLY ONE thinking entry (no duplicate at cancel AND finalize); got %+v", think)
	assert.Equal(t, "Begin analyzing the request. The key is [REDACTED] held", think[0].ThinkingText,
		"the entry carries the REDACTED text-so-far (whole-string scan at finalize)")
	assert.NotContains(t, think[0].ThinkingText, thinkingSentinel)
}
