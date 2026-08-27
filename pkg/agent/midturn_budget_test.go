// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// midturn_budget_test.go — ADR-066 D6 (T066-13): the mid-turn window check.
//
// Spec: docs/internal/specs/adr-066-context-overflow-spec.md — FR-021,
// FR-029, FR-030, FR-031, FR-032; B-25, B-34, B-35, B-36, B-36b; DS-5.
// Tests 17 (TestMidTurnBudget_OperationBySiteAndPosition) and 18
// (TestMidTurnBudget_TriggerTargetStop); test 19's mid-turn half lives in
// context_budget_test.go.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// midTurnFixture boots an AgentLoop with one default agent whose window is
// cw; absTriggerChars overrides absolute_trigger_chars when > 0.
func midTurnFixture(t *testing.T, cw, absTriggerChars int) (*AgentLoop, *AgentInstance) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 2000
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Context = config.DefaultContextSettings()
	if absTriggerChars > 0 {
		cfg.Context.AbsoluteTriggerChars = absTriggerChars
	}
	cfg.Context.DefaultContextWindow = intPtr(cw)

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, testutilScenarioText())
	t.Cleanup(func() { al.Close() })
	agent := registerAgent(t, al, home, "mia", testutilScenarioText(), true)
	return al, agent
}

// proseOfTokens returns prose whose estimateMessageTokens cost (as a bare
// tool message) is close to tok — chars = tok × 5/2, built from words so the
// base64-payload filter never rewrites it.
func proseOfTokens(tok int) string {
	chars := tok * 5 / 2
	unit := "lorem ipsum "
	return strings.Repeat(unit, chars/len(unit))
}

// seedMidTurn appends msgs to the agent's session store under key and
// returns the live window slice (the stand-in for the request slice the
// tool loop hands the check) plus a turnState for the same session.
func seedMidTurn(t *testing.T, agent *AgentInstance, key string, msgs []providers.Message) ([]providers.Message, *turnState) {
	t.Helper()
	for _, m := range msgs {
		agent.Sessions.AddFullMessage(key, m)
	}
	require.NoError(t, agent.Sessions.Save(key))
	window := agent.Sessions.GetHistory(key)
	require.Len(t, window, len(msgs))
	ts := newTurnState(agent, processOptions{SessionKey: key}, turnEventScope{turnID: "midturn-unit"})
	return window, ts
}

