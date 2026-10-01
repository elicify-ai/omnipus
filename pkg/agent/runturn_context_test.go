// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// runturn_context_test.go — ADR-066 D6 (T066-13): the mid-turn window check
// driven through the REAL turn loop (runTurn via ProcessScheduled /
// processTaskDirect) against recording providers.
//
// Spec: docs/internal/specs/adr-066-context-overflow-spec.md — tests 30
// (TestRunTurn_GuardTest_2MBResultCompletes), 31
// (TestRunTurn_LongTurn_50CallsAtCap_SmallWindow), 34
// (superseded by TestRunTurn_ImmutableResidueStillSends), 39
// (TestRunTurn_MidTurnNeverAdvancesSkip), 40 (TestRunTurn_SmallWindowClamp),
// 53 (TestRunTurn_InjectedSpanSubjectToD5); B-23, B-33, B-36, B-37, B-39,
// B-50d; SC-001, SC-002, SC-008, SC-011.

package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ctxTurnHarness is the D6 loop-level fixture: one agent ("mia"), a window
// pinned via the ladder's global default, the big_tool stub and a scripted
// provider. Modeled on empty_in_place_test.go's liveReloadHarness, with the
// window and result size parameterised.
type ctxTurnHarness struct {
	al         *AgentLoop
	agent      *AgentInstance
	provider   *testutil.ScenarioProvider
	store      *session.UnifiedStore
	sessionID  string
	sessionKey string
}

func newCtxTurnHarness(t *testing.T, provider *testutil.ScenarioProvider, window, toolSize int) *ctxTurnHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 2000
	cfg.Agents.Defaults.MaxToolIterations = 60
	cfg.Context = config.DefaultContextSettings()
	cfg.Context.DefaultContextWindow = intPtr(window)

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	mia := registerAgent(t, al, home, "mia", provider, true)
	al.RegisterTool(&bigResultTool{size: toolSize})
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"big_tool": "allow"},
	})
	return &ctxTurnHarness{al: al, agent: mia, provider: provider, store: al.GetSessionStore()}
}

func (h *ctxTurnHarness) runScheduled(t *testing.T, prompt string) (string, error) {
	t.Helper()
	meta, err := h.store.NewScheduledSession("mia")
	require.NoError(t, err)
	h.sessionID = meta.ID
	h.sessionKey = "agent:mia:session:" + meta.ID
	return h.al.ProcessScheduled(context.Background(), "mia", meta.ID, prompt, "scheduled", meta.ID)
}

// assertEveryRequestUnderBudget walks every recorded provider request and
// asserts the D6 invariant (SC-002): the estimator total — the same measure
// isOverContextBudget compares — never exceeds B at ANY iteration.
func assertEveryRequestUnderBudget(t *testing.T, h *ctxTurnHarness) {
	t.Helper()
	budget := agentContextBudget(h.agent)
	require.Positive(t, budget)
	// The runtime check adds the SENT tool surface (policy-filtered — here
	// one tool, a few dozen tokens); measuring the registry's full def set
	// would over-count by an order of magnitude, so the assertion covers
	// the message total, the dominant term of FR-029's `total`.
	for i, req := range h.provider.AllRequests() {
		assert.LessOrEqual(t, requestTokens(req, nil), budget,
			"request %d of %d exceeds B", i+1, h.provider.CallCount())
	}
}

