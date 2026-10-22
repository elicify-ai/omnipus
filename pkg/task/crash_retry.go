package task

// prepareCrashRetry runs under the same task-file lock as the winning transition.
// It retires only a crash-failed standalone binding and preserves its recovery fence.
func (su *storeUpdateLocked) prepareCrashRetry() {
	if su.patch.CrashInterrupted != nil {
		su.t.CrashInterrupted = *su.patch.CrashInterrupted
	}
	if !su.patch.RetryInterrupted || su.patch.Status == nil || *su.patch.Status != StatusInProgress ||
		su.t.Status != StatusFailed || !su.t.CrashInterrupted || su.t.PlanID != "" || su.t.Trigger.IsRepeating() {
		return
	}
	if su.t.SessionID != "" {
		found := false
		for _, id := range su.t.RecoverySessionIDs {
			if id == su.t.SessionID {
				found = true
				break
			}
		}
		if !found {
			su.t.RecoverySessionIDs = append(append([]string(nil), su.t.RecoverySessionIDs...), su.t.SessionID)
		}
	}
	su.t.SessionID = ""
	su.t.Result = ""
	su.t.Artifacts = nil
	su.t.CompletedAt = ""
	su.t.CrashInterrupted = false
}
