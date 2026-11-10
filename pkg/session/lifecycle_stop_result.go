package session

import "fmt"

// RecordStopControlResult refines only the ORIGINAL accepted ledger identity.
// It never stamps a lifecycle record, allocates a new control, or borrows a
// replacement's execution. Required effects must be durable before applied.
func (s *LifecycleStore) RecordStopControlResult(selected StopSelection, effectsDurable bool, pendingReason string) error {
	if err := validateLifecycleSessionID(selected.SessionID); err != nil {
		return err
	}
	mu := s.Lock(selected.SessionID)
	mu.Lock()
	defer mu.Unlock()
	lines, err := s.readControlLedgerLocked(selected.SessionID)
	if err != nil {
		return err
	}
	var original *controlLedgerLine
	for i := range lines {
		line := &lines[i]
		if line.ControlID == selected.Effect.ControlID {
			if line.Verb != controlVerbStop || line.StopEffect == nil || *line.StopEffect != selected.Effect {
				return fmt.Errorf("session: Stop result does not match the accepted control identity")
			}
			original = line
		}
	}
	if original == nil {
		return fmt.Errorf("session: Stop result has no matching accepted control")
	}
	if original.State == "applied" || original.State == "superseded" {
		return nil
	}
	rec, err := s.loadLocked(selected.SessionID)
	if err != nil {
		return err
	}
	current := stopResultOwnsRecord(selected, rec)
	state, reason := controlStateQueued, pendingReason
	switch {
	case effectsDurable && original.LandedStop != nil:
		state, reason = "applied", ""
	case !current:
		state, reason = "superseded", "a later execution or control owns the session"
	}
	if original.State == state && original.Reason == reason {
		return nil
	}
	result := *original
	result.State, result.Reason = state, reason
	return appendControlLineLocked(s, selected.SessionID, result)
}

func stopResultOwnsRecord(selected StopSelection, rec *LifecycleRecord) bool {
	if rec == nil || rec.Generation != selected.Effect.Target.Generation || rec.StopEffect == nil || *rec.StopEffect != selected.Effect {
		return false
	}
	target := selected.Effect.Target
	if !target.Selected() {
		return rec.ExecutionID == nil && target.BootSeq == 0
	}
	return rec.ExecutionID != nil && rec.ExecutionID.RunID == target.RunID && rec.ExecutionID.BootSeq == target.BootSeq
}
