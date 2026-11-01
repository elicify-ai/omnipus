package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type t10TaskEntryFixture struct {
	api      *restAPI
	s        *agent.TaskTriggerScheduler
	clk      *t10GatewayClock
	id, path string
	at       int64
}

func t10TaskEntry(t *testing.T, kind string, override ...providers.LLMProvider) *t10TaskEntryFixture {
	t.Helper()
	var provider providers.LLMProvider = &taskRunNowSuccessProvider{}
	if len(override) > 0 {
		provider = override[0]
	}
	api := newTestRestAPIAlignedStoresWithProvider(t, provider, judgeAgentForTaskRunTests(t))
	clk := &t10GatewayClock{at: time.Now().Add(24 * time.Hour).Truncate(time.Second)}
	at := clk.Now().Add(24 * time.Hour).UnixMilli()
	path := filepath.Join(api.homePath, "cron", "jobs.json")
	cs := cron.NewCronService(path)
	ctor := reflect.ValueOf(agent.NewTaskTriggerScheduler)
	require.Equal(t, 3, ctor.Type().NumIn(), "temporary constructor binding changed; retain real-entry assertions")
	require.Equal(t, 1, ctor.Type().NumOut())
	require.Equal(t, reflect.TypeOf((*agent.TaskTriggerScheduler)(nil)), ctor.Type().Out(0))
	first := reflect.ValueOf(path)
	if ctor.Type().In(0) == reflect.TypeOf(cs) {
		first = reflect.ValueOf(cs)
	}
	args := []reflect.Value{first, reflect.ValueOf(api.taskStore), reflect.ValueOf(api.taskExecutor)}
	for i, arg := range args {
		require.True(t, arg.Type().AssignableTo(ctor.Type().In(i)), "bind real scheduler argument %d, not a fake implementation", i)
	}
	scheduler := ctor.Call(args)[0].Interface().(*agent.TaskTriggerScheduler)
	scheduler.SetClock(clk)
	require.NoError(t, scheduler.Start())
	api.agentLoop.SetTaskTriggerScheduler(scheduler)
	t.Cleanup(func() { scheduler.Stop(); scheduler.WaitForLane() })
	ws := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, ws, []string{"mia"})
	trigger := `{"type":"manual","config":{}}`
	if kind == "once" {
		trigger = fmt.Sprintf(`{"type":"once","config":{"at_ms":%d}}`, at)
	}
	if kind == "recurring" {
		trigger = fmt.Sprintf(`{"type":"recurring","config":{"rrule":"FREQ=DAILY;COUNT=3","dtstart_ms":%d,"tz":"UTC"}}`, at)
	}
	body := fmt.Sprintf(`{"title":"T10 real entry","prompt":"Produce the complete T10 report.","workspace_id":%q,"agent_id":"mia","action":"llm","trigger":%s,`+singleAttemptJSON+`,`+minimalCriteriaDodJSON+`}`, ws, trigger)
	if kind == "none" {
		body = strings.Replace(body, `"trigger":`+trigger+`,`, "", 1)
	}
	w := httptest.NewRecorder()
	api.HandleTasks(w, httptest.NewRequest("POST", "/api/v1/tasks", strings.NewReader(body)))
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	id, ok := created["id"].(string)
	require.True(t, ok)
	bindMetVerdictJudge(t, api, func() []string {
		g, e := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, id)
		if e != nil || g == nil {
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
	if kind == "once" || kind == "recurring" {
		jobs := t10ReadTaskJobs(t, path, id)
		require.Len(t, jobs, 1, "real creation notification must arm exactly one registration")
		require.NotNil(t, jobs[0].State.NextRunAtMS)
		require.Equal(t, at, *jobs[0].State.NextRunAtMS)
	}
	return &t10TaskEntryFixture{api, scheduler, clk, id, path, at}
}
func t10ReadTaskJobs(t *testing.T, path, id string) []cron.CronJob {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	var disk cron.CronStore
	require.NoError(t, json.Unmarshal(data, &disk))
	out := []cron.CronJob{}
	for _, j := range disk.Jobs {
		if j.Payload.Message == id {
			// Discover actual registration independently of the producer-kind
			// delta so preserve-behavior tests can characterize the base. The
			// final-kind assertion lives in TestT10_RealTaskCreatingRegistrationKind
			// (and the all-producers test), not as an unrelated helper failure.
			out = append(out, j)
		}
	}
	return out
}

// Traces to: schedules-t10-spec-r6.md lines 717–722, 1208; TDD 5, D-4.4.
// Actual create-notification persistence complements the structural producer
// check; first-write-only observation remains a separate binding gap.
func TestT10_RealTaskCreatingRegistrationKind(t *testing.T) {
	for _, kind := range []string{"once", "recurring"} {
		t.Run(kind, func(t *testing.T) {
			f := t10TaskEntry(t, kind)
			jobs := t10ReadTaskJobs(t, f.path, f.id)
			require.Len(t, jobs, 1)
			assert.Equal(t, "task", jobs[0].Payload.Kind, "the real creating producer must persist the final task kind")
			assert.Equal(t, f.id, jobs[0].Payload.Message)
		})
	}
}

func t10RunRequest(f *t10TaskEntryFixture, occurrence *int64) *httptest.ResponseRecorder {
	body := `{}`
	if occurrence != nil {
		body = fmt.Sprintf(`{"occurrence_ms":%d}`, *occurrence)
	}
	w := httptest.NewRecorder()
	f.api.HandleTasks(w, httptest.NewRequest("POST", "/api/v1/tasks/"+f.id+"/runs", strings.NewReader(body)))
	return w
}
func t10AwaitCompletedRuns(t *testing.T, f *t10TaskEntryFixture, want int) []task.TaskRun {
	t.Helper()
	var rows []task.TaskRun
	require.Eventually(t, func() bool {
		var err error
		rows, err = f.api.taskStore.ListRuns(f.id)
		if err != nil || len(rows) != want {
			return false
		}
		for _, r := range rows {
			if r.EndedAt == nil {
				return false
			}
		}
		return true
	}, 8*time.Second, time.Millisecond, "real task attempt must settle in task run history")
	for _, r := range rows {
		assert.Equal(t, task.StatusDone, r.Status)
		assert.Contains(t, r.Result, taskRunNowSuccessSummary)
		assert.NotEmpty(t, r.SessionID)
	}
	return rows
}

// Traces to: schedules-t10-spec-r6.md lines 875–899, 923–927; TDD 21, D-6.1/2/3/8/14.
func TestT10_RESTRunNowConsumesArmedRegistration(t *testing.T) {
	for _, kind := range []string{"once", "recurring", "manual"} {
		for _, explicit := range []bool{false, true} {
			if kind != "recurring" && explicit {
				continue
			}
			t.Run(fmt.Sprintf("%s-explicit=%t", kind, explicit), func(t *testing.T) {
				f := t10TaskEntry(t, kind)
				var instant *int64
				if explicit {
					instant = &f.at
				}
				w := t10RunRequest(f, instant)
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				rows := t10AwaitCompletedRuns(t, f, 1)
				fresh, err := f.api.taskStore.Get(f.id)
				require.NoError(t, err)
				encoded, err := json.Marshal(fresh)
				require.NoError(t, err)
				var disk map[string]any
				require.NoError(t, json.Unmarshal(encoded, &disk))
				if kind == "manual" {
					assert.NotContains(t, disk, "consumed_occurrence_ms")
					return
				}
				assert.Equal(t, float64(f.at), disk["consumed_occurrence_ms"], "commit marker is the consumed scheduled instant, never execution wall clock")
				assert.Equal(t, &f.at, rows[0].OccurrenceMs, "run outcome/session comes from the consumed occurrence's real run record")
				jobs := t10ReadTaskJobs(t, f.path, f.id)
				if kind == "once" {
					for _, job := range jobs {
						assert.Nil(t, job.State.NextRunAtMS, "consumed once must not still be armed")
					}
				} else {
					require.Len(t, jobs, 1)
					require.NotNil(t, jobs[0].State.NextRunAtMS)
					assert.Equal(t, f.at+int64(24*time.Hour/time.Millisecond), *jobs[0].State.NextRunAtMS)
				}
				f.clk.advance(24 * time.Hour)
				f.s.RunDueJobs(f.clk.Now())
				f.s.WaitForLane()
				drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				assert.True(t, f.api.agentLoop.WaitForActiveRequestsContext(drainCtx), "admitted work must settle")
				after, err := f.api.taskStore.ListRuns(f.id)
				require.NoError(t, err)
				assert.Len(t, after, 1, "original scheduled instant must not execute the consumed occurrence again")
			})
		}
	}
}

// Traces to: schedules-t10-spec-r6.md lines 985–990, 959–965; TDD 21, D-11.9/D-6.15.
func TestT10_ScheduledTaskRejectsBothStatusActivationRoutesBeforeWrite(t *testing.T) {
	for _, kind := range []string{"once", "recurring"} {
		for _, status := range []string{"next", "in_progress"} {
			t.Run(kind+"-"+status, func(t *testing.T) {
				f := t10TaskEntry(t, kind)
				path := filepath.Join(f.api.taskStore.Dir(), f.id+".json")
				before, err := os.ReadFile(path)
				require.NoError(t, err)
				originalJobs := t10ReadTaskJobs(t, f.path, f.id)
				w := patchTask(t, f.api, f.id, fmt.Sprintf(`{"status":%q}`, status))
				assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				assert.Regexp(t, `(?i)run[ -]?now|/runs`, w.Body.String(), "truthful guidance to unified run-now route")
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, before, after, "refuse BEFORE any task write, including status writes later reverted")
				assert.Equal(t, originalJobs, t10ReadTaskJobs(t, f.path, f.id), "armed registration remains identical")
				rows, err := f.api.taskStore.ListRuns(f.id)
				require.NoError(t, err)
				assert.Empty(t, rows)
			})
		}
	}
}

