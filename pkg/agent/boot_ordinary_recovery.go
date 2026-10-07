package agent

import (
	"errors"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// A protected or superseded snapshot must not append a no-op lifecycle line.
// Mutate aborts persistence on this private signal; the boot writer treats it
// as successful preservation, not as a recovery refusal.
var errRestartStopUnchanged = errors.New("boot: restart stop no longer needed")

// admittedStandingRoot separates an idle, usable conversation from a saved
// execution that actually entered admission. A prior-boot needs_input belongs
// here too: its approval/question died with the process (#1217), so recovery
// clears it through the same restart Stop rather than re-asking. The plan sweep
// still owns task/plan roots.
func admittedStandingRoot(rec *session.LifecycleRecord) bool {
	return standingOrdinaryRoot(rec) && rec.ExecutionID != nil &&
		(rec.State == session.LifecycleRunning || rec.State == session.LifecycleQueued || rec.State == session.LifecycleNeedsInput)
}

func standingOrdinaryRoot(rec *session.LifecycleRecord) bool {
	return session.LifecycleRecordIsStandingRoot(rec)
}

// A restart stop ends the old execution, not the standing trigger. Normal
// scheduled/heartbeat entries may reuse the existing same-generation revival;
// a human Stop, timeout or steered helper never grants that automatic revival.
func restartStoppedStandingRoot(rec *session.LifecycleRecord) bool {
	return standingOrdinaryRoot(rec) && rec.State == session.LifecycleStopped &&
		rec.StopNote != nil && rec.StopNote.Cause == session.StopCauseRestart
}

// recoverOrdinaryRoot never dispatches a turn. It settles only a standing
// root's prior-boot execution through the existing ledger-backed restart Stop.
// The writer rechecks generation, run, boot and accepted Stop under the lock;
// terminal completions and idle roots do not reach the writer at all.
func (r *SteerBootRecovery) recoverOrdinaryRoot(id string, notice func(string, string)) error {
	rec, err := r.Lifecycle.Load(id)
	if errors.Is(err, session.ErrLifecycleNotFound) {
		return nil // A conversation that never admitted work has no record.
	}
	if err != nil {
		err = fmt.Errorf("session %s ordinary-root recovery load failed: %w", id, err)
		notice("load:"+id, err.Error())
		return err
	}
	if !admittedStandingRoot(rec) {
		return nil
	}
	if r.BootEpoch != nil && r.BootEpoch.Current() != 0 && rec.ExecutionID.BootSeq >= r.BootEpoch.Current() {
		return nil // Same-boot and future-boot work is not this restart's victim.
	}
	if err := r.failInterrupted(rec); err != nil {
		notice("interrupted-write:"+id, fmt.Sprintf("session %s restart stop refused at boot: %v", id, err))
		return err
	}
	return nil
}

// ordinaryRestartStopAllowedLocked validates the selected root execution in
// the writer's lock hold. No new run, generation or human Stop may be replaced
// by the scan's old snapshot. recoverOrdinaryRoot already selects a prior-boot
// snapshot, and full ExecutionID equality below rechecks its boot under this
// lock; a second old-boot comparison here would be redundant. The existing
// steered recovery remains separate.
func (r *SteerBootRecovery) ordinaryRestartStopAllowedLocked(selected, current *session.LifecycleRecord) (bool, error) {
	if !admittedStandingRoot(current) {
		bootRestartStopDebug(current, "ordinary_ineligible_state_or_owner")
		return false, nil
	}
	if selected.ExecutionID == nil || current.Generation != selected.Generation ||
		*current.ExecutionID != *selected.ExecutionID {
		bootRestartStopDebug(current, "ordinary_execution_changed")
		return false, nil
	}
	intents, err := r.Lifecycle.UnfinishedStopIntentsLocked(current.SessionID)
	if err != nil {
		bootRestartStopDebug(current, "ordinary_controls_unreadable")
		return false, fmt.Errorf("steer: boot: accepted Stop controls for %q unreadable: %w", current.SessionID, err)
	}
	if acceptedStopSelectsExecution(current, intents) {
		bootRestartStopDebug(current, "ordinary_accepted_stop")
		return false, nil
	}
	return true, nil
}

func bootRestartStopDebug(rec *session.LifecycleRecord, arm string) {
	logger.DebugCF("agent", "boot: restart stop skipped or refused", map[string]any{
		"session_id": rec.SessionID, "generation": rec.Generation, "arm": arm,
	})
}

// A matching accepted human Stop owns the landing even if its fence never
// reached the lifecycle record. The caller supplies a ledger snapshot read
// under the same record lock when this check protects a restart write.
func acceptedStopSelectsExecution(rec *session.LifecycleRecord, intents []session.UnfinishedStopIntent) bool {
	for _, intent := range intents {
		target := intent.Selection.Effect.Target
		if target.Generation != rec.Generation {
			continue
		}
		if !target.Selected() {
			if rec.ExecutionID == nil {
				return true
			}
			continue
		}
		if rec.ExecutionID != nil && rec.ExecutionID.RunID == target.RunID && rec.ExecutionID.BootSeq == target.BootSeq {
			return true
		}
	}
	return false
}