// TestRunTurn_GuardTest_2MBResultCompletes — spec test 30, B-39 / ADR §17.1
// (SC-008): a ~2 MB tool result enters the loop, is capped at the door, the
// assembled request stays under B, and the turn completes with NO
// user-facing error. Also pins the FR-033 order observably: the archive
// line holds the filtered full content while the provider saw the capped
// form.
func TestRunTurn_GuardTest_2MBResultCompletes(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("huge-1", "huge")}).
		WithText("done")
	h := newCtxTurnHarness(t, provider, 40_000, 2_000_000)

	reply, err := h.runScheduled(t, "fetch the huge thing")
	require.NoError(t, err, "the turn must complete with no user-facing error")
	assert.Equal(t, "done", reply)
	require.Equal(t, 2, h.provider.CallCount())

	assertEveryRequestUnderBudget(t, h)

	live := msgsByToolCallID(h.provider.LastMessages())
	require.Contains(t, live, "huge-1")
	assert.Contains(t, live["huge-1"].Content, `"content_state":"capped"`, "the model sees the capped form with the mark")
	assert.Less(t, len(live["huge-1"].Content), 200_000, "2 MB never reaches the request")

	// The archive kept the full (filtered) result — capping is projection,
	// not mutation (B-23).
	archive, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	found := false
	for _, line := range archive {
		if line.Role == "tool" && line.ToolCallID == "huge-1" {
			found = true
			assert.Greater(t, len(line.Content), 1_900_000, "archive holds the full result")
		}
	}
	assert.True(t, found)
}

