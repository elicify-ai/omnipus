package cron

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1621, CHECK finding 1: ownership must apply to accessor returns too,
// not just to ListJobs. Every mutation gets an independently loaded temp store.
func TestCronService_GetJobReturnsCallerOwnedJob(t *testing.T) {
	assertJobReturnOwnership(t, func(t *testing.T, once bool) (*CronService, *CronJob, CronJob) {
		want := jobReturnOwnershipFixture(once)
		cs := newListJobsStoredService(t, []CronJob{want})
		returned, found := cs.GetJob(want.ID)
		require.True(t, found, "instrument: the independently stored fixture exists")
		return cs, &returned, want
	})
}

func jobReturnOwnershipFixture(once bool) CronJob {
	want := listJobsStoredFixture()
	if once {
		want.Schedule = CronSchedule{Kind: "at", AtMS: int64Ptr(*want.State.NextRunAtMS)}
		want.DeleteAfterRun = true
	}
	return want
}

// The factory supplies its oracle independently of the method's return. Only
// AddJobFull's unpredictable ID may be copied out as an opaque identity token.
func assertJobReturnOwnership(t *testing.T, source func(*testing.T, bool) (*CronService, *CronJob, CronJob)) {
	t.Helper()
	for _, mutation := range jobReturnOwnershipMutations() {
		t.Run(mutation.name, func(t *testing.T) {
			cs, returned, want := source(t, mutation.once)
			require.NotNil(t, returned, "instrument: accessor returned its job")
			require.Equal(t, want, *returned, "accessor must first return the complete specified job")
			mutation.mutate(t, returned)

			assert.Equal(t, []CronJob{want}, cs.ListJobs(true),
				"caller mutation %q of the accessor return leaked into ListJobs", mutation.name)
			stored, found := cs.GetJob(want.ID)
			if assert.True(t, found, "caller mutation must not remove the service-owned job") {
				assert.Equal(t, want, stored,
					"caller mutation %q of the accessor return leaked into GetJob", mutation.name)
			}
		})
	}
}

type jobReturnOwnershipMutation struct {
	name   string
	once   bool
	mutate func(*testing.T, *CronJob)
}

func jobReturnOwnershipMutations() []jobReturnOwnershipMutation {
	return []jobReturnOwnershipMutation{
		{
			name: "job scalar fields",
			mutate: func(_ *testing.T, job *CronJob) {
				job.ID = "caller-owned-id"
				job.Name = "caller-owned-name"
				job.Enabled = false
				job.CreatedAtMS++
				job.UpdatedAtMS++
				job.DeleteAfterRun = !job.DeleteAfterRun
				job.AgentID = "caller-owned-agent"
				job.SessionMode = SessionModeIsolated
				job.RunIsolated = true
				job.CreatedBy = "caller-owned-user"
				job.SessionID = "caller-owned-session"
			},
		},
		{
			name: "payload fields",
			mutate: func(_ *testing.T, job *CronJob) {
				job.Payload = CronPayload{Kind: "caller-kind", Message: "caller message", Command: "caller-command"}
			},
		},
		{
			name: "schedule scalar fields",
			mutate: func(_ *testing.T, job *CronJob) {
				job.Schedule.Kind = "cron"
				job.Schedule.Expr = "0 * * * *"
				job.Schedule.TZ = "Europe/Warsaw"
			},
		},
		{
			name: "state scalar fields",
			mutate: func(_ *testing.T, job *CronJob) {
				job.State.LastStatus = "caller-status"
				job.State.LastError = "caller error"
				job.State.ConsecutiveFailures++
				job.State.Running = true
				job.State.RetryAttempt++
			},
		},
		{
			name: "Schedule.AtMS pointee", once: true,
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.Schedule.AtMS)
				(*job.Schedule.AtMS)++
			},
		},
		{
			name: "Schedule.EveryMS pointee",
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.Schedule.EveryMS)
				(*job.Schedule.EveryMS)++
			},
		},
		{
			name: "State.NextRunAtMS pointee",
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.State.NextRunAtMS)
				(*job.State.NextRunAtMS)++
			},
		},
		{
			name: "State.LastRunAtMS pointee",
			mutate: func(_ *testing.T, job *CronJob) {
				// An unrun AddJobFull result has no last-run pointer: adding
				// and editing one must still leave the stored value nil.
				if job.State.LastRunAtMS == nil {
					job.State.LastRunAtMS = int64Ptr(0)
				}
				(*job.State.LastRunAtMS)++
			},
		},
		{
			name: "State.History element",
			mutate: func(t *testing.T, job *CronJob) {
				// Get/Enable return a seeded element; AddJobFull can only
				// supply empty history, so append and edit caller-local data.
				if len(job.State.History) == 0 {
					job.State.History = append(job.State.History, CronRunRecord{})
				}
				require.Len(t, job.State.History, 1)
				record := &job.State.History[0]
				record.RanAtMs++
				record.Status = "caller-status"
				record.Error = "caller error"
				record.SessionID = "caller-history-session"
				record.DurationMs++
			},
		},
	}
}
