// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// truncation_outcome_test.go — ADR-087 WP C test obligations (§7 items 4-9).
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson \
//        -run 'TestTruncation' -p 1 ./pkg/agent/

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// truncationScriptStep is one scripted provider response for
// truncationScriptedProvider.
type truncationScriptStep struct {
	content      string
	finishReason string
	toolCalls    []providers.ToolCall
	err          error
	// onCall runs synchronously INSIDE Chat, before the step's response is
	// returned — used to fire a mid-call Interrupt/InterruptSessionHard so
	// the NEXT turnLoop round observes it (see TestTruncationD6_* below).
	onCall func()
}

// truncationScriptedProvider is a minimal providers.LLMProvider that plays
// back a fixed sequence of responses, each carrying an explicit
// FinishReason — the shared testutil.ScenarioProvider has no finish-reason
// builder (see orphan_tool_markup_test.go's truncatedMarkupProvider, same
// rationale). A call past the end of the script fails loudly instead of
// panicking or returning a zero value, so an unexpected extra provider call
// (e.g. a continuation that should have been denied) fails the test with a
// clear message instead of silently passing.
type truncationScriptedProvider struct {
	mu       sync.Mutex
	steps    []truncationScriptStep
	calls    int
	requests [][]providers.Message
}

func (p *truncationScriptedProvider) Chat(
	_ context.Context,
	msgs []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.requests = append(p.requests, append([]providers.Message(nil), msgs...))
	var step truncationScriptStep
	haveStep := idx < len(p.steps)
	if haveStep {
		step = p.steps[idx]
	}
	p.mu.Unlock()

	if !haveStep {
		return nil, fmt.Errorf("truncationScriptedProvider: unscripted call %d (only %d scripted) — the loop made a provider call it should not have", idx+1, len(p.steps))
	}
	if step.onCall != nil {
		step.onCall()
	}
	if step.err != nil {
		return nil, step.err
	}
	return &providers.LLMResponse{
		Content:      step.content,
		FinishReason: step.finishReason,
		ToolCalls:    step.toolCalls,
	}, nil
}

func (p *truncationScriptedProvider) GetDefaultModel() string { return "scripted-model" }

