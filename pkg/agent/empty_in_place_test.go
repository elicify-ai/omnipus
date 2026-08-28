// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// empty_in_place_test.go — ADR-066 D5 (T066-12): empty in place, persisted
// projection, turn-start restore point.
//
// Spec: docs/internal/specs/adr-066-context-overflow-spec.md — FR-017,
// FR-019, FR-020, FR-022, FR-023; B-21, B-21b, B-22, B-23, B-24, B-27b;
// tests 32 (TestRunTurn_LiveVsReloadBytesEqual) and 33
// (TestRunTurn_AbortRestoresTurnStartTriple); SC-003, SC-010.

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// bigResultTool is a tools.Tool whose every call returns `size` runes, so a
// turn of N calls fills the window by a known amount.
type bigResultTool struct {
	tools.BaseTool
	size int
}

func (b *bigResultTool) Name() string        { return "big_tool" }
func (b *bigResultTool) Description() string { return "T066-12 stub — returns a large result" }
func (b *bigResultTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{
		"tag": map[string]any{"type": "string"},
	}}
}
func (b *bigResultTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (b *bigResultTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	tag, _ := args["tag"].(string)
	// Prose, not a run of one letter: the loop's base64-looking-payload
	// filter would otherwise replace the result before the choke point.
	return &tools.ToolResult{ForLLM: tag + ":" + strings.Repeat("lorem ipsum ", b.size/len("lorem ipsum "))}
}

func toolCallFor(id, tag string) providers.ToolCall {
	return providers.ToolCall{ID: id, Function: &providers.FunctionCall{Name: "big_tool", Arguments: `{"tag":"` + tag + `"}`}}
}

func msgsByToolCallID(msgs []providers.Message) map[string]providers.Message {
	out := map[string]providers.Message{}
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID != "" {
			out[m.ToolCallID] = m
		}
	}
	return out
}

// --- unit: eligibility and the pass itself -------------------------------

