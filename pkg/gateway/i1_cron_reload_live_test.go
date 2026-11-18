package gateway

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
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
	once    sync.Once
	runs    atomic.Int32
	ctxErr  atomic.Value // the run context's error when the run was released
}

func (r *i1CronLiveWork) RunScheduled(ctx context.Context, job *cron.CronJob) (string, error) {
	r.runs.Add(1)
	r.once.Do(func() { close(r.entered) })
	<-r.release // The run ignores cancellation: only the context tells whether it was cancelled.
	if err := ctx.Err(); err != nil {
		r.ctxErr.Store(err)
	}
	return job.SessionID, nil
}

// Hot reload with a live external run in flight (#1760 keep-live contract; the
// original I1 control stopped the service by hand and expected a rebuilt one).
// The exact hot-reload entry (restartCron) must leave the SAME running service
// in place: the in-flight run keeps its live context, the job keeps its running
// flag, session and schedule (a reload must not mistake live work for a dead
// physical boot), and the service still accepts and runs work afterwards.
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
	live := cron.NewCronService(filepath.Join(home, "cron", "jobs.json"))
	work := &i1CronLiveWork{entered: make(chan struct{}), release: make(chan struct{})}
	live.SetRunner(work)
	require.NoError(t, live.Start())
	interval := int64(300000)
	job, err := live.AddJobFull(cron.JobSpec{Name: "live-heartbeat", AgentID: "mia",
		SessionMode: cron.SessionModeContinue, SessionID: "live-heartbeat-session",
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &interval}, Message: "live work"})
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() { _, _, runErr := live.RunNow(job.ID); finished <- runErr }()
	var released sync.Once
	releaseWork := func() { released.Do(func() { close(work.release) }) }
	t.Cleanup(func() { releaseWork(); live.Stop(); live.WaitForLane() })
	select {
	case <-work.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real cron lane never entered its external runner")
	}
	before, ok := live.GetJob(job.ID)
	require.True(t, ok)
	require.True(t, before.State.Running, "instrument: the run is genuinely in flight")

	svc := &services{homePath: home, CronService: live}
	rs := &restartServicesState{al: al, msgBus: mb, runningServices: svc}
	reloadErr, stop := rs.restartCron() // EXACT same-process hot-reload entry.
	require.NoError(t, reloadErr)
	require.False(t, stop)
	assert.Same(t, live, svc.CronService, "#1760: hot reload keeps the one live cron service")

	loaded, ok := svc.CronService.GetJob(job.ID)
	require.True(t, ok)
	assert.True(t, loaded.State.Running, "hot reload cannot mistake live work for a dead physical boot")
	assert.Equal(t, job.SessionID, loaded.SessionID)
	assert.Equal(t, job.Schedule, loaded.Schedule)

	releaseWork()
	select {
	case runErr := <-finished:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("released runner did not join")
	}
	assert.Nil(t, work.ctxErr.Load(), "#1760: hot reload must not cancel the in-flight run's context")
	done, ok := svc.CronService.GetJob(job.ID)
	require.True(t, ok)
	assert.False(t, done.State.Running, "the finished run clears its running flag on the kept service")

	// The kept service is still a working scheduler, not a stopped husk.
	_, _, runErr := svc.CronService.RunNow(job.ID)
	require.NoError(t, runErr)
	assert.EqualValues(t, 2, work.runs.Load(), "the kept service must accept and run new work after the reload")
}

// With no live cron service (a harness or an aborted boot) restartCron builds
// one, and the saved schedules.* settings must pass the SAME validation the
// live branch applies (CHECK round 2, finding 1): a negative retry backoff is
// refused, no service is published and none is left running.
func TestRestartCron_NoLiveService_RejectsNegativeBackoff_1760(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home,
		DefaultModel: config.DefaultModel{Model: "test-model"}}, List: []config.AgentConfig{{ID: "mia", Home: home}}}}
	cfg.Schedules.RetryBackoffMs = []int64{60000, -1}
	ensureTestWorkspaceMembership(t, cfg)
	mb := bus.NewMessageBus()
	al, err := agent.NewAgentLoop(cfg, mb, &restMockProvider{})
	require.NoError(t, err)
	t.Cleanup(func() { al.Close(); mb.Close() })

	svc := &services{homePath: home}
	rs := &restartServicesState{al: al, msgBus: mb, runningServices: svc}
	reloadErr, stop := rs.restartCron()
	t.Cleanup(func() {
		if svc.CronService != nil {
			svc.CronService.Stop()
		}
	})
	require.Error(t, reloadErr, "a negative retry backoff must be refused on the no-service path too")
	assert.Contains(t, reloadErr.Error(), "retry backoff entry 1 is negative")
	assert.True(t, stop)
	assert.Nil(t, svc.CronService, "a rejected configuration must not publish a cron service")
}
