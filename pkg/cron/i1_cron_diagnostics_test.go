package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The production bridge sends legacy log.Print records at INFO. Capture the
// real slog front end with the shipped WARN threshold, not a mocked logger.
func i1CronDiagnosticSink(t *testing.T) *bytes.Buffer {
	t.Helper()
	out := &bytes.Buffer{}
	previous := slog.Default()
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	level := slog.SetLogLoggerLevel(slog.LevelInfo)
	slog.SetDefault(slog.New(slog.NewJSONHandler(out, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		slog.SetLogLoggerLevel(level)
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	})
	log.Print("diagnostic INFO filter control")
	require.Empty(t, out.String(), "the sink must reject bridged INFO records")
	slog.Warn("diagnostic WARN control", "job_id", "sink-control")
	control := i1CronDiagnosticRecords(t, out)
	require.Len(t, control, 1, "prove the sink can see a warning before testing cron")
	require.Equal(t, "WARN", control[0]["level"])
	require.Equal(t, "sink-control", control[0]["job_id"])
	out.Reset()
	return out
}

func i1CronDiagnosticRecords(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &record))
		records = append(records, record)
	}
	return records
}

func TestI1CronSkipWarnCarriesJobID(t *testing.T) {
	for _, reason := range []string{"overlap", "owner-missing"} {
		t.Run(reason, func(t *testing.T) {
			out := i1CronDiagnosticSink(t)
			cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
			runner := &recordingRunner{}
			cs.SetRunner(runner)
			owner, message := "mia", "cron job skipped: no owning agent"
			if reason == "overlap" {
				owner, message = "mia", "cron job skipped: previous run still in progress"
			}
			interval := int64(60000)
			job, err := cs.AddJobFull(JobSpec{Name: "diagnostic-skip", AgentID: owner,
				Schedule: CronSchedule{Kind: "every", EveryMS: &interval}})
			require.NoError(t, err)
			if reason == "owner-missing" {
				// DEL-15 refuses an owner-less job at AddJobFull, so the guard's
				// only remaining input is a legacy/hand-edited store record —
				// model it by clearing the owner through UpdateJob.
				job.AgentID = ""
				require.NoError(t, cs.UpdateJob(job))
			}
			if reason == "overlap" {
				cs.mu.Lock()
				cs.store.Jobs[0].State.Running = true
				cs.mu.Unlock()
			}
			cs.executeJobByID(context.Background(), job.ID)
			require.Empty(t, runner.calls(), "a refused job cannot reach its runner")
			records := i1CronDiagnosticRecords(t, out)
			require.Len(t, records, 1, "a skip must remain visible at the shipped WARN threshold")
			require.Equal(t, "WARN", records[0]["level"])
			require.Equal(t, message, records[0]["msg"])
			require.Equal(t, job.ID, records[0]["job_id"])
			require.Equal(t, reason, records[0]["reason"])
		})
	}
}

func TestI1CronPersistFailureErrorCarriesJobID(t *testing.T) {
	for _, operation := range []string{"run", "skip", "running-reset", "remove", "enable"} {
		t.Run(operation, func(t *testing.T) {
			out := i1CronDiagnosticSink(t)
			path := filepath.Join(t.TempDir(), "jobs.json")
			cs := NewCronService(path)
			cs.SetRunner(&recordingRunner{})
			interval := int64(60000)
			job, err := cs.AddJobFull(JobSpec{Name: "diagnostic-write", AgentID: "mia",
				Schedule: CronSchedule{Kind: "every", EveryMS: &interval}})
			require.NoError(t, err)
			// A directory at the file target is a real atomic-write refusal on
			// every supported OS, unlike mode-bit tests run as an administrator.
			require.NoError(t, os.Remove(path))
			require.NoError(t, os.Mkdir(path, 0o700))
			wantMessages := []string{}
			switch operation {
			case "run":
				cs.executeJobByID(context.Background(), job.ID)
				wantMessages = []string{"cron failed to save store", "cron failed to save store"}
			case "skip":
				cs.mu.Lock()
				cs.rescheduleSkippedUnsafe(&cs.store.Jobs[0])
				cs.mu.Unlock()
				wantMessages = []string{"cron failed to persist skip-reschedule"}
			case "running-reset":
				cs.mu.Lock()
				cs.store.Jobs[0].State.Running = true
				cs.clearRunningUnsafe(job.ID)
				cs.mu.Unlock()
				wantMessages = []string{"cron failed to persist Running reset"}
			case "remove":
				cs.RemoveJob(job.ID)
				wantMessages = []string{"cron failed to save store after remove"}
			case "enable":
				cs.EnableJob(job.ID, false)
				wantMessages = []string{"cron failed to save store after enable"}
			}
			records := i1CronDiagnosticRecords(t, out)
			require.Len(t, records, len(wantMessages), "failed writes must remain visible at WARN threshold")
			for i, record := range records {
				require.Equal(t, "ERROR", record["level"])
				require.Equal(t, wantMessages[i], record["msg"])
				require.Equal(t, job.ID, record["job_id"])
				require.Contains(t, record["error"], path, "keep the real filesystem refusal, not just a failure label")
			}
		})
	}
}