// Traces to: schedules-t10-spec-r6.md lines 895–914, 923–927; TDD 21, D-6.4/5/9/10.
func TestT10_RunNowExplicitEligibilityAndDearmedRegistration(t *testing.T) {
	for _, variant := range []string{"past-historical-rerun", "other-future-refused", "dearmed-future-refused", "past-once-ordinary-control"} {
		t.Run(variant, func(t *testing.T) {
			kind := "recurring"
			if variant == "past-once-ordinary-control" {
				kind = "once"
			}
			f := t10TaskEntry(t, kind)
			var occurrence *int64
			if variant == "past-historical-rerun" {
				past := time.Now().Add(-time.Hour).UnixMilli()
				occurrence = &past
			}
			if variant == "other-future-refused" {
				other := f.at + 60000
				occurrence = &other
			}
			if variant == "dearmed-future-refused" {
				f.s.OnTaskDeleted(f.id)
				require.Empty(t, t10ReadTaskJobs(t, f.path, f.id))
			}
			if variant == "past-once-ordinary-control" {
				past := time.Now().Add(-time.Hour).UnixMilli()
				w := patchTask(t, f.api, f.id, fmt.Sprintf(`{"trigger":{"type":"once","config":{"at_ms":%d}}}`, past))
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				for _, job := range t10ReadTaskJobs(t, f.path, f.id) {
					require.Nil(t, job.State.NextRunAtMS, "a dormant job is not an armed occurrence")
				}
			}
			before := t10ReadTaskJobs(t, f.path, f.id)
			w := t10RunRequest(f, occurrence)
			if strings.Contains(variant, "refused") {
				t10AssertRunRefused(t, w)
				if w.Code == http.StatusAccepted {
					t10AwaitCompletedRuns(t, f, 1)
				}
				rows, err := f.api.taskStore.ListRuns(f.id)
				require.NoError(t, err)
				assert.Empty(t, rows, "refusal must not dispatch work")
			} else {
				require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
				rows := t10AwaitCompletedRuns(t, f, 1)
				assert.Equal(t, occurrence, rows[0].OccurrenceMs, "ordinary dispatch preserves explicit past context and no-occurrence control")
				assert.Equal(t, task.RunKindManual, rows[0].Kind)
			}
			fresh, err := f.api.taskStore.Get(f.id)
			require.NoError(t, err)
			data, err := json.Marshal(fresh)
			require.NoError(t, err)
			var stored map[string]any
			require.NoError(t, json.Unmarshal(data, &stored))
			assert.Nil(t, stored["consumed_occurrence_ms"], "historical/refused entries must not consume an armed occurrence")
			assert.Equal(t, before, t10ReadTaskJobs(t, f.path, f.id), "historical/refused entry leaves current registration unchanged")
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 929–941, 976–983; TDD 21, D-6.6/7/11/12/13/16/17.
// Missing exact seams are loud, not simulated successful admission/authority.
func TestT10_RunNowAdmissionAndT8Dependencies(t *testing.T) {
	for _, row := range []struct{ name, oracle string }{
		{"D6-6-trigger-edit-race", "reservation under task lock, dispatch outside; fresh registration identity, no orphan/double consume"},
		{"D6-7-run-slot-and-plan-gates", "real open-run slot and requirePlanExecuting unchanged; auto-deny context preserved"},
		{"D6-11-actual-starter-revoked", "C distinct from creator and assignee revoked while queued; no durable marker; original automatic fire survives"},
		{"D6-12-person-provenance", "human REST run-now uses manual provenance and real auto-deny audit"},
		{"D6-13-admission-refused", "no marker or durable reservation; original occurrence fires once"},
		{"D6-13-cancelled", "no marker or durable reservation; original occurrence fires once"},
		{"D6-13-shutdown", "no marker or durable reservation; original occurrence fires once"},
		{"D6-13-crash-before-admission", "restart arms original; no marker, suppressed boundary, cleanup journal or orphan"},
		{"D6-13-marker-save-fails", "failed marker commit consumes nothing; original occurrence intact"},
		{"D6-16-E0-explicit", "automatic delivery collected and held queued, O2 true and no run; explicit occurrence refused before E2; automatic delivers once"},
		{"D6-17-E0-task-level", "same O2 queued state refuses no-occurrence run-now before E4; settled-past and no-armed ordinary controls still dispatch"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t10AdmissionRow(t, row.name)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 916–921; TDD 21, D-3.4/D-6.3.
func TestT10_RunNowMarkerProgressionExactInstants(t *testing.T) {
	for _, variant := range []string{"physical-restart", "reconcile", "identical-save", "genuine-trigger-edit", "final-COUNT"} {
		t.Run(variant, func(t *testing.T) {
			t10MarkerProgression(t, variant)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 936–941; TDD 21, D-6.13.
func TestT10_CancelledCallerPreAdmissionBinding(t *testing.T) {
	f := t10TaskEntry(t, "once")
	before := t10ReadTaskJobs(t, f.path, f.id)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	f.api.HandleTasks(w, httptest.NewRequest("POST", "/api/v1/tasks/"+f.id+"/runs", strings.NewReader(`{}`)).WithContext(ctx))
	t10AssertRunRefused(t, w)
	if w.Code == http.StatusAccepted {
		t10AwaitCompletedRuns(t, f, 1)
	}
	rows, err := f.api.taskStore.ListRuns(f.id)
	require.NoError(t, err)
	require.Empty(t, rows, "cancelled caller must not open a run before admission")
	assert.Equal(t, before, t10ReadTaskJobs(t, f.path, f.id))
	t10AssertConsumptionMarker(t, f, nil)
	f.clk.advance(time.UnixMilli(f.at).Sub(f.clk.Now()))
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	automatic := t10AwaitCompletedRuns(t, f, 1)
	assert.Equal(t, task.RunKindScheduled, automatic[0].Kind, "positive control: original automatic occurrence survives")
	f.s.RunDueJobs(f.clk.Now())
	f.s.WaitForLane()
	rows, err = f.api.taskStore.ListRuns(f.id)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
}
