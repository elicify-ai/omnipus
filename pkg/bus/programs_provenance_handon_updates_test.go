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

// TestProgramProvenance_HandOn's scenarios are now mapped to their real
// producer/runtime successors below. No empty passing wrapper is retained.
// S75_telegram_to_delegated_helper migrated intact to
// pkg/agent::TestProgramProvenance_ActualDelegatedHelper: actual input ->
// delegate tool -> launcher -> durable child -> registered bash, exact
// count0/Telegram notice then count16 after the channel switch is on.
// The durable carrier is observed from the actual launch, not seeded.

// S76_telegram_to_peer_request migrated intact to
// pkg/gateway::TestProgramProvenance_ActualPeerRequest: real Dev send_message,
// real gateway admission/router/ledger and running Ops turn, exact count0
// and Telegram notice after the connected Ops count16 positive control.

// S77_telegram_to_new_board_task_ui_task_independent migrated intact to
// pkg/gateway::TestProgramProvenance_ActualBoardTasks: actual Telegram
// create_task versus the person's real REST create with the same prompt,
// actual Board start/executor/launcher/commands, exact count0/Telegram
// notice versus count16 and exact lineage/receiving origin sets.

// S78_provenance_survives_restart migrated intact, with the H49v2 lifecycle
// ruling, to pkg/agent::TestProgramProvenance_ActualStoppedHelperBoot:
// actual Telegram delegate/child16/question -> actual parent Stop -> persisted
// Telegram-off config, reopened runtime/stores and next genuine boot epoch ->
// actual boot leaves it stopped -> actual parent's delegate respond -> SAME
// helper/generation command0 and full Telegram notice/exact receiving origin.
// No needs_input/wait, seeded state/lineage, web helper input or auto-revival.

// S79_unresolved_chain_withholds migrated intact to
// pkg/agent::TestProgramProvenance_ActualUnreadableParent: actual delegate
// helper, parent-only journal corruption, readable-chain count16 control,
// actual child and exact count0/unresolved receiving-context oracle.
// D5 row13 is additionally real in TestProgramProvenance_ActualHelperHopBound:
// seventeen actual delegate edges, exact 15/16 eligible and 17 unresolved
// boundary counts. No manually constructed lineage or fake resolver.

// S80_mixed_provenance_through_a_peer_request migrated intact to
// pkg/gateway::TestProgramProvenance_ActualMixedPeerRequest: separate real
// Telegram/Discord inputs, admitted into one live Dev turn, actual peer
// tool/router/ledger/Ops command, exact two-origin set, count0 and full
// Discord-off notice with Telegram allowed. No manually joined carrier.

// S82_model_text_cannot_set_provenance migrated intact to the same actual
// pkg/gateway peer test: body says origin:webchat, but the actual ledger
// AND receiver turn carry exactly connector:telegram; real child count0.

// S111_omitted_producer_dev_command_gets_no_token_and_the_unresolved_notice
// migrated intact to pkg/agent::TestProgramProvenance_OmittedInputRuntime:
// real connected positive count16, real omitted-input child count0, exact
// unresolved notice. A source guard alone cannot substitute for that row.

func TestProgramProvenance_Updates(t *testing.T) {
	// S107_telegram_edits_a_web_created_task migrated intact to
	// pkg/gateway::TestProgramProvenance_ActualTaskPromptEdit: real web
	// create_task, actual Telegram update_task, exact prompt/origin snapshot
	// inside the real persist hook BEFORE reconciliation, actual Board run,
	// count0 and full Telegram notice. Repeated with fresh gateway-domain
	// loop/bus/stores/credential/launcher reconstruction, not just Store reopen.
	// OS re-exec, HTTP startup and UAT are outside this integration boundary.

	// S108 migrated intact to pkg/agent::TestProgramProvenance_ActualBackgroundHandoff:
	// actual Telegram-input child -> real notifier inbound -> real continuation
	// child, exact original-origin carrier, count0 and Telegram-off notice.

	// S109_goal_followup_from_a_telegram_created_goal migrated intact to
	// pkg/agent::TestProgramProvenance_ActualGoalFollowup: registered set_goal
	// in a real Telegram turn, actual persisted goal, real keeper tick/notifier
	// inbound and unchanged actual system-message consumer, real command0 and
	// full Telegram notice; exact source/stored/wake/receiving origins checked.
	// The clock edge is controlled; no goal, routing or provenance is seeded.

	// S110_steering_an_existing_helper_from_telegram migrated intact to
	// pkg/agent::TestProgramProvenance_ActualExistingHelperSteer: actual web
	// launch, idle same helper, real Telegram parent delegate-steer, unique
	// executed-child instrument, exact count0/Telegram notice and saved join.

	// S123_web_off_telegram_on_mixed_steering_both_orders migrated intact to
	// pkg/agent::TestProgramProvenance_ActualMixedHelperSteering: four actual
	// delegate/steer chains, both input orders, with/without fresh runtime
	// reconstruction before the next steer/helper turn. Exact saved/receiving
	// mixed origins, count0 and web-off notice remain strong assertions.
	// Restart reloads config, actors, credentials, sessions and lifecycle data;
	// recovery of an already queued steer or parked helper is not claimed.

	// S124_independent_joined_with_connector_and_ui_regression migrated intact
	// to pkg/gateway::TestProgramProvenance_ActualHeartbeatTask: persisted real
	// workspace heartbeat -> reconciler -> cron lane -> scheduled runner ->
	// registered create_task; real Telegram creator edit with same-write exact
	// independent+Telegram origin and actual Board/task command, web off/on.
	// Separate independently constructed setting chains, not a same-task rerun.
	// TestProgramProvenance_ActualPersonTaskAuthority additionally checks the
	// actual matching refused update outcome and byte-identical UI task record.

	// S125_person_ui_edit_preserves_provenance migrated intact to
	// pkg/gateway::TestProgramProvenance_ActualPersonTaskEdit: actual Telegram
	// create/update tool writes, real person's REST prompt PATCH with exact
	// origin at the in-lock persist boundary, real task command/count0 and
	// Telegram notice; actual delete/new UI task must be exactly independent.
	// Its real new-task child additionally checks A-1's enabled-web count16.
}
