//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Scenario 8; MAJ-CW-012: a REAL HTTP context rejection forces relief even
// below both local bounds. Whole recall leaves before the oldest whole step.
func TestCWSlideR1_RejectionDropsRecallThenOneWholeStep(t *testing.T) {
	h := cwR1New(t, 80_000)
	ts := h.turn("original user instructions")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	for _, id := range []string{"force-old-a", "force-old-b", "force-newest"} {
		h.append(t, cwR1Step(id, "narration for "+id, strings.Repeat("retained source ", 180))...)
	}
	archive := h.archive(t)
	recall := make([]providers.Message, 0, 3)
	recall = append(recall, providers.Message{Role: "user", Content: "recalled original context"})
	recall = append(recall, cwR1Step("recall_one", "literal recalled narration", strings.Repeat("recalled text ", 180))...)
	h.al.setRecallSpan(h.key, newRecallSpan(1, 1, recall, []int{1}))
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	cwR1Fits(t, h, messages)
	r, p := cwR1OpenAI(t, 2) // Original existing allowance: two retries, three sends.
	rr := cwR1Flow(h, ts, messages, p)
	cwR1Retry(t, rr)
	require.NoError(t, rr.rq.ri.rf.err, "third smaller request must succeed under original retry counter")
	requests := r.requests(t)
	require.Len(t, requests, 3, "original two-retry ceiling, never reset by each relief operation")
	first, second, third := cwR1Messages(t, requests[0]), cwR1Messages(t, requests[1]), cwR1Messages(t, requests[2])
	require.True(t, cwR1HasCall(first, "recall_one"), "instrument: rejected request actually contains the whole recall span")
	require.False(t, cwR1HasCall(second, "recall_one"), "first forced operation removes the ENTIRE recall span")
	require.True(t, cwR1HasCall(second, "force-old-a"), "stop at first progressing candidate: do not also slide a real step")
	require.False(t, cwR1HasCall(third, "force-old-a"), "next rejection removes ONE oldest complete assistant/result step")
	require.True(t, cwR1HasCall(third, "force-old-b"), "do not batch-drop the second older step after progress exists")
	cwR1Result(t, third, "force-newest")
	for i, sent := range [][]providers.Message{first, second, third} {
		cwR1AssertComplete(t, sent)
		cwR1AssertAnchor(t, sent, archive[0].Message)
		if i > 0 {
			require.Less(t, cwR1RetainedBytes(t, sent), cwR1RetainedBytes(t, [][]providers.Message{first, second, third}[i-1]),
				"strict final retained serialized payload progress, not notice/metadata changes")
		}
	}
	require.Equal(t, archive, h.archive(t), "reactive relief leaves literal admitted archive bytes unchanged")
}

