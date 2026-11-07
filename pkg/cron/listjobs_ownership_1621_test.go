package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listJobsStoredFixture specifies independent input values, not values read from
// ListJobs. JSON loading gives the service its own allocations, so an expected
// job cannot accidentally share pointers with the service under test.
func listJobsStoredFixture() CronJob {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC).UnixMilli()
	return CronJob{
		ID: "listjobs-fixture", Name: "stored recurring job", Enabled: true,
		Schedule: CronSchedule{
			Kind: "every", EveryMS: int64Ptr(60_000), TZ: "UTC",
		},
		Payload: CronPayload{
			Kind: KindHeartbeat, Message: "original message", Command: "original-command",
		},
		State: CronJobState{
			NextRunAtMS: int64Ptr(base + 60_000), LastRunAtMS: int64Ptr(base - 60_000),
			LastStatus: "error", LastError: "original failure",
			ConsecutiveFailures: 2, Running: false, RetryAttempt: 1,
			History: []CronRunRecord{{
				RanAtMs: base - 60_000, Status: "error", Error: "original failure",
				SessionID: "original-history-session", DurationMs: 25,
			}},
		},
		CreatedAtMS: base - 120_000, UpdatedAtMS: base,
		DeleteAfterRun: false, AgentID: "mia", SessionMode: SessionModeContinue,
		RunIsolated: false, CreatedBy: "fixture-user", SessionID: "original-session",
	}
}

func newListJobsStoredService(t *testing.T, jobs []CronJob) *CronService {
	t.Helper()
	data, err := json.Marshal(CronStore{Version: 1, Jobs: jobs})
	require.NoError(t, err, "encode the independently specified store fixture")
	path := filepath.Join(t.TempDir(), "jobs.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))
	cs := NewCronService(path)
	require.NoError(t, cs.Load(), "load the real temp store without starting the timer")
	return cs
}

// Issue #1621: callers own both the returned elements and every nested mutable
// value, for either filter flag. Each mutation gets a fresh store and oracle.
func TestCronService_ListJobsReturnsCallerOwnedJobs(t *testing.T) {
	mutations := []struct {
		name    string
		prepare func(*CronJob)
		mutate  func(*testing.T, *CronJob)
	}{
		{
			name: "job scalar fields",
			mutate: func(t *testing.T, job *CronJob) {
				job.ID = "caller-owned-id"
				job.Name = "caller-owned-name"
				job.Enabled = false
				job.CreatedAtMS++
				job.UpdatedAtMS++
				job.DeleteAfterRun = true
				job.AgentID = "caller-owned-agent"
				job.SessionMode = SessionModeIsolated
				job.RunIsolated = true
				job.CreatedBy = "caller-owned-user"
				job.SessionID = "caller-owned-session"
			},
		},
		{
			name: "payload fields",
			mutate: func(t *testing.T, job *CronJob) {
				job.Payload = CronPayload{Kind: "caller-kind", Message: "caller message", Command: "caller-command"}
			},
		},
		{
			name: "schedule scalar fields",
			mutate: func(t *testing.T, job *CronJob) {
				job.Schedule.Kind = "cron"
				job.Schedule.Expr = "0 * * * *"
				job.Schedule.TZ = "Europe/Warsaw"
			},
		},
		{
			name: "state scalar fields",
			mutate: func(t *testing.T, job *CronJob) {
				job.State.LastStatus = "caller-status"
				job.State.LastError = "caller error"
				job.State.ConsecutiveFailures++
				job.State.Running = true
				job.State.RetryAttempt++
			},
		},
		{
			name: "Schedule.AtMS pointee",
			prepare: func(job *CronJob) {
				job.Schedule = CronSchedule{Kind: "at", AtMS: int64Ptr(*job.State.NextRunAtMS)}
				job.DeleteAfterRun = true
			},
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.Schedule.AtMS)
				*job.Schedule.AtMS++
			},
		},
		{
			name: "Schedule.EveryMS pointee",
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.Schedule.EveryMS)
				*job.Schedule.EveryMS++
			},
		},
		{
			name: "State.NextRunAtMS pointee",
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.State.NextRunAtMS)
				*job.State.NextRunAtMS++
			},
		},
		{
			name: "State.LastRunAtMS pointee",
			mutate: func(t *testing.T, job *CronJob) {
				require.NotNil(t, job.State.LastRunAtMS)
				*job.State.LastRunAtMS++
			},
		},
		{
			name: "State.History element",
			mutate: func(t *testing.T, job *CronJob) {
				require.Len(t, job.State.History, 1)
				job.State.History[0] = CronRunRecord{
					RanAtMs: 1, Status: "caller-status", Error: "caller error",
					SessionID: "caller-history-session", DurationMs: 1,
				}
			},
		},
	}

	for _, includeDisabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("includeDisabled=%t", includeDisabled), func(t *testing.T) {
			for _, mutation := range mutations {
				t.Run(mutation.name, func(t *testing.T) {
					want := listJobsStoredFixture()
					if mutation.prepare != nil {
						mutation.prepare(&want)
					}
					cs := newListJobsStoredService(t, []CronJob{want})
					returned := cs.ListJobs(includeDisabled)
					require.Len(t, returned, 1, "instrument: enabled fixture is present for either filter")
					mutation.mutate(t, &returned[0])

					assert.Equal(t, []CronJob{want}, cs.ListJobs(true),
						"ListJobs(%t) caller mutation %q leaked into a fresh list", includeDisabled, mutation.name)
					stored, found := cs.GetJob(want.ID)
					if assert.True(t, found, "caller mutation must not remove the original service-owned job") {
						assert.Equal(t, want, stored,
							"ListJobs(%t) caller mutation %q leaked into GetJob", includeDisabled, mutation.name)
					}
				})
			}
		})
	}
}
