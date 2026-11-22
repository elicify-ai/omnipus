// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: TestProgramProvenance_HandOn (order 27) — S-75 (Telegram →
// delegated helper), S-76 (→ peer request), S-77 (→ new board task; the UI
// task is independent), S-78 (provenance survives restart), S-79
// (unresolved chain withholds), S-80 (mixed provenance through a peer
// request), S-82 (model text cannot set provenance); and
// TestProgramProvenance_Updates (order 28) — S-107 (Telegram edits a
// web-created task), S-108 (background completion continues a Telegram
// turn), S-109 (goal follow-up), S-110 (steering an existing helper from
// Telegram), S-123 (web off/Telegram on mixed steering, both orders),
// S-124 (independent work joined with a connector), S-125 (a person's UI
// edit preserves provenance).
//
// The hand-on chains need the runtime seams: provenance on the
// turn-feeding messages and durable write requests (FR-041), the join at
// every handoff, and the program-variable attach (FR-010/FR-011). None
// exists at RED — each scenario reports BLOCKED naming its seam, never a
// skip, with the spec's verbatim expectations recorded beside it for the
// same test author's GREEN reconciliation. (The join algebra itself is
// order 9's committed suite; the per-turn decision is order 26's unit
// rows; the record classification is order 12.)
//
// Oracle: US-5 scenario texts, A-1 (founder Q1), FR-010–FR-012, FR-041,
// R4-M2. Expected values come from the spec, never from the runtime.

package bus

import "testing"

// s1HandOnSeam is the missing runtime all rows share.
const s1HandOnSeam = "provenance on the turn-feeding messages and durable write requests (FR-041), the join at every handoff, and the program-variable attach (FR-010/FR-011) are not implemented"

func TestProgramProvenance_HandOn(t *testing.T) {
	t.Run("S75_telegram_to_delegated_helper", func(t *testing.T) {
		// GREEN asserts here (spec S-75): Telegram off, GitHub on for Dev;
		// the helper's output is 0 with the telegram notice; after Telegram
		// is switched on the same delegation outputs 16.
		t.Fatalf("BLOCKED: %s — required by S-75's helper outputs", s1HandOnSeam)
	})

	t.Run("S76_telegram_to_peer_request", func(t *testing.T) {
		// GREEN asserts here (spec S-76): Telegram off, GitHub on for Ops;
		// Ops's output is 0 with the telegram notice.
		t.Fatalf("BLOCKED: %s — required by S-76", s1HandOnSeam)
	})

	t.Run("S77_telegram_to_new_board_task_ui_task_independent", func(t *testing.T) {
		// GREEN asserts here (spec S-77): the task run outputs 0 with the
		// telegram notice; a task with the same text created by the person
		// in the UI follows A-1 and outputs 16.
		t.Fatalf("BLOCKED: %s — required by S-77's two-task assertion", s1HandOnSeam)
	})

	t.Run("S78_provenance_survives_restart", func(t *testing.T) {
		// GREEN asserts here (spec S-78): a helper launched from a Telegram
		// turn, parked, gateway restarted → resume runs print 0 with the
		// telegram notice.
		t.Fatalf("BLOCKED: %s — required by S-78's restart persistence", s1HandOnSeam)
	})

	t.Run("S79_unresolved_chain_withholds", func(t *testing.T) {
		// GREEN asserts here (spec S-79): a helper whose parent record is
		// unreadable → 0 with the unresolved notice (fail closed).
		t.Fatalf("BLOCKED: %s — required by S-79's unresolved notice", s1HandOnSeam)
	})

	t.Run("S80_mixed_provenance_through_a_peer_request", func(t *testing.T) {
		// GREEN asserts here (spec S-80): telegram allowed, discord not; the
		// peer-consumer run is 0 and the notice names discord.
		t.Fatalf("BLOCKED: %s — required by S-80's mixed join", s1HandOnSeam)
	})

	t.Run("S82_model_text_cannot_set_provenance", func(t *testing.T) {
		// GREEN asserts here (spec S-82): a peer request whose body says
		// "origin: webchat" leaves Ops's turn provenance {connector:telegram}
		// — model text never writes provenance — and outputs 0.
		t.Fatalf("BLOCKED: %s — required by S-82's cannot-forge assertion", s1HandOnSeam)
	})

	t.Run("S111_omitted_producer_dev_command_gets_no_token_and_the_unresolved_notice", func(t *testing.T) {
		// CHECK R11's runtime half, individually named (spec S-111 + FR-010
		// fail-closed): a turn whose producing write request OMITS
		// provenance starts Dev with NO program variables attached — a Dev
		// command that would read a token sees nothing — and the turn
		// carries the unresolved-provenance notice. The omission is fail-
		// closed at the runtime boundary, never zero-value-permissive.
		t.Fatalf("BLOCKED: %s — required by S-111's runtime half (the omitted-producer turn: Dev command sees no token, unresolved notice emitted)", s1HandOnSeam)
	})
}