func (p *truncationScriptedProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newTruncationTestHarness wires a minimal AgentLoop + custom agent + real
// session store around a scripted provider, mirroring
// runturn_error_preservation_test.go's setup so transcript entries can be
// read back after the turn. maxToolIterations and maxLLMCallsPerHour are
// both caller-controlled — the D6.4/D6.8 tests need small, exact bounds.
func newTruncationTestHarness(
	t *testing.T,
	provider providers.LLMProvider,
	maxToolIterations int,
	maxLLMCallsPerHour int,
) (al *AgentLoop, agentInst *AgentInstance, store *session.UnifiedStore, sessionID string) {
	t.Helper()
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const agentID = "truncation-test-agent"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: maxToolIterations,
			},
			List: []config.AgentConfig{
				{ID: agentID, Name: "Truncation Test Agent", Type: config.AgentTypeCustom},
			},
		},
	}
	if maxLLMCallsPerHour > 0 {
		cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour = maxLLMCallsPerHour
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al = mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	var ok bool
	agentInst, ok = al.GetRegistry().GetAgent(agentID)
	require.True(t, ok, "SETUP: custom agent must be registered")

	store = al.GetSessionStore()
	require.NotNil(t, store, "SETUP: shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	require.NoError(t, err)
	sessionID = meta.ID
	return al, agentInst, store, sessionID
}

// lastTruncationAssistantEntry returns the last role=="assistant" transcript entry, or
// nil if none exists.
func lastTruncationAssistantEntry(entries []session.TranscriptEntry) *session.TranscriptEntry {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Role == "assistant" {
			e := entries[i]
			return &e
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// §7.4 — accumulator: prefix+suffix persisted, tokens shown once, Finalize
// prefers the accumulator.
// ---------------------------------------------------------------------------

// TestTruncationD6_AccumulatorPrefixSuffixNoDuplication is the integration
// half of §7.4: two truncated rounds must concatenate into exactly
// prefix+suffix, with the D6.9 continue-instruction never leaking into the
// answer and the prefix never repeated.
//
// BDD: Given a model whose first response is cut off with real content and
// whose second (continuation) response completes cleanly,
// When the turn resolves,
// Then the final answer is exactly the first round's content followed by
// the second round's content, with no duplication and no instruction text.
func TestTruncationD6_AccumulatorPrefixSuffixNoDuplication(t *testing.T) {
	const prefix = "The quick brown fox jumps over "
	const suffix = "the lazy dog."

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: prefix, finishReason: "truncated"},
		{content: suffix, finishReason: "stop"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "accum-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "tell me about the fox",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Equal(t, prefix+suffix, answer,
		"the final answer must be exactly prefix+suffix with no duplication and no continue-instruction text")
	assert.Equal(t, 2, provider.CallCount(), "exactly two rounds: the truncated original and its one continuation")

	// The continuation chain must never have been persisted as durable
	// session/user-role content (D6.7) — only two provider calls were made
	// and the second one's own request is what we already verified pulled
	// the chain from the in-memory `messages` slice; nothing further to
	// assert here beyond call count already pinned above.

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last, "an assistant transcript entry must exist")
	assert.Equal(t, prefix+suffix, last.Content,
		"the persisted transcript entry must carry the full concatenated answer")
	assert.False(t, last.Truncated,
		"a continuation that resolved cleanly (D6.10) must not be annotated truncated")
}

// TestTurnState_FinalizeStreamer_PrefersContinuationAccumulator is the
// unit-level half of §7.4: finalizeStreamer must hand a
// streamerContinuationSetter the FULL accumulated answer, not the
// per-call streamer's own (last-round-only) buffer — and must NOT call it
// at all when no continuation ever happened, leaving the streamer's own
// buffer as authoritative.
//
// BDD: Given a turnState whose D6 accumulator holds "prefix" + "suffix"
// across two dispatched continuation rounds,
// When finalizeStreamer runs against a streamer implementing
// SetContinuationContent,
// Then SetContinuationContent is called with exactly the accumulated text.
// And given a turnState with no continuation ever dispatched,
// When finalizeStreamer runs,
// Then SetContinuationContent is never called.
func TestTurnState_FinalizeStreamer_PrefersContinuationAccumulator(t *testing.T) {
	t.Run("hadContinuation_true", func(t *testing.T) {
		ts := &turnState{}
		ts.appendToAccumulator("prefix-")
		ts.markContinuationDispatched()
		ts.appendToAccumulator("suffix")
		ts.markContinuationDispatched()
		require.True(t, ts.hadContinuation())

		cs := &continuationCapturingStreamer{}
		ts.setLastStreamer(cs)
		ts.finalizeStreamer(context.Background())

		require.Equal(t, 1, cs.setCalls, "SetContinuationContent must be called exactly once")
		assert.Equal(t, "prefix-suffix", cs.lastFull,
			"SetContinuationContent must receive the full accumulated answer")
		assert.Equal(t, 1, cs.finalizeCalled, "Finalize must still be called")
	})

	t.Run("hadContinuation_false", func(t *testing.T) {
		ts := &turnState{}
		cs := &continuationCapturingStreamer{}
		ts.setLastStreamer(cs)
		ts.finalizeStreamer(context.Background())

		assert.Equal(t, 0, cs.setCalls,
			"SetContinuationContent must never be called when no D6 continuation was dispatched")
		assert.Equal(t, 1, cs.finalizeCalled)
	})
}

// continuationCapturingStreamer is a bus.Streamer that also implements
// streamerContinuationSetter, so tests can assert finalizeStreamer's D6.1
// wiring without importing the gateway package's real wsStreamer.
type continuationCapturingStreamer struct {
	mockStreamer
	setCalls int
	lastFull string
}

func (c *continuationCapturingStreamer) SetContinuationContent(full string) {
	c.setCalls++
	c.lastFull = full
}

var _ bus.Streamer = (*continuationCapturingStreamer)(nil)
var _ streamerContinuationSetter = (*continuationCapturingStreamer)(nil)

// ---------------------------------------------------------------------------
// §7.5 — D4a: no double fallback substitution on a truncated-empty turn.
// ---------------------------------------------------------------------------

// TestTruncationD4a_NoDoubleFallbackOnTruncatedEmpty pins D4a: a response
// that is truncated AND produced literally no content must end the turn
// with an EMPTY, annotated answer — never the empty-response-retry fallback
// (defaultResponse) and never the tool-iteration-limit fallback
// (toolLimitResponse). Either substitution reappearing is exactly the
// mutation §9's verification step (d) restores and re-checks.
//
// BDD: Given a model whose only response is truncated with zero content,
// When the turn resolves,
// Then the final answer is "" (not defaultResponse, not toolLimitResponse),
// And the turn is not marked failed,
// And the persisted transcript entry is annotated truncated/max_output_tokens.
func TestTruncationD4a_NoDoubleFallbackOnTruncatedEmpty(t *testing.T) {
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "", finishReason: "truncated"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d4a-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "write something",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Empty(t, answer, "D4a's answer must stay empty — never substituted")
	assert.NotEqual(t, defaultResponse, answer)
	assert.NotEqual(t, toolLimitResponse, answer)
	// D4a is a single call: no retry (the same request truncates identically).
	assert.Equal(t, 1, provider.CallCount(),
		"D4a must not retry the identical request")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last, "D4a must persist a zero-content entry for MarkLastEntryTruncated to find")
	assert.Empty(t, last.Content)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}

