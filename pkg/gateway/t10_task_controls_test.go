package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func t10AssertConsumptionMarker(t *testing.T, f *t10TaskEntryFixture, want *int64) {
	t.Helper()
	fresh, err := f.api.taskStore.Get(f.id)
	require.NoError(t, err)
	data, err := json.Marshal(fresh)
	require.NoError(t, err)
	var value map[string]any
	require.NoError(t, json.Unmarshal(data, &value))
	if want == nil {
		assert.Nil(t, value["consumed_occurrence_ms"])
	} else {
		assert.Equal(t, float64(*want), value["consumed_occurrence_ms"], "marker must name scheduled instant, not wall time")
	}
}

// Traces to: schedules-t10-spec-r6.md S-H.3/S-H.16, D-6.1/D-11.9.
// Real status REST, ordinary queue drain and actual held worker. No fake successful start.
func TestT10_ManualStatusRoutesAndOrdinaryDrainRemainReachable(t *testing.T) {
	for _, kind := range []string{"manual", "none"} {
		for _, status := range []string{"next", "in_progress"} {
			t.Run(kind+"-"+status, func(t *testing.T) {
				p := &t10HeldTaskProvider{entered: make(chan struct{}), release: make(chan struct{})}
				f := t10TaskEntry(t, kind, p)
				var once sync.Once
				defer once.Do(func() { close(p.release) })
				w := patchTask(t, f.api, f.id, fmt.Sprintf(`{"status":%q}`, status))
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				if status == "next" {
					stored, err := f.api.taskStore.Get(f.id)
					require.NoError(t, err)
					assert.Equal(t, task.StatusNext, stored.Status, "ordinary pre-dispatch write is preserved")
					rows, err := f.api.taskStore.ListRuns(f.id)
					require.NoError(t, err)
					assert.Empty(t, rows)
					f.api.taskExecutor.CheckQueuedTasks(context.Background())
				}
				select {
				case <-p.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("ordinary status route/drain never dispatched its real worker")
				}
				stored, err := f.api.taskStore.Get(f.id)
				require.NoError(t, err)
				assert.Equal(t, task.StatusInProgress, stored.Status, "status was durable before provider turn began")
				rows, err := f.api.taskStore.ListRuns(f.id)
				require.NoError(t, err)
				require.Len(t, rows, 1)
				assert.Nil(t, rows[0].EndedAt)
				assert.NotEmpty(t, rows[0].SessionID)
				assert.Empty(t, t10ReadTaskJobs(t, f.path, f.id))
				t10AssertConsumptionMarker(t, f, nil)
				once.Do(func() { close(p.release) })
				t10AwaitCompletedRuns(t, f, 1)
			})
		}
	}
}

// Traces to: schedules-t10-spec-r6.md S-J.3/D-9.2–4, paired with held-run test.
func TestT10_RecurringSaveNoRunBackendMatrix(t *testing.T) {
	for _, ends := range []string{"After-N", "On-date", "Does-not-repeat"} {
		t.Run(ends, func(t *testing.T) {
			f := t10TaskEntry(t, "recurring")
			tk, err := f.api.taskStore.Get(f.id)
			require.NoError(t, err)
			goal, err := tools.GoalStoreForTasks(f.api.taskStore).GetByOwner(gen.GoalOwnerKindTask, f.id)
			require.NoError(t, err)
			trigger := map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=DAILY;COUNT=7", "dtstart_ms": f.at, "tz": "UTC"}}
			if ends == "On-date" {
				trigger["config"] = map[string]any{"rrule": "FREQ=DAILY;UNTIL=20291231T235959Z", "dtstart_ms": f.at, "tz": "UTC"}
			}
			if ends == "Does-not-repeat" {
				trigger = map[string]any{"type": "once", "config": map[string]any{"at_ms": f.at}}
			}
			body, err := json.Marshal(map[string]any{"title": tk.Title, "agent_id": tk.AgentID, "prompt": tk.Prompt, "criteria": tk.Criteria, "dod": goal.DoD, "trigger": trigger, "run_isolated": tk.RunIsolated})
			require.NoError(t, err)
			w := httptest.NewRecorder()
			f.api.HandleTasks(w, httptest.NewRequest("PATCH", "/api/v1/tasks/"+f.id, strings.NewReader(string(body))))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			fresh, err := f.api.taskStore.Get(f.id)
			require.NoError(t, err)
			actual, err := json.Marshal(fresh.Trigger)
			require.NoError(t, err)
			expected, err := json.Marshal(trigger)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(actual))
			assert.Equal(t, tk.Status, fresh.Status)
			assert.Equal(t, tk.Prompt, fresh.Prompt)
			assert.Equal(t, tk.Criteria, fresh.Criteria)
			after, err := tools.GoalStoreForTasks(f.api.taskStore).GetByOwner(gen.GoalOwnerKindTask, f.id)
			require.NoError(t, err)
			assert.Equal(t, goal.DoD, after.DoD)
			runs, err := f.api.taskStore.ListRuns(f.id)
			require.NoError(t, err)
			assert.Empty(t, runs)
			jobs := t10ReadTaskJobs(t, f.path, f.id)
			require.Len(t, jobs, 1)
			assert.Equal(t, &f.at, jobs[0].State.NextRunAtMS)
		})
	}
}

