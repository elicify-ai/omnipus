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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// midTurnFixture boots an AgentLoop with one default agent whose window is
// cw; shareFraction overrides tool_result_share_fraction when > 0.
func midTurnFixture(t *testing.T, cw int, shareFraction float64) (*AgentLoop, *AgentInstance) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "test-model"}
	cfg.Agents.Defaults.MaxTokens = 2000
	cfg.Agents.Defaults.MaxToolIterations = 10
	cfg.Context = config.DefaultContextSettings()
	if shareFraction > 0 {
		cfg.Context.ToolResultShareFraction = shareFraction
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
// B-21b; FR-030, FR-031, amended 2026-09-30 MAJ-CW-004): mid-turn relief
// tries a whole-step SLIDE before any in-place empty — a legal complete
// prefix the newest step never touches is cut from the live window and Skip
// genuinely advances past it, both for an earlier complete turn and for an
// older step within the current turn (sliding is gated on step completeness
// and the newest-step/anchor floor, never on turn boundaries). The last
// assistant step (the floor) is never slid or touched, and when sized by
// the D4 clamp it fits without any emptying.
func TestMidTurnBudget_OperationBySiteAndPosition(t *testing.T) {
	t.Run("mid-turn, oldest over-budget is an earlier complete turn: the whole earlier turn slides away and Skip advances (B-35 row 3, FR-030)", func(t *testing.T) {
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

		// ADR-066 MAJ-CW-004 (2026-09-30 amendment): slideOldest runs BEFORE
		// any emptying/shortening. Turn one is a legal complete prefix the
		// newest step (c2) never touches, so the real relief here starts
		// with a whole-step SLIDE, not an in-place empty: all four of turn
		// one's messages — including its trailing plain-text reply — are
		// cut from the live window and Skip genuinely advances past them —
		// this supersedes the pre-slide assumption that mid-turn relief
		// never changes the message count. The slide alone is not enough to
		// reach the 80%% target here, so shortenNext then caps the one
		// remaining (newest/floor) result's TEXT in place — its slot still
		// survives, matching MAJ-CW-002's floor-slot protection.
		require.Len(t, out, 3, "turn one's 4 messages slide away whole; only turn two's 3 messages remain")
		assert.Equal(t, window[4], out[0], "turn two's own user anchor survives unchanged")
		assert.Equal(t, window[5], out[1], "the newest step's assistant call survives unchanged")
		assert.Equal(t, "tool", out[2].Role)
		assert.Equal(t, "c2", out[2].ToolCallID, "the floor slot is preserved, not removed")
		assert.True(t, strings.HasPrefix(out[2].Content, "lorem"), "capped text retains the source head")
		assert.Contains(t, out[2].Content, `"content_state":"capped"`, "the floor is capped in place, not slid or emptied")
		assert.Less(t, len(out[2].Content), len(big), "the floor's text genuinely shrank")
		assert.Len(t, agent.Sessions.GetHistory(key), 3, "FR-030: Skip advances past the slid-away turn")
		assert.Less(t, len(agent.Sessions.GetHistory(key)), archiveLen, "Skip genuinely moved mid-turn")
		assert.LessOrEqual(t, requestTokens(out, nil), budget*4/5, "brought to the 80%% target by slide + cap")
	})

	t.Run("mid-turn, current-turn result of an older step: the older step slides away whole and Skip advances (DS-5 #4, FR-030)", func(t *testing.T) {
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
		archiveLen := len(window)
		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)

		// Same MAJ-CW-004 ordering as above: the older complete step (c1,
		// plus its own initiating user message — this synthetic
		// single-turn fixture sets no turn anchor to protect it) is a legal
		// complete prefix the newest step (c2) never touches, so
		// slideOldest removes it whole instead of emptying it in place. The
		// slide alone does not reach the 80%% target, so shortenNext then
		// caps the remaining (newest/floor) result's TEXT — its slot still
		// survives, matching MAJ-CW-002's floor-slot protection.
		require.Len(t, out, 2, "the older step's 3 messages slide away whole; only the newest step's 2 remain")
		assert.Equal(t, window[3], out[0], "the newest step's assistant call survives unchanged")
		assert.Equal(t, "tool", out[1].Role)
		assert.Equal(t, "c2", out[1].ToolCallID, "the floor slot is preserved, not removed")
		assert.True(t, strings.HasPrefix(out[1].Content, "lorem"), "capped text retains the source head")
		assert.Contains(t, out[1].Content, `"content_state":"capped"`, "the floor is capped in place, not slid or emptied")
		assert.Less(t, len(out[1].Content), len(big), "the floor's text genuinely shrank")
		assert.Len(t, agent.Sessions.GetHistory(key), 2, "FR-030: Skip advances past the slid-away older step")
		assert.Less(t, len(agent.Sessions.GetHistory(key)), archiveLen, "Skip genuinely moved mid-turn")
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
// trigger satisfied continues. The fatal-only guard subtests are retired by
// ADR-066's 2026-09-30 amendment MAJ-CW-004/010.
func TestMidTurnBudget_TriggerTargetStop(t *testing.T) {
	t.Run("share fires: slides whole steps oldest-first to 80% of absoluteShare, no re-fire (B-34, B-25, FR-030 MAJ-CW-004)", func(t *testing.T) {
		// Big window so total can never fire; fraction 0.03125 × resolved
		// window 128,000 → share limit 4,000 tokens, 80% target 3,200.
		al, agent := midTurnFixture(t, 128_000, 0.03125)
		key := "midturn-share"
		resolvedWindow, _, _ := agent.windowSnapshot()
		absShare := toolResultShareLimit(config.ContextSettings{ToolResultShareFraction: 0.03125}, resolvedWindow)
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

		// CORRECTED 2026-10-01 (FR-030/MAJ-CW-004): checkpointWindow tries
		// slideOldest before shortenNext. Every one of s1..s5's steps is a
		// legal complete non-floor prefix, so relief slides whole steps —
		// Skip genuinely advances — instead of marking them emptied in
		// place. Confirmed by the real run: one checkpointWindow call,
		// results_emptied=0, skip_after=7 (the user header line plus s1's,
		// s2's and s3's steps — 7 archive lines — slid away as one
		// prefix), share_after=2216 (<= the 3,200 target).
		before := ContextEmptiesTotal()
		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)
		assert.Equal(t, before, ContextEmptiesTotal(),
			"FR-023's empties counter only counts in-place empties; a whole-step slide is not one")
		assert.LessOrEqual(t, toolResultShareTokens(out), absShare*4/5, "share brought to 80%% of the fired condition")

		byID := msgsByToolCallID(out)
		for _, id := range []string{"s1", "s2", "s3"} {
			_, present := byID[id]
			assert.False(t, present, "oldest-first: %s's whole step was slid out of the window, not emptied in place", id)
		}
		for _, id := range []string{"s4", "s5"} {
			got, present := byID[id]
			require.True(t, present, "%s stays in the window — the slide stopped once the target was reached", id)
			assert.True(t, strings.HasPrefix(got.Content, "lorem"), "%s intact", id)
		}
		floor, floorPresent := byID["floor"]
		require.True(t, floorPresent, "the floor is never slid or touched")
		assert.True(t, strings.HasPrefix(floor.Content, "tiny"), "the floor is never touched")

		// B-25: the immediate re-check is a no-op.
		out2, err := al.midTurnWindowCheck(ts, out, nil)
		require.NoError(t, err)
		assert.Equal(t, before, ContextEmptiesTotal(), "re-check must not re-fire")
		assert.Equal(t, out, out2)
	})

	// CORRECTION 2026-10-01: the brief for this round (coordination/logs/
	// context-window/brief-r1-oracle-round2.md, item 3) said this subtest
	// "already passes (no legal complete step to slide in their fixtures,
	// so shortenNext() still runs as before — don't touch)". That premise
	// is false — verified by actually running it (not assumed from the
	// brief): t1 and t2 are both legal complete non-floor steps, so the
	// FIRST subtest's fix (above) un-masks a panic (index out of range)
	// this subtest was ALSO hitting, previously hidden because the first
	// subtest's panic aborted the whole test binary before this one ran.
	// Corrected with the identical FR-030/MAJ-CW-004 technique.
	t.Run("total fires: slides whole steps oldest-first to 80% of B (B-34, FR-030 MAJ-CW-004)", func(t *testing.T) {
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

		// Confirmed by the real run: one checkpointWindow call, skip 0->5
		// (the user header line plus t1's and t2's steps — 5 archive lines
		// — slid away as one prefix), results_emptied=0; only t3 (the
		// floor) survives.
		before := ContextEmptiesTotal()
		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err)
		assert.Equal(t, before, ContextEmptiesTotal(), "a whole-step slide is not an in-place empty")
		assert.LessOrEqual(t, requestTokens(out, nil), budget*4/5, "total brought to 80%% of B")
		byID := msgsByToolCallID(out)
		for _, id := range []string{"t1", "t2"} {
			_, present := byID[id]
			assert.False(t, present, "oldest-first: %s's whole step was slid out of the window, not emptied in place", id)
		}
		floor, floorPresent := byID["t3"]
		require.True(t, floorPresent, "the floor stays in the window")
		assert.True(t, strings.HasPrefix(floor.Content, "lorem"), "floor intact; relief stopped at the target")
	})

	// CORRECTION 2026-10-01: this subtest too was claimed by the brief to
	// "already pass (no legal complete step to slide in their fixtures)"
	// — also false as originally written: e1 here WAS an older, complete,
	// non-floor step, so FR-030/MAJ-CW-004 slides it (and the oversized
	// leading user message ahead of it) away whole, trivially satisfying
	// the trigger (verified: skip 0->3, share_after=9) and defeating the
	// "target unreachable" premise entirely — the panic this produced was
	// hidden behind the first two subtests' panics until they were fixed.
	// Redesigned so the brief's original claim actually holds: e1 and f1
	// are now BOTH parallel calls of the single (floor) step, so there is
	// genuinely no older step to slide — the oversized user message is the
	// one thing relief can never reach (not a "tool" message, no step
	// wraps it), matching DS-5 #7's real invariant.
	t.Run("target unreachable, trigger satisfied: continue with no error (B-36b / DS-5 #7, FR-030/031 MAJ-CW-004)", func(t *testing.T) {
		al, agent := midTurnFixture(t, 40_000, 0)
		key := "midturn-unreachable-target"
		budget := agentContextBudget(agent)
		original := proseOfTokens(budget / 5)
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			// An oversized non-tool message relief can never reach — not a
			// "tool" message, and there is no older step before the floor
			// to slide — but small enough that fully shortening the
			// floor's own e1 result toward mark-only (FR-031) satisfies B.
			{Role: "user", Content: proseOfTokens(budget * 95 / 100)},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				toolCallFor("e1", "a"), toolCallFor("f1", "b"),
			}},
			{Role: "tool", ToolCallID: "e1", Content: original},
			{Role: "tool", ToolCallID: "f1", Content: "tiny floor"},
		})
		require.Greater(t, requestTokens(window, nil), budget, "precondition: total fired")

		out, err := al.midTurnWindowCheck(ts, window, nil)
		require.NoError(t, err, "trigger back under B: the turn continues even though the 0.8·B target is unreachable")
		byID := msgsByToolCallID(out)
		e1After, ok := byID["e1"]
		require.True(t, ok, "e1 stays in the window — structurally protected as part of the floor step (FR-031)")
		assert.Less(t, len(e1After.Content), len(original),
			"e1's text was shortened toward mark-only — never emptied in place or slid away, it is part of the floor")
		assert.Greater(t, requestTokens(out, nil), budget*4/5, "target genuinely unreachable: the oversized user message dominates")
		assert.LessOrEqual(t, requestTokens(out, nil), budget, "…but the trigger is satisfied")
	})
}

