//go:build !cgo

// ADR-027 — workspace-scoped heartbeat reconciler tests. Proves:
//  1. A (workspace, Main-agent) pair with heartbeat enabled gets a recurring cron job.
//  2. A worker is never given a heartbeat job (even if the member_configs entry exists).
//  3. Disabling an agent's heartbeat removes its job.
//  4. Changing the interval updates the existing job (no duplicate).
//  5. Reconcile is idempotent (a second run makes no changes).
//  6. User (non-heartbeat) schedules are never touched.
//  7. Multiple workspaces × multiple members produce separate named jobs.
//  8. Message drift (body change) re-stamps the existing job in place.
//  9. SessionID drift (session replaced) re-stamps the existing job in place.

package gateway

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/workspace"
)

func newReconcileCron(t *testing.T) *cron.CronService {
	t.Helper()
	return cron.NewCronService(filepath.Join(t.TempDir(), "cron.json"))
}

func heartbeatJobsFor(cs *cron.CronService) []cron.CronJob {
	var out []cron.CronJob
	for _, j := range cs.ListJobs(true) {
		if isHeartbeatJob(j) {
			out = append(out, j)
		}
	}
	return out
}

// neverWorker is an isWorker predicate that always returns false (all agents are Main).
func neverWorker(_ string) bool { return false }

// alwaysWorker is an isWorker predicate that always returns true (all agents are workers).
func alwaysWorker(_ string) bool { return true }

// buildMemberConfigs is a convenience constructor for workspace.MemberConfigs.
func buildMemberConfigs(agentID string, enabled bool, intervalMins int, body, sessionID string) map[string]workspace.MemberConfig {
	return map[string]workspace.MemberConfig{
		agentID: {
			Heartbeat: &workspace.MemberHeartbeat{
				Enabled:         enabled,
				IntervalMinutes: intervalMins,
				Body:            body,
				SessionID:       sessionID,
			},
		},
	}
}

func TestReconcileHeartbeat_CreatesForEnabledMain(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check open PRs.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))

	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1, "exactly one heartbeat job")
	j := jobs[0]
	assert.Equal(t, heartbeatJobName("ws1", "mia"), j.Name)
	assert.Equal(t, "mia", j.AgentID)
	assert.Equal(t, heartbeatJobKind, j.Payload.Kind)
	assert.True(t, j.Enabled)
	// Heartbeat jobs must use the contract-valid trigger kind "every" (not
	// "recurring", which fails ScheduleTrigger.yaml Zod validation in the SPA).
	assert.Equal(t, "every", j.Schedule.Kind, "heartbeat job must use contract-valid kind 'every'")
	require.NotNil(t, j.Schedule.EveryMS)
	assert.Equal(t, int64(15)*60_000, *j.Schedule.EveryMS)
	assert.Equal(t, cron.SessionModeContinue, j.SessionMode)
	assert.Contains(t, j.Payload.Message, "Check open PRs.")
}

func TestReconcileHeartbeat_WorkerNeverScheduled(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("planner", true, 10, "Check tasks.", ""),
		},
	}
	// isWorker returns true for every agent → reconciler must skip planner.
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, alwaysWorker))
	assert.Empty(t, heartbeatJobsFor(cs), "a worker must never have a heartbeat schedule")
}

func TestReconcileHeartbeat_DisablingRemovesJob(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	require.Len(t, heartbeatJobsFor(cs), 1)

	// Disable → reconcile → job removed.
	workspaces[0].MemberConfigs["mia"].Heartbeat.Enabled = false
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	assert.Empty(t, heartbeatJobsFor(cs), "disabling heartbeat must remove the job")
}

func TestReconcileHeartbeat_IntervalChangeUpdatesInPlace(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	first := heartbeatJobsFor(cs)
	require.Len(t, first, 1)
	firstID := first[0].ID

	// Change interval.
	workspaces[0].MemberConfigs["mia"].Heartbeat.IntervalMinutes = 30
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "interval change must not create a duplicate")
	assert.Equal(t, firstID, after[0].ID, "same job updated in place")
	require.NotNil(t, after[0].Schedule.EveryMS)
	assert.Equal(t, int64(30)*60_000, *after[0].Schedule.EveryMS)
}

func TestReconcileHeartbeat_Idempotent(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	before := heartbeatJobsFor(cs)
	require.Len(t, before, 1)
	beforeID := before[0].ID

	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "second reconcile must not create a duplicate")
	assert.Equal(t, beforeID, after[0].ID)
}

