//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Traces to: schedules-t10-spec-r6.md lines 608–613; TDD 6, FR-25.
func TestT10_TaskReconcilePreservesForeignKindsByteForByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cron", "jobs.json")
	at := time.Now().Add(time.Hour).UnixMilli()
	foreign := []cron.CronJob{
		{ID: "heartbeat-with-history", Name: "heartbeat:default:mia", AgentID: "mia", Enabled: true, Payload: cron.CronPayload{Kind: "heartbeat", Message: "heartbeat body"}, Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}, State: cron.CronJobState{NextRunAtMS: &at, LastStatus: "ok", LastError: "historical sentinel"}},
		{ID: "loop-future", Name: "loop:session", AgentID: "mia", Enabled: true, Payload: cron.CronPayload{Kind: "loop", Message: "loop-session"}, Schedule: cron.CronSchedule{Kind: "at", AtMS: &at}, State: cron.CronJobState{NextRunAtMS: &at}},
	}
	all := append(append([]cron.CronJob{}, foreign...), cron.CronJob{ID: "orphan-task", Payload: cron.CronPayload{Kind: "task", Message: "deleted-task"}})
	data, err := json.Marshal(cron.CronStore{Version: 1, Jobs: all})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, data, 0600))
	cs := cron.NewCronService(path)
	ctor := reflect.ValueOf(NewTaskTriggerScheduler)
	first := reflect.ValueOf(path)
	if ctor.Type().In(0) == reflect.TypeOf(cs) {
		first = reflect.ValueOf(cs)
	}
	store := task.New(filepath.Join(t.TempDir(), "tasks"))
	sched := ctor.Call([]reflect.Value{first, reflect.ValueOf(store), reflect.Zero(reflect.TypeOf((*TaskExecutor)(nil)))})[0].Interface().(*TaskTriggerScheduler)
	require.NoError(t, sched.Reconcile())
	got := sched.cs.ListJobs(true)
	require.Len(t, got, 2, "only the orphan task job may be pruned")
	assert.ElementsMatch(t, foreign, got, "kind ownership includes every foreign field, not just retained IDs")
	persisted, err := os.ReadFile(path)
	require.NoError(t, err)
	var disk cron.CronStore
	require.NoError(t, json.Unmarshal(persisted, &disk))
	assert.ElementsMatch(t, foreign, disk.Jobs)
}

// Traces to: schedules-t10-spec-r6.md lines 761–778; TDD 12, D-1.2/6.
// Injected dispatch is the existing asynchronous-launch boundary, NOT a fake
// scheduler. This pins handler progression; real run-history storage is a
// separate inherited T8 obligation below.
func TestT10_TaskOccurrenceFailureUsesNextBoundaryNotBackoff(t *testing.T) {
	for _, cause := range []error{errors.New("launch refused A"), errors.New("launch refused B")} {
		t.Run(cause.Error(), func(t *testing.T) {
			s, store, clk, _ := newTriggerSchedulerForTest(t)
			calls := 0
			s.dispatch = func(context.Context, string, *int64) error { calls++; return cause }
			// Exact on-second recurrence fixture, like r6's scheduled instants;
			// this control tests next-boundary/no-backoff, not fractional DTSTART rounding.
			at := clk.Now().Truncate(time.Second).Add(time.Hour).UnixMilli()
			rule, tz := "FREQ=DAILY;COUNT=3", "UTC"
			v := &task.Task{ID: "failure-next-boundary", Title: "failed dispatch", AgentID: "mia", WorkspaceID: "ws-test", Status: task.StatusInbox, Trigger: &task.Trigger{Type: task.TriggerRecurring, Config: task.TriggerConfig{Rrule: &rule, DtstartMs: &at, Tz: &tz}}}
			require.NoError(t, store.Create(v))
			s.OnTaskUpserted(v)
			clk.Advance(time.Hour)
			s.RunDueJobs(clk.Now())
			s.WaitForLane()
			require.Equal(t, 1, calls)
			jobs := s.cs.ListJobs(true)
			require.Len(t, jobs, 1)
			require.NotNil(t, jobs[0].State.NextRunAtMS)
			assert.Equal(t, time.UnixMilli(at).Add(24*time.Hour).UnixMilli(), *jobs[0].State.NextRunAtMS)
			assert.Zero(t, jobs[0].State.RetryAttempt, "failed occurrence must not create a heartbeat backoff attempt")
		})
	}
}

// Traces to: schedules-t10-spec-r6.md lines 617, 624, 1312; TDD 18, FR-23.
// Needs T8 backend. Every row now drives production objects and has real
// state/dispatch/persistence assertions; missing process-edge hooks fail loudly.
func TestT10_InheritedT8OwnershipAndWaiting(t *testing.T) {
	for _, tc := range []struct{ id, oracle string }{
		{"12b-18.11-18.14", "tool-created future once/recurring stays inbox; no early turn/session; manual control drains"},
		{"12c-18.15-18.18-18.23", "foreign activation/status refused without a write; authorized manual clear removes registration; local assignee control fires"},
		{"12d-18.19-18.20-18.24", "every automatic promotion including delete-driven is guarded on fresh locked state; released work progresses"},
		{"12d-18.27-18.29", "transitive scheduled chains do not stall/expire or fabricate activity; independent genuine stall still supervises"},
		{"12d-18.31-18.32", "due/queued pending delivery remains expected waiting across plan passes and expiry, exactly one release"},
		{"12d-18.34", "atomic O1-to-O2 handoff for one and batch; exactly-once and balanced pending counters after success/cancel/panic/RunNow"},
		{"12d-18.36", "title edit and two sweep ticks keep queued registration; exhausted final occurrence kept before schedule computation"},
		{"12d-18.37", "overdue-unconsumed kept in process; physical boot skips undelivered once; started-unfinished delivered once replays once per 6b exception"},
		{"12e-18.21-18.22", "REST/tool writes serialize before notification; scheduler failure de-arms real live jobs and reports schedule_not_armed"},
		{"12e-18.25-18.26", "deferred completion/sweep validate fresh persisted trigger before replacing anything"},
		{"12f-18.30-18.33", "delivery marker scheduled instant, internal only, atomically invalidated on edit, preserved on identical save"},
		{"12f-18.35-18.38", "stale callback cannot write delivery or rearm after clear-and-identical-reattach; each authority conjunct has independent control"},
		{"12f-18.39", "real boot load-first aborts on damaged file byte-identically before reconcile/activate"},
		{"12f-18.40", "same scheduler across real config reload; overdue/queued/save-failure/rearm/damaged-disk variants preserve original attempt"},
	} {
		t.Run(tc.id, func(t *testing.T) { t10InheritedT8Row(t, tc.id) })
	}
}
