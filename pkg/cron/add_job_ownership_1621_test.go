package cron

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Issue #1621 / CHECK finding 1: the newly returned job is caller-owned, even
// when its schedule and state point to the same timestamp inside the service.
func TestCronService_AddJobFullReturnsCallerOwnedJob(t *testing.T) {
	assertJobReturnOwnership(t, func(t *testing.T, once bool) (*CronService, *CronJob, CronJob) {
		want := jobReturnOwnershipFixture(once)
		want.ID = "" // The generated identity is not an oracle for mutable fields.
		want.CreatedAtMS = want.UpdatedAtMS
		// A new, unrun job has no outcome or history; its next fire is the
		// specified one-minute interval/at-time after the fixed fixture clock.
		want.State = CronJobState{NextRunAtMS: int64Ptr(want.UpdatedAtMS + 60_000)}
		spec := JobSpec{
			Name: want.Name, Message: want.Payload.Message, Command: want.Payload.Command,
			AgentID: want.AgentID, SessionMode: want.SessionMode, RunIsolated: want.RunIsolated,
			CreatedBy: want.CreatedBy, SessionID: want.SessionID,
			Schedule: CronSchedule{Kind: want.Schedule.Kind, Expr: want.Schedule.Expr, TZ: want.Schedule.TZ},
		}
		// Input and oracle MUST NOT share pointees: storing the caller's input
		// pointers is a separate contract. If the input aliased want, an escaped
		// return pointer could corrupt both store and oracle and mask the bug.
		if want.Schedule.AtMS != nil {
			spec.Schedule.AtMS = int64Ptr(*want.Schedule.AtMS)
		}
		if want.Schedule.EveryMS != nil {
			spec.Schedule.EveryMS = int64Ptr(*want.Schedule.EveryMS)
		}
		cs := newListJobsStoredService(t, nil)
		cs.SetClock(newFakeClock(time.UnixMilli(want.UpdatedAtMS)))
		returned, err := cs.AddJobFull(spec)
		require.NoError(t, err)
		require.NotNil(t, returned)
		require.NotEmpty(t, returned.ID, "instrument: new job has an opaque generated identity")
		want.ID = returned.ID
		return cs, returned, want
	})
}