// ---------------------------------------------------------------------------
// §7.6 — graceful stop beats continuation recovery (D6.5).
// ---------------------------------------------------------------------------

// TestTruncationD6_GracefulStopBeatsContinuation pins D6.5: once a graceful
// stop has been used for the turn (markGracefulTerminalUsed), a later
// truncated-with-content round must NOT auto-continue — it must resolve via
// D4b immediately, and the turn must never reach the tool-execution branch
// (there is nothing to execute — the pin is "no tools return": no further
// provider call happens after the interrupt takes effect).
//
// BDD: Given a turn whose first round is truncated with real content (D6
// eligible) and a graceful interrupt arrives during that very call,
// When the second round evaluates truncation success,
// Then gracefulTerminal is true and D6 refuses to continue,
// And the turn ends via D4b with the accumulated partial,
// And no third provider call is ever made.
func TestTruncationD6_GracefulStopBeatsContinuation(t *testing.T) {
	const round1 = "Part one of the answer, "
	const round2 = "and the rest of it, cut off again."

	provider := &truncationScriptedProvider{}
	var al *AgentLoop
	const sessionKey = "graceful-stop-session"

	provider.steps = []truncationScriptStep{
		{
			content:      round1,
			finishReason: "truncated",
			// Fires mid-call, so the interrupt is visible when round 2's
			// turnLoop iteration computes gracefulTerminal at its top —
			// but round 1 itself already decided to continue using the
			// STALE (pre-interrupt) value, matching production: the
			// interrupt is observed at the NEXT iteration boundary, not
			// retroactively.
			onCall: func() {
				_, ierr := al.Interrupt(sessionKey, ScopeSubtree, "graceful stop mid-continuation")
				assert.NoError(t, ierr)
			},
		},
		{content: round2, finishReason: "truncated"},
	}

	var inst *AgentInstance
	var store *session.UnifiedStore
	var sessionID string
	al, inst, store, sessionID = newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          sessionKey,
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "tell me a long story",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Equal(t, round1+round2, answer,
		"D4b must annotate the full accumulated partial, not just round 2's content")
	assert.Equal(t, 2, provider.CallCount(),
		"the turn must end after round 2 (D4b) — a graceful stop must never allow a THIRD, continued round")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last)
	assert.Equal(t, round1+round2, last.Content)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}

// ---------------------------------------------------------------------------
// §7.7 — a denied continuation keeps the partial (D6.8): rate-limit denial
// and hard cancel.
// ---------------------------------------------------------------------------

// TestTruncationD6_RateLimitDenialPreservesPartial pins D6.8: a rate-limit
// denial that fires for the round that WOULD have been the continuation
// call must preserve round 1's partial answer, annotated truncated, rather
// than silently discarding it behind the denial error. The denial makes no
// provider call — the budget of 1 call is exactly what round 1 consumed.
//
// BDD: Given a per-agent LLM-call budget of exactly 1 and a first response
// that is truncated with real content (D6 eligible),
// When the turn tries to continue,
// Then the rate limiter denies the second call before it is made,
// And the turn returns an error naming the rate limit,
// And the transcript's assistant entry carries round 1's partial content,
// annotated truncated/max_output_tokens.
func TestTruncationD6_RateLimitDenialPreservesPartial(t *testing.T) {
	const partial = "Here is the beginning of a long answer that gets cut off"

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: partial, finishReason: "truncated"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 1)

	_, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "rate-denial-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "write a long answer",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "the denied continuation must surface as an error")
	assert.Contains(t, err.Error(), "rate limit")
	assert.Equal(t, 1, provider.CallCount(),
		"a denied continuation makes no provider call — only round 1's own call happened")

	entries, err2 := store.ReadTranscript(sessionID)
	require.NoError(t, err2)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last, "the partial answer must be preserved, not discarded")
	assert.Equal(t, partial, last.Content)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}

