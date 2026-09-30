//go:build goolm && stdjson

package agent

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// Scenario 2; MAJ-CW-002/004/005: a cut is never sanitizer repair.
func TestCWSlideR1_IncompleteParallelStepCannotBeCut(t *testing.T) {
	h := cwR1New(t, 8_192)
	ts := h.turn("parallel results belong together")
	anchor := providers.Message{Role: "user", Content: ts.userMessage}
	calls := []providers.ToolCall{cwR1Call("parallel-a"), cwR1Call("parallel-b")}
	h.append(t, anchor, providers.Message{Role: "assistant", ToolCalls: calls},
		providers.Message{Role: "tool", ToolCallID: "parallel-a", Content: strings.Repeat("result A prose ", 800)})
	partial := h.check(t, ts, h.agent.Sessions.GetHistory(h.key))
	require.Zero(t, cwR1Skip(t, h), "an incomplete/newest group is not legally evictable")
	require.Len(t, partial, 3, "do not cut assistant or the first available result slot")
	require.Equal(t, calls, partial[1].ToolCalls, "both declared calls and exact arguments survive")
	cwR1Result(t, partial, "parallel-a")
	require.False(t, cwR1HasCall(partial[:1], "parallel-b"), "test fixture has one declaration, not repaired calls")
	require.Len(t, h.archive(t), 3, "checkpoint cannot manufacture a missing result")

	bResult := providers.Message{Role: "tool", ToolCallID: "parallel-b", Content: strings.Repeat("result B prose ", 600)}
	h.append(t, bResult)
	complete := h.check(t, ts, append(partial, bResult))
	require.Zero(t, cwR1Skip(t, h), "completed but still newest group is structural floor")
	cwR1AssertComplete(t, complete)

	// Once another step completes, the original two-call group can be slid as
	// ONE unit. Its results never get associated with some global reused id.
	b := agentContextBudget(h.agent)
	newStep := cwR1Step("next", strings.Repeat("n", b*3/2), "short final result")
	h.append(t, newStep...)
	out := h.check(t, ts, append(complete, newStep...))
	require.Greater(t, cwR1Skip(t, h), 0, "the now-older complete two-call step must become evictable")
	require.False(t, cwR1HasCall(out, "parallel-a"), "whole old assistant step leaves")
	require.False(t, cwR1HasCall(out, "parallel-b"), "both old declared calls leave together")
	for _, m := range out {
		require.NotEqual(t, "parallel-a", m.ToolCallID, "first old result leaves only with its whole group")
		require.NotEqual(t, "parallel-b", m.ToolCallID, "second old result leaves with the same group")
	}
	cwR1AssertComplete(t, out)
	require.Len(t, h.archive(t), 6, "all full source lines remain archived")
}

// Scenario 3; MAJ-CW-001/002/004, B-56: immutable residue is NOT a local stop.
func TestCWSlideR1_NewestSlotsReachMarkOnlyAndReloadExactly(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "success_surface"
		if failure {
			name = "failure_surface"
		}
		t.Run(name, func(t *testing.T) {
			h := cwR1New(t, 20_000)
			b := agentContextBudget(h.agent)
			user := providers.Message{Role: "user", Content: strings.Repeat("immutable user instruction ", b/5)}
			ts := h.turn(user.Content)
			h.append(t, user, providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{cwR1Call("newest")}})
			full := strings.Repeat("甲乙丙丁戊己 readable result\n", 240)
			admitted := h.al.admitToolResult(ts, toolResultAdmission{
				Tool: "r1_tool", ToolCallID: "newest", Content: full, ParallelN: 1, IsError: failure,
			})
			beforeArchive := h.archive(t)
			window := append([]providers.Message{user, {Role: "assistant", ToolCalls: []providers.ToolCall{cwR1Call("newest")}}}, admitted.Message)
			require.Greater(t, requestTokens(window, nil), b, "immutable user residue exceeds B even without newest source")
			out := h.check(t, ts, window)
			require.Len(t, out, 3, "shorten text, never remove newest call/result slots")
			require.Equal(t, window[1].ToolCalls, out[1].ToolCalls, "newest ids, names and arguments remain exact")
			projected := cwR1Result(t, out, "newest")
			cwR1AssertMark(t, projected.Content, "emptied", "newest", 2, utf8.RuneCountInString(full))
			require.Equal(t, beforeArchive, h.archive(t), "pressure never modifies full filtered archive lines")
			require.Equal(t, full, h.archive(t)[2].Content, "full Unicode source remains archived")

			// Reopen the REAL store and change BOTH success/failure admission caps
			// upward. Neither a reload nor a changed policy may inflate pressure.
			h.cfg.Context.BuiltinSuccessCap = 200_000
			h.cfg.Context.BuiltinFailureCap = 200_000
			reopened, err := memory.NewJSONLStore(h.dir)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, reopened.Close()) })
			h.agent.Sessions = session.NewJSONLBackend(reopened)
			reload := h.al.assembleMessages(context.Background(), h.turn(user.Content), h.agent.Sessions.GetHistory(h.key), "", nil, nil)
			require.Equal(t, projected, cwR1Result(t, reload, "newest"), "SC-010: live == reloaded exact pressure bytes and slots")
			r, p := cwR1OpenAI(t, 0)
			rr := cwR1Flow(h, ts, out, p)
			cwR1Send(t, rr)
			require.Len(t, r.requests(t), 1, "immutable residue still reaches the provider; no local size-only exit")
			cwR1AssertComplete(t, cwR1Messages(t, r.requests(t)[0]))
		})
	}
}