func TestReconcileHeartbeat_LeavesUserSchedulesAlone(t *testing.T) {
	cs := newReconcileCron(t)
	// A user schedule (not a heartbeat). Use a contract-valid kind ("every").
	everyMS := int64(60_000)
	en := true
	_, err := cs.AddJobFull(cron.JobSpec{
		Name:     "my-daily-digest",
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:  "digest",
		AgentID:  "mia",
		Enabled:  &en,
	})
	require.NoError(t, err)

	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))

	// The user schedule survives untouched alongside the new heartbeat job.
	names := map[string]bool{}
	for _, j := range cs.ListJobs(true) {
		names[j.Name] = true
	}
	assert.True(t, names["my-daily-digest"], "user schedule must not be removed")
	assert.True(t, names[heartbeatJobName("ws1", "mia")], "heartbeat job must be created")
}

func TestReconcileHeartbeat_MessageDriftRestampsInPlace(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check the build queue.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	first := heartbeatJobsFor(cs)
	require.Len(t, first, 1)
	firstID := first[0].ID
	assert.Contains(t, first[0].Payload.Message, "Check the build queue.")

	// Edit body → reconcile → same job, new message.
	workspaces[0].MemberConfigs["mia"].Heartbeat.Body = "Review overnight alerts."
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))

	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "message drift must update in place, not duplicate")
	assert.Equal(t, firstID, after[0].ID, "same job updated in place")
	assert.Contains(t, after[0].Payload.Message, "Review overnight alerts.")
	assert.NotContains(t, after[0].Payload.Message, "Check the build queue.")
}

func TestReconcileHeartbeat_MultiWorkspaceMultiMember(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "WS1 check.", ""),
		},
		{
			ID:            "ws2",
			MemberConfigs: buildMemberConfigs("ray", true, 20, "WS2 scout.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))

	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 2, "one job per (workspace, agent) pair")

	names := map[string]bool{}
	for _, j := range jobs {
		names[j.Name] = true
	}
	assert.True(t, names[heartbeatJobName("ws1", "mia")], "ws1/mia job must exist")
	assert.True(t, names[heartbeatJobName("ws2", "ray")], "ws2/ray job must exist")
}

func TestReconcileHeartbeat_SessionIDPropagatesToJob(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", "sess-abc-123"),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1)
	assert.Equal(t, "sess-abc-123", jobs[0].SessionID, "session_id must propagate to the cron job")
}

func TestReconcileHeartbeat_SessionIDDriftUpdatesInPlace(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", "sess-old"),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	first := heartbeatJobsFor(cs)
	require.Len(t, first, 1)
	firstID := first[0].ID
	assert.Equal(t, "sess-old", first[0].SessionID)

	// Replace session_id → reconciler must update in place.
	workspaces[0].MemberConfigs["mia"].Heartbeat.SessionID = "sess-new"
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	after := heartbeatJobsFor(cs)
	require.Len(t, after, 1, "session_id drift must update in place, not duplicate")
	assert.Equal(t, firstID, after[0].ID)
	assert.Equal(t, "sess-new", after[0].SessionID)
}

func TestReconcileHeartbeat_WorkspaceDeletionRemovesJobs(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws1",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check tasks.", ""),
		},
		{
			ID:            "ws2",
			MemberConfigs: buildMemberConfigs("ray", true, 20, "Scout.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	require.Len(t, heartbeatJobsFor(cs), 2)

	// Delete ws2 → reconcile with only ws1 → ws2 job removed.
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces[:1], neverWorker))
	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1)
	assert.Equal(t, heartbeatJobName("ws1", "mia"), jobs[0].Name)
}

func TestReleaseHeartbeatJobsForWorkspace(t *testing.T) {
	cs := newReconcileCron(t)
	workspaces := []workspace.Workspace{
		{
			ID:            "ws-alpha",
			MemberConfigs: buildMemberConfigs("mia", true, 15, "Check.", ""),
		},
		{
			ID:            "ws-beta",
			MemberConfigs: buildMemberConfigs("ray", true, 20, "Scout.", ""),
		},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	require.Len(t, heartbeatJobsFor(cs), 2)

	// Release only ws-alpha; ws-beta job must survive.
	releaseHeartbeatJobsForWorkspace(cs, "ws-alpha")

	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1)
	assert.Equal(t, heartbeatJobName("ws-beta", "ray"), jobs[0].Name)
}
