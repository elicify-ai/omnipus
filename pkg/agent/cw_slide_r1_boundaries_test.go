//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// MAJ-CW-005: matching is within an assistant step. Two retained completed
// steps may legitimately reuse an id; a global sanitizer must not erase one.
func TestCWSlideR1_ReusedIDsStayScopedToTheirRetainedSteps(t *testing.T) {
	h := cwR1New(t, 32_768)
	ts := h.turn("reuse scope anchor")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	old := strings.Repeat("readable archived source ", 1_000)
	h.append(t, cwR1Step("discard-first", "oldest narration", old)...)
	h.append(t, cwR1Step("reused-id", "retained older narration", old)...)
	newestCalls := []providers.ToolCall{cwR1Call("reused-id"), cwR1Call("floor-other")}
	h.append(t, providers.Message{Role: "assistant", Content: "newest narration", ToolCalls: newestCalls},
		providers.Message{Role: "tool", ToolCallID: "reused-id", Content: "newest reuse result"},
		providers.Message{Role: "tool", ToolCallID: "floor-other", Content: "newest other result"})
	archive := h.archive(t)
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	require.Greater(t, toolResultShareTokens(messages), 16_384, "instrument: relative share fires")
	require.Less(t, requestTokens(messages, nil), agentContextBudget(h.agent), "instrument: total still fits")
	out := h.check(t, ts, messages)
	r, p := cwR1OpenAI(t, 0)
	cwR1Send(t, cwR1Flow(h, ts, out, p))
	require.Len(t, r.requests(t), 1)
	sent := cwR1Messages(t, r.requests(t)[0])
	cwR1AssertComplete(t, sent)
	var calls []providers.ToolCall
	var results []providers.Message
	for _, message := range sent {
		switch message.Role {
		case "assistant":
			calls = append(calls, message.ToolCalls...)
		case "tool":
			results = append(results, message)
		}
	}
	require.Equal(t, []providers.ToolCall{cwR1Call("reused-id"), cwR1Call("reused-id"), cwR1Call("floor-other")}, calls,
		"both retained assistant steps keep the reused call id and exact arguments")
	require.Equal(t, []providers.Message{
		{Role: "tool", ToolCallID: "reused-id", Content: old},
		{Role: "tool", ToolCallID: "reused-id", Content: "newest reuse result"},
		{Role: "tool", ToolCallID: "floor-other", Content: "newest other result"},
	}, results, "result identity includes its step/archive line, never just the bare id")
	require.Greater(t, cwR1Skip(t, h), 0)
	require.Equal(t, archive, h.archive(t))
}

// MAJ-CW-002/004/006: a control before older results blocks a prefix cut,
// but does not protect their text. Empty an older result before newest text.
func TestCWSlideR1_ProtectedControlForcesOlderMarkBeforeNewestText(t *testing.T) {
	h := cwR1New(t, 20_000)
	ts := h.turn("protected-prefix anchor")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	_, p := cwR1OpenAI(t, 0)
	rr := cwR1Flow(h, ts, messages, p)
	control := providers.Message{Role: "user", Content: "[Steering id=r1-protected-prefix] Keep this original direction."}
	rr.rq.ri.pendingMessages = []providers.Message{control}
	rr.rq.ri.pendingSteeringReceipts = []string{"r1-protected-prefix"}
	require.Equal(t, agentLoopRunTurnIterationNext, rr.rq.ri.beginIteration())
	full := strings.Repeat("older retained readable source ", 2_000)
	steps := append(cwR1Step("blocked-older", "older narration", full), cwR1Step("protected-newest", "newest narration", "unchanged newest source")...)
	h.append(t, steps...)
	rr.rq.ri.messages = append(rr.rq.ri.messages, steps...)
	archive := h.archive(t)
	out := h.check(t, ts, rr.rq.ri.messages)
	require.Zero(t, cwR1Skip(t, h), "Skip cannot cross a control that has never reached a provider send")
	cwR1AssertControls(t, out, []providers.Message{control})
	cwR1AssertComplete(t, out)
	require.True(t, cwR1HasCall(out, "blocked-older"), "the blocked older group keeps its call/result slots")
	cwR1AssertMarkAtTurn(t, cwR1Result(t, out, "blocked-older").Content, "emptied", "blocked-older", 3, utf8.RuneCountInString(full), 3)
	require.Equal(t, "unchanged newest source", cwR1Result(t, out, "protected-newest").Content, "older text reaches mark before touching newest text")
	require.Equal(t, archive, h.archive(t))
}

