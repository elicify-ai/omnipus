package agent

// task_trigger_test_hooks.go — test-only barrier seams on the trigger
// scheduler's producers, for the ordering fixtures of tool-parity spec r17
// §13.1 tests 12d/12e (scenarios 18.25, 18.26, 18.31, 18.35). Each runs
// before the producer takes the task's coordination lock
// (task.LockTaskWriteOrder), so a writer the test lets through first is never
// blocked by the barrier. All nil in production; the same style as
// SetRearmTestHook.

// triggerTestHooks holds the installed hooks. Guarded by TaskTriggerScheduler.mu.
type triggerTestHooks struct {
	// upsertNotify runs in OnTaskUpserted — the completion/create
	// notification with its supplied snapshot — before the coordination
	// lock (18.25).
	upsertNotify func(taskID string)
	// sweepRearm runs in the recovery sweep's re-arm of one task, after the
	// sweep's list snapshot and before the coordination lock (18.26).
	sweepRearm func(taskID string)
	// preMarkerLock runs in a scheduled fire before the coordination lock
	// under which the job's registration identity and trigger are validated
	// and the delivery marker is written (18.31, 18.35).
	preMarkerLock func(taskID, jobID string)
}

// SetUpsertNotifyTestHook installs fn as the barrier before OnTaskUpserted
// takes the task's coordination lock. Pass nil to clear it.
func (s *TaskTriggerScheduler) SetUpsertNotifyTestHook(fn func(taskID string)) {
	s.mu.Lock()
	s.testHooks.upsertNotify = fn
	s.mu.Unlock()
}

// SetSweepRearmTestHook installs fn as the barrier between the recovery
// sweep's snapshot and its per-task coordination lock. Pass nil to clear it.
func (s *TaskTriggerScheduler) SetSweepRearmTestHook(fn func(taskID string)) {
	s.mu.Lock()
	s.testHooks.sweepRearm = fn
	s.mu.Unlock()
}

// SetPreMarkerLockTestHook installs fn as the barrier before a scheduled
// fire takes the coordination lock that guards the delivery-marker write.
// Pass nil to clear it.
func (s *TaskTriggerScheduler) SetPreMarkerLockTestHook(fn func(taskID, jobID string)) {
	s.mu.Lock()
	s.testHooks.preMarkerLock = fn
	s.mu.Unlock()
}

func (s *TaskTriggerScheduler) currentTestHooks() triggerTestHooks {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.testHooks
}
