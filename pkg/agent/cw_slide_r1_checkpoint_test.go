//go:build goolm && stdjson

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Scenario 1; MAJ-CW-004/005/007, B-54, planned test 59, founder Q30=B.
func TestCWSlideR1_CheckpointUsesRelativeBounds(t *testing.T) {
	for _, trigger := range []string{"total_only", "share_only", "both"} {
		t.Run(trigger, func(t *testing.T) {
			h := cwR1New(t, 20_000)
			fraction := 0.5 // MAJ-CW-007 shipped default, not the old absolute setting.
			if trigger == "share_only" {
				fraction = 0.125
				cwR1SetFraction(t, h, fraction)
			}
			b := agentContextBudget(h.agent)
			s := max(1, int(fraction*float64(h.agent.ContextWindow)))
			require.Positive(t, b, "fixture has a positive input budget")
			ts := h.turn("continue this one long turn")
			h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
			for _, id := range []string{"old-a", "old-b", "newest"} {
				narration, result := "", strings.Repeat("readable prose ", b/15)
				if trigger == "total_only" {
					// Each narration is roughly half B; the structural floor fits,
					// three steps do not, while their short results stay below S.
					narration = strings.Repeat("a", b*5/4)
					result = "small readable result"
				} else if trigger == "share_only" {
					result = strings.Repeat("readable prose ", s*5/(4*15))
				}
				h.append(t, cwR1Step(id, narration, result)...)
			}
			before := h.agent.Sessions.GetHistory(h.key)
			total, share := requestTokens(before, nil), toolResultShareTokens(before)
			require.Equal(t, trigger != "share_only", total > b, "fixture's total trigger")
			require.Equal(t, trigger != "total_only", share > s, "fixture's W-based share trigger")
			out := h.check(t, ts, before)
			require.Greater(t, cwR1Skip(t, h), 0,
				"MAJ-CW-005: pressure must advance Skip across completed steps, not merely empty their text")
			require.False(t, cwR1HasCall(out, "old-a"), "oldest complete step must leave the live request")
			require.True(t, cwR1HasCall(out, "newest"), "newest call slots are structural floor")
			cwR1AssertComplete(t, out)
			require.LessOrEqual(t, requestTokens(out, nil), b, "neither bound remains exceeded")
			require.LessOrEqual(t, toolResultShareTokens(out), s, "S derives from W, not B")
			if trigger != "share_only" {
				require.LessOrEqual(t, requestTokens(out, nil), b*4/5, "reachable fired total target is 80%% B")
			}
			if trigger != "total_only" {
				require.LessOrEqual(t, toolResultShareTokens(out), s*4/5, "reachable fired share target is 80%% S")
			}
		})
	}
}

// Equality and +/- one estimator token derive from floor(chars*2/5), with
// W=32768, default f=.5, S=16384. Two declared results each carry 8192 tokens.
func TestCWSlideR1_ShareTriggerIsStrictAtEquality(t *testing.T) {
	for _, tc := range []struct {
		name  string
		chars int
		delta int
	}{{"below", -1, -1}, {"equal", 0, 0}, {"above", 3, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			h := cwR1New(t, 32_768)
			ts := h.turn("boundary")
			calls := []providers.ToolCall{cwR1Call("eq-a"), cwR1Call("eq-b")}
			h.append(t, providers.Message{Role: "user", Content: "boundary"},
				providers.Message{Role: "assistant", ToolCalls: calls},
				providers.Message{Role: "tool", ToolCallID: "eq-a", Content: strings.Repeat("x", 8192*5/2-16+tc.chars)},
				providers.Message{Role: "tool", ToolCallID: "eq-b", Content: strings.Repeat("y", 8192*5/2-16)})
			before := h.agent.Sessions.GetHistory(h.key)
			require.Equal(t, 16_384+tc.delta, toolResultShareTokens(before), "independently derived boundary")
			require.Less(t, requestTokens(before, nil), agentContextBudget(h.agent), "total must not fire")
			meta := h.meta(t)
			out := h.check(t, ts, cwR1Clone(t, before))
			if tc.delta <= 0 {
				require.Equal(t, before, out, "strict > means equality cannot shorten or evict")
				require.Equal(t, meta, h.meta(t), "equality must not persist a projection or Skip")
			} else {
				require.LessOrEqual(t, toolResultShareTokens(out), 16_384*4/5,
					"one token above S must pressure-shorten the newest text to the reachable target")
				cwR1AssertComplete(t, out)
			}
		})
	}
}

