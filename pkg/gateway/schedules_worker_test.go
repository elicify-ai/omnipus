package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
)

// TestRunner_WorkerOwner_NoHeartbeatRun verifies that a scheduled/heartbeat run
// whose owner is a worker is rejected with no fallback and the executor is never
// invoked. Workers have no heartbeat and run ONLY via delegation, so a per-agent
// schedule pointed at a worker must not fire.
func TestRunner_WorkerOwner_NoHeartbeatRun(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "worker", Type: config.AgentTypeWorker},
	}
	// The worker is registered+enabled — the rejection must come from the worker
	// guard, not the registration check.
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"worker": true})

	job := &cron.CronJob{ID: "jw", Name: "heartbeat", AgentID: "worker", CreatedBy: "alice"}
	sid, err := r.RunScheduled(context.Background(), job)

	assert.Error(t, err, "a scheduled run owned by a worker must fail")
	assert.Contains(t, err.Error(), "worker", "the rejection reason must reference the worker tier")
	assert.Empty(t, sid)
	assert.Empty(t, exec.calls, "executor must never run for a worker-owned schedule")
}

// TestRunner_BaseOwner_StillRuns is a control: a non-worker owner is NOT blocked
// by the worker guard (the guard is worker-specific, not a blanket reject).
func TestRunner_BaseOwner_StillRuns(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCore},
	}
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "jm", Name: "daily", AgentID: "mia", CreatedBy: "alice",
		Payload: cron.CronPayload{Message: "go"},
	}
	_, err := r.RunScheduled(context.Background(), job)

	assert.NoError(t, err, "a base-agent owner must not be blocked by the worker guard")
	assert.NotEmpty(t, exec.calls, "executor must run for a non-worker owner")
}
