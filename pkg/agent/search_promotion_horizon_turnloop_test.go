// Omnipus — ADR-071 §4.3.1(a) review-fix regression test
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// search_promotion_horizon_turnloop_test.go covers the BUG 2 fix from the
// tool-manifest-tier-redesign review-fix pass: tickSearchPromotionHorizon's
// call site moved from inside runTurn's turnLoop for-loop body (where it fired
// once per turnLoop round — one per LLM call — because the sibling TTL-tick
// log line's own "iteration" field is the round counter, not a per-turn one)
// to a single point AFTER that for-loop exits, so it fires exactly once per
// real conversational turn regardless of how many rounds of tool-calling that
// turn took. search_promotion_horizon_adr071_test.go already covers the
// primitive (tickSearchPromotionHorizon itself, called directly); this file
// covers the CALL SITE — the thing that actually regressed.

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSearchPromotionHorizon_CountsRealTurnsNotToolCallRounds drives a single
// al.runAgentLoop call (one real conversational turn) through THREE turnLoop
// rounds:
//
//   - round 1: the LLM returns TWO parallel tool calls (proves the fix is not
//     merely "per tool call" either — both must be handled inside one round
//     without an extra tick).
//   - round 2: the LLM returns ONE more tool call — a second round of the
//     SAME turn.
//   - round 3: the LLM returns a direct text answer with no tool calls,
//     ending the turn. This round's own `break` exits turnLoop's for-loop
//     BEFORE ever reaching the per-round TickTTL/tick call site, so it
//     contributes zero additional in-loop ticks either way — the fix and the
//     bug both agree on round 3; the distinguishing rounds are 1 and 2.
//
// Pre-fix: tickSearchPromotionHorizon sat inside the turnLoop for-loop body,
// right after the per-round tool-execution block — so it fired once at the
// end of round 1 and once at the end of round 2 (round 3 never reaches it),
// for 2 ticks across this ONE conversational turn.
//
// Post-fix: the call sits once, after the whole turnLoop for-loop exits — 1
// tick for the whole turn, matching searchPromotionHorizonTurns' own doc
// comment ("counted across the whole conversation").
func TestSearchPromotionHorizon_CountsRealTurnsNotToolCallRounds(t *testing.T) {
	provider := newScriptedProvider(
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "call-1a", Name: "echo_text", Arguments: map[string]any{"text": "a"}},
				{ID: "call-1b", Name: "echo_text", Arguments: map[string]any{"text": "b"}},
			},
		},
		&providers.LLMResponse{
			ToolCalls: []providers.ToolCall{
				{ID: "call-2a", Name: "echo_text", Arguments: map[string]any{"text": "c"}},
			},
		},
		&providers.LLMResponse{Content: "final answer"},
	)

	tmpDirOuter := t.TempDir()
	home := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("failed to create home dir: %v", err)
	}
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	defer al.Close()
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("expected default agent")
	}

	// echoTextTool is defined in hooks_test.go (same package) — a trivial
	// full-tier tool; ToolSearch/lazy-loading machinery is irrelevant to this
	// bug, so a plain always-callable tool keeps the fixture minimal.
	al.RegisterTool(&echoTextTool{})
	// No-default-policy model (CLAUDE.md hard constraint 6): echo_text needs
	// an explicit agent-level grant or every call is denied before the round
	// even completes.
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"echo_text": "allow"},
	})

	const sessionKey = "search-horizon-turn-1"
	resp, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:      sessionKey,
		Channel:         "cli",
		ChatID:          "direct",
		UserMessage:     "do the thing",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	if err != nil {
		t.Fatalf("runAgentLoop failed: %v", err)
	}
	if resp != "final answer" {
		t.Fatalf("resp = %q, want %q", resp, "final answer")
	}
	// Fixture sanity: if the scripted provider was not actually driven
	// through all three rounds, this test cannot distinguish the bug from
	// the fix (both would report the same, smaller count).
	if got := provider.CallCount(); got != 3 {
		t.Fatalf("fixture: provider.CallCount() = %d, want 3 (one per turnLoop round) — the turn "+
			"did not actually drive three rounds", got)
	}

	// The bucket a live turn would derive: manifestBucketKey(agentID,
	// TranscriptSessionID, sessionKey) with TranscriptSessionID == "" here
	// (processOptions above never sets it), matching ts.manifestBucket()'s
	// fallback to sessionKey alone.
	bucket := manifestBucketKey(agent.ID, "", sessionKey)
	al.loadedToolsMu.Lock()
	got := al.bucketTurnCounter[bucket]
	al.loadedToolsMu.Unlock()

	if got != 1 {
		t.Errorf("bucketTurnCounter[%q] = %d, want 1 — tickSearchPromotionHorizon must fire exactly "+
			"once per conversational turn (this turn had 3 turnLoop rounds and 3 tool calls across "+
			"them); a pre-fix in-loop call site would have ticked twice here (once after round 1's "+
			"tool execution, once after round 2's — round 3 breaks out before reaching it)", bucket, got)
	}
}
