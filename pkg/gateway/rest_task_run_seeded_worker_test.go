// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_task_run_seeded_worker_test.go — a task assigned to the seeded Worker
// finishes. A native task run completes only when its goal_claim is upheld by
// the Judge (ADR-043 §8, ADR-084 §11, issue #710), so the Worker's own seeded
// tool policy decides whether any task assigned to it can ever reach Done.
// The live smoke test on build f4e482561 found it could not: the Worker did
// the work, its goal_claim was refused by its seeded policy, and the task
// looped through its tries.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestTaskRun_SeededWorker_ReachesDoneThroughGoalClaimAndJudge assigns a task
// to the Worker exactly as a fresh install seeds it (coreagent.SeedConfig, not
// a hand-written policy map), runs it through the real REST route, executor,
// agent loop, tool-policy filter and Judge, and requires a genuine Done whose
// result is the Worker's own claim evidence.
func TestTaskRun_SeededWorker_ReachesDoneThroughGoalClaimAndJudge(t *testing.T) {
	seeded := config.DefaultConfig()
	require.True(t, coreagent.SeedConfig(seeded))
	var worker config.AgentConfig
	for _, ac := range seeded.Agents.List {
		if ac.ID == string(coreagent.IDWorker) {
			worker = ac
		}
	}
	require.Equal(t, string(coreagent.IDWorker), worker.ID, "the fresh-install seed must produce the Worker")
	require.NotNil(t, worker.Tools, "the seeded Worker must carry its own tool-policy map")

	api := newTestRestAPIAlignedStoresWithProvider(
		t, &taskRunNowSuccessProvider{}, judgeAgentForTaskRunTests(t), worker)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{string(coreagent.IDWorker)})

	tsk := createTaskViaAPI(t, api, "SeededWorkerTaskRun", wsID)
	bindMetVerdictJudge(t, api, func() []string {
		g, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, tsk.Id)
		if err != nil || g == nil {
			return nil
		}
		ids := make([]string, 0, len(g.Criteria)+len(g.DoD))
		for _, c := range g.Criteria {
			ids = append(ids, c.ID)
		}
		for _, c := range g.DoD {
			ids = append(ids, c.ID)
		}
		return ids
	})
	wAssign := patchTask(t, api, tsk.Id, `{"agent_id":"worker"}`)
	require.Equal(t, http.StatusOK, wAssign.Code,
		"the Worker is on the workspace team, so assigning it must succeed; body=%s", wAssign.Body.String())

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tasks/", api.HandleTasks)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/api/v1/tasks/"+tsk.Id+"/runs", "application/json", bytes.NewReader([]byte("{}")))
	require.NoError(t, err)
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "body=%s", string(body))

	// Wait on the task store, not on GET /tasks/{id}/runs: the REST route is
	// behind a per-IP rate limiter shared by every test in this binary, and
	// polling it tightly for the full timeout would starve later tests of it.
	storeStatus := func() task.Status {
		got, err := api.taskStore.Get(tsk.Id)
		if err != nil || got == nil {
			return ""
		}
		return got.Status
	}
	assert.Eventually(t, func() bool { return storeStatus() == task.StatusDone },
		15*time.Second, 50*time.Millisecond)
	require.Equal(t, task.StatusDone, storeStatus(),
		"a task assigned to the seeded Worker must reach Done through goal_claim and a met Judge verdict; "+
			"a Worker whose seeded policy refuses goal_claim can never finish a task")

	// The task's status is written a moment before its run row is closed, so
	// wait for the run row too — again on the store, not the rate-limited route.
	assert.Eventually(t, func() bool {
		runs, err := api.taskStore.ListRuns(tsk.Id)
		if err != nil {
			return false
		}
		for _, run := range runs {
			if run.Status == task.StatusDone {
				return true
			}
		}
		return false
	}, 15*time.Second, 50*time.Millisecond, "the task's run row must close as done")

	// One read through the real route: the Done run carries the claim evidence.
	w := getTaskRuns(t, api, tsk.Id)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var runs []gen.TaskRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runs))
	var done *gen.TaskRun
	for i := range runs {
		if runs[i].Status == gen.TaskRunStatusDone {
			done = &runs[i]
			break
		}
	}
	require.NotNil(t, done, "the task's run history must contain the Done run; runs=%s", w.Body.String())
	require.NotNil(t, done.Result, "an upheld claim's evidence becomes the run's result")
	assert.Contains(t, *done.Result, taskRunNowSuccessSummary,
		"the Done run's result must be the Worker's own goal_claim evidence")
}