// TestMidTurnBudget_NewestSharePressureShortensAndSends preserves issue #775's
// share-only pressure shape, with the oracle superseded by ADR-066's 2026-09-30
// amendment (MAJ-CW-001/002/004/007/010). Newest-step structure is protected,
// not its text: shorten head/tail with an addressed mark, preserve the full
// archive, and send the request instead of returning a local size-only error.
func TestMidTurnBudget_NewestSharePressureShortensAndSends(t *testing.T) {
	// Pin the already-resolved window: this exercises relief, not model resolution
	// or admission caps, just as the original synthetic over-share residue did.
	h := cwR1New(t, 770_000)
	r, p := cwR1OpenAI(t, 0)
	user := providers.Message{Role: "user", Content: proseOfTokens(100_000)}
	ts := h.turn(user.Content)
	full := "newest-result head\n" + proseOfTokens(387_500) + "\nnewest-result tail"
	h.append(t, user,
		providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{cwR1Call("floor")}},
		providers.Message{Role: "tool", ToolCallID: "floor", Content: full})
	window := h.agent.Sessions.GetHistory(h.key)
	archiveBefore := h.archive(t)
	require.Len(t, window, 3, "instrument: one user and one complete newest step")
	require.Equal(t, full, archiveBefore[2].Content, "instrument: the full source is archived")

	budget := agentContextBudget(h.agent)
	shareLimit := toolResultShareLimit(h.cfg.Context, h.agent.ContextWindow)
	// MAJ-CW-007: default 0.5 × W=770,000 gives S=385,000; the reachable
	// proactive target is 0.8 × S=308,000, independently of the total budget B.
	require.Equal(t, 385_000, shareLimit)
	noteTokens := h.al.ephemeralSystemNoteTokens(ts) + h.al.manifestNoteTokens(ts, h.cfg)
	require.Less(t, requestTokens(window, nil)+noteTokens, budget,
		"precondition: the total-budget bound did not fire")
	require.Greater(t, toolResultShareTokens(window), shareLimit,
		"precondition: only the tool-result-share bound fired")
	// Precondition by construction, not by eligibility re-check: window has
	// exactly one tool result (the floor set, following the one and only
	// assistant step) and D5's own standalone eligibility pass
	// (eligibleToolResults) was retired after the R1 GREEN refactor — no
	// older result exists in this fixture for any pass, old or new, to
	// empty or slide; newest structure must remain.

	out, err := h.al.midTurnWindowCheck(ts, window, nil)
	require.NoError(t, err, "MAJ-CW-004/010: newest share pressure must not end the turn locally")
	projected := cwR1Result(t, out, "floor")
	require.Less(t, len(projected.Content), len(full), "newest result text must actually shorten")
	if strings.HasPrefix(projected.Content, "{") {
		cwR1AssertProjection(t, projected.Content, full, "floor", 2, 0)
	} else {
		start, end := strings.Index(projected.Content, "\n{"), strings.LastIndex(projected.Content, "}\n")
		require.GreaterOrEqual(t, start, 0, "shortened head must precede an intact addressed mark")
		require.Greater(t, end, start, "shortened tail must follow the addressed mark")
		// Source is ASCII, so these byte lengths are also the retained rune counts.
		// The cap is not fixed by the ADR; the exact source halves and mark are.
		kept := len(projected.Content[:start]) + len(projected.Content[end+2:])
		require.Positive(t, kept, "capped state retains source text; mark-only uses emptied state")
		cwR1AssertProjection(t, projected.Content, full, "floor", 2, kept)
	}
	require.Equal(t, archiveBefore, h.archive(t), "relief must preserve every full archive line")

	// Exercise real final assembly and the HTTP adapter, not a direct mock call.
	rr := cwR1Flow(h, ts, out, p)
	cwR1Send(t, rr)
	requests := r.requests(t)
	require.Len(t, requests, 1, "the relieved turn sends exactly one successful provider request")
	sent := cwR1Messages(t, requests[0])
	for _, view := range [][]providers.Message{out, sent} {
		cwR1AssertAnchor(t, view, user)
		cwR1AssertComplete(t, view)
		var assistants []providers.Message
		for _, message := range view {
			if message.Role == "assistant" {
				assistants = append(assistants, message)
			}
		}
		require.Equal(t, []providers.Message{window[1]}, assistants,
			"newest assistant call, ids, arguments and ordering must remain exact")
		require.Equal(t, projected, cwR1Result(t, view, "floor"),
			"the exact shortened result role, correlation and slot survive final serialization")
		require.LessOrEqual(t, toolResultShareTokens(view), 308_000,
			"MAJ-CW-004: attainable fired-share target is 80%% of S, not a fatal residue guard")
	}
	require.Equal(t, archiveBefore, h.archive(t), "sending must not rewrite the full archived source")
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
//
// RELOCATED 2026-10-01 (architect ruling, already confirmed against this
// code): contextResidueOverflowsTotal moved from the shared midTurnWindowCheck
// per-result checkpoint to checkpointRequest (pkg/agent/window_runtime.go) —
// the real post-note-assembly final-request checkpoint — firing only when
// the assembled candidate is still over budget/share AND the window would
// have fit WITHOUT the notes, i.e.
// requestTokens(retainLiveWindow(rq.ri.messages, candidate), toolDefs) <= budget
// (and the matching share bound). midTurnWindowCheck structurally never
// carries request-only notes, so it can no longer reach this counter. This
// test now drives checkpointRequest the same way loop_run_turn.go's
// prepareLLMRequest does: build the candidate exactly as prepareCallMessages
// does for the workspace-instructions note (injectWorkspaceInstructions +
// buildWorkspaceInstructionsNote), then call checkpointRequest(true) on the
// same minimal agentLoopRunTurnRequest chain cwR1Flow
// (cw_slide_r1_transport_test.go) builds for the full-turn harness — true
// because this test exercises the counting discriminator itself (both the
// positive and negative-control subtests), not loop_run_turn.go's real
// prepare-side telemetry call, which always passes false
// (gap-5 fix, 2026-10-01: checkpointRequest gates the relocated
// contextResidueOverflowsTotal increment behind a countOverflow bool —
// the retry-closure in callLLMWithRetries is the sole production caller
// that passes true).
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

	t.Run("positive: note-only overflow at checkpointRequest increments the counter (C1 preserved, relocated)", func(t *testing.T) {
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

		// Reproduce prepareCallMessages' own injection for this one note —
		// the exact call shape of pkg/agent/loop_run_turn.go's
		// injectWorkspaceInstructions(callMessages, buildWorkspaceInstructionsNote(...)) call.
		candidate := injectWorkspaceInstructions(window, buildWorkspaceInstructionsNote(wsID))
		require.Greater(t, requestTokens(candidate, nil), budget,
			"precondition: the assembled candidate (window + note) is what now overflows B")

		rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background()}
		rf := &agentLoopRunTurnFallbacks{rt: rt, callMessages: candidate}
		ri := &agentLoopRunTurnIteration{rf: rf, messages: window}
		rq := &agentLoopRunTurnRequest{ri: ri}

		before := ContextResidueOverflowsTotal()
		err := rq.checkpointRequest(true)
		require.NoError(t, err, "FR-032 amendment: a note-only overflow (nothing eligible to empty, "+
			"window fits without the notes) must not end the turn — the provider's own context error "+
			"is the backstop, not this guard")
		assert.Equal(t, window, rq.ri.messages,
			"nothing was eligible to empty; the live slice is returned unchanged")
		assert.Greater(t, ContextResidueOverflowsTotal(), before,
			"C1 preserved, relocated: checkpointRequest still measured and logged the note-inflated "+
				"total instead of silently treating the turn as fitting")
	})

	t.Run("negative control: a window that is irreducibly over budget on its own must NOT increment the counter", func(t *testing.T) {
		// Architect's own discriminator (restated above): the counter fires
		// only when the LIVE window — WITHOUT request-only notes — would
		// have fit both bounds. Here nothing is injected (no WorkspaceID, so
		// buildWorkspaceInstructionsNote resolves no default workspace and
		// returns "") and the window itself — one oversized message with no
		// tool call for slideOldest/shortenNext to act on — is over budget
		// for a reason that has nothing to do with notes. The relocated
		// counter must stay narrow to notes specifically, not degrade into a
		// generic "still over budget" catch-all.
		key := "midturn-c1-negative-control"
		window, ts := seedMidTurn(t, agent, key, []providers.Message{
			{Role: "user", Content: proseOfTokens(budget * 2)},
		})
		require.Empty(t, ts.opts.WorkspaceID, "precondition: no workspace note can be injected")
		require.Empty(t, buildWorkspaceInstructionsNote(ts.opts.WorkspaceID),
			"precondition: nothing to inject for this turn")
		require.Greater(t, requestTokens(window, nil), budget,
			"precondition: the window alone (no notes) already exceeds B — irreducible pressure")

		rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background()}
		rf := &agentLoopRunTurnFallbacks{rt: rt, callMessages: window}
		ri := &agentLoopRunTurnIteration{rf: rf, messages: window}
		rq := &agentLoopRunTurnRequest{ri: ri}

		before := ContextResidueOverflowsTotal()
		err := rq.checkpointRequest(true)
		require.NoError(t, err, "irreducible overflow is still not a local size-only failure (MAJ-CW-004/010)")
		assert.Equal(t, before, ContextResidueOverflowsTotal(),
			"the relocated counter must not fire here: the live window is ALSO over budget without "+
				"any notes, so the notes-specific discriminator must stay false")
	})
}

