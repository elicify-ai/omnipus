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
// The carrier, runtime attachment and record classification now exist.
// Remaining rows below are incomplete real producer/write/dispatch fixtures,
// not assertions that those production mechanisms are all absent. Seeded
// record re-entry tests and provenance-algebra tests do not certify an actual
// delegate, peer, task, goal or UI writer. The original spec oracles stay loud
// until each complete chain is exercised.
//
// Oracle: US-5 scenario texts, A-1 (founder Q1), FR-010–FR-012, FR-041,
// R4-M2. Expected values come from the spec, never from the runtime.

package bus

import "testing"

// These are outstanding test-author fixtures, not accepted deferrals.
const s1HandOnSeam = "the complete actual producer/write/dispatch fixture for this chain is not yet implemented in the test harness; seeded-record and direct-carrier tests do not establish this handoff"

func TestProgramProvenance_HandOn(t *testing.T) {
	// S75_telegram_to_delegated_helper migrated intact to
	// pkg/agent::TestProgramProvenance_ActualDelegatedHelper: actual input ->
	// delegate tool -> launcher -> durable child -> registered bash, exact
	// count0/Telegram notice then count16 after the channel switch is on.
	// The durable carrier is observed from the actual launch, not seeded.

	// S76_telegram_to_peer_request migrated intact to
	// pkg/gateway::TestProgramProvenance_ActualPeerRequest: real Dev send_message,
	// real gateway admission/router/ledger and running Ops turn, exact count0
	// and Telegram notice after the connected Ops count16 positive control.

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

	// S82_model_text_cannot_set_provenance migrated intact to the same actual
	// pkg/gateway peer test: body says origin:webchat, but the actual ledger
	// AND receiver turn carry exactly connector:telegram; real child count0.

	// S111_omitted_producer_dev_command_gets_no_token_and_the_unresolved_notice
	// migrated intact to pkg/agent::TestProgramProvenance_OmittedInputRuntime:
	// real connected positive count16, real omitted-input child count0, exact
	// unresolved notice. A source guard alone cannot substitute for that row.
}

func TestProgramProvenance_Updates(t *testing.T) {
	t.Run("S107_telegram_edits_a_web_created_task", func(t *testing.T) {
		// GREEN asserts here (spec S-107): the prompt edit joins telegram
		// into the task's stored provenance in the SAME reconciled write; the
		// run outputs 0 with the telegram notice; the same after a restart
		// between the edit and the run.
		t.Fatalf("BLOCKED: %s — required by S-107's same-write join", s1HandOnSeam)
	})

	// S108 migrated intact to pkg/agent::TestProgramProvenance_ActualBackgroundHandoff:
	// actual Telegram-input child -> real notifier inbound -> real continuation
	// child, exact original-origin carrier, count0 and Telegram-off notice.

	t.Run("S109_goal_followup_from_a_telegram_created_goal", func(t *testing.T) {
		// GREEN asserts here (spec S-109): the goal keeper's follow-up turn
		// outputs 0 with the telegram notice.
		t.Fatalf("BLOCKED: %s — required by S-109", s1HandOnSeam)
	})

	// S110_steering_an_existing_helper_from_telegram migrated intact to
	// pkg/agent::TestProgramProvenance_ActualExistingHelperSteer: actual web
	// launch, idle same helper, real Telegram parent delegate-steer, unique
	// executed-child instrument, exact count0/Telegram notice and saved join.

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