// TestRunTurn_LongTurn_50CallsAtCap_SmallWindow — spec test 31 as amended by
// the 2026-09-30 #1081 amendment (ADR-066 §18.2 MAJ-CW-004/005/007, §18.4
// row 1; spec Amendment [2026-09-30] FR-029–FR-032, SC-002, B-54; oracle
// adapted 2026-10-02 from the pre-amendment no-cut assertions): a turn of 50
// tool calls, each near the cap, against a small window. Every request stays
// ≤ B at every iteration (SC-002). Relief is slide-first (FR-032): legal
// completed steps leave the request WHOLE, so Skip genuinely advances —
// repeatedly — and because this fixture always has a legal slide available,
// nothing is ever emptied in place and no emptied mark ever appears. The
// verbatim user anchor and the newest structural floor (call-50's complete
// group, full text — FR-031 lets pressure shorten it, but slides suffice
// here) survive every request; the archive keeps every admitted byte (B-23).
func TestRunTurn_LongTurn_50CallsAtCap_SmallWindow(t *testing.T) {
	const calls = 50
	const prompt = "fill the window fifty times"
	provider := testutil.NewScenario()
	for i := 1; i <= calls; i++ {
		provider.WithToolCalls([]providers.ToolCall{toolCallFor(fmt.Sprintf("call-%d", i), fmt.Sprintf("tag%d", i))})
	}
	provider.WithText("done")
	h := newCtxTurnHarness(t, provider, 40_000, 30_000)

	checksBefore := MidTurnBudgetChecksTotal()
	emptiesBefore := ContextEmptiesTotal()
	reply, err := h.runScheduled(t, prompt)
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	require.Equal(t, calls+1, h.provider.CallCount(), "50 tool steps + the final text; the guard never fired")

	// Checkpoint composition (MAJ-CW-004): one check per admitted result
	// (calls) plus one prepare-side and one send-side check per assembled
	// request (2 × (calls+1)). Asserted as that composition — not a magic
	// total — so adding or removing a checkpoint site fails loudly here
	// instead of silently needing a new hardcoded number.
	assert.Equal(t, int64(calls+2*(calls+1)), MidTurnBudgetChecksTotal()-checksBefore,
		"a check at every admitted result (%d) plus the prepare-side and send-side checks for each of the %d assembled requests", calls, calls+1)

	// Slide-first relief (FR-032 / MAJ-CW-004): every relief pass in this
	// fixture has a legal completed step to slide (steps 1..calls-1 are all
	// complete before the newest group finishes), so emptying is never
	// reached. The no-legal-slide fallback (emptying to the mark) is covered
	// separately by midturn_budget_test.go's emptying tests.
	assert.Equal(t, emptiesBefore, ContextEmptiesTotal(),
		"slide-first: with a legal slide always available, no result is ever emptied")

	assertEveryRequestUnderBudget(t, h)

	// The floor (FR-031): in every request after step k, result k (the
	// newest) is present, intact and FULL-SIZE. Amended FR-031 permits
	// pressure-shortening the newest text, but only when relief needs it —
	// here slides always suffice, so the newest result must keep its whole
	// door-admitted size (fixture arithmetic: tag prefix + 30,000 chars, the
	// result enters whole because it is under the half-B admission clamp).
	for i, req := range h.provider.AllRequests() {
		if i == 0 {
			continue
		}
		newest := fmt.Sprintf("call-%d", i)
		byID := msgsByToolCallID(req)
		require.Contains(t, byID, newest, "request %d must carry result %s", i+1, newest)
		assert.True(t, strings.HasPrefix(byID[newest].Content, fmt.Sprintf("tag%d:", i)),
			"SC-011: the most recent result is intact in request %d", i+1)
		assert.GreaterOrEqual(t, len(byID[newest].Content), 30_000,
			"FR-031: slides suffice in this fixture — request %d's newest result must be full-size, not pressure-shortened", i+1)
	}

	// §18.4 row 1: many admitted results force MULTIPLE mid-turn Skip
	// advances, observed on the recorded requests: the set of live results
	// only ever shrinks — a group slid out of the window never returns
	// (FR-030; sliding is removal from the view, not a mark) — and it shrinks
	// at more than one point (repeated advances, not one final cut).
	requests := h.provider.AllRequests()
	everGone := map[string]bool{}
	advances := 0
	for i, req := range requests {
		live := msgsByToolCallID(req)
		if i > 0 {
			returned := []string{}
			for id := range everGone {
				if _, ok := live[id]; ok {
					returned = append(returned, id)
				}
			}
			assert.Empty(t, returned,
				"FR-030: results slid out of the live window never return (returned by request %d)", i+1)
			removed := false
			for id := range msgsByToolCallID(requests[i-1]) {
				if _, ok := live[id]; !ok {
					everGone[id] = true
					removed = true
				}
			}
			if removed {
				advances++
			}
		}
	}
	assert.GreaterOrEqual(t, advances, 2,
		"§18.4: many admitted results force MULTIPLE mid-turn Skip advances — the live set must shrink at more than one point")

	// The final request (MAJ-CW-005 + fixture arithmetic): exactly the
	// verbatim initiating user anchor and the newest COMPLETE group. One
	// retained full result keeps both bounds satisfied at the 80% target;
	// two would re-fire the share bound (S = 0.5·W = 20,000); zero would
	// violate the structural floor. Older completed groups all slid away.
	final := h.provider.LastMessages()
	userCount, callCount, resultCount := 0, 0, 0
	callIndex, resultIndex := -1, -1
	for i, m := range final {
		switch m.Role {
		case "user":
			userCount++
			assert.Equal(t, prompt, m.Content,
				"MAJ-CW-005: the initiating user anchor survives every slide verbatim")
		case "assistant":
			if len(m.ToolCalls) > 0 {
				callCount++
				callIndex = i
				require.Len(t, m.ToolCalls, 1, "the retained newest group keeps its single declared call")
				assert.Equal(t, "call-50", m.ToolCalls[0].ID,
					"the retained newest call keeps its declared identity")
				assert.Equal(t, toolCallFor("call-50", "tag50").Function, m.ToolCalls[0].Function,
					"the retained newest call keeps its declared tool name and exact arguments")
			}
		case "tool":
			resultCount++
			resultIndex = i
			assert.Equal(t, "call-50", m.ToolCallID,
				"the newest result is the only retained result — every older complete group slid away whole (FR-030/MAJ-CW-004)")
		}
	}
	assert.Equal(t, 1, userCount, "exactly one user message: the anchor, never duplicated by a slide")
	assert.Equal(t, 1, callCount, "exactly one retained call: the newest group")
	assert.Equal(t, 1, resultCount, "exactly one retained result: the newest group")
	assert.Less(t, callIndex, resultIndex, "the retained call precedes its result")

	archive, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	// B-23, exact fixture arithmetic: 1 initiating user + 50 × (call +
	// result) + 1 final assistant. Sliding is removal from the request view
	// (Q3) — the archive never grows a duplicate or loses a line to relief.
	require.Len(t, archive, 2*calls+2)

	// B-23: every archive tool line still holds its full bytes.
	toolLines := 0
	for _, line := range archive {
		if line.Role == "tool" {
			toolLines++
			assert.Greater(t, len(line.Content), 30_000, "archive line %s untouched", line.ToolCallID)
		}
	}
	assert.Equal(t, calls, toolLines)

	// The committed live view (MAJ-CW-005): the anchor user line + the newest
	// complete group + the final assistant text — 4 records against the
	// 102-line archive. Skip genuinely advanced mid-turn; the full history
	// stays recoverable from the untouched archive (recall/B-58 coverage is
	// separate). The transient relief notice is request-only, never persisted.
	history := h.agent.Sessions.GetHistory(h.sessionKey)
	require.Len(t, history, 4, "anchor + newest complete group + final assistant — no emptied marks, no duplicates")
	assert.Equal(t, "user", history[0].Role)
	assert.Equal(t, prompt, history[0].Content, "the live anchor carries the original user text")
	assert.Equal(t, "assistant", history[1].Role)
	require.Len(t, history[1].ToolCalls, 1)
	assert.Equal(t, "call-50", history[1].ToolCalls[0].ID)
	assert.Equal(t, "tool", history[2].Role)
	assert.Equal(t, "call-50", history[2].ToolCallID)
	assert.Equal(t, "assistant", history[3].Role)
	assert.Equal(t, "done", history[3].Content)
	assert.Greater(t, len(archive)-len(history), 0,
		"FR-030 (amended): Skip genuinely advanced mid-turn — the live window is an anchored suffix, not the whole archive")
}

