// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_pending_note_test.go covers ADR-078 D2: the per-turn ephemeral
// pending-goal context note (buildGoalPendingNote/injectGoalPendingNote,
// goal_pending_note.go) and its C1 budget accounting
// (ephemeralSystemNoteTokens, midturn_budget.go). Mirrors the
// twoPhaseHarness/setGoal fixtures already established by
// goal_two_phase_test.go.

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// TestGoalPendingNote_InjectedWhileFreshPending is spec item 1 (ADR-078
// test plan #1): with GoalPendingJSON set and GoalCondition == "" (a fresh
// pending goal), buildGoalPendingNote returns a non-empty note containing
// the intent and each criterion's plain-language text.
func TestGoalPendingNote_InjectedWhileFreshPending(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved"), nil
		}, nil)
	setGoal(t, al, agentInst, opts, "write the report")

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.GoalPendingJSON == "" || meta.GoalCondition != "" {
		t.Fatalf("setup: want a fresh pending goal, got %+v", meta)
	}

	note := buildGoalPendingNote(store, sid)
	if note == "" {
		t.Fatal("buildGoalPendingNote returned empty for a fresh pending goal")
	}
	if !strings.Contains(note, "write the report") {
		t.Fatalf("note missing the intent, got:\n%s", note)
	}
	if !strings.Contains(note, "the report is saved") {
		t.Fatalf("note missing the criterion text, got:\n%s", note)
	}
	if !strings.Contains(note, "awaiting") {
		t.Fatalf("note must state the goal is awaiting confirmation, got:\n%s", note)
	}
	if !strings.Contains(note, ConfirmGoalWord) {
		t.Fatalf("note must steer the user to the deterministic confirm word/button, got:\n%s", note)
	}

	// The injector splices it as a system message at index 1, mirroring
	// injectWorkspaceInstructions' contract.
	msgs := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hi"},
	}
	out := injectGoalPendingNote(msgs, note)
	if len(out) != 3 {
		t.Fatalf("want 3 messages after injection, got %d", len(out))
	}
	if out[1].Role != "system" || out[1].Content != note {
		t.Fatalf("injected note not at index 1: %+v", out[1])
	}
}

// TestGoalPendingNote_EmptyWhenNoPendingOrClarificationOrActive is spec
// item 2 (ADR-078 test plan #2): buildGoalPendingNote returns "" for the
// three gates in D2/D4 — no goal at all, a pending CLARIFICATION, and an
// ACTIVE goal (including an active goal's own pending amendment window).
func TestGoalPendingNote_EmptyWhenNoPendingOrClarificationOrActive(t *testing.T) {
	t.Run("no_goal_at_all", func(t *testing.T) {
		al, agentInst, _, store, sid, _ := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				t.Fatal("no /goal was issued — the LLM must never be called")
				return nil, nil
			}, nil)
		_ = agentInst
		if note := buildGoalPendingNote(store, sid); note != "" {
			t.Fatalf("want empty note with no goal at all, got:\n%s", note)
		}
		_ = al
	})

	t.Run("empty_or_nil_store_or_session", func(t *testing.T) {
		if note := buildGoalPendingNote(nil, "some-session"); note != "" {
			t.Fatalf("nil store must yield empty note, got:\n%s", note)
		}
		_, _, _, store, _, _ := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				t.Fatal("unused")
				return nil, nil
			}, nil)
		if note := buildGoalPendingNote(store, ""); note != "" {
			t.Fatalf("empty sessionID must yield empty note, got:\n%s", note)
		}
	})

	t.Run("pending_clarification_excluded", func(t *testing.T) {
		al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return questionJSON("Which one?"), nil
			}, nil)
		setGoal(t, al, agentInst, opts, "ambiguous goal")

		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatalf("GetMeta: %v", err)
		}
		if meta.GoalClarificationJSON == "" {
			t.Fatal("setup: clarification must be pending")
		}
		if note := buildGoalPendingNote(store, sid); note != "" {
			t.Fatalf("a pending clarification must NOT get the pending-goal note "+
				"(it has its own conversational surface), got:\n%s", note)
		}
	})

	t.Run("active_goal_amendment_window_excluded", func(t *testing.T) {
		al, agentInst, provider, store, sid, opts := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return nil, nil // marker-only + deterministic amendment: never called
			}, nil)
		allowBashPolicy(agentInst)
		// Activate a goal immediately via the marker-only (zero-LLM) path.
		setGoal(t, al, agentInst, opts, "[tests]")
		// Restate over the now-ACTIVE goal: this parks a pending AMENDMENT
		// while GoalCondition stays set — the D4 exclusion.
		_, handled, reply := setGoal(t, al, agentInst, opts, "the docs also read well")
		if !handled || !strings.Contains(reply, "amendment") {
			t.Fatalf("setup: want a deterministic amendment echo, got handled=%v reply=%q", handled, reply)
		}
		if provider.callCount() != 0 {
			t.Fatalf("active-goal restate must not call the LLM, got %d calls", provider.callCount())
		}

		meta, err := store.GetMeta(sid)
		if err != nil {
			t.Fatalf("GetMeta: %v", err)
		}
		if meta.GoalPendingJSON == "" || meta.GoalCondition == "" {
			t.Fatalf("setup: want GoalPendingJSON set AND GoalCondition still set (active amendment window), got %+v", meta)
		}

		if note := buildGoalPendingNote(store, sid); note != "" {
			t.Fatalf("an active goal's own pending amendment must NOT get the "+
				"\"awaiting confirmation, not yet active\" note (it contradicts the running goal), got:\n%s", note)
		}
	})
}