// TestMidTurnBudget_OperationBySiteAndPosition — spec test 17 (B-35, B-36,
// B-21b; FR-030, FR-031): mid-turn the check NEVER advances Skip — an
// earlier complete turn still in the slice has its results EMPTIED, never
// cut; a current-turn result of an older step is emptied; the last
// assistant step (the floor) is never touched and, sized by the D4 clamp,
// fits without any emptying.
func TestMidTurnBudget_OperationBySiteAndPosition(t *testing.T) {
	t.Run("mid-turn, oldest over-budget is an earlier complete turn: Skip unchanged, its results emptied (B-35 row 3)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-earlier-turn"
		budget := agentContextBudget(agent)
		require.Positive(t, budget)
		big := proseOfTokens(budget * 6 / 10)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			{Role: "user", Content: "turn one"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c1", "one")}},
			{Role: "tool", ToolCallID: "c1", Content: big},
			{Role: "assistant", Content: "turn one done"},
			{Role: "user", Content: "turn two"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c2", "two")}},
			{Role: "tool", ToolCallID: "c2", Content: big},
		})
		archiveLen := len(window)
		require.True(t, requestTokens(window, nil) > budget, "precondition: total fired")

		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)

		assert.Len(t, out, archiveLen, "bytes shrink only by marks — never by removing messages")
		assert.Contains(t, out[2].Content, `"content_state":"emptied"`, "the earlier turn's result becomes the mark")
		assert.True(t, strings.HasPrefix(out[6].Content, "lorem"), "the floor (last assistant step's result) is intact")
		assert.Len(t, agent.Sessions.GetHistory(key), archiveLen, "FR-030: Skip did not move mid-turn")
		assert.LessOrEqual(t, requestTokens(out, nil), budget*4/5, "emptied to 80%% of the fired condition")
	})

	t.Run("mid-turn, current-turn result of an older step is emptied (DS-5 #4)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-older-step"
		budget := agentContextBudget(agent)
		big := proseOfTokens(budget * 6 / 10)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			{Role: "user", Content: "one turn, two steps"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c1", "one")}},
			{Role: "tool", ToolCallID: "c1", Content: big},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c2", "two")}},
			{Role: "tool", ToolCallID: "c2", Content: big},
		})
		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)
		assert.Contains(t, out[2].Content, `"content_state":"emptied"`, "older step emptied (R1)")
		assert.True(t, strings.HasPrefix(out[4].Content, "lorem"), "newest step intact (R2)")
		assert.Len(t, agent.Sessions.GetHistory(key), len(out), "Skip unchanged")
	})

	t.Run("last assistant step never emptied; clamp-sized parallel step fits with no fire (B-36 / DS-5 #5)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-parallel-floor"
		budget := agentContextBudget(agent)
		// Three parallel results, each at the /N-clamped effective cap in
		// tokens (cap/N chars where the D4 rule fires): ≈ 0.5·B/3 each.
		each := proseOfTokens(budget / 6)
		before := ContextEmptiesTotal()
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			{Role: "user", Content: "parallel step"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				toolCallFor("p1", "a"), toolCallFor("p2", "b"), toolCallFor("p3", "c"),
			}},
			{Role: "tool", ToolCallID: "p1", Content: each},
			{Role: "tool", ToolCallID: "p2", Content: each},
			{Role: "tool", ToolCallID: "p3", Content: each},
		})
		require.LessOrEqual(t, requestTokens(window, nil), budget, "precondition: the clamp keeps the floor under B")

		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)
		for _, i := range []int{2, 3, 4} {
			assert.True(t, strings.HasPrefix(out[i].Content, "lorem"), "floor result %d intact", i)
		}
		assert.Equal(t, before, ContextEmptiesTotal(), "no emptying pass ran")
	})
}

