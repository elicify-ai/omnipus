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
	// ADR-066 D2: the ladder's global default pins W → B estimator tokens
	// minus the pinned core; four 30,000-char results (12,000 tokens each)
	// overflow it, two relief operations bring it back. CORRECTED 2026-10-01
	// (FR-030/MAJ-CW-004): those two operations are whole-step SLIDES, not
	// in-place empties — slideOldest runs before shortenNext in the relief
	// order, and a legal complete prefix is available on every firing for
	// this fixture, so shortenNext (the empty-in-place path) never fires.
	//
	// W was 40,000 (B ≈ 36,000) until the feat/library-improvements merge
	// (integrate/library-improvements-v0.1.1) unconditionally registered the
	// knowledge tool family for EVERY agent (registerKnowledgeTools,
	// instance.go — ADR-067 D7). That grows both budget sites' tool-surface
	// estimate: the mid-turn D6 check's compressed-manifest note
	// (manifestNoteTokens, midturn_budget.go) AND windowTrim's
	// sentToolSurfaceTokens (which additionally charges FULL schemas for
	// every ManifestFull/Infra-tier tool, unaffected by allow/deny policy —
	// loop.go). At W=40,000 the grown mid-turn overhead alone pushed the
	// check over budget after only 3 of the 4 results (36,006 estimator
	// tokens, right at the old B≈36,000 edge), firing two PREMATURE mid-turn
	// relief operations before the scripted overflow error at step 5 ever
	// arrived, and windowTrim's larger tool-surface estimate then needed a
	// THIRD operation on top of those two — three total instead of the two
	// this test pins, mirroring the same tool-catalog-growth class both merge
	// parents independently hit and fixed in eventbus_test.go's own
	// DefaultContextWindow pin (ADR-066 D2 rung 3). 54,000 (B ≈ 45,404 for
	// this fixture) restores the original margin: the two site's overhead
	// terms diverge enough with the grown catalog that only a narrow window
	// keeps ALL of steps 1-4 under B (no premature mid-turn relief) while
	// still needing exactly two relief operations, oldest-first, at the
	// retry to fit — confirmed empirically against the live
	// sentToolSurfaceTokens/agentContextBudget values for this fixture's
	// agent+tools (passing range roughly [48,000, 59,000]).
	cfg.Context.DefaultContextWindow = intPtr(54_000)

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