// Traces to: schedules-t10-spec-r6.md S-H.1/2/11, D-6.2/3, F-16.
// The tool callback is the REAL executor. A refusing spy alone did not prove
// consumption, marker persistence, run history or suppression of the old fire.
func TestT10_RunTaskToolConsumesRealArmedOccurrence(t *testing.T) {
	for _, variant := range []string{"once", "recurring", "recurring-done"} {
		t.Run(variant, func(t *testing.T) {
			kind := strings.TrimSuffix(variant, "-done")
			f := t10TaskEntry(t, kind)
			wantRuns := 1
			if variant == "recurring-done" {
				meta, err := f.api.agentLoop.GetSessionStore().NewScheduledSession("mia")
				require.NoError(t, err)
				previous := f.at - int64(24*time.Hour/time.Millisecond)
				r, created, err := f.api.taskStore.OpenRun(f.id, &previous, task.RunKindScheduled, meta.ID, nil)
				require.NoError(t, err)
				require.True(t, created)
				require.NoError(t, f.api.taskStore.CloseRun(f.id, r.RunID, task.StatusDone, "prior scheduled result"))
				done := task.StatusDone
				_, err = f.api.taskStore.Update(f.id, task.Patch{Status: &done})
				require.NoError(t, err)
				wantRuns = 2
			}
			tool := tools.NewTaskRunTool(f.api.taskStore)
			tool.SetDelegationDenyChecker(func(context.Context, string, string) *tools.DelegationDenial { return nil })
			tool.SetStartTaskNow(f.api.taskExecutor.StartTaskNow)
			result := tool.Execute(context.Background(), map[string]any{"task_id": f.id})
			require.False(t, result.IsError, result.ForLLM)
			// S-H.11 / S-H.2: the oracle is the run run_task just started. A prior
			// occurrence's saved result (seeded for recurring-done) is not that
			// run and must not be required to carry its success text.
			rows := t10AwaitEndedRuns(t, f, wantRuns)
			manual := []task.TaskRun{}
			for _, row := range rows {
				if row.Kind == task.RunKindManual {
					manual = append(manual, row)
				}
			}
			require.Len(t, manual, 1, "run_task starts exactly one new run")
			assert.Equal(t, task.StatusDone, manual[0].Status)
			assert.Contains(t, manual[0].Result, taskRunNowSuccessSummary, "the run started by run_task carries the occurrence outcome")
			assert.Equal(t, &f.at, manual[0].OccurrenceMs)
			assert.NotEmpty(t, manual[0].SessionID)
			if variant == "recurring-done" {
				var prior []task.TaskRun
				for _, row := range rows {
					if row.Kind == task.RunKindScheduled {
						prior = append(prior, row)
					}
				}
				require.Len(t, prior, 1)
				assert.Equal(t, task.StatusDone, prior[0].Status)
				assert.Equal(t, "prior scheduled result", prior[0].Result, "the earlier occurrence's result is left as it was saved")
			}
			t10AssertConsumptionMarker(t, f, &f.at)
			f.clk.advance(time.UnixMilli(f.at).Sub(f.clk.Now()))
			f.s.RunDueJobs(f.clk.Now())
			f.s.WaitForLane()
			wait, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.True(t, f.api.agentLoop.WaitForActiveRequestsContext(wait))
			runs, err := f.api.taskStore.ListRuns(f.id)
			require.NoError(t, err)
			assert.Len(t, runs, wantRuns, "tool consumes the armed occurrence, never ordinary dispatch plus duplicate automatic fire")
		})
	}
}

// t10AwaitEndedRuns waits until want runs have all ended. It does not require
// every historical run to contain the new run's success text: S-H.11's oracle
// is the run run_task just started (S-H.2), not a prior occurrence's result.
func t10AwaitEndedRuns(t *testing.T, f *t10TaskEntryFixture, want int) []task.TaskRun {
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
	}, 8*time.Second, time.Millisecond, "run_task's occurrence must settle in task run history")
	return rows
}
