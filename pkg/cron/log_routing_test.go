package cron

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CodeQL triage (class A): pkg/cron must log through the project logger (slog,
// bridged to pkg/logger), never the stdlib log package, so a schedule field with
// CR/ESC in it reaches the central control-byte escaping. ORACLE: nothing is
// written to the stdlib logger while a job (with a hostile name) runs, and the
// job's name is carried as a structured field of an slog record.
func TestCronLogsThroughSlogNotTheStdlibLogger(t *testing.T) {
	var std bytes.Buffer
	prevWriter, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&std)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(prevWriter); log.SetFlags(prevFlags) })

	var structured bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&structured, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	// slog.SetDefault also redirects the stdlib logger into the handler; undo
	// that so the assertion below measures calls to the log package itself.
	log.SetOutput(&std)

	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
	cs.SetRunner(&recordingRunner{})
	interval := int64(60000)
	hostile := "evil\r12:00:00 ERR forged\x1b[31m"
	job, err := cs.AddJobFull(JobSpec{Name: hostile, AgentID: "mia",
		Schedule: CronSchedule{Kind: "every", EveryMS: &interval}})
	require.NoError(t, err)

	cs.executeJobByID(context.Background(), job.ID)

	assert.Empty(t, std.String(), "pkg/cron must not write through the stdlib log package")
	assert.Contains(t, structured.String(), `"job":"evil\r12:00:00 ERR forged\u001b[31m"`,
		"the job name is a structured slog field (JSON-escaped)")
}