// TestRunTurn_LiveVsReloadBytesEqual — spec test 32, FR-030 (MAJ-CW-004,
// amended 2026-09-30) + SC-010, with FR-031 (the floor survives intact) and
// B-23 (no archive line changes).
//
// CORRECTED 2026-10-01: mid-turn relief tries dropRecall -> slideOldest ->
// shortenNext in that order (window_checkpoint.go::checkpointWindow). With
// four complete, non-floor steps piling up, slideOldest finds a legal
// whole-step cut on every firing before shortenNext ever runs — confirmed
// against the real run: two relief events, skip 0->5 then 5->7, both
// site=mid_turn, both results_emptied=0. This fixture never reaches the
// empty-in-place path at all: the earlier steps are CUT, not marked. The
// old oracle ("two emptied marks, two intact, Skip never moves") was
// pre-amendment; the correct invariant is: only the newest (floor) step,
// call-4, survives in the live window, Skip has advanced past everything
// else, and nothing is marked "emptied" (FR-023's counter is for in-place
// empties only, which a slide is not).
func TestRunTurn_LiveVsReloadBytesEqual(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	h := newLiveReloadHarness(t, home, scriptedOverflowTurn())

	before := ContextEmptiesTotal()
	reply, err := h.runScheduled(t, "fill the window")
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	require.Equal(t, 6, h.provider.CallCount(), "4 tool steps + the overflow + the retry")

	// Live: the bytes the provider received on the retried call. Only the
	// newest (floor) step's result reaches the wire — call-1/2/3's whole
	// steps were slid out of the window before the request was ever sent.
	live := msgsByToolCallID(h.provider.LastMessages())
	require.Len(t, live, 1, "only the floor step's result (call-4) survives in the live window")
	_, floorPresent := live["call-4"]
	require.True(t, floorPresent, "call-4 is the newest step's result")
	assert.True(t, strings.HasPrefix(live["call-4"].Content, "four:"), "the floor is never cut or shortened")
	for _, id := range []string{"call-1", "call-2", "call-3"} {
		_, present := live[id]
		assert.False(t, present, "%s was slid out of the window whole, not emptied in place", id)
	}
	assert.Equal(t, before, ContextEmptiesTotal(),
		"FR-023's empties counter only counts in-place empties; a whole-step slide is not one")

	// State: Skip advanced past every non-floor step; nothing was marked
	// emptied — slideOldest deletes projection entries for cut lines, it
	// never creates an "emptied" mark (window_relief.go::slideOldest).
	pm := h.agent.Sessions.Projection(h.sessionKey)
	archive, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	emptiedLines := map[string]int{}
	for k, v := range pm.Entries {
		if v == memory.ProjectionEmptied {
			emptiedLines[k.ToolCallID] = k.ArchiveLine
		}
	}
	assert.Empty(t, emptiedLines, "slide, not empty: no result was marked emptied, got %v", pm.Entries)
	history := h.agent.Sessions.GetHistory(h.sessionKey)
	skip := len(archive) - len(history)
	assert.Greater(t, skip, 0, "FR-030: Skip genuinely advances on a mid-turn slide")
	assert.Less(t, len(history), len(archive), "the live window is genuinely smaller than the full archive")
	byIDHist := msgsByToolCallID(history)
	for _, id := range []string{"call-1", "call-2", "call-3"} {
		_, survived := byIDHist[id]
		assert.False(t, survived, "%s was cut from the retained window, not left as a mark", id)
	}
	_, call4Survived := byIDHist["call-4"]
	assert.True(t, call4Survived, "the floor result stays in the retained window")

	// B-23: the archive still holds every full result — a slide moves Skip,
	// it never truncates or rewrites archive lines.
	for _, line := range archive {
		if line.Role == "tool" {
			assert.Greater(t, len(line.Content), 30_000, "archive line %s untouched", line.ToolCallID)
		}
	}

	// Reload: a fresh process over the same home assembles the session —
	// SC-010 still requires live bytes == reload bytes, now for the one
	// surviving result.
	h2 := newLiveReloadHarness(t, home, testutil.NewScenario())
	h2.sessionKey = h.sessionKey
	ts := newTurnState(h2.agent, processOptions{SessionKey: h.sessionKey}, turnEventScope{turnID: "reload"})
	reloaded := msgsByToolCallID(h2.al.assembleMessages(
		context.Background(), ts, h2.agent.Sessions.GetHistory(h.sessionKey), "", nil, nil))
	require.Len(t, reloaded, 1, "the reassembled window carries only the floor result, same as live")
	assert.Equal(t, live["call-4"].Content, reloaded["call-4"].Content, "SC-010: live bytes == reload bytes")
	assert.Equal(t, live["call-4"].ToolCallID, reloaded["call-4"].ToolCallID)

	// FR-022: nothing was emptied in place this turn, so the transcript's
	// content_state stays unset for all four calls.
	entries, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	states := map[string]session.ToolCall{}
	for _, e := range entries {
		for _, tc := range e.ToolCalls {
			states[string(tc.ID)] = tc
		}
	}
	require.Len(t, states, 4)
	for _, id := range []string{"call-1", "call-2", "call-3", "call-4"} {
		assert.Equal(t, "", states[id].ContentState, "%s was never emptied in place", id)
	}
}