// TestMidTurnBudget_C1_NotesStillTriggerEmptying preserves C1's note-driven
// TRIGGER and 80% TARGET at the actual assembled-request checkpoint. R1
// moved request-only note accounting from midTurnWindowCheck to
// checkpointRequest. FR-030/032's 2026-09-30 amendment also tries a legal
// whole-step slide before emptying: here the older e1 step slides, while the
// initiating user anchor and newest f1 step survive exactly. The historical
// test name is retained; asserting an in-place empty would now pin the wrong
// relief operation. The same request without the note must be a no-op.
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
	ts.userMessage = window[0].Content // the real current turn's initiating user anchor

	require.LessOrEqual(t, requestTokens(window, nil), budget,
		"precondition: the bare messages slice alone fits B — the C1 bug is invisible without this")
	resolvedWindow, _, _ := agent.windowSnapshot()
	require.LessOrEqual(t, toolResultShareTokens(window), toolResultShareLimit(al.GetConfig().Context, resolvedWindow),
		"precondition: share alone does not fire — only the assembled note can cause relief")

	store, supportsWindow := agent.Sessions.(session.ContextWindowStore)
	require.True(t, supportsWindow)
	start, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	require.Zero(t, start.State.Skip, "instrument: both complete steps start in the live window")

	// Match prepareCallMessages: pinned system instructions first, with the
	// real workspace note injected separately, never persisted as history.
	core := providers.Message{Role: "system", Content: "pinned test instructions"}
	bareRequest := append([]providers.Message{core}, window...)
	require.LessOrEqual(t, requestTokens(bareRequest, nil), budget)
	rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background()}
	rf := &agentLoopRunTurnFallbacks{rt: rt, callMessages: bareRequest}
	ri := &agentLoopRunTurnIteration{rf: rf, messages: window}
	rq := &agentLoopRunTurnRequest{ri: ri}

	before := ContextEmptiesTotal()
	require.NoError(t, rq.checkpointRequest(false))
	assert.Equal(t, window, ri.messages, "negative control: the same history without notes needs no relief")
	assert.Equal(t, bareRequest, rf.callMessages, "negative control: no request content changed")
	unrelieved, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, start, unrelieved, "negative control: no window/archive state changed")

	note := buildWorkspaceInstructionsNote(wsID)
	require.NotEmpty(t, note)
	rf.callMessages = injectWorkspaceInstructions(bareRequest, note)
	require.Greater(t, requestTokens(rf.callMessages, nil), budget,
		"precondition: the actual assembled request exceeds B only because the workspace note is present")
	require.NoError(t, rq.checkpointRequest(false), "C1: note-driven relief must not end the turn")

	// FR-030/032: e1 is a complete older step, so slide it before emptying.
	// Skip crosses the first three archive lines; the user at line 0 remains
	// anchored, alongside f1's exact assistant call and tiny result.
	wantLive := []providers.Message{window[0], window[3], window[4]}
	assert.Equal(t, wantLive, ri.messages, "the assembled note must cause real whole-step relief")
	assert.Equal(t, wantLive, agent.Sessions.GetHistory(key), "the committed live view matches the request")
	assert.NotContains(t, msgsByToolCallID(rf.callMessages), "e1", "the older result is slid out, not retained or emptied")
	assert.Equal(t, window[4], msgsByToolCallID(rf.callMessages)["f1"], "the newest result survives exactly")
	assert.Contains(t, rf.callMessages, providers.Message{Role: "system", Content: note}, "the full workspace note survives relief")
	assert.True(t, strings.HasPrefix(rf.callMessages[0].Content, core.Content), "pinned instructions survive breadcrumb rebuilding")
	assert.LessOrEqual(t, requestTokens(rf.callMessages, nil), budget*4/5, "the actual assembled request reaches the 80% target")
	assert.Equal(t, before, ContextEmptiesTotal(), "a whole-step slide is not an in-place empty")

	relieved, err := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, 3, relieved.State.Skip, "the older complete prefix occupies exactly three archive lines")
	require.NotNil(t, relieved.State.AnchorLine, "the initiating user remains anchored after Skip passes it")
	assert.Zero(t, *relieved.State.AnchorLine, "the anchor keeps its original archive identity")
	assert.Equal(t, start.Archive, relieved.Archive, "all full archived messages remain unchanged")
}