// TestEmptyInPlace_EligibilityAndOrder pins FR-017: oldest first, the floor
// set (every result of the LAST assistant step) is never eligible, results
// of an EARLIER turn still in the slice ARE eligible (B-21b), an orphaned
// result (call not in the slice) is skipped, an already-emptied entry is
// skipped, and a message off the archive (lineOf < 0) is skipped.
func TestEmptyInPlace_EligibilityAndOrder(t *testing.T) {
	big := strings.Repeat("b", 2000)
	msgs := []providers.Message{
		{Role: "user", Content: "turn one"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c1", "one")}},
		{Role: "tool", ToolCallID: "c1", Content: big},
		{Role: "user", Content: "turn two"},
		{Role: "tool", ToolCallID: "orphan", Content: big}, // no call in the slice
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c2", "two")}},
		{Role: "tool", ToolCallID: "c2", Content: big},
		{Role: "tool", ToolCallID: "spliced", Content: big}, // off-archive (recall span)
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c3", "three"), toolCallFor("c4", "four")}},
		{Role: "tool", ToolCallID: "c3", Content: big},
		{Role: "tool", ToolCallID: "c4", Content: big},
	}
	archive := make([]memory.ArchivedMessage, len(msgs))
	for i, m := range msgs {
		archive[i] = memory.ArchivedMessage{Message: m}
	}
	lineOf := func(i int) int {
		if msgs[i].ToolCallID == "spliced" {
			return -1
		}
		return i
	}

	got := eligibleToolResults(msgs, lineOf, nil)
	assert.Equal(t, []int{2, 6}, got,
		"oldest-first; floor set {c3,c4} excluded; orphan and off-archive excluded; earlier turn's c1 eligible")

	already := memory.ProjectionSet{{ToolCallID: "c1", ArchiveLine: 2}: memory.ProjectionEmptied}
	assert.Equal(t, []int{6}, eligibleToolResults(msgs, lineOf, already), "an emptied entry is not re-emptied")

	// The pass stops as soon as the predicate holds (FR-021): with a target
	// satisfied after one empty, only c1 goes.
	work := append([]providers.Message(nil), msgs...)
	calls := 0
	emptied := emptyOldestFirst(work, got, lineOf, archive, func(m []providers.Message) bool {
		calls++
		return m[2].Content != big // "fits" once c1 is a mark
	})
	require.Len(t, emptied, 1)
	assert.Equal(t, "c1", emptied[0].ToolCallID)
	assert.Equal(t, 2, emptied[0].ArchiveLine)
	assert.Equal(t, "big_tool", emptied[0].Tool)
	assert.Equal(t, 2000, emptied[0].SizeChars)
	assert.Contains(t, work[2].Content, `"content_state":"emptied"`)
	assert.Equal(t, "c1", work[2].ToolCallID, "role, id and slot unchanged")
	assert.Equal(t, "tool", work[2].Role)
	assert.Equal(t, big, work[6].Content, "c2 untouched: the pass stopped at the target")
	assert.Equal(t, big, msgs[6].Content)

	// Never fits → every candidate goes, the floor set never does.
	work = append([]providers.Message(nil), msgs...)
	emptied = emptyOldestFirst(work, got, lineOf, archive, func([]providers.Message) bool { return false })
	require.Len(t, emptied, 2)
	assert.Equal(t, big, work[9].Content, "floor set intact")
	assert.Equal(t, big, work[10].Content, "floor set intact")
	assert.Equal(t, big, work[4].Content, "orphan intact")
	assert.Equal(t, big, work[7].Content, "off-archive intact")

	// Already fits → nothing happens, no mark is built.
	work = append([]providers.Message(nil), msgs...)
	assert.Empty(t, emptyOldestFirst(work, got, lineOf, archive, func([]providers.Message) bool { return true }))
	assert.Equal(t, msgs, work)
}

// TestEmptyInPlace_MarkSizeFromArchiveLine pins the B-22 invariant at the
// unit level: when the pass is handed the CAPPED in-memory copy of a result
// (the mid-turn slice, T066-13), the mark still names the FULL size from
// the archive line and is byte-identical to what projection.go produces on
// reload for the same state.
func TestEmptyInPlace_MarkSizeFromArchiveLine(t *testing.T) {
	full := strings.Repeat("f", 50_000)
	archive := []memory.ArchivedMessage{
		{Message: providers.Message{Role: "user", Content: "q"}},
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c1", "a")}}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c1", Content: full}},
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c2", "b")}}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c2", Content: "small"}},
	}
	live := []providers.Message{
		archive[0].Message, archive[1].Message,
		{Role: "tool", ToolCallID: "c1", Content: "CAPPED FORM (much shorter)"},
		archive[3].Message, archive[4].Message,
	}
	lineOf := midTurnLineResolver(archive, live)
	assert.Equal(t, 2, lineOf(2))
	assert.Equal(t, 4, lineOf(4))
	assert.Equal(t, -1, lineOf(1), "an assistant message has no result line")

	emptied := emptyOldestFirst(live, eligibleToolResults(live, lineOf, nil), lineOf, archive,
		func([]providers.Message) bool { return false })
	require.Len(t, emptied, 1)
	assert.Equal(t, 50_000, emptied[0].SizeChars)
	assert.Contains(t, live[2].Content, `"size_chars":50000`)

	reload := make([]providers.Message, len(archive))
	for i := range archive {
		reload[i] = archive[i].Message
	}
	set := memory.ProjectionSet{{ToolCallID: "c1", ArchiveLine: 2}: memory.ProjectionEmptied}
	projected := projectMessages(reload, func(i int) int { return i }, set, projectionContext{
		policy: capPolicyFor(config.DefaultContextSettings(), 100_000), archive: archive,
	})
	assert.Equal(t, live[2].Content, projected[2].Content, "live bytes == reload bytes")
}

// --- integration: the real loop, the real JSONL store ---------------------

// liveReloadHarness runs one scripted turn of four big_tool calls against a
// window small enough that the trim site's floor path (register #3 /
// B-21b) must empty the two oldest results, and returns what it needs for
// the B-22 / B-24 assertions.
type liveReloadHarness struct {
	al         *AgentLoop
	agent      *AgentInstance
	provider   *testutil.ScenarioProvider
	store      *session.UnifiedStore
	sessionID  string
	sessionKey string
}