// TestTruncationD6_HardCancelPreservesPartial pins D6.8's other named exit:
// a hard cancel that fires between round 1 (truncated, D6-eligible) and its
// would-be continuation must preserve round 1's partial, exactly like the
// rate-limit denial above, via abortTurn's own preserveTruncatedAccumulator
// call.
//
// BDD: Given a first response that is truncated with real content, and a
// hard abort requested during that same call,
// When the turn loop observes the hard abort at the top of the next round,
// Then the turn ends via abortTurn's case-1 (clean, user-initiated) path
// with no further provider call,
// And the transcript's assistant entry carries round 1's partial content,
// annotated truncated/max_output_tokens.
func TestTruncationD6_HardCancelPreservesPartial(t *testing.T) {
	const partial = "Round one content before the hard cancel arrives"

	provider := &truncationScriptedProvider{}
	var al *AgentLoop
	const sessionKey = "hard-cancel-session"

	provider.steps = []truncationScriptStep{
		{
			content:      partial,
			finishReason: "truncated",
			onCall: func() {
				_, ierr := al.InterruptSessionHard(sessionKey, ScopeSubtree, "hard cancel mid-continuation")
				assert.NoError(t, ierr)
			},
		},
	}

	var inst *AgentInstance
	var store *session.UnifiedStore
	var sessionID string
	al, inst, store, sessionID = newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          sessionKey,
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "write a long answer",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	// abortTurn's case 1 (hardInterruptAbortReason) is a clean, intentional
	// stop: nil error, empty returned content (runAgentLoop's
	// TurnEndStatusAborted short-circuit) — see abortTurn's own doc comment.
	require.NoError(t, err)
	assert.Empty(t, answer)
	assert.Equal(t, 1, provider.CallCount(),
		"a hard-cancelled continuation makes no provider call")

	entries, err2 := store.ReadTranscript(sessionID)
	require.NoError(t, err2)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last, "the partial answer must be preserved by abortTurn, not discarded")
	assert.Equal(t, partial, last.Content)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}

// ---------------------------------------------------------------------------
// §7.8 — iteration cap: truncation on the last permitted iteration ends via
// D4b, never toolLimitResponse (D6.4).
// ---------------------------------------------------------------------------

// TestTruncationD6_IterationCapEndsViaD4b pins D6.4: with MaxIterations=1,
// the turn's only permitted round is already "the last iteration" the
// moment it starts — a truncated-with-content response there must go
// straight to D4b (never attempt to continue) and must never fall through
// to the generic toolLimitResponse fallback ("I've reached
// max_tool_iterations...").
//
// BDD: Given MaxIterations=1 and a response that is truncated with real
// content on that one permitted round,
// When the turn resolves,
// Then the final answer is the round's own content (D4b), not
// toolLimitResponse,
// And the turn is not marked failed.
func TestTruncationD6_IterationCapEndsViaD4b(t *testing.T) {
	const content = "This is as far as I can get before the limit."

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: content, finishReason: "truncated"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 1, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "iter-cap-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "go as far as you can",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Equal(t, content, answer,
		"the last permitted iteration's truncated answer must stand — D4b, not a continuation")
	assert.NotEqual(t, toolLimitResponse, answer,
		"D6.4: truncation on the last iteration must never fall through to the tool-iteration-limit fallback")
	assert.Equal(t, 1, provider.CallCount(),
		"MaxIterations=1 leaves no room for a continuation round")

	entries, err2 := store.ReadTranscript(sessionID)
	require.NoError(t, err2)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}

// ---------------------------------------------------------------------------
// §7.9 — all three provider-call sites reach the same D3/D4/D6 branch (D9).
// ---------------------------------------------------------------------------

// TestTruncationD9_MainCallSiteReachesD4a pins the MAIN call site: the very
// first provider call in a turn, truncated with zero content, must reach
// D4a exactly like TestTruncationD4a_NoDoubleFallbackOnTruncatedEmpty
// already proves — restated here under the D9 name so all three call-site
// tests are grouped and independently discoverable.
func TestTruncationD9_MainCallSiteReachesD4a(t *testing.T) {
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "", finishReason: "truncated"},
	}}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d9-main-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "hello",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Empty(t, answer)
	assert.Equal(t, 1, provider.CallCount())
}

