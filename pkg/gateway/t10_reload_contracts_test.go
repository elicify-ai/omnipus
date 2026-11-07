package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Seed an existing typed job. Creating-kind correctness is independently
// observed in TestT10_ProducersPublishFinalKindExactlyOnce.
func t10ReloadHeartbeat(t *testing.T, cs *cron.CronService) *cron.CronJob {
	t.Helper()
	every := int64(3600000)
	j, err := cs.AddJobFull(cron.JobSpec{Kind: cron.KindHeartbeat, Name: "T10 reload heartbeat", AgentID: "mia", Message: "retry", Schedule: cron.CronSchedule{Kind: "every", EveryMS: &every}})
	require.NoError(t, err)
	j.Payload.Kind = "heartbeat"
	require.NoError(t, cs.UpdateJob(j))
	return j
}

// Traces to: schedules-t10-spec-r6.md S-D.5, FR-07/26, TDD 10.
func TestT10_ReloadKeepsBootBackoffUntilPhysicalRestart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry-only", true: "retry-and-retired-capacity"}[legacy], func(t *testing.T) {
			st := t10BootStage(t)
			st.cfg.Schedules.RetryBackoffMs = []int64{11000, 22000, 33000}
			cs, err := setupCronTool(st.agentLoop, st.msgBus, st.homePath, st.cfg, nil)
			require.NoError(t, err)
			clk := &t10GatewayClock{at: time.Now().Add(48 * time.Hour)}
			cs.SetClock(clk)
			runner := &t10GatewayRunner{fail: true}
			cs.SetRunner(runner)
			require.NoError(t, cs.StartAfterPhysicalBoot())
			svc := &services{homePath: st.homePath, CronService: cs}
			st.runningServices = svc
			defer func() { cs.Stop(); cs.WaitForLane() }()
			j := t10ReloadHeartbeat(t, cs)
			clk.advance(time.Hour)
			first := clk.Now()
			cs.RunDueJobs(first)
			cs.WaitForLane()
			failed, ok := cs.GetJob(j.ID)
			require.True(t, ok)
			require.Equal(t, 1, failed.State.RetryAttempt)
			require.Equal(t, first.Add(11*time.Second).UnixMilli(), *failed.State.NextRunAtMS)
			// Real config swap plus real scheduler reload phases, not a call to a live
			// backoff setter. The obsolete key enters via JSON so it cannot mandate a field.
			encoded, err := json.Marshal(st.cfg)
			require.NoError(t, err)
			var doc map[string]any
			require.NoError(t, json.Unmarshal(encoded, &doc))
			schedules := map[string]any{"retry_backoff_ms": []int64{41000, 42000, 43000}}
			if legacy {
				schedules["max_concurrent_runs"] = 1
			}
			doc["schedules"] = schedules
			encoded, err = json.Marshal(doc)
			require.NoError(t, err)
			next, err := st.cfg.Clone()
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, next))
			stopAndCleanupServices(svc, time.Second, true)
			st.agentLoop.SwapConfig(next)
			rs := &restartServicesState{al: st.agentLoop, msgBus: st.msgBus, runningServices: svc, cfg: st.agentLoop.GetConfig()}
			reloadErr, stop := rs.restartCron()
			require.NoError(t, reloadErr)
			require.False(t, stop)
			assert.Same(t, cs, svc.CronService, "reload must preserve the only engine")
			retained, ok := cs.GetJob(j.ID)
			require.True(t, ok)
			assert.Equal(t, failed, retained, "reload cannot reset backoff state or recompute next instant")
			clk.advance(11 * time.Second)
			second := clk.Now()
			cs.RunDueJobs(second)
			cs.WaitForLane()
			retained, _ = cs.GetJob(j.ID)
			assert.Equal(t, int32(2), runner.calls.Load(), "same engine still dispatches after reload")
			assert.Equal(t, 2, retained.State.RetryAttempt)
			assert.Equal(t, second.Add(22*time.Second).UnixMilli(), *retained.State.NextRunAtMS, "old boot retry schedule remains load-bearing")
			// Physical restart is intentionally distinct: construct new engine from
			// the same file and the new config, then prove the new value via failure.
			cs.Stop()
			cs.WaitForLane()
			svc.CronService.Stop()
			svc.CronService.WaitForLane()
			fresh, err := setupCronTool(st.agentLoop, st.msgBus, st.homePath, next, nil)
			require.NoError(t, err)
			fresh.SetClock(clk)
			fresh.SetRunner(runner)
			require.NoError(t, fresh.StartAfterPhysicalBoot())
			defer fresh.Stop()
			newJob := t10ReloadHeartbeat(t, fresh)
			clk.advance(time.Hour)
			at := clk.Now()
			fresh.RunDueJobs(at)
			fresh.WaitForLane()
			got, ok := fresh.GetJob(newJob.ID)
			require.True(t, ok)
			assert.Equal(t, 1, got.State.RetryAttempt)
			assert.Equal(t, at.Add(41*time.Second).UnixMilli(), *got.State.NextRunAtMS)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md S-C.3, TDD 19/FR-26.