// TestRunTurn_ImmutableResidueStillSends — ADR-066's 2026-09-30 amendment,
// MAJ-CW-004/005/010 and §18.4, replaces old spec test 34's local fatal guard.
// An oversized task prompt reaches the turn as immutable user text. Relief
// cannot make that text fit, but must still send a structurally valid request;
// only a genuine provider rejection may fail it. This provider accepts it.
// The ordinary-turn control is retained unchanged.
func TestRunTurn_ImmutableResidueStillSends(t *testing.T) {
	t.Run("oversized immutable user text still reaches the next provider request", func(t *testing.T) {
		provider := testutil.NewScenario().
			WithToolCalls([]providers.ToolCall{toolCallFor("g-1", "one")}).
			WithText("done")
		h := newCtxTurnHarness(t, provider, 40_000, 100)

		budget := agentContextBudget(h.agent)
		fault := proseOfTokens(budget * 12 / 10) // > B on its own; result relief cannot shorten user text
		require.Greater(t, requestTokens([]providers.Message{{Role: "user", Content: fault}}, nil), budget,
			"instrument: the immutable user text alone exceeds B")

		reply, err := h.runScheduled(t, fault)
		require.NoError(t, err, "MAJ-CW-004/010: estimated immutable residue must not end the turn locally")
		assert.Equal(t, "done", reply, "the accepting provider's completion must reach the caller")
		require.Equal(t, 2, h.provider.CallCount(), "one tool step followed by the accepting completion")
		requests := h.provider.AllRequests()
		require.Len(t, requests, 2, "the second request must cross the provider boundary")

		var users []providers.Message
		var calls []providers.ToolCall
		var results []providers.Message
		callIndex, resultIndex := -1, -1
		for i, message := range requests[1] {
			switch message.Role {
			case "user":
				users = append(users, message)
			case "assistant":
				calls = append(calls, message.ToolCalls...)
				if len(message.ToolCalls) > 0 {
					callIndex = i
				}
			case "tool":
				results = append(results, message)
				resultIndex = i
			}
		}
		require.Equal(t, []providers.Message{{Role: "user", Content: fault}}, users,
			"the original user anchor is preserved verbatim, exactly once")
		require.Len(t, calls, 1, "newest assistant call structure survives relief")
		assert.Equal(t, "g-1", calls[0].ID, "the declared call identity cannot change")
		assert.Equal(t, toolCallFor("g-1", "one").Function, calls[0].Function,
			"the declared tool name and exact arguments cannot change")
		require.Len(t, results, 1, "the newest result slot cannot disappear or duplicate")
		assert.Equal(t, "g-1", results[0].ToolCallID, "the result remains paired with its declared call")
		assert.Less(t, callIndex, resultIndex, "the newest assistant call precedes its result")
	})

	t.Run("no-fault control: the same loop shape completes without the guard", func(t *testing.T) {
		provider := testutil.NewScenario().
			WithToolCalls([]providers.ToolCall{toolCallFor("c-1", "one")}).
			WithToolCalls([]providers.ToolCall{toolCallFor("c-2", "two")}).
			WithToolCalls([]providers.ToolCall{toolCallFor("c-3", "three")}).
			WithToolCalls([]providers.ToolCall{toolCallFor("c-4", "four")}).
			WithText("done")
		h := newCtxTurnHarness(t, provider, 40_000, 30_000)
		reply, err := h.runScheduled(t, "an ordinary prompt")
		require.NoError(t, err, "not reachable across DS-5 without the fault")
		assert.Equal(t, "done", reply)
		assert.Equal(t, 5, h.provider.CallCount())
	})
}

