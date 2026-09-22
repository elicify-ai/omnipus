// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// truncation_loop_fixes_test.go — the six ADR-087 WP C correctness findings
// raised by code review against pkg/agent/loop.go. Each test reproduces one
// finding's concrete failure scenario end-to-end through runTurn and fails on
// the pre-fix code.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson \
//        -run 'TestTruncation' -p 1 ./pkg/agent/
//
// Shared scaffolding (truncationScriptedProvider, newTruncationTestHarness,
// lastTruncationAssistantEntry) lives in truncation_outcome_test.go.

package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// truncationEchoTool is a trivially executable tool so a scripted tool call
// actually runs and leaves an observable trace (calls counter + a tool result
// message in the next request).
type truncationEchoTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (e *truncationEchoTool) Name() string        { return "echo_tool" }
func (e *truncationEchoTool) Description() string { return "ADR-087 test stub — echoes its tag" }
func (e *truncationEchoTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"tag": map[string]any{"type": "string"},
	}}
}
func (e *truncationEchoTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (e *truncationEchoTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	e.calls.Add(1)
	tag, _ := args["tag"].(string)
	return &tools.ToolResult{ForLLM: "echo_tool_result:" + tag}
}

// registerTruncationEchoTool makes echo_tool callable by the harness agent.
func registerTruncationEchoTool(al *AgentLoop, inst *AgentInstance) *truncationEchoTool {
	tool := &truncationEchoTool{}
	al.RegisterTool(tool)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"echo_tool": "allow"},
	})
	return tool
}

// truncationEchoCall is a scripted tool call for echo_tool.
func truncationEchoCall(id, tag string) providers.ToolCall {
	return providers.ToolCall{
		ID:       id,
		Type:     "function",
		Name:     "echo_tool",
		Function: &providers.FunctionCall{Name: "echo_tool", Arguments: `{"tag":"` + tag + `"}`},
	}
}

// truncatedToolArgumentsError is the WP D evidence-carrying refusal a
// truncated tool call produces (ADR-087 D5) — the exact error
// evaluateTruncatedToolCallError keys its D3 repair on.
func truncatedToolArgumentsError() error {
	return &common.ToolArgumentsError{
		Cause:        common.ErrToolArgumentsUndecodable,
		ToolName:     "echo_tool",
		FinishReason: "length",
		Truncated:    true,
	}
}

// assistantTranscriptContents returns every assistant transcript entry's
// content in on-disk order — the order a replay (and the next turn's context)
// actually sees.
func assistantTranscriptContents(entries []session.TranscriptEntry) []string {
	var out []string
	for _, e := range entries {
		if e.Role == "assistant" {
			out = append(out, e.Content)
		}
	}
	return out
}

// countMessagesContaining counts how many messages in a request carry needle
// anywhere in their content.
func countMessagesContaining(msgs []providers.Message, needle string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, needle) {
			n++
		}
	}
	return n
}

// hasRole reports whether any message in msgs has the given role.
func hasRole(msgs []providers.Message, role string) bool {
	for _, m := range msgs {
		if m.Role == role {
			return true
		}
	}
	return false
}

// llmAbortHook is an LLMInterceptor that returns HookActionAbortTurn on the
// Nth BeforeLLM (or AfterLLM) invocation and continues otherwise — the
// process-hook abort ADR-087 D6.8 has to survive.
type llmAbortHook struct {
	abortBeforeOnCall int32 // 1-based; 0 disables
	abortAfterOnCall  int32
	beforeCalls       atomic.Int32
	afterCalls        atomic.Int32
}

func (h *llmAbortHook) BeforeLLM(_ context.Context, req *LLMHookRequest) (*LLMHookRequest, HookDecision, error) {
	if h.beforeCalls.Add(1) == h.abortBeforeOnCall {
		return req, HookDecision{Action: HookActionAbortTurn, Reason: "test abort at before_llm"}, nil
	}
	return req, HookDecision{Action: HookActionContinue}, nil
}

