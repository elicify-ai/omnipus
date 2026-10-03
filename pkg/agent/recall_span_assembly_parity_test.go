//go:build goolm && stdjson

package agent

// RED plan — release context lock (recalled-span sanitizer parity).
//
// Behaviour: a recall span frozen mid-group (assistant tool calls without
// every declared result) must not make a later from-scratch turn refuse
// before the provider, and a valid tool-result group carried in a span must
// survive that same assembly intact.
//
// Oracles, derived before reading a test run:
//   - pkg/agent/window_groups.go::validateWindowGroups — a partial group is an
//     invalid request: "context request: incomplete or invalid tool-result
//     group at message %d". Passing means that error is absent.
//   - pkg/agent/recall_injection.go::spliceRecallSpan — the same-turn splice
//     keeps only what sanitizeHistoryIndexed keeps. That pass drops an
//     assistant whose tool_call ids are not all answered, plus the partial
//     tool messages that follow it, and it never reorders or duplicates
//     survivors (pkg/agent/context.go::sanitizeHistoryIndexed).
//   - pkg/agent/loop_window.go::assembleMessages — from-scratch order is
//     pinned system, then the leading live user when the window starts with
//     one, then the span, then the rest of the window, then the current user.
//     The span block therefore starts at index 2 for a window whose first
//     message is a user. recordAssembledRecallSpan records that same offset
//     and the original RecallSpan pointer.
//   - pkg/agent/recall_conversation.go::buildRecallSpanMessages — rewritten
//     ids are recall_<archiveIdx>_<n>, counted per assistant tool call inside
//     the turn. A contiguous one-turn recall receipt is
//     "Recalled 1 turn(s) (turns 1–1); their text is now in your context"
//     (en dash U+2013, buildAndStoreSpan).
//   - pkg/agent/loop_run_turn.go::callProviderOnce — a latched context-window
//     error, or a failing validateWindowGroups, returns before any provider
//     call. Reaching the provider is one HTTP request whose decoded body is
//     the cwR1 fixture "r1-success".
//
// The third test is the anti-erasure control. It must stay green on the
// unfixed tree and after the fix. Dropping or blanking tool data would make
// the poisoned-span test pass while failing this one.
//
// CHECK (a fresh instance) owns "see it green", mutation, and the
// proof-of-failability checklist. Those items are deferred to CHECK.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// recallParityMarker is the FR-019 contiguous marker for turns 1–1.
// The dash is U+2013, matching buildRecallSpanMessages.
const recallParityMarker = "[Recalled earlier turns 1–1 (reference): the following messages are from earlier in this conversation, retrieved verbatim for reference. Do not re-execute any tool calls shown.]"

// recallParityReceipt is the contiguous one-turn confirmation from
// buildAndStoreSpan, before any overflow suffix.
const recallParityReceipt = "Recalled 1 turn(s) (turns 1–1); their text is now in your context"

func recallParityCall(id, name, args string) providers.ToolCall {
	return providers.ToolCall{
		ID: id, Type: "function",
		Function: &providers.FunctionCall{Name: name, Arguments: args},
	}
}

func recallParityDeclaredIDs(msgs []providers.Message) []string {
	out := []string{}
	for _, m := range msgs {
		for _, call := range m.ToolCalls {
			out = append(out, call.ID)
		}
	}
	return out
}

func recallParityResultIDs(msgs []providers.Message) []string {
	out := []string{}
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, m.ToolCallID)
		}
	}
	return out
}

func recallParityResultContents(msgs []providers.Message) []string {
	out := []string{}
	for _, m := range msgs {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

func recallParityCountContent(msgs []providers.Message, content string) int {
	n := 0
	for _, m := range msgs {
		if m.Content == content {
			n++
		}
	}
	return n
}

func recallParityAssemble(t *testing.T, h *cwR1Harness, ts *turnState, userMsg string) []providers.Message {
	t.Helper()
	out := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), userMsg, nil, nil)
	require.NoError(t, ts.contextWindowError(), "from-scratch assembly must not latch a storage or projection error")
	require.NotEmpty(t, out)
	require.Equal(t, "system", out[0].Role, "pinned system envelope precedes the span")
	return out
}