func newLiveReloadHarness(t *testing.T, home string, provider *testutil.ScenarioProvider) *liveReloadHarness {
	t.Helper()
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 2000
	cfg.Agents.Defaults.MaxToolIterations = 10
	// ADR-066 D2: the ladder's global default pins W = 40,000 → B ≈ 36,000
	// estimator tokens minus the pinned core; four 30,000-char results
	// (12,000 tokens each) overflow it, two empties bring it back.
	cfg.Context.DefaultContextWindow = intPtr(40_000)

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	mia := registerAgent(t, al, home, "mia", provider, true)
	al.RegisterTool(&bigResultTool{size: 30_000})
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"big_tool": "allow"},
	})
	return &liveReloadHarness{al: al, agent: mia, provider: provider, store: al.GetSessionStore()}
}

// scriptedOverflowTurn is the provider script shared by tests 32 and 33:
// four single-call steps, then a context-overflow error (which runs
// windowTrim at the Site-4 retry trim — the floor keeps the whole current
// turn, so D5 empties its oldest results), then the final text.
func scriptedOverflowTurn() *testutil.ScenarioProvider {
	return testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("call-1", "one")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("call-2", "two")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("call-3", "three")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("call-4", "four")}).
		WithError(errors.New("status 400: context_length_exceeded")).
		WithText("done")
}

func (h *liveReloadHarness) runScheduled(t *testing.T, prompt string) (string, error) {
	t.Helper()
	meta, err := h.store.NewScheduledSession("mia")
	require.NoError(t, err)
	h.sessionID = meta.ID
	h.sessionKey = "agent:mia:session:" + meta.ID
	return h.al.ProcessScheduled(context.Background(), "mia", meta.ID, prompt, "scheduled", meta.ID)
}

// TestRunTurn_LiveVsReloadBytesEqual — spec test 32, B-22 / SC-010, with
// B-21b (the floor-kept oversized turn is emptied oldest-first, Skip does
// not move), B-23 (no archive line changes) and B-27b (counter + state).
func TestRunTurn_LiveVsReloadBytesEqual(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	h := newLiveReloadHarness(t, home, scriptedOverflowTurn())

	before := ContextEmptiesTotal()
	reply, err := h.runScheduled(t, "fill the window")
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	require.Equal(t, 6, h.provider.CallCount(), "4 tool steps + the overflow + the retry")

	// Live: the bytes the provider received on the retried call.
	live := msgsByToolCallID(h.provider.LastMessages())
	require.Len(t, live, 4)
	assert.Contains(t, live["call-1"].Content, `"content_state":"emptied"`, "oldest emptied first")
	assert.Contains(t, live["call-2"].Content, `"content_state":"emptied"`)
	assert.Contains(t, live["call-1"].Content, `"size_chars":30004`, "tag + 30,000 chars of prose")
	assert.True(t, strings.HasPrefix(live["call-3"].Content, "three:"), "target reached after two: third intact")
	assert.True(t, strings.HasPrefix(live["call-4"].Content, "four:"), "the floor set is never emptied")
	assert.Equal(t, before+2, ContextEmptiesTotal(), "context_empties_total counts the two (B-27b)")

	// State persisted keyed (id, line) → emptied; Skip untouched (an empty is
	// not a cut — mid-turn, the window's message count is unchanged).
	pm := h.agent.Sessions.Projection(h.sessionKey)
	archive, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	emptiedLines := map[string]int{}
	for k, v := range pm.Entries {
		if v == memory.ProjectionEmptied {
			emptiedLines[k.ToolCallID] = k.ArchiveLine
		}
	}
	require.Len(t, emptiedLines, 2, "got %v", pm.Entries)
	assert.Equal(t, "call-1", archive[emptiedLines["call-1"]].ToolCallID, "the key's line IS the result's line")
	assert.Equal(t, "call-2", archive[emptiedLines["call-2"]].ToolCallID)
	assert.Len(t, h.agent.Sessions.GetHistory(h.sessionKey), len(archive), "Skip did not move (B-21b)")

	// B-23: the archive still holds every full result.
	for _, line := range archive {
		if line.Role == "tool" {
			assert.Greater(t, len(line.Content), 30_000, "archive line %s untouched", line.ToolCallID)
		}
	}

	// Reload: a fresh process over the same home assembles the session.
	h2 := newLiveReloadHarness(t, home, testutil.NewScenario())
	h2.sessionKey = h.sessionKey
	ts := newTurnState(h2.agent, processOptions{SessionKey: h.sessionKey}, turnEventScope{turnID: "reload"})
	reloaded := msgsByToolCallID(h2.al.assembleMessages(
		context.Background(), ts, h2.agent.Sessions.GetHistory(h.sessionKey), "", nil, nil))
	require.Len(t, reloaded, 4)
	for _, id := range []string{"call-1", "call-2", "call-3", "call-4"} {
		assert.Equal(t, live[id].Content, reloaded[id].Content, "SC-010: live bytes == reload bytes for %s", id)
		assert.Equal(t, live[id].ToolCallID, reloaded[id].ToolCallID)
	}

	// FR-022: the transcript carries content_state emptied + the projected
	// result for the two, and nothing for the other two.
	entries, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	states := map[string]session.ToolCall{}
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			states[string(tc.ID)] = tc
		}
	}
	require.Len(t, states, 4)
	assert.Equal(t, "emptied", states["call-1"].ContentState)
	assert.Equal(t, live["call-1"].Content, states["call-1"].Result["text"], "transcript result = projected content")
	assert.Equal(t, "emptied", states["call-2"].ContentState)
	assert.Equal(t, "", states["call-3"].ContentState)
	assert.Equal(t, "", states["call-4"].ContentState)
}

