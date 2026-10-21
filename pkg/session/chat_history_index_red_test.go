package session

import (
	"fmt"
	"testing"
)

// RED blockers are intentional executable failures, not skips or fake test-side
// implementations. D1/D1a/D4b specify architecture but no Go callable signatures.
// The paired gateway tests exercise real existing entry points meanwhile. GREEN
// must replace each blocker with its real seam-driven assertions; a new API's
// mere existence must never turn these tests green.
// Oracle: chat-history-paging-spec.md D1/D1a, D-H1/D-H2 and B21/B22/B31/B40/B41.
func TestChatHistoryIndex_HealthyCutAndBoundedReads(t *testing.T) {
	for _, prefix := range []int{1000, 100000} {
		for _, limit := range []int{40, 100} {
			t.Run(fmt.Sprintf("prefix%d_limit%d", prefix, limit), func(t *testing.T) {
				t.Fatalf("BLOCKED: immutable indexed reader and addressed-record/disposition read observer not implemented — required by D1/D1a, T06, B21/B22/B31/B40; fixture has %d omitted prefix groups and identical200-group suffix; expected exactly%d selected groups plus at most one lookahead, zero omitted-prefix content reads, allocations/work within10%%, no full disposition scan", prefix, limit)
			})
		}
	}
	for _, pending := range []int{0, 1, 63, 64, 65} {
		t.Run(fmt.Sprintf("pending%d", pending), func(t *testing.T) {
			if pending > 64 {
				t.Fatal("BLOCKED: bounded suffix reconciler and publication/read counters not implemented — required by D1a/T06/B40;65 pending facts must return fixed history_unavailable with zero candidate-root publications on repeated Retry, reading at most anchor+65 overflow probe")
			}
			t.Fatalf("BLOCKED: bounded suffix reconciler and publication/read counters not implemented — required by D1a/T06/B40; pending=%d must heal once from valid saved prefix only;0 is byte/mtime no-op; private mutations advance coverage without rows", pending)
		})
	}
	t.Run("missing_baseline_is_not_empty", func(t *testing.T) {
		t.Fatal("BLOCKED: missing-baseline initializer/refusal seam not implemented — required by D1/D1a/T06/B31; new sends remain canonical, never seed suffix-only ready history, never scan lifetime")
	})
}

func TestChatHistoryRecovery_PublicationCrashBoundAndIdempotence(t *testing.T) {
	stages := []string{"canonical_fsync", "derived_row_fsync", "lookup_staging", "checkpoint_publication"}
	for _, stage := range stages {
		for _, pending := range []int{0, 1, 63, 64, 65} {
			t.Run(fmt.Sprintf("%s_pending%d", stage, pending), func(t *testing.T) {
				t.Fatalf("BLOCKED: shared chat-index reducer/reconciler and deterministic publication fault barrier not implemented — required by D1a/T29/D-H1; stage=%s pending=%d; expect <=64 unique occurrences applied once,65 refuses without any candidate ready root, duplicate durable row charged once, no token renumber/canonical trimming/prefix scan", stage, pending)
			})
		}
	}
	for _, shape := range []string{
		"32_archive_plus32_missing_rolled_is64", "33_archive_plus32_missing_rolled_is65",
		"archive_plus_disposition_plus_private_model_ref_combined_bound", "already_durable_derived_row_counts_once",
		"torn_unpublished_derived_tail_redone", "complete_bad_derived_row_refused",
		"canonical_torn_record_never_truncated", "changed_saved_anchor_refused", "truncated_saved_prefix_refused",
		"current_partition_missing_is_inconsistent", "repointed_partition_id_refused",
		"concurrent_recoverers_one_owner", "append_after_captured_end_belongs_to_next_cut",
		"missing_or_incompatible_checkpoint_never_seeded", "canonical_save_success_survives_derived_publication_error",
	} {
		t.Run(shape, func(t *testing.T) {
			t.Fatalf("BLOCKED: shared recovery engine/read and root-publication observation seams not implemented — required by D1a/T29/D-H1/D-H2/D-N1: %s; no partial ready root, no repeated64-batch lifetime walk; actual canonical/derived bytes and exact counters are required, not a mock reducer", shape)
		})
	}
}

func TestChatHistoryRetention_ActualRetainedContinuation(t *testing.T) {
	for _, scenario := range []string{
		"first_expired_exact_full_terminal_has_no_before", "middle_expired_neighbors_keep_order_and_positions",
		"last_rolled_expired_current_retained_truthful_after", "effect_retained_target_proven_expired_no_orphan",
		"retained_target_unreadable_effect_is_storage_error", "sweep_after_cut_reselects_generation",
		"retention_disabled_generation_unchanged", "token_rank_only_tombstones_rich_fragment_retired",
		"known_expired_before_after_and_closed_range_remain_fences", "unknown_foreign_fence_still404",
		"unlink_crash_then_next_removed_count_zero_reconciles_once", "64_absent_rolled_facts_heal_once",
		"65_absent_facts_refuse_without_candidate_publish", "permission_failure_not_expiry_and_partial_success_truthful",
	} {
		t.Run(scenario, func(t *testing.T) {
			t.Fatalf("BLOCKED: chat retained-set/tombstone ledger, indexed bounded reader and delete-publication fault seam not implemented — required by D2/D14/T19/B27/B41/B50, D-X1..X6/D-H2: %s; must exercise actual files/sweep and assert exact retained IDs/flags/generation plus token+rank-only disk metadata", scenario)
		})
	}
}

func TestChatHistoryReadCut_SlowReadDoesNotHoldAppendLocks(t *testing.T) {
	for _, writer := range []string{"chat", "settle_effect", "disposition", "rollover"} {
		t.Run(writer, func(t *testing.T) {
			t.Fatalf("BLOCKED: immutable bounded-record content-read barrier and captured-cut reader not implemented — required by D1/D14/T26/B32/B50/D-L1; while real content I/O is blocked after cut capture, actual %s append must complete before release, returned fields/checked addresses must remain from earlier cut, and each record handle must close; no sleep/elapsed-time proxy", writer)
		})
	}
}