func (h *llmAbortHook) AfterLLM(_ context.Context, resp *LLMHookResponse) (*LLMHookResponse, HookDecision, error) {
	if h.afterCalls.Add(1) == h.abortAfterOnCall {
		return resp, HookDecision{Action: HookActionAbortTurn, Reason: "test abort at after_llm"}, nil
	}
	return resp, HookDecision{Action: HookActionContinue}, nil
}

var _ LLMInterceptor = (*llmAbortHook)(nil)

// ---------------------------------------------------------------------------
// F2 — a D3-repaired response that carries tool calls must be DISPATCHED,
// not discarded into the empty-response fallback.
// ---------------------------------------------------------------------------

// TestTruncationD3_RepairedToolCallFromEmptyRetryIsDispatched reproduces the
// finding at the empty-response retry's D3 repair site: that repair exists to
// solicit a re-issued, smaller TOOL CALL, but it runs inside the direct-answer
// branch — a branch entered because the ORIGINAL response had no tool calls —
// and nothing downstream re-inspected response.ToolCalls. A successfully
// repaired call was therefore thrown away and the turn ended on the
// defaultResponse fallback with markTurnFailed, i.e. reported as silence.
//
// BDD: Given round 1 returns empty content with a NON-truncated finish reason
// (the FR-006 empty-response path),
// And the empty-response retry is refused with ErrToolArgumentsUndecodable
// carrying truncation evidence (so D3 dispatches its one repair),
// And the repaired call comes back with empty content but a valid tool call,
// When the turn continues,
// Then the tool actually executes,
// And the turn's answer is the model's later real answer — never
// defaultResponse.
func TestTruncationD3_RepairedToolCallFromEmptyRetryIsDispatched(t *testing.T) {
	const finalAnswer = "I read the file and here is the summary."

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		// round 1, main call site: empty content, NOT truncated → FR-006.
		{content: "", finishReason: "stop"},
		// the empty-response retry's own attempt: refused as a truncated
		// tool call → D3 dispatches exactly one repair.
		{err: truncatedToolArgumentsError()},
		// the repaired call: a smaller, complete tool call, still no prose.
		{content: "", finishReason: "stop", toolCalls: []providers.ToolCall{truncationEchoCall("call-1", "small")}},
		// round 2: the real answer, after the tool result is in context.
		{content: finalAnswer, finishReason: "stop"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	echo := registerTruncationEchoTool(al, inst)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d3-repair-dispatch-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "summarise the file",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)

	assert.Equal(t, int32(1), echo.calls.Load(),
		"the repaired tool call must be executed exactly once — discarding it is the bug")
	assert.Equal(t, finalAnswer, answer,
		"the turn must end on the model's real answer, not the empty-response fallback")
	assert.NotEqual(t, defaultResponse, answer)
	assert.Equal(t, 4, provider.CallCount(),
		"round 1, the refused retry, the repaired call, and the post-tool round")

	entries, readErr := store.ReadTranscript(sessionID)
	require.NoError(t, readErr)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last)
	assert.Equal(t, finalAnswer, last.Content)
}

// ---------------------------------------------------------------------------
// F3 — the D6.7 chain rebuild must find the chain by IDENTITY, not by "the
// last two entries of messages".
// ---------------------------------------------------------------------------