// TestRunTurn_AbortRestoresTurnStartTriple — spec test 33, B-24 / SC-003:
// after TWO emptying passes inside one turn, an abort restores archive
// length, Skip and the projection set to their TURN-START values — never an
// intermediate one — and the transcript follows.
func TestRunTurn_AbortRestoresTurnStartTriple(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Turn A completes normally and leaves two results emptied: that is
	// the turn-start state turn B must come back to.
	h := newLiveReloadHarness(t, home, scriptedOverflowTurn())
	_, err := h.runScheduled(t, "turn A")
	require.NoError(t, err)
	archiveA, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	skipA := len(archiveA) - len(h.agent.Sessions.GetHistory(h.sessionKey))
	setA := h.agent.Sessions.Projection(h.sessionKey).Entries.Clone()
	require.Len(t, setA, 2, "precondition: turn A emptied two results")
	transcriptA, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)

	// Turn B: the same session, a new turn state, two more passes driven
	// directly through the D5 entry point (the mid-turn caller, T066-13)
	// on this turn's appended results, then an abort.
	ts := newTurnState(h.agent, processOptions{
		SessionKey:          h.sessionKey,
		TranscriptSessionID: h.sessionID,
		TranscriptStore:     h.store,
	}, turnEventScope{turnID: "turn-b"})
	require.Equal(t, len(archiveA), ts.initialArchiveLen)
	require.Equal(t, setA, ts.initialEmptiedSet, "captured once at turn start")

	appendInTurnMessages(h.al, h.sessionKey, []providers.Message{
		{Role: "user", Content: "turn B"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("b-1", "b1")}},
		{Role: "tool", ToolCallID: "b-1", Content: strings.Repeat("1", 30_000)},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("b-2", "b2")}},
		{Role: "tool", ToolCallID: "b-2", Content: strings.Repeat("2", 30_000)},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("b-3", "b3")}},
		{Role: "tool", ToolCallID: "b-3", Content: strings.Repeat("3", 30_000)},
	})
	for _, id := range []string{"b-1", "b-2", "b-3"} {
		require.NoError(t, h.store.AppendTranscript(h.sessionID, session.TranscriptEntry{
			ID: "entry-" + id, Type: session.EntryTypeToolCall, AgentID: "mia",
			ToolCalls: []session.ToolCall{{ID: session.ToolCallID(id), Tool: "big_tool", Status: "success",
				Result: map[string]any{"text": "full " + id}}},
		}))
	}
	window := h.agent.Sessions.GetHistory(h.sessionKey)
	archive, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	lineOf := midTurnLineResolver(archive, window)

	// Oldest-first across the WHOLE window (B-21b): turn A's two surviving
	// results (call-3, call-4) are older than this turn's, so pass 1 empties
	// call-3 and pass 2 empties call-4 — results of an EARLIER, completed
	// turn, emptied during turn B, with archive lines BELOW the turn-start
	// archive length. That is exactly the case the rollback must undo by
	// restoring the SET, not by truncating lines.
	oneEmpty := func(want string) func([]providers.Message) bool {
		return func(m []providers.Message) bool {
			return strings.Contains(msgsByToolCallID(m)[want].Content, `"content_state":"emptied"`)
		}
	}
	e1 := h.al.emptyInPlace(ts, h.agent, h.sessionKey, window, lineOf, archive, oneEmpty("call-3"), emptyingSiteMidTurn)
	require.Len(t, e1, 1)
	assert.Equal(t, "call-3", e1[0].ToolCallID)
	e2 := h.al.emptyInPlace(ts, h.agent, h.sessionKey, window, lineOf, archive, oneEmpty("call-4"), emptyingSiteMidTurn)
	require.Len(t, e2, 1)
	assert.Equal(t, "call-4", e2[0].ToolCallID)
	assert.Less(t, e2[0].ArchiveLine, ts.initialArchiveLen, "an earlier turn's line")
	mid := h.agent.Sessions.Projection(h.sessionKey).Entries
	require.Len(t, mid, 4, "intermediate state: turn A's two + the two emptied now")
	tb, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	midStates := map[string]string{}
	for _, e := range tb {
		for _, tc := range e.ToolCalls {
			midStates[string(tc.ID)] = tc.ContentState
		}
	}
	assert.Equal(t, "emptied", midStates["call-3"], "transcript followed the pass")
	assert.Equal(t, "emptied", midStates["call-4"])

	// Abort.
	require.NoError(t, ts.restoreSession(h.agent))

	archiveAfter, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	assert.Len(t, archiveAfter, len(archiveA), "archive length back to turn start")
	assert.Equal(t, skipA, len(archiveAfter)-len(h.agent.Sessions.GetHistory(h.sessionKey)), "Skip back to turn start")
	assert.Equal(t, setA, h.agent.Sessions.Projection(h.sessionKey).Entries,
		"projection set back to the TURN-START set, not the intermediate one")
	assert.Equal(t, setA, ts.initialEmptiedSet, "the captured triple never moved")

	transcriptAfter, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	require.Len(t, transcriptAfter, len(transcriptA)+3)
	assert.Equal(t, transcriptA, transcriptAfter[:len(transcriptA)],
		"turn A's records back to their turn-start state (call-3/call-4 reverted, call-1/call-2 still emptied)")
}

