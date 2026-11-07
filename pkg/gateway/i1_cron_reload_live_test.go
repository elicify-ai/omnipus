package gateway

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type i1CronLiveWork struct {
	entered chan struct{}
	release chan struct{}
}

func (r *i1CronLiveWork) RunScheduled(_ context.Context, job *cron.CronJob) (string, error) {
	close(r.entered)
	<-r.release // An external run can outlive Stop's bounded cancellation drain.
	return job.SessionID, nil
}

// Traces to: schedules-t10-spec-r6.md S-C.3, FR-26.
// A same-process reload keeps the one engine. The in-flight callback is not
// stranded: Running stays set across the reload and clears when that same
// instance's callback returns.
func TestI1CronHotReloadPreservesLiveRunning(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home,
		DefaultModel: config.DefaultModel{Model: "test-model"}}, List: []config.AgentConfig{{ID: "mia", Home: home}}}}
	ensureTestWorkspaceMembership(t, cfg)
	mb := bus.NewMessageBus()
	al, err := agent.NewAgentLoop(cfg, mb, &restMockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close(); mb.Close() })
	engine := cron.NewCronService(filepath.Join(home, "cron", "jobs.json"))
	work := &i1CronLiveWork{entered: make(chan struct{}), release: make(chan struct{})}
	engine.SetRunner(work)
	require.NoError(t, engine.Start())
	interval := int64(300000)
	job, err := engine.AddJobFull(cron.JobSpec{Kind: cron.KindHeartbeat, Name: "live-heartbeat", AgentID: "mia",
		SessionMode: cron.SessionModeContinue, SessionID: "live-heartbeat-session",
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &interval}, Message: "live work"})
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() { _, _, runErr := engine.RunNow(job.ID); finished <- runErr }()
	var once sync.Once
	release := func() { once.Do(func() { close(work.release) }) }
	t.Cleanup(func() { release(); engine.Stop(); engine.WaitForLane() })
	select {
	case <-work.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real cron lane never entered its external runner")
	}
	stored, ok := engine.GetJob(job.ID)
	require.True(t, ok)
	require.True(t, stored.State.Running, "the live callback must be in flight before reload")
	svc := &services{homePath: home, CronService: engine}
	rs := &restartServicesState{al: al, msgBus: mb, runningServices: svc}
	reloadErr, stop := rs.restartCron()
	require.NoError(t, reloadErr)
	require.False(t, stop)
	require.Same(t, engine, svc.CronService, "FR-26: a reload keeps the one engine; it does not reconstruct it")
	loaded, ok := svc.CronService.GetJob(job.ID)
	require.True(t, ok)
	assert.True(t, loaded.State.Running, "reload must not clear a live Running flag or strand the callback on a new load")
	assert.Equal(t, job.SessionID, loaded.SessionID)
	assert.Equal(t, job.Schedule, loaded.Schedule)
	release()
	select {
	case runErr := <-finished:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("released runner did not join on the kept engine")
	}
	settled, ok := svc.CronService.GetJob(job.ID)
	require.True(t, ok)
	assert.False(t, settled.State.Running, "completion lands on the same engine; the running job is not stranded")
	assert.Same(t, engine, svc.CronService, "settlement did not swap the engine")
}