// TestRunTurn_AbortRestoresTurnStartTriple — spec test 33, B-24 / SC-003: an
// in-turn-B emptying pass on an earlier, completed turn's result leaves a
// non-trivial intermediate projection state; an abort restores archive
// length, Skip and the projection set to their TURN-START values — never
// the intermediate one — and the transcript follows.
//
// CORRECTED 2026-10-01 (FR-030/MAJ-CW-004, same mechanism as test 32): turn
// A's own mid-turn checkpointing now slides whole steps instead of emptying
// in place, so it leaves the turn-start projection set EMPTY, not holding
// two marks — confirmed by the real run (two relief events, skip 0->5 then
// 5->7, both results_emptied=0), identical to test 32's fixture since both
// share newLiveReloadHarness + scriptedOverflowTurn(). Turn A's only
// surviving step is its floor, call-4 (call-1..call-3 were slid away
// whole, out of the window entirely — there is nothing left of them to
// target with a second direct emptyInPlace pass).
//
// The original test drove TWO direct passes (call-3 then call-4) because
// both were real, turn-A-admitted results with a genuine TranscriptLine
// registration (set by the real admission pipeline,
// tool_result_transcript.go::persistResultTranscriptLine) — the only kind
// of result recordEmptiedOnTranscript (empty_in_place.go) will actually
// rewrite on the transcript; this turn's own synthetic b-1/b-2/b-3
// (appended directly via appendInTurnMessages + a raw AppendTranscript
// call, bypassing that pipeline) never get that registration, so a pass
// targeting one of them silently no-ops on the transcript side — confirmed
// by an isolated run (targeting b-1 for a second pass left its transcript
// content_state "" where "emptied" was expected). Redesigned to ONE direct
// pass, on call-4 (the one remaining real, transcript-registered,
// earlier-turn candidate): it still proves the exact invariant B-24/SC-003
// needs — a non-trivial intermediate state (archive line below the
// turn-start length) reverts to the captured turn-start triple, not to the
// intermediate one — with the transcript following.
func TestRunTurn_AbortRestoresTurnStartTriple(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// Turn A completes normally; its mid-turn checkpoint slides away
	// call-1..call-3 whole (FR-030), leaving the projection set EMPTY —
	// that is the turn-start state turn B must come back to.
	h := newLiveReloadHarness(t, home, scriptedOverflowTurn())
	_, err := h.runScheduled(t, "turn A")
	require.NoError(t, err)
	archiveA, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	skipA := len(archiveA) - len(h.agent.Sessions.GetHistory(h.sessionKey))
	require.Greater(t, skipA, 0, "precondition: turn A's own checkpoint already slid steps away (FR-030)")
	setA := h.agent.Sessions.Projection(h.sessionKey).Entries.Clone()
	require.Empty(t, setA, "precondition: turn A slid, it never emptied in place — the turn-start set is empty")
	transcriptA, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)

	// Turn B: the same session, a new turn state, one more pass driven
	// directly through the real live descendant of the deleted D5
	// entry point — windowCheckpoint.shortenResult, committed via
	// commitWindowProjections (window_relief.go, window_projection_effects.go)
	// — the same two calls the mid-turn caller (checkpointWindow, T066-13)
	// makes, on turn A's surviving result, then an abort.
	ts := newTurnState(h.agent, processOptions{
		SessionKey:          h.sessionKey,
		TranscriptSessionID: h.sessionID,
		TranscriptStore:     h.store,
	}, turnEventScope{turnID: "turn-b"})
	require.Equal(t, len(archiveA), ts.initialWindow.Count)
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

	// Oldest-first across the WHOLE window (B-21b): turn A's one surviving
	// result, call-4 (its earlier steps call-1..call-3 were already SLID out
	// of the window whole during turn A's own checkpoint, not left as
	// marks — FR-030/MAJ-CW-004), is the oldest eligible result in turn B's
	// window (this turn's own b-1/b-2/b-3 are all newer).
	//
	// Driven end-to-end through the organic relief chain (dropRecall ->
	// slideOldest -> shortenNext, window_checkpoint.go::checkpointWindow),
	// slideOldest would win first and shortenResult would never fire for
	// this fixture — so the intermediate state B-24/SC-003 needs (a result
	// of an EARLIER, completed turn, emptied during THIS turn, with an
	// archive line BELOW the turn-start archive length — exactly the case
	// the rollback must undo by restoring the SET, not by truncating lines)
	// is built DIRECTLY via the checkpoint/commit path instead: a checkpoint
	// over turn B's current window, one targeted shortenResult pass on
	// call-4, then commitWindowProjections — the same two calls the live
	// mid-turn caller makes, just without the ordering that would skip past
	// call-4 for this fixture's step shape.
	cs := config.DefaultContextSettings()
	if cfg := h.al.GetConfig(); cfg != nil {
		cs = cfg.Context
	}
	p, cwStore, err := newWindowCheckpoint(context.Background(), ts, window, cs)
	require.NoError(t, err)
	idx := -1
	for i, m := range p.messages {
		if m.Role == "tool" && m.ToolCallID == "call-4" {
			idx = i
		}
	}
	require.GreaterOrEqual(t, idx, 0, "call-4 must be present in turn B's checkpoint message slice")
	// halve=false: shortenResult's own behavior (window_relief.go) is
	// kept=0 — fully emptied, state -> memory.ProjectionEmptied — when
	// halve is false, and kept=k/2 — state -> memory.ProjectionCapped —
	// only when halve is true. "Empty it in place" (B-24/SC-003) is the
	// fully-emptied case, so false.
	changed, err := p.shortenResult(idx, false)
	require.NoError(t, err)
	require.True(t, changed, "call-4 must actually be shortened (emptied) by the pass")

	// commitWindowProjections persists the window-metadata transaction and
	// rewrites the transcript in one call (window_projection_effects.go) —
	// the same call the live mid-turn caller makes. No separate
	// Sessions.Save() follows: CommitWindow writes straight into the
	// session store's own in-memory window state (pkg/session/context_window.go),
	// which Sessions.GetHistory/Projection below read directly, same as the
	// precedent TestCommitWindowProjections_TranscriptUndoFailure_PropagatesNotSwallowed
	// (window_projection_effects_test.go), which reads the committed state
	// back via store.SnapshotWindow with no Save() call either — Save()
	// only runs inside restoreSession itself (turn_exit.go), as part of
	// the abort below, unchanged by this fix.
	changes, err := h.al.commitWindowProjections(context.Background(), p, cwStore)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, "call-4", changes[0].key.ToolCallID)
	assert.Less(t, changes[0].key.ArchiveLine, ts.initialWindow.Count, "an earlier turn's line")
	assert.Equal(t, memory.ProjectionEmptied, changes[0].state)
	mid := h.agent.Sessions.Projection(h.sessionKey).Entries
	require.Len(t, mid, 1, "intermediate state: turn A's zero (slid) + the one emptied now")
	tb, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	midStates := map[string]string{}
	for _, e := range tb {
		for _, tc := range e.ToolCalls {
			midStates[string(tc.ID)] = tc.ContentState
		}
	}
	assert.Equal(t, "emptied", midStates["call-4"], "transcript followed the pass")

	// Abort.
	require.NoError(t, ts.restoreSession(h.agent))

	archiveAfter, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	// FR-006: the abort is non-destructive — turn B's appended bytes are
	// RETAINED (append-only archive); only the model view excludes them.
	assert.Len(t, archiveAfter, len(archiveA)+7, "FR-006: abort preserves the appended bytes")
	cwAfter, ok := h.agent.Sessions.(session.ContextWindowStore)
	require.True(t, ok)
	snapAfter, err := cwAfter.SnapshotWindow(context.Background(), h.sessionKey)
	require.NoError(t, err)
	assert.Equal(t, ts.initialWindow.Skip, snapAfter.State.Skip, "Skip back to the turn-start value")
	assert.Equal(t, setA, h.agent.Sessions.Projection(h.sessionKey).Entries,
		"projection set back to the TURN-START set, not the intermediate one")
	assert.Equal(t, setA, ts.initialEmptiedSet, "the captured triple never moved")

	transcriptAfter, err := h.store.ReadTranscript(h.sessionID)
	require.NoError(t, err)
	require.Len(t, transcriptAfter, len(transcriptA)+3)
	assert.Equal(t, transcriptA, transcriptAfter[:len(transcriptA)],
		"turn A's records back to their turn-start state: call-4's in-turn-B empty reverted, "+
			"call-1/call-2/call-3 untouched throughout (they were slid, not emptied, by turn A itself)")
}

// --- reused tool_call_ids (B-29b) ----------------------------------------

// TestMidTurnLineResolver_ReusedIDsResolvePositionally pins the composite
// address D5 persists under.
//
// The regression: the resolver returned the MOST RECENT archive line
// carrying a message's tool_call_id, so two window messages sharing an id
// both resolved to the newer line. Emptying the OLDER one then (a) would have
// built its mark from the newer line — wrong size, wrong content behind the
// recall key — and (b) would have persisted (id, newerLine) → emptied, so the
// reload projection (archiveLineResolver, index-exact) would have blanked the
// NEWER result while the older one came back at full content. Live and
// reload would have disagreed, breaking B-22, and the wrong result would have
// been destroyed.
//
// D5's own standalone end-to-end empty pass (eligibleToolResults /
// emptyOldestFirst) that used to demonstrate this positionally was retired
// after the R1 GREEN refactor moved emptying behind a new call site; this
// test now pins the regression at the resolver level only — midTurnLineResolver
// and archiveLineResolver (both still live) must each address a reused-id
// result by its OWN archive line, not the most recent line sharing that id.
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
