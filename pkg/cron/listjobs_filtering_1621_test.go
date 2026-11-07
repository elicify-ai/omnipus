package cron

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Issue #1621 / T3: copying must preserve every stored field and select all jobs
// for true, and exactly the enabled jobs for false. Ordering is not specified.
func TestCronService_ListJobsPreservesStoredFieldsAndFiltering(t *testing.T) {
	enabledRecurring := listJobsStoredFixture()
	enabledRecurring.ID = "enabled-recurring"

	// Distinct one-time values exercise AtMS, nil EveryMS/last-run/history,
	// DeleteAfterRun=true and a different payload/owner/session combination.
	enabledOnce := CronJob{
		ID: "enabled-once", Name: "stored one-time job", Enabled: true,
		Schedule: CronSchedule{Kind: "at", AtMS: int64Ptr(2_000_000_000_000)},
		Payload:  CronPayload{Kind: KindHeartbeat, Message: "one-time message"},
		State: CronJobState{
			NextRunAtMS: int64Ptr(2_000_000_000_000), Running: true,
		},
		CreatedAtMS: 1_800_000_000_000, UpdatedAtMS: 1_800_000_000_001,
		DeleteAfterRun: true, AgentID: "jim", SessionMode: SessionModeMain,
		CreatedBy: "once-user", SessionID: "once-session",
	}

	disabledRecurring := listJobsStoredFixture()
	disabledRecurring.ID = "disabled-recurring"
	disabledRecurring.Name = "disabled recurring job"
	disabledRecurring.Enabled = false
	disabledRecurring.State.NextRunAtMS = nil
	disabledRecurring.AgentID = "general-purpose"
	disabledRecurring.SessionMode = SessionModeIsolated
	disabledRecurring.RunIsolated = true
	disabledRecurring.SessionID = ""

	disabledCron := CronJob{
		ID: "disabled-cron", Name: "disabled cron job", Enabled: false,
		Schedule: CronSchedule{Kind: "cron", Expr: "15 9 * * 1", TZ: "Europe/Warsaw"},
		Payload:  CronPayload{Kind: KindHeartbeat, Message: "disabled message", Command: "disabled-command"},
		State: CronJobState{
			LastRunAtMS: int64Ptr(1_700_000_000_000), LastStatus: "ok",
		},
		CreatedAtMS: 1_600_000_000_000, UpdatedAtMS: 1_700_000_000_000,
		AgentID: "mia", SessionMode: SessionModeContinue,
		CreatedBy: "cron-user", SessionID: "cron-session",
	}

	cases := []struct {
		name        string
		stored      []CronJob
		wantEnabled []CronJob
	}{
		{name: "nil store jobs"},
		{name: "empty store jobs", stored: []CronJob{}},
		{name: "enabled only", stored: []CronJob{enabledOnce}, wantEnabled: []CronJob{enabledOnce}},
		{name: "disabled only", stored: []CronJob{disabledCron}},
		{
			name:        "mixed enabled and disabled",
			stored:      []CronJob{disabledRecurring, enabledRecurring, enabledOnce, disabledCron},
			wantEnabled: []CronJob{enabledRecurring, enabledOnce},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := newListJobsStoredService(t, tc.stored)
			for _, includeDisabled := range []bool{true, false} {
				t.Run(fmt.Sprintf("includeDisabled=%t", includeDisabled), func(t *testing.T) {
					want := tc.wantEnabled
					if includeDisabled {
						want = tc.stored
					}
					assert.ElementsMatch(t, want, cs.ListJobs(includeDisabled),
						"ListJobs(%t) must preserve complete stored values and exact membership", includeDisabled)
				})
			}
		})
	}
}
