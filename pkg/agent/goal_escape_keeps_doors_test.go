// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// UAT 2026-09-14 (B-1 run 4) regression coverage for
// appendGoalDoorsAfterEscape (tool_manifest.go): after ADR-088 D3's bounded
// escape releases the narrowed first-move request, the compressed tool
// surface must still let the model call set_goal (and, on webchat with the
// question budget unspent, AskUserQuestion) while the goal has no record.
// In the live run both were lazy tools absent from the widened surface, and
// the turn looped on set_todos instead of registering.
package agent

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestBuildCompressedToolDefs_GoalEscapeKeepsFirstMoveDoorsCallable(t *testing.T) {
	// Precondition: both doors are lazy on the compressed surface. If either
	// ever became ManifestFull this test would pass vacuously.
	for _, name := range []string{tools.SetGoalToolName, tools.AskUserQuestionToolName} {
		if tier := tools.ToolManifestTier(name); tier != tools.ManifestLazy {
			t.Fatalf("precondition: %s must be ManifestLazy for this test to mean anything, got %v", name, tier)
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

	t.Run("escaped_recordless_webchat_offers_set_goal_and_ask", func(t *testing.T) {
		ts := recordlessTurn(t, "goal-escape-web", "webchat")
		ts.armGoalNarrowEscape()
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if !got[tools.SetGoalToolName] {
			t.Fatalf("after the bounded escape with the record still empty, set_goal must be callable; got %v", got)
		}
		if !got[tools.AskUserQuestionToolName] {
			t.Fatalf("webchat with the question budget unspent must keep AskUserQuestion callable; got %v", got)
		}
		// This fixture's policy allows only the two doors, so the rest of the
		// surface is the infra tool. It must survive: the doors are ADDED to
		// the compressed surface, never substituted for it.
		if !got["ToolSearch"] {
			t.Fatalf("the doors must be added to the compressed surface, not replace it (ToolSearch missing); got %v", got)
		}
	})

	t.Run("not_escaped_adds_nothing", func(t *testing.T) {
		// Before the escape the real request uses the narrowed pair (layer1),
		// never this builder; the builder itself must not widen anything.
		ts := recordlessTurn(t, "goal-not-escaped", "webchat")
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if got[tools.SetGoalToolName] || got[tools.AskUserQuestionToolName] {
			t.Fatalf("without an escape the lazy doors must stay unloaded; got %v", got)
		}
	})

	t.Run("escaped_but_record_registered_adds_nothing", func(t *testing.T) {
		store, sid := newGoalTestSession(t, al, agentInst.ID)
		armGoalRecord(t, sid, "build a game", recordedGoalCriteria("t"), 0, time.Now())
		ts := &turnState{agent: agentInst, channel: "webchat", opts: processOptions{
			TranscriptStore: store, TranscriptSessionID: sid, Channel: "webchat",
		}}
		ts.armGoalNarrowEscape()
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if got[tools.SetGoalToolName] || got[tools.AskUserQuestionToolName] {
			t.Fatalf("once a record exists the doors are ordinary lazy tools again; got %v", got)
		}
	})

	t.Run("escaped_non_web_offers_set_goal_only", func(t *testing.T) {
		ts := recordlessTurn(t, "goal-escape-telegram", "telegram")
		ts.armGoalNarrowEscape()
		got := names(al.buildCompressedToolDefs(ts, policyFiltered()))
		if !got[tools.SetGoalToolName] {
			t.Fatalf("a channel origin must keep set_goal callable after the escape; got %v", got)
		}
		if got[tools.AskUserQuestionToolName] {
			t.Fatal("AskUserQuestion is web-only and must not be added on a channel origin")
		}
	})

	t.Run("escaped_set_goal_policy_denied_is_not_forced", func(t *testing.T) {
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
			t.Fatal("a policy-denied set_goal must never be force-included — the doors come from the policy-filtered set only")
		}
	})
}
