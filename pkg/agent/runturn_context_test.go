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
// (TestRunTurn_ThrashGuard_InjectedFaultOnly), 39
// (TestRunTurn_MidTurnNeverAdvancesSkip), 40 (TestRunTurn_SmallWindowClamp),
// 53 (TestRunTurn_InjectedSpanSubjectToD5); B-23, B-33, B-36, B-37, B-39,
// B-50d; SC-001, SC-002, SC-008, SC-011.

package agent

import (
	"context"
	"errors"
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

// TestRunTurn_LongTurn_50CallsAtCap_SmallWindow — spec test 31, B-33 / B-21
// / B-36 / ADR §17.2 (SC-002, SC-011): a turn of 50 tool calls, each near
// the cap, against a small window. Every request stays ≤ B at every
// iteration, the most recent result is always intact (the floor), emptied
// results carry marks, Skip never moves mid-turn, the check ran once per
// admitted result (B-33) and no archive byte changed (B-23).
func TestRunTurn_LongTurn_50CallsAtCap_SmallWindow(t *testing.T) {
	const calls = 50
	provider := testutil.NewScenario()
	for i := 1; i <= calls; i++ {
		provider.WithToolCalls([]providers.ToolCall{toolCallFor(fmt.Sprintf("call-%d", i), fmt.Sprintf("tag%d", i))})
	}
	provider.WithText("done")
	h := newCtxTurnHarness(t, provider, 40_000, 30_000)

	checksBefore := MidTurnBudgetChecksTotal()
	emptiesBefore := ContextEmptiesTotal()
	reply, err := h.runScheduled(t, "fill the window fifty times")
	require.NoError(t, err)
	assert.Equal(t, "done", reply)
	require.Equal(t, calls+1, h.provider.CallCount(), "50 tool steps + the final text; the guard never fired")

	assert.Equal(t, checksBefore+calls, MidTurnBudgetChecksTotal(),
		"B-33: the check runs once per admitted result — N results, N mid-turn checks")
	assert.Greater(t, ContextEmptiesTotal(), emptiesBefore, "a 50-call turn against a small window must have emptied")

	assertEveryRequestUnderBudget(t, h)

	// The floor: in every request after step k, result k (the newest) is
	// intact. Request i+2 is the first carrying result i+1.
	for i, req := range h.provider.AllRequests() {
		if i == 0 {
			continue
		}
		newest := fmt.Sprintf("call-%d", i)
		byID := msgsByToolCallID(req)
		require.Contains(t, byID, newest, "request %d must carry result %s", i+1, newest)
		assert.True(t, strings.HasPrefix(byID[newest].Content, fmt.Sprintf("tag%d:", i)),
			"SC-011: the most recent result is intact in request %d", i+1)
	}

	// Marks in the final request; Skip untouched (mid-turn never cuts).
	final := h.provider.LastMessages()
	marks := 0
	for _, m := range final {
		if m.Role == "tool" && strings.Contains(m.Content, `"content_state":"emptied"`) {
			marks++
		}
	}
	assert.Greater(t, marks, 0, "emptied results carry marks in the live request")

	archive, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	assert.Len(t, h.agent.Sessions.GetHistory(h.sessionKey), len(archive),
		"FR-030: Skip never moved — the pre-turn site saw an empty session and mid-turn never cuts")

	// B-23: every archive tool line still holds its full bytes.
	toolLines := 0
	for _, line := range archive {
		if line.Role == "tool" {
			toolLines++
			assert.Greater(t, len(line.Content), 30_000, "archive line %s untouched", line.ToolCallID)
		}
	}
	assert.Equal(t, calls, toolLines)
}

// TestRunTurn_ThrashGuard_InjectedFaultOnly — spec test 34, B-37 / ADR
// §17.4 (FR-032): the guard is reachable ONLY through an injected fault —
// here an oversized non-tool message smuggled past the D4 bounds via the
// internal task-prompt path (processTaskDirect/ProcessScheduled prompts are
// not user messages and carry no bound; that is the injection). It then
// produces the typed context_unrecoverable exit, one ERROR line, and NO
// further provider call. Without the fault the same loop across the DS-5
// shapes completes (the no-fault control).
func TestRunTurn_ThrashGuard_InjectedFaultOnly(t *testing.T) {
	t.Run("injected oversized non-tool message reaches the guard; provider not called again", func(t *testing.T) {
		readLog := captureLogFile(t, logger.ERROR)
		provider := testutil.NewScenario().
			WithToolCalls([]providers.ToolCall{toolCallFor("g-1", "one")}).
			WithText("MUST NEVER BE REQUESTED")
		h := newCtxTurnHarness(t, provider, 40_000, 100)

		budget := agentContextBudget(h.agent)
		fault := proseOfTokens(budget * 12 / 10) // > B on its own; emptying cannot touch a user message

		_, err := h.runScheduled(t, fault)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrContextUnrecoverable),
			"the guard must surface the typed sentinel, got %v", err)
		assert.Equal(t, 1, h.provider.CallCount(),
			"FR-032: after the guard fires the provider is NEVER called again")
		assert.Contains(t, readLog(), "context_unrecoverable", "one ERROR line names the typed code")
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
// §17.4c (FR-030): when the oldest over-budget content mid-turn is an
// EARLIER complete turn, Skip does not move and the request bytes shrink
// only by marks — the message count never drops mid-turn.
func TestRunTurn_MidTurnNeverAdvancesSkip(t *testing.T) {
	// Turn A: two 25,000-char results — fits B (≈ 33k tokens) at rest, so
	// turn B's PRE-turn site has nothing to do and Skip stays put.
	providerA := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("a-1", "aone")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("a-2", "atwo")}).
		WithText("turn A done")
	h := newCtxTurnHarness(t, providerA, 40_000, 25_000)
	_, err := h.runScheduled(t, "turn A")
	require.NoError(t, err)

	archiveA, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	skipA := len(archiveA) - len(h.agent.Sessions.GetHistory(h.sessionKey))
	require.Zero(t, skipA, "precondition: nothing evicted after turn A")

	// Turn B on the SAME session: two 30,000-char results push the total
	// over B mid-turn; the oldest over-budget content is turn A.
	providerB := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{toolCallFor("b-1", "bone")}).
		WithToolCalls([]providers.ToolCall{toolCallFor("b-2", "btwo")}).
		WithText("turn B done")
	h.provider = providerB
	h.agent.Provider = providerB
	h.al.RegisterTool(&bigResultTool{size: 30_000})

	reply, err := h.al.processTaskDirect(context.Background(), "mia", "turn B", h.sessionKey, "chat-skip-test")
	require.NoError(t, err)
	assert.Equal(t, "turn B done", reply)

	archiveB, err := h.agent.Sessions.ReadArchive(context.Background(), h.sessionKey)
	require.NoError(t, err)
	assert.Equal(t, len(archiveB)-len(h.agent.Sessions.GetHistory(h.sessionKey)), skipA,
		"FR-030: Skip did not move — mid-turn only empties")

	// Turn A's results were emptied (marks), turn B's floor result intact.
	final := msgsByToolCallID(providerB.LastMessages())
	assert.Contains(t, final["a-1"].Content, `"content_state":"emptied"`, "the earlier turn's oldest result is a mark")
	assert.True(t, strings.HasPrefix(final["b-2"].Content, "btwo:"), "the floor is intact")

	// Bytes shrink only by marks: between consecutive turn-B requests the
	// message count only ever GROWS (by the appended step), never drops.
	reqs := providerB.AllRequests()
	for i := 1; i < len(reqs); i++ {
		assert.GreaterOrEqual(t, len(reqs[i]), len(reqs[i-1]),
			"request %d dropped messages mid-turn — a cut, not an empty", i+1)
	}
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
	provider := &recallInjectionProvider{
		first:  map[string]any{"turn_range": "1-1"},
		script: []func() (*providers.LLMResponse, error){bigCall("d5-1", "one"), bigCall("d5-2", "two")},
	}
	// W = 50,000 → B ≈ 44k tokens: the span (+ the small window + this
	// fixture's ~20k-token full tool surface, which decideRecallInjection
	// counts) fits comfortably, while two door-capped 60,000-char results
	// (~22k tokens each) push the D6 total over B mid-turn.
	al, agent := recallInjectionFixture(t, provider, 50_000, 1_000, turns)
	al.RegisterTool(&bigResultTool{size: 60_000})
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			"recall_conversation": config.ToolPolicyAllow,
			"big_tool":            config.ToolPolicyAllow,
		},
	})
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-2)

	_, err := al.processTaskDirect(context.Background(), agent.ID, "recall then work", recallInjectionSessionKey, "chat-50d")
	require.NoError(t, err)
	require.GreaterOrEqual(t, provider.calls(), 4, "recall + two big steps + the final call")

	req2 := provider.request(2)
	if !requestContains(req2, nonce) {
		// Surface the captured agent log on precondition failure — the
		// usual cause is the fixture's ~20k-token tool surface squeezing
		// the injection budget (see the sizing note above).
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