// The original source is deliberately non-repeating Unicode so an arbitrary
// substring, bytes-based split, odd-rune swap or second cap cannot look correct.
func TestCWSlideR1_RejectionHalvesNewestSourceRunesExactly(t *testing.T) {
	for _, sourceRunes := range []int{4_095, 4_096} {
		t.Run(fmt.Sprintf("source_%d", sourceRunes), func(t *testing.T) {
			h := cwR1New(t, 80_000)
			ts := h.turn("one newest step")
			full := cwR1Unicode(sourceRunes)
			h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
			h.append(t, cwR1Step("halve-first", "exact assistant narration", full)...)
			archive := h.archive(t)
			messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			cwR1Fits(t, h, messages)
			r, p := cwR1OpenAI(t, 2)
			rr := cwR1Flow(h, ts, messages, p)
			cwR1Retry(t, rr)
			require.NoError(t, rr.rq.ri.rf.err, "newest content is mutable despite locally fitting estimates")
			requests := r.requests(t)
			require.Len(t, requests, 3, "two exact halving operations, not one unchanged retry")
			kept := sourceRunes
			for i := 1; i < 3; i++ {
				kept /= 2 // MAJ-CW-012: floor(k/2), NOT half of the rendered mark.
				sent := cwR1Messages(t, requests[i])
				cwR1AssertProjection(t, cwR1Result(t, sent, "halve-first").Content, full, "halve-first", 2, kept)
				cwR1AssertComplete(t, sent)
				var calls []providers.ToolCall
				for _, message := range sent {
					if message.Role == "assistant" {
						calls = append(calls, message.ToolCalls...)
					}
				}
				require.Equal(t, []providers.ToolCall{cwR1Call("halve-first")}, calls,
					"source relief cannot change any retained call id/name/type/arguments")
				require.Less(t, cwR1RetainedBytes(t, sent), cwR1RetainedBytes(t, cwR1Messages(t, requests[i-1])), "each retried retained payload is actually smaller")
			}
			require.Equal(t, archive, h.archive(t), "Unicode full source remains archived verbatim")
			last := cwR1Result(t, cwR1Messages(t, requests[2]), "halve-first")
			h.cfg.Context.BuiltinSuccessCap = 150_000 // Changed admission policy cannot inflate an exact pressure limit.
			reopened, err := session.NewUnifiedStore(h.dir)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			h.agent.Sessions = reopened
			reloaded := h.al.assembleMessages(context.Background(), h.turn(ts.userMessage), h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			require.Equal(t, last, cwR1Result(t, reloaded, "halve-first"), "exact partial pressure bytes survive policy change and a real store reload")
		})
	}
}

func TestCWSlideR1_RepeatedRejectionKeepsOriginalCeilingAndLastError(t *testing.T) {
	h := cwR1New(t, 80_000)
	ts := h.turn("retry ceiling")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	h.append(t, cwR1Step("ceiling-newest", "", cwR1Unicode(8_192))...)
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	cwR1Fits(t, h, messages)
	r, p := cwR1OpenAI(t, 20)
	rr := cwR1Flow(h, ts, messages, p)
	cwR1Retry(t, rr)
	require.ErrorContains(t, rr.rq.ri.rf.err, "cw-r1-reject-3: context_length_exceeded", "last actual error at the original ceiling survives, not a local size error")
	require.Equal(t, CodeContextTooLong, TranslateTurnError(rr.rq.ri.rf.err).Code, "provider context outcome stays context_too_long")
	requests := r.requests(t)
	require.Len(t, requests, 3, "no new allowance or reset when mutable text remains")
	for i := 1; i < 3; i++ {
		cwR1AssertProjection(t, cwR1Result(t, cwR1Messages(t, requests[i]), "ceiling-newest").Content,
			cwR1Unicode(8_192), "ceiling-newest", 2, 8_192>>i)
	}
}

// k=0 and k=1 have no candidate smaller than the original serialized source:
// an addressed mark would ADD bytes. Do not install it or spend a retry merely
// because metadata/a notice changed. In particular, k=1 is not a byte slice.
func TestCWSlideR1_NoProgressReturnsFirstRealRejectionWithoutResend(t *testing.T) {
	for _, source := range []string{"", "界"} {
		t.Run(fmt.Sprintf("source_runes_%d", utf8.RuneCountInString(source)), func(t *testing.T) {
			h := cwR1New(t, 80_000)
			ts := h.turn("no-progress immutable user")
			h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
			h.append(t, cwR1Step("no-progress", "immutable assistant narration", source)...)
			meta, archive := h.meta(t), h.archive(t)
			messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			cwR1Fits(t, h, messages)
			r, p := cwR1OpenAI(t, 20)
			rr := cwR1Flow(h, ts, messages, p)
			cwR1Retry(t, rr)
			require.Len(t, r.requests(t), 1, "no unchanged/mark-growth retry, even if provider might accept it")
			require.ErrorContains(t, rr.rq.ri.rf.err, "cw-r1-reject-1: context_length_exceeded", "the only genuine rejection is returned")
			require.Equal(t, CodeContextTooLong, TranslateTurnError(rr.rq.ri.rf.err).Code)
			require.Equal(t, meta, h.meta(t), "no progressing candidate means no installed metadata change")
			require.Equal(t, archive, h.archive(t))
		})
	}
}

func cwR1Retry(t *testing.T, rr *agentLoopRunTurnResponse) {
	t.Helper()
	cwR1Prepare(t, rr)
	require.Equal(t, agentLoopRunTurnResponseNext, rr.callLLMWithRetries(), "use real bounded recovery conductor")
}

func cwR1Fits(t *testing.T, h *cwR1Harness, messages []providers.Message) {
	t.Helper()
	require.LessOrEqual(t, requestTokens(messages, nil), agentContextBudget(h.agent), "instrument: rejected request locally fits B")
	require.LessOrEqual(t, toolResultShareTokens(messages), h.agent.ContextWindow/2, "instrument: rejected request locally fits default S=W/2")
}

func cwR1Unicode(n int) string {
	runes := make([]rune, n)
	for i := range runes {
		runes[i] = rune(0x4e00 + i) // Distinct valid Han code points; not encoded binary or sensitive content.
	}
	return string(runes)
}

// Contract oracle: AsyncAPI ToolResultRecallMark's closed shape and addressed
// full-source length. We never call the production mark/projection producer.
func cwR1AssertMark(t *testing.T, content, state, id string, line, size int) {
	t.Helper()
	cwR1AssertMarkAtTurn(t, content, state, id, line, size, 2)
}

func cwR1AssertMarkAtTurn(t *testing.T, content, state, id string, line, size, turn int) {
	t.Helper()
	require.True(t, utf8.ValidString(content), "mark/projection must remain valid Unicode")
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(content), &got), "complete addressed mark, not a truncated JSON fragment")
	hint, ok := got["hint"].(string)
	require.True(t, ok, "recall hint is required by ToolResultRecallMark")
	require.Contains(t, hint, "recall_conversation", "addressed recall remains possible")
	require.Contains(t, hint, fmt.Sprintf("tool_call_id=%q", id), "hint names original id")
	require.Contains(t, hint, fmt.Sprintf("archive_line=%d", line), "hint names original zero-based archive identity")
	delete(got, "hint") // Wording is not fixed by the contract; all other facts are exact.
	require.Equal(t, map[string]any{
		"error": "tool_result_recall_mark", "tool": "r1_tool", "tool_call_id": id,
		"archive_line": float64(line), "size_chars": float64(size), "turn": float64(turn), "content_state": state,
	}, got, "closed addressed-mark facts derive from the archive and contract")
}