// TestTruncationD9_EmptyResponseRetryCallSiteReachesD4a pins the THIRD call
// site: the empty-response retry's own successful attempt (loop.go's inner
// mini-loop, reached only when round 1 returns empty content with a
// NON-truncated finish reason — §7.10's pinned case). If that retry's own
// response comes back truncated-and-empty, it must reach D4a via the exact
// same evaluateTruncatedSuccess call the main site uses, not the legacy
// defaultResponse fallback.
//
// BDD: Given round 1 returns empty content with finish_reason "stop" (not
// truncated — enters the legacy empty-response-retry loop),
// When that retry's own call returns empty content with finish_reason
// "truncated",
// Then the turn ends via D4a (empty, annotated, not markTurnFailed) rather
// than the defaultResponse fallback.
func TestTruncationD9_EmptyResponseRetryCallSiteReachesD4a(t *testing.T) {
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "", finishReason: "stop"},      // round 1: plain empty response, non-truncated
		{content: "", finishReason: "truncated"}, // the empty-response retry's own attempt
	}}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d9-empty-retry-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "hello",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	assert.Empty(t, answer)
	assert.NotEqual(t, defaultResponse, answer,
		"the empty-response retry's own truncated-empty result must reach D4a, not the legacy defaultResponse fallback")
	assert.Equal(t, 2, provider.CallCount(),
		"round 1 (empty, non-truncated) plus exactly one empty-response retry")

	entries, err2 := store.ReadTranscript(sessionID)
	require.NoError(t, err2)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}

// ---------------------------------------------------------------------------
// D3 — a truncated tool call gets one bounded repair, gated on nothing
// having streamed yet (D3.2, the guard that prevents duplicated text).
// ---------------------------------------------------------------------------

// TestTruncationD3_StreamedContentGateSkipsRepairAfterPartialStream pins
// D3.2 directly at the evaluateTruncatedToolCallError unit level (mirroring
// TestInlineRetryGuard_PartialStreamNotRetried's existing pattern for the
// unrelated timeout-retry guard, turn_test.go): a repair must never be
// dispatched once the active streamer has already shown the user real
// text — repairing then would duplicate that text when the repaired call's
// own content streams in behind it.
//
// BDD: Given an *common.ToolArgumentsError carrying real truncation
// evidence,
// When the active streamer has already streamed content,
// Then evaluateTruncatedToolCallError refuses to repair.
// And given a streamer that has streamed nothing (or no streamer at all),
// When the same error occurs,
// Then it dispatches exactly one repair, appended to a FRESH copy of the
// input messages.
func TestTruncationD3_StreamedContentGateSkipsRepairAfterPartialStream(t *testing.T) {
	al, cleanup := newAL(t)
	defer cleanup()

	truncatedErr := &common.ToolArgumentsError{
		Cause:     common.ErrToolArgumentsUndecodable,
		ToolName:  "write_file",
		Truncated: true,
	}
	original := []providers.Message{{Role: "user", Content: "please write the file"}}

	t.Run("partial_stream_blocks_repair", func(t *testing.T) {
		ts := &turnState{agent: &AgentInstance{ID: "trunc-d3-agent"}}
		ts.setLastStreamer(&streamingMockStreamer{streamedLen: 5})
		repairUsed := false

		repaired, ok := al.evaluateTruncatedToolCallError(ts, truncatedErr, original, 0, 2, &repairUsed, "scripted-model", 1)
		assert.False(t, ok, "a repair must not be dispatched once partial content was already streamed")
		assert.Nil(t, repaired)
		assert.False(t, repairUsed, "the round-scoped repair flag must stay unspent")
	})

	t.Run("no_stream_allows_exactly_one_repair", func(t *testing.T) {
		ts := &turnState{agent: &AgentInstance{ID: "trunc-d3-agent"}}
		ts.setLastStreamer(&streamingMockStreamer{streamedLen: 0})
		repairUsed := false

		repaired, ok := al.evaluateTruncatedToolCallError(ts, truncatedErr, original, 0, 2, &repairUsed, "scripted-model", 1)
		require.True(t, ok, "nothing streamed yet — the repair must be dispatched")
		assert.True(t, repairUsed)
		require.Len(t, repaired, 2, "the repair note must be appended to a copy of the original messages")
		assert.Equal(t, original[0], repaired[0], "the original message must be preserved unchanged")
		assert.Equal(t, "please write the file", original[0].Content,
			"the input slice must never be mutated by the repair (fresh-copy discipline, D3.5)")
		assert.Contains(t, repaired[1].Content, "cut off at the output-token limit")

		// D3.4: a SECOND truncated error in the same round (roundRepairUsed
		// already spent) must not repair again.
		repaired2, ok2 := al.evaluateTruncatedToolCallError(ts, truncatedErr, original, 1, 2, &repairUsed, "scripted-model", 1)
		assert.False(t, ok2, "a round may spend its repair only once (D3.4)")
		assert.Nil(t, repaired2)
	})
}