// TestMidTurnBudget_TriggerTargetStop — spec test 18 (B-34, B-25, B-36b;
// FR-021, FR-029, FR-032): the fired condition picks the target (80 % of
// itself); emptying stops at the target or when nothing eligible remains;
// an immediate re-check does not re-fire; an unreachable target with the
// trigger satisfied continues; the guard fires only when a trigger is still
// exceeded after every eligible result went.
func TestMidTurnBudget_TriggerTargetStop(t *testing.T) {
	t.Run("share fires: emptied to 80% of absoluteShare, oldest first, one pass, no re-fire (B-34, B-25)", func(t *testing.T) {
		// Big window so total can never fire; absolute_trigger_chars 10,000
		// → absoluteShare 4,000 estimator tokens, target 3,200.
		al, agent := midTurnFixture(t, 400_000, 10_000)
		key := "midturn-share"
		absShare := absoluteShareTokens(config.ContextSettings{AbsoluteTriggerChars: 10_000})
		require.Equal(t, 4_000, absShare)
		each := proseOfTokens(1_100)
		msgs := make([]providers.Message, 0, 13)
		msgs = append(msgs, providers.Message{Role: "user", Content: "five results"})
		for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
			msgs = append(msgs,
				providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor(id, id)}},
				providers.Message{Role: "tool", ToolCallID: id, Content: each},
			)
		}
		msgs = append(msgs,
			providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("floor", "f")}},
			providers.Message{Role: "tool", ToolCallID: "floor", Content: "tiny"},
		)
		window, ts := seedMidTurn(t, agent, key, msgs)
		require.Greater(t, toolResultShareTokens(window), absShare, "precondition: share fired")
		require.LessOrEqual(t, requestTokens(window, nil), agentContextBudget(agent), "precondition: total did NOT fire")

		before := ContextEmptiesTotal()
		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)
		assert.Equal(t, before+3, ContextEmptiesTotal(), "DS-5 #6: target needs three — three emptied in ONE pass")
		assert.LessOrEqual(t, toolResultShareTokens(out), absShare*4/5, "share brought to 80%% of the fired condition")
		for i, id := range []string{"s1", "s2", "s3"} {
			assert.Contains(t, out[2+2*i].Content, `"content_state":"emptied"`, "oldest-first: %s emptied", id)
		}
		assert.True(t, strings.HasPrefix(out[8].Content, "lorem"), "s4 intact — emptying stopped at the target")
		assert.True(t, strings.HasPrefix(out[10].Content, "lorem"), "s5 intact")

		// B-25: the immediate re-check is a no-op.
		out2, err := al.midTurnWindowCheck(ts, out, nil)
		require.NoError(t, err)
		assert.Equal(t, before+3, ContextEmptiesTotal(), "re-check must not re-fire")
		assert.Equal(t, out, out2)
	})

	t.Run("total fires: emptied to 80% of B and stops (B-34)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-total"
		budget := agentContextBudget(agent)
		each := proseOfTokens(budget * 35 / 100)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			{Role: "user", Content: proseOfTokens(budget / 5)},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("t1", "a")}},
			{Role: "tool", ToolCallID: "t1", Content: each},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("t2", "b")}},
			{Role: "tool", ToolCallID: "t2", Content: each},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("t3", "c")}},
			{Role: "tool", ToolCallID: "t3", Content: each},
		})
		require.Greater(t, requestTokens(window, nil), budget, "precondition: total fired")

		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)
		assert.LessOrEqual(t, requestTokens(out, nil), budget*4/5, "total brought to 80%% of B")
		assert.Contains(t, out[2].Content, `"content_state":"emptied"`)
		assert.Contains(t, out[4].Content, `"content_state":"emptied"`)
		assert.True(t, strings.HasPrefix(out[6].Content, "lorem"), "floor intact; emptying stopped at the target")
	})

	t.Run("target unreachable, trigger satisfied: continue with no error (B-36b / DS-5 #7)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-unreachable-target"
		budget := agentContextBudget(agent)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			// An oversized non-tool message emptying cannot touch — but small
			// enough that removing the one eligible result satisfies B.
			{Role: "user", Content: proseOfTokens(budget * 95 / 100)},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("e1", "a")}},
			{Role: "tool", ToolCallID: "e1", Content: proseOfTokens(budget / 5)},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("f1", "b")}},
			{Role: "tool", ToolCallID: "f1", Content: "tiny floor"},
		})
		require.Greater(t, requestTokens(window, nil), budget, "precondition: total fired")

		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err, "trigger back under B: the turn continues even though the 0.8·B target is unreachable")
		assert.Contains(t, out[2].Content, `"content_state":"emptied"`, "the one eligible result was emptied")
		assert.Greater(t, requestTokens(out, nil), budget*4/5, "target genuinely unreachable")
		assert.LessOrEqual(t, requestTokens(out, nil), budget, "…but the trigger is satisfied")
	})

	t.Run("guard: trigger still exceeded after every eligible result — ErrContextUnrecoverable (B-37 / DS-5 #8)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-guard"
		budget := agentContextBudget(agent)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			{Role: "user", Content: proseOfTokens(budget * 12 / 10)}, // injected oversized non-tool message
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("f1", "a")}},
			{Role: "tool", ToolCallID: "f1", Content: "tiny floor"},
		})
		require.Greater(t, requestTokens(window, nil), budget)

		_, err := al.midTurnWindowCheck(ts, window, nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrContextUnrecoverable), "FR-032: the guard is the typed sentinel, got %v", err)
	})

	t.Run("guard is decided BEFORE the pass: an unsatisfiable budget empties and persists nothing", func(t *testing.T) {
		// The regression: when the un-emptiable residue (tool defs + the
		// system/user messages + the floor set) alone exceeds B, the check
		// still ran the whole D5 pass first — emptying every eligible result
		// AND persisting each (tool_call_id, archive_line) -> emptied — and
		// only then fired the guard. typedTurnExit (unlike abortTurn) never
		// calls restoreSession, so those projection entries survived forever:
		// the marks became permanent and every later turn on the session died
		// the same way, with the results it might have used already destroyed.
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-guard-preflight"
		budget := agentContextBudget(agent)
		eligible := proseOfTokens(budget / 2)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			// Un-emptiable on its own: no pass can bring total under B.
			{Role: "user", Content: proseOfTokens(budget * 12 / 10)},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("c1", "a")}},
			{Role: "tool", ToolCallID: "c1", Content: eligible},
			{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("f1", "b")}},
			{Role: "tool", ToolCallID: "f1", Content: "tiny floor"},
		})
		require.Greater(t, requestTokens(window, nil), budget, "precondition: total fired")
		require.Equal(t, []int{2}, eligibleToolResults(window,
			midTurnLineResolverForTest(t, agent, key, window), nil),
			"precondition: there IS an eligible result the old code would have emptied")

		before := ContextEmptiesTotal()
		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrContextUnrecoverable), "still the typed sentinel, got %v", err)

		assert.Equal(t, before, ContextEmptiesTotal(),
			"a turn that is going to abort must not empty anything")
		assert.Equal(t, eligible, out[2].Content,
			"the eligible result must be left intact — the turn aborts, the content is not destroyed")
		assert.Empty(t, agent.Sessions.Projection(key).Entries,
			"no projection state may be persisted on a turn the guard is certain to kill: "+
				"typedTurnExit never rolls it back")
	})
}

