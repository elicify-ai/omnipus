package agent

// Commissioning failures, NOT behavioral RED. r17 requires deterministic
// schedules at these exact ordering boundaries and a persisted delivery marker.
// These fixtures are not bound in this pack. This is NOT proof that every
// usable process-edge seam is absent: real filesystem replacement failures can
// exercise cron saves, and existing re-arm/SpawnReset seams remain available.
// Do not invent a hook or move a pause under a lock that a concurrent writer
// needs. The single T8 test author owns the remaining fixture reconciliation.
import "testing"

func TestTaskTrigger_ReconciliationOrderingAndPartialFailure(t *testing.T) {
	for _, row := range []struct{ id, prerequisite string }{
		{"1.14", "prompt/trigger shared mutation plus a goal-write fault edge and scheduler observation"},
		{"1.15", "scheduler reconciliation failure at the real cron-save boundary after live append"},
		{"18.21", "in-lock persistence-to-reconciliation barrier and established queued writer B"},
		{"18.22", "in-lock trigger-change reconciliation barrier followed by queued manual-clear writer B"},
		{"18.25", "completion notification barrier before acquiring the per-task ordering boundary"},
		{"18.26", "recovery snapshot barrier before acquiring the per-task ordering boundary"},
	} {
		t.Run(row.id, func(t *testing.T) {
			t.Fatalf("BLOCKED: deterministic fixture not bound for %s — required by r17 §13.1 test 12e / %s; no scheduling or side-effect claim is verified; absence of every usable process-edge seam is not established", row.prerequisite, row.id)
		})
	}
}

func TestTaskTrigger_DeliveryAuthorityAndPendingBoundaryPrerequisites(t *testing.T) {
	for _, row := range []struct{ id, prerequisite string }{
		{"18.24", "promotion snapshot-to-write barrier with the plan already running"},
		{"18.31", "delivery-marker persistence and pre-marker ordering barrier"},
		{"18.32", "delivery-marker persistence with real queued-lane ownership"},
		{"18.33a", "atomic marker invalidation on clear and reattach in the shared mutation write"},
		{"18.33b1", "atomic marker invalidation on replacement after settled delivery"},
		{"18.33c", "identical re-save preserves the persisted delivered-but-held marker"},
		{"18.34-single", "after-collection-unlock barrier in the already-published reservation queued span, before dispatch entry; no dispatch-local second publication"},
		{"18.34-batch", "barrier between two collected jobs plus original-ID reservation settlement observation"},
		{"18.34-cancel-panic-run-now", "full plan/drain integration of the original-ID accounting; TestT8CronDispatch_OriginalIDSettlement covers only the real cron lane"},
		{"18.35a", "pre-marker-lock callback barrier plus fresh-trigger and registration-identity authorization"},
		{"18.35b", "pre-marker-lock barrier while same-content clear/reattach revokes the old registration"},
		{"18.36", "persisted delivery marker to assert queued occurrence settlement after edits and sweeps"},
		{"18.40a-c", "collection-save failure and queued dispatch observation through the production reload boundary"},
	} {
		t.Run(row.id, func(t *testing.T) {
			t.Fatalf("BLOCKED: deterministic fixture not bound for %s — required by r17 TK-30 / §13.1 test 12d or 12f / %s; this is a commissioning receipt, not behavioral coverage or proof that every required seam is absent", row.prerequisite, row.id)
		})
	}
}