// TestTruncationD6_StripContinuationChainRemovesByIdentity is the unit-level
// pin for the finding: the chain must be located by what it IS, wherever it
// sits, never by "the last two entries". Every row here is a real shape the
// loop produces — the chain still at the tail, a steering message appended
// after it (case a), an assistant tool_calls message plus its tool result
// appended after it (case b) — and the positional rebuild got two of the
// three wrong.
func TestTruncationD6_StripContinuationChainRemovesByIdentity(t *testing.T) {
	base := []providers.Message{
		{Role: "system", Content: "you are a test agent"},
		{Role: "user", Content: "tell me a long story"},
	}
	chain := continuationChainMessages("PART-ONE ")
	require.Len(t, chain, 2, "SETUP: a chain is exactly the assistant partial + the continue instruction")
	require.Equal(t, "assistant", chain[0].Role)
	require.Equal(t, "user", chain[1].Role)

	steer := providers.Message{Role: "user", Content: "STEERING"}
	toolCallMsg := providers.Message{
		Role:      "assistant",
		Content:   "",
		ToolCalls: []providers.ToolCall{truncationEchoCall("c1", "x")},
	}
	toolResult := providers.Message{Role: "tool", ToolCallID: "c1", Content: "echo_tool_result:x"}

	cases := []struct {
		name string
		msgs []providers.Message
		want []providers.Message
	}{
		{
			name: "chain_still_at_tail",
			msgs: append(append([]providers.Message(nil), base...), chain...),
			want: base,
		},
		{
			name: "steering_appended_after_chain",
			msgs: append(append(append([]providers.Message(nil), base...), chain...), steer),
			want: append(append([]providers.Message(nil), base...), steer),
		},
		{
			name: "tool_round_appended_after_chain",
			msgs: append(append(append([]providers.Message(nil), base...), chain...), toolCallMsg, toolResult),
			want: append(append([]providers.Message(nil), base...), toolCallMsg, toolResult),
		},
		{
			name: "chain_not_present_is_unchanged",
			msgs: append(append([]providers.Message(nil), base...), steer),
			want: append(append([]providers.Message(nil), base...), steer),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := append([]providers.Message(nil), tc.msgs...)
			got := stripContinuationChain(input, chain)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.msgs, input, "the input slice must never be mutated in place")
		})
	}
}

// TestTruncationD6_ChainRebuildKeepsSteeringMessage is case (a) of the
// finding: a steering message injected at the top of a later round lands
// AFTER the continuation chain, so the old "strip the last two entries"
// rebuild removed the continue-instruction AND the steering message while
// leaving the stale assistant partial in place — the model then continued
// without the user's steering and saw its own partial twice.
//
// BDD: Given round 1 is truncated with content (a D6 chain is appended),
// And a steering message arrives and is injected at the top of a later round,
// When a later round is truncated again and rebuilds the chain,
// Then the continuation request still carries the steering message,
// And the answer-so-far appears exactly once.
func TestTruncationD6_ChainRebuildKeepsSteeringMessage(t *testing.T) {
	const partOne = "ALPHA-PART-ONE "
	const partTwo = "BRAVO-PART-TWO "
	const steerText = "CHARLIE-STEERING-INSTRUCTION"
	const sessionKey = "d6-chain-steering-session"

	provider := &truncationScriptedProvider{}
	var al *AgentLoop

	provider.steps = []truncationScriptStep{
		// round 1: truncated with content → continuation #1, chain appended.
		{content: partOne, finishReason: "truncated"},
		// round 2: no content, not truncated; steering arrives DURING the
		// call, so the post-response steering poll parks it in
		// pendingMessages and the loop continues — the chain stays live and
		// the steering message is injected at the TOP of round 3, i.e. AFTER
		// the chain.
		{
			content:      "",
			finishReason: "stop",
			onCall: func() {
				require.NoError(t, al.EnqueueSteeringMessage(sessionKey, "truncation-test-agent", steer.Principal{},
					providers.Message{Role: "user", Content: steerText}))
			},
		},
		// round 3: truncated again → continuation #2 rebuilds the chain.
		{content: partTwo, finishReason: "truncated"},
		// round 4 (the continuation): finishes cleanly.
		{content: "and the end.", finishReason: "stop"},
	}

	var inst *AgentInstance
	var store *session.UnifiedStore
	var sessionID string
	al, inst, store, sessionID = newTruncationTestHarness(t, provider, 10, 0)

	_, err := al.runAgentLoop(context.Background(), inst, processOptions{
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

	requests := provider.Requests()
	require.Len(t, requests, 4, "four rounds must have been dispatched")
	continuation := requests[3]

	assert.Equal(t, 1, countMessagesContaining(continuation, steerText),
		"the rebuilt continuation request must still carry the steering message — "+
			"a positional 'last two entries' strip removes it")
	assert.Equal(t, 1, countMessagesContaining(continuation, partOne),
		"the answer-so-far must appear exactly once — a stale chain entry plus the "+
			"rebuilt accumulator is the duplication this fix removes")
}

// TestTruncationD6_ChainRebuildKeepsToolResult is case (b): an ordinary
// tool-calling round between two truncated rounds appends an assistant
// tool_calls message and its tool result AFTER the chain. The positional
// rebuild sliced exactly those two off — the model lost the tool result it
// had just paid for, and still saw its own partial twice.
//
// BDD: Given round 1 is truncated with content (a D6 chain is appended),
// And round 2 is an ordinary tool-calling round,
// When round 3 is truncated again and rebuilds the chain,
// Then the continuation request still carries the tool result,
// And the answer-so-far appears exactly once.
func TestTruncationD6_ChainRebuildKeepsToolResult(t *testing.T) {
	const partOne = "ALPHA-PART-ONE "
	const partTwo = "BRAVO-PART-TWO "

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: partOne, finishReason: "truncated"},
		{content: "", finishReason: "stop", toolCalls: []providers.ToolCall{truncationEchoCall("call-1", "tagged")}},
		{content: partTwo, finishReason: "truncated"},
		{content: "and the end.", finishReason: "stop"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	echo := registerTruncationEchoTool(al, inst)

	_, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d6-chain-toolresult-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "look it up then tell me a long story",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), echo.calls.Load(), "SETUP: the scripted tool call must have run")

	requests := provider.Requests()
	require.Len(t, requests, 4, "four rounds must have been dispatched")
	continuation := requests[3]

	assert.True(t, hasRole(continuation, "tool"),
		"the rebuilt continuation request must still carry the tool result — "+
			"a positional 'last two entries' strip deletes the assistant tool_calls "+
			"message and its result together")
	assert.Equal(t, 1, countMessagesContaining(continuation, "echo_tool_result:tagged"),
		"the tool result must survive exactly once")
	assert.Equal(t, 1, countMessagesContaining(continuation, partOne),
		"the answer-so-far must appear exactly once")
}