// --- reused tool_call_ids (B-29b) ----------------------------------------

// TestEligibleToolResults_FloorIsIndexKeyedNotIDKeyed pins FR-031's floor as
// "the results of the LAST assistant step", addressed by slice INDEX.
//
// The regression: the floor was a set of bare tool_call_id strings. Providers
// reuse ids — memory.ProjectionKey is composite precisely because "providers
// reuse ids such as call_0 on every turn" (B-29b) — so with a provider that
// numbers calls per message, `floor = {call_0}` excluded EVERY older call_0
// result in the window. Nothing was eligible, emptyInPlace emptied nothing,
// and D6's thrash guard ended the turn with ErrContextUnrecoverable while
// large, evictable results sat right there in the window.
func TestEligibleToolResults_FloorIsIndexKeyedNotIDKeyed(t *testing.T) {
	big := strings.Repeat("b", 2000)
	// A provider that restarts its numbering on every assistant message.
	msgs := []providers.Message{
		{Role: "user", Content: "turn one"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("call_0", "one")}},
		{Role: "tool", ToolCallID: "call_0", Content: big},
		{Role: "user", Content: "turn two"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("call_0", "two")}},
		{Role: "tool", ToolCallID: "call_0", Content: big},
		{Role: "user", Content: "turn three"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("call_0", "three")}},
		{Role: "tool", ToolCallID: "call_0", Content: big},
	}
	lineOf := func(i int) int { return i }

	got := eligibleToolResults(msgs, lineOf, nil)
	assert.Equal(t, []int{2, 5}, got,
		"only the LAST step's result (index 8) is floor; the two older call_0 results are eligible")
}