func TestReload_KeepsOneServiceAndRederivesHeartbeat(t *testing.T) {
	st := t10BootStage(t)
	t10SeedRecurringTask(t, st)
	_, stop, err := st.startSchedulers()
	require.NoError(t, err)
	require.False(t, stop)
	svc := st.runningServices
	engine, tasks, loops := svc.CronService, svc.TaskTrigger, svc.LoopScheduler
	path := filepath.Join(st.homePath, "cron", "jobs.json")
	// Malformed disk is deliberate: same-process reload must never read it.
	damaged := []byte(`{"version":1,"jobs":[{"id":"t10-damage","enabled":"invalid"}]}`)
	require.NoError(t, os.WriteFile(path, damaged, 0600))
	stopAndCleanupServices(svc, time.Second, true)
	rs := &restartServicesState{al: st.agentLoop, msgBus: st.msgBus, runningServices: svc, cfg: st.agentLoop.GetConfig()}
	err, stop = rs.restartCron()
	assert.NoError(t, err)
	assert.False(t, stop)
	assert.Same(t, engine, svc.CronService)
	err, stop = rs.restartSchedulersAndDrains()
	require.NoError(t, err)
	require.False(t, stop)
	// Domain schedulers must also survive the reload, not merely retain a disk path.
	assert.Same(t, tasks, svc.TaskTrigger)
	assert.Same(t, loops, svc.LoopScheduler)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, damaged, after, "reload performs no scheduler-store read/write")
	clk := &t10GatewayClock{at: time.Now().Add(48 * time.Hour)}
	engine.SetClock(clk)
	recorder := &t10RecordHeartbeat{messages: map[string]string{}}
	engine.SetRunner(recorder)
	// OPS-10's re-derivation is the same production reconciler used by reload
	// workspace changes, invoked against the preserved engine, not a new service.
	workspaces := []workspace.Workspace{{ID: "t10-reload-w", CoreTeam: []string{"mia"}, MemberConfigs: buildMemberConfigs("mia", true, 5, "updated T10 heartbeat")}}
	bytes, err := json.Marshal(workspaces[0])
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(st.homePath, "workspaces"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(st.homePath, "workspaces", "t10-reload-w.json"), bytes, 0600))
	api := &restAPI{homePath: st.homePath, agentLoop: st.agentLoop}
	api.cronService.Store(engine)
	api.reconcileHeartbeatSchedules()
	clk.advance(5 * time.Minute)
	engine.RunDueJobs(clk.Now())
	engine.WaitForLane()
	recorder.mu.Lock()
	assert.Equal(t, buildHeartbeatMessage("updated T10 heartbeat"), recorder.messages["mia"])
	recorder.mu.Unlock()
}