// TestMidTurnBudget_ResidueRegression_NotesAloneDoNotEndTurn — direct
// regression test for the FR-032 residue bug the C1 fix (4d357904)
// introduced: it added noteTokens (the un-emptiable ephemeral system
// notes — scratchpad, AGENT.md, web-rendering, compressed manifest) to the
// residue midTurnPassCanSucceed refuses on, making those notes count
// against the thrash guard even though D5 can never empty them. A
// large-but-legal AGENT.md (D9's 262,144-byte cap has no budget-aware
// clamp) then made every tool-calling turn on that workspace end with
// ErrContextUnrecoverable — permanently, since the cause is static
// configuration, not an injected fault (ADR-066 §7).
//
// Scenario, distinct from C1's positive subtest (which has NO tool results
// at all, so D5 is a structural no-op): the ONLY tool result present IS the
// entire floor set — the last (and only) assistant step's own result.
// shortenNext (pkg/agent/window_relief.go) treats the newest step's result
// as eligible too — halving it repeatedly as pressure demands, which this
// scenario's exhaustive loop drives all the way to content_state=emptied,
// verified empirically (CHECK-A note: an earlier draft of this fix assumed
// the newest step's result could only ever be shortened, never emptied;
// that assumption does not hold — shortenResult marks state=Emptied once
// `kept` reaches 0, which repeated halving reaches). The AGENT.md note is
// sized (like C1's) so it alone already exceeds B: the same robust
// technique C1 uses, and for the same reason — it makes the residue
// irreducible no matter how exhaustively D5 works the floor result, so the
// counter/no-fatal-exit assertions below hold regardless of exactly how far
// shortenNext gets. What this test actually proves, distinct from C1: D5
// performing real, exhaustive work on the window (not a no-op) still ends
// in the correct outcome — no fatal exit, and the residue still observed.
func TestMidTurnBudget_ResidueRegression_NotesAloneDoNotEndTurn(t *testing.T) {
	al, agent := midTurnFixture(t, 40_000, 0)
	budget := agentContextBudget(agent)
	require.Positive(t, budget)

	home := os.Getenv(config.EnvHome)
	require.NotEmpty(t, home, "midTurnFixture must set OMNIPUS_HOME")
	wsID := "residue-regression-ws"
	wsDir := filepath.Join(home, "workspaces", wsID)
	require.NoError(t, os.MkdirAll(wsDir, 0o755))
	agentMD := proseOfTokens(budget * 6 / 5) // ~1.2x budget, mirrors C1: the note alone exceeds B
	require.NoError(t, os.WriteFile(filepath.Join(wsDir, "AGENT.md"), []byte(agentMD), 0o644))

	key := "midturn-residue-regression"
	floor := proseOfTokens(budget / 2) // ~0.5B: a large single result, distinct from C1's empty window
	window, ts := seedMidTurn(t, agent, key, []providers.Message{
		{Role: "user", Content: "run the one tool"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("f1", "a")}},
		{Role: "tool", ToolCallID: "f1", Content: floor},
	})
	ts.opts.WorkspaceID = wsID

	windowOnly := requestTokens(window, nil)
	require.LessOrEqual(t, windowOnly, budget,
		"precondition: the window itself already fits B without any D5 relief")
	noteTokens := al.ephemeralSystemNoteTokens(ts)
	require.Greater(t, noteTokens, budget,
		"precondition: the injected workspace-instructions note alone exceeds B — no amount of D5 "+
			"shortening of the floor result can ever bring the total under B")

	// Reproduce prepareCallMessages' own injection for this one note — the
	// exact call shape of pkg/agent/loop_run_turn.go's
	// injectWorkspaceInstructions(callMessages, buildWorkspaceInstructionsNote(...))
	// call — so the candidate matches what checkpointRequest receives for
	// real turns (architect's ruling: the counter now lives at the final
	// assembled-request checkpoint, not the old per-result path).
	candidate := injectWorkspaceInstructions(window, buildWorkspaceInstructionsNote(wsID))
	require.Greater(t, requestTokens(candidate, nil), budget,
		"precondition: the assembled candidate (window + note) is what now overflows B")

	rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background()}
	rf := &agentLoopRunTurnFallbacks{rt: rt, callMessages: candidate}
	ri := &agentLoopRunTurnIteration{rf: rf, messages: window}
	rq := &agentLoopRunTurnRequest{ri: ri}

	before := ContextResidueOverflowsTotal()
	err := rq.checkpointRequest(true)
	require.NoError(t, err,
		"REGRESSION: FR-032's fatal exit is reserved for an injected fault (a non-tool message itself "+
			"oversized), never for a configuration-size condition like an oversized AGENT.md — the un-emptiable "+
			"notes must not end an otherwise-fitting turn")
	assert.NotEqual(t, window, rq.ri.messages,
		"distinct from C1's positive subtest (an empty window D5 can only ever return unchanged): here "+
			"D5 has a real tool result to work with and exhausts every operation available on it "+
			"(shortenNext repeatedly halves the newest step's result, eventually emptying it) trying — "+
			"and failing — to relieve the irreducible notes-driven pressure")
	assert.Greater(t, ContextResidueOverflowsTotal(), before,
		"even after D5 exhausts every operation available on the window, the note-driven residue is "+
			"still observed and logged (one ERROR), never silently swallowed")
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
