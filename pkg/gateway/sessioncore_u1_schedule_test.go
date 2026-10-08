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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/cron"
)

// DEL-01: a workspace-scoped "main" session-mode job resolves to the computed
// main id, never to the retired per-agent sched-main-<agent> id.
func TestSessionCoreU1_ScheduledMainModeResolvesToTheComputedMain(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "u1-main-mode", Name: heartbeatJobName("W1", "mia"), AgentID: "mia",
		SessionMode: cron.SessionModeMain,
		Payload:     cron.CronPayload{Kind: heartbeatJobKind, Message: "check"},
	}
	sid, err := r.pickSession(job, "mia")
	require.NoError(t, err)
	assert.Equal(t, "main-session-W1-mia", sid, "DEL-01: the computed main, spec literal")
	assert.NotEqual(t, "sched-main-mia", sid)

	meta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "main", string(meta.Type))
	assert.Equal(t, "mia", meta.AgentID)
	assert.Equal(t, "W1", meta.WorkspaceID)

	_, err = exec.store.GetMeta("sched-main-mia")
	assert.Error(t, err, "DEL-01: the retired standing-session id is never minted")
}
