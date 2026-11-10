package cron

import "fmt"

// RemoveJobs atomically removes the named jobs. Unlike the single-job legacy
// RemoveJob API, a persistence failure is returned to the caller and rolls back
// the in-memory change. Boot reconciliation must not start a runner after an
// unpersisted duplicate cleanup, or claim that jobs were removed when they were
// not. Unknown IDs and an empty set are no-ops.
func (cs *CronService) RemoveJobs(jobIDs []string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	ids := make(map[string]bool, len(jobIDs))
	for _, id := range jobIDs {
		ids[id] = true
	}
	before := cs.store.Jobs
	kept := make([]CronJob, 0, len(before))
	for _, job := range before {
		if !ids[job.ID] {
			kept = append(kept, job)
		}
	}
	if len(kept) == len(before) {
		return nil
	}
	cs.store.Jobs = kept
	if err := cs.saveStoreUnsafe(); err != nil {
		cs.store.Jobs = before
		return fmt.Errorf("cron: remove jobs: %w", err)
	}
	cs.notify()
	return nil
}