// MAJ-CW-012: k=0 is not mutable; k=1 becomes a mark. If that mark grows
// the payload, halve the next declared source in the SAME staged pass before
// spending a retry. This covers successful k=1 relief and mark-overhead growth.
func TestCWSlideR1_ForcedNewestOrderContinuesPastMarkGrowth(t *testing.T) {
	for _, first := range []string{"", "界"} {
		t.Run(fmt.Sprintf("first_source_runes_%d", utf8.RuneCountInString(first)), func(t *testing.T) {
			h := cwR1New(t, 80_000)
			ts := h.turn("declared-order anchor")
			full := cwR1Unicode(4_095)
			calls := []providers.ToolCall{cwR1Call("first-source"), cwR1Call("second-source")}
			h.append(t, providers.Message{Role: "user", Content: ts.userMessage},
				providers.Message{Role: "assistant", Content: "immutable newest narration", ToolCalls: calls},
				providers.Message{Role: "tool", ToolCallID: "first-source", Content: first},
				providers.Message{Role: "tool", ToolCallID: "second-source", Content: full})
			archive := h.archive(t)
			messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			cwR1Fits(t, h, messages)
			r, p := cwR1OpenAI(t, 2)
			rr := cwR1Flow(h, ts, messages, p)
			cwR1Retry(t, rr)
			require.NoError(t, rr.rq.ri.rf.err, "three actual sends, not an overhead-only extra attempt")
			requests := r.requests(t)
			require.Len(t, requests, 3, "original two retries cover two progressing candidates")
			for i := 1; i < 3; i++ {
				sent := cwR1Messages(t, requests[i])
				cwR1AssertComplete(t, sent)
				if first == "" {
					require.Equal(t, "", cwR1Result(t, sent, "first-source").Content, "k=0 has no eligible source operation")
				} else {
					cwR1AssertMark(t, cwR1Result(t, sent, "first-source").Content, "emptied", "first-source", 2, 1)
				}
				cwR1AssertProjection(t, cwR1Result(t, sent, "second-source").Content, full, "second-source", 3, 4_095>>i)
				require.Less(t, cwR1RetainedBytes(t, sent), cwR1RetainedBytes(t, cwR1Messages(t, requests[i-1])), "mark growth cannot consume a send without serialized progress")
			}
			require.Zero(t, cwR1Skip(t, h), "newest two-call structural floor is not evicted")
			require.Equal(t, archive, h.archive(t))
		})
	}
}

func cwR1SetBudget(t *testing.T, h *cwR1Harness, budget int) {
	t.Helper()
	require.Positive(t, budget)
	pinned := pinnedCoreOverheadTokens(h.agent)
	for window := budget + h.agent.MaxTokens + pinned; ; window++ {
		// ceil(.05 W) == (W+19)/20, derived from MAJ-CW-007.
		if window-h.agent.MaxTokens-(window+19)/20-pinned >= budget {
			h.agent.ContextWindow = window
			break
		}
	}
	require.Equal(t, budget, agentContextBudget(h.agent), "fixture solves the one spec budget exactly")
}

// Scenario 8; MAJ-CW-012 orders an older addressed mark before newest source
// shortening when a protected control blocks the prefix slide. Its final floor
// paragraph requires each rejected/retried request to retain the original steer.
// Oracle: one real rejection, then the first smaller candidate succeeds; only
// the older source is emptied. This is distinct from proactive share pressure.
func TestCWSlideR1_RejectionMarksBlockedOlderBeforeNewestText(t *testing.T) {
	h := cwR1New(t, 80_000)
	ts := h.turn("reactive older-mark anchor")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	r, p := cwR1OpenAI(t, 1)
	rr := cwR1Flow(h, ts, messages, p)
	control := providers.Message{Role: "user", Content: "[Steering id=r1-reactive-mark] Retain this original direction in every retry."}
	rr.rq.ri.pendingMessages = []providers.Message{control}
	rr.rq.ri.pendingSteeringReceipts = []string{"r1-reactive-mark"}
	require.Equal(t, agentLoopRunTurnIterationNext, rr.rq.ri.beginIteration())
	older, newest := cwR1Unicode(4_096), cwR1Unicode(4_095)
	steps := append(cwR1Step("reactive-blocked-older", "older narration", older),
		cwR1Step("reactive-mark-newest", "newest narration", newest)...)
	h.append(t, steps...)
	rr.rq.ri.messages = append(rr.rq.ri.messages, steps...)
	archive := h.archive(t)
	cwR1Fits(t, h, rr.rq.ri.messages)
	cwR1Retry(t, rr)
	require.NoError(t, rr.rq.ri.rf.err, "one forced older-mark candidate must succeed despite local fit")
	requests := r.requests(t)
	require.Len(t, requests, 2, "one genuine rejection and one progressing retry")
	first, second := cwR1Messages(t, requests[0]), cwR1Messages(t, requests[1])
	// MAJ-CW-002/005: the archive-backed view preserves the exact original
	// anchor and order; provider normalization may merge adjacent user text.
	anchor := providers.Message{Role: "user", Content: ts.userMessage}
	live := h.agent.Sessions.GetHistory(h.key)
	require.NotEmpty(t, live, "archive-backed live view retains the original anchor")
	require.Equal(t, anchor, live[0], "archive-backed live view keeps the exact original anchor before the retained suffix")
	cwR1AssertAnchor(t, live, anchor)
	for _, sent := range [][]providers.Message{first, second} {
		cwR1AssertControls(t, sent, []providers.Message{control})
		cwR1AssertComplete(t, sent)
		var wireText strings.Builder
		for _, message := range sent {
			wireText.WriteString(message.Content)
			wireText.WriteByte('\n')
		}
		require.Equal(t, 1, strings.Count(wireText.String(), anchor.Content),
			"normalized wire retains one exact original anchor text, even when merged with adjacent user text")
	}
	require.True(t, cwR1HasCall(second, "reactive-blocked-older"), "the protected prefix keeps the older call/result slots")
	cwR1AssertMarkAtTurn(t, cwR1Result(t, second, "reactive-blocked-older").Content,
		"emptied", "reactive-blocked-older", 3, 4_096, 3)
	require.Equal(t, newest, cwR1Result(t, second, "reactive-mark-newest").Content,
		"first progressing older mark stops relief before touching newest source")
	require.Less(t, cwR1RetainedBytes(t, second), cwR1RetainedBytes(t, first), "a complete addressed mark still leaves a smaller final payload")
	require.Zero(t, cwR1Skip(t, h), "the cursor cannot cross the retained original control")
	require.Equal(t, archive, h.archive(t), "reactive projection never rewrites full filtered source")
}