// TestRunTurn_MidTurnNeverAdvancesSkip — spec test 39, B-35 row 3 / ADR
// §17.4c (FR-030, amended 2026-09-30 MAJ-CW-004): renamed in substance, not
// just in oracle — "Skip never advances" is exactly the pre-slide claim
// MAJ-CW-004 replaces. Through the REAL turn loop, an earlier complete
// turn (and, within it, an earlier complete step) that the current turn's
// step never touches is cut from the live window WHOLE, and Skip genuinely
// advances past it — never left as an in-place "emptied" mark. What FR-030
// still guarantees, and this test now proves instead: the current turn's
// own initiating user message is the anchor and survives; the newest
// (floor) step is never slid or corrupted; every request the provider
// receives stays under budget (SC-002); and a genuine slide can shrink the
// message count between consecutive requests — that is no longer a defect.
func TestRunTurn_MidTurnNeverAdvancesSkip(t *testing.T) {
	// Turn A: two 25,000-char results. Precondition CORRECTED 2026-10-01:
	// under the real slide-before-empty order, even turn A's own mid-turn
	// checkpoint (after a-2's result is admitted) finds a-1's step a legal
	// complete prefix the newest step (a-2) doesn't touch, and slides it
	// away whole — this was never reachable under the old no-slide model.
	providerA := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("a-1", "aone")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("a-2", "atwo")}).
		WithText("turn A done")
	h := newCtxTurnHarness(t, providerA, 40_000, 25_000)
	_, err := h.runScheduled(t, "turn A")
	require.NoError(t, err)

	archiveA, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	historyA := h.agent.Sessions.GetHistory(h.sessionKey)
	skipA := len(archiveA) - len(historyA)
	assert.Greater(t, skipA, 0,
		"FR-030/MAJ-CW-004: turn A's own mid-turn checkpoint already slides a-1's step away whole")
	assert.Less(t, len(historyA), len(archiveA), "the live window genuinely shrank versus the full archive")
	byIDA := msgsByToolCallID(historyA)
	_, a1Survived := byIDA["a-1"]
	assert.False(t, a1Survived, "a-1's step was cut from the live window, not left as a mark")
	floorA, a2Survived := byIDA["a-2"]
	require.True(t, a2Survived, "a-2 is the newest step's result — never slid or touched")
	assert.True(t, strings.HasPrefix(floorA.Content, "atwo:"), "turn A's floor result is intact")

	// Turn B on the SAME session: two 30,000-char results push the total
	// further over B mid-turn. The oldest over-budget content is now all of
	// turn A's surviving carryover (its own user anchor stops protecting it
	// once turn B establishes its OWN anchor) plus, in turn, b-1's step.
	providerB := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("b-1", "bone")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("b-2", "btwo")}).
		WithText("turn B done")
	h.provider = providerB
	h.agent.Provider = providerB
	h.al.RegisterTool(&bigResultTool{size: 30_000})

	reply, err := h.al.ProcessScheduled(context.Background(), "mia", h.sessionID, "turn B", "scheduled", h.sessionID)
	require.NoError(t, err)
	assert.Equal(t, "turn B done", reply)

	archiveB, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	historyB := h.agent.Sessions.GetHistory(h.sessionKey)
	skipB := len(archiveB) - len(historyB)
	assert.Greater(t, skipB, skipA,
		"FR-030/MAJ-CW-004: Skip keeps advancing as turn B's own results force further slides")
	assert.Less(t, len(historyB), len(archiveB), "the live window stays genuinely smaller than the full archive")

	// Turn A's carryover (a-1, a-2) and turn B's own older step (b-1) are
	// all cut from the live window whole — none of them is present with an
	// "emptied" mark, because they were never emptied in place; they were
	// slid away. Only the newest (floor) step, b-2, survives — intact.
	byIDB := msgsByToolCallID(historyB)
	for _, id := range []string{"a-1", "a-2", "b-1"} {
		_, survived := byIDB[id]
		assert.False(t, survived, "%s's step was cut from the live window, not left as a mark", id)
	}
	floorB, b2Survived := byIDB["b-2"]
	require.True(t, b2Survived, "b-2 is the newest step's result — never slid or touched")
	assert.True(t, strings.HasPrefix(floorB.Content, "btwo:"), "the floor is intact")

	final := msgsByToolCallID(providerB.LastMessages())
	finalFloor, ok := final["b-2"]
	require.True(t, ok, "the floor result reaches the final provider request")
	assert.True(t, strings.HasPrefix(finalFloor.Content, "btwo:"), "the floor is intact in the final sent request")
	for _, id := range []string{"a-1", "a-2", "b-1"} {
		_, present := final[id]
		assert.False(t, present, "%s never reaches the final provider request — it was slid away", id)
	}

	// The slide genuinely shrinks the live request between consecutive
	// turn-B requests — this supersedes the pre-slide invariant that the
	// message count never drops mid-turn; a real cut, not just a mark, is
	// exactly what MAJ-CW-004 adds.
	reqs := providerB.AllRequests()
	require.GreaterOrEqual(t, len(reqs), 2, "turn B makes at least two provider requests")
	assert.Less(t, len(reqs[len(reqs)-1]), len(reqs[0]),
		"the final request is smaller than the first: a genuine slide occurred mid-turn")
	assertEveryRequestUnderBudget(t, h)
}

