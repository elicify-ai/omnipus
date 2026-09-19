// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT 2026-09-14 (B-1 run 4) regression coverage for the goal first-move
// doors (set_goal / AskUserQuestion) on the compressed tool surface. Under
// the original lazy-tier contract the doors were ManifestLazy, could vanish
// from ADR-088 D3's widened escape surface, and appendGoalDoorsAfterEscape
// (tool_manifest.go) re-added them — in the live run the turn looped on
// set_todos 137 times instead of registering. ADR-090 §5.4 promoted both
// doors into the global upfront set, making the first-move guarantee
// STRUCTURAL: an allowed door is a callable def on every request. The
// escape-door machinery remains as defense in depth (its dedup no-op is
// pinned below), and the policy boundary — doors come only from the
// policy-filtered set, never force-included — is unchanged.
package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestBuildCompressedToolDefs_GoalEscapeKeepsFirstMoveDoorsCallable(t *testing.T) {
	// ADR-090 §5.4 promoted BOTH doors into the global upfront set
	// (ManifestFull). The old precondition (both lazy) is superseded: the
	// first-move guarantee is now structural — an allowed door is a callable
	// def on EVERY request, so the UAT B-1 run 4 failure mode (doors silently
	// absent from the widened surface) can no longer occur at all. If either
	// door is ever demoted from ManifestFull, this precondition fails loudly:
	// the escape-door machinery below would again be the only thing standing
	// between the model and a recordless loop.
	for _, name := range []string{tools.SetGoalToolName, tools.AskUserQuestionToolName} {
		if tier := tools.ToolManifestTier(name); tier != tools.ManifestFull {
			t.Fatalf("precondition: %s must be ManifestFull (ADR-090 §5.4 global upfront set) for this test to mean anything, got %v", name, tier)
		}
	}

	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	allowGoalToolsPolicy(agentInst)

	names := func(defs []providers.ToolDefinition) map[string]bool {
		out := make(map[string]bool, len(defs))
		for _, d := range defs {
			out[d.Function.Name] = true
		}
		return out
	}
	counts := func(defs []providers.ToolDefinition) map[string]int {
		out := make(map[string]int, len(defs))
		for _, d := range defs {
			out[d.Function.Name]++
		}
		return out
	}
	policyFiltered := func() []tools.Tool {
		f, _ := tools.FilterToolsByPolicy(agentInst.Tools.GetAll(), agentInst.AgentType, agentInst.LoadToolPolicy())
		return f
	}
	// recordlessTurn builds a turn on a fresh session carrying an active goal
	// with an empty record — the D3 base predicate — on channel.
	recordlessTurn := func(t *testing.T, goalID, channel string) *turnState {
		t.Helper()
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		setActiveGoalRecordless(t, store, sid, goalID, "improve it")
		return &turnState{agent: agentInst, channel: channel, opts: processOptions{
			TranscriptStore: store, TranscriptSessionID: sid, Channel: channel,
		}}
	}

	t.Run("recordless_webchat_offers_doors_without_any_escape", func(t *testing.T) {
		// The ADR-090 structural guarantee: with the doors upfront and
		// allowed, a recordless goal offers BOTH doors on the very first
		// compressed request — no bounded escape required. (The old
		// "not_escaped_adds_nothing" subtest asserted the inverse under the
		// lazy-tier contract; that contract is superseded by ADR-090 §5.4.)
		ts := recordlessTurn(t, "goal-upfront-web", "webchat")
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if !got[tools.SetGoalToolName] {
			t.Fatalf("set_goal is in the ADR-090 upfront set and allowed — must be callable on turn 1 with the record still empty; got %v", got)
		}
		if !got[tools.AskUserQuestionToolName] {
			t.Fatalf("AskUserQuestion is in the ADR-090 upfront set and allowed — must be callable on turn 1; got %v", got)
		}
		// The fixture policy allows only the two doors, so the rest of the
		// surface is the infra tool. It must survive alongside them.
		if !got["ToolSearch"] {
			t.Fatalf("the doors must sit next to the discovery infrastructure, not replace it (ToolSearch missing); got %v", got)
		}
	})

	t.Run("escaped_recordless_doors_not_duplicated", func(t *testing.T) {
		// The escape machinery still runs (ADR-088 D3 turn widening) but with
		// the doors upfront its door-adder must be a pure no-op: each door
		// already present exactly once, never appended a second time.
		ts := recordlessTurn(t, "goal-escape-web", "webchat")
		ts.armGoalNarrowEscape()
		got := counts(al.buildCompressedToolDefs(ts, policyFiltered()))
		for _, name := range []string{tools.SetGoalToolName, tools.AskUserQuestionToolName, "ToolSearch"} {
			if got[name] != 1 {
				t.Fatalf("%s must appear exactly once in the compressed surface (deduped, no double-add); got count %d", name, got[name])
			}
		}
	})

	t.Run("escaped_but_record_registered_still_offers_doors", func(t *testing.T) {
		// Under the lazy contract the doors were "ordinary lazy tools again"
		// once a record existed. Upfront tools are not conditioned on goal
		// state at all: an allowed door stays callable after registration
		// too (set_goal itself is how a record gets REPLACED via steering —
		// ADR-088's steering-replaces-confirmation posture).
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		armGoalRecord(t, sid, "build a game", recordedGoalCriteria("t"), 0, time.Now())
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
		}}
		ts.armGoalNarrowEscape()
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if !got[tools.SetGoalToolName] || !got[tools.AskUserQuestionToolName] {
			t.Fatalf("upfront doors must stay callable regardless of goal-record state; got %v", got)
		}
	})

	t.Run("channel_origin_offers_set_goal_upfront", func(t *testing.T) {
		// ADR-090's upfront set is channel-agnostic: an allowed door is on
		// the surface from a channel origin too. The webchat-only rule for
		// AskUserQuestion was always an ADR-088 D3 OFFER rule (the escape
		// door-adder's includeAsk gate in goal_loop_forcing.go, plus the
		// execution-layer refusals), never a registration or surface rule —
		// so under the upfront contract AskUserQuestion legitimately appears
		// here as well; what a channel origin must not get is the ask door
		// OFFERED by goal forcing, which this builder does not control.
		ts := recordlessTurn(t, "goal-upfront-telegram", "telegram")
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if !got[tools.SetGoalToolName] {
			t.Fatalf("set_goal (ADR-090 upfront, allowed) must be callable from a channel origin; got %v", got)
		}
		if !got[tools.AskUserQuestionToolName] {
			t.Fatalf("AskUserQuestion (ADR-090 upfront, allowed) is channel-agnostic on the surface — the webchat rule lives in the goal-forcing offer layer, not here; got %v", got)
		}
	})

	t.Run("escaped_set_goal_policy_denied_is_not_forced", func(t *testing.T) {
		// Unchanged boundary, now doubly important: the doors reach the
		// surface ONLY through the policy-filtered set. Upfront membership is
		// a visibility tier, not a grant — a policy-denied set_goal must
		// never be force-included (unlike the registration-gated infra
		// force-include, which deliberately bypasses policy).
		ts := recordlessTurn(t, "goal-escape-denied", "webchat")
		ts.armGoalNarrowEscape()
		agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{
				tools.SetGoalToolName:         config.ToolPolicyDeny,
				tools.AskUserQuestionToolName: config.ToolPolicyAllow,
			},
		})
		defer allowGoalToolsPolicy(agentInst)
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if got[tools.SetGoalToolName] {
			t.Fatal("a policy-denied set_goal must never be force-included — upfront tier or not, the doors come from the policy-filtered set only")
		}
		if !got[tools.AskUserQuestionToolName] {
			t.Fatal("the allowed ask door must remain callable while set_goal is denied")
		}
	})
}