// TestGoalPendingNote_BudgetAccounted is spec item 3 (ADR-078 test plan #3,
// guards the loop.go C1 contract): ephemeralSystemNoteTokens must include
// the pending-note's tokens when a fresh pending goal exists — otherwise
// the pre-turn window-budget check under-counts the real assembled
// request by however large the pending-goal note is.
func TestGoalPendingNote_BudgetAccounted(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved", "the appendix is attached", "the summary is under 200 words"), nil
		}, nil)
	setGoal(t, al, agentInst, opts, "write a long detailed quarterly report with several sections")

	meta, err := store.GetMeta(sid)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if meta.GoalPendingJSON == "" {
		t.Fatal("setup: pending goal required")
	}

	ts := newTurnState(agentInst, processOptions{
		SessionKey:          "budget-key",
		TranscriptStore:     store,
		TranscriptSessionID: sid,
	}, turnEventScope{turnID: "goal-pending-budget-unit"})

	withGoal := al.ephemeralSystemNoteTokens(ts)
	if withGoal <= 0 {
		t.Fatalf("ephemeralSystemNoteTokens with a fresh pending goal = %d, want > 0", withGoal)
	}

	// Same turnState shape but pointed at a session with no goal at all —
	// isolates the pending-note's own contribution.
	emptyStore, emptySID := newGoalTestSession(t, al, agentInst.ID)
	tsNoGoal := newTurnState(agentInst, processOptions{
		SessionKey:          "budget-key",
		TranscriptStore:     emptyStore,
		TranscriptSessionID: emptySID,
	}, turnEventScope{turnID: "goal-pending-budget-unit-control"})
	withoutGoal := al.ephemeralSystemNoteTokens(tsNoGoal)

	if withGoal <= withoutGoal {
		t.Fatalf("ephemeralSystemNoteTokens must be strictly larger with a fresh pending goal present: "+
			"with=%d without=%d", withGoal, withoutGoal)
	}

	noteTokens := estimateMessageTokens(providers.Message{Role: "system", Content: buildGoalPendingNote(store, sid)})
	if withGoal-withoutGoal != noteTokens {
		t.Fatalf("the pending-note's own token delta = %d, want exactly %d (the note's own estimated cost, "+
			"with nothing else differing between the two sessions)", withGoal-withoutGoal, noteTokens)
	}
}

// TestGoalTwoPhase_PendingConfirmReplyTaxonomy_CarriesNote is a sibling to
// goal_two_phase_test.go's TestGoalTwoPhase_PendingConfirmReplyTaxonomy
// (ADR-078 test plan #4): re-asserts that the router's taxonomy is
// unchanged by this ADR — a bare "confirm" still activates, and a bare
// non-confirm reply still leaves the pending goal intact — AND additionally
// asserts the subsequent turn would now carry the pending-goal note (the
// new, context-aware half of case (c) in ADR-078 D3's behavioral
// guarantee).
func TestGoalTwoPhase_PendingConfirmReplyTaxonomy_CarriesNote(t *testing.T) {
	al, agentInst, _, store, sid, opts := twoPhaseHarness(t,
		func(int, []providers.Message) (*providers.LLMResponse, error) {
			return compileJSON("the report is saved"), nil
		}, nil)
	setGoal(t, al, agentInst, opts, "write the report")
	meta, _ := store.GetMeta(sid)
	if meta.GoalPendingJSON == "" {
		t.Fatal("setup: pending goal required")
	}

	t.Run("bare_confirm_still_activates", func(t *testing.T) {
		handled, _ := al.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: "confirm", UserInitiated: true}, agentInst, opts)
		if handled {
			t.Fatal("a fresh-pending confirm must NOT answer synchronously — the turn continues into round 1")
		}
		after, _ := store.GetMeta(sid)
		if after.GoalCondition == "" || after.GoalPendingJSON != "" {
			t.Fatalf("bare confirm must still activate (router unchanged): %+v", after)
		}
	})

	t.Run("bare_non_confirm_leaves_pending_intact_and_next_turn_carries_the_note", func(t *testing.T) {
		al2, agentInst2, _, store2, sid2, opts2 := twoPhaseHarness(t,
			func(int, []providers.Message) (*providers.LLMResponse, error) {
				return compileJSON("the report is saved"), nil
			}, nil)
		setGoal(t, al2, agentInst2, opts2, "write the report")

		handled, reply := al2.applyGoalPendingReply(context.Background(),
			bus.InboundMessage{Content: "hey, unrelated question about the weather", UserInitiated: true}, agentInst2, opts2)
		if handled || reply != "" {
			t.Fatalf("ordinary chat must still pass through untouched (router unchanged), got handled=%v reply=%q", handled, reply)
		}
		after, _ := store2.GetMeta(sid2)
		if after.GoalPendingJSON == "" || after.GoalCondition != "" {
			t.Fatal("a routine chat message must still never silently mutate goal state (US-3 S9)")
		}

		// The new, context-aware half: the turn that runs next carries the
		// pending-goal note (ADR-078 D3's case (c)).
		note := buildGoalPendingNote(store2, sid2)
		if note == "" {
			t.Fatal("the subsequent turn must carry the pending-goal note (ADR-078 D2/D3)")
		}
		if !strings.Contains(note, "write the report") {
			t.Fatalf("note must describe the still-pending goal, got:\n%s", note)
		}
	})
}
