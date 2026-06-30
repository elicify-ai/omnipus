// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the JobSpec.SessionID field added for heartbeat session linking
// (FR-007b, A2/F-02).
//
// Traces to: pkg/cron/service.go — AddJobFull threads SessionID from JobSpec
// onto the created CronJob so the runner can continue the pre-created eager
// session rather than minting a fresh session each run.

package cron

import (
	"os"
	"testing"
)

// TestJobSpec_SessionID_RoundTrips verifies that a SessionID set in JobSpec is
// persisted onto the CronJob returned by AddJobFull and survives a GetJob
// round-trip (FR-007b).
func TestJobSpec_SessionID_RoundTrips(t *testing.T) {
	tmpFile := t.TempDir() + "/cron_jobspec_test.json"
	defer os.Remove(tmpFile)

	cs := NewCronService(tmpFile)

	const wantSessionID = "sess-heartbeat-123"

	everyMS := int64(300000) // 5 min
	spec := JobSpec{
		Name: "heartbeat:ws-alpha:mia",
		Schedule: CronSchedule{
			Kind:    "every",
			EveryMS: &everyMS,
		},
		Message:     "run heartbeat",
		AgentID:     "mia",
		SessionMode: SessionModeContinue,
		SessionID:   wantSessionID,
	}

	job, err := cs.AddJobFull(spec)
	if err != nil {
		t.Fatalf("AddJobFull failed: %v", err)
	}
	if job.SessionID != wantSessionID {
		t.Fatalf("expected SessionID=%q on returned job, got %q", wantSessionID, job.SessionID)
	}

	// Confirm it survives a GetJob round-trip (i.e., is persisted to disk).
	got, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found via GetJob after AddJobFull")
	}
	if got.SessionID != wantSessionID {
		t.Fatalf("expected persisted SessionID=%q, got %q", wantSessionID, got.SessionID)
	}
}

// TestJobSpec_SessionID_EmptyOK verifies that a JobSpec with no SessionID
// produces a job with an empty SessionID (no default injected).
func TestJobSpec_SessionID_EmptyOK(t *testing.T) {
	tmpFile := t.TempDir() + "/cron_jobspec_empty_test.json"
	defer os.Remove(tmpFile)

	cs := NewCronService(tmpFile)

	everyMS := int64(300000)
	spec := JobSpec{
		Name:        "normal-job",
		Schedule:    CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:     "tick",
		AgentID:     "mia",
		SessionMode: SessionModeIsolated,
	}

	job, err := cs.AddJobFull(spec)
	if err != nil {
		t.Fatalf("AddJobFull failed: %v", err)
	}
	if job.SessionID != "" {
		t.Fatalf("expected empty SessionID for isolated job, got %q", job.SessionID)
	}

	got, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found via GetJob")
	}
	if got.SessionID != "" {
		t.Fatalf("expected empty persisted SessionID, got %q", got.SessionID)
	}
}