// TestMidTurnBudget_C1_CallMessagesInjections — ADR-066 D6, C1 (CRITICAL):
// both budget sites must measure the request the provider ACTUALLY receives
// (loop.go's callMessages), not the raw window (messages). callMessages is
// assembled by injecting the scratchpad note, the workspace instructions
// note (AGENT.md — up to 262,144 bytes, no budget-aware cap), the
// web-rendering note and the compressed manifest note AFTER both budget
// checks run, so a large AGENT.md alone can leave `messages` fitting B while
// the real request blows past it — the provider then returns
// context_too_long on a window the engine believed it was protecting
// (the ADR's §1 incident class, reintroduced).
//
// REPLACED 2026-08-27 (FR-032 residue regression, C1's own follow-on bug):
// this test used to assert ErrContextUnrecoverable here — that pinned the
// regression C1 (4d357904) itself introduced. With NO tool result in the
// window at all there is nothing D5 could ever empty, so folding noteTokens
// into the FR-032 thrash-guard predicate meant EVERY turn on a workspace
// with a large-but-legal AGENT.md died the same way, forever (ADR-066 §7's
// "unreachable by construction" made ordinary by a config value, not an
// injected fault). C1's actual claim — the check must still SEE the
// note-inflated total — is preserved and asserted below via
// ContextResidueOverflowsTotal: the overflow is detected and logged, not
// silently ignored, but it is no longer turn-fatal (§8: "nothing
// size-related is turn-fatal once D4–D6 are in"). The companion test
// TestMidTurnBudget_C1_NotesStillTriggerEmptying proves noteTokens still
// drive real D5 work when there IS something eligible to empty.
func TestMidTurnBudget_C1_CallMessagesInjections(t *testing.T) {
	al, agent := midTurnFixture(t, 40_000, 0)
	budget := agentContextBudget(agent)
	require.Positive(t, budget)

	// A workspace whose AGENT.md, once injected, alone exceeds the entire
	// budget — sized off proseOfTokens so its estimator cost is
	// deterministic and directly comparable to budget.
	home := os.Getenv(config.EnvHome)
	require.NotEmpty(t, home, "midTurnFixture must set OMNIPUS_HOME")
	wsID := "big-instructions-ws"
	wsDir := filepath.Join(home, "workspaces", wsID)
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	agentMD := proseOfTokens(budget * 6 / 5) // ~1.2x budget in estimator tokens
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "AGENT.md"), []byte(agentMD), 0o644))

	key := "midturn-c1-callmessages"
	// A tiny conversation with NO tool results: `messages` alone fits B
	// comfortably, and there is nothing eligible for the D5 pass to empty —
	// the window portion is therefore trivially under B no matter what the
	// notes cost. If the check fires at all here it can only be because it
	// saw the injected note weight (C1); the correct outcome is now "log
	// and continue", never the FR-032 guard.
	window, ts := seedMidTurn(t, agent, key, []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	})
	ts.opts.WorkspaceID = wsID

	require.LessOrEqual(t, requestTokens(window, nil), budget,
		"precondition: messages alone fits B — the C1 bug is invisible without this")
	require.Greater(t, al.ephemeralSystemNoteTokens(ts), budget,
		"precondition: the injected workspace-instructions note alone exceeds B")

	before := ContextResidueOverflowsTotal()
	out, err := al.midTurnWindowCheck(ts, window, nil)
	require.NoError(t, err, "FR-032 amendment: a note-only overflow (nothing eligible to empty, "+
		"window fits without the notes) must not end the turn — the provider's own context error "+
		"is the backstop, not this guard")
	assert.Equal(t, window, out, "nothing was eligible to empty; the slice is returned unchanged")
	assert.Greater(t, ContextResidueOverflowsTotal(), before,
		"C1 preserved: the check still measured and logged the note-inflated total instead of "+
			"silently treating the turn as fitting")
}