func TestProgramProvenance_Updates(t *testing.T) {
	t.Run("S107_telegram_edits_a_web_created_task", func(t *testing.T) {
		// GREEN asserts here (spec S-107): the prompt edit joins telegram
		// into the task's stored provenance in the SAME reconciled write; the
		// run outputs 0 with the telegram notice; the same after a restart
		// between the edit and the run.
		t.Fatalf("BLOCKED: %s — required by S-107's same-write join", s1HandOnSeam)
	})

	t.Run("S108_background_completion_continues_a_telegram_turn", func(t *testing.T) {
		// GREEN asserts here (spec S-108): the completion's system message
		// starts Dev's continuation turn → its output is 0 with the telegram
		// notice.
		t.Fatalf("BLOCKED: %s — required by S-108", s1HandOnSeam)
	})

	t.Run("S109_goal_followup_from_a_telegram_created_goal", func(t *testing.T) {
		// GREEN asserts here (spec S-109): the goal keeper's follow-up turn
		// outputs 0 with the telegram notice.
		t.Fatalf("BLOCKED: %s — required by S-109", s1HandOnSeam)
	})

	t.Run("S110_steering_an_existing_helper_from_telegram", func(t *testing.T) {
		// GREEN asserts here (spec S-110): the helper's turn that consumes
		// the steer outputs 0 with the telegram notice.
		t.Fatalf("BLOCKED: %s — required by S-110", s1HandOnSeam)
	})

	t.Run("S123_web_off_telegram_on_mixed_steering_both_orders", func(t *testing.T) {
		// GREEN asserts here (spec S-123): both steering orders output 0
		// with the web-chat notice; both turn provenances are
		// {web, connector:telegram}; the same after a restart before each
		// helper's turn.
		t.Fatalf("BLOCKED: %s — required by S-123's both-orders assertion", s1HandOnSeam)
	})

	t.Run("S124_independent_joined_with_connector_and_ui_regression", func(t *testing.T) {
		// GREEN asserts here (spec S-124): the creator edit is accepted, the
		// task's provenance is {independent, connector:telegram}, output 0
		// (web off per founder Q1 = A) and 16 with web on; a person-created
		// task's edit is refused as today and its prompt and provenance are
		// unchanged.
		t.Fatalf("BLOCKED: %s — required by S-124's join and regression rows", s1HandOnSeam)
	})

	t.Run("S125_person_ui_edit_preserves_provenance", func(t *testing.T) {
		// GREEN asserts here (spec S-125): a person's UI edit leaves the
		// stored provenance exactly {connector:telegram} (no web added,
		// nothing removed) and the run outputs 0; a person's new task is
		// {independent}.
		t.Fatalf("BLOCKED: %s — required by S-125's preserve rule", s1HandOnSeam)
	})
}
