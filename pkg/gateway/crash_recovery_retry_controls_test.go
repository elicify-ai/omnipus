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
	beforeSessions, err := api.agentLoop.GetSessionStore().ListSessions()
	require.NoError(t, err)
	beforeIDs := map[string]bool{}
	for _, s := range beforeSessions {
		beforeIDs[s.ID] = true
	}
	p := &r2RetryProvider{crashTaskProvider: newCrashTaskProvider()}
	p.retry = true
	work, err := workspace.EnsureWorkDir(api.homePath, old.WorkspaceID)
	require.NoError(t, err)
	p.path = filepath.Join(work, "concurrent-retry.txt")
	inst, ok := api.agentLoop.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	inst.Provider = p
	inst.Tools.RegisterReplacing(tools.NewAppendFileTool(work, true))
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow, "goal_claim": config.ToolPolicyAllow}})
	t.Cleanup(func() { p.open(); api.taskExecutor.Drain(10 * time.Second) })
	p.resultCommitted = make(chan struct{}, 8)
	trace := &r2ExecutionTrace{requests: map[string]int{}, effects: map[string]int{}}
	require.NoError(t, api.agentLoop.MountHook(agent.NamedHook("r2-per-execution-retry-observer", trace)))
	observed := &r2ObservedLauncher{SessionLauncher: agent.NewSteerLauncher(api.agentLoop), entered: make(chan steer.LaunchResult, 1), release: make(chan struct{})}
	openLaunch := sync.OnceFunc(func() { close(observed.release) })
	t.Cleanup(openLaunch)
	api.taskExecutor.SetSessionLauncher(observed)
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
	launched := gatewayCrashAwait(t, observed.entered)
	require.NotEmpty(t, launched.SessionID, "instrument: winner crossed the REAL durable launcher under its reservation")
	otherResponse := gatewayCrashAwait(t, responses)
	assert.Equal(t, http.StatusOK, otherResponse.Code, "the other real PATCH returned while winner was held: %s", otherResponse.Body.String())
	openLaunch()
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
	// CHECK F1 / DESIGN 4: OpenRun can coalesce different sessions. Enumerate
	// the actual durable sessions and lifecycle execution claims independently.
	afterSessions, err := api.agentLoop.GetSessionStore().ListSessions()
	require.NoError(t, err)
	var freshIDs []string
	for _, s := range afterSessions {
		if !beforeIDs[s.ID] {
			freshIDs = append(freshIDs, s.ID)
		}
	}
	assert.Equal(t, []string{fresh.SessionID}, freshIDs, "exactly ONE fresh session, including workers refused after creation")
	records, err := api.agentLoop.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	require.NoError(t, err)
	var taskClaims []session.LifecycleRecord
	for _, rec := range records {
		if !beforeIDs[rec.SessionID] && rec.Origin != nil && rec.Origin.TaskID == old.ID {
			taskClaims = append(taskClaims, rec)
		}
	}
	assert.Len(t, taskClaims, 1, "one fresh task-session lifecycle, not a second distinct failed or queued worker")
	for _, rec := range taskClaims {
		assert.Equal(t, fresh.SessionID, rec.SessionID)
		require.NotNil(t, rec.ExecutionID, "fresh task owns a real admission execution claim")
		assert.NotEmpty(t, rec.ExecutionID.RunID)
		for _, run := range runs {
			if run.RunID != closed.RunID {
				assert.Equal(t, rec.SessionID, run.SessionID, "new run and execution name the SAME unique session")
			}
		}
		t.Logf("RETRY_EXECUTION session=%s generation=%d execution=%+v", rec.SessionID, rec.Generation, rec.ExecutionID)
	}
	requests, effects := trace.snapshot("")
	assert.Len(t, requests, 1, "provider requests are observed per real session/turn, never a global scripted step")
	assert.Len(t, effects, 1, "successful effects are observed per real session/turn")
	for key, count := range requests {
		assert.Contains(t, key, fresh.SessionID)
		assert.Equal(t, 2, count, "one tool request and its settled-result follow-up in the same execution")
		assert.Equal(t, 1, effects[key], "one actual effect for the unique admitted execution")
	}
	t.Logf("RETRY_IDENTITIES fresh=%v requests=%v effects=%v", freshIDs, requests, effects)
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
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"append_file": config.ToolPolicyAllow, "goal_claim": config.ToolPolicyAllow}})
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
