package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/plan"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r6 specifies refusal/truthful error, not a particular 4xx/5xx status.
// Assert the generated error envelope as well as the error status class;
// state/history/consumption and later automatic-fire oracles remain separate.
func t10AssertRunRefused(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	assert.GreaterOrEqual(t, w.Code, http.StatusBadRequest, w.Body.String())
	assert.Less(t, w.Code, 600, w.Body.String())
	var response gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.NotEmpty(t, strings.TrimSpace(response.Error), "refusal must carry a truthful error, not an empty success envelope")
}

// This adapter controls only the real reservation→admission edge. It never
// consumes an occurrence or replaces StartTaskNow/REST with a fake dispatcher.
func t10AdmissionHook(t *testing.T, f *t10TaskEntryFixture, hook func(context.Context, string) error) func() {
	t.Helper()
	edge, ok := any(f.api.taskExecutor).(interface {
		SetRunNowAdmissionHookForTest(func(context.Context, string) error)
	})
	if !ok {
		t.Fatal("BLOCKED: real run-now reservation/pre-admission hook absent — S-H.6/9/10, needs T8 backend; bind equivalent production edge")
	}
	edge.SetRunNowAdmissionHookForTest(hook)
	restore := func() { edge.SetRunNowAdmissionHookForTest(nil) }
	t.Cleanup(restore)
	return restore
}
func t10AssertOriginalAutoFire(t *testing.T, f *t10TaskEntryFixture) {
	t.Helper()
	f.clk.advance(time.UnixMilli(f.at).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	rows := t10AwaitCompletedRuns(t, f, 1)
	assert.Equal(t, task.RunKindScheduled, rows[0].Kind)
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	rows2, e := f.api.taskStore.ListRuns(f.id)
	require.NoError(t, e)
	assert.Len(t, rows2, 1)
}
func t10NoAdmissionEffects(t *testing.T, f *t10TaskEntryFixture, before []cron.CronJob) {
	t.Helper()
	t10AssertConsumptionMarker(t, f, nil)
	rows, e := f.api.taskStore.ListRuns(f.id)
	require.NoError(t, e)
	assert.Empty(t, rows)
	fresh, e := f.api.taskStore.Get(f.id)
	require.NoError(t, e)
	assert.Empty(t, fresh.SessionID)
	assert.Equal(t, before, t10ReadTaskJobs(t, f.path, f.id))
	b, e := os.ReadFile(filepath.Join(f.api.taskStore.Dir(), f.id+".json"))
	require.NoError(t, e)
	assert.NotRegexp(t, `(?i)reservation|pending_run_now|cleanup_journal`, string(b), "reservation is memory-only")
}
func t10RunSlotAndPlanGates(t *testing.T) {
	t.Run("open-run-slot", func(t *testing.T) {
		p := &t10HeldTaskProvider{entered: make(chan struct{}), release: make(chan struct{})}
		f := t10TaskEntry(t, "manual", p)
		var once sync.Once
		defer once.Do(func() { close(p.release) })
		// Start ordinary work, then attach a future arm while that real run is held.
		// The setup cannot itself consume the scheduled occurrence being tested.
		first := t10RunRequest(f, nil)
		require.Equal(t, http.StatusAccepted, first.Code, first.Body.String())
		select {
		case <-p.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("real first run never entered")
		}
		save := patchTask(t, f.api, f.id, fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, f.at))
		require.Equal(t, http.StatusOK, save.Code, save.Body.String())
		before := t10ReadTaskJobs(t, f.path, f.id)
		require.Len(t, before, 1)
		existing, e := f.api.taskStore.ListRuns(f.id)
		require.NoError(t, e)
		require.Len(t, existing, 1)
		w := t10RunRequest(f, nil)
		// D-6.7 explicitly preserves BOTH existing-session return and refusal;
		// it does not prescribe 409. Pin the identity/state, not invented prose/code.
		if w.Code == http.StatusAccepted {
			var response map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			assert.Equal(t, existing[0].SessionID, response["session_id"])
		} else {
			t10AssertRunRefused(t, w)
		}
		t10AssertConsumptionMarker(t, f, nil)
		assert.Equal(t, before, t10ReadTaskJobs(t, f.path, f.id))
		rows, e := f.api.taskStore.ListRuns(f.id)
		require.NoError(t, e)
		assert.Len(t, rows, 1)
		once.Do(func() { close(p.release) })
		t10AwaitCompletedRuns(t, f, 1)
	})
	t.Run("draft-plan", func(t *testing.T) {
		f := t10TaskEntry(t, "once")
		tk, e := f.api.taskStore.Get(f.id)
		require.NoError(t, e)
		ps := wirePlanStore(t, f.api)
		p := makeTestPlan(t, ps, tk.WorkspaceID, plan.StateDraft)
		_, e = f.api.taskStore.Update(f.id, task.Patch{PlanID: &p.ID})
		require.NoError(t, e)
		before := t10ReadTaskJobs(t, f.path, f.id)
		w := t10RunRequest(f, nil)
		t10AssertRunRefused(t, w)
		t10NoAdmissionEffects(t, f, before)
		// After detaching the draft member, the unchanged automatic registration can run.
		empty := ""
		_, e = f.api.taskStore.Update(f.id, task.Patch{PlanID: &empty})
		require.NoError(t, e)
		t10AssertOriginalAutoFire(t, f)
	})
}
func t10BeforeAdmissionFailure(t *testing.T, row string) {
	f := t10TaskEntry(t, "once")
	before := t10ReadTaskJobs(t, f.path, f.id)
	if strings.Contains(row, "marker-save") {
		original := gatewayTaskAtomicWriter
		require.NotNil(t, original)
		var mu sync.Mutex
		attempts := 0
		gatewayTaskAtomicWriter = func(path string, b []byte, perm os.FileMode) error {
			if path == filepath.Join(f.api.taskStore.Dir(), f.id+".json") {
				var v map[string]any
				if e := json.Unmarshal(b, &v); e != nil {
					return e
				}
				if v["consumed_occurrence_ms"] != nil {
					mu.Lock()
					attempts++
					mu.Unlock()
					return errors.New("T10 consumption marker filesystem failure")
				}
			}
			return original(path, b, perm)
		}
		defer func() { gatewayTaskAtomicWriter = original }()
		w := t10RunRequest(f, nil)
		t10AssertRunRefused(t, w)
		if w.Code == http.StatusAccepted {
			t10AwaitCompletedRuns(t, f, 1)
		}
		mu.Lock()
		assert.Equal(t, 1, attempts, "real admission must attempt marker commit before launching")
		mu.Unlock()
		t10NoAdmissionEffects(t, f, before)
		gatewayTaskAtomicWriter = original
		t10AssertOriginalAutoFire(t, f)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hit := make(chan struct{}, 1)
	restore := t10AdmissionHook(t, f, func(got context.Context, id string) error {
		if id != f.id {
			return nil
		}
		hit <- struct{}{}
		if strings.Contains(row, "cancelled") {
			cancel()
			return got.Err()
		}
		if strings.Contains(row, "shutdown") {
			return agent.ErrExecutorDraining
		}
		return errors.New("T10 pre-admission refusal")
	})
	w := httptest.NewRecorder()
	f.api.HandleTasks(w, httptest.NewRequest("POST", "/api/v1/tasks/"+f.id+"/runs", strings.NewReader(`{}`)).WithContext(ctx))
	require.Len(t, hit, 1, "no-op admission cannot pass")
	t10AssertRunRefused(t, w)
	t10NoAdmissionEffects(t, f, before)
	restore()
	if strings.Contains(row, "crash") {
		t10PhysicalTaskRestart(t, f)
	}
	t10AssertOriginalAutoFire(t, f)
}
func t10TriggerReservationEditRace(t *testing.T) {
	f := t10TaskEntry(t, "once")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	restore := t10AdmissionHook(t, f, func(_ context.Context, id string) error {
		if id == f.id {
			close(entered)
			<-release
		}
		return nil
	})
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- t10RunRequest(f, nil) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real reserved operation never reached admission")
	}
	next := f.at + 3600000
	edit := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		edit <- patchTask(t, f.api, f.id, fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, next))
	}()
	select {
	case w := <-edit:
		require.Equal(t, 200, w.Code, w.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch held task coordination lock across admission")
	}
	once.Do(func() { close(release) })
	w := <-result
	t10AssertRunRefused(t, w)
	restore()
	t10AssertConsumptionMarker(t, f, nil)
	rows, e := f.api.taskStore.ListRuns(f.id)
	require.NoError(t, e)
	assert.Empty(t, rows)
	jobs := t10ReadTaskJobs(t, f.path, f.id)
	require.Len(t, jobs, 1)
	assert.Equal(t, &next, jobs[0].State.NextRunAtMS)
	f.at = next
	t10AssertOriginalAutoFire(t, f)
}
func t10ActualCallerRevoked(t *testing.T) {
	f := t10TaskEntry(t, "once")
	tk, e := f.api.taskStore.Get(f.id)
	require.NoError(t, e)
	cfg := f.api.agentLoop.GetConfig()
	for _, id := range []string{"creator-A", "starter-C"} {
		ag := agent.NewAgentInstance(&config.AgentConfig{ID: id, Type: "custom"}, &cfg.Agents.Defaults, cfg, &restMockProvider{})
		f.api.agentLoop.GetRegistry().UpsertAgent(ag)
	}
	setWorkspaceCoreTeam(t, f.api, tk.WorkspaceID, []string{"mia", "creator-A", "starter-C"})
	edges := []workspace.DelegationEdge{{FromAgent: "creator-A", ToAgent: "mia"}, {FromAgent: "starter-C", ToAgent: "mia"}}
	require.NoError(t, workspace.SaveDelegation(f.api.homePath, tk.WorkspaceID, edges))
	// Fresh server-side attribution and stored initiator intentionally name A,
	// not actual starter C. A retains permission after C is revoked.
	path := filepath.Join(f.api.taskStore.Dir(), f.id+".json")
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	var stored map[string]any
	require.NoError(t, json.Unmarshal(b, &stored))
	stored["created_by_agent_id"] = "creator-A"
	stored["initiator"] = map[string]any{"agent_id": "creator-A"}
	b, e = json.Marshal(stored)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(path, b, 0600))
	before := t10ReadTaskJobs(t, f.path, f.id)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	restore := t10AdmissionHook(t, f, func(_ context.Context, id string) error {
		if id == f.id {
			close(entered)
			<-release
		}
		return nil
	})
	result := make(chan error, 1)
	go func() {
		_, err := f.api.taskExecutor.StartTaskNowAs(context.Background(), f.id, &task.Initiator{AgentID: "starter-C"})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real starter never reached queued admission")
	}
	require.NoError(t, workspace.SaveDelegation(f.api.homePath, tk.WorkspaceID, edges[:1]))
	once.Do(func() { close(release) })
	err := <-result
	require.Error(t, err)
	assert.Regexp(t, `(?i)delegation|permission|authori`, err.Error())
	restore()
	t10NoAdmissionEffects(t, f, before)
	t10AssertOriginalAutoFire(t, f)
}
func t10PersonProvenance(t *testing.T) {
	f := t10TaskEntry(t, "once")
	w := t10RunRequest(f, nil)
	require.Equal(t, 202, w.Code, w.Body.String())
	rows := t10AwaitCompletedRuns(t, f, 1)
	ls := f.api.agentLoop.GetSessionLifecycleStore()
	require.NotNil(t, ls)
	rec, e := ls.Load(rows[0].SessionID)
	require.NoError(t, e)
	assert.Nil(t, rec.InitiatedBy, "person must not be replaced by task creator or assignee")
	assert.Equal(t, task.RunKindManual, rows[0].Kind)
	assert.Equal(t, &f.at, rows[0].OccurrenceMs)
	t10AssertConsumptionMarker(t, f, &f.at)
}
func t10E0Queued(t *testing.T, explicit bool) {
	f := t10TaskEntry(t, "recurring")
	// The final T10 constructor injects this engine. Refuse to exercise a fake
	// parallel engine on the pre-T10 path-first constructor.
	ctor := reflect.TypeOf(agent.NewTaskTriggerScheduler)
	require.Equal(t, 3, ctor.NumIn(), "bind actual shared-engine constructor")
	require.Equal(t, 1, ctor.NumOut())
	require.Equal(t, reflect.TypeOf((*agent.TaskTriggerScheduler)(nil)), ctor.Out(0))
	if ctor.In(0) != reflect.TypeOf((*cron.CronService)(nil)) {
		t.Fatal("BLOCKED: needs T8 backend and T10 shared engine constructor for real E0 collected-queued fixture")
	}
	cs := cron.NewCronService(f.path)
	f.s.Stop()
	f.s.WaitForLane()
	f.s = reflect.ValueOf(agent.NewTaskTriggerScheduler).Call([]reflect.Value{reflect.ValueOf(cs), reflect.ValueOf(f.api.taskStore), reflect.ValueOf(f.api.taskExecutor)})[0].Interface().(*agent.TaskTriggerScheduler)
	f.s.SetClock(f.clk)
	require.NoError(t, f.s.Start())
	require.NoError(t, f.s.Reconcile())
	f.api.agentLoop.SetTaskTriggerScheduler(f.s)
	t.Cleanup(func() { f.s.Stop(); f.s.WaitForLane() })
	edge, ok := any(cs).(interface {
		SetBeforeDispatchTestHook(func(context.Context, string))
	})
	if !ok {
		t.Fatal("BLOCKED: needs T8 real queued-dispatch process-edge hook — 18.34/S-H.15")
	}
	jobs := t10ReadTaskJobs(t, f.path, f.id)
	require.Len(t, jobs, 1)
	original := jobs[0].ID
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer func() { once.Do(func() { close(release) }); cs.WaitForLane() }()
	edge.SetBeforeDispatchTestHook(func(_ context.Context, id string) {
		if id == original {
			close(entered)
			<-release
		}
	})
	defer edge.SetBeforeDispatchTestHook(nil)
	f.clk.advance(time.UnixMilli(f.at).Sub(f.clk.Now()))
	cs.RunDueJobs(f.clk.Now())
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("real automatic collection never entered queued state")
	}
	assert.True(t, cs.JobDispatchInFlight(original))
	rows, e := f.api.taskStore.ListRuns(f.id)
	require.NoError(t, e)
	require.Empty(t, rows)
	var at *int64
	if explicit {
		at = &f.at
	}
	w := t10RunRequest(f, at)
	t10AssertRunRefused(t, w)
	t10AssertConsumptionMarker(t, f, nil)
	once.Do(func() { close(release) })
	cs.WaitForLane()
	rows = t10AwaitCompletedRuns(t, f, 1)
	assert.Equal(t, task.RunKindScheduled, rows[0].Kind)
	assert.False(t, cs.JobDispatchInFlight(original))
	assert.Equal(t, &f.at, rows[0].OccurrenceMs)
}
func t10AdmissionRow(t *testing.T, name string) {
	switch name {
	case "D6-6-trigger-edit-race":
		t10TriggerReservationEditRace(t)
	case "D6-7-run-slot-and-plan-gates":
		t10RunSlotAndPlanGates(t)
	case "D6-11-actual-starter-revoked":
		t10ActualCallerRevoked(t)
	case "D6-12-person-provenance":
		t10PersonProvenance(t)
	case "D6-16-E0-explicit":
		t10E0Queued(t, true)
	case "D6-17-E0-task-level":
		t10E0Queued(t, false)
	default:
		t10BeforeAdmissionFailure(t, name)
	}
}