// TestRunTurn_SmallWindowClamp — spec test 40, B-11b / B-11c / B-36 / ADR
// §17.4b (SC-008): on an 8,192-token window a 200,000-char result enters
// capped to at most half the budget (the D4 clamp), the floor is never
// emptied, and the turn completes.
func TestRunTurn_SmallWindowClamp(t *testing.T) {
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("small-1", "sw")}).
		WithText("done")
	h := newCtxTurnHarness(t, provider, 8_192, 200_000)

	budget := agentContextBudget(h.agent)
	require.Positive(t, budget, "8,192 window with 2,000 max_tokens leaves a real budget")

	emptiesBefore := ContextEmptiesTotal()
	reply, err := h.runScheduled(t, "small window")
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	require.Equal(t, 2, h.provider.CallCount())

	live := msgsByToolCallID(h.provider.LastMessages())
	require.Contains(t, live, "small-1")
	// ±16 runes of tolerance: B moves by a few tokens between runs (the
	// pinned system prompt embeds run-varying text) and the head/tail cut
	// avoids splitting a rune, so the capped form can land a rune past this
	// test's own floor-division of the same formula.
	halfBudgetChars := budget/2*5/2 + 16
	assert.LessOrEqual(t, len([]rune(live["small-1"].Content)), halfBudgetChars,
		"B-11b: the result is clamped to ≤ 0.5·B (in chars)")
	assert.Contains(t, live["small-1"].Content, `"content_state":"capped"`)
	assert.Equal(t, emptiesBefore, ContextEmptiesTotal(),
		"B-36: the clamp makes the floor fit — nothing to empty")
	assertEveryRequestUnderBudget(t, h)
}

