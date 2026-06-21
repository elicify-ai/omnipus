//go:build !cgo

// O6 — heartbeat-as-schedule reconciler tests. Proves:
//  1. A Main agent with heartbeat enabled gets a recurring cron job.
//  2. A worker is never given a heartbeat job (even if the flag is set).
//  3. Disabling an agent's heartbeat removes its job.
//  4. Changing the interval updates the existing job (no duplicate).
//  5. Reconcile is idempotent (a second run makes no changes).
//  6. User (non-heartbeat) schedules are never touched.

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
)

func newReconcileCron(t *testing.T) *cron.CronService {
	t.Helper()
	return cron.NewCronService(filepath.Join(t.TempDir(), "cron.json"))
}

func boolPtrHB(b bool) *bool { return &b }

func heartbeatJobsFor(cs *cron.CronService) []cron.CronJob {
	var out []cron.CronJob
	for _, j := range cs.ListJobs(true) {
		if isHeartbeatJob(j) {
			out = append(out, j)
		}
	}
	return out
}

func TestReconcileHeartbeat_CreatesForEnabledMain(t *testing.T) {
	cs := newReconcileCron(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 15},
				{ID: "jim", Type: config.AgentTypeCore}, // heartbeat unset → no job
			},
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))

	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1, "exactly one heartbeat job (mia)")
	j := jobs[0]
	assert.Equal(t, heartbeatJobName("mia"), j.Name)
	assert.Equal(t, "mia", j.AgentID)
	assert.Equal(t, heartbeatJobKind, j.Payload.Kind)
	assert.True(t, j.Enabled)
	require.NotNil(t, j.Schedule.EveryMS)
	assert.Equal(t, int64(15)*60_000, *j.Schedule.EveryMS)
	assert.Equal(t, cron.SessionModeContinue, j.SessionMode)
}

func TestReconcileHeartbeat_WorkerNeverScheduled(t *testing.T) {
	cs := newReconcileCron(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				// Even with the flag set, a worker must never get a heartbeat job.
				{ID: "planner", Type: config.AgentTypeWorker, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 10},
			},
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	assert.Empty(t, heartbeatJobsFor(cs), "a worker must never have a heartbeat schedule")
}

func TestReconcileHeartbeat_DisablingRemovesJob(t *testing.T) {
	cs := newReconcileCron(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 15},
			},
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	require.Len(t, heartbeatJobsFor(cs), 1)

	// Disable → reconcile → job removed.
	cfg.Agents.List[0].HeartbeatEnabled = boolPtrHB(false)
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	assert.Empty(t, heartbeatJobsFor(cs), "disabling heartbeat must remove the job")
}

func TestReconcileHeartbeat_IntervalChangeUpdatesInPlace(t *testing.T) {
	cs := newReconcileCron(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 15},
			},
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	first := heartbeatJobsFor(cs)
	require.Len(t, first, 1)
	firstID := first[0].ID

	cfg.Agents.List[0].HeartbeatInterval = 30
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "interval change must not create a duplicate")
	assert.Equal(t, firstID, after[0].ID, "same job updated in place")
	require.NotNil(t, after[0].Schedule.EveryMS)
	assert.Equal(t, int64(30)*60_000, *after[0].Schedule.EveryMS)
}

func TestReconcileHeartbeat_Idempotent(t *testing.T) {
	cs := newReconcileCron(t)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 15},
			},
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	before := heartbeatJobsFor(cs)
	require.Len(t, before, 1)
	beforeID := before[0].ID

	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))
	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "second reconcile must not create a duplicate")
	assert.Equal(t, beforeID, after[0].ID)
}

func TestReconcileHeartbeat_LeavesUserSchedulesAlone(t *testing.T) {
	cs := newReconcileCron(t)
	// A user schedule (not a heartbeat).
	everyMS := int64(60_000)
	en := true
	_, err := cs.AddJobFull(cron.JobSpec{
		Name:     "my-daily-digest",
		Schedule: cron.CronSchedule{Kind: "recurring", EveryMS: &everyMS},
		Message:  "digest",
		AgentID:  "mia",
		Enabled:  &en,
	})
	require.NoError(t, err)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 15},
			},
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, func(string) string { return "" }))

	// The user schedule survives untouched alongside the new heartbeat job.
	names := map[string]bool{}
	for _, j := range cs.ListJobs(true) {
		names[j.Name] = true
	}
	assert.True(t, names["my-daily-digest"], "user schedule must not be removed")
	assert.True(t, names[heartbeatJobName("mia")], "heartbeat job must be created")
}

// TestReconcileHeartbeat_MessageDriftRestampsInPlace proves that editing an
// agent's HEARTBEAT.md re-stamps the existing heartbeat job's message in place
// (same job ID, no duplicate). The reconciler's drift check includes
// Payload.Message, so a content change must converge the stored job.
func TestReconcileHeartbeat_MessageDriftRestampsInPlace(t *testing.T) {
	cs := newReconcileCron(t)
	ws := t.TempDir()
	hbPath := filepath.Join(ws, "HEARTBEAT.md")
	require.NoError(t, os.WriteFile(hbPath, []byte("Check the build queue."), 0o600))

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{ID: "mia", Type: config.AgentTypeCore, HeartbeatEnabled: boolPtrHB(true), HeartbeatInterval: 15},
			},
		},
	}
	resolve := func(string) string { return ws }

	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, resolve))
	first := heartbeatJobsFor(cs)
	require.Len(t, first, 1)
	firstID := first[0].ID
	assert.Contains(t, first[0].Payload.Message, "Check the build queue.",
		"initial heartbeat message must embed the HEARTBEAT.md content")

	// Edit HEARTBEAT.md → reconcile → same job, new message.
	require.NoError(t, os.WriteFile(hbPath, []byte("Review overnight alerts."), 0o600))
	require.NoError(t, ReconcileHeartbeatSchedules(cs, cfg, resolve))

	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "message drift must update in place, not duplicate")
	assert.Equal(t, firstID, after[0].ID, "same job updated in place")
	assert.Contains(t, after[0].Payload.Message, "Review overnight alerts.",
		"edited HEARTBEAT.md content must be re-stamped onto the job")
	assert.NotContains(t, after[0].Payload.Message, "Check the build queue.",
		"the stale heartbeat message must be replaced")
}