// ---------------------------------------------------------------------------
// F4 — the truncated-with-complete-tool-calls carve-out must NOT seed the
// accumulator with content the tool-call branch already persists.
// ---------------------------------------------------------------------------

// TestTruncationD4_ToolCallCarveOutDoesNotSeedAccumulator pins the D4
// carve-out: a response that is truncated but still carries complete tool
// calls executes them once and carries the truncation forward as PENDING
// only. Seeding its narration into the D6 accumulator double-counted it —
// the tool-call branch immediately below already writes that exact text as an
// intermediate transcript entry (and into session history on the assistant
// tool_calls message), so any later accumulator emitter wrote it a second
// time. The cheapest deterministic emitter is D6.8's own terminal-exit
// preservation.
//
// BDD: Given a round that is truncated AND carries a complete tool call, with
// narration text,
// And the following round fails at the provider,
// When the turn ends on that error and D6.8 preserves the pending truncation,
// Then the narration appears in the transcript exactly once,
// And the annotated entry carries no duplicated copy of it.
func TestTruncationD4_ToolCallCarveOutDoesNotSeedAccumulator(t *testing.T) {
	const narration = "Checking the file."

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{
			content:      narration,
			finishReason: "truncated",
			toolCalls:    []providers.ToolCall{truncationEchoCall("call-1", "file")},
		},
		{err: errors.New("provider unavailable")},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	echo := registerTruncationEchoTool(al, inst)

	_, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d4-carveout-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "check the file",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "the second round's provider failure must surface")
	require.Equal(t, int32(1), echo.calls.Load(), "SETUP: the carve-out executes the tool exactly once")

	entries, readErr := store.ReadTranscript(sessionID)
	require.NoError(t, readErr)

	narrationCount := 0
	for _, e := range entries {
		if e.Content == narration {
			narrationCount++
		}
	}
	assert.Equal(t, 1, narrationCount,
		"the narration is persisted by the tool-call branch itself — seeding it into "+
			"the accumulator makes D6.8's terminal preservation write it a second time")

	truncatedEntries := 0
	for _, e := range entries {
		if e.Truncated {
			truncatedEntries++
			assert.Equal(t, "max_output_tokens", e.TruncationReason)
			assert.NotEqual(t, narration, e.Content,
				"the annotated entry must not be a duplicate of the already-persisted narration")
		}
	}
	assert.Equal(t, 1, truncatedEntries, "exactly one entry carries the truncation annotation")
}

