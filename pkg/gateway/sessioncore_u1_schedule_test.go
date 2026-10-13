// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U1: DEL-01 for the schedules seam. The standing-session
// branch `sched-main-<agent>` (pkg/gateway/schedules.go::pickSession) is matured
// into the one computed main. Existing TestRunner_SessionMode_Main (schedules_
// runner_test.go) asserts the OLD literal "sched-main-mia" and must be replaced
// by the GREEN step; it is reported, not edited, here.

package gateway

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// DEL-01: a workspace-scoped heartbeat job resolves to the computed main id,
// never to a retired per-agent address. The job under test is the one the
// RECONCILER actually produces — not a hand-built job: a hand-built main-mode
// job would let a reconciler that mints an addressed CONTINUE heartbeat (or a
// stored sched-main-<agent> address) survive, because the test would assert on a
// job the reconciler never emits (C1 CHECK survivor m18, 2026-10-08).
func TestSessionCoreU1_ScheduledMainModeResolvesToTheComputedMain(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	// Drive the real reconciler to produce the heartbeat job for the enabled
	// (W1, mia) pair.
	cs := cron.NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
	workspaces := []workspace.Workspace{{
		ID:            "W1",
		CoreTeam:      []string{"mia"},
		MemberConfigs: buildMemberConfigs("mia", true, 15, "check"),
	}}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, workspaces, neverWorker))
	jobs := heartbeatJobsFor(cs)
	require.Len(t, jobs, 1, "exactly one heartbeat job for the enabled (W1, mia) pair")
	job := jobs[0]
	require.Equal(t, heartbeatJobName("W1", "mia"), job.Name)

	// FR-017 / DEL-01: a heartbeat runs in the member's computed MAIN. The
	// reconciler's job must therefore be MAIN-mode and must carry NO stored
	// address — an addressed session id is the retired continue-style identity.
	require.Equal(t, cron.SessionModeMain, job.SessionMode,
		"DEL-01: the reconciler's heartbeat job is main-mode, not an addressed continue")
	require.Empty(t, job.SessionID,
		"the main id is COMPUTED from the pair; the reconciler's job stores no address")

	sid, err := r.pickSession(&job, "mia")
	require.NoError(t, err)
	assert.Equal(t, "main-session-W1+mia", sid, "DEL-01: the computed main, founder-ruled literal")
	assert.NotEqual(t, "sched-main-mia", sid, "DEL-01: the retired standing-session id is never minted")
	assert.NotEqual(t, "retired-heartbeat-mia", sid, "no addressed heartbeat identity is ever resolved")

	meta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "main", string(meta.Type))
	assert.Equal(t, "mia", meta.AgentID)
	assert.Equal(t, "W1", meta.WorkspaceID)

	_, err = exec.store.GetMeta("sched-main-mia")
	assert.Error(t, err, "DEL-01: the retired standing-session id is never minted")
	_, err = exec.store.GetMeta("retired-heartbeat-mia")
	assert.Error(t, err, "no addressed heartbeat session id is ever minted")
}
