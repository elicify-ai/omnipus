package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// stampG5ExitedExecution migrates an already-exited-turn fixture, not a live
// execution. These tests require no registered turn (completion-write liveness,
// a deferred goal gate, or competing post-turn tails), so Dispatch would change
// their preconditions. Use the same concrete-store stamper Dispatch uses instead.
//
// The sub-agent control-plane ADR D2, Execution identity and effect-boundary
// checks: "Persist it under the lifecycle lock before admission, and copy it
// unchanged into the admission entry and live execution handle." A running
// record persisted by hand without this stamp has no producing run to claim.
func stampG5ExitedExecution(t *testing.T, al *AgentLoop, rec *session.LifecycleRecord) *session.LifecycleRecord {
	t.Helper()
	if al.bootEpochFor() == 0 {
		mintGenuineBootEpochForLoop(t, al)
	}
	store := al.GetSessionLifecycleStore()
	if rec.ExecutionID == nil {
		if err := stampAdmissionExecution(store, rec.SessionID, rec.Generation, freshRunID(), al.bootEpochFor(), nil); err != nil {
			t.Fatalf("SETUP stamp exited producing execution: %v", err)
		}
	}
	got, err := store.Load(rec.SessionID)
	if err != nil {
		t.Fatalf("SETUP reload producing execution: %v", err)
	}
	if got.ExecutionID == nil || got.ExecutionID.RunID == "" || got.ExecutionID.BootSeq != al.bootEpochFor() {
		t.Fatalf("SETUP persisted producing identity = %+v, require fresh run in genuine boot %d", got.ExecutionID, al.bootEpochFor())
	}
	if got.Generation != rec.Generation || got.State != rec.State {
		t.Fatalf("SETUP identity stamp changed generation/state: %d/%s -> %d/%s", rec.Generation, rec.State, got.Generation, got.State)
	}
	return got
}

// takeG5StoppedNotice supplies the real consumer fact omitted by the old
// fixtures. ADR-20261004 locked decision 1: "A stop notice rings until the
// parent takes it"; C3 retains the durable inbox ack as that taken boundary.
// The existing exactly-one-wake assertions remain valid AFTER this take.
func takeG5StoppedNotice(t *testing.T, al *AgentLoop, parentID, noticeID string) {
	t.Helper()
	if err := al.GetMessageInboxStore().Ack(parentID, []string{noticeID}); err != nil {
		t.Fatalf("take stopped-child notice: %v", err)
	}
	taken, err := stopNoticeTaken(al.GetMessageInboxStore(), parentID, noticeID)
	if err != nil || !taken {
		t.Fatalf("durable stopped-child notice taken = %v, error = %v, want true/nil", taken, err)
	}
}
