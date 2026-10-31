package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 950–957, 1296; TDD 25, D-11.1.
// Two byte-identical trigger definitions, zero history, different LIVE engine
// registration state. This cannot pass by expanding RRULE or consulting runs.
func TestT10_OccurrencesHTTPProjectsLiveArmedTwinAndDearmedTwin(t *testing.T) {
	f := t10TaskEntry(t, "recurring")
	original, err := f.api.taskStore.Get(f.id)
	require.NoError(t, err)
	twin := createRecurringTaskViaAPI(t, f.api, original.WorkspaceID, "T10 dearmed twin", *original.Trigger.Config.Rrule, f.at, "UTC", "")
	require.Len(t, t10ReadTaskJobs(t, f.path, twin.Id), 1)
	// Remove through the scheduler's actual registration API; do not alter the
	// task, recurrence or history. This models live de-arming after sync failure.
	f.s.OnTaskDeleted(twin.Id)
	left, err := f.api.taskStore.Get(f.id)
	require.NoError(t, err)
	right, err := f.api.taskStore.Get(twin.Id)
	require.NoError(t, err)
	require.Equal(t, left.Trigger, right.Trigger)
	for _, id := range []string{f.id, twin.Id} {
		runs, err := f.api.taskStore.ListRuns(id)
		require.NoError(t, err)
		require.Empty(t, runs)
	}
	w := getOccurrences(t, f.api, url.Values{
		"workspace_id": {original.WorkspaceID}, "from_ms": {strconv.FormatInt(f.at-1000, 10)},
		"to_ms": {strconv.FormatInt(f.at+int64(48*time.Hour/time.Millisecond), 10)}, "tz": {"UTC"},
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var sets []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &sets))
	byID := map[string]map[string]any{}
	for _, set := range sets {
		id, ok := set["task_id"].(string)
		require.True(t, ok)
		byID[id] = set
	}
	require.Contains(t, byID, f.id)
	require.Contains(t, byID, twin.Id)
	assert.Equal(t, float64(f.at), byID[f.id]["armed_occurrence_ms"], "must come from live ArmedOccurrenceMS, not matching recurrence math")
	assert.Nil(t, byID[twin.Id]["armed_occurrence_ms"])
	assert.Equal(t, byID[f.id]["occurrences_ms"], byID[twin.Id]["occurrences_ms"], "de-arming does not rewrite the recurrence projection")
}

type t10HeldTaskProvider struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *t10HeldTaskProvider) GetDefaultModel() string { return "test-model" }
func (p *t10HeldTaskProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	p.once.Do(func() { close(p.entered) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return (&taskRunNowSuccessProvider{}).Chat(ctx, messages, tools, model, options)
	}
}

// Traces to: schedules-t10-spec-r6.md lines 1066–1076, 1273–1280; TDD 24, D-9.1–4.
// This is the BACKEND leg: a real dispatched worker is held, a real run record
// is open, and the exact full Calendar save payload goes through real REST.
func TestT10_RecurringSaveWithRealRunInFlightNamesActualBlocker(t *testing.T) {
	for _, ends := range []string{"After-N", "On-date", "Does-not-repeat"} {
		t.Run(ends, func(t *testing.T) {
			p := &t10HeldTaskProvider{entered: make(chan struct{}), release: make(chan struct{})}
			f := t10TaskEntry(t, "recurring", p)
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(p.release) }) })
			w := t10RunRequest(f, nil)
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			select {
			case <-p.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("BLOCKED: real worker never entered provider — no in-flight save claim can be made")
			}
			runs, err := f.api.taskStore.ListRuns(f.id)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			require.Nil(t, runs[0].EndedAt)
			tk, err := f.api.taskStore.Get(f.id)
			require.NoError(t, err)
			require.Equal(t, task.StatusInProgress, tk.Status)
			goalBefore, err := tools.GoalStoreForTasks(f.api.taskStore).GetByOwner(gen.GoalOwnerKindTask, f.id)
			require.NoError(t, err)
			require.NotNil(t, goalBefore)
			trigger := map[string]any{"type": "recurring", "config": map[string]any{"rrule": "FREQ=DAILY;COUNT=7", "dtstart_ms": f.at, "tz": "UTC"}}
			if ends == "On-date" {
				trigger["config"] = map[string]any{"rrule": "FREQ=DAILY;UNTIL=20291231T235959Z", "dtstart_ms": f.at, "tz": "UTC"}
			}
			if ends == "Does-not-repeat" {
				trigger = map[string]any{"type": "once", "config": map[string]any{"at_ms": f.at}}
			}
			body, err := json.Marshal(map[string]any{"title": tk.Title, "agent_id": tk.AgentID, "prompt": tk.Prompt, "criteria": tk.Criteria, "dod": goalBefore.DoD, "trigger": trigger, "run_isolated": tk.RunIsolated})
			require.NoError(t, err)
			response := httptest.NewRecorder()
			f.api.HandleTasks(response, httptest.NewRequest(http.MethodPatch, "/api/v1/tasks/"+f.id, strings.NewReader(string(body))))
			assert.NotRegexp(t, `(?i)stale.view|view.*(stale|out.of.date)|reload.*view`, response.Body.String(), "#1243 must not blame the view for an unchanged frozen definition")
			if response.Code == http.StatusOK {
				fresh, err := f.api.taskStore.Get(f.id)
				require.NoError(t, err)
				if ends == "After-N" {
					assert.Equal(t, "FREQ=DAILY;COUNT=7", *fresh.Trigger.Config.Rrule)
				} else if ends == "On-date" {
					assert.Contains(t, *fresh.Trigger.Config.Rrule, "UNTIL=20291231T235959Z")
				} else {
					assert.Equal(t, task.TriggerOnce, fresh.Trigger.Type)
					assert.Equal(t, &f.at, fresh.Trigger.Config.AtMs)
				}
				assert.Equal(t, tk.Criteria, fresh.Criteria, "mutable trigger does not edit frozen definition")
				goalAfter, err := tools.GoalStoreForTasks(f.api.taskStore).GetByOwner(gen.GoalOwnerKindTask, f.id)
				require.NoError(t, err)
				assert.Equal(t, goalBefore.DoD, goalAfter.DoD)
			} else {
				assert.Contains(t, []int{http.StatusConflict, http.StatusBadRequest}, response.Code)
				assert.Regexp(t, `(?i)(active|in.flight|running).*(run|definition|criteria)|(run|definition|criteria).*(active|in.flight|running)`, response.Body.String(), fmt.Sprintf("name the real blocker, not a generic failure: %s", response.Body.String()))
			}
			once.Do(func() { close(p.release) })
			t10AwaitCompletedRuns(t, f, 1)
		})
	}
}