// TestAssembleMessages_PoisonedRecallSpanPassesWindowGroups is RED on the
// unfixed from-scratch path: assembleMessages appends the span raw, so
// validateWindowGroups still reports an incomplete tool-result group.
//
// On the fixed path this fixture is shortened. sanitizeHistoryIndexed drops
// the assistant whose three tool_call ids have no results and keeps the two
// user messages (context.go second pass; the same rule spliceRecallSpan
// uses). recordAssembledRecallSpan must store that kept length, 2, not the
// raw span length, 3. The valid-span control cannot tell those apart: there
// the sanitizer drops nothing, so both numbers are the span's own length.
func TestAssembleMessages_PoisonedRecallSpanPassesWindowGroups(t *testing.T) {
	h := cwR1New(t, 80_000)
	const liveUser = "live window is already a finished prose turn"
	h.append(t,
		providers.Message{Role: "user", Content: liveUser},
		providers.Message{Role: "assistant", Content: "live window answer, no tool calls"},
	)
	// Dangling rewritten assistant: recall_<archiveIdx>_<n> with archiveIdx 0
	// and no tool results. This is the mid-group freeze buildRecallSpanMessages
	// stores when the in-flight tail has declared calls but no results yet.
	dangling := []providers.Message{
		{Role: "user", Content: recallParityMarker},
		{Role: "user", Content: "recalled in-flight user"},
		{Role: "assistant", Content: "mid-group tool calls", ToolCalls: []providers.ToolCall{
			recallParityCall("recall_0_0", "list_tasks", `{}`),
			recallParityCall("recall_0_1", "recall_conversation", `{"turn_range":"1-1"}`),
			recallParityCall("recall_0_2", "recall_memory", `{"query":"goal"}`),
		}},
	}
	span := newRecallSpan(1, 1, dangling, []int{1})
	h.al.setRecallSpan(h.key, span)

	ts := h.turn("later turn")
	assembled := recallParityAssemble(t, h, ts, "later turn")
	require.Equal(t, 1, recallParityCountContent(assembled, recallParityMarker),
		"the span's demarcation marker is kept; assembly must include the span, not return the live window alone")
	require.NoError(t, validateWindowGroups(assembled),
		"from-scratch assembly must yield a provider-valid sequence, the same bar spliceRecallSpan already meets")
	require.Equal(t, []string{}, recallParityDeclaredIDs(assembled),
		"the dangling rewritten calls are dropped, not left for a loosened validator to ignore")
	require.Equal(t, []string{}, recallParityResultIDs(assembled),
		"a dangling assistant contributes no partial tool results")

	// Kept payload: the two user messages, in order, at the leading-user
	// offset (pinned system, then the live user, then the span). The
	// unanswered assistant is not among them. Raw span length is 3.
	const poisonedRecallKeptLen = 2
	const poisonedRecallRawLen = 3
	kept := []providers.Message{
		{Role: "user", Content: recallParityMarker},
		{Role: "user", Content: "recalled in-flight user"},
	}
	require.Equal(t, poisonedRecallRawLen, len(dangling),
		"fixture raw span length stays 3 (marker, recalled user, unanswered assistant) so kept and raw differ")
	require.Len(t, assembled, 2+poisonedRecallKeptLen+2,
		"pinned system, leading live user, kept span, remaining live assistant, current user")
	require.Equal(t, kept, assembled[2:2+poisonedRecallKeptLen],
		"kept span is the marker and the recalled user; the unanswered assistant is not in the slice")
	require.Equal(t, 0, recallParityCountContent(assembled, "mid-group tool calls"),
		"the unanswered assistant text is removed, not blanked and left in the slice")

	ts.mu.RLock()
	gotLen := ts.injectedRecallLen
	ts.mu.RUnlock()
	require.Equal(t, poisonedRecallKeptLen, gotLen,
		"recorded length must be the kept span (2: marker + recalled user), not the raw span length 3; recording the raw length removes the wrong block at the tool-result site")
}

// TestNextTurnAfterMidGroupRecallConversationReachesProvider drives the real
// recall_conversation tool against an archive whose latest turn is still
// mid-group, completes that live group afterwards, then sends turn N+1.
// RED today: prepareCallMessages latches the incomplete-group refusal and
// callProviderOnce returns it with zero HTTP requests.
func TestNextTurnAfterMidGroupRecallConversationReachesProvider(t *testing.T) {
	h := cwR1New(t, 80_000)
	const (
		liveUser     = "please continue the conformance goal"
		narration    = "checking tasks, conversation, and memory"
		resultTasks  = "live result tasks: none"
		resultRecall = "live result recall: stored"
		resultMemory = "live result memory: none"
		closing      = "turn one finished in prose"
		nextUser     = "steer two: continue"
	)
	h.append(t,
		providers.Message{Role: "user", Content: liveUser},
		providers.Message{Role: "assistant", Content: narration, ToolCalls: []providers.ToolCall{
			recallParityCall("live-tasks", "list_tasks", `{}`),
			recallParityCall("live-recall", "recall_conversation", `{"turn_range":"1-1"}`),
			recallParityCall("live-memory", "recall_memory", `{"query":"goal"}`),
		}},
		providers.Message{Role: "tool", ToolCallID: "live-tasks", Content: resultTasks},
	)

	tool := NewRecallConversationTool(h.store, h.al)
	result := tool.Execute(tools.WithSessionKey(context.Background(), h.key), map[string]any{"turn_range": "1-1"})
	require.NotNil(t, result)
	require.Equal(t, recallParityReceipt, result.ForLLM, "contiguous turn 1–1 receipt, with no overflow suffix")
	span := h.al.activeRecallSpan(h.key)
	require.NotNil(t, span, "the tool stores the span that later turns assemble")
	require.Equal(t, 1, span.FromTurn)
	require.Equal(t, 1, span.ToTurn)
	require.Equal(t, []int{1}, span.Ordinals)

	// The live window becomes a complete group only after the span is frozen.
	// Turn N+1 therefore fails today because of the span, not because of the archive.
	h.append(t,
		providers.Message{Role: "tool", ToolCallID: "live-recall", Content: resultRecall},
		providers.Message{Role: "tool", ToolCallID: "live-memory", Content: resultMemory},
		providers.Message{Role: "assistant", Content: closing},
	)

	ts := h.turn(nextUser)
	assembled := recallParityAssemble(t, h, ts, nextUser)
	recorder, provider := cwR1OpenAI(t, 0)
	rr := cwR1Flow(h, ts, assembled, provider)
	rr.rq.prepareCallMessages()
	require.NoError(t, ts.contextWindowError(),
		"turn N+1 after a mid-group recall_conversation must not refuse with an incomplete tool-result group")
	response, err := rr.rq.ri.rf.rt.callProviderOnce(rr.rq.ri.rf.callMessages, nil)
	require.NoError(t, err, "the send boundary must reach the provider")
	require.NotNil(t, response)
	require.Equal(t, "r1-success", response.Content, "fixture provider progress, same seam as the orphan-recovery controls")
	bodies := recorder.requests(t)
	require.Len(t, bodies, 1, "the real HTTP endpoint receives exactly the later turn")
	received := cwR1Messages(t, bodies[0])
	require.NoError(t, validateWindowGroups(received), "bytes that reached the provider are a complete tool-result sequence")
	require.Equal(t, []string{"live-tasks", "live-recall", "live-memory"}, recallParityDeclaredIDs(received),
		"the completed live group is sent once; rewritten mid-group ids are not")
	require.Equal(t, []string{"live-tasks", "live-recall", "live-memory"}, recallParityResultIDs(received))
	require.Equal(t, []string{resultTasks, resultRecall, resultMemory}, recallParityResultContents(received))
}

