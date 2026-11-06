package cron

import (
	"testing"
	"time"
)

// Issue #1621 / CHECK finding 1: enabling a job changes the stored flag, but
// editing the returned job must not perform any further service update.
func TestCronService_EnableJobReturnsCallerOwnedJob(t *testing.T) {
	assertJobReturnOwnership(t, func(t *testing.T, once bool) (*CronService, *CronJob, CronJob) {
		want := jobReturnOwnershipFixture(once)
		initial := jobReturnOwnershipFixture(once)
		initial.Enabled = false
		initial.State.NextRunAtMS = nil
		cs := newListJobsStoredService(t, []CronJob{initial})
		// The fixture expects one minute after this clock (or its explicit
		// at-time), not a timestamp copied out of EnableJob's return.
		cs.SetClock(newFakeClock(time.UnixMilli(want.UpdatedAtMS)))
		returned := cs.EnableJob(want.ID, true)
		return cs, returned, want
	})
}
