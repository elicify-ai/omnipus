package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t10RecordHeartbeat struct {
	mu       sync.Mutex
	messages map[string]string
}

func (r *t10RecordHeartbeat) RunScheduled(_ context.Context, j *cron.CronJob) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages[j.AgentID] = j.Payload.Message
	return "main-" + j.AgentID, nil
}

// Traces to: schedules-t10-spec-r6.md lines 841–846, 1093–1097; TDD 5/14/17, D-3.7/D-5.10/D-10.4.
// This proves reconciler-to-engine timing/content without the retired API. The
// owner-pinned ProcessScheduled/main-session behavior is covered by the existing
// scheduled-runner regression suite, not faked by this recording handler.
func TestT10_HeartbeatsReDerivedAndFireWithoutSchedulesAPI(t *testing.T) {
	cs := newReconcileCron(t)
	clk := &t10GatewayClock{at: time.Now().Add(48 * time.Hour)}
	cs.SetClock(clk)
	recorder := &t10RecordHeartbeat{messages: map[string]string{}}
	cs.SetRunner(recorder)
	ws := []workspace.Workspace{
		{ID: "alpha", CoreTeam: []string{"mia"}, MemberConfigs: buildMemberConfigs("mia", true, 5, "check alpha")},
		{ID: "beta", CoreTeam: []string{"ava"}, MemberConfigs: buildMemberConfigs("ava", true, 15, "check beta")},
	}
	require.NoError(t, ReconcileHeartbeatSchedules(cs, ws, neverWorker))
	jobs := cs.ListJobs(true)
	require.Len(t, jobs, 2)
	require.NoError(t, ReconcileHeartbeatSchedules(cs, ws, neverWorker))
	assert.Equal(t, jobs, cs.ListJobs(true), "reconciliation is idempotent")
	require.NoError(t, cs.StartAfterPhysicalBoot())
	defer cs.Stop()
	clk.advance(5 * time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()
	recorder.mu.Lock()
	assert.Equal(t, map[string]string{"mia": buildHeartbeatMessage("check alpha")}, recorder.messages)
	recorder.mu.Unlock()
	clk.advance(10 * time.Minute)
	cs.RunDueJobs(clk.Now())
	cs.WaitForLane()
	recorder.mu.Lock()
	assert.Equal(t, map[string]string{"mia": buildHeartbeatMessage("check alpha"), "ava": buildHeartbeatMessage("check beta")}, recorder.messages)
	recorder.mu.Unlock()
	for _, j := range cs.ListJobs(true) {
		assert.Equal(t, "heartbeat", j.Payload.Kind)
		assert.Equal(t, "main-"+j.AgentID, j.SessionID)
		assert.Equal(t, "ok", j.State.LastStatus)
	}
}

// Traces to: schedules-t10-spec-r6.md lines 608–613; TDD 5/6, FR-25.
func TestT10_HeartbeatReconcilePreservesForeignKindsByteForByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	at := time.Now().Add(time.Hour).UnixMilli()
	foreign := []cron.CronJob{
		{ID: "foreign-loop", Name: "heartbeat:deceptive-name", Payload: cron.CronPayload{Kind: "loop", Message: "s1"}, Enabled: true, Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}, State: cron.CronJobState{NextRunAtMS: &at, LastError: "retained history"}},
		{ID: "foreign-task", Name: "task:t1", Payload: cron.CronPayload{Kind: "task", Message: "t1"}, Enabled: true, Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}, State: cron.CronJobState{NextRunAtMS: &at}},
	}
	all := append(append([]cron.CronJob{}, foreign...), cron.CronJob{ID: "orphan-heartbeat", Name: "heartbeat:absent:mia", Payload: cron.CronPayload{Kind: "heartbeat"}})
	data, err := json.Marshal(cron.CronStore{Version: 1, Jobs: all})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
	cs := cron.NewCronService(path)
	require.NoError(t, ReconcileHeartbeatSchedules(cs, nil, neverWorker))
	assert.ElementsMatch(t, foreign, cs.ListJobs(true), "only payload kind owns a mutation; a foreign job's name cannot make it heartbeat-owned")
}