// TestMidTurnBudget_C1_NotesStillTriggerEmptying — companion to the C1 test
// above: proves noteTokens are still wired into the TRIGGER and the 80%
// TARGET (the FR-032 amendment excludes them only from the thrash-guard's
// fatal predicate, never from `total`/`fits`). An agent whose bare
// `messages` comfortably fit B, but whose real request (messages + the
// injected AGENT.md note) does not, must still empty the one ELIGIBLE tool
// result it can reach — if noteTokens had been dropped from the trigger
// instead of just the guard, `messages` alone already fits B and the check
// would return at its very first comparison, emptying nothing.
func TestMidTurnBudget_C1_NotesStillTriggerEmptying(t *testing.T) {
	al, agent := midTurnFixture(t, 40_000, 0)
	budget := agentContextBudget(agent)
	require.Positive(t, budget)

	home := os.Getenv(config.EnvHome)
	require.NotEmpty(t, home, "midTurnFixture must set OMNIPUS_HOME")
	wsID := "c1-not-regressed-ws"
	wsDir := filepath.Join(home, "workspaces", wsID)
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	// Sized so the note alone (~0.6B) is comfortably under B, but combined
	// with the eligible result (~0.5B) the total exceeds B — while the bare
	// window (no notes) does not.
	agentMD := proseOfTokens(budget * 6 / 10)
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "AGENT.md"), []byte(agentMD), 0o644))

	key := "midturn-c1-not-regressed"
	eligible := proseOfTokens(budget / 2) // an older step's result
	window, ts := seedMidTurn(t, agent, key, []providers.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("e1", "a")}},
		{Role: "tool", ToolCallID: "e1", Content: eligible},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("f1", "b")}},
		{Role: "tool", ToolCallID: "f1", Content: "tiny floor"},
	})
	ts.opts.WorkspaceID = wsID

	windowOnly := requestTokens(window, nil)
	require.LessOrEqual(t, windowOnly, budget,
		"precondition: the bare messages slice alone fits B — the C1 bug is invisible without this")
	noteTokens := al.ephemeralSystemNoteTokens(ts)
	require.Positive(t, noteTokens)
	require.Greater(t, windowOnly+noteTokens, budget,
		"precondition: total (window + the un-emptiable note) exceeds B — the trigger fires (C1)")

	before := ContextEmptiesTotal()
	out, err := al.midTurnWindowCheck(ts, window, nil)
	require.NoError(t, err, "the window (after emptying the one eligible result) fits B on its own; "+
		"only the un-emptiable note pushes total over — not fatal per the FR-032 amendment")
	assert.Greater(t, ContextEmptiesTotal(), before,
		"C1 not regressed: the check must still have measured the note-inflated total and emptied "+
			"the eligible result — proof that noteTokens still drive the trigger and target, not just "+
			"logging")
	assert.Contains(t, out[2].Content, `"content_state":"emptied"`, "the one eligible result was emptied")
	assert.Equal(t, "tiny floor", out[4].Content, "the floor set is never touched")
}

