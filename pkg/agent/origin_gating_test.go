// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// origin_gating_test.go covers ADR-049 Gap #8/r2 (spec Part B FR-075/SD-B6/R6):
// /goal and /loop must be inert unless the turn is user-initiated. Rather than
// asserting on handleCommand's (reply, handled) return — which is
// indistinguishable between "not matched at all" and "matched=true,
// handled=false" (the goal-set rewrite case) — these tests assert on the
// session's persisted UnifiedMeta: did a goal/loop actually START or not,
// exactly mirroring the spec's own Independent Test wording ("assert it is
// inert (no goal started); ... assert a goal starts").
package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func TestOriginGating_GoalLoop(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	if !ok {
		t.Fatal("native-agent not registered")
	}
	store := al.GetSessionStore()
	if store == nil {
		t.Fatal("shared session store not available")
	}

	cases := []struct {
		name          string
		userInitiated bool
		wantStarted   bool
	}{
		// R6: Web WS message handler / channel adapter inbound (real human
		// Sender) both set UserInitiated=true at their origination point —
		// simulated directly here via the field the origin-gating contract
		// actually reads.
		{"user_inbound", true, true},
		// R6: cron/scheduled runner (exec.ProcessScheduled) never publishes
		// through the bus at all — but if it somehow did, it MUST leave this
		// false. Simulated directly.
		{"cron_scheduled", false, false},
		// R6: async-notifier synthesized system message — false.
		{"async_injected", false, false},
		// R6: delegated sub-turn / task-run worker — false.
		{"delegated_subturn", false, false},
	}

	for _, tc := range cases {
		t.Run("goal/"+tc.name, func(t *testing.T) {
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			opts := processOptions{
				TranscriptStore:     store,
				TranscriptSessionID: meta.ID,
				Channel:             "webchat",
				ChatID:              "chat-" + meta.ID,
				SessionKey:          "sk-" + meta.ID,
				UserInitiated:       tc.userInitiated,
			}
			msg := bus.InboundMessage{
				Content:       "/goal make the tests pass",
				UserInitiated: tc.userInitiated,
			}
			if _, handled := al.handleCommand(context.Background(), msg, agentInst, &opts); handled && !tc.userInitiated {
				t.Fatal("a non-user-initiated /goal must never be synchronously handled (status/clear are also gated)")
			}
			// ADR-074 D4a: a user-initiated PROSE set parks as pending and
			// activates on the explicit confirm — drive the confirm through
			// the same origin-gated pipeline. For the non-user-initiated
			// cases the set was inert, and this confirm must be too.
			al.handleCommand(context.Background(), bus.InboundMessage{
				Content:       "/goal confirm",
				UserInitiated: tc.userInitiated,
			}, agentInst, &opts)
			after, err := store.GetMeta(meta.ID)
			if err != nil {
				t.Fatalf("GetMeta: %v", err)
			}
			started := after.GoalCondition != ""
			if started != tc.wantStarted {
				t.Errorf("goal started = %v, want %v (UserInitiated=%v)", started, tc.wantStarted, tc.userInitiated)
			}
		})

		t.Run("loop/"+tc.name, func(t *testing.T) {
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentInst.ID)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}
			ls, _ := newLoopSchedulerForTest(t, al)
			al.SetLoopScheduler(ls)

			opts := processOptions{
				TranscriptStore:     store,
				TranscriptSessionID: meta.ID,
				Channel:             "webchat",
				ChatID:              "chat-" + meta.ID,
				SessionKey:          "sk-" + meta.ID,
				UserInitiated:       tc.userInitiated,
			}
			msg := bus.InboundMessage{
				Content:       "/loop every 5m ping",
				UserInitiated: tc.userInitiated,
			}
			al.handleCommand(context.Background(), msg, agentInst, &opts)
			after, err := store.GetMeta(meta.ID)
			if err != nil {
				t.Fatalf("GetMeta: %v", err)
			}
			started := after.LoopMode != ""
			if started != tc.wantStarted {
				t.Errorf("loop started = %v, want %v (UserInitiated=%v)", started, tc.wantStarted, tc.userInitiated)
			}
		})
	}
}

// TestOriginGating_NonCommandTextUnaffected proves the origin gate is
// specific to /goal and /loop — an ordinary, non-command message from a
// non-user-initiated origin is untouched by this change (handleCommand
// returns matched=false for it regardless, via the leading HasCommandPrefix
// check that runs before any origin check).
func TestOriginGating_NonCommandTextUnaffected(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	agentInst, _ := al.GetRegistry().GetAgent("native-agent")
	opts := processOptions{UserInitiated: false}
	msg := bus.InboundMessage{Content: "just a normal message, not a command", UserInitiated: false}
	if _, handled := al.handleCommand(context.Background(), msg, agentInst, &opts); handled {
		t.Fatal("plain text must never be 'handled' by the command layer")
	}
}
