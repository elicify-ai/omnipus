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

// An already-passing control: exercise the actual hot-reload method with an
// old process-local lane still live, not just Start on a reused pointer.
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
	old := cron.NewCronService(filepath.Join(home, "cron", "jobs.json"))
	work := &i1CronLiveWork{entered: make(chan struct{}), release: make(chan struct{})}
	old.SetRunner(work)
	old.SetStopDrainTimeout(time.Millisecond)
	require.NoError(t, old.Start())
	interval := int64(300000)
	job, err := old.AddJobFull(cron.JobSpec{Name: "live-heartbeat", AgentID: "mia",
		SessionMode: cron.SessionModeContinue, SessionID: "live-heartbeat-session",
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &interval}, Message: "live work"})
	require.NoError(t, err)
	finished := make(chan error, 1)
	go func() { _, _, runErr := old.RunNow(job.ID); finished <- runErr }()
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(work.release) }); old.Stop(); old.WaitForLane() })
	select {
	case <-work.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real old cron lane never entered its external runner")
	}
	old.Stop() // Deliberately returns while the non-cooperative work remains live.
	stored, ok := old.GetJob(job.ID)
	require.True(t, ok)
	require.True(t, stored.State.Running)
	svc := &services{homePath: home, CronService: old}
	rs := &restartServicesState{al: al, msgBus: mb, runningServices: svc}
	reloadErr, stop := rs.restartCron() // EXACT same-process hot-reload entry.
	require.NoError(t, reloadErr)
	require.False(t, stop)
	t.Cleanup(svc.CronService.Stop)
	assert.NotSame(t, old, svc.CronService)
	loaded, ok := svc.CronService.GetJob(job.ID)
	require.True(t, ok)
	assert.True(t, loaded.State.Running, "hot reload cannot mistake live old work for a dead physical boot")
	assert.Equal(t, job.SessionID, loaded.SessionID)
	assert.Equal(t, job.Schedule, loaded.Schedule)
	once.Do(func() { close(work.release) })
	select {
	case runErr := <-finished:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("released old runner did not join")
	}
}