// ---------------------------------------------------------------------------
// F5 — ONE choke point: every runTurn exit preserves a pending D6
// continuation, including the process-hook aborts that were never wired.
// ---------------------------------------------------------------------------

// TestTruncationD6_HookAbortPreservesPartial pins the D6.8 choke point
// against the exits that used to call nothing at all. A process hook
// returning AbortTurn at the top of (or immediately after) a continuation
// round left the partial either persisted as a COMPLETE, un-annotated answer
// (webchat, via the streamer's own finalize) or dropped outright (every
// non-streamed surface — heartbeat, cron, delegated sub-turns — where the
// chain never reached history at all, D6.7).
//
// BDD: Given round 1 is truncated with content and a continuation is
// dispatched,
// When a hook aborts the turn at before_llm (or at after_llm on the
// continuation's own response),
// Then the partial is persisted, annotated truncated/max_output_tokens.
func TestTruncationD6_HookAbortPreservesPartial(t *testing.T) {
	const partial = "The first half of the answer that never got finished"

	cases := []struct {
		name          string
		hook          *llmAbortHook
		steps         []truncationScriptStep
		expectedCalls int
	}{
		{
			name: "before_llm_on_continuation_round",
			hook: &llmAbortHook{abortBeforeOnCall: 2},
			steps: []truncationScriptStep{
				{content: partial, finishReason: "truncated"},
			},
			expectedCalls: 1,
		},
		{
			name: "after_llm_on_continuation_response",
			hook: &llmAbortHook{abortAfterOnCall: 2},
			steps: []truncationScriptStep{
				{content: partial, finishReason: "truncated"},
				{content: " and the second half.", finishReason: "stop"},
			},
			expectedCalls: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &truncationScriptedProvider{steps: tc.steps}
			al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
			require.NoError(t, al.MountHook(NamedHook("truncation-abort-hook", tc.hook)))

			_, err := al.runAgentLoop(context.Background(), inst, processOptions{
				SessionKey:          "d6-hook-abort-" + tc.name,
				Channel:             "web",
				ChatID:              sessionID,
				UserMessage:         "write a long answer",
				DefaultResponse:     defaultResponse,
				SendResponse:        false,
				TranscriptSessionID: sessionID,
				TranscriptStore:     store,
			})
			require.Error(t, err, "a hook AbortTurn must surface as an error")
			assert.Equal(t, tc.expectedCalls, provider.CallCount())

			entries, readErr := store.ReadTranscript(sessionID)
			require.NoError(t, readErr)
			var preserved *session.TranscriptEntry
			for i := range entries {
				if entries[i].Role == "assistant" && strings.Contains(entries[i].Content, partial) {
					e := entries[i]
					preserved = &e
				}
			}
			require.NotNil(t, preserved,
				"the partial must survive a hook abort — this exit called nothing before the fix")
			assert.True(t, preserved.Truncated, "and it must be annotated, not read back as a complete answer")
			assert.Equal(t, "max_output_tokens", preserved.TruncationReason)
		})
	}
}

// ---------------------------------------------------------------------------
// F6 — a non-truncated round that resolves a continuation must settle the
// accumulated prefix IN ORDER, not leave it to be prepended at turn end.
// ---------------------------------------------------------------------------