func cwR1AssertProjection(t *testing.T, content, full, id string, line, kept int) {
	t.Helper()
	require.True(t, utf8.ValidString(content), "head/tail never split UTF-8 bytes")
	if kept == 0 {
		cwR1AssertMark(t, content, "emptied", id, line, utf8.RuneCountInString(full))
		return
	}
	start, end := strings.Index(content, "\n{"), strings.LastIndex(content, "}\n")
	require.GreaterOrEqual(t, start, 0, "capped projection has an intact addressed mark after its head")
	require.Greater(t, end, start, "capped projection has a tail after the intact mark")
	source := []rune(full)
	require.Equal(t, string(source[:(kept+1)/2]), content[:start], "MAJ-CW-012: head gets ceil(kept/2) ORIGINAL source runes")
	require.Equal(t, string(source[len(source)-kept/2:]), content[end+2:], "tail gets floor(kept/2) ORIGINAL source runes")
	cwR1AssertMark(t, content[start+1:end+1], "capped", id, line, len(source))
}

func cwR1RetainedBytes(t *testing.T, messages []providers.Message) int {
	t.Helper()
	// These large-removal fixtures require the WHOLE final message array to
	// shrink: a sufficient progress witness even counting transient notices.
	// Never discard every system message: required breadcrumbs live there.
	// Exact notice-only exclusion at the byte boundary belongs to unit 3;
	// this conservative witness is not a claim to test that carrier's format.
	data, err := json.Marshal(messages)
	require.NoError(t, err)
	return len(data)
}
