package cron

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLegacyJobTimeoutSecondsIsIgnored pins issue #1306 for scheduled work:
// a jobs.json written by an older release still carries a per-schedule
// "timeoutSeconds". It must load, the scheduled run must receive a context
// with NO deadline (the old value is ignored, never enforced), and the next
// save must drop the retired key.
func TestLegacyJobTimeoutSecondsIsIgnored(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	seed, path := newAutonomyService(t, clk)
	job := addDueJob(t, seed, "mia")

	// Inject the retired key exactly as an older release persisted it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read jobs.json: %v", err)
	}
	var doc map[string]any
	if uerr := json.Unmarshal(raw, &doc); uerr != nil {
		t.Fatalf("parse jobs.json: %v", uerr)
	}
	jobs, _ := doc["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("SETUP: jobs.json has %d jobs, want 1: %s", len(jobs), raw)
	}
	first, ok := jobs[0].(map[string]any)
	if !ok {
		t.Fatalf("SETUP: job entry is not an object: %s", raw)
	}
	first["timeoutSeconds"] = 1
	legacy, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal legacy jobs.json: %v", err)
	}
	if werr := os.WriteFile(path, legacy, 0o600); werr != nil {
		t.Fatalf("write legacy jobs.json: %v", werr)
	}

	cs := NewCronService(path)
	cs.SetClock(clk)
	var sawDeadline *bool
	cs.SetRunner(&recordingRunner{ctxHook: func(ctx context.Context, _ *CronJob) (string, error) {
		_, has := ctx.Deadline()
		sawDeadline = &has
		return "sess-legacy", nil
	}})
	if serr := cs.startNoLoop(); serr != nil {
		t.Fatalf("Start: %v", serr)
	}
	defer cs.Stop()
	clk.Advance(2 * time.Minute)

	status, _, runErr := cs.RunNow(job.ID)
	if runErr != nil || status != "ok" {
		t.Fatalf("RunNow(legacy job) = %q, %v; want ok", status, runErr)
	}
	if sawDeadline == nil {
		t.Fatal("the runner was never invoked")
	}
	if *sawDeadline {
		t.Fatal("a legacy job's timeoutSeconds put a deadline on the run; it must be ignored (issue #1306)")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read jobs.json: %v", err)
	}
	if strings.Contains(string(after), "timeoutSeconds") {
		t.Fatalf("jobs.json still carries the retired timeoutSeconds after a save: %s", after)
	}
}
