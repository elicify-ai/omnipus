//go:build goolm && stdjson

package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// DESIGN 4: the locked prior state, not each caller's stale pre-read, decides
// the winning explicit Retry. Both requests start at the same held barrier.
func TestCrashRecovery_ConcurrentRetryCreatesExactlyOneFreshAttempt(t *testing.T) {
	api, old, closed := crashTaskAfterBoot(t)
	p := newCrashTaskProvider()
	p.retry = true
	work, err := workspace.EnsureWorkDir(api.homePath, old.WorkspaceID)
	require.NoError(t, err)
	p.path = filepath.Join(work, "concurrent-retry.txt")
	inst, ok := api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = p
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow}})
	t.Cleanup(func() { p.open(); api.taskExecutor.Drain(10 * time.Second) })
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+old.ID, strings.NewReader(`{"status":"in_progress"}`))
			r.Header.Set("Content-Type", "application/json")
			api.HandleTasks(w, r)
			responses <- w
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	for w := range responses {
		require.Equal(t, http.StatusOK, w.Code, "a winning or idempotent live Retry: %s", w.Body.String())
	}
	runs, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	require.Len(t, runs, 2, "concurrent Retry creates one additional run, never zero or two")
	fresh, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	assert.NotEqual(t, old.SessionID, fresh.SessionID)
	for _, run := range runs {
		if run.RunID == closed.RunID {
			assert.Equal(t, closed, run, "old immutable attempt preserved")
		}
	}
	gatewayCrashAwait(t, p.resultCommitted)
	bytes, err := os.ReadFile(p.path)
	require.NoError(t, err)
	assert.Equal(t, "RETRY\n", string(bytes), "concurrent admission applies exactly one new effect")
}

func TestCrashRecovery_RetryAtCapacityQueuesThenRunsAfterRelease(t *testing.T) {
	api, old, closed := crashTaskAfterBoot(t)
	api.agentLoop.GetConfig().Performance.MaxParallelAgents = 1
	p := newCrashTaskProvider()
	p.retry = true
	work, err := workspace.EnsureWorkDir(api.homePath, old.WorkspaceID)
	require.NoError(t, err)
	p.path = filepath.Join(work, "queued-retry.txt")
	inst, ok := api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = p
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow}})
	t.Cleanup(func() { p.open(); api.taskExecutor.Drain(10 * time.Second) })
	parent, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, api.agentLoop.GetSessionStore().SetMeta(parent.ID, session.MetaPatch{WorkspaceID: &old.WorkspaceID}))
	launcher := agent.NewSteerLauncher(api.agentLoop)
	busy, err := launcher.Launch(context.Background(), steer.LaunchRequest{SteeringSessionID: parent.ID, TargetAgentID: "mia", Task: "hold the only capacity slot", Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "retry-capacity"}})
	require.NoError(t, err)
	dispatched, err := launcher.Dispatch(context.Background(), busy.SessionID, busy.Generation)
	require.NoError(t, err)
	require.Equal(t, steer.DispatchRunning, dispatched.State)
	require.False(t, gatewayCrashAwait(t, p.entered), "instrument: independent helper owns the only real slot")
	w := patchTask(t, api, old.ID, `{"status":"in_progress"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	runs, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	require.Len(t, runs, 2, "capacity queue still owns a real fresh run, not just a task timer")
	fresh, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	require.NotEqual(t, old.SessionID, fresh.SessionID)
	rec, err := api.agentLoop.GetSessionLifecycleStore().Load(fresh.SessionID)
	require.NoError(t, err)
	assert.Equal(t, session.LifecycleQueued, rec.State, "Retry respects occupied capacity")
	_, err = api.agentLoop.StopSession(context.Background(), agent.StopRequest{SessionID: busy.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "owner"}})
	require.NoError(t, err)
	gatewayCrashAwait(t, p.resultCommitted)
	bytes, err := os.ReadFile(p.path)
	require.NoError(t, err)
	assert.Equal(t, "RETRY\n", string(bytes))
	after, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	require.Len(t, after, 2)
	for _, run := range after {
		if run.RunID == closed.RunID {
			assert.Equal(t, closed, run)
		}
	}
}

func TestCrashRecovery_RepeatingRetryStillUsesRunEndpoint(t *testing.T) {
	api, old, closed := crashTaskAfterBoot(t)
	w := patchTask(t, api, old.ID, `{"trigger":{"type":"recurring","config":{"rrule":"FREQ=DAILY","dtstart_ms":1792627200000,"tz":"UTC"}}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	w = patchTask(t, api, old.ID, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "crash Retry does not replace repeating-task run semantics")
	assert.Contains(t, w.Body.String(), "use POST /api/v1/tasks/"+old.ID+"/runs instead")
	runs, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{closed.RunID}, func() []string {
		ids := []string{}
		for _, r := range runs {
			ids = append(ids, r.RunID)
		}
		return ids
	}())
}

func TestCrashRecovery_StoppedPlanRetryCannotUseStandalonePath(t *testing.T) {
	api, old, closed := crashTaskAfterBoot(t)
	store := wirePlanStore(t, api)
	p := makeTestPlan(t, store, old.WorkspaceID, plan.StateDraft)
	w := patchTask(t, api, old.ID, fmt.Sprintf(`{"plan_id":%q}`, p.ID))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	drivePlanToFailed(t, api, p.ID, plan.FailedReasonStoppedByUser)
	w = patchTask(t, api, old.ID, `{"status":"in_progress"}`)
	assert.Equal(t, http.StatusConflict, w.Code, "a stale binding cannot bypass the stopped-plan dispatch gate")
	fresh, err := api.taskStore.Get(old.ID)
	require.NoError(t, err)
	assert.NotEqual(t, "in_progress", string(fresh.Status))
	runs, err := api.taskStore.ListRuns(old.ID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, closed.RunID, runs[0].RunID)
}