// TestMidTurnBudget_ResidueRegression_NotesAloneDoNotEndTurn — direct
// regression test for the FR-032 residue bug the C1 fix (4d357904)
// introduced: it added noteTokens (the un-emptiable ephemeral system
// notes — scratchpad, AGENT.md, web-rendering, compressed manifest) to the
// residue midTurnPassCanSucceed refuses on, making those notes count
// against the thrash guard even though D5 can never touch them. A
// large-but-legal AGENT.md (D9's 262,144-byte cap has no budget-aware
// clamp) then made every tool-calling turn on that workspace end with
// ErrContextUnrecoverable — permanently, since the cause is static
// configuration, not an injected fault (ADR-066 §7).
//
// Scenario, mirroring the reported worked example: the ONLY tool result
// present IS the entire floor set (the last assistant step's own result —
// never eligible for D5, so nothing at all can be emptied), sized near the
// D4 clamp; the workspace's AGENT.md, once injected, is large enough that
// notes + floor exceed B, but the floor alone (the window, minus the
// un-emptiable notes) fits comfortably under B on its own.
func TestMidTurnBudget_ResidueRegression_NotesAloneDoNotEndTurn(t *testing.T) {
	al, agent := midTurnFixture(t, 40_000, 0)
	budget := agentContextBudget(agent)
	require.Positive(t, budget)

	home := os.Getenv(config.EnvHome)
	require.NotEmpty(t, home, "midTurnFixture must set OMNIPUS_HOME")
	wsID := "residue-regression-ws"
	wsDir := filepath.Join(home, "workspaces", wsID)
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	agentMD := proseOfTokens(budget * 7 / 10) // ~0.7B, mirrors the reported ~16,000/23,033
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "AGENT.md"), []byte(agentMD), 0o644))

	key := "midturn-residue-regression"
	floor := proseOfTokens(budget / 2) // ~0.5B, mirrors the D4-clamp-sized single result
	window, ts := seedMidTurn(t, agent, key, []providers.Message{
		{Role: "user", Content: "run the one tool"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("f1", "a")}},
		{Role: "tool", ToolCallID: "f1", Content: floor},
	})
	ts.opts.WorkspaceID = wsID

	windowOnly := requestTokens(window, nil)
	require.LessOrEqual(t, windowOnly, budget,
		"precondition: the window itself already fits B — the single result IS the floor, so nothing "+
			"is eligible to empty and the D5 pass can only ever be a no-op here")
	noteTokens := al.ephemeralSystemNoteTokens(ts)
	require.Positive(t, noteTokens)
	require.Greater(t, windowOnly+noteTokens, budget,
		"precondition: total (window + the un-emptiable notes) exceeds B — the trigger still fires (C1)")

	before := ContextResidueOverflowsTotal()
	out, err := al.midTurnWindowCheck(ts, window, nil)
	require.NoError(t, err,
		"REGRESSION: FR-032's fatal exit is reserved for an injected fault (a non-tool message itself "+
			"oversized), never for a configuration-size condition like an oversized AGENT.md — the un-emptiable "+
			"notes must not end an otherwise-fitting turn")
	assert.Equal(t, floor, out[2].Content, "nothing was eligible to empty — the floor content is untouched")
	assert.Greater(t, ContextResidueOverflowsTotal(), before,
		"the overflow is still observed and logged (one ERROR), never silently swallowed")
}

// midTurnLineResolverForTest builds the same resolver midTurnWindowCheck
// uses, so a precondition can assert what the real pass would have seen.
func midTurnLineResolverForTest(
	t *testing.T, agent *AgentInstance, key string, window []providers.Message,
) func(int) int {
	t.Helper()
	archive, err := agent.Sessions.ReadArchive(context.Background(), key)
	require.NoError(t, err)
	return midTurnLineResolver(archive, window)
}

// testutilScenarioText returns a one-line text provider for fixtures whose
// tests never reach the provider.
func testutilScenarioText() providers.LLMProvider {
	return scenarioTextProvider{}
}

type scenarioTextProvider struct{}

func (scenarioTextProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "unused"}, nil
}
func (scenarioTextProvider) GetDefaultModel() string { return "test-model" }