// TestRunTurn_InjectedSpanSubjectToD5 — spec test 53, B-50d (FR-043): a
// recall span injected mid-turn is SUBJECT to D5/D6 like any tool content —
// counted by the check and, under pressure, the FIRST thing to go
// (FR-019's drop-span-first rule, applied at the mid-turn site), before any
// real tool result is emptied. The drop is loud (INFO), never silent.
func TestRunTurn_InjectedSpanSubjectToD5(t *testing.T) {
	readLog := captureLogFile(t, logger.INFO)
	const nonce = "N-50d-SPAN-NONCE"
	filler := strings.Repeat("x", 400)
	turns := [][]providers.Message{makeTurn(filler+" "+nonce, filler)}
	for i := 0; i < 3; i++ {
		turns = append(turns, makeTurn(fmt.Sprintf("turn %d %s", i+2, filler), filler))
	}
	bigCall := func(id, tag string) func() (*providers.LLMResponse, error) {
		return func() (*providers.LLMResponse, error) {
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: id, Type: "function", Name: "big_tool",
				Arguments: map[string]any{"tag": tag},
			}}}, nil
		}
	}
	// The script's first step after the recall is a ToolSearch promotion, not
	// decoration. Under the fixture's compressed manifest (ADR-071) a lazy
	// tool is callable only after ToolSearch promotes it, and big_tool — a
	// test stub unknown to pkg/tools/manifest.go — resolves ManifestLazy, so
	// it is never in the sent defs on its own. When the offer gate
	// (toolNotOfferedRefusal, tool_offer_gate.go — ADR-071 §1.1 / ADR-088 D3)
	// met this fixture's Compressed=true (set for the token-accounting
	// reasons documented above), the two bare big_tool calls were refused as
	// "not offered", no 60,000-char result ever entered the slice, no
	// pressure ever materialized, and the span was correctly never dropped —
	// the failure this test hit after the merge combined those two branches.
	// A compliant model promotes the tool first; the script does too.
	promoteBigTool := func() (*providers.LLMResponse, error) {
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
			ID: "d5-promote", Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []string{"big_tool"}},
		}}}, nil
	}
	provider := &recallInjectionProvider{
		first:  map[string]any{"turn_range": "1-1"},
		script: []func() (*providers.LLMResponse, error){promoteBigTool, bigCall("d5-1", "one"), bigCall("d5-2", "two")},
	}
	// W was 50,000 (B ≈ 44k) until the feat/library-improvements merge
	// (integrate/library-improvements-v0.1.1) unconditionally registered the
	// knowledge tool family for EVERY agent (registerKnowledgeTools,
	// instance.go — ADR-067 D7). At the time this was misdiagnosed as a
	// tool-CATALOG-growth problem and "fixed" by raising W to 55,000 (B ≈
	// 48.6k) — see recallInjectionFixture's Compressed=true comment for the
	// real cause: decideRecallInjection's sentToolSurfaceTokens-based check
	// was silently taking its "compressed manifest OFF" fallback (because
	// this fixture's bare cfg literal never set
	// cfg.Tools.Manifest.Compressed), charging a full JSON schema for EVERY
	// registered tool regardless of manifest tier or allow/deny policy — an
	// order-of-magnitude over-count no real install produces, since
	// config.DefaultConfig() always ships Compressed=true. Raising W papered
	// over the symptom without fixing that; it only bought headroom until
	// the next unconditional tool registration used it up.
	//
	// That next registration was dd3c26832 (merge fix/kb12, 2026-09-08):
	// knowledge_base_create and knowledge_list joined the family (still
	// unconditional for every agent, still charged as full schemas under
	// the same off-by-default fallback), pushing the measured tool surface
	// from ~45.2k to ~47.4k — past B again and failing this same
	// precondition a second time, by the same mechanism, one merge later.
	//
	// recallInjectionFixture now sets Compressed=true (the shipped
	// default), so this check takes its REAL path: knowledge_base_create,
	// knowledge_list and every sibling knowledge/grep tool are
	// ManifestLazy+SearchOnly (pkg/tools/manifest.go), costing one compact
	// manifest-note line each — not a full schema — until an agent actually
	// loads one via ToolSearch. Catalog growth no longer perturbs this
	// fixture's margin, so 55,000 is not a razor-edge number chasing a
	// moving uncompressed total: it only needs the small seeded window plus
	// the 410-token span to fit the REAL (now much smaller) tool surface at
	// injection time, while staying tight enough that two door-capped
	// 60,000-char big_tool results still push the D6 total over B, so the
	// injected span is still the first thing dropped under pressure.
	al, agent := recallInjectionFixture(t, provider, 55_000, 1_000, turns)
	al.RegisterTool(&bigResultTool{size: 60_000})
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			"recall_conversation": config.ToolPolicyAllow,
			"big_tool":            config.ToolPolicyAllow,
			// ToolSearch must be in the agent's OWN map (mirroring the
			// "seeded as real data for every agent" floor production ships,
			// pkg/coreagent/seed.go / seed_system.go), not just the
			// filter-time backstop: the exec-time TOCTOU re-check
			// (resolveToolPolicyAtExec) re-resolves the policy from this map
			// and fails closed to deny on any tool with no entry — a
			// hand-built map that omits it shows ToolSearch in the defs but
			// has its calls denied as a "mid-turn policy change", so the
			// promotion step below never runs.
			"ToolSearch": config.ToolPolicyAllow,
		},
	})
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-2)

	_, err := al.processTaskDirect(context.Background(), agent.ID, "recall then work", recallInjectionSessionKey, "chat-50d")
	require.NoError(t, err)
	require.Equal(t, 5, provider.calls(), "recall + ToolSearch promotion + two big steps + the final answer")

	req2 := provider.request(2)
	if !requestContains(req2, nonce) {
		// Surface the captured agent log on precondition failure — the
		// usual cause is the recall-span-vs-tool-surface budget squeeze
		// this test has hit twice before (see the sizing note above); the
		// log's "recall span refused" line names the exact byte counts.
		t.Log(readLog())
	}
	require.True(t, requestContains(req2, nonce), "precondition: the span WAS injected into request 2")

	final := provider.request(provider.calls())
	assert.False(t, requestContains(final, nonce),
		"B-50d: under pressure the injected span is dropped from the request")
	assert.Equal(t, 0, countMarkers(final), "no recall marker survives the pressure drop")
	assert.Nil(t, al.activeRecallSpan(recallInjectionSessionKey), "the span is gone, not parked")

	logs := readLog()
	assert.Contains(t, logs, "recall span dropped", "FR-043: the drop logs at INFO — never silent")
	assert.Contains(t, logs, "pressure", "…with the pressure reason")
}