// TestMidTurnLineResolver_ReusedIDsResolvePositionally pins the composite
// address D5 persists under.
//
// The regression: the resolver returned the MOST RECENT archive line
// carrying a message's tool_call_id, so two window messages sharing an id
// both resolved to the newer line. Emptying the OLDER one then (a) built its
// mark from the newer line — wrong size, wrong content behind the recall
// key — and (b) persisted (id, newerLine) → emptied, so the reload
// projection (archiveLineResolver, index-exact) blanked the NEWER result
// while the older one came back at full content. Live and reload disagreed,
// breaking B-22, and the wrong result was destroyed.
func TestMidTurnLineResolver_ReusedIDsResolvePositionally(t *testing.T) {
	turn1 := strings.Repeat("1", 100)
	turn2 := strings.Repeat("2", 200)
	turn3 := strings.Repeat("3", 300)
	archive := []memory.ArchivedMessage{
		{Message: providers.Message{Role: "user", Content: "one"}},                                                   // 0
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("call_0", "a")}}}, // 1
		{Message: providers.Message{Role: "tool", ToolCallID: "call_0", Content: turn1}},                             // 2
		{Message: providers.Message{Role: "user", Content: "two"}},                                                   // 3
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("call_0", "b")}}}, // 4
		{Message: providers.Message{Role: "tool", ToolCallID: "call_0", Content: turn2}},                             // 5
		{Message: providers.Message{Role: "user", Content: "three"}},                                                 // 6
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("call_1", "c")}}}, // 7
		{Message: providers.Message{Role: "tool", ToolCallID: "call_1", Content: turn3}},                             // 8
	}
	live := make([]providers.Message, len(archive))
	for i := range archive {
		live[i] = archive[i].Message
	}

	lineOf := midTurnLineResolver(archive, live)
	assert.Equal(t, 2, lineOf(2), "turn 1's call_0 result must resolve to ITS OWN line, not the newest call_0 line")
	assert.Equal(t, 5, lineOf(5), "turn 2's call_0 result resolves to line 5")
	assert.Equal(t, 8, lineOf(8))
	assert.Equal(t, -1, lineOf(0), "a user message has no result line")

	// It also agrees with the reload resolver, which is index-exact here.
	reloadLineOf := archiveLineResolver(archive, live)
	for _, i := range []int{2, 5, 8} {
		assert.Equal(t, reloadLineOf(i), lineOf(i),
			"the mid-turn and reload resolvers must address the same line for message %d", i)
	}

	// End to end: emptying the OLDEST eligible result must mark line 2 with
	// turn 1's size, not line 5 with turn 2's.
	work := append([]providers.Message(nil), live...)
	emptied := emptyOldestFirst(work, eligibleToolResults(work, lineOf, nil), lineOf, archive,
		func(m []providers.Message) bool { return m[2].Content != turn1 })
	require.Len(t, emptied, 1)
	assert.Equal(t, 2, emptied[0].ArchiveLine, "the persisted key must cite the message's OWN archive line")
	assert.Equal(t, len(turn1), emptied[0].SizeChars, "the mark must describe the content that was actually lost")
	assert.Equal(t, turn2, work[5].Content, "turn 2's newer result must be untouched")
}

// TestMidTurnLineResolver_OffArchiveMessageConsumesNoLine — a spliced recall
// span is not on the archive and must not steal a line from a real result.
func TestMidTurnLineResolver_OffArchiveMessageConsumesNoLine(t *testing.T) {
	archive := []memory.ArchivedMessage{
		{Message: providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c1", "a")}}},
		{Message: providers.Message{Role: "tool", ToolCallID: "c1", Content: "real"}},
	}
	live := []providers.Message{
		archive[0].Message,
		archive[1].Message,
		{Role: "tool", ToolCallID: "spliced", Content: "recall span"},
	}
	lineOf := midTurnLineResolver(archive, live)
	assert.Equal(t, -1, lineOf(2), "an off-archive message resolves to -1")
	assert.Equal(t, 1, lineOf(1), "and must not have consumed the real result's line")
}