// Scenario 4; MAJ-CW-005: original user/media is a pinned VIEW, not a new line.
func TestCWSlideR1_AnchorIsOriginalVerbatimArchiveLine(t *testing.T) {
	h := cwR1New(t, 16_384)
	anchor := providers.Message{Role: "user", Content: "original user text\nretain whitespace and media verbatim ",
		Media: []string{"media://r1/front.png", "media://r1/back.png"}}
	transcript, err := session.NewUnifiedStore(filepath.Join(t.TempDir(), "sessions"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, transcript.Close()) })
	meta, err := transcript.NewScheduledSession(h.agent.ID)
	require.NoError(t, err)
	require.NoError(t, transcript.AppendTranscript(meta.ID, session.TranscriptEntry{
		ID: "r1-original-user-entry", Role: "user", Content: anchor.Content, AgentID: h.agent.ID,
	}))
	initialTranscript, err := transcript.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, initialTranscript, 1, "instrument: the real transcript contains its original user entry")
	ts := newTurnState(h.agent, processOptions{SessionKey: h.key, UserMessage: anchor.Content,
		Media: anchor.Media, Channel: "scheduled", TranscriptStore: transcript, TranscriptSessionID: meta.ID},
		turnEventScope{turnID: "anchor-turn"})
	h.append(t, anchor)
	b := agentContextBudget(h.agent)
	for _, id := range []string{"anchor-old-a", "anchor-old-b", "anchor-new"} {
		h.append(t, cwR1Step(id, strings.Repeat("n", b*5/4), "short result")...)
	}
	archive := h.archive(t)
	out := h.check(t, ts, h.agent.Sessions.GetHistory(h.key))
	require.Greater(t, cwR1Skip(t, h), 0, "slide passes the original user archive line")
	cwR1AssertAnchor(t, out, anchor)
	require.Equal(t, archive, h.archive(t), "anchor installation cannot append a duplicate archive line")
	entries, err := transcript.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var users []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Role == "user" {
			users = append(users, entry)
		}
	}
	require.Equal(t, initialTranscript, users, "the real transcript keeps exactly its original user entry, no duplicate anchor append")
	require.Equal(t, float64(len(archive)), h.meta(t)["count"], "Count remains the actual original archive count")
	without := append([]providers.Message(nil), out[1:]...)
	require.Equal(t, requestTokens(without, nil)+estimateMessageTokens(anchor), requestTokens(out, nil),
		"the one verbatim user/media anchor is counted once")

	reopened, err := memory.NewJSONLStore(h.dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	h.agent.Sessions = session.NewJSONLBackend(reopened)
	reloaded := h.agent.Sessions.GetHistory(h.key)
	cwR1AssertAnchor(t, reloaded, archive[0].Message)
	require.Equal(t, out, reloaded, "original identity/anchor survives storage-only reload, without a supplied user copy")
	require.Equal(t, archive, h.archive(t), "reopening cannot duplicate or rewrite the initiating user line")
	reloadedEntries, err := transcript.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Equal(t, entries, reloadedEntries, "context reload cannot append anything to the real transcript")
}

func cwR1AssertAnchor(t *testing.T, messages []providers.Message, anchor providers.Message) {
	t.Helper()
	copies := 0
	firstHistory := -1
	for i, m := range messages {
		if firstHistory < 0 && m.Role != "system" {
			firstHistory = i
		}
		if m.Role == "user" && m.Content == anchor.Content {
			copies++
			require.Equal(t, anchor, m, "anchor includes original verbatim text, media and message fields")
			require.Equal(t, firstHistory, i, "anchor must precede the retained suffix")
		}
	}
	require.Equal(t, 1, copies, "exactly one copy of the original user/media anchor")
}