// TestTruncationD6_ToolRoundSettlesPrefixInOrder pins the ordering contract.
// Before the fix a continuation followed by a tool-calling round followed by a
// plain final answer persisted the tool round's narration first and the
// truncated prefix only at the very end, prepended to the final answer:
// [P2][P1+P3]. Content was preserved, order was not — the SPA's replay merge
// renders that as "P2\n\nP1P3" and the next turn's context reads P1 after P2.
//
// The chosen resolution (documented on flushContinuationAccumulator): the
// accumulator holds only text not yet persisted, and is flushed as its own
// in-order entry the moment another path is about to persist a later round's
// content. Result: three entries, [P1][P2][P3].
//
// BDD: Given round 1 is truncated with content P1 (a continuation is
// dispatched),
// And round 2 is a non-truncated tool-calling round whose narration is P2,
// And round 3 is a plain final answer P3,
// When the turn resolves,
// Then the transcript's assistant entries are exactly [P1, P2, P3] in that
// order,
// And session history carries the same three in the same order,
// And the turn's answer is P3 alone (P1 is already settled, never repeated).
func TestTruncationD6_ToolRoundSettlesPrefixInOrder(t *testing.T) {
	const p1 = "First, the part that got cut off."
	const p2 = "Now let me check the file."
	const p3 = "Here is the finished answer."
	const sessionKey = "d6-order-session"

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: p1, finishReason: "truncated"},
		{content: p2, finishReason: "stop", toolCalls: []providers.ToolCall{truncationEchoCall("call-1", "file")}},
		{content: p3, finishReason: "stop"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	echo := registerTruncationEchoTool(al, inst)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          sessionKey,
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "answer at length, checking the file",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	require.Equal(t, int32(1), echo.calls.Load(), "SETUP: the scripted tool call must have run")

	assert.Equal(t, p3, answer,
		"the truncated prefix was already settled in order — repeating it in the final "+
			"answer is exactly the mis-ordering this fixes")

	entries, readErr := store.ReadTranscript(sessionID)
	require.NoError(t, readErr)
	assert.Equal(t, []string{p1, p2, p3}, assistantTranscriptContents(entries),
		"the archive must read [P1][P2][P3]; the bug produced [P2][P1+P3]")

	var historyAssistant []string
	for _, m := range inst.Sessions.GetHistory(sessionKey) {
		if m.Role == "assistant" {
			historyAssistant = append(historyAssistant, m.Content)
		}
	}
	assert.Equal(t, []string{p1, p2, p3}, historyAssistant,
		"the next turn's context must read the same three segments in the same order")
}

// ---------------------------------------------------------------------------
// F9 — the success arm must read response.Content, never the
// ReasoningContent-substituted local.
// ---------------------------------------------------------------------------

// TestTruncationD4a_ReasoningOnlyTruncatedIsNotContinued pins the boundary
// between a model's chain-of-thought and its answer. loop.go substitutes
// ReasoningContent for an empty Content on the direct-answer path (a legacy
// salvage for reasoning-only models); handing that substituted value to the
// D4/D6 success arm meant a reasoning-only truncated response had its
// chain-of-thought accumulated, echoed back to the model under "Continue from
// exactly where it stopped", and persisted as the answer.
//
// A reasoning-only truncated response produced no answer text, so it is D4a:
// end the turn with an empty, annotated entry, no continuation, no second
// call.
//
// BDD: Given a response with empty Content, non-empty ReasoningContent and
// finish_reason "truncated",
// When the success arm evaluates it,
// Then the turn ends via D4a with an empty annotated answer,
// And no continuation round is dispatched,
// And the chain-of-thought is never sent back to the model.
func TestTruncationD4a_ReasoningOnlyTruncatedIsNotContinued(t *testing.T) {
	const chainOfThought = "SECRET-REASONING-TRACE the user must never be shown"

	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "", reasoningContent: chainOfThought, finishReason: "truncated"},
		// Scripted but must never run: a continuation here is the bug.
		{content: " continued from the reasoning.", finishReason: "stop"},
	}}

	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "d4a-reasoning-session",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "think hard about this",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)

	assert.Equal(t, 1, provider.CallCount(),
		"a reasoning-only truncated response is D4a — it must never earn a continuation round")
	assert.Empty(t, answer, "D4a's answer is empty")
	assert.NotContains(t, answer, chainOfThought,
		"the model's chain-of-thought must never become the persisted answer")

	for i, req := range provider.Requests() {
		assert.Equal(t, 0, countMessagesContaining(req, chainOfThought),
			"request %d must not echo the chain-of-thought back to the model", i)
	}

	entries, readErr := store.ReadTranscript(sessionID)
	require.NoError(t, readErr)
	last := lastTruncationAssistantEntry(entries)
	require.NotNil(t, last, "D4a still persists a zero-content entry to carry the annotation")
	assert.Empty(t, last.Content)
	assert.True(t, last.Truncated)
	assert.Equal(t, "max_output_tokens", last.TruncationReason)
}