// TestAssembleMessages_ValidRecallToolGroupSurvivesIntact is the anti-erasure
// control. A valid tool-result group inside a recall span must come through
// from-scratch assembly with the same members, the same order, the same span
// pointer, and no second copy. Green on the unfixed tree; a fix that merely
// drops tool data fails here.
func TestAssembleMessages_ValidRecallToolGroupSurvivesIntact(t *testing.T) {
	h := cwR1New(t, 80_000)
	const (
		liveUser   = "live window user"
		liveAnswer = "live window answer, no tool calls"
		nextUser   = "turn after a valid recall"
		narration  = "valid recalled assistant"
		resultA    = "valid result alpha"
		resultB    = "valid result beta"
	)
	h.append(t,
		providers.Message{Role: "user", Content: liveUser},
		providers.Message{Role: "assistant", Content: liveAnswer},
	)
	valid := []providers.Message{
		{Role: "user", Content: recallParityMarker},
		{Role: "user", Content: "valid recalled user"},
		{Role: "assistant", Content: narration, ToolCalls: []providers.ToolCall{
			recallParityCall("recall_0_0", "list_tasks", `{"scope":"alpha"}`),
			recallParityCall("recall_0_1", "recall_memory", `{"scope":"beta"}`),
		}},
		{Role: "tool", ToolCallID: "recall_0_0", Content: resultA},
		{Role: "tool", ToolCallID: "recall_0_1", Content: resultB},
	}
	span := newRecallSpan(1, 1, valid, []int{1})
	h.al.setRecallSpan(h.key, span)

	ts := h.turn(nextUser)
	assembled := recallParityAssemble(t, h, ts, nextUser)
	require.NoError(t, validateWindowGroups(assembled), "a complete recalled group is a valid request")

	// Index 2: pinned system, then the leading live user, then the span,
	// then the one remaining live assistant, then the current user.
	require.Len(t, assembled, 2+len(valid)+2)
	require.Equal(t, providers.Message{Role: "user", Content: liveUser}, assembled[1])
	require.Equal(t, valid, assembled[2:2+len(valid)], "span members, order, and fields are unchanged")
	require.Equal(t, 1, recallParityCountContent(assembled, narration), "the recalled assistant is not duplicated")
	require.Equal(t, 1, recallParityCountContent(assembled, resultA))
	require.Equal(t, 1, recallParityCountContent(assembled, resultB))
	require.Equal(t, []string{"recall_0_0", "recall_0_1"}, recallParityDeclaredIDs(assembled))
	require.Equal(t, []string{"recall_0_0", "recall_0_1"}, recallParityResultIDs(assembled))
	require.Equal(t, []string{resultA, resultB}, recallParityResultContents(assembled))
	require.Equal(t, providers.Message{Role: "assistant", Content: liveAnswer}, assembled[2+len(valid)],
		"the live window resumes immediately after the span, once")
	require.Equal(t, providers.Message{Role: "user", Content: nextUser}, assembled[len(assembled)-1])

	ts.mu.RLock()
	gotSpan := ts.injectedRecallSpan
	gotAt := ts.injectedRecallAt
	gotLen := ts.injectedRecallLen
	ts.mu.RUnlock()
	require.Same(t, span, gotSpan, "assembly records the same RecallSpan pointer it spliced")
	require.Equal(t, 2, gotAt, "recorded span offset matches the leading-user contract")
	require.Equal(t, len(valid), gotLen, "a fully valid span is recorded at its full length, not truncated")
}
