package cron

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestI1CronBulkPersistFailureErrorCarriesJobIDs(t *testing.T) {
	for _, operation := range []string{"due-collection", "owner-migration"} {
		t.Run(operation, func(t *testing.T) {
			out := i1CronDiagnosticSink(t)
			path := filepath.Join(t.TempDir(), "jobs.json")
			cs := NewCronService(path)
			cs.SetRunner(&recordingRunner{})
			owner := "mia"
			if operation == "owner-migration" {
				owner = ""
			}
			interval := int64(60000)
			job, err := cs.AddJobFull(JobSpec{Name: "diagnostic-bulk", AgentID: owner,
				Schedule: CronSchedule{Kind: "every", EveryMS: &interval}})
			require.NoError(t, err)
			require.NoError(t, cs.startNoLoop())
			t.Cleanup(cs.Stop)
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, 0o700))
			message, count := "cron failed to persist owner migration", 1
			if operation == "due-collection" {
				cs.mu.Lock()
				cs.store.Jobs[0].State.Running = true
				cs.mu.Unlock()
				cs.RunDueJobs(time.Now().Add(2 * time.Minute))
				cs.WaitForLane()
				message, count = "cron failed to save store", 3
			} else {
				cs.SetDefaultAgentID("mia")
			}
			records := i1CronDiagnosticRecords(t, out)
			require.Len(t, records, count)
			require.Equal(t, "ERROR", records[0]["level"])
			require.Equal(t, message, records[0]["msg"])
			require.Equal(t, []any{job.ID}, records[0]["job_ids"])
			require.Contains(t, records[0]["error"], path)
			if operation == "due-collection" {
				require.Equal(t, "ERROR", records[1]["level"])
				require.Equal(t, "cron failed to persist skip-reschedule", records[1]["msg"])
				require.Equal(t, job.ID, records[1]["job_id"])
				require.Contains(t, records[1]["error"], path)
				require.Equal(t, "WARN", records[2]["level"])
				require.Equal(t, "cron job skipped: previous run still in progress", records[2]["msg"])
				require.Equal(t, job.ID, records[2]["job_id"])
				require.Equal(t, "overlap", records[2]["reason"])
			}
		})
	}
}