func TestCWSlideR1_EveryAdmittedResultMakesRepeatedProgress(t *testing.T) {
	h := cwR1New(t, 12_000)
	r, p := cwR1OpenAI(t, 0)
	ts := h.turn("keep working through ten steps")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	initial := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	rr := cwR1Flow(h, ts, initial, p)
	advances, lastSkip := 0, 0
	for step := 0; step < 10; step++ {
		id := fmt.Sprintf("long-%02d", step)
		assistant := providers.Message{Role: "assistant", Content: strings.Repeat("narration ", 250),
			ToolCalls: []providers.ToolCall{cwR1Call(id)}}
		h.append(t, assistant)
		rr.rq.ri.messages = append(rr.rq.ri.messages, assistant)
		rr.normalizedToolCalls = assistant.ToolCalls
		checks := MidTurnBudgetChecksTotal()
		cwR1FinishResult(t, h, rr, id, strings.Repeat("readable result ", 320), 1, 0)
		require.Equal(t, checks+1, MidTurnBudgetChecksTotal(), "the real admission site checks every result")
		skip := cwR1Skip(t, h)
		require.GreaterOrEqual(t, skip, lastSkip, "Skip cannot move backward within a live turn")
		if skip > lastSkip {
			advances++
		}
		lastSkip = skip
		cwR1Send(t, rr)
		requests := r.requests(t)
		sent := cwR1Messages(t, requests[len(requests)-1])
		cwR1AssertComplete(t, sent)
		cwR1Result(t, sent, id)
		require.LessOrEqual(t, requestTokens(sent, nil), agentContextBudget(h.agent), "actual sent bytes fit total")
		require.LessOrEqual(t, toolResultShareTokens(sent), 6_000, "actual sent bytes fit W/2 share")
	}
	require.GreaterOrEqual(t, advances, 2, "tiny-window ONE turn must make multiple actual mid-turn Skip advances")
	require.Len(t, r.requests(t), 10, "the recording endpoint observed continued provider progress")
	require.Len(t, h.archive(t), 21, "one user + ten whole steps; slides cannot rewrite the archive")
	last := cwR1Messages(t, r.requests(t)[9])
	require.False(t, cwR1HasCall(last, "long-00"), "final serialized request must actually omit the old step")
}

func TestCWSlideR1_FinalAssembledNotesUseSameCheckpoint(t *testing.T) {
	h := cwR1New(t, 40_000)
	r, p := cwR1OpenAI(t, 0)
	ts := h.turn("final assembly")
	h.append(t, providers.Message{Role: "user", Content: ts.userMessage})
	for _, id := range []string{"note-old-a", "note-old-b", "note-new"} {
		h.append(t, cwR1Step(id, strings.Repeat("narration ", 150), strings.Repeat("result prose ", 150))...)
	}
	messages := h.al.assembleMessages(context.Background(), ts, h.agent.Sessions.GetHistory(h.key), "", nil, nil)
	note := buildGoalRubricInjectionNote(true, false)
	require.NotEmpty(t, note, "real request-only rubric note is the fixture instrument")
	base := requestTokens(messages, nil)
	goal := base + estimateMessageTokens(providers.Message{Role: "system", Content: note})/2
	// Solve the SPEC formula B=W-max_tokens-ceil(.05W)-pinned, not a
	// runtime-observed expected slide size. This places B between base and note.
	pinned := pinnedCoreOverheadTokens(h.agent)
	for w := goal + h.agent.MaxTokens + pinned; ; w++ {
		if w-h.agent.MaxTokens-(w+19)/20-pinned >= goal {
			h.agent.ContextWindow = w
			break
		}
	}
	require.Less(t, base, agentContextBudget(h.agent), "pre-note slice is under total")
	pre := h.check(t, ts, cwR1Clone(t, messages))
	require.Equal(t, messages, pre, "no premature pressure before request-only note assembly")
	rr := cwR1Flow(h, ts, pre, p)
	rr.rq.goalForce.rubric = true // Real assembly stage's normal rubric predicate.
	cwR1Send(t, rr)
	require.Greater(t, cwR1Skip(t, h), 0, "final assembled note must trigger the SAME legal-slide checkpoint before send")
	sent := cwR1Messages(t, r.requests(t)[0])
	require.LessOrEqual(t, requestTokens(sent, nil), agentContextBudget(h.agent), "measure actual assembled request, notes once")
	cwR1AssertComplete(t, sent)
}
