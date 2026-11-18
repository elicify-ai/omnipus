// turn_reasoning_handoff_test.go — CHECK5 m3 gap closure: the receiver-side
// unit (pkg/gateway effort_turn_reasoning_streamer_test.go) calls
// SetTurnReasoningTokens itself and so bypasses the production caller. This
// test drives the real producer→caller pair: two provider calls booked at the
// production attempt boundary (accountProviderAttempt → logCompletedCall →
// accountProviderCall), the mid-turn no-text tool call flushed exactly as
// logCompletedCall flushes it (prompt_cache_accounting.go), then
// turnState.finalizeStreamer — the production handoff site — handing the
// streamer the turn's whole-turn reasoning sum, exactly once.
//
// Spec: effort-levels spec r10, accounting table row 11 / S-95 — two calls of
// one tool round report 30 and 0, and the turn carries 30 on every transport
// (FR-058). The streamer contract is turn_stream.go::TurnReasoningStreamer:
// SetCallAccounting receives only the calls still pending at the end (a
// no-text tool call books its accounting the moment it completes), so the
// handover must come from the turn's own sum over EVERY call, never from the
// pending calls alone.
package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// turnReasoningHandoffStreamer records what finalizeStreamer pushes into a
// streamer implementing both optional accounting interfaces — the shape the
// production wsStreamer presents (CallAccountingStreamer + TurnReasoningStreamer
// on bus.Streamer).
type turnReasoningHandoffStreamer struct {
	mu              sync.Mutex
	accountingCalls [][]session.TranscriptEntry
	reasoningVals   []*int
	finalized       bool
	finalContent    string
}

func (f *turnReasoningHandoffStreamer) Update(context.Context, string) error { return nil }
func (f *turnReasoningHandoffStreamer) Cancel(context.Context)               {}

func (f *turnReasoningHandoffStreamer) Finalize(_ context.Context, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalized = true
	f.finalContent = content
	return nil
}

func (f *turnReasoningHandoffStreamer) SetCallAccounting(entries []session.TranscriptEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountingCalls = append(f.accountingCalls, entries)
}

func (f *turnReasoningHandoffStreamer) SetTurnReasoningTokens(n *int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reasoningVals = append(f.reasoningVals, n)
}

// TestFinalizeStreamer_TurnReasoningHandoffCoversFlushedMidTurnCall pins the
// caller side of the turn-reasoning handoff: a mid-turn no-text tool call
// reporting 30 is flushed to the transcript the moment it completes, the final
// content-bearing call reporting 0 stays pending, and finalization hands the
// streamer the whole-turn sum 30 — exactly once. Removing the handoff (CHECK5
// mutant m3) leaves the setter never called; summing only the pending calls
// would hand over 0. Both die on the assertions below.
func TestFinalizeStreamer_TurnReasoningHandoffCoversFlushedMidTurnCall(t *testing.T) {
	cat, err := catalog.NewCatalog([]byte(backendCallCatalog))
	require.NoError(t, err, "load the spec-priced catalog fixture")
	previous := providers.ProviderCatalog()
	providers.SetCatalog(cat)
	t.Cleanup(func() { providers.SetCatalog(previous) })

	store, err := session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
	require.NoError(t, err)
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "handoff-agent")
	require.NoError(t, err)

	ts := &turnState{
		agentID:             "handoff-agent",
		turnID:              "turn-handoff",
		startedAt:           time.Now(),
		agent:               &AgentInstance{ID: "handoff-agent"},
		transcriptStore:     store,
		transcriptSessionID: meta.ID,
	}
	rt := &agentLoopRunTurn{ts: ts}

	// Spec accounting row 11 (S-95): two calls of one tool round — the
	// mid-turn no-text call reports 30, the final call reports 0; the turn
	// sum is 30 + 0 = 30.
	thirty := 30
	toolCall := &providers.LLMResponse{
		FinishReason: "tool_calls",
		Usage:        &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 30, TotalTokens: 40, ReasoningTokens: &thirty},
	}
	rt.accountProviderAttempt("anthropic", "cache-model", toolCall, nil)

	// Positive control 1: the no-text call's entry was flushed mid-turn (the
	// logCompletedCall branch for empty content), carrying its own count and
	// leaving nothing pending — the exact state shape production reaches
	// between the tool round and the answer.
	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, entries, 1, "the mid-turn tool call's accounting was flushed the moment it completed")
	require.NotNil(t, entries[0].ReasoningTokens, "the flushed entry carries its own count")
	require.Equal(t, 30, *entries[0].ReasoningTokens, "the flushed tool-call entry stores reasoning_tokens 30")
	require.Equal(t, 0, ts.callAccountingCount(), "the flushed call no longer sits in pending")

	zero := 0
	answer := &providers.LLMResponse{
		Content:      "FINAL ANSWER",
		FinishReason: "stop",
		Usage:        &providers.UsageInfo{PromptTokens: 5, CompletionTokens: 80, TotalTokens: 85, ReasoningTokens: &zero},
	}
	rt.accountProviderAttempt("anthropic", "cache-model", answer, nil)
	require.Equal(t, 1, ts.callAccountingCount(), "only the content-bearing final call is still pending at finalization")

	streamer := &turnReasoningHandoffStreamer{}
	ts.lastStreamer = streamer
	require.NoError(t, ts.finalizeStreamer(context.Background()))

	require.True(t, streamer.finalized, "positive control: finalizeStreamer ran the streamer branch")
	require.Len(t, streamer.reasoningVals, 1,
		"finalization hands the turn's reasoning sum over exactly once — removing the handoff (m3) leaves it never called")
	require.NotNil(t, streamer.reasoningVals[0], "the handover is a reported sum, not an absent one")
	require.Equal(t, 30, *streamer.reasoningVals[0],
		"the handover is the whole-turn sum 30 + 0 = 30, never the pending calls' 0 (spec row 11 / FR-058)")
	require.Len(t, streamer.accountingCalls, 1, "the pending calls are handed over through SetCallAccounting")
	if len(streamer.accountingCalls) == 1 {
		require.Len(t, streamer.accountingCalls[0], 1, "only the final call is still pending at the end")
	}
}
